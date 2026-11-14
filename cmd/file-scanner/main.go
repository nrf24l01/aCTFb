package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/files"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	platforms3 "github.com/nrf24l01/actfb/internal/platform/s3"
)

const (
	startupTimeout      = 30 * time.Second
	shutdownTimeout     = 15 * time.Second
	scanTimeout         = 5 * time.Minute
	nakDelayTransient   = 10 * time.Second
	nakDelayUnavailable = 30 * time.Second
	maintenanceInterval = time.Minute
	maintenanceBatch    = 100
)

type scanRequest struct {
	FileUUID uuid.UUID `json:"fileUuid"`
}

type scanRunner interface {
	Scan(ctx context.Context, fileID uuid.UUID) error
}

type message interface {
	Ack() error
	NakWithDelay(time.Duration) error
	Term() error
	Data() []byte
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("file scanner stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("file scanner stopped")
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.Files.Enabled {
		return errors.New("files are disabled; set files.enabled=true and a scan key before starting the scanner")
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()

	db, err := postgres.NewPostgresClient(startupCtx, &cfg.Datasources.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			logger.Error("close postgres", "error", closeErr)
		}
	}()

	s3Client, err := platforms3.New(startupCtx, cfg.Datasources.S3)
	if err != nil {
		return fmt.Errorf("connect S3: %w", err)
	}
	if err := s3Client.Check(startupCtx); err != nil {
		return fmt.Errorf("check S3 bucket: %w", err)
	}

	natsClient, err := platformnats.New(cfg.Datasources.Nats)
	if err != nil {
		return fmt.Errorf("connect NATS: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if closeErr := natsClient.Close(closeCtx); closeErr != nil {
			logger.Error("close nats", "error", closeErr)
		}
	}()

	if err := files.EnsureScanStream(startupCtx, natsClient); err != nil {
		return fmt.Errorf("ensure %s stream: %w", files.ScanStreamName, err)
	}

	scanner, err := files.NewScanner(db, s3Client, files.ScannerConfig{
		RequireClamAV: cfg.Files.RequireClamAV,
		ClamAVAddress: cfg.Files.ClamAVAddress,
	})
	if err != nil {
		return err
	}
	maintenance, err := files.NewService(db, s3Client, nil)
	if err != nil {
		return err
	}

	go runMaintenance(ctx, maintenance, logger)

	consumeCtx, err := natsClient.SubscribeJetStream(startupCtx, files.ScanStreamName, jetstream.ConsumerConfig{
		Durable:       files.ScanConsumer,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       6 * time.Minute,
		MaxAckPending: 8,
		MaxDeliver:    -1,
		FilterSubject: files.ScanSubject,
	}, func(msg jetstream.Msg) {
		processMessage(ctx, msg, scanner, logger)
	})
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", files.ScanSubject, err)
	}
	defer consumeCtx.Stop()

	if cfg.Files.RequireClamAV && cfg.Files.ClamAVAddress == "" {
		logger.Error("ClamAV is required but files.clamav_address is empty; scans will remain VALIDATING")
	}
	if cfg.Files.ClamAVAddress == "" {
		logger.Warn("ClamAV address is empty; no file will become READY without a trusted clean engine")
	}

	logger.Info("file scanner ready",
		"stream", files.ScanStreamName,
		"consumer", files.ScanConsumer,
		"subject", files.ScanSubject,
		"clamav_configured", cfg.Files.ClamAVAddress != "",
	)

	<-ctx.Done()
	return nil
}

func processMessage(parent context.Context, msg message, scanner scanRunner, logger *slog.Logger) {
	var request scanRequest
	if err := json.Unmarshal(msg.Data(), &request); err != nil || request.FileUUID == uuid.Nil {
		// Malformed messages cannot become valid on redelivery.
		_ = msg.Term()
		return
	}
	ctx, cancel := context.WithTimeout(parent, scanTimeout)
	defer cancel()

	err := scanner.Scan(ctx, request.FileUUID)
	switch {
	case err == nil:
		if ackErr := msg.Ack(); ackErr != nil {
			logger.Error("ack scan message", "file_uuid", request.FileUUID, "error", ackErr)
		}
	case errors.Is(err, files.ErrFileNotFound):
		// The draft or attachment row was deleted/expired before the scan.
		if ackErr := msg.Ack(); ackErr != nil {
			logger.Error("ack missing scan message", "file_uuid", request.FileUUID, "error", ackErr)
		}
	case errors.Is(err, files.ErrScannerUnavailable):
		// The object remains VALIDATING. Never fake READY or REJECTED because
		// ClamAV is temporarily unreachable.
		logger.Warn("scanner unavailable; retrying", "file_uuid", request.FileUUID)
		if nakErr := msg.NakWithDelay(nakDelayUnavailable); nakErr != nil {
			logger.Error("nak scan message", "file_uuid", request.FileUUID, "error", nakErr)
		}
	default:
		logger.Error("scan attempt failed; retrying", "file_uuid", request.FileUUID, "error", err)
		if nakErr := msg.NakWithDelay(nakDelayTransient); nakErr != nil {
			logger.Error("nak scan message", "file_uuid", request.FileUUID, "error", nakErr)
		}
	}
}

func runMaintenance(ctx context.Context, service *files.FileService, logger *slog.Logger) {
	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			count, err := service.ExpireDrafts(runCtx, maintenanceBatch)
			cancel()
			if err != nil {
				logger.Error("expire drafts failed", "error", err)
				continue
			}
			if count > 0 {
				logger.Info("expired submission drafts", "count", count)
			}
		}
	}
}
