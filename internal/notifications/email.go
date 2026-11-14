package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	"github.com/nrf24l01/actfb/internal/notifications/nats"
	"github.com/nrf24l01/actfb/internal/platform/email"
)

const emailProcessTimeout = time.Minute

// EmailClient is the subset of the SMTP client used by the notification worker.
type EmailClient interface {
	SendEmail(ctx context.Context, to string, subject string, textBody string, htmlBody string) error
}

// EmailTaskSubscriber is the JetStream interface consumed by the worker.
type EmailTaskSubscriber interface {
	CreateStreams(ctx context.Context) error
	SubscribeEmailTasks(
		ctx context.Context,
		handler func(context.Context, *emailv1.EmailTask) error,
	) (jetstream.ConsumeContext, error)
}

// VerificationEmailData is the template payload for verification emails.
type VerificationEmailData = email.VerificationEmailData

// PasswordResetEmailData is the template payload for password reset emails.
type PasswordResetEmailData struct {
	Username string
	Token    string
	Email    string
}

type NotificationService struct {
	email    EmailClient
	nats     EmailTaskSubscriber
	renderer TemplateRenderer

	mu     sync.Mutex
	sub    jetstream.ConsumeContext
	cancel context.CancelFunc
	logger *slog.Logger
}

func NewNotificationService(
	emailClient EmailClient,
	natsClient EmailTaskSubscriber,
	renderers ...TemplateRenderer,
) *NotificationService {
	var renderer TemplateRenderer
	if len(renderers) > 0 {
		renderer = renderers[0]
	}

	return &NotificationService{
		email:    emailClient,
		nats:     natsClient,
		renderer: renderer,
	}
}

