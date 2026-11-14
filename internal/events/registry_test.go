package events

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validJudgedPayload() SubmissionJudgedPayload {
	now := time.Now().UTC().Truncate(time.Second)
	return SubmissionJudgedPayload{
		SubmissionUUID: uuid.New(),
		BoardUUID:      uuid.New(),
		ModuleUUID:     uuid.New(),
		TaskUUID:       uuid.New(),
		SubjectType:    SubjectTypeUser,
		SubjectUUID:    uuid.New(),
		Status:         "ACCEPTED",
		OccurredAt:     now,
	}
}

func judgedPayloadJSON(t *testing.T, payload SubmissionJudgedPayload) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func TestRegistrySubmissionJudgedSchema(t *testing.T) {
	registry := CoreRegistry()
	payload := validJudgedPayload()

	event := Event{
		ID:          uuid.New(),
		Type:        EventSubmissionJudged,
		OccurredAt:  payload.OccurredAt,
		BoardID:     &payload.BoardUUID,
		ModuleID:    &payload.ModuleUUID,
		SubjectType: payload.SubjectType,
		SubjectID:   &payload.SubjectUUID,
		Payload:     judgedPayloadJSON(t, payload),
	}
	if err := registry.ValidateEvent(event); err != nil {
		t.Fatalf("ValidateEvent() error = %v", err)
	}

	event.Payload = json.RawMessage(`{"submissionUuid":"` + payload.SubmissionUUID.String() + `","flag":"secret"}`)
	if err := registry.ValidateEvent(event); !errors.Is(err, ErrForbiddenPayloadKey) {
		t.Fatalf("flag payload error = %v, want ErrForbiddenPayloadKey", err)
	}

	event.Payload = json.RawMessage(`{"submissionUuid":"` + payload.SubmissionUUID.String() + `","surprise":true}`)
	if err := registry.ValidateEvent(event); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("unknown field error = %v, want ErrInvalidPayload", err)
	}

	event.Payload = json.RawMessage(`{}`)
	if err := registry.ValidateEvent(event); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("empty submission payload error = %v, want ErrInvalidPayload", err)
	}
}

func TestRegistryRejectsForbiddenPayloadKeys(t *testing.T) {
	registry := CoreRegistry()
	base := validJudgedPayload()
	baseJSON := judgedPayloadJSON(t, base)

	for _, key := range []string{
		"flag", "answer", "requestHash", "text", "fileBytes", "binary",
		"password", "accessToken", "credentials", "acceptedAnswer",
	} {
		t.Run(key, func(t *testing.T) {
			var object map[string]any
			if err := json.Unmarshal(baseJSON, &object); err != nil {
				t.Fatalf("unmarshal base payload: %v", err)
			}
			object[key] = "do-not-persist"
			raw, err := json.Marshal(object)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}

			event := Event{
				ID:          uuid.New(),
				Type:        EventSubmissionJudged,
				OccurredAt:  base.OccurredAt,
				SubjectType: base.SubjectType,
				SubjectID:   &base.SubjectUUID,
				Payload:     raw,
			}
			if err := registry.ValidateEvent(event); !errors.Is(err, ErrForbiddenPayloadKey) {
				t.Fatalf("payload key %q error = %v, want ErrForbiddenPayloadKey", key, err)
			}
		})
	}

	nested := Event{
		ID:         uuid.New(),
		Type:       EventInstanceCreated,
		OccurredAt: time.Now().UTC(),
		Payload:    json.RawMessage(`{"instanceUuid":"x","nested":{"token":"secret"}}`),
	}
	if err := registry.ValidateEvent(nested); !errors.Is(err, ErrForbiddenPayloadKey) {
		t.Fatalf("nested token error = %v, want ErrForbiddenPayloadKey", err)
	}
}

func TestRegistryUnknownTypesAndPluginNamespaces(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterPlugin("vendor.plugin", "plugin.vendor.plugin.created"); err != nil {
		t.Fatalf("RegisterPlugin() error = %v", err)
	}
	if err := registry.RegisterCore(EventSubmissionJudged); err != nil {
		t.Fatalf("RegisterCore() error = %v", err)
	}

	if err := registry.Validate(EventSubmissionJudged); err != nil {
		t.Fatalf("Validate(core) error = %v", err)
	}
	if err := registry.Validate("plugin.vendor.plugin.created"); err != nil {
		t.Fatalf("Validate(plugin) error = %v", err)
	}
	if err := registry.Validate("instance.unknown"); !errors.Is(err, ErrUnknownEventType) {
		t.Fatalf("Validate(unknown) error = %v, want ErrUnknownEventType", err)
	}

	if err := registry.ValidatePluginPublish("vendor.plugin", "plugin.vendor.plugin.created"); err != nil {
		t.Fatalf("ValidatePluginPublish(allowed) error = %v", err)
	}
	if err := registry.ValidatePluginPublish("vendor.plugin", EventSubmissionJudged); !errors.Is(err, ErrPluginCoreTopic) {
		t.Fatalf("plugin core topic error = %v, want ErrPluginCoreTopic", err)
	}
	if err := registry.ValidatePluginPublish("vendor.plugin", "plugin.other.plugin.created"); !errors.Is(err, ErrEventNotDeclared) {
		t.Fatalf("other plugin topic error = %v, want ErrEventNotDeclared", err)
	}
	if err := registry.ValidatePluginPublish("vendor.plugin", "plugin.vendor.plugin.undeclared"); !errors.Is(err, ErrEventNotDeclared) {
		t.Fatalf("undeclared plugin topic error = %v, want ErrEventNotDeclared", err)
	}

	if err := registry.ValidateCorePublish("plugin.vendor.plugin.created"); !errors.Is(err, ErrPluginCoreTopic) {
		t.Fatalf("core publish plugin topic error = %v, want ErrPluginCoreTopic", err)
	}
	if err := registry.ValidateCorePublish(EventSubmissionJudged); err != nil {
		t.Fatalf("ValidateCorePublish(core) error = %v", err)
	}

	if err := registry.RegisterPlugin("vendor", "plugin.vendor.created"); err == nil {
		t.Fatal("RegisterPlugin(non-namespaced id) error = nil")
	}
	if err := registry.RegisterCore("plugin.vendor.plugin.created"); err == nil {
		t.Fatal("RegisterCore(plugin topic) error = nil")
	}
}

func TestRegistryEventEnvelopeValidation(t *testing.T) {
	registry := CoreRegistry()
	event := Event{
		ID:         uuid.New(),
		Type:       EventInstanceCreated,
		OccurredAt: time.Now().UTC(),
		Payload:    json.RawMessage(`{"instanceUuid":"` + uuid.NewString() + `"}`),
	}
	if err := registry.ValidateEvent(event); err != nil {
		t.Fatalf("valid event error = %v", err)
	}

	event.ID = uuid.Nil
	if err := registry.ValidateEvent(event); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("nil id error = %v, want ErrInvalidEvent", err)
	}

	event.ID = uuid.New()
	event.SubjectType = "USER"
	if err := registry.ValidateEvent(event); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("subject type without id error = %v, want ErrInvalidEvent", err)
	}

	subjectID := uuid.New()
	event.SubjectType = ""
	event.SubjectID = &subjectID
	if err := registry.ValidateEvent(event); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("subject id without type error = %v, want ErrInvalidEvent", err)
	}
}
