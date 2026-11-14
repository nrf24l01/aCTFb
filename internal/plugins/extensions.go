package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// ExtensionAPIVersion is the versioned contract for installable extension
// points. It deliberately describes only HTTP v1 operations and typed Go
// interfaces; the core never imports plugin code.
const ExtensionAPIVersion = "http-v1"

// ExtensionType is one of the controlled installable extension surfaces.
type ExtensionType string

const (
	ExtensionChecker     ExtensionType = "checker"
	ExtensionRuntime     ExtensionType = "runtime"
	ExtensionModule      ExtensionType = "module"
	ExtensionIntegration ExtensionType = "integration"
)

// ExtensionOperation is one declared HTTP v1 operation of an extension.
type ExtensionOperation struct {
	Name           string `json:"name"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	RequestSchema  string `json:"requestSchema,omitempty"`
	ResponseSchema string `json:"responseSchema,omitempty"`
}

// ExtensionDefinition is the trusted registry entry for a plugin extension.
// It grants nothing; routes still require the normal plugin permission grant.
type ExtensionDefinition struct {
	Type         ExtensionType              `json:"type"`
	Key          string                     `json:"key"`
	APIVersion   string                     `json:"apiVersion"`
	Operations   []ExtensionOperation       `json:"operations"`
	Permissions  []authorization.Permission `json:"permissions,omitempty"`
	Capabilities []Capability               `json:"capabilities,omitempty"`
}

// ExtensionRegistry is the central trusted catalog for checker/runtime/module/
// integration extension definitions. Only the plugin manager or trusted boot
// code registers definitions after manifest validation.
type ExtensionRegistry interface {
	Register(def ExtensionDefinition) error
	Lookup(kind ExtensionType, key string) (ExtensionDefinition, bool)
	List() []ExtensionDefinition
}

var extensionKeyPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)+$`)

// ExtensionCatalog is the thread-safe ExtensionRegistry implementation.
type ExtensionCatalog struct {
	mu   sync.RWMutex
	defs map[string]ExtensionDefinition
}

// NewExtensionRegistry returns an empty trusted extension catalog.
func NewExtensionRegistry() *ExtensionCatalog {
	return &ExtensionCatalog{defs: make(map[string]ExtensionDefinition)}
}

var _ ExtensionRegistry = (*ExtensionCatalog)(nil)

// Register validates and stores one extension definition. Duplicate or
// conflicting keys are rejected; an identical reload is idempotent.
func (c *ExtensionCatalog) Register(def ExtensionDefinition) error {
	if c == nil {
		return errors.New("plugins: nil extension registry")
	}
	if def.APIVersion == "" {
		def.APIVersion = ExtensionAPIVersion
	}
	if def.APIVersion != ExtensionAPIVersion {
		return fmt.Errorf("plugins: unsupported extension apiVersion %q", def.APIVersion)
	}
	switch def.Type {
	case ExtensionChecker, ExtensionRuntime, ExtensionModule, ExtensionIntegration:
	default:
		return fmt.Errorf("plugins: invalid extension type %q", def.Type)
	}
	if !extensionKeyPattern.MatchString(def.Key) {
		return fmt.Errorf("plugins: extension key %q must be namespaced", def.Key)
	}
	operations := make([]ExtensionOperation, 0, len(def.Operations))
	seen := make(map[string]struct{}, len(def.Operations))
	for _, operation := range def.Operations {
		if strings.TrimSpace(operation.Name) == "" || len(operation.Name) > 128 {
			return errors.New("plugins: extension operation name is required")
		}
		switch operation.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return fmt.Errorf("plugins: extension operation %s has invalid method", operation.Name)
		}
		if err := validateRoutePath(operation.Path); err != nil {
			return fmt.Errorf("plugins: extension operation %s: %w", operation.Name, err)
		}
		key := operation.Method + " " + operation.Path
		if _, exists := seen[key]; exists {
			return fmt.Errorf("plugins: duplicate extension operation %s", key)
		}
		seen[key] = struct{}{}
		operations = append(operations, operation)
	}
	def.Operations = operations
	def.Permissions = append([]authorization.Permission(nil), def.Permissions...)
	def.Capabilities = append([]Capability(nil), def.Capabilities...)

	c.mu.Lock()
	defer c.mu.Unlock()
	key := extensionCatalogKey(def.Type, def.Key)
	if existing, ok := c.defs[key]; ok {
		if existing.APIVersion == def.APIVersion && equalOperations(existing.Operations, def.Operations) {
			return nil
		}
		return fmt.Errorf("plugins: extension %s/%s is already registered", def.Type, def.Key)
	}
	c.defs[key] = def
	return nil
}

