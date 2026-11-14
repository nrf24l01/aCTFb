package nats

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"google.golang.org/protobuf/proto"
)

const (
	emailTasksSubject = "email.tasks"
	emailTasksStream  = "EMAIL_TASKS"
)

type jetStreamPublisher interface {
	PublishJetStream(
		ctx context.Context,
		subject string,
		data []byte,
		opts ...jetstream.PublishOpt,
	) (*jetstream.PubAck, error)
}

type EmailTaskPublisher struct {
	nats jetStreamPublisher
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
	return p.publish(ctx, buildEmailVerificationTask(to, username, code, taskID), taskID)
}

func (p *EmailTaskPublisher) PublishPasswordResetTask(
	ctx context.Context,
	to, username, token string,
	taskID uuid.UUID,
) error {
	return p.publish(ctx, buildPasswordResetTask(to, username, token, taskID), taskID)
}

func (p *EmailTaskPublisher) publish(ctx context.Context, task *emailv1.EmailTask, taskID uuid.UUID) error {
	if p == nil || p.nats == nil {
		return fmt.Errorf("publish email task %s: publisher is not configured", taskID)
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

func buildEmailVerificationTask(to, username, code string, taskID uuid.UUID) *emailv1.EmailTask {
	return &emailv1.EmailTask{
		Id: taskID.String(),
		To: to,
		Payload: &emailv1.EmailTask_Verification{
			Verification: &emailv1.VerificationEmail{
				Username: username,
				Code:     code,
			},
		},
	}
}

func buildPasswordResetTask(to, username, token string, taskID uuid.UUID) *emailv1.EmailTask {
	return &emailv1.EmailTask{
		Id: taskID.String(),
		To: to,
		Payload: &emailv1.EmailTask_PasswordReset{
			PasswordReset: &emailv1.PasswordResetEmail{
				Username: username,
				Token:    token,
			},
		},
	}
}
