// Package deployment owns the provider-independent deployment template model,
// strict manifest parsing/validation, immutable version publication, preview and
// rendering.
//
// Import direction is frozen: internal/runtime and internal/instances import
// this package; this package must never import internal/runtime. Driver
// metadata/capability validation is supplied through the narrow interfaces in
// driver.go so the package has no dependency on an execution registry.
package deployment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Kind is the logical resource kind. It is deliberately independent from the
// execution driver (proxmox, kubevirt, docker, podman, kubernetes).
type Kind string

const (
	KindVM        Kind = "VM"
	KindContainer Kind = "CONTAINER"
)

// SourceType is the provider-independent origin of a node's root resource.
type SourceType string

const (
	SourceOCIImage   SourceType = "OCI_IMAGE"
	SourceVMImage    SourceType = "VM_IMAGE"
	SourceVMTemplate SourceType = "VM_TEMPLATE"
)

// Exposure controls whether an endpoint is published outside the instance
// network. Public host/port allocation is never part of a template.
type Exposure string

const (
	ExposurePublic  Exposure = "PUBLIC"
	ExposurePrivate Exposure = "PRIVATE"
)

// Lifecycle reset strategies. SNAPSHOT is only accepted when the selected
// driver metadata advertises SnapshotReset.
const (
	ResetRecreate = "RECREATE"
	ResetSnapshot = "SNAPSHOT"
)

// Canonical manifest envelope values.
const (
	APIVersion = "actfb.dev/v1alpha1"
	KindName   = "DeploymentTemplate"
)

// Bounded policy. These limits are intentionally enforced before YAML/JSON
// decoding: 64 KiB manifest, 1 MiB per package file, 16 MiB total package,
// 256 files, 20 nodes, 64 parameters and 64 generated values.
const (
	MaxManifestBytes = 64 * 1024
	MaxFileBytes     = 1 << 20
	MaxPackageBytes  = 16 << 20
	MaxFiles         = 256
	MaxNodes         = 20
	MaxParameters    = 64
	MaxGenerated     = 64
	MaxEnvPerNode    = 128
	MaxFilesPerNode  = 64
	MaxEndpoints     = 32
	MaxChecks        = 64
	MaxPathLength    = 255
	MaxStringLength  = 20000

	MinGeneratedLength = 8
	MaxGeneratedLength = 128

	// Shared resource bounds, expressed in the units carried by Resources.
	MinCPUMillicores = 1
	MaxCPUMillicores = 128_000
	MinMemoryMiB     = 8
	MaxMemoryMiB     = 1_048_576
	MinDiskMiB       = 1
	MaxDiskMiB       = 10_485_760

	// Render bounds. Recursion is additionally interrupted by cancellation and
	// by this output budget.
	MaxRenderBytes = MaxFileBytes
	MaxRenderTotal = MaxPackageBytes
)

// Metadata is the user-facing identity of a deployment template.
type Metadata struct {
	Name        string `json:"name" yaml:"name"`
	Slug        string `json:"slug" yaml:"slug"`
	Description string `json:"description" yaml:"description"`
}

// Parameter is a declared template input. Default is stored in canonical
// string form; YAML booleans and integers are converted on parse.
type Parameter struct {
	Key      string   `json:"key" yaml:"key"`
	Label    string   `json:"label" yaml:"label"`
	Type     string   `json:"type" yaml:"type"`
	Options  []string `json:"options,omitempty" yaml:"options,omitempty"`
	Default  string   `json:"default,omitempty" yaml:"default,omitempty"`
	Required bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Secret   bool     `json:"secret,omitempty" yaml:"secret,omitempty"`
}

// GeneratedValue describes a value generated per instance, never per template.
type GeneratedValue struct {
	Key           string `json:"key" yaml:"key"`
	Kind          string `json:"kind" yaml:"kind"`
	Length        int    `json:"length" yaml:"length"`
	RotateOnReset bool   `json:"rotateOnReset,omitempty" yaml:"rotateOnReset,omitempty"`
}

// Source declares what to instantiate without any native provider ID.
type Source struct {
	Type SourceType `json:"type" yaml:"type"`
	Ref  string     `json:"ref" yaml:"ref"`
}

// Resources are common, unit-explicit node resources.
type Resources struct {
	CPUMillicores int `json:"cpuMillicores" yaml:"cpuMillicores"`
	MemoryMiB     int `json:"memoryMiB" yaml:"memoryMiB"`
	DiskMiB       int `json:"diskMiB" yaml:"diskMiB"`
}

// EnvVar is a container environment entry.
type EnvVar struct {
	Name      string    `json:"name" yaml:"name"`
	ValueFrom EnvSource `json:"valueFrom" yaml:"valueFrom"`
}

// EnvSource is a one-of generated/parameter/literal reference.
type EnvSource struct {
	Generated *string `json:"generated,omitempty" yaml:"generated,omitempty"`
	Parameter *string `json:"parameter,omitempty" yaml:"parameter,omitempty"`
	Literal   *string `json:"literal,omitempty" yaml:"literal,omitempty"`
}

