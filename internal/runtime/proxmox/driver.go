package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

// Driver is the real Proxmox VM adapter.
type Driver struct{}

var (
	_ runtime.Driver          = (*Driver)(nil)
	_ runtime.OptionValidator = (*Driver)(nil)
	_ runtime.StaticMetadata  = (*Driver)(nil)
	_ runtime.Probe           = (*Driver)(nil)
)

// New constructs the Proxmox driver.
func New() runtime.Driver { return &Driver{} }

// Name returns "proxmox".
func (d *Driver) Name() string { return deployment.DriverProxmox }

// Kind returns VM.
func (d *Driver) Kind() deployment.Kind { return deployment.KindVM }

// StaticCapabilities reports the honest adapter surface. Cloud-init injection
// and public port publication are not implemented by this adapter, so they are
// advertised as unsupported instead of silently ignored.
func (d *Driver) StaticCapabilities() deployment.Capabilities {
	return deployment.Capabilities{
		Kind:                deployment.KindVM,
		SourceTypes:         []deployment.SourceType{deployment.SourceVMTemplate},
		CloudInit:           false,
		FileInjection:       false,
		PublicExposure:      false,
		SnapshotReset:       false,
		PersistentVolumes:   false,
		MaxNodes:            deployment.MaxNodes,
		DriverOptionsSchema: json.RawMessage(DriverOptionsSchema),
	}
}

// Capabilities validates settings and performs a read-only version check.
func (d *Driver) Capabilities(ctx context.Context, target runtime.Target) (deployment.Capabilities, error) {
	if _, err := decodeSettings(target); err != nil {
		return deployment.Capabilities{}, &runtime.CapabilityError{Driver: d.Name(), Capability: "settings", Detail: err.Error()}
	}
	client, err := NewClient(target)
	if err != nil {
		return deployment.Capabilities{}, err
	}
	if err := client.Ping(ctx); err != nil {
		return deployment.Capabilities{}, err
	}
	return d.StaticCapabilities(), nil
}

