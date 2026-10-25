package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/notifications"
	subscriber "github.com/nrf24l01/actfb/internal/notifications/nats"
	"github.com/nrf24l01/actfb/internal/platform/email"
	"github.com/nrf24l01/actfb/internal/platform/nats"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	ctx := context.Background()

	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	nats, err := nats.New(cfg.Datasources.Nats)
	if err != nil {
		logger.Error("Failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	emailTaskSubscriber := subscriber.NewEmailTaskSubscriber(nats)

	emailClient, err := email.NewEmailClient(ctx, &cfg.Email)
	if err != nil {
		logger.Error("Failed to create email client", "error", err)
		os.Exit(1)
	}

	notificationService := notifications.NewNotificationService(emailClient, emailTaskSubscriber)

	if err := notificationService.Start(ctx, logger); err != nil {
		logger.Error("Failed to start notification service", "error", err)
		os.Exit(1)
	}

	<-ctx.Done()
	logger.Info("Email service stopped")
}
