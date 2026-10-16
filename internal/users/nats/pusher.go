package nats

import (
	"context"
	"uuid"

	"github.com/gogo/protobuf/proto"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	"github.com/nrf24l01/actfb/internal/platform/nats"
)

type EmailTaskPublisher struct {
	nats *nats.Client
}

type EmailType int

const (
	EmailTypeVerification EmailType = iota
)

func NewEmailPublisher(natsClient *nats.Client) *EmailTaskPublisher {
	return &EmailTaskPublisher{
		nats: natsClient,
	}
}

func (p *EmailTaskPublisher) PublishEmailVerificationTask(ctx context.Context, to, username, code string, task_id uuid.UUID) error {
	task := &emailv1.EmailTask{
		Id: task_id.String(),
		To: to,
		Payload: &emailv1.EmailTask_Verification{
			Verification: &emailv1.VerificationEmail{
				Username: username,
				Code:     code,
			},
		},
	}

	task_bytes, err := proto.Marshal(task)
	if err != nil {
		return err
	}

	return p.nats.PublishSync(ctx, "email.tasks", task_bytes)
}
