package notifications

import (
	"context"
	"fmt"
	"log/slog"

	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	"github.com/nrf24l01/actfb/internal/notifications/nats"
	"github.com/nrf24l01/actfb/internal/platform/email"
)

type NotificationService struct {
	email *email.Client
	nats  *nats.EmailTaskSubscriber
}

func NewNotificationService(emailClient *email.Client, natsClient *nats.EmailTaskSubscriber) *NotificationService {
	return &NotificationService{
		email: emailClient,
		nats:  natsClient,
	}
}

func (ns *NotificationService) Start(ctx context.Context, logger *slog.Logger) error {
	if err := ns.nats.CreateStreams(ctx); err != nil {
		return err
	}

	_, err := ns.nats.SubscribeEmailTasks(ctx, func(ctx context.Context, task *emailv1.EmailTask) error {
		switch payload := task.Payload.(type) {
		case *emailv1.EmailTask_Verification:
			logger.Debug("Received verification email task", "to", task.To, "username", payload.Verification.Username)
			err := ns.email.SendVerificationEmail(ctx, task.To, payload.Verification.Code, payload.Verification.Username)
			if err != nil {
				logger.Error("Failed to send verification email", "error", err, "to", task.To)
				return fmt.Errorf("send verification email: %w", err)
			}
			logger.Info("Verification email sent successfully", "to", task.To)
			return nil
		default:
			return fmt.Errorf("unknown email task type: %T", payload)
		}
	})
	if err != nil {
		return fmt.Errorf("subscribe to email tasks: %w", err)
	}

	return nil
}
