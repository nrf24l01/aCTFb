package plugins

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/events"
)

type recordingPublisher struct {
	published []events.Event
}

func (p *recordingPublisher) Publish(_ context.Context, event events.Event) error {
	p.published = append(p.published, event)
	return nil
}

func TestCoreProxyPublishEventNamespaceAndScope(t *testing.T) {
	registry := events.CoreRegistry()
	if err := registry.RegisterPlugin("example.hello", "plugin.example.hello.ping"); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	proxy := &CoreProxyService{eventRegistry: registry, publisher: publisher}
	userID := uuid.New()
	boardID := uuid.New()
	identity := Identity{PluginID: "example.hello", Capabilities: []Capability{CapabilityEventsSubscribe}}
	delegation := Delegation{PluginID: "example.hello", UserID: userID, Scope: authorization.Scope{BoardID: boardID}}
	call := CallContext{Identity: identity, Delegation: delegation, UserID: userID, Scope: delegation.Scope}

	coreEvent := events.Event{
		ID: uuid.New(), Type: events.EventSubmissionJudged, BoardID: &boardID,
		Payload: json.RawMessage(`{}`),
	}
	if err := proxy.PublishEvent(context.Background(), call, coreEvent); err == nil {
		t.Fatal("plugins must never publish core submission events")
	}

	undeclared := events.Event{
		ID: uuid.New(), Type: "plugin.example.hello.undeclared", BoardID: &boardID,
		Payload: json.RawMessage(`{}`),
	}
	if err := proxy.PublishEvent(context.Background(), call, undeclared); err == nil {
		t.Fatal("undeclared plugin event must be rejected")
	}

	forgedBoard := uuid.New()
	forged := events.Event{
		ID: uuid.New(), Type: "plugin.example.hello.ping", BoardID: &forgedBoard,
		Payload: json.RawMessage(`{"ok":true}`),
	}
	if err := proxy.PublishEvent(context.Background(), call, forged); err == nil {
		t.Fatal("event board outside delegation scope must be rejected")
	}

	otherUser := uuid.New()
	forgedSubject := events.Event{
		ID: uuid.New(), Type: "plugin.example.hello.ping", BoardID: &boardID,
		SubjectType: events.SubjectTypeUser, SubjectID: &otherUser,
		Payload: json.RawMessage(`{"ok":true}`),
	}
	if err := proxy.PublishEvent(context.Background(), call, forgedSubject); err == nil {
		t.Fatal("forged subject identity must be rejected")
	}

	valid := events.Event{
		ID: uuid.New(), Type: "plugin.example.hello.ping", BoardID: &boardID,
		SubjectType: events.SubjectTypeUser, SubjectID: &userID,
		Payload: json.RawMessage(`{"ok":true}`),
	}
	if err := proxy.PublishEvent(context.Background(), call, valid); err != nil {
		t.Fatalf("valid plugin event rejected: %v", err)
	}
	if len(publisher.published) != 1 || publisher.published[0].Type != valid.Type {
		t.Fatalf("published = %#v", publisher.published)
	}

	noCapability := call
	noCapability.Identity = Identity{PluginID: "example.hello"}
	if err := proxy.PublishEvent(context.Background(), noCapability, valid); err == nil {
		t.Fatal("event publication requires the approved capability")
	}
}
