package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

const (
	annotationEndpoints = "actfb.io/endpoints"
	annotationNode      = "actfb.io/node"
	annotationGen       = "actfb.io/generation"
)

// Driver is the real Kubernetes CONTAINER adapter.
type Driver struct{}

var (
	_ runtime.Driver          = (*Driver)(nil)
	_ runtime.OptionValidator = (*Driver)(nil)
	_ runtime.StaticMetadata  = (*Driver)(nil)
	_ runtime.Probe           = (*Driver)(nil)
)

// New constructs the Kubernetes driver. Unsupported resources are reported as
// capability errors, never silently skipped.
func New() runtime.Driver { return &Driver{} }

// Name returns "kubernetes".
func (d *Driver) Name() string { return deployment.DriverKubernetes }

// Kind returns CONTAINER.
func (d *Driver) Kind() deployment.Kind { return deployment.KindContainer }

// StaticCapabilities describes the adapter without contacting a cluster.
func (d *Driver) StaticCapabilities() deployment.Capabilities {
	return deployment.Capabilities{
		Kind:                deployment.KindContainer,
		SourceTypes:         []deployment.SourceType{deployment.SourceOCIImage},
		CloudInit:           false,
		FileInjection:       true,
		PublicExposure:      true,
		SnapshotReset:       false,
		PersistentVolumes:   false,
		MaxNodes:            deployment.MaxNodes,
		DriverOptionsSchema: json.RawMessage(DriverOptionsSchema),
	}
}

// Capabilities validates settings and returns the effective capability set.
func (d *Driver) Capabilities(ctx context.Context, target runtime.Target) (deployment.Capabilities, error) {
	if _, err := decodeSettings(target); err != nil {
		return deployment.Capabilities{}, &runtime.CapabilityError{Driver: d.Name(), Capability: "settings", Detail: err.Error()}
	}
	client, err := NewClient(target)
	if err != nil {
		return deployment.Capabilities{}, err
	}
	settings, _ := decodeSettings(target)
	if err := client.Ping(ctx, settings.Namespace); err != nil {
		return deployment.Capabilities{}, err
	}
	return d.StaticCapabilities(), nil
}

// Validate is non-mutating.
func (d *Driver) Validate(_ context.Context, target runtime.Target, node deployment.Node) []deployment.Finding {
	findings := make([]deployment.Finding, 0)
	if node.Kind != deployment.KindContainer {
		return append(findings, runtime.ErrorFinding("kind", "DRIVER_KIND_MISMATCH", "kubernetes driver requires a CONTAINER node"))
	}
	settings, err := decodeSettings(target)
	if err != nil {
		findings = append(findings, runtime.ErrorFinding("integration.settings", "DRIVER_SETTINGS_INVALID", err.Error()))
	} else if !settings.PodPIDsLimitVerified {
		findings = append(findings, runtime.ErrorFinding("integration.settings", "PID_LIMIT_NOT_VERIFIED", "the operator must confirm a kubelet PodPidsLimit before untrusted containers are allowed"))
	}
	if _, err := ParseWorkloadOptions(node.DriverOptions[d.Name()]); err != nil {
		findings = append(findings, runtime.ErrorFinding("driverOptions."+d.Name(), "DRIVER_OPTION_INVALID", err.Error()))
	}
	if node.Source.Type != deployment.SourceOCIImage {
		findings = append(findings, runtime.ErrorFinding("source.type", "SOURCE_TYPE_UNSUPPORTED", "container nodes require OCI_IMAGE"))
	}
	if err := runtime.ValidateOCIDigest(node.Source.Ref); err != nil {
		findings = append(findings, runtime.ErrorFinding("source.ref", "SOURCE_NOT_DIGEST_PINNED", err.Error()))
	}
	if node.Bootstrap != nil {
		findings = append(findings, runtime.ErrorFinding("bootstrap", "CLOUD_INIT_UNSUPPORTED", "cloud-init is VM-only"))
	}
	findings = append(findings, runtime.ValidateResources(node.Kind, node.Resources)...)
	findings = append(findings, runtime.ValidateEndpoints(node.Kind, node.Endpoints)...)
	for index, endpoint := range node.Endpoints {
		if endpoint.Exposure != deployment.ExposurePublic {
			continue
		}
		field := fmt.Sprintf("endpoints[%d]", index)
		if strings.TrimSpace(target.Publication.PublicTCPHost) == "" {
			findings = append(findings, runtime.ErrorFinding(field, "PUBLICATION_NOT_CONFIGURED", "PUBLIC endpoints require an integration publication host"))
		}
		if target.Publication.PublicPortFrom < 30000 || target.Publication.PublicPortTo > 32767 {
			findings = append(findings, runtime.ErrorFinding(field, "NODEPORT_RANGE_REQUIRED", "the publication range must be inside the Kubernetes NodePort range 30000-32767"))
		}
	}
	return findings
}

