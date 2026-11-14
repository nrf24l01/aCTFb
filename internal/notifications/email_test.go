package notifications

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	emailv1 "github.com/nrf24l01/actfb/internal/generated/proto/emails"
	subnats "github.com/nrf24l01/actfb/internal/notifications/nats"
)

type sentEmail struct {
	to       string
	subject  string
	textBody string
	htmlBody string
}

type fakeEmailClient struct {
	mu      sync.Mutex
	sent    []sentEmail
	sendErr error
}

type fakeRenderer struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeRenderer) Render(name string, data interface{}) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, name)
	if f.err != nil {
		return "", f.err
	}

	switch templateData := data.(type) {
	case VerificationEmailData:
		switch name {
		case "verification/subject.tmpl":
			return fmt.Sprintf("Verify %s", templateData.Username), nil
		case "verification/body.txt.tmpl":
			return fmt.Sprintf("Hello %s, your code is %s", templateData.Username, templateData.Code), nil
		case "verification/body.html.tmpl":
			return fmt.Sprintf("<p>Hello %s, your code is %s</p>", templateData.Username, templateData.Code), nil
		default:
			return "", fmt.Errorf("unexpected template %q", name)
		}
	case PasswordResetEmailData:
		switch name {
		case "password_reset/subject.tmpl":
			return fmt.Sprintf("Reset %s", templateData.Username), nil
		case "password_reset/body.txt.tmpl":
			return fmt.Sprintf("Hello %s, reset token %s", templateData.Username, templateData.Token), nil
		case "password_reset/body.html.tmpl":
			return fmt.Sprintf("<p>Hello %s, reset token %s</p>", templateData.Username, templateData.Token), nil
		default:
			return "", fmt.Errorf("unexpected template %q", name)
		}
	default:
		return "", fmt.Errorf("unexpected template data type %T", data)
	}
}

func (f *fakeEmailClient) SendEmail(_ context.Context, to, subject, textBody, htmlBody string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.sendErr != nil {
		return f.sendErr
	}

	f.sent = append(f.sent, sentEmail{
		to:       to,
		subject:  subject,
		textBody: textBody,
		htmlBody: htmlBody,
	})

	return nil
}

type fakeConsumeContext struct {
	drainOnce sync.Once
	drained   chan struct{}
	stopped   chan struct{}
	closed    chan struct{}
}

