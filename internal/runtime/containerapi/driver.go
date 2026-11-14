package containerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

// DriverOptionsSchema is the closed schema advertised to the template
// validator/preview. Provider credentials never appear here.
const DriverOptionsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "socket": {"type": "string", "description": "daemon unix socket; supplied by the administrator"},
    "endpoint": {"type": "string", "pattern": "^unix://"},
    "runtime": {"enum": ["runc", "crun"]},
    "registryAllowlist": {"type": "array", "items": {"type": "string"}, "maxItems": 64},
    "dedicatedNode": {"type": "string", "maxLength": 253},
    "dedicatedNodeVerified": {"type": "boolean"},
    "allowImagePull": {"type": "boolean"},
    "pidsLimit": {"type": "integer", "minimum": 16, "maximum": 4096}
  }
}`

// Settings is the strict administrator-supplied integration settings block.
type Settings struct {
	Socket                string               `json:"socket,omitempty"`
	Endpoint              string               `json:"endpoint,omitempty"`
	Runtime               string               `json:"runtime,omitempty"`
	RegistryAllowlist     []string             `json:"registryAllowlist,omitempty"`
	DedicatedNode         string               `json:"dedicatedNode,omitempty"`
	DedicatedNodeVerified bool                 `json:"dedicatedNodeVerified,omitempty"`
	AllowImagePull        bool                 `json:"allowImagePull,omitempty"`
	PIDsLimit             int                  `json:"pidsLimit,omitempty"`
	Network               *runtime.NetworkPool `json:"network,omitempty"`
}

// WorkloadOptions is the strict template-side driver options block.
type WorkloadOptions struct {
	User                   string         `json:"user,omitempty"`
	UserNumeric            bool           `json:"userNumeric,omitempty"`
	WorkingDir             string         `json:"workingDir,omitempty"`
	Entrypoint             []string       `json:"entrypoint,omitempty"`
	Cmd                    []string       `json:"cmd,omitempty"`
	ReadOnlyRootFilesystem *bool          `json:"readOnlyRootFilesystem,omitempty"`
	Tmpfs                  map[string]int `json:"tmpfs,omitempty"`
	StopSignal             string         `json:"stopSignal,omitempty"`
	HealthPath             string         `json:"healthPath,omitempty"`
}

var allowedTmpfsPrefixes = []string{"/tmp", "/var/tmp", "/run", "/var/run", "/var/cache", "/app/data"}

// ParseSettings strictly decodes the integration option block.
func ParseSettings(raw json.RawMessage) (Settings, error) {
	settings := Settings{}
	if len(raw) == 0 {
		return settings, nil
	}
	if err := runtime.StrictUnmarshal(raw, &settings); err != nil {
		return Settings{}, fmt.Errorf("unknown or invalid container settings: %w", err)
	}
	if settings.Socket != "" && settings.Endpoint != "" {
		return Settings{}, errors.New("socket and endpoint are mutually exclusive")
	}
	if settings.Endpoint != "" && !strings.HasPrefix(settings.Endpoint, "unix://") {
		return Settings{}, errors.New("endpoint must be a unix:// socket; remote daemons are not supported")
	}
	switch settings.Runtime {
	case "", "runc", "crun":
	default:
		return Settings{}, errors.New("runtime must be runc or crun")
	}
	if settings.PIDsLimit < 0 || settings.PIDsLimit > runtime.MaxContainerPIDs {
		return Settings{}, errors.New("pidsLimit is out of range")
	}
	if err := runtime.ValidateNetworkPool(settings.Network, "bridge", "network", "vlan"); err != nil {
		return Settings{}, err
	}
	if settings.DedicatedNodeVerified && strings.TrimSpace(settings.DedicatedNode) == "" {
		return Settings{}, errors.New("dedicatedNodeVerified requires dedicatedNode")
	}
	for _, host := range settings.RegistryAllowlist {
		if strings.ContainsAny(host, "/ \t\r\n") || strings.Contains(host, "..") {
			return Settings{}, errors.New("registry allowlist entries must be registry hosts")
		}
	}
	return settings, nil
}

// ParseWorkloadOptions strictly decodes the template option block.
func ParseWorkloadOptions(raw json.RawMessage) (WorkloadOptions, error) {
	options := WorkloadOptions{}
	if len(raw) == 0 {
		return options, nil
	}
	if err := runtime.StrictUnmarshal(raw, &options); err != nil {
		return WorkloadOptions{}, err
	}
	if options.UserNumeric && options.User == "" {
		return WorkloadOptions{}, errors.New("userNumeric requires user")
	}
	if options.User != "" && options.UserNumeric {
		parts := strings.SplitN(options.User, ":", 2)
		if len(parts) > 2 {
			return WorkloadOptions{}, errors.New("user must be uid or uid:gid when userNumeric is true")
		}
		for _, part := range parts {
			if _, err := strconv.Atoi(part); err != nil {
				return WorkloadOptions{}, errors.New("user must be uid or uid:gid when userNumeric is true")
			}
		}
	}
	if options.WorkingDir != "" {
		if !path.IsAbs(options.WorkingDir) || strings.Contains(options.WorkingDir, "..") {
			return WorkloadOptions{}, errors.New("workingDir must be an absolute path without traversal")
		}
	}
	for _, argument := range append(append([]string{}, options.Entrypoint...), options.Cmd...) {
		if strings.ContainsAny(argument, "\x00") || len(argument) > 4096 {
			return WorkloadOptions{}, errors.New("command arguments are invalid")
		}
	}
	if len(options.Entrypoint) > 32 || len(options.Cmd) > 128 {
		return WorkloadOptions{}, errors.New("too many command arguments")
	}
	for target, size := range options.Tmpfs {
		if !path.IsAbs(target) || strings.Contains(target, "..") {
			return WorkloadOptions{}, fmt.Errorf("tmpfs %q must be an absolute path", target)
		}
		if size <= 0 || size > runtime.MaxContainerDiskMiB {
			return WorkloadOptions{}, fmt.Errorf("tmpfs %q size is out of range", target)
		}
		allowed := false
		for _, prefix := range allowedTmpfsPrefixes {
			if target == prefix || strings.HasPrefix(target, prefix+"/") {
				allowed = true
				break
			}
		}
		if !allowed {
			return WorkloadOptions{}, fmt.Errorf("tmpfs %q is not an allowed writable path", target)
		}
	}
	switch options.StopSignal {
	case "", "SIGTERM", "SIGINT", "SIGQUIT":
	default:
		return WorkloadOptions{}, errors.New("stopSignal must be SIGTERM, SIGINT or SIGQUIT")
	}
	if options.HealthPath != "" {
		if !strings.HasPrefix(options.HealthPath, "/") || strings.Contains(options.HealthPath, "..") || len(options.HealthPath) > 255 {
			return WorkloadOptions{}, errors.New("healthPath must be an absolute path")
		}
	}
	return options, nil
}

// Config selects which engine this adapter talks to. Docker and podman each
// construct their own Config with their own socket resolution; neither falls
// back to the other.
type Config struct {
	DriverName    string
	DefaultSocket func(explicit string) string
	NodeKind      deployment.Kind
	Forwarders    *ForwarderRegistry
	// SupportsPublic selects whether the worker can publish an allocated host
	// port for this engine. Podman rootless endpoint publication is not proven,
	// so that adapter advertises PRIVATE endpoints only.
	SupportsPublic bool
}

// Driver is the Docker-Engine-API-compatible container driver.
type Driver struct {
	name           string
	defaultSocket  func(string) string
	forwarders     *ForwarderRegistry
	supportsPublic bool
}

var (
	_ runtime.Driver          = (*Driver)(nil)
	_ runtime.OptionValidator = (*Driver)(nil)
	_ runtime.StaticMetadata  = (*Driver)(nil)
	_ runtime.Probe           = (*Driver)(nil)
)

// New constructs an engine driver. A nil forwarder registry installs a
// process-local one; the trusted worker owns the forwarders that publish
// allocated host ports into the isolated instance network.
func New(config Config) (*Driver, error) {
	if strings.TrimSpace(config.DriverName) == "" {
		return nil, errors.New("containerapi: driver name is required")
	}
	if config.NodeKind != deployment.KindContainer {
		return nil, fmt.Errorf("containerapi: %s only supports CONTAINER nodes", config.DriverName)
	}
	forwarders := config.Forwarders
	if forwarders == nil {
		forwarders = NewForwarderRegistry()
	}
	return &Driver{name: config.DriverName, defaultSocket: config.DefaultSocket, forwarders: forwarders, supportsPublic: config.SupportsPublic}, nil
}

// Name returns the engine name.
func (d *Driver) Name() string { return d.name }

// Kind returns CONTAINER.
func (d *Driver) Kind() deployment.Kind { return deployment.KindContainer }

// StaticCapabilities returns capability metadata without contacting the daemon.
func (d *Driver) StaticCapabilities() deployment.Capabilities {
	return deployment.Capabilities{
		Kind:                deployment.KindContainer,
		SourceTypes:         []deployment.SourceType{deployment.SourceOCIImage},
		CloudInit:           false,
		FileInjection:       true,
		SnapshotReset:       false,
		PersistentVolumes:   false,
		PublicExposure:      d.supportsPublic,
		MaxNodes:            deployment.MaxNodes,
		DriverOptionsSchema: json.RawMessage(DriverOptionsSchema),
	}
}

// Capabilities resolves the target settings and returns the effective
// capability set. Missing/foreign sockets are an unsupported capability, not a
// silent fallback.
func (d *Driver) Capabilities(ctx context.Context, target runtime.Target) (deployment.Capabilities, error) {
	if err := runtime.ValidateTarget(target); err != nil {
		return deployment.Capabilities{}, err
	}
	if target.Driver != d.name {
		return deployment.Capabilities{}, &runtime.CapabilityError{Driver: d.name, Capability: "driver", Detail: target.Driver}
	}
	if _, err := decodeSettings(target); err != nil {
		return deployment.Capabilities{}, &runtime.CapabilityError{Driver: d.name, Capability: "settings", Detail: err.Error()}
	}
	if _, err := d.client(target); err != nil {
		return deployment.Capabilities{}, err
	}
	if err := ctx.Err(); err != nil {
		return deployment.Capabilities{}, err
	}
	return d.StaticCapabilities(), nil
}

// Validate implements the non-mutating per-node contract.
func (d *Driver) Validate(_ context.Context, target runtime.Target, node deployment.Node) []deployment.Finding {
	findings := make([]deployment.Finding, 0)
	if node.Kind != deployment.KindContainer {
		findings = append(findings, runtime.ErrorFinding("kind", "DRIVER_KIND_MISMATCH", "container driver requires a CONTAINER node"))
		return findings
	}
	settings, err := decodeSettings(target)
	if err != nil {
		findings = append(findings, runtime.ErrorFinding("integration.settings", "DRIVER_SETTINGS_INVALID", "integration settings are invalid"))
	}
	if _, err := ParseWorkloadOptions(node.DriverOptions[d.name]); err != nil {
		findings = append(findings, runtime.ErrorFinding("driverOptions."+d.name, "DRIVER_OPTION_INVALID", err.Error()))
	}
	if node.Source.Type != deployment.SourceOCIImage {
		findings = append(findings, runtime.ErrorFinding("source.type", "SOURCE_TYPE_UNSUPPORTED", "container nodes require OCI_IMAGE"))
	}
	if err := runtime.ValidateOCIDigest(node.Source.Ref); err != nil {
		findings = append(findings, runtime.ErrorFinding("source.ref", "SOURCE_NOT_DIGEST_PINNED", err.Error()))
	} else if !registryAllowed(settings.RegistryAllowlist, node.Source.Ref) {
		findings = append(findings, runtime.ErrorFinding("source.ref", "REGISTRY_NOT_ALLOWED", "image registry is not allowlisted by the integration"))
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
		if !d.supportsPublic {
			findings = append(findings, runtime.ErrorFinding(field, "PUBLIC_EXPOSURE_UNSUPPORTED", "this engine cannot publish allocated host ports; use PRIVATE endpoints"))
			continue
		}
		if strings.TrimSpace(target.Publication.PublicTCPHost) == "" {
			findings = append(findings, runtime.ErrorFinding(field, "PUBLICATION_NOT_CONFIGURED", "PUBLIC endpoints require an integration publication host"))
		} else if net.ParseIP(target.Publication.PublicTCPHost) == nil {
			findings = append(findings, runtime.ErrorFinding(field, "PUBLICATION_HOST_NOT_BINDABLE", "the container worker can only bind an IP publication host; FQDN publication needs an external ingress"))
		}
		if target.Publication.PublicPortFrom <= 0 || target.Publication.PublicPortTo < target.Publication.PublicPortFrom {
			findings = append(findings, runtime.ErrorFinding(field, "PUBLICATION_PORT_RANGE_INVALID", "a valid public port range is required"))
		}
	}
	if !settings.DedicatedNodeVerified {
		findings = append(findings, deployment.Finding{
			Path: "integration.settings", Code: "DEDICATED_NODE_NOT_VERIFIED", Severity: deployment.SeverityWarning,
			Message: "the worker refuses container workloads until an operator verifies a dedicated worker node",
		})
	}
	return findings
}

// Plan returns a reconciliation hint without mutating the provider.
func (d *Driver) Plan(_ context.Context, _ runtime.Target, desired deployment.Node, current *runtime.ProviderResource) (runtime.Plan, error) {
	plan := runtime.Plan{NodeKey: desired.Key}
	if current == nil || current.ProviderID == "" {
		plan.Action = "CREATE"
		return plan, nil
	}
	switch current.State {
	case runtime.NodeStateRunning:
		plan.Action = "NONE"
	case runtime.NodeStateDestroyed:
		plan.Action = "CREATE"
	default:
		plan.Action = "RECREATE"
	}
	return plan, nil
}

// Apply creates or adopts exactly the requested generation. A retry never
// creates a duplicate: only the container carrying the exact
// instance/node/generation labels is adopted; older owned generations are
// removed, and a future generation is refused.
func (d *Driver) Apply(ctx context.Context, target runtime.Target, desired deployment.Node, op runtime.Operation) (runtime.ProviderResource, error) {
	if err := runtime.ValidateTarget(target); err != nil {
		return runtime.ProviderResource{}, err
	}
	if target.Driver != d.name {
		return runtime.ProviderResource{}, &runtime.CapabilityError{Driver: d.name, Capability: "driver", Detail: target.Driver}
	}
	if !op.Kind.Valid() {
		return runtime.ProviderResource{}, errors.New("containerapi: invalid operation kind")
	}
	if op.Kind == runtime.OperationDestroy {
		return runtime.ProviderResource{}, errors.New("containerapi: DESTROY must use Destroy")
	}
	if findings := d.Validate(ctx, target, desired); hasErrors(findings) {
		return runtime.ProviderResource{}, &runtime.CapabilityError{Driver: d.name, Capability: "validation", Detail: findings[0].Code}
	}
	settings, err := decodeSettings(target)
	if err != nil {
		return runtime.ProviderResource{}, &runtime.CapabilityError{Driver: d.name, Capability: "settings", Detail: err.Error()}
	}
	// Local daemon isolation is not sufficient for untrusted student code unless
	// the operator has explicitly marked this worker dedicated.
	if !settings.DedicatedNodeVerified || strings.TrimSpace(settings.DedicatedNode) == "" {
		return runtime.ProviderResource{}, &runtime.CapabilityError{
			Driver: d.name, Capability: "dedicated_worker_node",
			Detail: "container workloads require a verified dedicated worker node",
		}
	}
	workload, err := ParseWorkloadOptions(desired.DriverOptions[d.name])
	if err != nil {
		return runtime.ProviderResource{}, &runtime.CapabilityError{Driver: d.name, Capability: "driverOptions", Detail: err.Error()}
	}
	instanceSettings, err := runtime.DecodeTargetSettings(target.Settings)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	client, err := d.client(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	if err := client.Ping(ctx); err != nil {
		return runtime.ProviderResource{}, err
	}

	networkName := runtime.SanitizeName("actfb", op.InstanceID.String())
	_, err = client.EnsureNetwork(ctx, NetworkSpec{
		Name:     networkName,
		Internal: true,
		Labels: map[string]string{
			runtime.LabelManaged:  "true",
			runtime.LabelInstance: op.InstanceID.String(),
			runtime.LabelDriver:   d.name,
			"actfb.kind":          "instance-network",
		},
	})
	if err != nil {
		return runtime.ProviderResource{}, err
	}

	existing, err := client.ListContainers(ctx, map[string]string{
		runtime.LabelInstance: op.InstanceID.String(),
		runtime.LabelNode:     op.NodeKey,
	})
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	var adopted *ContainerSummary
	for index := range existing {
		item := existing[index]
		if !runtime.Owns(item.Labels, op.InstanceID, op.NodeKey) {
			return runtime.ProviderResource{}, fmt.Errorf("%w: container %s", runtime.ErrOwnershipConflict, item.ID)
		}
		generation := runtime.Generation(item.Labels)
		if generation > op.Generation {
			return runtime.ProviderResource{}, fmt.Errorf("%w: generation %d", runtime.ErrStaleGeneration, generation)
		}
		if generation == op.Generation {
			adopted = &existing[index]
			continue
		}
		// A stale generation is owned platform garbage: remove it before creating
		// the requested generation. Removal is label-verified above.
		if item.Labels["actfb.role"] == "inject" {
			_ = client.RemoveContainer(ctx, item.ID, true)
			continue
		}
		if err := client.StopContainer(ctx, item.ID, 5); err != nil {
			return runtime.ProviderResource{}, err
		}
		if err := client.RemoveContainer(ctx, item.ID, true); err != nil {
			return runtime.ProviderResource{}, err
		}
		_ = client.RemoveVolume(ctx, volumeName(op, generation))
		d.forwarders.StopOwner(op.InstanceID, op.NodeKey)
	}

	containerID := ""
	if adopted != nil {
		containerID = adopted.ID
	} else {
		containerID, err = d.create(ctx, client, target, desired, workload, networkName, op)
		if err != nil {
			return runtime.ProviderResource{}, err
		}
	}

	state, err := client.InspectContainer(ctx, containerID)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	if !state.Running() {
		if state.Exited() {
			return runtime.ProviderResource{}, fmt.Errorf("containerapi: container exited during provisioning (code %d)", state.Detail.ExitCode)
		}
		if err := client.StartContainer(ctx, containerID); err != nil {
			return runtime.ProviderResource{}, err
		}
		state, err = d.waitRunning(ctx, client, containerID)
		if err != nil {
			return runtime.ProviderResource{}, err
		}
	}
	if !runtime.Owns(state.Labels, op.InstanceID, op.NodeKey) || runtime.Generation(state.Labels) != op.Generation {
		return runtime.ProviderResource{}, fmt.Errorf("%w: inspected container labels do not match", runtime.ErrOwnershipConflict)
	}

	endpoints, err := d.endpoints(ctx, client, target, desired, state, networkName, instanceSettings.Allocation, op)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	return runtime.ProviderResource{
		ProviderID: containerID,
		State:      runtime.NodeStateRunning,
		Endpoints:  endpoints,
		Message:    "container running",
	}, nil
}

func (d *Driver) create(ctx context.Context, client *Client, target runtime.Target, desired deployment.Node, workload WorkloadOptions, networkName string, op runtime.Operation) (string, error) {
	settings, _ := decodeSettings(target)
	instanceSettings, err := runtime.DecodeTargetSettings(target.Settings)
	if err != nil {
		return "", err
	}
	readOnly := true
	if workload.ReadOnlyRootFilesystem != nil {
		readOnly = *workload.ReadOnlyRootFilesystem
	}
	tmpfsSuffix := ""
	if workload.UserNumeric && workload.User != "" {
		uid := workload.User
		gid := workload.User
		if parts := strings.SplitN(workload.User, ":", 2); len(parts) == 2 {
			uid, gid = parts[0], parts[1]
		}
		tmpfsSuffix = ",uid=" + uid + ",gid=" + gid
	}
	tmpfs := map[string]string{}
	for targetPath, sizeMiB := range workload.Tmpfs {
		tmpfs[targetPath] = fmt.Sprintf("rw,noexec,nosuid,size=%dm%s", sizeMiB, tmpfsSuffix)
	}
	// A read-only root still needs a bounded writable scratch area.
	if readOnly {
		if _, exists := tmpfs["/tmp"]; !exists {
			scratch := desired.Resources.DiskMiB
			if scratch <= 0 {
				scratch = runtime.MinContainerDiskMiB
			}
			tmpfs["/tmp"] = fmt.Sprintf("rw,noexec,nosuid,size=%dm%s", scratch, tmpfsSuffix)
		}
	}
	pids := settings.PIDsLimit
	if pids == 0 {
		pids = 128
	}
	if pids < runtime.MinContainerPIDs {
		pids = runtime.MinContainerPIDs
	}
	env := []string{}
	if instanceSettings.Material != nil {
		names := make([]string, 0, len(instanceSettings.Material.Env))
		for name := range instanceSettings.Material.Env {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !validEnvName(name) {
				return "", &runtime.CapabilityError{Driver: d.name, Capability: "env", Detail: "invalid environment variable name"}
			}
			env = append(env, name+"="+instanceSettings.Material.Env[name])
		}
	}
	name := runtime.SanitizeName("actfb", op.InstanceID.String(), op.NodeKey, "g"+strconv.FormatInt(op.Generation, 10))
	labels := runtime.Labels(d.name, op)
	labels["actfb.endpoints"] = endpointLabel(desired.Endpoints)
	mounts, err := d.injectFiles(ctx, client, desired, instanceSettings.Material, name, networkName, op)
	if err != nil {
		return "", err
	}
	containerID, err := client.CreateContainer(ctx, ContainerSpec{
		Name:            name,
		Image:           desired.Source.Ref,
		Cmd:             workload.Cmd,
		Entrypoint:      workload.Entrypoint,
		Env:             env,
		Labels:          labels,
		User:            workload.User,
		WorkingDir:      workload.WorkingDir,
		Hostname:        runtime.SanitizeName("node", op.NodeKey),
		StopSignal:      workload.StopSignal,
		NetworkMode:     networkName,
		Mounts:          mounts,
		ReadOnlyRootFS:  readOnly,
		CPUMillicores:   desired.Resources.CPUMillicores,
		MemoryMiB:       desired.Resources.MemoryMiB,
		DiskMiB:         desired.Resources.DiskMiB,
		PIDs:            pids,
		Tmpfs:           tmpfs,
		SecurityOpts:    []string{"no-new-privileges=true"},
		CapDrop:         []string{"ALL"},
		NoNewPrivileges: true,
	})
	if err != nil {
		return "", err
	}
	return containerID, nil
}

// injectFiles materializes rendered files into a platform volume and returns
// read-only volume-subpath mounts. Host binds are never used. The helper
// container executes only the fixed `/bin/sh -c sleep` command from the
// digest-pinned workload image, never student code.
func (d *Driver) injectFiles(ctx context.Context, client *Client, desired deployment.Node, material *runtime.Material, name, networkName string, op runtime.Operation) ([]Mount, error) {
	if material == nil || len(material.Files) == 0 {
		return nil, nil
	}
	volume := volumeName(op, op.Generation)
	volumeLabels := runtime.Labels(d.name, op)
	volumeLabels["actfb.role"] = "inject-volume"
	if err := client.CreateVolume(ctx, volume, volumeLabels); err != nil {
		return nil, err
	}
	helperLabels := runtime.Labels(d.name, op)
	helperLabels["actfb.role"] = "inject"
	memory := desired.Resources.MemoryMiB
	if memory < 32 {
		memory = 32
	}
	helperID, err := client.CreateContainer(ctx, ContainerSpec{
		Name:            name + "-inject",
		Image:           desired.Source.Ref,
		Entrypoint:      []string{"/bin/sh"},
		Cmd:             []string{"-c", "sleep 30"},
		Labels:          helperLabels,
		NetworkMode:     networkName,
		ReadOnlyRootFS:  false,
		CPUMillicores:   100,
		MemoryMiB:       memory,
		DiskMiB:         runtime.MinContainerDiskMiB,
		PIDs:            32,
		Tmpfs:           map[string]string{"/tmp": "rw,noexec,nosuid,size=8m"},
		SecurityOpts:    []string{"no-new-privileges=true"},
		CapDrop:         []string{"ALL"},
		NoNewPrivileges: true,
		Mounts:          []Mount{{Type: "volume", Source: volume, Target: "/data"}},
	})
	if err != nil {
		_ = client.RemoveVolume(ctx, volume)
		return nil, err
	}
	if err := client.StartContainer(ctx, helperID); err != nil {
		_ = client.RemoveContainer(ctx, helperID, true)
		_ = client.RemoveVolume(ctx, volume)
		return nil, err
	}
	archiveFiles := make([]ArchiveFile, 0, len(material.Files))
	mounts := make([]Mount, 0, len(material.Files))
	for index, file := range material.Files {
		if !strings.HasPrefix(file.Target, "/") || strings.Contains(file.Target, "..") {
			_ = client.RemoveContainer(ctx, helperID, true)
			_ = client.RemoveVolume(ctx, volume)
			return nil, &runtime.CapabilityError{Driver: d.name, Capability: "files", Detail: "injection target must be an absolute path without traversal"}
		}
		mode := int64(0o644)
		if file.Mode != "" {
			parsed, parseErr := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(file.Mode, "0o"), "0"), 8, 32)
			if parseErr != nil {
				_ = client.RemoveContainer(ctx, helperID, true)
				_ = client.RemoveVolume(ctx, volume)
				return nil, &runtime.CapabilityError{Driver: d.name, Capability: "fileMode", Detail: "invalid file mode"}
			}
			mode = parsed
		}
		subpath := "files/" + strconv.Itoa(index)
		archiveFiles = append(archiveFiles, ArchiveFile{Name: subpath, Mode: mode, Content: file.Content})
		mounts = append(mounts, Mount{Type: "volume", Source: volume, Target: file.Target, Subpath: subpath, ReadOnly: true})
	}
	if err := client.UploadFiles(ctx, helperID, "/data", archiveFiles); err != nil {
		_ = client.RemoveContainer(ctx, helperID, true)
		_ = client.RemoveVolume(ctx, volume)
		return nil, err
	}
	_ = client.StopContainer(ctx, helperID, 1)
	if err := client.RemoveContainer(ctx, helperID, true); err != nil {
		return nil, err
	}
	return mounts, nil
}

func (d *Driver) waitRunning(ctx context.Context, client *Client, containerID string) (ContainerState, error) {
	deadline := time.Now().Add(15 * time.Second)
	for {
		state, err := client.InspectContainer(ctx, containerID)
		if err != nil {
			return state, err
		}
		if state.Running() {
			return state, nil
		}
		if state.Exited() {
			return state, fmt.Errorf("containerapi: container exited with code %d", state.Detail.ExitCode)
		}
		if time.Now().After(deadline) {
			return state, fmt.Errorf("%w: container did not reach RUNNING", runtime.ErrProviderUnavailable)
		}
		select {
		case <-ctx.Done():
			return state, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (d *Driver) endpoints(ctx context.Context, client *Client, target runtime.Target, desired deployment.Node, state ContainerState, networkName string, allocation *runtime.Allocation, op runtime.Operation) ([]runtime.ProviderEndpoint, error) {
	containerIP := state.IPOn(networkName)
	if containerIP == "" {
		return nil, fmt.Errorf("%w: container has no address on the instance network", runtime.ErrProviderUnavailable)
	}
	endpoints := make([]runtime.ProviderEndpoint, 0, len(desired.Endpoints))
	for _, endpoint := range desired.Endpoints {
		provider := runtime.ProviderEndpoint{
			Key:      endpoint.Key,
			Type:     endpoint.Protocol,
			Host:     containerIP,
			Port:     endpoint.TargetPort,
			Exposure: endpoint.Exposure,
		}
		if endpoint.Exposure == deployment.ExposurePublic {
			if allocation == nil {
				return nil, &runtime.CapabilityError{Driver: d.name, Capability: "publication", Detail: "core port allocation is missing"}
			}
			binding, ok := allocation.Ports[endpoint.Key]
			if !ok || binding.Port <= 0 || binding.Host == "" {
				return nil, &runtime.CapabilityError{Driver: d.name, Capability: "publication", Detail: "core port allocation is missing"}
			}
			if binding.Host != target.Publication.PublicTCPHost {
				return nil, &runtime.CapabilityError{Driver: d.name, Capability: "publication", Detail: "allocation host does not match the integration policy"}
			}
			if err := d.forwarders.Ensure(endpoint.Key, op, binding.Host, binding.Port, containerIP, endpoint.TargetPort); err != nil {
				return nil, err
			}
			provider.Host = binding.Host
			provider.Port = binding.Port
			if endpoint.Protocol == "http" || endpoint.Protocol == "https" {
				scheme := endpoint.Protocol
				if scheme == "http" && target.Publication.ForceHTTPS {
					scheme = "http"
				}
				raw := fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(binding.Host, strconv.Itoa(binding.Port)))
				provider.URL = &raw
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, provider)
	}
	_ = client
	return endpoints, nil
}

// Inspect returns the provider-observed state and re-establishes forwarders
// after a worker restart so a pre-existing RUNNING instance stays reachable.
func (d *Driver) Inspect(ctx context.Context, target runtime.Target, providerID string) (runtime.ProviderResource, error) {
	client, err := d.client(target)
	if err != nil {
		return runtime.ProviderResource{}, err
	}
	state, err := client.InspectContainer(ctx, providerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return runtime.ProviderResource{ProviderID: providerID, State: runtime.NodeStateDestroyed}, nil
		}
		return runtime.ProviderResource{}, err
	}
	resource := runtime.ProviderResource{ProviderID: providerID, State: runtime.NodeStateUnknown}
	if state.Running() {
		resource.State = runtime.NodeStateRunning
	} else if state.Exited() {
		resource.State = runtime.NodeStateError
		resource.Message = "container is not running"
	}
	instanceSettings, decodeErr := runtime.DecodeTargetSettings(target.Settings)
	if decodeErr == nil && instanceSettings.Allocation != nil {
		var endpointDescriptions []runtime.ProviderEndpoint
		if raw := state.Labels["actfb.endpoints"]; raw != "" {
			_ = json.Unmarshal([]byte(raw), &endpointDescriptions)
		}
		instanceID, parseErr := uuid.Parse(state.Labels[runtime.LabelInstance])
		if parseErr != nil {
			return resource, nil
		}
		nodeKey := state.Labels[runtime.LabelNode]
		generation := runtime.Generation(state.Labels)
		containerIP := state.IPOn(runtime.SanitizeName("actfb", instanceID.String()))
		for _, endpoint := range endpointDescriptions {
			binding, ok := instanceSettings.Allocation.Ports[endpoint.Key]
			if !ok || endpoint.Exposure != deployment.ExposurePublic {
				resource.Endpoints = append(resource.Endpoints, endpoint)
				continue
			}
			if containerIP != "" {
				_ = d.forwarders.Ensure(endpoint.Key, runtime.Operation{
					InstanceID: instanceID, NodeKey: nodeKey, Generation: generation, Kind: runtime.OperationCreate,
				}, binding.Host, binding.Port, containerIP, endpoint.Port)
			}
			endpoint.Host = binding.Host
			endpoint.Port = binding.Port
			resource.Endpoints = append(resource.Endpoints, endpoint)
		}
	}
	return resource, nil
}

// Destroy removes exactly the owned generation. A container with foreign
// labels is never deleted. Missing resources are success.
func (d *Driver) Destroy(ctx context.Context, target runtime.Target, providerID string, op runtime.Operation) error {
	client, err := d.client(target)
	if err != nil {
		return err
	}
	if strings.TrimSpace(providerID) == "" {
		d.forwarders.StopOwner(op.InstanceID, op.NodeKey)
		return nil
	}
	state, err := client.InspectContainer(ctx, providerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.forwarders.StopOwner(op.InstanceID, op.NodeKey)
			return nil
		}
		return err
	}
	if !runtime.Owns(state.Labels, op.InstanceID, op.NodeKey) {
		return fmt.Errorf("%w: refusing to destroy a container owned by another instance", runtime.ErrOwnershipConflict)
	}
	if err := client.StopContainer(ctx, providerID, 5); err != nil {
		return err
	}
	if err := client.RemoveContainer(ctx, providerID, true); err != nil {
		return err
	}
	// Provider confirmation happened above; only now are the platform volume and
	// publication sockets released.
	_ = client.RemoveVolume(ctx, volumeName(op, runtime.Generation(state.Labels)))
	d.forwarders.StopOwner(op.InstanceID, op.NodeKey)
	remaining, err := client.ListContainers(ctx, map[string]string{runtime.LabelInstance: op.InstanceID.String()})
	if err == nil && len(remaining) == 0 {
		_ = client.RemoveNetwork(ctx, runtime.SanitizeName("actfb", op.InstanceID.String()))
	}
	return nil
}

// Probe performs a real read-only daemon ping for the admin test endpoint.
func (d *Driver) Probe(ctx context.Context, target runtime.Target) error {
	client, err := d.client(target)
	if err != nil {
		return err
	}
	return client.Ping(ctx)
}

// ValidateSettings implements runtime.SettingsValidator.
func (d *Driver) ValidateSettings(raw json.RawMessage) []deployment.Finding {
	if _, err := ParseSettings(raw); err != nil {
		return []deployment.Finding{runtime.ErrorFinding("", "DRIVER_SETTINGS_INVALID", err.Error())}
	}
	return nil
}

// ValidateOptions implements runtime.OptionValidator for the template block.
func (d *Driver) ValidateOptions(raw json.RawMessage) []deployment.Finding {
	if _, err := ParseWorkloadOptions(raw); err != nil {
		return []deployment.Finding{runtime.ErrorFinding("", "DRIVER_OPTION_INVALID", err.Error())}
	}
	return nil
}

func (d *Driver) client(target runtime.Target) (*Client, error) {
	settings, err := decodeSettings(target)
	if err != nil {
		return nil, &runtime.CapabilityError{Driver: d.name, Capability: "settings", Detail: err.Error()}
	}
	if settings.Endpoint != "" {
		return NewUnix(d.name, settings.Endpoint)
	}
	socket := settings.Socket
	if socket == "" {
		if d.defaultSocket == nil {
			socket = "/var/run/docker.sock"
		} else {
			socket = d.defaultSocket("")
		}
	}
	socket = strings.TrimSpace(strings.TrimPrefix(socket, "unix://"))
	if socket == "" {
		return nil, &runtime.CapabilityError{Driver: d.name, Capability: "socket", Detail: "no daemon socket configured"}
	}
	if strings.Contains(socket, "://") || !strings.HasPrefix(socket, "/") {
		return nil, &runtime.CapabilityError{Driver: d.name, Capability: "socket", Detail: "invalid socket path"}
	}
	return NewUnix(d.name, socket)
}

func decodeSettings(target runtime.Target) (Settings, error) {
	settings, err := runtime.DecodeTargetSettings(target.Settings)
	if err != nil {
		return Settings{}, err
	}
	return ParseSettings(settings.DriverOptions())
}

func registryAllowed(allowlist []string, ref string) bool {
	if len(allowlist) == 0 {
		return false
	}
	reference := ref
	if at := strings.Index(reference, "@"); at >= 0 {
		reference = reference[:at]
	}
	host := reference
	if slash := strings.Index(reference, "/"); slash >= 0 {
		host = reference[:slash]
		if !strings.ContainsAny(host, ".:") {
			host = "docker.io"
		}
	}
	for _, allowed := range allowlist {
		if strings.EqualFold(strings.TrimSpace(allowed), host) {
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

func hasErrors(findings []deployment.Finding) bool {
	for _, finding := range findings {
		if finding.Severity == deployment.SeverityError {
			return true
		}
	}
	return false
}

// ForwarderRegistry owns the worker-local TCP publication sockets. It is
// process-local by design: the allocated host port is released only after the
// provider destroy is confirmed, and a worker restart re-establishes forwarders
// during Inspect/reconcile.
type ForwarderRegistry struct {
	mu    sync.Mutex
	items map[string]*forwarder
}

type forwarder struct {
	key      string
	owner    string
	listener net.Listener
	done     chan struct{}
}

// NewForwarderRegistry creates an empty registry.
func NewForwarderRegistry() *ForwarderRegistry {
	return &ForwarderRegistry{items: make(map[string]*forwarder)}
}

// Ensure binds a forwarder for the exact node/generation/key and returns
// promptly when the identical forwarder is already active.
func (r *ForwarderRegistry) Ensure(key string, op runtime.Operation, listenHost string, listenPort int, targetHost string, targetPort int) error {
	if listenHost == "" || listenPort <= 0 || listenPort > 65535 {
		return errors.New("containerapi: invalid publication allocation")
	}
	owner := op.InstanceID.String() + "/" + op.NodeKey
	internalKey := owner + "/" + key

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.items[internalKey]; ok {
		if existing.owner == owner {
			return nil
		}
		return fmt.Errorf("%w: publication port is owned by another instance", runtime.ErrOwnershipConflict)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(listenHost, strconv.Itoa(listenPort)))
	if err != nil {
		return fmt.Errorf("%w: cannot bind allocated publication port %d", runtime.ErrProviderUnavailable, listenPort)
	}
	item := &forwarder{key: internalKey, owner: owner, listener: listener, done: make(chan struct{})}
	r.items[internalKey] = item
	go r.serve(item, targetHost, targetPort)
	return nil
}

func (r *ForwarderRegistry) serve(item *forwarder, targetHost string, targetPort int) {
	defer close(item.done)
	for {
		connection, err := item.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer connection.Close()
			dialer := net.Dialer{Timeout: 5 * time.Second}
			upstream, dialErr := dialer.Dial("tcp", net.JoinHostPort(targetHost, strconv.Itoa(targetPort)))
			if dialErr != nil {
				return
			}
			defer upstream.Close()
			bridgeBidirectional(connection, upstream)
		}()
	}
}

// StopOwner closes every forwarder for one instance/node. It is called only
// after the provider destroy has been confirmed.
func (r *ForwarderRegistry) StopOwner(instanceID uuid.UUID, nodeKey string) {
	owner := instanceID.String() + "/" + nodeKey
	r.mu.Lock()
	var toStop []*forwarder
	for key, item := range r.items {
		if item.owner == owner {
			toStop = append(toStop, item)
			delete(r.items, key)
		}
	}
	r.mu.Unlock()
	for _, item := range toStop {
		_ = item.listener.Close()
		<-item.done
	}
}

// StopAll is used by the worker during graceful shutdown. It never affects the
// provider resources themselves; a later reconcile re-creates the listeners.
func (r *ForwarderRegistry) StopAll() {
	r.mu.Lock()
	items := make([]*forwarder, 0, len(r.items))
	for key, item := range r.items {
		items = append(items, item)
		delete(r.items, key)
	}
	r.mu.Unlock()
	for _, item := range items {
		_ = item.listener.Close()
		<-item.done
	}
}

// Active reports the number of active publication sockets (diagnostics/tests).
func (r *ForwarderRegistry) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

func bridgeBidirectional(left net.Conn, right net.Conn) {
	done := make(chan struct{}, 2)
	copyConn := func(dst, src net.Conn) {
		buffer := make([]byte, 32<<10)
		for {
			read, err := src.Read(buffer)
			if read > 0 {
				if _, writeErr := dst.Write(buffer[:read]); writeErr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go copyConn(left, right)
	go copyConn(right, left)
	<-done
	_ = left.Close()
	_ = right.Close()
	<-done
}

func volumeName(op runtime.Operation, generation int64) string {
	return runtime.SanitizeName("actfb", op.InstanceID.String(), op.NodeKey, "g"+strconv.FormatInt(generation, 10)) + "-vol"
}

func endpointLabel(endpoints []deployment.Endpoint) string {
	projected := make([]runtime.ProviderEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		projected = append(projected, runtime.ProviderEndpoint{
			Key: endpoint.Key, Type: endpoint.Protocol, Port: endpoint.TargetPort, Exposure: endpoint.Exposure,
		})
	}
	raw, _ := json.Marshal(projected)
	return string(raw)
}
