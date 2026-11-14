package instances

import (
	"context"
	"errors"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	// JobStream is the durable JetStream stream for instance jobs.
	JobStream = "ACTFB_INSTANCES"
	// JobSubject is the subject the worker consumes.
	JobSubject = "actfb.instances.jobs"
)

// JetStreamPublisher is the narrow publish contract implemented by the shared
// NATS wrapper.
type JetStreamPublisher interface {
	PublishJetStream(ctx context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// NATSJobPublisher publishes ids-only jobs durably.
type NATSJobPublisher struct {
	client JetStreamPublisher
}

// NewNATSJobPublisher constructs the publisher.
func NewNATSJobPublisher(client JetStreamPublisher) *NATSJobPublisher {
	return &NATSJobPublisher{client: client}
}

var _ JobPublisher = (*NATSJobPublisher)(nil)

// PublishJob persists one job with a stable message id so a retried publish is
// deduplicated by JetStream.
func (p *NATSJobPublisher) PublishJob(ctx context.Context, job Job) error {
	if p == nil || p.client == nil {
		return errors.New("instances: job publisher is not configured")
	}
	payload, err := MarshalJob(job)
	if err != nil {
		return err
	}
	messageID := job.InstanceID.String() + ":" + strings.Join([]string{formatInt(job.Generation), job.Reason}, "-")
	_, err = p.client.PublishJetStream(ctx, JobSubject, payload, jetstream.WithMsgID(messageID))
	return err
}

func formatInt(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buffer := make([]byte, 0, 20)
	for value > 0 {
		buffer = append([]byte{byte('0' + value%10)}, buffer...)
		value /= 10
	}
	if negative {
		return "-" + string(buffer)
	}
	return string(buffer)
}
