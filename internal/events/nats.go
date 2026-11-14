package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
)

// EnsureStream creates or updates the shared durable event stream.
func EnsureStream(ctx context.Context, client *platformnats.Client) error {
	if client == nil {
		return errors.New("events: NATS client is not configured")
	}

	_, err := client.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     StreamName,
		Subjects: []string{SubjectPrefix + ">"},
		Storage:  jetstream.FileStorage,
	})
	if err != nil {
		return fmt.Errorf("events: ensure stream: %w", err)
	}
	return nil
}

// NATSPublisher publishes to JetStream with the event ID as the message ID so
// retries and crashes dedupe within the stream window.
type NATSPublisher struct {
	client   *platformnats.Client
	registry *Registry
}

type NATSPublisherOption func(*NATSPublisher)

func WithNATSPublisherRegistry(registry *Registry) NATSPublisherOption {
	return func(publisher *NATSPublisher) {
		if registry != nil {
			publisher.registry = registry
		}
	}
}

func NewNATSPublisher(client *platformnats.Client, opts ...NATSPublisherOption) *NATSPublisher {
	publisher := &NATSPublisher{
		client:   client,
		registry: CoreRegistry(),
	}
	for _, opt := range opts {
		opt(publisher)
	}
	return publisher
}

var _ Publisher = (*NATSPublisher)(nil)

func (p *NATSPublisher) Publish(ctx context.Context, event Event) error {
	if p == nil || p.client == nil {
		return errors.New("events: NATS publisher is not configured")
	}

	event = normalizeEvent(event)
	if err := p.registryOrDefault().ValidateEvent(event); err != nil {
		return err
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("events: marshal %s: %w", event.ID, err)
	}

	_, err = p.client.PublishJetStream(
		ctx,
		SubjectFor(event.Type),
		data,
		jetstream.WithMsgID(event.ID.String()),
		jetstream.WithExpectStream(StreamName),
	)
	if err != nil {
		return fmt.Errorf("events: publish %s: %w", event.ID, err)
	}

	return nil
}

func (p *NATSPublisher) registryOrDefault() *Registry {
	if p != nil && p.registry != nil {
		return p.registry
	}
	return CoreRegistry()
}