// Lookup returns one definition.
func (c *ExtensionCatalog) Lookup(kind ExtensionType, key string) (ExtensionDefinition, bool) {
	if c == nil {
		return ExtensionDefinition{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	def, ok := c.defs[extensionCatalogKey(kind, key)]
	return def, ok
}

// List returns a deterministic copy.
func (c *ExtensionCatalog) List() []ExtensionDefinition {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]ExtensionDefinition, 0, len(c.defs))
	for _, def := range c.defs {
		result = append(result, def)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type == result[j].Type {
			return result[i].Key < result[j].Key
		}
		return result[i].Type < result[j].Type
	})
	return result
}

func extensionCatalogKey(kind ExtensionType, key string) string {
	return string(kind) + "\x00" + key
}

func equalOperations(left, right []ExtensionOperation) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// CheckerV1 is the isolated submission-checker contract. The core authorizes,
// supplies bounded metadata, validates the returned verdict/score and alone
// persists the score; plugin code must not receive a direct core DB connection.
type CheckerV1 interface {
	Check(ctx context.Context, request CheckerRequest) (CheckerResult, error)
}

// CheckerRequest is bounded, non-secret submission metadata. File bytes are not
// included; a checker fetches only explicitly allowed metadata.
type CheckerRequest struct {
	CheckerKey   string          `json:"checkerKey"`
	ActorUserID  uuid.UUID       `json:"-"`
	BoardID      uuid.UUID       `json:"boardId"`
	ModuleID     uuid.UUID       `json:"moduleId"`
	TaskID       uuid.UUID       `json:"taskId"`
	SubmissionID uuid.UUID       `json:"submissionId"`
	SubjectType  string          `json:"subjectType"`
	SubjectID    uuid.UUID       `json:"subjectId"`
	Text         string          `json:"text,omitempty"`
	MaxPoints    int             `json:"maxPoints"`
	Config       json.RawMessage `json:"config,omitempty"`
}

// CheckerResult is advisory until core validates and applies it.
type CheckerResult struct {
	Verdict string `json:"verdict"` // ACCEPTED | REJECTED | PENDING_REVIEW
	Score   *int   `json:"score,omitempty"`
	Message string `json:"message,omitempty"`
}

// RuntimeDriverV1 is the versioned VM/container driver extension. Core keeps
// account, credential, lease and quota policy; the driver receives a normalized
// plan, never raw provider credentials.
type RuntimeDriverV1 interface {
	Plan(ctx context.Context, request RuntimePlanRequest) (RuntimePlan, error)
	Provision(ctx context.Context, plan RuntimePlan) (RuntimeState, error)
	Inspect(ctx context.Context, state RuntimeState) (RuntimeState, error)
	Reset(ctx context.Context, state RuntimeState) error
	Destroy(ctx context.Context, state RuntimeState) error
}

// RuntimePlanRequest is a secret-free normalized request.
type RuntimePlanRequest struct {
	BoardID  uuid.UUID       `json:"boardId"`
	ModuleID uuid.UUID       `json:"moduleId"`
	Kind     string          `json:"kind"` // VM | CONTAINER
	Driver   string          `json:"driver"`
	Snapshot json.RawMessage `json:"snapshot"`
}

// RuntimePlan is the driver's proposed resource creation.
type RuntimePlan struct {
	Driver  string          `json:"driver"`
	Kind    string          `json:"kind"`
	Changes json.RawMessage `json:"changes"`
}

// RuntimeState is the provider-neutral observed resource state.
type RuntimeState struct {
	Driver     string          `json:"driver"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Resources  json.RawMessage `json:"resources,omitempty"`
	Generation int64           `json:"generation,omitempty"`
}

// ModuleProviderV1 describes a plugin-provided Board Module kind. The returned
// type must be ModuleType EXTENSION plus an extensionKey at persistence; native
// TASKS/PENTEST/ATTACK_DEFENSE remain native core modules.
type ModuleProviderV1 interface {
	Describe(ctx context.Context) (ModuleDescriptor, error)
	ValidateConfig(ctx context.Context, config json.RawMessage) error
}

// ModuleDescriptor is the declarative module extension description.
type ModuleDescriptor struct {
	ExtensionKey string          `json:"extensionKey"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	ConfigSchema json.RawMessage `json:"configSchema,omitempty"`
}

// IntegrationProviderV1 describes an external integration such as Git. Core
// stores encrypted credentials and never exposes them to the plugin.
type IntegrationProviderV1 interface {
	Describe(ctx context.Context) (IntegrationDescriptor, error)
	TestConnection(ctx context.Context, settings json.RawMessage) error
}

// IntegrationDescriptor is the declarative integration description.
type IntegrationDescriptor struct {
	ExtensionKey string          `json:"extensionKey"`
	Name         string          `json:"name"`
	Category     string          `json:"category"` // GIT | COMPUTE
	ConfigSchema json.RawMessage `json:"configSchema,omitempty"`
}
