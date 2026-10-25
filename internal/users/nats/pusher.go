package nats

import (
	"context"
	"fmt"

	"github.com/gogo/protobuf/proto"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
)

const (
	emailTasksSubject = "email.tasks"
	emailTasksStream  = "EMAIL_TASKS"
)

type EmailTaskPublisher struct {
	nats *platformnats.Client
}

type EmailType int

const (
	EmailTypeVerification EmailType = iota
)

func NewEmailPublisher(natsClient *platformnats.Client) *EmailTaskPublisher {
	return &EmailTaskPublisher{
		nats: natsClient,
	}
}

func (p *EmailTaskPublisher) PublishEmailVerificationTask(
	ctx context.Context,
	to, username, code string,
	taskID uuid.UUID,
) error {
	task := &emailv1.EmailTask{
		Id: taskID.String(),
		To: to,
		Payload: &emailv1.EmailTask_Verification{
			Verification: &emailv1.VerificationEmail{
				Username: username,
				Code:     code,
			},
		},
	}

	taskBytes, err := proto.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal email task %s: %w", taskID, err)
	}

	_, err = p.nats.PublishJetStream(
		ctx,
		emailTasksSubject,
		taskBytes,
		jetstream.WithMsgID(taskID.String()),
		jetstream.WithExpectStream(emailTasksStream),
	)
	if err != nil {
		return fmt.Errorf("publish email task %s: %w", taskID, err)
	}

	return nil
}