func newFakeConsumeContext() *fakeConsumeContext {
	return &fakeConsumeContext{
		drained: make(chan struct{}),
		stopped: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (f *fakeConsumeContext) Stop() {
	select {
	case <-f.stopped:
	default:
		close(f.stopped)
	}
}

func (f *fakeConsumeContext) Drain() {
	f.drainOnce.Do(func() {
		close(f.drained)
		close(f.closed)
	})
}

func (f *fakeConsumeContext) Closed() <-chan struct{} {
	return f.closed
}

type fakeTaskSubscriber struct {
	streamsCreated int
	handler        func(context.Context, *emailv1.EmailTask) error
	consume        *fakeConsumeContext
	subscribeErr   error
}

func (f *fakeTaskSubscriber) CreateStreams(context.Context) error {
	f.streamsCreated++
	return nil
}

func (f *fakeTaskSubscriber) SubscribeEmailTasks(
	_ context.Context,
	handler func(context.Context, *emailv1.EmailTask) error,
) (jetstream.ConsumeContext, error) {
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}

	f.handler = handler
	if f.consume == nil {
		f.consume = newFakeConsumeContext()
	}

	return f.consume, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func verificationTask(to, username, code string) *emailv1.EmailTask {
	return &emailv1.EmailTask{
		Id: "task-1",
		To: to,
		Payload: &emailv1.EmailTask_Verification{
			Verification: &emailv1.VerificationEmail{
				Username: username,
				Code:     code,
			},
		},
	}
}

func passwordResetTask(to, username, token string) *emailv1.EmailTask {
	return &emailv1.EmailTask{
		Id: "task-reset-1",
		To: to,
		Payload: &emailv1.EmailTask_PasswordReset{
			PasswordReset: &emailv1.PasswordResetEmail{
				Username: username,
				Token:    token,
			},
		},
	}
}

func TestStartSendsVerificationEmail(t *testing.T) {
	emailClient := &fakeEmailClient{}
	renderer := &fakeRenderer{}
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(emailClient, taskSubscriber, renderer)

	if err := service.Start(context.Background(), testLogger()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if taskSubscriber.streamsCreated != 1 {
		t.Fatalf("streams created = %d, want 1", taskSubscriber.streamsCreated)
	}
	if taskSubscriber.handler == nil {
		t.Fatal("email task handler was not registered")
	}

	task := verificationTask("learner@example.test", "learner", "123456")
	if err := taskSubscriber.handler(context.Background(), task); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	emailClient.mu.Lock()
	defer emailClient.mu.Unlock()

	if len(emailClient.sent) != 1 {
		t.Fatalf("sent emails = %d, want 1", len(emailClient.sent))
	}

	got := emailClient.sent[0]
	if got.to != task.To {
		t.Errorf("to = %q, want %q", got.to, task.To)
	}
	if got.subject != "Verify learner" {
		t.Errorf("subject = %q, want %q", got.subject, "Verify learner")
	}
	if !strings.Contains(got.textBody, "123456") {
		t.Errorf("text body does not contain verification code: %q", got.textBody)
	}
	if !strings.Contains(got.htmlBody, "123456") {
		t.Errorf("html body does not contain verification code: %q", got.htmlBody)
	}

	wantTemplates := []string{
		"verification/subject.tmpl",
		"verification/body.html.tmpl",
		"verification/body.txt.tmpl",
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if len(renderer.calls) != len(wantTemplates) {
		t.Fatalf("templates rendered = %v, want %v", renderer.calls, wantTemplates)
	}
	for index, want := range wantTemplates {
		if renderer.calls[index] != want {
			t.Errorf("template[%d] = %q, want %q", index, renderer.calls[index], want)
		}
	}
}

func TestStartSendsPasswordResetEmail(t *testing.T) {
	emailClient := &fakeEmailClient{}
	renderer := &fakeRenderer{}
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(emailClient, taskSubscriber, renderer)

	if err := service.Start(context.Background(), testLogger()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	token := "opaque-password-reset-token-1234567890"
	task := passwordResetTask("learner@example.test", "learner", token)
	if err := taskSubscriber.handler(context.Background(), task); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	emailClient.mu.Lock()
	defer emailClient.mu.Unlock()

	if len(emailClient.sent) != 1 {
		t.Fatalf("sent emails = %d, want 1", len(emailClient.sent))
	}
	got := emailClient.sent[0]
	if got.to != task.To {
		t.Errorf("to = %q, want %q", got.to, task.To)
	}
	if got.subject != "Reset learner" {
		t.Errorf("subject = %q, want %q", got.subject, "Reset learner")
	}
	if !strings.Contains(got.textBody, token) || !strings.Contains(got.htmlBody, token) {
		t.Errorf("password reset bodies do not contain token")
	}

	wantTemplates := []string{
		"password_reset/subject.tmpl",
		"password_reset/body.html.tmpl",
		"password_reset/body.txt.tmpl",
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if len(renderer.calls) != len(wantTemplates) {
		t.Fatalf("templates rendered = %v, want %v", renderer.calls, wantTemplates)
	}
	for index, want := range wantTemplates {
		if renderer.calls[index] != want {
			t.Errorf("template[%d] = %q, want %q", index, renderer.calls[index], want)
		}
	}
}

func TestPasswordResetDoesNotLogRecipientOrToken(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	emailClient := &fakeEmailClient{}
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(emailClient, taskSubscriber, &fakeRenderer{})

	if err := service.Start(context.Background(), logger); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	to := "secret-recipient@example.test"
	token := "secret-reset-token-abcdefghijklmnopqrstuvwxyz"
	if err := taskSubscriber.handler(context.Background(), passwordResetTask(to, "learner", token)); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	logOutput := logs.String()
	if strings.Contains(logOutput, to) {
		t.Errorf("password reset logs contain recipient: %q", logOutput)
	}
	if strings.Contains(logOutput, token) {
		t.Errorf("password reset logs contain token: %q", logOutput)
	}
}

func TestHandlerRejectsEmptyPasswordResetToken(t *testing.T) {
	emailClient := &fakeEmailClient{}
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(emailClient, taskSubscriber, &fakeRenderer{})

	if err := service.Start(context.Background(), testLogger()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	err := taskSubscriber.handler(context.Background(), passwordResetTask("learner@example.test", "learner", ""))
	if !errors.Is(err, subnats.ErrPermanent) {
		t.Fatalf("handler() error = %v, want permanent empty-token error", err)
	}
}

func TestPasswordResetSendFailureIsTransient(t *testing.T) {
	sendErr := errors.New("smtp unavailable")
	emailClient := &fakeEmailClient{sendErr: sendErr}
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(emailClient, taskSubscriber, &fakeRenderer{})

	if err := service.Start(context.Background(), testLogger()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	err := taskSubscriber.handler(context.Background(), passwordResetTask(
		"learner@example.test",
		"learner",
		"opaque-reset-token",
	))
	if !errors.Is(err, sendErr) {
		t.Fatalf("handler() error = %v, want wrapped SMTP error", err)
	}
	if errors.Is(err, subnats.ErrPermanent) {
		t.Fatal("SMTP send failure was classified as permanent")
	}
}

func TestPasswordResetSendFailureDoesNotLeakSensitiveValues(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	to := "secret-recipient@example.test"
	token := "secret-reset-token-abcdefghijklmnopqrstuvwxyz"
	sendErr := fmt.Errorf("smtp rejected recipient %s with token %s", to, token)

	emailClient := &fakeEmailClient{sendErr: sendErr}
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(emailClient, taskSubscriber, &fakeRenderer{})
	if err := service.Start(context.Background(), logger); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	err := taskSubscriber.handler(context.Background(), passwordResetTask(to, "learner", token))
	if !errors.Is(err, sendErr) {
		t.Fatalf("handler() error = %v, want wrapped SMTP error", err)
	}
	if strings.Contains(err.Error(), to) || strings.Contains(err.Error(), token) {
		t.Fatalf("returned error leaked recipient/token: %v", err)
	}
	logOutput := logs.String()
	if strings.Contains(logOutput, to) || strings.Contains(logOutput, token) {
		t.Fatalf("logs leaked recipient/token: %q", logOutput)
	}
}

func TestHandlerReturnsErrorWhenEmailSendFails(t *testing.T) {
	sendErr := errors.New("smtp unavailable")
	emailClient := &fakeEmailClient{sendErr: sendErr}
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(emailClient, taskSubscriber, &fakeRenderer{})

	if err := service.Start(context.Background(), testLogger()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	err := taskSubscriber.handler(context.Background(), verificationTask("learner@example.test", "learner", "123456"))
	if !errors.Is(err, sendErr) {
		t.Fatalf("handler() error = %v, want wrapped %v", err, sendErr)
	}
}

func TestHandlerTreatsTemplateFailureAsPermanent(t *testing.T) {
	rendererErr := errors.New("template missing")
	emailClient := &fakeEmailClient{}
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(emailClient, taskSubscriber, &fakeRenderer{err: rendererErr})

	if err := service.Start(context.Background(), testLogger()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	err := taskSubscriber.handler(context.Background(), verificationTask("learner@example.test", "learner", "123456"))
	if !errors.Is(err, subnats.ErrPermanent) {
		t.Fatalf("handler() error = %v, want permanent template error", err)
	}
	if !errors.Is(err, rendererErr) {
		t.Fatalf("handler() error = %v, want wrapped renderer error", err)
	}

	emailClient.mu.Lock()
	defer emailClient.mu.Unlock()
	if len(emailClient.sent) != 0 {
		t.Fatalf("sent emails = %d, want 0", len(emailClient.sent))
	}
}

func TestHandlerRejectsMissingRecipient(t *testing.T) {
	emailClient := &fakeEmailClient{}
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(emailClient, taskSubscriber, &fakeRenderer{})

	if err := service.Start(context.Background(), testLogger()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	err := taskSubscriber.handler(context.Background(), verificationTask("", "learner", "123456"))
	if err == nil {
		t.Fatal("handler() error = nil, want validation error")
	}

	emailClient.mu.Lock()
	defer emailClient.mu.Unlock()
	if len(emailClient.sent) != 0 {
		t.Fatalf("sent emails = %d, want 0", len(emailClient.sent))
	}
}

func TestShutdownDrainsConsumer(t *testing.T) {
	taskSubscriber := &fakeTaskSubscriber{}
	service := NewNotificationService(&fakeEmailClient{}, taskSubscriber, &fakeRenderer{})

	if err := service.Start(context.Background(), testLogger()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	select {
	case <-taskSubscriber.consume.drained:
	default:
		t.Fatal("consumer was not drained")
	}

	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
}
