package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrUnknownEventType    = errors.New("events: unknown event type")
	ErrInvalidEventType    = errors.New("events: invalid event type")
	ErrInvalidEvent        = errors.New("events: invalid event")
	ErrInvalidPayload      = errors.New("events: invalid payload")
	ErrForbiddenPayloadKey = errors.New("events: forbidden payload key")
	ErrPluginCoreTopic     = errors.New("events: plugins may not publish core event topics")
	ErrEventNotDeclared    = errors.New("events: event type is not declared for this plugin")
)

var (
	eventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	pluginIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)+$`)
)

// PayloadValidator optionally enforces an event-specific JSON schema in
// addition to the generic object/secret-key checks.
type PayloadValidator func(payload json.RawMessage) error

// Registry declares trusted core event types and plugin-namespaced event types.
// It is safe for concurrent reads after registration.
type Registry struct {
	mu      sync.RWMutex
	core    map[string]PayloadValidator
	plugins map[string]map[string]PayloadValidator
}

func NewRegistry() *Registry {
	return &Registry{
		core:    make(map[string]PayloadValidator),
		plugins: make(map[string]map[string]PayloadValidator),
	}
}

// CoreRegistry returns a registry with the frozen core event types and the
// submission.judged schema.
func CoreRegistry() *Registry {
	registry := NewRegistry()

	for _, eventType := range []string{
		EventInstanceCreated,
		EventInstanceStarted,
		EventInstanceStopped,
		EventInstanceDestroyed,
		EventInstanceExpired,
	} {
		_ = registry.RegisterCore(eventType)
	}
	_ = registry.RegisterCoreSchema(EventSubmissionJudged, ValidateSubmissionJudgedPayload)

	return registry
}

// RegisterCore declares a trusted core event type with generic payload checks.
func (r *Registry) RegisterCore(eventType string) error {
	return r.registerCore(eventType, nil)
}

// RegisterCoreSchema declares a core event type with an additional schema.
func (r *Registry) RegisterCoreSchema(eventType string, validator PayloadValidator) error {
	return r.registerCore(eventType, validator)
}

func (r *Registry) registerCore(eventType string, validator PayloadValidator) error {
	if err := validateCoreEventType(eventType); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.core[eventType]; exists {
		return fmt.Errorf("%w: duplicate core event type", ErrInvalidEventType)
	}
	r.core[eventType] = validator
	return nil
}

// RegisterPlugin declares event types under plugin.<id>.*. The event names must
// live in the supplied plugin's namespace. It grants nothing else.
func (r *Registry) RegisterPlugin(pluginID string, eventTypes ...string) error {
	if !pluginIDPattern.MatchString(pluginID) {
		return fmt.Errorf("%w: plugin id must be namespaced", ErrInvalidEventType)
	}
	if len(eventTypes) == 0 {
		return fmt.Errorf("%w: no plugin event types supplied", ErrInvalidEventType)
	}

	for _, eventType := range eventTypes {
		if err := validatePluginEventType(pluginID, eventType); err != nil {
			return err
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.plugins[pluginID] == nil {
		r.plugins[pluginID] = make(map[string]PayloadValidator)
	}

	for _, eventType := range eventTypes {
		if _, exists := r.plugins[pluginID][eventType]; !exists {
			r.plugins[pluginID][eventType] = nil
		}
	}

	return nil
}

// RegisterPluginSchema declares a plugin event and its payload schema.
func (r *Registry) RegisterPluginSchema(pluginID, eventType string, validator PayloadValidator) error {
	if !pluginIDPattern.MatchString(pluginID) {
		return fmt.Errorf("%w: plugin id must be namespaced", ErrInvalidEventType)
	}
	if err := validatePluginEventType(pluginID, eventType); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plugins[pluginID] == nil {
		r.plugins[pluginID] = make(map[string]PayloadValidator)
	}
	r.plugins[pluginID][eventType] = validator
	return nil
}

func (r *Registry) Validate(eventType string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if _, ok := r.core[eventType]; ok {
		return nil
	}
	for _, declared := range r.plugins {
		if _, ok := declared[eventType]; ok {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrUnknownEventType, eventType)
}

func (r *Registry) ValidateEvent(event Event) error {
	if event.ID == uuid.Nil {
		return fmt.Errorf("%w: id is required", ErrInvalidEvent)
	}
	if err := r.Validate(event.Type); err != nil {
		return err
	}
	if event.SubjectType != "" {
		if event.SubjectType != SubjectTypeUser && event.SubjectType != SubjectTypeTeam {
			return fmt.Errorf("%w: subject type must be USER or TEAM", ErrInvalidEvent)
		}
		if event.SubjectID == nil {
			return fmt.Errorf("%w: subject id is required with subject type", ErrInvalidEvent)
		}
	} else if event.SubjectID != nil {
		return fmt.Errorf("%w: subject type is required with subject id", ErrInvalidEvent)
	}

	validator := r.validatorFor(event.Type)
	return validatePayload(event.Payload, validator)
}

// ValidateCorePublish allows only registered core types. Plugin topics are
// rejected for core proxy/plugin callers.
func (r *Registry) ValidateCorePublish(eventType string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if strings.HasPrefix(eventType, "plugin.") {
		return ErrPluginCoreTopic
	}
	if _, ok := r.core[eventType]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownEventType, eventType)
	}
	return nil
}

// ValidatePluginPublish allows only registered plugin.<id>.* topics belonging
// to pluginID. Core topics such as submission.judged are never allowed.
func (r *Registry) ValidatePluginPublish(pluginID, eventType string) error {
	if !pluginIDPattern.MatchString(pluginID) {
		return fmt.Errorf("%w: plugin id must be namespaced", ErrInvalidEventType)
	}
	if !strings.HasPrefix(eventType, "plugin.") {
		return ErrPluginCoreTopic
	}
	if err := validatePluginEventType(pluginID, eventType); err != nil {
		return ErrEventNotDeclared
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.plugins[pluginID][eventType]; !ok {
		return ErrEventNotDeclared
	}
	return nil
}

func (r *Registry) validatorFor(eventType string) PayloadValidator {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if validator, ok := r.core[eventType]; ok {
		return validator
	}
	for _, declared := range r.plugins {
		if validator, ok := declared[eventType]; ok {
			return validator
		}
	}
	return nil
}

func validateCoreEventType(eventType string) error {
	if !eventTypePattern.MatchString(eventType) || strings.HasPrefix(eventType, "plugin.") {
		return ErrInvalidEventType
	}
	return nil
}

func validatePluginEventType(pluginID, eventType string) error {
	if !eventTypePattern.MatchString(eventType) {
		return ErrInvalidEventType
	}
	prefix := "plugin." + pluginID + "."
	if !strings.HasPrefix(eventType, prefix) || len(eventType) == len(prefix) {
		return ErrEventNotDeclared
	}
	return nil
}

func validatePayload(raw json.RawMessage, validator PayloadValidator) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}

	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: malformed JSON", ErrInvalidPayload)
	}
	if err := ensureEOF(decoder); err != nil {
		return fmt.Errorf("%w: trailing JSON data", ErrInvalidPayload)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: payload must be a JSON object", ErrInvalidPayload)
	}
	if err := rejectForbiddenKeys(object); err != nil {
		return err
	}

	if validator != nil {
		return validator(raw)
	}
	return nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("trailing data")
}

func rejectForbiddenKeys(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if forbiddenPayloadKey(key) {
				return fmt.Errorf("%w", ErrForbiddenPayloadKey)
			}
			if err := rejectForbiddenKeys(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := rejectForbiddenKeys(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func forbiddenPayloadKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(normalized)

	for _, part := range []string{
		"password", "passwd", "secret", "token", "credential",
		"apikey", "privatekey", "accesskey", "authorization",
		"filebytes", "binary",
	} {
		if strings.Contains(normalized, part) {
			return true
		}
	}

	switch normalized {
	case "flag", "answer", "text", "submittedtext", "content",
		"bytes", "binary", "blob", "filebytes", "plaintext", "requesthash", "answerhash":
		return true
	}

	if strings.HasSuffix(normalized, "hash") ||
		strings.HasSuffix(normalized, "answer") ||
		strings.HasSuffix(normalized, "flag") {
		return true
	}

	return false
}

// ValidateSubmissionJudgedPayload rejects unknown/unsafe fields for the frozen
// submission.judged schema.
func ValidateSubmissionJudgedPayload(raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("%w: malformed submission.judged payload", ErrInvalidPayload)
	}

	allowed := map[string]struct{}{
		"submissionUuid": {}, "boardUuid": {}, "moduleUuid": {}, "taskUuid": {},
		"subjectType": {}, "subjectUuid": {}, "status": {}, "scoreAwarded": {},
		"occurredAt": {},
	}
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%w: unsupported submission.judged field", ErrInvalidPayload)
		}
	}

	var payload SubmissionJudgedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("%w: malformed submission.judged payload", ErrInvalidPayload)
	}
	if payload.SubmissionUUID == uuid.Nil || payload.BoardUUID == uuid.Nil ||
		payload.ModuleUUID == uuid.Nil || payload.TaskUUID == uuid.Nil ||
		payload.SubjectUUID == uuid.Nil {
		return fmt.Errorf("%w: submission.judged identifiers are required", ErrInvalidPayload)
	}
	if payload.SubjectType != SubjectTypeUser && payload.SubjectType != SubjectTypeTeam {
		return fmt.Errorf("%w: submission.judged subject type must be USER or TEAM", ErrInvalidPayload)
	}
	if strings.TrimSpace(payload.Status) == "" || payload.OccurredAt.IsZero() {
		return fmt.Errorf("%w: submission.judged status and occurredAt are required", ErrInvalidPayload)
	}

	return nil
}