// Start creates the EMAIL_TASKS stream and starts consuming email tasks.
// The caller must call Shutdown to stop the consumer and release resources.
func (ns *NotificationService) Start(ctx context.Context, logger *slog.Logger) error {
	if ns == nil {
		return errors.New("notification service is nil")
	}
	if ns.email == nil {
		return errors.New("email client is nil")
	}
	if ns.nats == nil {
		return errors.New("NATS email task subscriber is nil")
	}
	if ns.renderer == nil {
		return errors.New("email template renderer is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	ns.mu.Lock()
	if ns.sub != nil || ns.cancel != nil {
		ns.mu.Unlock()
		return errors.New("notification service is already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	ns.cancel = cancel
	ns.logger = logger
	ns.mu.Unlock()

	cleanup := func() {
		cancel()
		ns.mu.Lock()
		ns.cancel = nil
		ns.mu.Unlock()
	}

	if err := ns.nats.CreateStreams(runCtx); err != nil {
		cleanup()
		return fmt.Errorf("create email task stream: %w", err)
	}

	sub, err := ns.nats.SubscribeEmailTasks(runCtx, ns.handleEmailTask)
	if err != nil {
		cleanup()
		return fmt.Errorf("subscribe to email tasks: %w", err)
	}

	ns.mu.Lock()
	ns.sub = sub
	ns.mu.Unlock()

	logger.Info("notification worker started", "stream", "EMAIL_TASKS", "subject", "email.tasks")

	return nil
}

// Shutdown drains the consumer, waiting for in-flight work until ctx expires.
func (ns *NotificationService) Shutdown(ctx context.Context) error {
	if ns == nil {
		return nil
	}

	ns.mu.Lock()
	sub := ns.sub
	cancel := ns.cancel
	ns.sub = nil
	ns.cancel = nil
	ns.mu.Unlock()

	if sub == nil {
		if cancel != nil {
			cancel()
		}
		return nil
	}

	sub.Drain()

	select {
	case <-sub.Closed():
		if cancel != nil {
			cancel()
		}
		return nil
	case <-ctx.Done():
		sub.Stop()
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("drain email task consumer: %w", ctx.Err())
	}
}

func (ns *NotificationService) handleEmailTask(ctx context.Context, task *emailv1.EmailTask) error {
	logger := ns.logger
	if logger == nil {
		logger = slog.Default()
	}

	if task == nil {
		return fmt.Errorf("%w: nil email task", nats.ErrPermanent)
	}
	if strings.TrimSpace(task.To) == "" {
		return fmt.Errorf("%w: email task %q has empty recipient", nats.ErrPermanent, task.Id)
	}

	switch payload := task.Payload.(type) {
	case *emailv1.EmailTask_Verification:
		if payload.Verification == nil {
			return fmt.Errorf("%w: verification payload is nil in task %q", nats.ErrPermanent, task.Id)
		}
		if strings.TrimSpace(payload.Verification.Code) == "" {
			return fmt.Errorf("%w: verification code is empty in task %q", nats.ErrPermanent, task.Id)
		}

		logger.Info("sending verification email",
			"task_id", task.Id,
			"to", task.To,
			"username", payload.Verification.Username,
		)

		processCtx, cancel := context.WithTimeout(ctx, emailProcessTimeout)
		defer cancel()

		if err := ns.sendVerificationEmail(
			processCtx,
			task.To,
			payload.Verification.Code,
			payload.Verification.Username,
		); err != nil {
			logger.Error("failed to send verification email",
				"task_id", task.Id,
				"to", task.To,
				"error", err,
			)
			return fmt.Errorf("send verification email for task %q: %w", task.Id, err)
		}

		logger.Info("verification email sent",
			"task_id", task.Id,
			"to", task.To,
		)

		return nil

	case *emailv1.EmailTask_PasswordReset:
		if payload.PasswordReset == nil {
			return fmt.Errorf("%w: password reset payload is nil in task %q", nats.ErrPermanent, task.Id)
		}
		if strings.TrimSpace(payload.PasswordReset.Token) == "" {
			return fmt.Errorf("%w: password reset token is empty in task %q", nats.ErrPermanent, task.Id)
		}

		// Never log the recipient or token for password reset messages.
		logger.Info("sending password reset email", "task_id", task.Id)

		processCtx, cancel := context.WithTimeout(ctx, emailProcessTimeout)
		defer cancel()

		if err := ns.sendPasswordResetEmail(
			processCtx,
			task.To,
			payload.PasswordReset.Token,
			payload.PasswordReset.Username,
		); err != nil {
			redacted := redactEmailError(err, task.To, payload.PasswordReset.Token)
			logger.Error("failed to send password reset email", "task_id", task.Id, "error", redacted)
			return fmt.Errorf("send password reset email for task %q: %w", task.Id, redacted)
		}

		logger.Info("password reset email sent", "task_id", task.Id)
		return nil

	default:
		return fmt.Errorf("%w: unknown email task payload %T", nats.ErrPermanent, payload)
	}
}

// sendVerificationEmail renders the notification-owned verification templates
// and sends them through the shared SMTP client. Template order matters: the
// text body is passed as text/plain and the HTML body as the alternative.
func (ns *NotificationService) sendVerificationEmail(ctx context.Context, to, code, username string) error {
	data := VerificationEmailData{
		Username: username,
		Code:     code,
		Email:    to,
	}

	subject, err := ns.renderer.Render("verification/subject.tmpl", data)
	if err != nil {
		return fmt.Errorf("%w: render verification subject template: %w", nats.ErrPermanent, err)
	}

	htmlBody, err := ns.renderer.Render("verification/body.html.tmpl", data)
	if err != nil {
		return fmt.Errorf("%w: render verification HTML template: %w", nats.ErrPermanent, err)
	}

	textBody, err := ns.renderer.Render("verification/body.txt.tmpl", data)
	if err != nil {
		return fmt.Errorf("%w: render verification text template: %w", nats.ErrPermanent, err)
	}

	if err := ns.email.SendEmail(ctx, to, subject, textBody, htmlBody); err != nil {
		return fmt.Errorf("send verification email: %w", err)
	}

	return nil
}

// sendPasswordResetEmail renders the reset templates and sends them through
// the shared SMTP client. The opaque reset token is shown in the message; no
// token-bearing URL is generated.
func (ns *NotificationService) sendPasswordResetEmail(ctx context.Context, to, token, username string) error {
	data := PasswordResetEmailData{
		Username: username,
		Token:    token,
		Email:    to,
	}

	subject, err := ns.renderer.Render("password_reset/subject.tmpl", data)
	if err != nil {
		return fmt.Errorf("%w: render password reset subject template: %w", nats.ErrPermanent, err)
	}

	htmlBody, err := ns.renderer.Render("password_reset/body.html.tmpl", data)
	if err != nil {
		return fmt.Errorf("%w: render password reset HTML template: %w", nats.ErrPermanent, err)
	}

	textBody, err := ns.renderer.Render("password_reset/body.txt.tmpl", data)
	if err != nil {
		return fmt.Errorf("%w: render password reset text template: %w", nats.ErrPermanent, err)
	}

	if err := ns.email.SendEmail(ctx, to, subject, textBody, htmlBody); err != nil {
		return fmt.Errorf("send password reset email: %w", err)
	}

	return nil
}

type redactedError struct {
	cause   error
	message string
}

func (e *redactedError) Error() string { return e.message }
func (e *redactedError) Unwrap() error { return e.cause }

func redactEmailError(err error, values ...string) error {
	message := err.Error()
	for _, value := range values {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	return &redactedError{cause: err, message: message}
}
