// Package runtime owns the trusted execution driver contract and the real
// provider adapters (docker, podman, kubernetes, proxmox, kubevirt).
//
// Import direction is frozen: this package imports internal/deployment;
// internal/deployment must never import internal/runtime. Drivers never receive
// Board/User/Score/ACL data; they receive only a deployment.Node plus a
// provider Target whose credentials were decrypted inside the trusted worker.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
)

// OperationKind is one node-level lifecycle step.
type OperationKind string

const (
	OperationCreate  OperationKind = "CREATE"
	OperationReset   OperationKind = "RESET"
	OperationDestroy OperationKind = "DESTROY"
)

// Valid reports whether the operation kind is one of the frozen values.
func (k OperationKind) Valid() bool {
	switch k {
	case OperationCreate, OperationReset, OperationDestroy:
		return true
	default:
		return false
	}
}

// Operation identifies one idempotent provider mutation. Generation is monotonic
// per node and every materialized provider object carries it as a label, so a
// retry or a post-crash worker adopts only the exact owner/generation and never
// touches another instance's resources.
type Operation struct {
	ID         uuid.UUID
	InstanceID uuid.UUID
	NodeKey    string
	Generation int64
	Kind       OperationKind
}

// Target is the trusted provider selection for one node. Credentials are
// decrypted only in the trusted worker; Settings is core-composed JSON, never
// client JSON (see TargetSettings).
type Target struct {
	IntegrationUUID uuid.UUID
	Driver          string
	Kind            deployment.Kind
	BaseURL         string
	Credentials     map[string]string
	Settings        json.RawMessage
	Publication     deployment.InstancePublicationPolicy
}

// DriverName is the integration driver name without the raw settings.
func (t Target) DriverName() string { return t.Driver }

// NodeState is the provider-observed state of one node.
type NodeState string

const (
	NodeStateUnknown      NodeState = "UNKNOWN"
	NodeStateProvisioning NodeState = "PROVISIONING"
	NodeStateRunning      NodeState = "RUNNING"
	NodeStateError        NodeState = "ERROR"
	NodeStateDestroyed    NodeState = "DESTROYED"
)

// ProviderEndpoint is a provider-observed access endpoint. Host/Port always
// refer to the provider network; the orchestrator is the only component allowed
// to translate a PUBLIC endpoint into a host-published lease.
type ProviderEndpoint struct {
	Key      string
	Type     string
	Host     string
	Port     int
	URL      *string
	Exposure deployment.Exposure
}

// ProviderResource is the provider-confirmed inventory for one node.
type ProviderResource struct {
	ProviderID string
	State      NodeState
	Endpoints  []ProviderEndpoint
	Message    string
}

// Plan is a non-mutating reconciliation hint.
type Plan struct {
	NodeKey      string
	Action       string // CREATE, UPDATE, RECREATE, DELETE, NONE
	ProviderHint string
	Findings     []deployment.Finding
}

// Driver is the exact frozen execution contract.
type Driver interface {
	Name() string
	Kind() deployment.Kind
	Capabilities(ctx context.Context, target Target) (deployment.Capabilities, error)
	Validate(ctx context.Context, target Target, node deployment.Node) []deployment.Finding
	Plan(ctx context.Context, target Target, desired deployment.Node, current *ProviderResource) (Plan, error)
	Apply(ctx context.Context, target Target, desired deployment.Node, op Operation) (ProviderResource, error)
	Inspect(ctx context.Context, target Target, providerID string) (ProviderResource, error)
	Destroy(ctx context.Context, target Target, providerID string, op Operation) error
}

// Probe is an optional driver extension for the integration "test connection"
// button. It performs a read-only provider call and never provisions anything.
type Probe interface {
	Probe(ctx context.Context, target Target) error
}

// Registry is the trusted driver registry.
type Registry interface {
	Register(driver Driver) error
	Get(name string) (Driver, bool)
	List() []Driver
}

// -----------------------------------------------------------------------------
// Stable provider labels
// -----------------------------------------------------------------------------

const (
	// LabelManaged marks every object the platform created.
	LabelManaged = "actfb.managed"
	// LabelInstance, LabelNode, LabelGeneration and LabelOperation are the exact
	// owner labels. Adoption requires managed=true plus an exact instance/node
	// match; a generation that is not the requested one is cleaned up, never
	// adopted, and a higher (future) generation is never touched.
	LabelInstance   = "actfb.instance"
	LabelNode       = "actfb.node"
	LabelGeneration = "actfb.generation"
	LabelOperation  = "actfb.operation"
	LabelDriver     = "actfb.driver"
)

// Labels returns the exact label set for a materialized node resource.
func Labels(driver string, op Operation) map[string]string {
	return map[string]string{
		LabelManaged:    "true",
		LabelInstance:   op.InstanceID.String(),
		LabelNode:       op.NodeKey,
		LabelGeneration: fmt.Sprintf("%d", op.Generation),
		LabelOperation:  string(op.Kind),
		LabelDriver:     driver,
	}
}

// Owns reports whether the label set belongs to exactly this instance/node.
// The generation is deliberately not part of ownership: a stale generation must
// be discoverable so it can be cleaned up.
func Owns(labels map[string]string, instanceID uuid.UUID, nodeKey string) bool {
	if labels == nil {
		return false
	}
	if labels[LabelManaged] != "true" {
		return false
	}
	if labels[LabelInstance] != instanceID.String() {
		return false
	}
	return labels[LabelNode] == nodeKey
}