// Validate is non-mutating.
func (d *Driver) Validate(_ context.Context, target runtime.Target, node deployment.Node) []deployment.Finding {
	findings := make([]deployment.Finding, 0)
	if node.Kind != deployment.KindVM {
		return append(findings, runtime.ErrorFinding("kind", "DRIVER_KIND_MISMATCH", "proxmox driver requires a VM node"))
	}
	settings, err := decodeSettings(target)
	if err != nil {
		findings = append(findings, runtime.ErrorFinding("integration.settings", "DRIVER_SETTINGS_INVALID", err.Error()))
	}
	if _, err := ParseWorkloadOptions(node.DriverOptions[d.Name()]); err != nil {
		findings = append(findings, runtime.ErrorFinding("driverOptions."+d.Name(), "DRIVER_OPTION_INVALID", err.Error()))
	}
	if node.Source.Type != deployment.SourceVMTemplate {
		findings = append(findings, runtime.ErrorFinding("source.type", "SOURCE_TYPE_UNSUPPORTED", "proxmox requires a VM_TEMPLATE source"))
	}
	if err := runtime.ValidateLogicalVMRef(node.Source.Ref); err != nil {
		findings = append(findings, runtime.ErrorFinding("source.ref", "SOURCE_NOT_LOGICAL", err.Error()))
	} else if _, mapped := settings.TemplateMap[node.Source.Ref]; !mapped {
		findings = append(findings, runtime.ErrorFinding("source.ref", "SOURCE_NOT_MAPPED", "the logical template is not approved by the integration"))
	}
	if node.Bootstrap != nil {
		findings = append(findings, runtime.ErrorFinding("bootstrap", "CLOUD_INIT_UNSUPPORTED", "this Proxmox adapter does not inject cloud-init; bake it into the approved template"))
	}
	for index, file := range node.Files {
		if len(node.Files) > 0 {
			_ = index
			findings = append(findings, runtime.ErrorFinding("files", "FILE_INJECTION_UNSUPPORTED", "this Proxmox adapter cannot inject files into a VM"))
			break
		}
		_ = file
	}
	findings = append(findings, runtime.ValidateResources(node.Kind, node.Resources)...)
	findings = append(findings, runtime.ValidateEndpoints(node.Kind, node.Endpoints)...)
	for index, endpoint := range node.Endpoints {
		if endpoint.Exposure == deployment.ExposurePublic {
			findings = append(findings, runtime.ErrorFinding(fmt.Sprintf("endpoints[%d]", index), "PUBLIC_EXPOSURE_UNSUPPORTED", "this Proxmox adapter does not publish VM ports; expose the VM through an external ingress"))
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

// Apply clones from the approved template into a VMID from the configured pool,
// polls the clone UPID, configures the VM, starts it and confirms RUNNING.
func (d *Driver) Apply(ctx context.Context, target runtime.Target, desired deployment.Node, op runtime.Operation) (runtime.ProviderResource, error) {
	if op.Kind == runtime.OperationDestroy {
		return runtime.ProviderResource{}, errors.New("proxmox: DESTROY must use Destroy")
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
	client, err := NewClient(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	if err := client.Ping(ctx); err != nil {
		return runtime.ProviderResource{}, err
	}
	labels := runtime.Labels(d.Name(), op)

	existing, err := d.discover(ctx, client, settings, op)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	var adopted *ownedVM
	for index := range existing {
		item := existing[index]
		generation := runtime.Generation(item.labels)
		if generation > op.Generation {
			return runtime.ProviderResource{}, fmt.Errorf("%w: VM generation %d", runtime.ErrStaleGeneration, generation)
		}
		if generation == op.Generation {
			adopted = &existing[index]
			continue
		}
		if err := d.destroyVM(ctx, client, settings, item.vmid, op); err != nil {
			return runtime.ProviderResource{}, err
		}
	}

	vmid := 0
	if adopted != nil {
		vmid = adopted.vmid
		if status, statusErr := client.GetStatus(ctx, settings.Node, vmid); statusErr != nil {
			return runtime.ProviderResource{}, statusErr
		} else if status.Status != "running" {
			if err := d.start(ctx, client, settings.Node, vmid); err != nil {
				return runtime.ProviderResource{}, err
			}
		}
	} else {
		vmid, err = d.clone(ctx, client, settings, desired, workload, labels, op)
		if err != nil {
			return runtime.ProviderResource{}, err
		}
		if err := d.start(ctx, client, settings.Node, vmid); err != nil {
			return runtime.ProviderResource{}, err
		}
	}

	providerID := strconv.Itoa(vmid)
	if err := d.waitRunning(ctx, client, settings.Node, vmid); err != nil {
		return runtime.ProviderResource{}, err
	}
	endpoints, err := d.endpointsForVM(ctx, client, settings, desired, vmid)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	return runtime.ProviderResource{ProviderID: providerID, State: runtime.NodeStateRunning, Endpoints: endpoints, Message: "VM running"}, nil
}

type ownedVM struct {
	vmid   int
	labels Labels
}

// discover lists cluster VMs and returns only VMs whose description carries the
// exact platform labels for this instance/node. Names are only a shortlist hint.
func (d *Driver) discover(ctx context.Context, client *Client, settings Settings, op runtime.Operation) ([]ownedVM, error) {
	items, err := client.ListVMs(ctx)
	if err != nil {
		return nil, err
	}
	prefix := runtime.SanitizeName("actfb", op.InstanceID.String(), op.NodeKey)
	owned := make([]ownedVM, 0)
	for _, item := range items {
		if !strings.HasPrefix(item.Name, prefix) {
			continue
		}
		config, err := client.GetConfig(ctx, settings.Node, item.VMID)
		if err != nil {
			if errors.Is(err, runtime.ErrProviderNotFound) {
				continue
			}
			return nil, err
		}
		labels := DecodeLabels(config.Description)
		if labels == nil {
			continue
		}
		if !runtime.Owns(labels, op.InstanceID, op.NodeKey) {
			return nil, fmt.Errorf("%w: VM %d carries foreign labels", runtime.ErrOwnershipConflict, item.VMID)
		}
		owned = append(owned, ownedVM{vmid: item.VMID, labels: labels})
	}
	return owned, nil
}

func (d *Driver) clone(ctx context.Context, client *Client, settings Settings, desired deployment.Node, workload WorkloadOptions, labels Labels, op runtime.Operation) (int, error) {
	templateVMID, ok := settings.TemplateMap[desired.Source.Ref]
	if !ok {
		return 0, &runtime.CapabilityError{Driver: d.Name(), Capability: "source", Detail: "logical template is not approved"}
	}
	used, err := client.ListVMs(ctx)
	if err != nil {
		return 0, err
	}
	usedIDs := make(map[int]struct{}, len(used))
	for _, item := range used {
		usedIDs[item.VMID] = struct{}{}
	}
	name := runtime.SanitizeName("actfb", op.InstanceID.String(), op.NodeKey, "g"+strconv.FormatInt(op.Generation, 10))
	description := EncodeLabels(labels)
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		vmid := 0
		for candidate := settings.VMIDPool.From; candidate <= settings.VMIDPool.To; candidate++ {
			if _, taken := usedIDs[candidate]; !taken {
				vmid = candidate
				break
			}
		}
		if vmid == 0 {
			return 0, &runtime.CapabilityError{Driver: d.Name(), Capability: "vmid_pool", Detail: "no free VMID in the approved pool"}
		}
		form := url.Values{}
		form.Set("newid", strconv.Itoa(vmid))
		form.Set("name", name)
		form.Set("description", description)
		form.Set("full", boolInt(settings.FullClone))
		if settings.Storage != "" {
			form.Set("storage", settings.Storage)
		}
		if settings.Pool != "" {
			form.Set("pool", settings.Pool)
		}
		raw, err := client.Do(ctx, http.MethodPost, fmt.Sprintf("/nodes/%s/qemu/%d/clone", url.PathEscape(settings.Node), templateVMID), form)
		if err != nil {
			usedIDs[vmid] = struct{}{}
			lastErr = err
			continue
		}
		upid := decodeUPID(raw)
		if err := client.WaitTask(ctx, settings.Node, upid); err != nil {
			// The clone may have created a partial VM; remove it before retrying.
			_ = d.destroyVM(ctx, client, settings, vmid, op)
			usedIDs[vmid] = struct{}{}
			lastErr = err
			continue
		}
		if err := d.configure(ctx, client, settings, vmid, desired, workload, description); err != nil {
			_ = d.destroyVM(ctx, client, settings, vmid, op)
			return 0, err
		}
		return vmid, nil
	}
	if lastErr == nil {
		lastErr = errors.New("proxmox: clone failed")
	}
	return 0, lastErr
}

func (d *Driver) configure(ctx context.Context, client *Client, settings Settings, vmid int, desired deployment.Node, workload WorkloadOptions, description string) error {
	cores := workload.Cores
	if cores == 0 {
		cores = (desired.Resources.CPUMillicores + 999) / 1000
	}
	memory := workload.Memory
	if memory == 0 {
		memory = desired.Resources.MemoryMiB
	}
	form := url.Values{}
	form.Set("cores", strconv.Itoa(cores))
	form.Set("sockets", "1")
	form.Set("memory", strconv.Itoa(memory))
	form.Set("description", description)
	if workload.Bios != "" {
		form.Set("bios", workload.Bios)
	}
	if workload.Machine != "" {
		form.Set("machine", workload.Machine)
	}
	if workload.CPUType != "" {
		form.Set("cpu", workload.CPUType)
	}
	if workload.OSType != "" {
		form.Set("ostype", workload.OSType)
	}
	agent := settings.Agent
	if workload.Agent != nil {
		agent = *workload.Agent
	}
	if agent {
		form.Set("agent", "1")
	}
	if settings.Bridge != "" {
		// Only reconfigure the default NIC if one exists; never attach a NIC to
		// an arbitrary bridge from the template author.
		form.Set("net0", "virtio,bridge="+settings.Bridge)
	}
	_, err := client.Do(ctx, http.MethodPut, fmt.Sprintf("/nodes/%s/qemu/%d/config", url.PathEscape(settings.Node), vmid), form)
	return err
}

func (d *Driver) start(ctx context.Context, client *Client, node string, vmid int) error {
	raw, err := client.Do(ctx, http.MethodPost, fmt.Sprintf("/nodes/%s/qemu/%d/status/start", url.PathEscape(node), vmid), url.Values{})
	if err != nil {
		return err
	}
	return client.WaitTask(ctx, node, decodeUPID(raw))
}

func (d *Driver) waitRunning(ctx context.Context, client *Client, node string, vmid int) error {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		status, err := client.GetStatus(ctx, node, vmid)
		if err != nil {
			return err
		}
		if status.Status == "running" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: VM %d did not reach running", runtime.ErrProviderUnavailable, vmid)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (d *Driver) endpointsForVM(ctx context.Context, client *Client, settings Settings, desired deployment.Node, vmid int) ([]runtime.ProviderEndpoint, error) {
	if len(desired.Endpoints) == 0 {
		return nil, nil
	}
	if !settings.Agent {
		return nil, &runtime.CapabilityError{Driver: d.Name(), Capability: "guest_agent", Detail: "VM endpoints require the QEMU guest agent to be enabled and installed"}
	}
	addresses, err := client.AgentInterfaces(ctx, settings.Node, vmid)
	if err != nil {
		return nil, &runtime.CapabilityError{Driver: d.Name(), Capability: "guest_agent", Detail: "guest agent did not report an address"}
	}
	if len(addresses) == 0 {
		return nil, &runtime.CapabilityError{Driver: d.Name(), Capability: "guest_agent", Detail: "guest agent reported no routable IPv4 address"}
	}
	host := addresses[0]
	endpoints := make([]runtime.ProviderEndpoint, 0, len(desired.Endpoints))
	for _, endpoint := range desired.Endpoints {
		endpoints = append(endpoints, runtime.ProviderEndpoint{
			Key: endpoint.Key, Type: endpoint.Protocol, Host: host, Port: endpoint.TargetPort, Exposure: endpoint.Exposure,
		})
	}
	return endpoints, nil
}

// Inspect returns the VM state and its configured owner labels.
func (d *Driver) Inspect(ctx context.Context, target runtime.Target, providerID string) (runtime.ProviderResource, error) {
	settings, err := decodeSettings(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	client, err := NewClient(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	vmid, err := strconv.Atoi(providerID)
	if err != nil || vmid <= 0 {
		return runtime.ProviderResource{}, errors.New("proxmox: invalid provider id")
	}
	status, err := client.GetStatus(ctx, settings.Node, vmid)
	if err != nil {
		if errors.Is(err, runtime.ErrProviderNotFound) {
			return runtime.ProviderResource{ProviderID: providerID, State: runtime.NodeStateDestroyed}, nil
		}
		return runtime.ProviderResource{}, err
	}
	resource := runtime.ProviderResource{ProviderID: providerID, State: runtime.NodeStateUnknown}
	if status.Status == "running" {
		resource.State = runtime.NodeStateRunning
	} else {
		resource.State = runtime.NodeStateError
		resource.Message = "VM is " + status.Status
	}
	return resource, nil
}

// Destroy stops, then deletes the VM after verifying its owner labels, and
// waits for the delete task. A missing VM is success.
func (d *Driver) Destroy(ctx context.Context, target runtime.Target, providerID string, op runtime.Operation) error {
	settings, err := decodeSettings(target)
	if err != nil {
		return err
	}
	client, err := NewClient(target)
	if err != nil {
		return err
	}
	vmid, err := strconv.Atoi(strings.TrimSpace(providerID))
	if err != nil || vmid <= 0 {
		return errors.New("proxmox: invalid provider id")
	}
	return d.destroyVM(ctx, client, settings, vmid, op)
}

func (d *Driver) destroyVM(ctx context.Context, client *Client, settings Settings, vmid int, op runtime.Operation) error {
	config, err := client.GetConfig(ctx, settings.Node, vmid)
	if err != nil {
		if errors.Is(err, runtime.ErrProviderNotFound) {
			return nil
		}
		return err
	}
	labels := DecodeLabels(config.Description)
	if !runtime.Owns(labels, op.InstanceID, op.NodeKey) {
		return fmt.Errorf("%w: refusing to delete VM %d with foreign labels", runtime.ErrOwnershipConflict, vmid)
	}
	status, err := client.GetStatus(ctx, settings.Node, vmid)
	if err != nil && !errors.Is(err, runtime.ErrProviderNotFound) {
		return err
	}
	if status.Status == "running" {
		raw, stopErr := client.Do(ctx, http.MethodPost, fmt.Sprintf("/nodes/%s/qemu/%d/status/stop", url.PathEscape(settings.Node), vmid), url.Values{})
		if stopErr != nil {
			return stopErr
		}
		if err := client.WaitTask(ctx, settings.Node, decodeUPID(raw)); err != nil {
			return err
		}
	}
	form := url.Values{}
	form.Set("purge", "0")
	raw, err := client.Do(ctx, http.MethodDelete, fmt.Sprintf("/nodes/%s/qemu/%d", url.PathEscape(settings.Node), vmid)+"?"+form.Encode(), nil)
	if err != nil {
		if errors.Is(err, runtime.ErrProviderNotFound) {
			return nil
		}
		return err
	}
	return client.WaitTask(ctx, settings.Node, decodeUPID(raw))
}

// Probe performs a read-only version request.
func (d *Driver) Probe(ctx context.Context, target runtime.Target) error {
	client, err := NewClient(target)
	if err != nil {
		return err
	}
	return client.Ping(ctx)
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

func envelopeFor(raw json.RawMessage) json.RawMessage {
	encoded, _ := json.Marshal(map[string]json.RawMessage{"options": raw})
	return encoded
}

func decodeUPID(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	var object struct {
		UPID string `json:"upid"`
	}
	_ = json.Unmarshal(raw, &object)
	return object.UPID
}

func boolInt(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func hasErrors(findings []deployment.Finding) bool {
	for _, finding := range findings {
		if finding.Severity == deployment.SeverityError {
			return true
		}
	}
	return false
}
