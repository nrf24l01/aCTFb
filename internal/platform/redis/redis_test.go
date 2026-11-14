package redis

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/nrf24l01/actfb/internal/config"
)

func TestRedisClientCheckAndClose(t *testing.T) {
	addr := os.Getenv("ACTFB_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set ACTFB_TEST_REDIS_ADDR to run the real Redis client test")
	}

	host, portRaw, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("ACTFB_TEST_REDIS_ADDR = %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatalf("ACTFB_TEST_REDIS_ADDR port = %q: %v", portRaw, err)
	}

	client, err := NewRedisClient(&config.RedisConfig{Host: host, Port: port})
	if err != nil {
		t.Fatalf("NewRedisClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Check(ctx); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if client.Client() == nil {
		t.Fatal("Client() = nil, want shared client")
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := client.Check(ctx); err == nil {
		t.Fatal("Check() after Close() error = nil, want closed-client error")
	}
}
