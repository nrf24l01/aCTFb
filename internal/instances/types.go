// Package instances owns the instance lifecycle, provider-independent desired
// snapshots, network/endpoint leases and the trusted orchestration worker. It
// imports both internal/deployment and internal/runtime; neither imports it.
package instances

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

// Scope is the instance parent resource.
type Scope string

const (
	ScopeTask    Scope = "TASK"
	ScopePentest Scope = "PENTEST"
)

// Status is the frozen six-value instance status enum.
type Status string

const (
	StatusProvisioning Status = "PROVISIONING"
	StatusRunning      Status = "RUNNING"
	StatusResetting    Status = "RESETTING"
	StatusDestroying   Status = "DESTROYING"
	StatusError        Status = "ERROR"
	StatusExpired      Status = "EXPIRED"
)

// ValidStatus reports whether the value is one of the six frozen statuses.
func ValidStatus(status Status) bool {
	switch status {
	case StatusProvisioning, StatusRunning, StatusResetting, StatusDestroying, StatusError, StatusExpired:
		return true
	default:
		return false
	}
}

// Action is a participant lifecycle action. EXTEND is not an OperationKind: it
// only changes the TTL and is never sent to a provider.
type Action string

const (
	ActionReset   Action = "RESET"
	ActionDestroy Action = "DESTROY"
	ActionExtend  Action = "EXTEND"
)

// Instance is the participant/admin read model.
type Instance struct {
	UUID           uuid.UUID
	BoardID        uuid.UUID
	ModuleID       uuid.UUID
	Scope          Scope
	ScopeUUID      uuid.UUID
	SubjectType    string
	SubjectID      uuid.UUID
	SubjectName    string
	Status         Status
	CreatedAt      time.Time
	StartedAt      *time.Time
	ExpiresAt      *time.Time
	ExtensionCount int64
	Generation     int64
	BindingID      *uuid.UUID
	VersionID      *uuid.UUID
	RemovedAt      *time.Time
	Nodes          []Node
}

// RemainingSeconds is clamped at zero.
func (i Instance) RemainingSeconds(now time.Time) int64 {
	if i.ExpiresAt == nil {
		return 0
	}
	remaining := i.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	return int64(remaining / time.Second)
}

// Node is one instance node with confirmed endpoints only.
type Node struct {
	ID           uuid.UUID
	Key          string
	Name         string
	Kind         deployment.Kind
	Status       string
	ProviderID   string
	Generation   int64
	Endpoints    []Endpoint
	ErrorMessage string
}

// Endpoint is a core-leased access endpoint.
type Endpoint struct {
	Key           string
	Type          string
	Host          string
	Port          int
	URL           *string
	Exposure      deployment.Exposure
	AllocatedPort *int
	ReleasedAt    *time.Time
}

// DesiredSnapshot is the secret-free immutable snapshot stored on
// instances.desired_snapshot at queue time and copied onto every operation.
// Secret values are referenced by name only; actual values live in
// instance_secret_values (sealed).
type DesiredSnapshot struct {
	Version             int               `json:"version"`
	TemplateVersionID   uuid.UUID         `json:"templateVersionId"`
	TemplateDigest      string            `json:"templateDigest"`
	BindingID           uuid.UUID         `json:"bindingId"`
	Parameters          map[string]string `json:"parameters,omitempty"`
	SecretParameterKeys []string          `json:"secretParameterKeys,omitempty"`
	Generated           []GeneratedRef    `json:"generated,omitempty"`
	Nodes               []SnapshotNode    `json:"nodes"`
	TTLSeconds          int64             `json:"ttlSeconds,omitempty"`
	RequestHash         string            `json:"requestHash,omitempty"`
}

// GeneratedRef references a generated value by key and rotation policy.
type GeneratedRef struct {
	Key           string `json:"key"`
	Kind          string `json:"kind"`
	Length        int    `json:"length"`
	RotateOnReset bool   `json:"rotateOnReset,omitempty"`
}

// SnapshotNode is the provider-independent node plan.
type SnapshotNode struct {
	Key             string            `json:"key"`
	Kind            deployment.Kind   `json:"kind"`
	Source          deployment.Source `json:"source"`
	IntegrationUUID uuid.UUID         `json:"integrationId"`
	Public          bool              `json:"public"`
}

// AuthorizedContext is the worker's authoritative re-check input. It is stored
// on every operation and never taken from the NATS message.
type AuthorizedContext struct {
	ActorUserID    uuid.UUID  `json:"actorUserId"`
	BoardID        uuid.UUID  `json:"boardId"`
	ModuleID       uuid.UUID  `json:"moduleId"`
	Scope          Scope      `json:"scope"`
	ScopeUUID      uuid.UUID  `json:"scopeUuid"`
	SubjectType    string     `json:"subjectType"`
	SubjectID      uuid.UUID  `json:"subjectId"`
	Permission     string     `json:"permission"`
	EffectiveUntil *time.Time `json:"effectiveUntil,omitempty"`
	System         bool       `json:"system,omitempty"`
}

// Operation is the persistent worker unit.
type Operation struct {
	ID             uuid.UUID
	InstanceID     uuid.UUID
	NodeKey        string
	Kind           runtime.OperationKind
	Generation     int64
	Status         string
	IdempotencyKey string
}

// Service options and shared dependencies are documented on Service.

// RenderInputProvider resolves the immutable template + trusted secrets at
// apply time. It is implemented by the PostgreSQL store.
type RenderInputProvider interface {
	RenderInput(ctx context.Context, snapshot DesiredSnapshot, instanceID uuid.UUID, generation int64, reset bool) (deployment.RenderInput, map[string]string, error)
}

// SecretStore persists generated values sealed.
type SecretStore interface {
	LoadGenerated(ctx context.Context, instanceID uuid.UUID, nodeKey string) (map[string]string, error)
	SaveGenerated(ctx context.Context, instanceID uuid.UUID, nodeKey string, values map[string]string) error
}

// Job is the NATS payload: ids only, never credentials or roles.
type Job struct {
	InstanceID   uuid.UUID   `json:"instanceId"`
	Generation   int64       `json:"generation"`
	OperationIDs []uuid.UUID `json:"operationIds"`
	Reason       string      `json:"reason,omitempty"`
}

// MarshalJob encodes the bounded job.
func MarshalJob(job Job) ([]byte, error) {
	if len(job.OperationIDs) == 0 || len(job.OperationIDs) > 64 {
		return nil, &Error{Code: CodeValidationFailed, Message: "invalid job"}
	}
	return json.Marshal(job)
}
