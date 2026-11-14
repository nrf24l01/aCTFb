package attackdefense

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
)

// Durable job wake-ups. The message carries a run id only; the database is the
// source of truth and worker reconciliation recovers lost messages.
const (
	JobStreamName = "ACTFB_AD_JOBS"
	JobSubject    = "actfb.ad.jobs"
	JobConsumer   = "ad-worker"
)

// RunJob is the only accepted NATS payload.
type RunJob struct {
	RunID uuid.UUID `json:"runId"`
}

// EnsureJobStream creates or updates the work-queue stream.
func EnsureJobStream(ctx context.Context, client *platformnats.Client) error {
	if client == nil {
		return fmt.Errorf("attackdefense: NATS client is required")
	}
	_, err := client.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      JobStreamName,
		Subjects:  []string{JobSubject},
		Retention: jetstream.WorkQueuePolicy,
		Storage:   jetstream.FileStorage,
		MaxAge:    24 * 60 * 60 * 1e9,
	})
	return err
}

// NATSJobPublisher publishes run ids with Nats-Msg-Id dedupe.
type NATSJobPublisher struct {
	client *platformnats.Client
}

// NewNATSJobPublisher constructs the publisher.
func NewNATSJobPublisher(client *platformnats.Client) (*NATSJobPublisher, error) {
	if client == nil {
		return nil, fmt.Errorf("attackdefense: NATS client is required")
	}
	return &NATSJobPublisher{client: client}, nil
}

// PublishRun implements JobPublisher.
func (p *NATSJobPublisher) PublishRun(ctx context.Context, runID uuid.UUID) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("attackdefense: NATS publisher is not configured")
	}
	payload, err := json.Marshal(RunJob{RunID: runID})
	if err != nil {
		return err
	}
	_, err = p.client.PublishJetStream(ctx, JobSubject, payload, jetstream.WithMsgID(runID.String()))
	return err
}