// FileSpec injects a package file into a node.
type FileSpec struct {
	Source string `json:"source" yaml:"source"`
	Target string `json:"target" yaml:"target"`
	Mode   string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

// Endpoint is a provider-independent service port. Host/port allocation is not
// part of the template.
type Endpoint struct {
	Key        string   `json:"key" yaml:"key"`
	Protocol   string   `json:"protocol" yaml:"protocol"`
	TargetPort int      `json:"targetPort" yaml:"targetPort"`
	Exposure   Exposure `json:"exposure" yaml:"exposure"`
	Label      string   `json:"label,omitempty" yaml:"label,omitempty"`
}

// Bootstrap is VM-only cloud-init bootstrap input.
type Bootstrap struct {
	CloudInitFile string `json:"cloudInitFile" yaml:"cloudInitFile"`
}

// Check describes a post-provision readiness check.
type Check struct {
	Kind     string `json:"kind" yaml:"kind"`
	Node     string `json:"node" yaml:"node"`
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
}

// Lifecycle carries common lifecycle semantics.
type Lifecycle struct {
	Reset string `json:"reset,omitempty" yaml:"reset,omitempty"`
}

// Node is one logical VM or container. DriverOptions is namespaced by driver;
// only the block for the selected integration driver may ever be forwarded.
type Node struct {
	Key           string                     `json:"key" yaml:"key"`
	Name          string                     `json:"name" yaml:"name"`
	Kind          Kind                       `json:"kind" yaml:"kind"`
	Source        Source                     `json:"source" yaml:"source"`
	Resources     Resources                  `json:"resources" yaml:"resources"`
	Env           []EnvVar                   `json:"env,omitempty" yaml:"env,omitempty"`
	Bootstrap     *Bootstrap                 `json:"bootstrap,omitempty" yaml:"bootstrap,omitempty"`
	Files         []FileSpec                 `json:"files,omitempty" yaml:"files,omitempty"`
	Endpoints     []Endpoint                 `json:"endpoints,omitempty" yaml:"endpoints,omitempty"`
	DriverOptions map[string]json.RawMessage `json:"driverOptions,omitempty" yaml:"driverOptions,omitempty"`
}

// Template is the normalized manifest model persisted in PostgreSQL. Secret
// parameter values, provider credentials and instance runtime IDs are never
// fields of Template.
type Template struct {
	APIVersion string           `json:"apiVersion" yaml:"apiVersion"`
	Kind       string           `json:"kind" yaml:"kind"`
	Metadata   Metadata         `json:"metadata" yaml:"metadata"`
	Parameters []Parameter      `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Generated  []GeneratedValue `json:"generated,omitempty" yaml:"generated,omitempty"`
	Nodes      []Node           `json:"nodes" yaml:"nodes"`
	Checks     []Check          `json:"checks,omitempty" yaml:"checks,omitempty"`
	Lifecycle  Lifecycle        `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`
}

// TemplateVersion is an immutable published snapshot.
type TemplateVersion struct {
	UUID        uuid.UUID
	TemplateID  uuid.UUID
	Version     int
	Digest      string
	Manifest    Template
	Files       map[string][]byte
	PublishedAt time.Time
	PublishedBy uuid.UUID
}

// Binding is the approved 009 contract. The template service reads/writes
// versions only; binding lifecycle belongs to the instances orchestrator.
type Binding struct {
	UUID              uuid.UUID
	BoardID           uuid.UUID
	ModuleID          uuid.UUID
	TaskID            *uuid.UUID
	TemplateVersionID uuid.UUID
	Targets           map[Kind]uuid.UUID
	Parameters        map[string]string
	Lifecycle         Lifecycle
}

// Subject is the instance owner subject.
type Subject struct {
	Type string `json:"type"`
	ID   uuid.UUID
}

// DesiredNode is a node with its selected integration.
type DesiredNode struct {
	Node
	IntegrationUUID uuid.UUID
}

// DesiredDeployment is the provider-independent request handed to the runtime
// orchestrator after a binding is authorized and parameters are rendered.
type DesiredDeployment struct {
	BindingID  uuid.UUID
	InstanceID uuid.UUID
	BoardID    uuid.UUID
	ModuleID   uuid.UUID
	Subject    Subject
	Nodes      []DesiredNode
	ExpiresAt  time.Time
}

// Capabilities advertises what a driver can execute. Missing capabilities are
// validation errors for templates that require them; they are never ignored.
type Capabilities struct {
	Kind                Kind
	SourceTypes         []SourceType
	CloudInit           bool
	FileInjection       bool
	PublicExposure      bool
	SnapshotReset       bool
	PersistentVolumes   bool
	MaxNodes            int
	DriverOptionsSchema json.RawMessage
}

// InstancePublicationPolicy is the only source of public host/port ranges.
// It is read from the trusted integration, never from a user manifest.
type InstancePublicationPolicy struct {
	PublicTCPHost  string `json:"publicTcpHost"`
	PublicPortFrom int    `json:"publicPortFrom"`
	PublicPortTo   int    `json:"publicPortTo"`
	HTTPBaseDomain string `json:"httpBaseDomain"`
	ForceHTTPS     bool   `json:"forceHttps"`
}

// Finding is one validation result. Path is a stable field path, never raw
// manifest content. Severity is "error" or "warning".
type Finding struct {
	Path     string `json:"path" yaml:"path"`
	Code     string `json:"code" yaml:"code"`
	Message  string `json:"message" yaml:"message"`
	Severity string `json:"severity" yaml:"severity"`
}

const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// ValidationResult is returned by Validate.
type ValidationResult struct {
	Valid    bool      `json:"valid"`
	Findings []Finding `json:"findings"`
}

// HasErrors reports whether at least one finding blocks publication.
func (r ValidationResult) HasErrors() bool {
	for _, finding := range r.Findings {
		if finding.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Validator is the provider-independent contract; capabilities are keyed by
// kind and come from trusted driver metadata.
type Validator interface {
	Validate(ctx context.Context, version TemplateVersion, capabilities map[Kind]Capabilities) (ValidationResult, error)
}
