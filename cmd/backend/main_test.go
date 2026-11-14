package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/nrf24l01/actfb/internal/config"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	usersnats "github.com/nrf24l01/actfb/internal/users/nats"
)

func TestInitializeMailRuntimeGatesOnEmailEnabledNotVerification(t *testing.T) {
	cfg := &config.Config{}
	cfg.Email.Enabled = true
	cfg.Security.EmailVerifyEnabled = false
	cfg.Datasources.Nats = config.NatsConfig{Host: "nats", Port: 4222}

	client := &platformnats.Client{}
	var clientCreated, streamEnsured, publisherCreated bool

	deps := mailDeps{
		newClient: func(config.NatsConfig) (*platformnats.Client, error) {
			clientCreated = true
			return client, nil
		},
		ensureStream: func(context.Context, *platformnats.Client) error {
			streamEnsured = true
			return nil
		},
		newPublisher: func(natsClient *platformnats.Client) *usersnats.EmailTaskPublisher {
			publisherCreated = true
			return usersnats.NewEmailPublisher(natsClient)
		},
	}

	runtime, err := initializeMailRuntime(context.Background(), cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), deps)
	if err != nil {
		t.Fatalf("initializeMailRuntime() error = %v", err)
	}
	if !clientCreated || !streamEnsured || !publisherCreated {
		t.Fatalf("initialization flags: client=%t stream=%t publisher=%t, want all true",
			clientCreated, streamEnsured, publisherCreated)
	}
	if runtime.natsClient != client {
		t.Fatal("runtime did not retain the NATS client")
	}
	if runtime.publisher == nil {
		t.Fatal("runtime publisher = nil, want publisher when SMTP email is enabled")
	}
}

func TestInitializeMailRuntimeEmailDisabledDoesNotTouchNATS(t *testing.T) {
	cfg := &config.Config{}
	cfg.Email.Enabled = false
	cfg.Security.EmailVerifyEnabled = true

	var called bool
	deps := mailDeps{
		newClient: func(config.NatsConfig) (*platformnats.Client, error) {
			called = true
			return &platformnats.Client{}, nil
		},
		ensureStream: func(context.Context, *platformnats.Client) error {
			called = true
			return nil
		},
		newPublisher: func(*platformnats.Client) *usersnats.EmailTaskPublisher {
			called = true
			return nil
		},
	}

	runtime, err := initializeMailRuntime(context.Background(), cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), deps)
	if err != nil {
		t.Fatalf("initializeMailRuntime() error = %v", err)
	}
	if called {
		t.Fatal("email disabled must not initialize NATS or the publisher")
	}
	if runtime.natsClient != nil || runtime.publisher != nil {
		t.Fatal("email disabled runtime should be empty")
	}
}

func TestInitializeMailRuntimePropagatesNATSError(t *testing.T) {
	cfg := &config.Config{}
	cfg.Email.Enabled = true
	cfg.Security.EmailVerifyEnabled = false

	deps := mailDeps{
		newClient: func(config.NatsConfig) (*platformnats.Client, error) {
			return nil, errors.New("nats unavailable")
		},
		newPublisher: usersnats.NewEmailPublisher,
	}

	if _, err := initializeMailRuntime(context.Background(), cfg, nil, deps); err == nil {
		t.Fatal("initializeMailRuntime() error = nil, want NATS error")
	}
}

func TestNewAuthLimiterMemoryBackend(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.AuthRateLimitBackend = config.RateLimitBackendMemory

	limiter, redisClient, err := newAuthLimiter(cfg)
	if err != nil {
		t.Fatalf("newAuthLimiter() error = %v", err)
	}
	if limiter == nil {
		t.Fatal("newAuthLimiter() returned nil limiter")
	}
	if redisClient != nil {
		t.Fatal("memory backend should not create a Redis client")
	}
}

func TestNewAuthLimiterRejectsUnknownBackend(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.AuthRateLimitBackend = "unknown"

	if _, _, err := newAuthLimiter(cfg); err == nil {
		t.Fatal("newAuthLimiter() error = nil, want unsupported backend error")
	}
}

func TestNewAuthLimiterRedisBackend(t *testing.T) {
	addr := os.Getenv("ACTFB_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set ACTFB_TEST_REDIS_ADDR to run the real Redis startup test")
	}

	host, portRaw, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("ACTFB_TEST_REDIS_ADDR = %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatalf("ACTFB_TEST_REDIS_ADDR port = %q: %v", portRaw, err)
	}

	cfg := &config.Config{}
	cfg.Security.AuthRateLimitBackend = config.RateLimitBackendRedis
	cfg.Datasources.Redis = config.RedisConfig{Host: host, Port: port}

	limiter, redisClient, err := newAuthLimiter(cfg)
	if err != nil {
		t.Fatalf("newAuthLimiter() error = %v", err)
	}
	if limiter == nil || redisClient == nil {
		t.Fatalf("redis backend limiter/client = (%v, %v), want both non-nil", limiter, redisClient)
	}
	if err := redisClient.Close(); err != nil {
		t.Fatalf("close redis client: %v", err)
	}
}
