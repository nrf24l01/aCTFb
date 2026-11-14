package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/notifications"
	subscriber "github.com/nrf24l01/actfb/internal/notifications/nats"
	"github.com/nrf24l01/actfb/internal/platform/email"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
)

const (
	serviceShutdownTimeout = 10 * time.Second
	natsCloseTimeout       = 5 * time.Second
)

var requiredTemplates = []string{
	"verification/subject.tmpl",
	"verification/body.html.tmpl",
	"verification/body.txt.tmpl",
	"password_reset/subject.tmpl",
	"password_reset/body.html.tmpl",
	"password_reset/body.txt.tmpl",
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if err := run(context.Background(), logger); err != nil {
		logger.Error("notification worker stopped with error", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, logger *slog.Logger) (runErr error) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := validateNotificationConfig(cfg); err != nil {
		return err
	}

	natsClient, err := platformnats.New(cfg.Datasources.Nats)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}

	emailClient, err := email.NewEmailClient(ctx, &cfg.Email)
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), natsCloseTimeout)
		defer cancel()
		closeErr := natsClient.Close(closeCtx)
		return errors.Join(fmt.Errorf("create email client: %w", err), closeErr)
	}

	service := notifications.NewNotificationService(
		emailClient,
		subscriber.NewEmailTaskSubscriber(natsClient),
		notifications.NewFileTemplateRenderer(cfg.Email.TemplatesDir),
	)

	defer func() {
		logger.Info("stopping notification worker")

		serviceCtx, serviceCancel := context.WithTimeout(context.Background(), serviceShutdownTimeout)
		defer serviceCancel()
		if err := service.Shutdown(serviceCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("shutdown notification service: %w", err))
		}

		natsCtx, natsCancel := context.WithTimeout(context.Background(), natsCloseTimeout)
		defer natsCancel()
		if err := natsClient.Close(natsCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close NATS: %w", err))
		}

		if err := emailClient.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close email client: %w", err))
		}

		logger.Info("notification worker stopped")
	}()

	if err := service.Start(ctx, logger); err != nil {
		return fmt.Errorf("start notification service: %w", err)
	}

	logger.Info("notification worker ready",
		"stream", "EMAIL_TASKS",
		"subject", "email.tasks",
		"nats_host", cfg.Datasources.Nats.Host,
		"smtp_host", cfg.Email.SMTP.Host,
		"smtp_port", cfg.Email.SMTP.Port,
	)

	<-ctx.Done()
	logger.Info("shutdown signal received", "reason", context.Cause(ctx))

	return nil
}

func validateNotificationConfig(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if !cfg.Email.Enabled {
		return errors.New("SMTP is disabled (SMTP_ENABLED=false); notification worker has nothing to send")
	}
	if strings.TrimSpace(cfg.Email.SMTP.Host) == "" {
		return errors.New("SMTP host is empty; set SMTP_HOST")
	}
	if cfg.Email.SMTP.Port < 1 || cfg.Email.SMTP.Port > 65535 {
		return fmt.Errorf("SMTP port %d is outside the valid range 1..65535", cfg.Email.SMTP.Port)
	}
	if strings.TrimSpace(cfg.Email.SMTP.From) == "" {
		return errors.New("SMTP from address is empty; set SMTP_FROM")
	}
	if (cfg.Email.SMTP.User == "") != (cfg.Email.SMTP.Password == "") {
		return errors.New("SMTP user and password must be specified together")
	}
	if strings.TrimSpace(cfg.Email.TemplatesDir) == "" {
		return errors.New("email templates directory is empty; set SMTP_TEMPLATES_DIR")
	}

	info, err := os.Stat(cfg.Email.TemplatesDir)
	if err != nil {
		return fmt.Errorf("email templates directory %q: %w", cfg.Email.TemplatesDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("email templates path %q is not a directory", cfg.Email.TemplatesDir)
	}

	for _, template := range requiredTemplates {
		path := filepath.Join(cfg.Email.TemplatesDir, template)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("required email template %q: %w", path, err)
		}
		if info.IsDir() {
			return fmt.Errorf("required email template %q is a directory", path)
		}
	}

	return nil
}