// Generation parses the actfb.generation label. Zero means "unlabelled/legacy"
// and is never adopted.
func Generation(labels map[string]string) int64 {
	if labels == nil {
		return 0
	}
	var value int64
	if _, err := fmt.Sscanf(labels[LabelGeneration], "%d", &value); err != nil {
		return 0
	}
	return value
}

// -----------------------------------------------------------------------------
// Trusted settings envelope and core-issued allocation
// -----------------------------------------------------------------------------

// NetworkClaim is the trusted allocation claim copied from
// instance_network_leases.claim by the orchestrator, never from a manifest.
type NetworkClaim struct {
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	Bridge    string `json:"bridge,omitempty"`
	CIDR      string `json:"cidr,omitempty"`
	VLAN      int    `json:"vlan,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// NetworkPool is the administrator-supplied trusted network allocation pool.
// A client manifest can never select a bridge, VLAN or namespace.
type NetworkPool struct {
	Kind      string `json:"kind,omitempty"`
	Bridge    string `json:"bridge,omitempty"`
	CIDR      string `json:"cidr,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	VLANFrom  int    `json:"vlanFrom,omitempty"`
	VLANTo    int    `json:"vlanTo,omitempty"`
}

// PortBinding is one core-issued public port lease.
type PortBinding struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Allocation is the core-owned network/endpoint allocation for one instance.
// Public ports are chosen by the orchestrator from the integration publication
// policy and persisted in instance_endpoints before the provider is called.
type Allocation struct {
	Network NetworkClaim           `json:"network"`
	Ports   map[string]PortBinding `json:"ports,omitempty"`
}

// Material is the rendered, secret-bearing node input produced by the trusted
// worker from the immutable template version. It is never stored in
// desired_snapshot (which keeps secret references instead of values).
type Material struct {
	Env       map[string]string `json:"env,omitempty"`
	Files     []MaterialFile    `json:"files,omitempty"`
	Bootstrap []byte            `json:"bootstrap,omitempty"`
}

// MaterialFile is one rendered injectable file.
type MaterialFile struct {
	Target  string `json:"target"`
	Mode    string `json:"mode,omitempty"`
	Content []byte `json:"content"`
}

// TargetSettings is the decoded content of Target.Settings. Options is the
// typed integration settings block selected by the administrator; Allocation
// and Material are core-owned values injected by the trusted worker.
type TargetSettings struct {
	Options    json.RawMessage `json:"options,omitempty"`
	Allocation *Allocation     `json:"allocation,omitempty"`
	Material   *Material       `json:"material,omitempty"`
}

// DecodeTargetSettings strictly decodes the trusted envelope. Unknown envelope
// fields are rejected so a client cannot smuggle provider options through a
// field the orchestrator does not understand.
func DecodeTargetSettings(raw json.RawMessage) (TargetSettings, error) {
	var settings TargetSettings
	if len(raw) == 0 {
		return settings, nil
	}
	if err := StrictUnmarshal(raw, &settings); err != nil {
		return TargetSettings{}, fmt.Errorf("runtime: invalid target settings: %w", err)
	}
	return settings, nil
}

// EncodeTargetSettings marshals the trusted envelope.
func EncodeTargetSettings(settings TargetSettings) (json.RawMessage, error) {
	return json.Marshal(settings)
}

// DriverOptions returns the integration option block for a driver.
func (s TargetSettings) DriverOptions() json.RawMessage {
	if len(s.Options) == 0 {
		return json.RawMessage(`{}`)
	}
	return s.Options
}

// StrictUnmarshal decodes JSON while rejecting unknown fields and trailing
// values. It is the only decoder used for provider options.
func StrictUnmarshal(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

// -----------------------------------------------------------------------------
// Errors
// -----------------------------------------------------------------------------

// ErrUnsupported is returned (wrapped) when a driver cannot satisfy a requested
// capability. Callers must map it to a deterministic validation/unsupported
// error instead of silently ignoring the request.
var ErrUnsupported = errors.New("runtime: unsupported capability")

// ErrProviderUnavailable is a safe "provider unreachable" signal.
var ErrProviderUnavailable = errors.New("runtime: provider unavailable")

// ErrProviderNotFound means the provider object does not exist.
var ErrProviderNotFound = errors.New("runtime: provider resource not found")

// ErrOwnershipConflict means a resource with the exact name exists but is not
// owned by this instance/node. It is never adopted or deleted.
var ErrOwnershipConflict = errors.New("runtime: provider resource ownership conflict")

// ErrStaleGeneration means a newer generation already exists.
var ErrStaleGeneration = errors.New("runtime: provider generation is newer than requested")

// CapabilityError is a deterministic unsupported-capability error.
type CapabilityError struct {
	Driver     string
	Capability string
	Detail     string
}

func (e *CapabilityError) Error() string {
	message := "driver " + e.Driver + " does not support " + e.Capability
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

func (e *CapabilityError) Unwrap() error { return ErrUnsupported }

// ProviderError is a sanitized provider failure. Body text, credentials and
// URLs with query secrets are never included.
type ProviderError struct {
	Driver     string
	Operation  string
	StatusCode int
	Code       string
}

func (e *ProviderError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("runtime: %s %s failed (%s)", e.Driver, e.Operation, e.Code)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("runtime: %s %s failed with HTTP %d", e.Driver, e.Operation, e.StatusCode)
	}
	return fmt.Sprintf("runtime: %s %s failed", e.Driver, e.Operation)
}

func (e *ProviderError) Unwrap() error { return ErrProviderUnavailable }
