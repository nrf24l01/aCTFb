// Command instance-worker is the trusted execution worker for instance
// provisioning and the integration connection test.
//
// It is a separate process from the HTTP backend: only this process holds the
// container daemon socket and decrypts provider credentials. It consumes
// ids-only NATS jobs, rechecks authorization from live PostgreSQL state, and
// persists provider-confirmed inventory.
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

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/instances"
	"github.com/nrf24l01/actfb/internal/integrations"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/runtime/containerapi"
	"github.com/nrf24l01/actfb/internal/runtime/docker"
	"github.com/nrf24l01/actfb/internal/runtime/kubernetes"
	"github.com/nrf24l01/actfb/internal/runtime/kubevirt"
	"github.com/nrf24l01/actfb/internal/runtime/podman"
	"github.com/nrf24l01/actfb/internal/runtime/proxmox"
)

const (
	startupTimeout      = 30 * time.Second
	shutdownTimeout     = 20 * time.Second
	jobTimeout          = 15 * time.Minute
	reconcileInterval   = 30 * time.Second
	nakDelay            = 20 * time.Second
	reconcileBatch      = 100
	integrationTestTime = 15 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("instance worker stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("instance worker stopped")
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if strings.TrimSpace(cfg.Deployment.CredentialKey) == "" {
		return errors.New("ACTFB_CREDENTIAL_ENCRYPTION_KEY is required by the trusted instance worker")
	}
	sealer, err := secrets.NewCipher(cfg.Deployment.CredentialKey, cfg.Deployment.KeyVersion)
	if err != nil {
		return fmt.Errorf("construct credential sealer: %w", err)
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()

	db, err := postgres.NewPostgresClient(startupCtx, &cfg.Datasources.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer func() { _ = db.Close() }()

	natsClient, err := platformnats.New(cfg.Datasources.Nats)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = natsClient.Close(closeCtx)
	}()

	forwarders := containerapi.NewForwarderRegistry()
	defer forwarders.StopAll()
	dockerSocket := strings.TrimSpace(os.Getenv("ACTFB_INSTANCE_WORKER_DOCKER_SOCKET"))
	podmanSocket := strings.TrimSpace(os.Getenv("ACTFB_INSTANCE_WORKER_PODMAN_SOCKET"))
	dockerDriver, err := docker.New(docker.WithForwarders(forwarders), docker.WithSocket(dockerSocket))
	if err != nil {
		return err
	}
	podmanDriver, err := podman.New(podman.WithForwarders(forwarders), podman.WithSocket(podmanSocket))
	if err != nil {
		return err
	}
	registry, err := runtime.NewRegistry(
		dockerDriver,
		podmanDriver,
		kubernetes.New(),
		proxmox.New(),
		kubevirt.New(),
	)
	if err != nil {
		return fmt.Errorf("build driver registry: %w", err)
	}

	orchestrator, err := instances.NewOrchestrator(db, registry, authorization.NewAuthorizer(db), sealer, cfg.Deployment.KeyVersion,
		instances.WithOrchestratorLogger(logger))
	if err != nil {
		return err
	}

	if _, err := natsClient.CreateOrUpdateStream(startupCtx, jetstream.StreamConfig{
		Name:     instances.JobStream,
		Subjects: []string{instances.JobSubject},
		Storage:  jetstream.FileStorage,
	}); err != nil {
		return fmt.Errorf("ensure instance job stream: %w", err)
	}
	consumer, err := natsClient.SubscribeJetStream(startupCtx, instances.JobStream, jetstream.ConsumerConfig{
		Durable:       "instance-worker",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       jobTimeout + time.Minute,
		MaxDeliver:    -1,
		FilterSubject: instances.JobSubject,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}, func(message jetstream.Msg) {
		handleMessage(ctx, orchestrator, logger, message)
	})
	if err != nil {
		return fmt.Errorf("subscribe instance jobs: %w", err)
	}
	defer consumer.Stop()

	tester := &integrationTester{db: db, registry: registry, sealer: sealer, logger: logger}
	subscription, err := natsClient.Subscribe(integrations.TestSubject, func(message *nats.Msg) {
		tester.handle(ctx, message)
	})
	if err != nil {
		return fmt.Errorf("subscribe integration test: %w", err)
	}
	defer func() { _ = subscription.Unsubscribe() }()

	go reconcileLoop(ctx, orchestrator, logger)

	logger.Info("instance worker ready", "subject", instances.JobSubject, "drivers", len(registry.List()))
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = shutdownCtx
	return nil
}

func handleMessage(ctx context.Context, orchestrator *instances.Orchestrator, logger *slog.Logger, message jetstream.Msg) {
	var job instances.Job
	decoder := json.NewDecoder(strings.NewReader(string(message.Data())))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&job); err != nil {
		logger.Error("invalid instance job", "error", err.Error())
		_ = message.Term()
		return
	}
	jobCtx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()
	if err := orchestrator.HandleJob(jobCtx, job); err != nil {
		logger.Error("instance job failed", "instanceId", job.InstanceID.String(), "generation", job.Generation, "error", errorText(err))
		_ = message.NakWithDelay(nakDelay)
		return
	}
	if err := message.Ack(); err != nil {
		logger.Error("ack instance job", "error", err.Error())
	}
}

func reconcileLoop(ctx context.Context, orchestrator *instances.Orchestrator, logger *slog.Logger) {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if jobs, err := orchestrator.ExpiredJobs(ctx, reconcileBatch); err != nil {
				logger.Error("expire instances", "error", err.Error())
			} else {
				for _, job := range jobs {
					runCtx, cancel := context.WithTimeout(ctx, jobTimeout)
					if err := orchestrator.HandleJob(runCtx, job); err != nil {
						logger.Error("expired instance cleanup failed", "instanceId", job.InstanceID.String(), "error", errorText(err))
					}
					cancel()
				}
			}
			jobs, err := orchestrator.PendingJobs(ctx, reconcileBatch)
			if err != nil {
				logger.Error("reconcile instance jobs", "error", err.Error())
				continue
			}
			for _, job := range jobs {
				runCtx, cancel := context.WithTimeout(ctx, jobTimeout)
				if err := orchestrator.HandleJob(runCtx, job); err != nil {
					logger.Error("reconcile job failed", "instanceId", job.InstanceID.String(), "error", errorText(err))
				}
				cancel()
			}
		}
	}
}

