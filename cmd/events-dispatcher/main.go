package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nrf24l01/actfb/internal/attackdefense"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/events"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const (
	startupTimeout  = 30 * time.Second
	shutdownTimeout = 15 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("events dispatcher stopped with error", "error", err)
		os.Exit(1)
	}

	logger.Info("events dispatcher stopped")
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()

	db, err := postgres.NewPostgresClient(startupCtx, &cfg.Datasources.Postgres)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			logger.Error("close postgres", "error", closeErr)
		}
	}()

	natsClient, err := platformnats.New(cfg.Datasources.Nats)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if closeErr := natsClient.Close(closeCtx); closeErr != nil {
			logger.Error("close nats", "error", closeErr)
		}
	}()

	if err := events.EnsureStream(startupCtx, natsClient); err != nil {
		return err
	}

	registry := attackdefense.EventRegistry()
	publisher := events.NewNATSPublisher(natsClient, events.WithNATSPublisherRegistry(registry))
	dispatcher := events.NewDispatcher(db, publisher,
		events.WithDispatcherLogger(logger),
		events.WithDispatcherRegistry(registry),
	)

	logger.Info("events dispatcher started", "stream", events.StreamName)
	if err := dispatcher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}