// Plan is a non-mutating reconciliation hint.
func (d *Driver) Plan(_ context.Context, _ runtime.Target, desired deployment.Node, current *runtime.ProviderResource) (runtime.Plan, error) {
	plan := runtime.Plan{NodeKey: desired.Key}
	if current == nil || current.ProviderID == "" {
		plan.Action = "CREATE"
		return plan, nil
	}
	if current.State == runtime.NodeStateRunning {
		plan.Action = "NONE"
	} else {
		plan.Action = "RECREATE"
	}
	return plan, nil
}

// Apply reconciles exactly one generation. A retry adopts the pod with the
// exact labels; stale owned generations are deleted first, and a future
// generation aborts the operation.
func (d *Driver) Apply(ctx context.Context, target runtime.Target, desired deployment.Node, op runtime.Operation) (runtime.ProviderResource, error) {
	if op.Kind == runtime.OperationDestroy {
		return runtime.ProviderResource{}, errors.New("kubernetes: DESTROY must use Destroy")
	}
	if findings := d.Validate(ctx, target, desired); hasErrors(findings) {
		return runtime.ProviderResource{}, &runtime.CapabilityError{Driver: d.Name(), Capability: "validation", Detail: findings[0].Code}
	}
	settings, err := decodeSettings(target)
	if err != nil {
		return runtime.ProviderResource{}, &runtime.CapabilityError{Driver: d.Name(), Capability: "settings", Detail: err.Error()}
	}
	workload, err := ParseWorkloadOptions(desired.DriverOptions[d.Name()])
	if err != nil {
		return runtime.ProviderResource{}, &runtime.CapabilityError{Driver: d.Name(), Capability: "driverOptions", Detail: err.Error()}
	}
	envelope, err := runtime.DecodeTargetSettings(target.Settings)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	client, err := NewClient(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	if err := client.Ping(ctx, settings.Namespace); err != nil {
		return runtime.ProviderResource{}, err
	}
	labels := runtime.Labels(d.Name(), op)
	name := runtime.SanitizeName("actfb", op.InstanceID.String(), op.NodeKey, "g"+strconv.FormatInt(op.Generation, 10))
	annotations := map[string]string{
		annotationNode:      op.NodeKey,
		annotationGen:       strconv.FormatInt(op.Generation, 10),
		annotationEndpoints: endpointAnnotation(desired.Endpoints),
	}

	if err := d.cleanStalePods(ctx, client, settings.Namespace, op); err != nil {
		return runtime.ProviderResource{}, err
	}
	if err := d.ensureSecret(ctx, client, settings.Namespace, name, envelope.Material, labels); err != nil {
		return runtime.ProviderResource{}, err
	}
	pod, err := d.ensurePod(ctx, client, settings, name, labels, annotations, target, desired, workload, envelope.Material)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	if !runtime.Owns(pod.Metadata.Labels, op.InstanceID, op.NodeKey) {
		return runtime.ProviderResource{}, fmt.Errorf("%w: pod labels do not match the requested owner", runtime.ErrOwnershipConflict)
	}
	if err := d.waitReady(ctx, client, settings.Namespace, name); err != nil {
		return runtime.ProviderResource{}, err
	}
	endpoints, err := d.ensureEndpoints(ctx, client, settings.Namespace, name, labels, desired, envelope.Allocation, target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	if err := d.ensureNetworkPolicy(ctx, client, settings.Namespace, name, labels, desired); err != nil {
		return runtime.ProviderResource{}, err
	}
	return runtime.ProviderResource{ProviderID: name, State: runtime.NodeStateRunning, Endpoints: endpoints, Message: "pod ready"}, nil
}

func (d *Driver) ensureSecret(ctx context.Context, client *Client, namespace, name string, material *runtime.Material, labels map[string]string) error {
	data := map[string]string{}
	if material != nil {
		names := make([]string, 0, len(material.Env))
		for envName := range material.Env {
			names = append(names, envName)
		}
		sort.Strings(names)
		for _, envName := range names {
			data["env."+envName] = material.Env[envName]
		}
		for index, file := range material.Files {
			data["file."+strconv.Itoa(index)] = string(file.Content)
		}
	}
	if len(data) == 0 {
		return nil
	}
	payload := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name": name + "-input", "namespace": namespace, "labels": labels,
		},
		"type":       "Opaque",
		"stringData": data,
	}
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/secrets"
	if err := client.Do(ctx, http.MethodPost, path, payload, nil); err != nil {
		if !IsConflict(err) {
			return err
		}
		var existing struct {
			Metadata ObjectMeta `json:"metadata"`
		}
		if err := client.Do(ctx, http.MethodGet, path+"/"+url.PathEscape(name+"-input"), nil, &existing); err != nil {
			return err
		}
		if err := CheckOwnership(existing.Metadata.Labels, ownerUUID(labels[runtime.LabelInstance]), labels[runtime.LabelNode]); err != nil {
			return err
		}
		// The secret content is derived from the immutable version and the
		// per-instance generated values; on reset a new generation name is used,
		// so an existing same-generation secret is adopted unchanged.
	}
	return nil
}

