package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	"github.com/nrf24l01/actfb/internal/platform/nats"
	"google.golang.org/protobuf/proto"
)

const (
	defaultProcessTimeout = time.Minute
	nakRedeliveryDelay    = 10 * time.Second
)

// ErrPermanent marks a message that must not be retried (for example an
// unknown payload or malformed task). Transient errors are retried with NAK.
var ErrPermanent = errors.New("permanent email task error")

type EmailTaskSubscriber struct {
	nats *nats.Client

	processTimeout time.Duration
	logger         *slog.Logger
}

func NewEmailTaskSubscriber(natsClient *nats.Client) *EmailTaskSubscriber {
	return &EmailTaskSubscriber{
		nats:           natsClient,
		processTimeout: defaultProcessTimeout,
		logger:         slog.Default(),
	}
}

func (s *EmailTaskSubscriber) CreateStreams(ctx context.Context) error {
	if s == nil || s.nats == nil {
		return errors.New("NATS email task subscriber is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("create email task stream: %w", err)
	}

	_, err := s.nats.CreateOrUpdateStream(
		ctx,
		jetstream.StreamConfig{
			Name:     "EMAIL_TASKS",
			Subjects: []string{"email.tasks"},
			Storage:  jetstream.FileStorage,
		},
	)
	if err != nil {
		return fmt.Errorf("create or update EMAIL_TASKS stream: %w", err)
	}

	return nil
}

func (s *EmailTaskSubscriber) SubscribeEmailTasks(
	ctx context.Context,
	handler func(context.Context, *emailv1.EmailTask) error,
) (jetstream.ConsumeContext, error) {
	if s == nil || s.nats == nil {
		return nil, errors.New("NATS email task subscriber is not initialized")
	}
	if handler == nil {
		return nil, errors.New("email task handler is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("subscribe to email tasks: %w", err)
	}

	return s.nats.SubscribeJetStream(
		ctx,
		"EMAIL_TASKS",
		jetstream.ConsumerConfig{
			Durable:       "email-worker",
			FilterSubject: "email.tasks",
			AckPolicy:     jetstream.AckExplicitPolicy,
			AckWait:       2 * time.Minute,
			MaxDeliver:    5,
		},
		func(msg jetstream.Msg) {
			s.processMessage(ctx, msg, handler)
		},
	)
}

func (s *EmailTaskSubscriber) processMessage(
	ctx context.Context,
	msg jetstream.Msg,
	handler func(context.Context, *emailv1.EmailTask) error,
) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}

	var task emailv1.EmailTask
	if err := proto.Unmarshal(msg.Data(), &task); err != nil {
		logger.Error("failed to decode email task", "error", err)
		s.terminate(msg, logger, "decode email task: "+err.Error())
		return
	}

	processTimeout := s.processTimeout
	if processTimeout <= 0 {
		processTimeout = defaultProcessTimeout
	}

	processCtx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()

	if err := handler(processCtx, &task); err != nil {
		if ctx.Err() != nil {
			logger.Warn("email task interrupted by shutdown; leaving message for redelivery",
				"task_id", task.Id,
				"error", err,
			)
			return
		}

		if errors.Is(err, ErrPermanent) {
			logger.Error("email task failed permanently",
				"task_id", task.Id,
				"error", err,
			)
			s.terminate(msg, logger, err.Error())
			return
		}

		logger.Error("email task failed; scheduling redelivery",
			"task_id", task.Id,
			"delay", nakRedeliveryDelay,
			"error", err,
		)
		if nakErr := msg.NakWithDelay(nakRedeliveryDelay); nakErr != nil {
			logger.Error("failed to NAK email task", "task_id", task.Id, "error", nakErr)
		}
		return
	}

	if err := msg.Ack(); err != nil {
		logger.Error("failed to ACK email task", "task_id", task.Id, "error", err)
	}
}

func (s *EmailTaskSubscriber) terminate(msg jetstream.Msg, logger *slog.Logger, reason string) {
	if err := msg.TermWithReason(reason); err != nil {
		logger.Warn("failed to terminate email task with reason; using Term",
			"reason", reason,
			"error", err,
		)
		if termErr := msg.Term(); termErr != nil {
			logger.Error("failed to terminate email task", "error", termErr)
		}
	}
}
