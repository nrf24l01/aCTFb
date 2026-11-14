// Command ad-worker executes durable Attack+Defense runs.
//
// It is intentionally a separate process from the HTTP backend. The backend
// never executes participant code and never needs the Docker socket, the Git
// binary or integration credentials in memory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/nrf24l01/actfb/internal/attackdefense"
	"github.com/nrf24l01/actfb/internal/config"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

const (
	startupTimeout  = 30 * time.Second
	shutdownTimeout = 20 * time.Second
	runTimeoutCap   = 2 * time.Hour
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("ad worker stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("ad worker stopped")
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if strings.TrimSpace(cfg.Deployment.CredentialKey) == "" {
		return errors.New("deployment.credential_key (legacy ACTFB_CREDENTIAL_ENCRYPTION_KEY) is required to decrypt Git integration credentials")
	}
	sealer, err := secrets.NewCipher(cfg.Deployment.CredentialKey, cfg.Deployment.KeyVersion)
	if err != nil {
		return fmt.Errorf("construct credential sealer: %w", err)
	}
	allowLoopback := cfg.AttackDefense.AllowInsecureLoopback
	allowlist := cfg.AttackDefense.RunnerImages
	if len(allowlist) == 0 {
		return errors.New("attack_defense.runner_images (legacy ACTFB_AD_RUNNER_IMAGES) must list at least one pinned sha256 image digest")
	}
	concurrency := cfg.AttackDefense.WorkerConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 8 {
		concurrency = 8
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

	natsClient, err := platformnats.New(cfg.Datasources.Nats)
	if err != nil {
		return fmt.Errorf("connect NATS: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if closeErr := natsClient.Close(closeCtx); closeErr != nil {
			logger.Error("close NATS", "error", closeErr)
		}
	}()
	if err := attackdefense.EnsureJobStream(startupCtx, natsClient); err != nil {
		return fmt.Errorf("ensure %s stream: %w", attackdefense.JobStreamName, err)
	}
	publisher, err := attackdefense.NewNATSJobPublisher(natsClient)
	if err != nil {
		return err
	}

	checker, err := attackdefense.NewDockerChecker(attackdefense.CheckerConfig{
		DockerBinary:   cfg.AttackDefense.DockerBinary,
		ImageAllowlist: allowlist,
		CPUs:           cfg.AttackDefense.CheckerCPUs,
		Memory:         cfg.AttackDefense.CheckerMemory,
		PIDs:           cfg.AttackDefense.CheckerPIDs,
		TmpfsSize:      cfg.AttackDefense.CheckerTmpfs,
	})
	if err != nil {
		return err
	}
	fetcher := attackdefense.NewGitFetcher(attackdefense.GitFetcherConfig{
		GitBinary:             cfg.AttackDefense.GitBinary,
		AllowInsecureLoopback: allowLoopback,
		Timeout:               cfg.AttackDefense.GitTimeout,
	})
	service, err := attackdefense.NewService(db, nil, sealer,
		attackdefense.WithGitProviderFactory(&attackdefense.GitProviderFactory{AllowInsecureLoopback: allowLoopback}),
		attackdefense.WithChecker(checker),
		attackdefense.WithGitFetcher(fetcher),
		attackdefense.WithRunnerImageAllowlist(allowlist),
		attackdefense.WithJobPublisher(publisher),
		attackdefense.WithAllowInsecureWebhookTransport(allowLoopback),
		attackdefense.WithLogger(logger),
	)
	if err != nil {
		return err
	}
	worker, err := attackdefense.NewWorker(service)
	if err != nil {
		return err
	}

	logger.Info("ad worker started",
		"stream", attackdefense.JobStreamName, "durable", attackdefense.JobConsumer,
		"concurrency", concurrency, "images", len(allowlist))

	semaphore := make(chan struct{}, concurrency)
	handler := func(msg jetstream.Msg) {
		var job attackdefense.RunJob
		if err := json.Unmarshal(msg.Data(), &job); err != nil || job.RunID.String() == "" {
			_ = msg.Term()
			logger.Warn("ad worker dropped malformed job message")
			return
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			_ = msg.NakWithDelay(5 * time.Second)
			return
		}
		go func() {
			defer func() { <-semaphore }()
			runCtx, cancel := context.WithTimeout(ctx, runTimeoutCap)
			defer cancel()
			if _, err := worker.RunOnce(runCtx, job.RunID); err != nil {
				// Never claim success for a technical failure, but do not
				// poison the durable consumer: reconciliation re-reads the
				// database and delivers the run again.
				logger.Error("ad worker run failed", "run", job.RunID, "error", err)
			}
			_ = msg.Ack()
		}()
	}
	consumer, err := natsClient.SubscribeJetStream(startupCtx, attackdefense.JobStreamName, jetstream.ConsumerConfig{
		Durable:       attackdefense.JobConsumer,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxAckPending: concurrency * 4,
	}, handler)
	if err != nil {
		return fmt.Errorf("subscribe AD jobs: %w", err)
	}
	defer consumer.Stop()

	interval := cfg.AttackDefense.ReconcileInterval
	reconcile := func() {
		reconcileCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if _, err := worker.ProcessQueued(reconcileCtx, concurrency*4); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("ad worker reconciliation failed", "error", err)
		}
	}
	reconcile()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			reconcile()
		}
	}
}