func (d *Driver) ensurePod(ctx context.Context, client *Client, settings Settings, name string, labels, annotations map[string]string, target runtime.Target, desired deployment.Node, workload WorkloadOptions, material *runtime.Material) (struct {
	Metadata ObjectMeta `json:"metadata"`
	Status   struct {
		Phase string `json:"phase"`
	} `json:"status"`
}, error) {
	var existing struct {
		Metadata ObjectMeta `json:"metadata"`
		Status   struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}
	namespace := settings.Namespace
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(name)
	err := client.Do(ctx, http.MethodGet, path, nil, &existing)
	if err == nil {
		return existing, nil
	}
	if !IsNotFound(err) {
		return existing, err
	}
	pod, err := buildPod(namespace, name, labels, annotations, target, settings, desired, workload, material)
	if err != nil {
		return existing, err
	}
	if err := client.Do(ctx, http.MethodPost, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/pods", pod, &existing); err != nil {
		if !IsConflict(err) {
			return existing, err
		}
		if err := client.Do(ctx, http.MethodGet, path, nil, &existing); err != nil {
			return existing, err
		}
	}
	return existing, nil
}

func buildPod(namespace, name string, labels, annotations map[string]string, target runtime.Target, settings Settings, desired deployment.Node, workload WorkloadOptions, material *runtime.Material) (map[string]any, error) {
	readOnly := true
	if workload.ReadOnlyRootFS != nil {
		readOnly = *workload.ReadOnlyRootFS
	}
	env := make([]any, 0)
	volumeMounts := make([]any, 0)
	volumes := make([]any, 0)
	if material != nil {
		names := make([]string, 0, len(material.Env))
		for envName := range material.Env {
			names = append(names, envName)
		}
		sort.Strings(names)
		for _, envName := range names {
			if !validEnvName(envName) {
				return nil, &runtime.CapabilityError{Driver: deployment.DriverKubernetes, Capability: "env", Detail: "invalid environment variable name"}
			}
			env = append(env, map[string]any{
				"name": envName,
				"valueFrom": map[string]any{
					"secretKeyRef": map[string]any{"name": name + "-input", "key": "env." + envName},
				},
			})
		}
		if len(material.Files) > 0 {
			items := make([]any, 0, len(material.Files))
			for index, file := range material.Files {
				if !strings.HasPrefix(file.Target, "/") || strings.Contains(file.Target, "..") {
					return nil, &runtime.CapabilityError{Driver: deployment.DriverKubernetes, Capability: "files", Detail: "injection path must be absolute without traversal"}
				}
				key := "file." + strconv.Itoa(index)
				items = append(items, map[string]any{"key": key, "path": key})
				volumeMounts = append(volumeMounts, map[string]any{
					"name": "input", "mountPath": file.Target, "subPath": key, "readOnly": true,
				})
			}
			volumes = append(volumes, map[string]any{
				"name":   "input",
				"secret": map[string]any{"secretName": name + "-input", "defaultMode": 0o444, "items": items},
			})
		}
	}
	limits := map[string]string{
		"cpu":               fmt.Sprintf("%dm", desired.Resources.CPUMillicores),
		"memory":            fmt.Sprintf("%dMi", desired.Resources.MemoryMiB),
		"ephemeral-storage": fmt.Sprintf("%dMi", desired.Resources.DiskMiB),
	}
	runAsNonRoot := true
	securityContext := map[string]any{
		"allowPrivilegeEscalation": false,
		"readOnlyRootFilesystem":   readOnly,
		"capabilities":             map[string]any{"drop": []string{"ALL"}},
		"seccompProfile":           map[string]any{"type": "RuntimeDefault"},
	}
	if workload.RunAsUser != nil {
		if *workload.RunAsUser == 0 {
			return nil, &runtime.CapabilityError{Driver: deployment.DriverKubernetes, Capability: "runAsUser", Detail: "root is not allowed"}
		}
		securityContext["runAsUser"] = *workload.RunAsUser
		runAsNonRoot = true
	}
	if workload.RunAsGroup != nil {
		securityContext["runAsGroup"] = *workload.RunAsGroup
	}
	tmpfsNames := make([]string, 0, len(workload.Tmpfs))
	for mountPath := range workload.Tmpfs {
		tmpfsNames = append(tmpfsNames, mountPath)
	}
	sort.Strings(tmpfsNames)
	for index, mountPath := range tmpfsNames {
		volumeName := "scratch-" + strconv.Itoa(index)
		volumes = append(volumes, map[string]any{
			"name": volumeName,
			"emptyDir": map[string]any{
				"medium":    "Memory",
				"sizeLimit": fmt.Sprintf("%dMi", workload.Tmpfs[mountPath]),
			},
		})
		volumeMounts = append(volumeMounts, map[string]any{"name": volumeName, "mountPath": mountPath})
	}
	if readOnly {
		// A read-only root needs a bounded scratch area; the template may not
		// have declared one.
		hasTmp := false
		for _, mountPath := range tmpfsNames {
			if mountPath == "/tmp" {
				hasTmp = true
				break
			}
		}
		if !hasTmp {
			volumes = append(volumes, map[string]any{
				"name": "scratch-tmp",
				"emptyDir": map[string]any{
					"medium":    "Memory",
					"sizeLimit": fmt.Sprintf("%dMi", desired.Resources.DiskMiB),
				},
			})
			volumeMounts = append(volumeMounts, map[string]any{"name": "scratch-tmp", "mountPath": "/tmp"})
		}
	}

	container := map[string]any{
		"name":            "node",
		"image":           desired.Source.Ref,
		"imagePullPolicy": "IfNotPresent",
		"resources": map[string]any{
			"requests": limits,
			"limits":   limits,
		},
		"securityContext": securityContext,
		"env":             env,
		"volumeMounts":    volumeMounts,
	}
	if len(workload.Command) > 0 {
		container["command"] = workload.Command
	}
	if len(workload.Args) > 0 {
		container["args"] = workload.Args
	}
	if workload.WorkingDir != "" {
		container["workingDir"] = workload.WorkingDir
	}

	podSpec := map[string]any{
		"restartPolicy":                "Always",
		"automountServiceAccountToken": false,
		"hostNetwork":                  false,
		"hostPID":                      false,
		"hostIPC":                      false,
		"enableServiceLinks":           false,
		"securityContext":              map[string]any{"runAsNonRoot": runAsNonRoot, "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
		"containers":                   []any{container},
		"volumes":                      volumes,
	}
	if workload.ServiceAccount != "" {
		podSpec["serviceAccountName"] = workload.ServiceAccount
	}
	if secret := strings.TrimSpace(settings.ImagePullSecret); secret != "" {
		podSpec["imagePullSecrets"] = []any{map[string]any{"name": secret}}
	}
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name": name, "namespace": namespace, "labels": labels, "annotations": annotations,
		},
		"spec": podSpec,
	}, nil
}