// integrationTester performs the trusted, real connection test. Compute probes
// use the driver registry; Git uses a bounded authenticated HTTPS request. No
// provider response body is ever returned.
type integrationTester struct {
	db       *postgres.DB
	registry *runtime.DriverRegistry
	sealer   secrets.Sealer
	logger   *slog.Logger
}

func (t *integrationTester) handle(parent context.Context, message *nats.Msg) {
	var request struct {
		IntegrationID string `json:"integrationId"`
	}
	result := integrations.TestResult{OK: false, Message: "connection test failed"}
	if err := json.Unmarshal(message.Data, &request); err != nil {
		reply(message, result)
		return
	}
	ctx, cancel := context.WithTimeout(parent, integrationTestTime)
	defer cancel()
	if err := t.test(ctx, request.IntegrationID); err != nil {
		if t.logger != nil {
			t.logger.Warn("integration test failed", "integrationId", request.IntegrationID, "error", errorText(err))
		}
		result.Message = "provider connection test failed"
		reply(message, result)
		return
	}
	result = integrations.TestResult{OK: true, Message: "connection succeeded"}
	reply(message, result)
}

func reply(message *nats.Msg, result integrations.TestResult) {
	payload, _ := json.Marshal(result)
	_ = message.Respond(payload)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *instances.Error
	if errors.As(err, &apiErr) {
		return string(apiErr.Code)
	}
	return "provider error"
}
