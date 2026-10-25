package nats

import (
	"context"
	"log"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/nats-io/nats.go/jetstream"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	"github.com/nrf24l01/actfb/internal/platform/nats"
)

type EmailTaskSubscriber struct {
	nats *nats.Client
}

func NewEmailTaskSubscriber(natsClient *nats.Client) *EmailTaskSubscriber {
	return &EmailTaskSubscriber{
		nats: natsClient,
	}
}

func (s *EmailTaskSubscriber) CreateStreams(ctx context.Context) error {
	_, err := s.nats.CreateOrUpdateStream(
		ctx,
		jetstream.StreamConfig{
			Name:     "EMAIL_TASKS",
			Subjects: []string{"email.tasks"},
			Storage:  jetstream.FileStorage,
		},
	)
	if err != nil {
		return err
	}
	return nil
}

func (s *EmailTaskSubscriber) SubscribeEmailTasks(ctx context.Context, handler func(context.Context, *emailv1.EmailTask) error) (jetstream.ConsumeContext, error) {
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
			var task emailv1.EmailTask

			if err := proto.Unmarshal(msg.Data(), &task); err != nil {
				log.Printf("decode email task: %v", err)
				if err := msg.Term(); err != nil {
					log.Printf("term email task: %v", err)
				}
				return
			}

			processCtx, cancel := context.WithTimeout(
				context.Background(),
				time.Minute,
			)
			defer cancel()

			if err := handler(processCtx, &task); err != nil {
				log.Printf("process email task %s: %v", task.Id, err)

				if err := msg.NakWithDelay(10 * time.Second); err != nil {
					log.Printf("nak email task: %v", err)
				}
				return
			}

			if err := msg.Ack(); err != nil {
				log.Printf("ack email task: %v", err)
			}
		},
	)
}