func (d *Driver) waitReady(ctx context.Context, client *Client, namespace, name string) error {
	deadline := time.Now().Add(60 * time.Second)
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(name)
	for {
		var pod struct {
			Status struct {
				Phase      string `json:"phase"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		}
		if err := client.Do(ctx, http.MethodGet, path, nil, &pod); err != nil {
			return err
		}
		if pod.Status.Phase == "Running" {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == "Ready" && condition.Status == "True" {
					return nil
				}
			}
			// A pod without a readiness condition is running as far as the API
			// reports; no container readiness probe is configured by default.
			if len(pod.Status.Conditions) == 0 {
				return nil
			}
		}
		if pod.Status.Phase == "Failed" {
			return fmt.Errorf("%w: pod entered Failed phase", runtime.ErrProviderUnavailable)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: pod did not become ready", runtime.ErrProviderUnavailable)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (d *Driver) ensureEndpoints(ctx context.Context, client *Client, namespace, podName string, labels map[string]string, desired deployment.Node, allocation *runtime.Allocation, target runtime.Target) ([]runtime.ProviderEndpoint, error) {
	var pod struct {
		Status struct {
			PodIP string `json:"podIP"`
		} `json:"status"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/pods/"+url.PathEscape(podName), nil, &pod); err != nil {
		return nil, err
	}
	endpoints := make([]runtime.ProviderEndpoint, 0, len(desired.Endpoints))
	for _, endpoint := range desired.Endpoints {
		provider := runtime.ProviderEndpoint{Key: endpoint.Key, Type: endpoint.Protocol, Host: pod.Status.PodIP, Port: endpoint.TargetPort, Exposure: endpoint.Exposure}
		if endpoint.Exposure == deployment.ExposurePublic {
			if allocation == nil {
				return nil, &runtime.CapabilityError{Driver: d.Name(), Capability: "publication", Detail: "core port allocation is missing"}
			}
			binding, ok := allocation.Ports[endpoint.Key]
			if !ok || binding.Port < 30000 || binding.Port > 32767 {
				return nil, &runtime.CapabilityError{Driver: d.Name(), Capability: "publication", Detail: "allocated NodePort is missing or out of range"}
			}
			if err := d.ensureService(ctx, client, namespace, podName, labels, endpoint, binding.Port); err != nil {
				return nil, err
			}
			provider.Host = binding.Host
			provider.Port = binding.Port
			if endpoint.Protocol == "http" || endpoint.Protocol == "https" {
				raw := fmt.Sprintf("%s://%s", endpoint.Protocol, net.JoinHostPort(binding.Host, strconv.Itoa(binding.Port)))
				provider.URL = &raw
			}
		}
		endpoints = append(endpoints, provider)
	}
	return endpoints, nil
}

func (d *Driver) ensureService(ctx context.Context, client *Client, namespace, podName string, labels map[string]string, endpoint deployment.Endpoint, nodePort int) error {
	name := runtime.SanitizeName(podName, endpoint.Key)
	payload := map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": name, "namespace": namespace, "labels": labels},
		"spec": map[string]any{
			"type":     "NodePort",
			"selector": map[string]string{annotationNode + "-selector": labels[runtime.LabelNode]},
			"ports": []any{map[string]any{
				"name":       runtime.SanitizeName(endpoint.Key),
				"protocol":   "TCP",
				"port":       endpoint.TargetPort,
				"targetPort": endpoint.TargetPort,
				"nodePort":   nodePort,
			}},
		},
	}
	// Selectors cannot use the annotation key; use the instance label so only
	// pods of this instance are selected. The per-node pod label is also set.
	payload["spec"].(map[string]any)["selector"] = map[string]string{
		runtime.LabelInstance: labels[runtime.LabelInstance],
		runtime.LabelNode:     labels[runtime.LabelNode],
	}
	if err := client.Do(ctx, http.MethodPost, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/services", payload, nil); err != nil {
		if !IsConflict(err) {
			return err
		}
		var existing struct {
			Metadata ObjectMeta `json:"metadata"`
		}
		if err := client.Do(ctx, http.MethodGet, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/services/"+url.PathEscape(name), nil, &existing); err != nil {
			return err
		}
		return CheckOwnership(existing.Metadata.Labels, ownerUUID(labels[runtime.LabelInstance]), labels[runtime.LabelNode])
	}
	return nil
}

func (d *Driver) ensureNetworkPolicy(ctx context.Context, client *Client, namespace, podName string, labels map[string]string, desired deployment.Node) error {
	name := runtime.SanitizeName(podName, "netpol")
	ingress := make([]any, 0)
	publicPorts := make([]any, 0)
	for _, endpoint := range desired.Endpoints {
		if endpoint.Exposure == deployment.ExposurePublic {
			publicPorts = append(publicPorts, map[string]any{"protocol": "TCP", "port": endpoint.TargetPort})
		}
	}
	if len(publicPorts) > 0 {
		// Only the published ports accept external traffic; egress stays denied.
		ingress = append(ingress, map[string]any{
			"from":  []any{map[string]any{"ipBlock": map[string]any{"cidr": "0.0.0.0/0"}}, map[string]any{"ipBlock": map[string]any{"cidr": "::/0"}}},
			"ports": publicPorts,
		})
	}
	// Same-instance pods may talk to each other (manager/target), but no other
	// namespace or instance can.
	ingress = append(ingress, map[string]any{
		"from": []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]string{runtime.LabelInstance: labels[runtime.LabelInstance]}}}},
	})
	payload := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   map[string]any{"name": name, "namespace": namespace, "labels": labels},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]string{runtime.LabelInstance: labels[runtime.LabelInstance], runtime.LabelNode: labels[runtime.LabelNode]}},
			"policyTypes": []string{"Ingress", "Egress"},
			"ingress":     ingress,
			"egress":      []any{},
		},
	}
	path := "/apis/networking.k8s.io/v1/namespaces/" + url.PathEscape(namespace) + "/networkpolicies"
	if err := client.Do(ctx, http.MethodPost, path, payload, nil); err != nil {
		if !IsConflict(err) {
			return err
		}
		var existing struct {
			Metadata ObjectMeta `json:"metadata"`
		}
		if err := client.Do(ctx, http.MethodGet, path+"/"+url.PathEscape(name), nil, &existing); err != nil {
			return err
		}
		return CheckOwnership(existing.Metadata.Labels, ownerUUID(labels[runtime.LabelInstance]), labels[runtime.LabelNode])
	}
	return nil
}

