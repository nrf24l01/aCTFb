// Package kubevirt implements the real KubeVirt REST adapter. It creates a
// VirtualMachine (CDI data volume cloned from an administrator-approved source
// PVC), a per-VM egress-deny NetworkPolicy and, for published endpoints, a
// NodePort Service. Native names are never accepted from a template author.
package kubevirt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
	k8s "github.com/nrf24l01/actfb/internal/runtime/kubernetes"
)

const (
	DriverOptionsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "namespace": {"type": "string", "maxLength": 63},
    "storageClass": {"type": "string", "maxLength": 63},
    "podPidsLimitVerified": {"type": "boolean"},
    "vmImageMap": {"type": "object", "additionalProperties": {"type": "string"}},
    "imagePullSecret": {"type": "string", "maxLength": 253},
    "network": {"type": "object"}
  }
}`
	annotationEndpoints = "actfb.io/endpoints"
)

// Settings is the strict administrator block.
type Settings struct {
	Namespace            string               `json:"namespace"`
	StorageClass         string               `json:"storageClass,omitempty"`
	PodPIDsLimitVerified bool                 `json:"podPidsLimitVerified,omitempty"`
	VMImageMap           map[string]string    `json:"vmImageMap"`
	ImagePullSecret      string               `json:"imagePullSecret,omitempty"`
	Network              *runtime.NetworkPool `json:"network,omitempty"`
}

// WorkloadOptions is the strict template block.
type WorkloadOptions struct {
	MachineType string `json:"machineType,omitempty"`
	RunStrategy string `json:"runStrategy,omitempty"`
}

func decodeSettings(target runtime.Target) (Settings, error) {
	envelope, err := runtime.DecodeTargetSettings(target.Settings)
	if err != nil {
		return Settings{}, err
	}
	raw := envelope.DriverOptions()
	settings := Settings{}
	if len(raw) == 0 {
		return settings, errors.New("kubevirt settings are required")
	}
	if err := runtime.StrictUnmarshal(raw, &settings); err != nil {
		return Settings{}, fmt.Errorf("unknown or invalid kubevirt settings: %w", err)
	}
	settings.Namespace = strings.TrimSpace(settings.Namespace)
	if settings.Namespace == "" || settings.Namespace == "default" || settings.Namespace == "kube-system" {
		return Settings{}, errors.New("namespace must be a dedicated platform namespace")
	}
	if err := runtime.ValidateNetworkPool(settings.Network, "namespace"); err != nil {
		return Settings{}, err
	}
	for logical, pvc := range settings.VMImageMap {
		if err := runtime.ValidateLogicalVMRef(logical); err != nil {
			return Settings{}, fmt.Errorf("vmImageMap key %q is invalid", logical)
		}
		if !dnsLabel(pvc) {
			return Settings{}, fmt.Errorf("vmImageMap value for %q must be a PVC name", logical)
		}
	}
	return settings, nil
}

// ParseWorkloadOptions strictly decodes template options.
func ParseWorkloadOptions(raw json.RawMessage) (WorkloadOptions, error) {
	options := WorkloadOptions{}
	if len(raw) == 0 {
		return options, nil
	}
	if err := runtime.StrictUnmarshal(raw, &options); err != nil {
		return WorkloadOptions{}, err
	}
	switch options.RunStrategy {
	case "", "Always", "Halted", "Manual", "RerunOnFailure":
	default:
		return WorkloadOptions{}, errors.New("runStrategy is invalid")
	}
	if options.MachineType != "" && !strings.HasPrefix(options.MachineType, "q35") && !strings.HasPrefix(options.MachineType, "pc") {
		return WorkloadOptions{}, errors.New("machineType must be q35 or pc")
	}
	return options, nil
}

// Driver is the KubeVirt VM adapter.
type Driver struct{}

var (
	_ runtime.Driver          = (*Driver)(nil)
	_ runtime.OptionValidator = (*Driver)(nil)
	_ runtime.StaticMetadata  = (*Driver)(nil)
	_ runtime.Probe           = (*Driver)(nil)
)

// New constructs the KubeVirt driver.
func New() runtime.Driver { return &Driver{} }

// Name returns "kubevirt".
func (d *Driver) Name() string { return deployment.DriverKubeVirt }

// Kind returns VM.
func (d *Driver) Kind() deployment.Kind { return deployment.KindVM }

// StaticCapabilities describes the adapter.
func (d *Driver) StaticCapabilities() deployment.Capabilities {
	return deployment.Capabilities{
		Kind:                deployment.KindVM,
		SourceTypes:         []deployment.SourceType{deployment.SourceVMImage},
		CloudInit:           true,
		FileInjection:       false,
		PublicExposure:      true,
		SnapshotReset:       false,
		PersistentVolumes:   false,
		MaxNodes:            deployment.MaxNodes,
		DriverOptionsSchema: json.RawMessage(DriverOptionsSchema),
	}
}

// Capabilities validates settings and performs a read-only API check.
func (d *Driver) Capabilities(ctx context.Context, target runtime.Target) (deployment.Capabilities, error) {
	settings, settingsErr := decodeSettings(target)
	if settingsErr != nil {
		return deployment.Capabilities{}, &runtime.CapabilityError{Driver: d.Name(), Capability: "settings", Detail: settingsErr.Error()}
	}
	client, err := k8s.NewClientForNamespace(target, settings.Namespace, "")
	if err != nil {
		return deployment.Capabilities{}, err
	}
	if err := client.Ping(ctx, settings.Namespace); err != nil {
		return deployment.Capabilities{}, err
	}
	return d.StaticCapabilities(), nil
}

// Validate is non-mutating.
func (d *Driver) Validate(_ context.Context, target runtime.Target, node deployment.Node) []deployment.Finding {
	findings := make([]deployment.Finding, 0)
	if node.Kind != deployment.KindVM {
		return append(findings, runtime.ErrorFinding("kind", "DRIVER_KIND_MISMATCH", "kubevirt driver requires a VM node"))
	}
	settings, err := decodeSettings(target)
	if err != nil {
		findings = append(findings, runtime.ErrorFinding("integration.settings", "DRIVER_SETTINGS_INVALID", err.Error()))
	} else if !settings.PodPIDsLimitVerified {
		findings = append(findings, runtime.ErrorFinding("integration.settings", "PID_LIMIT_NOT_VERIFIED", "the operator must confirm a kubelet PodPidsLimit before untrusted VMs are allowed"))
	}
	if _, err := ParseWorkloadOptions(node.DriverOptions[d.Name()]); err != nil {
		findings = append(findings, runtime.ErrorFinding("driverOptions."+d.Name(), "DRIVER_OPTION_INVALID", err.Error()))
	}
	if node.Source.Type != deployment.SourceVMImage {
		findings = append(findings, runtime.ErrorFinding("source.type", "SOURCE_TYPE_UNSUPPORTED", "kubevirt requires a VM_IMAGE source"))
	}
	if err := runtime.ValidateLogicalVMRef(node.Source.Ref); err != nil {
		findings = append(findings, runtime.ErrorFinding("source.ref", "SOURCE_NOT_LOGICAL", err.Error()))
	} else if _, mapped := settings.VMImageMap[node.Source.Ref]; !mapped {
		findings = append(findings, runtime.ErrorFinding("source.ref", "SOURCE_NOT_MAPPED", "the logical VM image is not approved by the integration"))
	}
	if node.Bootstrap == nil {
		findings = append(findings, runtime.WarningFinding("bootstrap", "CLOUD_INIT_RECOMMENDED", "without cloud-init the cloned image must already boot and expose its services"))
	}
	for index := range node.Files {
		findings = append(findings, runtime.ErrorFinding(fmt.Sprintf("files[%d]", index), "FILE_INJECTION_UNSUPPORTED", "this KubeVirt adapter cannot inject files into a running VM"))
		break
	}
	findings = append(findings, runtime.ValidateResources(node.Kind, node.Resources)...)
	findings = append(findings, runtime.ValidateEndpoints(node.Kind, node.Endpoints)...)
	for index, endpoint := range node.Endpoints {
		if endpoint.Exposure != deployment.ExposurePublic {
			continue
		}
		if target.Publication.PublicPortFrom < 30000 || target.Publication.PublicPortTo > 32767 {
			findings = append(findings, runtime.ErrorFinding(fmt.Sprintf("endpoints[%d]", index), "NODEPORT_RANGE_REQUIRED", "the publication range must be inside the NodePort range 30000-32767"))
		}
	}
	return findings
}

// Plan is a non-mutating hint.
func (d *Driver) Plan(_ context.Context, _ runtime.Target, desired deployment.Node, current *runtime.ProviderResource) (runtime.Plan, error) {
	plan := runtime.Plan{NodeKey: desired.Key}
	if current == nil || current.ProviderID == "" {
		plan.Action = "CREATE"
	} else if current.State == runtime.NodeStateRunning {
		plan.Action = "NONE"
	} else {
		plan.Action = "RECREATE"
	}
	return plan, nil
}

// Apply reconciles one VM generation.
func (d *Driver) Apply(ctx context.Context, target runtime.Target, desired deployment.Node, op runtime.Operation) (runtime.ProviderResource, error) {
	if op.Kind == runtime.OperationDestroy {
		return runtime.ProviderResource{}, errors.New("kubevirt: DESTROY must use Destroy")
	}
	if findings := d.Validate(ctx, target, desired); hasErrors(findings) {
		return runtime.ProviderResource{}, &runtime.CapabilityError{Driver: d.Name(), Capability: "validation", Detail: findings[0].Code}
	}
	settings, err := decodeSettings(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	workload, err := ParseWorkloadOptions(desired.DriverOptions[d.Name()])
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	envelope, err := runtime.DecodeTargetSettings(target.Settings)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	client, err := k8s.NewClientForNamespace(target, settings.Namespace, "")
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	if err := client.Ping(ctx, settings.Namespace); err != nil {
		return runtime.ProviderResource{}, err
	}
	labels := runtime.Labels(d.Name(), op)
	name := runtime.SanitizeName("actfb", op.InstanceID.String(), op.NodeKey, "g"+strconv.FormatInt(op.Generation, 10))
	if err := d.cleanupStale(ctx, client, settings.Namespace, op); err != nil {
		return runtime.ProviderResource{}, err
	}
	vm, err := d.buildVM(ctx, client, settings, name, labels, desired, workload, envelope.Material)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	if err := d.ensureVM(ctx, client, settings.Namespace, name, vm); err != nil {
		return runtime.ProviderResource{}, err
	}
	if err := d.waitRunning(ctx, client, settings.Namespace, name); err != nil {
		return runtime.ProviderResource{}, err
	}
	endpoints, err := d.ensureEndpoints(ctx, client, settings.Namespace, name, labels, desired, envelope.Allocation)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	if err := d.ensureNetworkPolicy(ctx, client, settings.Namespace, name, labels, desired); err != nil {
		return runtime.ProviderResource{}, err
	}
	return runtime.ProviderResource{ProviderID: name, State: runtime.NodeStateRunning, Endpoints: endpoints, Message: "virtual machine running"}, nil
}

func (d *Driver) buildVM(ctx context.Context, client *k8s.Client, settings Settings, name string, labels map[string]string, desired deployment.Node, workload WorkloadOptions, material *runtime.Material) (map[string]any, error) {
	sourcePVC, ok := settings.VMImageMap[desired.Source.Ref]
	if !ok {
		return nil, &runtime.CapabilityError{Driver: d.Name(), Capability: "source", Detail: "logical VM image is not approved"}
	}
	annotations := map[string]string{annotationEndpoints: endpointAnnotation(desired.Endpoints)}
	volumes := []any{
		map[string]any{"name": "rootdisk", "dataVolume": map[string]any{"name": name + "-dv"}},
	}
	if material != nil && len(material.Bootstrap) > 0 {
		secret := map[string]any{
			"apiVersion": "v1", "kind": "Secret",
			"metadata":   map[string]any{"name": name + "-cloudinit", "namespace": settings.Namespace, "labels": labels},
			"type":       "Opaque",
			"stringData": map[string]string{"userdata": string(material.Bootstrap)},
		}
		if err := client.Do(ctx, http.MethodPost, "/api/v1/namespaces/"+url.PathEscape(settings.Namespace)+"/secrets", secret, nil); err != nil && !k8s.IsConflict(err) {
			return nil, err
		}
		volumes = append(volumes, map[string]any{
			"name": "cloudinitdisk",
			"cloudInitNoCloud": map[string]any{
				"secretRef": map[string]any{"name": name + "-cloudinit"},
			},
		})
	}
	disks := []any{map[string]any{"name": "rootdisk", "disk": map[string]any{"bus": "virtio"}}}
	if material != nil && len(material.Bootstrap) > 0 {
		disks = append(disks, map[string]any{"name": "cloudinitdisk", "disk": map[string]any{"bus": "virtio"}})
	}
	runStrategy := workload.RunStrategy
	if runStrategy == "" {
		runStrategy = "Always"
	}
	machine := map[string]any{}
	if workload.MachineType != "" {
		machine["type"] = workload.MachineType
	}
	vm := map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata":   map[string]any{"name": name, "namespace": settings.Namespace, "labels": labels, "annotations": annotations},
		"spec": map[string]any{
			"runStrategy": runStrategy,
			"dataVolumeTemplates": []any{map[string]any{
				"metadata": map[string]any{"name": name + "-dv", "labels": labels},
				"spec": map[string]any{
					"pvc": map[string]any{
						"accessModes":      []string{"ReadWriteOnce"},
						"resources":        map[string]any{"requests": map[string]string{"storage": fmt.Sprintf("%dMi", desired.Resources.DiskMiB)}},
						"storageClassName": settings.StorageClass,
					},
					"source": map[string]any{"pvc": map[string]any{"name": sourcePVC, "namespace": settings.Namespace}},
				},
			}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"domain": map[string]any{
						"machine": machine,
						"cpu":     map[string]any{"cores": maxInt(1, desired.Resources.CPUMillicores/1000)},
						"resources": map[string]any{
							"requests": map[string]string{
								"memory": fmt.Sprintf("%dMi", desired.Resources.MemoryMiB),
								"cpu":    fmt.Sprintf("%dm", desired.Resources.CPUMillicores),
							},
						},
						"devices": map[string]any{
							"disks":      disks,
							"interfaces": []any{map[string]any{"name": "default", "masquerade": map[string]any{}}},
						},
					},
					"networks": []any{map[string]any{"name": "default", "pod": map[string]any{}}},
					"volumes":  volumes,
				},
			},
		},
	}
	return vm, nil
}

func (d *Driver) ensureVM(ctx context.Context, client *k8s.Client, namespace, name string, vm map[string]any) error {
	path := "/apis/kubevirt.io/v1/namespaces/" + url.PathEscape(namespace) + "/virtualmachines"
	var existing struct {
		Metadata k8s.ObjectMeta `json:"metadata"`
	}
	if err := client.Do(ctx, http.MethodPost, path, vm, &existing); err != nil {
		if !k8s.IsConflict(err) {
			return err
		}
		if err := client.Do(ctx, http.MethodGet, path+"/"+url.PathEscape(name), nil, &existing); err != nil {
			return err
		}
		if !runtime.Owns(existing.Metadata.Labels, parseUUID(existing.Metadata.Labels[runtime.LabelInstance]), existing.Metadata.Labels[runtime.LabelNode]) {
			return fmt.Errorf("%w: virtual machine has foreign labels", runtime.ErrOwnershipConflict)
		}
	}
	return nil
}

func (d *Driver) waitRunning(ctx context.Context, client *k8s.Client, namespace, name string) error {
	deadline := time.Now().Add(5 * time.Minute)
	path := "/apis/kubevirt.io/v1/namespaces/" + url.PathEscape(namespace) + "/virtualmachines/" + url.PathEscape(name)
	for {
		var vm struct {
			Status struct {
				PrintableStatus string `json:"printableStatus"`
				Created         bool   `json:"created"`
				Ready           bool   `json:"ready"`
			} `json:"status"`
		}
		if err := client.Do(ctx, http.MethodGet, path, nil, &vm); err != nil {
			return err
		}
		if vm.Status.Ready || vm.Status.PrintableStatus == "Running" || (vm.Status.Created && vm.Status.PrintableStatus == "") {
			return nil
		}
		if vm.Status.PrintableStatus == "Failed" || vm.Status.PrintableStatus == "CrashLoopBackOff" || vm.Status.PrintableStatus == "ErrorUnschedulable" {
			return fmt.Errorf("%w: virtual machine status %s", runtime.ErrProviderUnavailable, vm.Status.PrintableStatus)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: virtual machine did not become ready", runtime.ErrProviderUnavailable)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (d *Driver) ensureEndpoints(ctx context.Context, client *k8s.Client, namespace, vmName string, labels map[string]string, desired deployment.Node, allocation *runtime.Allocation) ([]runtime.ProviderEndpoint, error) {
	var vmi struct {
		Status struct {
			Interfaces []struct {
				IPAddress string `json:"ipAddress"`
			} `json:"interfaces"`
		} `json:"status"`
	}
	_ = client.Do(ctx, http.MethodGet, "/apis/kubevirt.io/v1/namespaces/"+url.PathEscape(namespace)+"/virtualmachineinstances/"+url.PathEscape(vmName), nil, &vmi)
	podIP := ""
	if len(vmi.Status.Interfaces) > 0 {
		podIP = vmi.Status.Interfaces[0].IPAddress
	}
	endpoints := make([]runtime.ProviderEndpoint, 0, len(desired.Endpoints))
	for _, endpoint := range desired.Endpoints {
		provider := runtime.ProviderEndpoint{Key: endpoint.Key, Type: endpoint.Protocol, Host: podIP, Port: endpoint.TargetPort, Exposure: endpoint.Exposure}
		if endpoint.Exposure == deployment.ExposurePublic {
			if allocation == nil {
				return nil, &runtime.CapabilityError{Driver: d.Name(), Capability: "publication", Detail: "core port allocation is missing"}
			}
			binding, ok := allocation.Ports[endpoint.Key]
			if !ok || binding.Port < 30000 || binding.Port > 32767 {
				return nil, &runtime.CapabilityError{Driver: d.Name(), Capability: "publication", Detail: "allocated NodePort is missing or out of range"}
			}
			selector := map[string]string{runtime.LabelInstance: labels[runtime.LabelInstance], runtime.LabelNode: labels[runtime.LabelNode]}
			service := map[string]any{
				"apiVersion": "v1", "kind": "Service",
				"metadata": map[string]any{"name": runtime.SanitizeName(vmName, endpoint.Key), "namespace": namespace, "labels": labels},
				"spec": map[string]any{
					"type": "NodePort", "selector": selector,
					"ports": []any{map[string]any{
						"name": runtime.SanitizeName(endpoint.Key), "protocol": "TCP",
						"port": endpoint.TargetPort, "targetPort": endpoint.TargetPort, "nodePort": binding.Port,
					}},
				},
			}
			if err := client.Do(ctx, http.MethodPost, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/services", service, nil); err != nil && !k8s.IsConflict(err) {
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

func (d *Driver) ensureNetworkPolicy(ctx context.Context, client *k8s.Client, namespace, vmName string, labels map[string]string, desired deployment.Node) error {
	publicPorts := make([]any, 0)
	for _, endpoint := range desired.Endpoints {
		if endpoint.Exposure == deployment.ExposurePublic {
			publicPorts = append(publicPorts, map[string]any{"protocol": "TCP", "port": endpoint.TargetPort})
		}
	}
	ingress := make([]any, 0)
	if len(publicPorts) > 0 {
		ingress = append(ingress, map[string]any{
			"from":  []any{map[string]any{"ipBlock": map[string]any{"cidr": "0.0.0.0/0"}}, map[string]any{"ipBlock": map[string]any{"cidr": "::/0"}}},
			"ports": publicPorts,
		})
	}
	policy := map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": runtime.SanitizeName(vmName, "netpol"), "namespace": namespace, "labels": labels},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]string{runtime.LabelInstance: labels[runtime.LabelInstance], runtime.LabelNode: labels[runtime.LabelNode]}},
			"policyTypes": []string{"Ingress", "Egress"},
			"ingress":     ingress,
			"egress":      []any{},
		},
	}
	if err := client.Do(ctx, http.MethodPost, "/apis/networking.k8s.io/v1/namespaces/"+url.PathEscape(namespace)+"/networkpolicies", policy, nil); err != nil && !k8s.IsConflict(err) {
		return err
	}
	return nil
}

func (d *Driver) cleanupStale(ctx context.Context, client *k8s.Client, namespace string, op runtime.Operation) error {
	selector := url.QueryEscape(runtime.LabelInstance + "=" + op.InstanceID.String() + "," + runtime.LabelNode + "=" + op.NodeKey)
	var list struct {
		Items []struct {
			Metadata k8s.ObjectMeta `json:"metadata"`
		} `json:"items"`
	}
	if err := client.Do(ctx, http.MethodGet, "/apis/kubevirt.io/v1/namespaces/"+url.PathEscape(namespace)+"/virtualmachines?labelSelector="+selector, nil, &list); err != nil {
		return err
	}
	for _, item := range list.Items {
		if !runtime.Owns(item.Metadata.Labels, op.InstanceID, op.NodeKey) {
			return fmt.Errorf("%w: foreign virtual machine %s", runtime.ErrOwnershipConflict, item.Metadata.Name)
		}
		generation := runtime.Generation(item.Metadata.Labels)
		if generation > op.Generation {
			return fmt.Errorf("%w: future VM generation %d", runtime.ErrStaleGeneration, generation)
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

func (d *Driver) deleteOwned(ctx context.Context, client *k8s.Client, namespace, name string, op runtime.Operation) error {
	path := "/apis/kubevirt.io/v1/namespaces/" + url.PathEscape(namespace) + "/virtualmachines/" + url.PathEscape(name)
	// Halt first so a running guest is stopped cleanly.
	var vm struct {
		Metadata k8s.ObjectMeta `json:"metadata"`
		Spec     map[string]any `json:"spec"`
	}
	if err := client.Do(ctx, http.MethodGet, path, nil, &vm); err != nil {
		if k8s.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !runtime.Owns(vm.Metadata.Labels, op.InstanceID, op.NodeKey) {
		return fmt.Errorf("%w: refusing to delete a foreign virtual machine", runtime.ErrOwnershipConflict)
	}
	if vm.Spec == nil {
		vm.Spec = map[string]any{}
	}
	vm.Spec["runStrategy"] = "Halted"
	_ = client.Do(ctx, http.MethodPut, path, map[string]any{
		"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachine",
		"metadata": vm.Metadata, "spec": vm.Spec,
	}, nil)
	if err := client.Do(ctx, http.MethodDelete, path+"?gracePeriodSeconds=30&propagationPolicy=Background", nil, nil); err != nil && !k8s.IsNotFound(err) {
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err := client.Do(ctx, http.MethodGet, path, nil, nil)
		if k8s.IsNotFound(err) {
			break
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: virtual machine deletion not confirmed", runtime.ErrProviderUnavailable)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	// CDI data volumes have owner references, but the PVC is also deleted
	// explicitly to guarantee no orphaned storage.
	_ = client.Do(ctx, http.MethodDelete, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/persistentvolumeclaims/"+url.PathEscape(name+"-dv"), nil, nil)
	_ = client.Do(ctx, http.MethodDelete, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/secrets/"+url.PathEscape(name+"-cloudinit"), nil, nil)
	selector := url.QueryEscape(runtime.LabelInstance + "=" + op.InstanceID.String() + "," + runtime.LabelNode + "=" + op.NodeKey)
	var services struct {
		Items []struct {
			Metadata k8s.ObjectMeta `json:"metadata"`
		} `json:"items"`
	}
	_ = client.Do(ctx, http.MethodGet, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/services?labelSelector="+selector, nil, &services)
	for _, service := range services.Items {
		_ = client.Do(ctx, http.MethodDelete, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/services/"+url.PathEscape(service.Metadata.Name), nil, nil)
	}
	_ = client.Do(ctx, http.MethodDelete, "/apis/networking.k8s.io/v1/namespaces/"+url.PathEscape(namespace)+"/networkpolicies/"+url.PathEscape(runtime.SanitizeName(name, "netpol")), nil, nil)
	return nil
}

// Inspect returns VM state by name.
func (d *Driver) Inspect(ctx context.Context, target runtime.Target, providerID string) (runtime.ProviderResource, error) {
	settings, err := decodeSettings(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	client, err := k8s.NewClientForNamespace(target, settings.Namespace, "")
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	var vm struct {
		Metadata k8s.ObjectMeta `json:"metadata"`
		Status   struct {
			PrintableStatus string `json:"printableStatus"`
			Ready           bool   `json:"ready"`
		} `json:"status"`
	}
	path := "/apis/kubevirt.io/v1/namespaces/" + url.PathEscape(settings.Namespace) + "/virtualmachines/" + url.PathEscape(providerID)
	if err := client.Do(ctx, http.MethodGet, path, nil, &vm); err != nil {
		if k8s.IsNotFound(err) {
			return runtime.ProviderResource{ProviderID: providerID, State: runtime.NodeStateDestroyed}, nil
		}
		return runtime.ProviderResource{}, err
	}
	resource := runtime.ProviderResource{ProviderID: providerID, State: runtime.NodeStateUnknown}
	if vm.Status.Ready || vm.Status.PrintableStatus == "Running" {
		resource.State = runtime.NodeStateRunning
	} else if vm.Status.PrintableStatus == "Failed" || vm.Status.PrintableStatus == "ErrorUnschedulable" {
		resource.State = runtime.NodeStateError
		resource.Message = "virtual machine " + vm.Status.PrintableStatus
	}
	var endpoints []runtime.ProviderEndpoint
	if raw := vm.Metadata.Annotations[annotationEndpoints]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &endpoints)
	}
	resource.Endpoints = endpoints
	return resource, nil
}

// Destroy halts and deletes the VM and waits for confirmation.
func (d *Driver) Destroy(ctx context.Context, target runtime.Target, providerID string, op runtime.Operation) error {
	settings, err := decodeSettings(target)
	if err != nil {
		return err
	}
	client, err := k8s.NewClientForNamespace(target, settings.Namespace, "")
	if err != nil {
		return err
	}
	if strings.TrimSpace(providerID) == "" {
		return nil
	}
	return d.deleteOwned(ctx, client, settings.Namespace, providerID, op)
}

// Probe performs a read-only API check.
func (d *Driver) Probe(ctx context.Context, target runtime.Target) error {
	settings, err := decodeSettings(target)
	if err != nil {
		return err
	}
	client, err := k8s.NewClientForNamespace(target, settings.Namespace, "")
	if err != nil {
		return err
	}
	return client.Ping(ctx, settings.Namespace)
}

// ValidateSettings implements runtime.SettingsValidator.
func (d *Driver) ValidateSettings(raw json.RawMessage) []deployment.Finding {
	target := runtime.Target{Settings: envelopeFor(raw)}
	if _, err := decodeSettings(target); err != nil {
		return []deployment.Finding{runtime.ErrorFinding("", "DRIVER_SETTINGS_INVALID", err.Error())}
	}
	return nil
}

// ValidateOptions implements the closed option schema.
func (d *Driver) ValidateOptions(raw json.RawMessage) []deployment.Finding {
	if _, err := ParseWorkloadOptions(raw); err != nil {
		return []deployment.Finding{runtime.ErrorFinding("", "DRIVER_OPTION_INVALID", err.Error())}
	}
	return nil
}

func endpointAnnotation(endpoints []deployment.Endpoint) string {
	projected := make([]runtime.ProviderEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		projected = append(projected, runtime.ProviderEndpoint{Key: endpoint.Key, Type: endpoint.Protocol, Port: endpoint.TargetPort, Exposure: endpoint.Exposure})
	}
	raw, _ := json.Marshal(projected)
	return string(raw)
}

func parseUUID(raw string) uuid.UUID {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}

func dnsLabel(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for index, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
		case char == '-' && index > 0:
		default:
			return false
		}
	}
	return true
}

func envelopeFor(raw json.RawMessage) json.RawMessage {
	encoded, _ := json.Marshal(map[string]json.RawMessage{"options": raw})
	return encoded
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func hasErrors(findings []deployment.Finding) bool {
	for _, finding := range findings {
		if finding.Severity == deployment.SeverityError {
			return true
		}
	}
	return false
}