func (d *Driver) cleanStalePods(ctx context.Context, client *Client, namespace string, op runtime.Operation) error {
	selector := url.QueryEscape(runtime.LabelInstance + "=" + op.InstanceID.String() + "," + runtime.LabelNode + "=" + op.NodeKey)
	var list struct {
		Items []struct {
			Metadata ObjectMeta `json:"metadata"`
		} `json:"items"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/pods?labelSelector="+selector, nil, &list); err != nil {
		return err
	}
	for _, item := range list.Items {
		if !runtime.Owns(item.Metadata.Labels, op.InstanceID, op.NodeKey) {
			return fmt.Errorf("%w: foreign pod %s", runtime.ErrOwnershipConflict, item.Metadata.Name)
		}
		generation := runtime.Generation(item.Metadata.Labels)
		if generation > op.Generation {
			return fmt.Errorf("%w: future pod generation %d", runtime.ErrStaleGeneration, generation)
		}
		if generation == op.Generation {
			continue
		}
		if err := d.deleteOwned(ctx, client, namespace, item.Metadata.Name, op); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) deleteOwned(ctx context.Context, client *Client, namespace, name string, op runtime.Operation) error {
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(name)
	if err := client.Do(ctx, http.MethodDelete, path+"?gracePeriodSeconds=5&propagationPolicy=Background", nil, nil); err != nil && !IsNotFound(err) {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := client.Do(ctx, http.MethodGet, path, nil, nil)
		if IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: pod deletion not confirmed", runtime.ErrProviderUnavailable)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Inspect returns provider state by pod name.
func (d *Driver) Inspect(ctx context.Context, target runtime.Target, providerID string) (runtime.ProviderResource, error) {
	settings, err := decodeSettings(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	client, err := NewClient(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	var pod struct {
		Metadata ObjectMeta `json:"metadata"`
		Status   struct {
			Phase string `json:"phase"`
			PodIP string `json:"podIP"`
		} `json:"status"`
	}
	path := "/api/v1/namespaces/" + url.PathEscape(settings.Namespace) + "/pods/" + url.PathEscape(providerID)
	if err := client.Do(ctx, http.MethodGet, path, nil, &pod); err != nil {
		if IsNotFound(err) {
			return runtime.ProviderResource{ProviderID: providerID, State: runtime.NodeStateDestroyed}, nil
		}
		return runtime.ProviderResource{}, err
	}
	resource := runtime.ProviderResource{ProviderID: providerID, State: runtime.NodeStateUnknown}
	if pod.Status.Phase == "Running" {
		resource.State = runtime.NodeStateRunning
	} else if pod.Status.Phase == "Failed" || pod.Status.Phase == "Succeeded" {
		resource.State = runtime.NodeStateError
	}
	var endpoints []runtime.ProviderEndpoint
	if raw := pod.Metadata.Annotations[annotationEndpoints]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &endpoints)
	}
	resource.Endpoints = endpoints
	return resource, nil
}

// Destroy deletes the pod and waits until the API confirms its removal. Ports
// are released by the orchestrator only after this returns nil.
func (d *Driver) Destroy(ctx context.Context, target runtime.Target, providerID string, op runtime.Operation) error {
	settings, err := decodeSettings(target)
	if err != nil {
		return err
	}
	client, err := NewClient(target)
	if err != nil {
		return err
	}
	if strings.TrimSpace(providerID) == "" {
		return nil
	}
	var pod struct {
		Metadata ObjectMeta `json:"metadata"`
	}
	path := "/api/v1/namespaces/" + url.PathEscape(settings.Namespace) + "/pods/" + url.PathEscape(providerID)
	if err := client.Do(ctx, http.MethodGet, path, nil, &pod); err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	if !runtime.Owns(pod.Metadata.Labels, op.InstanceID, op.NodeKey) {
		return fmt.Errorf("%w: refusing to delete a foreign pod", runtime.ErrOwnershipConflict)
	}
	if err := d.deleteOwned(ctx, client, settings.Namespace, providerID, op); err != nil {
		return err
	}
	for _, resource := range []struct{ path, name string }{
		{"/api/v1/namespaces/" + url.PathEscape(settings.Namespace) + "/secrets/", providerID + "-input"},
	} {
		_ = client.Do(ctx, http.MethodDelete, resource.path+url.PathEscape(resource.name), nil, nil)
	}
	var services struct {
		Items []struct {
			Metadata ObjectMeta `json:"metadata"`
		} `json:"items"`
	}
	selector := url.QueryEscape(runtime.LabelInstance + "=" + op.InstanceID.String() + "," + runtime.LabelNode + "=" + op.NodeKey)
	_ = client.Do(ctx, http.MethodGet, "/api/v1/namespaces/"+url.PathEscape(settings.Namespace)+"/services?labelSelector="+selector, nil, &services)
	for _, service := range services.Items {
		_ = client.Do(ctx, http.MethodDelete, "/api/v1/namespaces/"+url.PathEscape(settings.Namespace)+"/services/"+url.PathEscape(service.Metadata.Name), nil, nil)
	}
	_ = client.Do(ctx, http.MethodDelete, "/apis/networking.k8s.io/v1/namespaces/"+url.PathEscape(settings.Namespace)+"/networkpolicies/"+url.PathEscape(runtime.SanitizeName(providerID, "netpol")), nil, nil)
	return nil
}

// Probe performs a read-only API check.
func (d *Driver) Probe(ctx context.Context, target runtime.Target) error {
	settings, err := decodeSettings(target)
	if err != nil {
		return err
	}
	client, err := NewClient(target)
	if err != nil {
		return err
	}
	return client.Ping(ctx, settings.Namespace)
}

// ValidateSettings implements runtime.SettingsValidator.
func (d *Driver) ValidateSettings(raw json.RawMessage) []deployment.Finding {
	if _, err := ParseSettings(raw); err != nil {
		return []deployment.Finding{runtime.ErrorFinding("", "DRIVER_SETTINGS_INVALID", err.Error())}
	}
	return nil
}

// ValidateOptions implements the closed template option schema.
func (d *Driver) ValidateOptions(raw json.RawMessage) []deployment.Finding {
	if _, err := ParseWorkloadOptions(raw); err != nil {
		return []deployment.Finding{runtime.ErrorFinding("", "DRIVER_OPTION_INVALID", err.Error())}
	}
	return nil
}

func ownerUUID(raw string) uuid.UUID {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}

func endpointAnnotation(endpoints []deployment.Endpoint) string {
	projected := make([]runtime.ProviderEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		projected = append(projected, runtime.ProviderEndpoint{Key: endpoint.Key, Type: endpoint.Protocol, Port: endpoint.TargetPort, Exposure: endpoint.Exposure})
	}
	raw, _ := json.Marshal(projected)
	return string(raw)
}

func hasErrors(findings []deployment.Finding) bool {
	for _, finding := range findings {
		if finding.Severity == deployment.SeverityError {
			return true
		}
	}
	return false
}

func validEnvName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for index, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && index > 0:
		default:
			return false
		}
	}
	return true
}
