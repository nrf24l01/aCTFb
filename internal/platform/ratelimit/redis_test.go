package ratelimit

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func redisTestClient(t *testing.T) (*redis.Client, string) {
	t.Helper()

	addr := os.Getenv("ACTFB_TEST_REDIS_ADDR")
	if strings.TrimSpace(addr) == "" {
		t.Skip("set ACTFB_TEST_REDIS_ADDR to run real Redis limiter tests")
	}

	db := 0
	if raw := os.Getenv("ACTFB_TEST_REDIS_DB"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("ACTFB_TEST_REDIS_DB = %q: %v", raw, err)
		}
		db = value
	}

	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     os.Getenv("ACTFB_TEST_REDIS_PASSWORD"),
		DB:           db,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("ping test redis at %s: %v", addr, err)
	}

	prefix := fmt.Sprintf(
		"actfb:test:%d:%s:",
		time.Now().UnixNano(),
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()),
	)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		keys, err := client.Keys(ctx, prefix+"*").Result()
		if err == nil && len(keys) > 0 {
			_ = client.Del(ctx, keys...).Err()
		}
		_ = client.Close()
	})

	return client, prefix
}

func TestRedisLimiterSharedQuotaAcrossInstances(t *testing.T) {
	client, prefix := redisTestClient(t)
	ctx := context.Background()

	first := NewRedisLimiter(client, prefix)
	second := NewRedisLimiter(client, prefix)

	for i := 0; i < 3; i++ {
		limiter := first
		if i%2 == 1 {
			limiter = second
		}
		allowed, retry, err := limiter.Allow(ctx, "shared-user", 3, time.Minute)
		if err != nil || !allowed || retry != 0 {
			t.Fatalf("Allow #%d = (%t, %s, %v), want allowed", i+1, allowed, retry, err)
		}
	}

	allowed, retry, err := second.Allow(ctx, "shared-user", 3, time.Minute)
	if err != nil {
		t.Fatalf("fourth Allow error = %v", err)
	}
	if allowed || retry <= 0 || retry > time.Minute {
		t.Fatalf("fourth Allow = (%t, %s), want denied with bounded retry", allowed, retry)
	}
}

func TestRedisLimiterTTLExpiryAndReset(t *testing.T) {
	client, prefix := redisTestClient(t)
	ctx := context.Background()
	limiter := NewRedisLimiter(client, prefix)

	allowed, _, err := limiter.Allow(ctx, "expiry", 1, 300*time.Millisecond)
	if err != nil || !allowed {
		t.Fatalf("first Allow = (%t, %v), want allowed", allowed, err)
	}

	allowed, _, err = limiter.Allow(ctx, "expiry", 1, 300*time.Millisecond)
	if err != nil || allowed {
		t.Fatalf("second Allow = (%t, %v), want denied", allowed, err)
	}

	time.Sleep(400 * time.Millisecond)

	allowed, _, err = limiter.Allow(ctx, "expiry", 1, 300*time.Millisecond)
	if err != nil || !allowed {
		t.Fatalf("Allow after TTL = (%t, %v), want allowed", allowed, err)
	}

	allowed, _, err = limiter.Allow(ctx, "reset", 1, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("first reset Allow = (%t, %v), want allowed", allowed, err)
	}
	allowed, _, err = limiter.Allow(ctx, "reset", 1, time.Minute)
	if err != nil || allowed {
		t.Fatalf("second reset Allow = (%t, %v), want denied", allowed, err)
	}
	if err := limiter.Reset(ctx, "reset"); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	allowed, _, err = limiter.Allow(ctx, "reset", 1, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("Allow after Reset = (%t, %v), want allowed", allowed, err)
	}
}

func TestRedisLimiterAtomicUnderConcurrency(t *testing.T) {
	client, prefix := redisTestClient(t)
	ctx := context.Background()
	limiter := NewRedisLimiter(client, prefix)
	const (
		limit      = 10
		goroutines = 100
	)

	var allowedCount atomic.Int64
	var errorCount atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, err := limiter.Allow(ctx, "race-key", limit, time.Minute)
			if err != nil {
				errorCount.Add(1)
				return
			}
			if allowed {
				allowedCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if errors := errorCount.Load(); errors != 0 {
		t.Fatalf("concurrent Allow errors = %d, want 0", errors)
	}
	if got := allowedCount.Load(); got != limit {
		t.Fatalf("allowed count = %d, want exactly %d", got, limit)
	}
}

func TestRedisLimiterKeysAreHashedAndNamespaced(t *testing.T) {
	client, prefix := redisTestClient(t)
	ctx := context.Background()
	limiter := NewRedisLimiter(client, prefix)
	const rawKey = "user@example.com"

	if allowed, _, err := limiter.Allow(ctx, rawKey, 1, time.Minute); err != nil || !allowed {
		t.Fatalf("Allow() = (%t, %v), want allowed", allowed, err)
	}

	keys, err := client.Keys(ctx, prefix+"*").Result()
	if err != nil {
		t.Fatalf("Keys() error = %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("redis keys = %v, want one key", keys)
	}
	if strings.Contains(keys[0], rawKey) {
		t.Fatalf("redis key leaked raw identifier: %q", keys[0])
	}
	if len(keys[0]) != len(prefix)+64 {
		t.Fatalf("redis key %q length = %d, want prefix + 64 hex chars", keys[0], len(keys[0]))
	}
}

func TestRedisLimiterFailsClosedWhenRedisUnavailable(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
		MaxRetries:   -1,
	})
	t.Cleanup(func() { _ = client.Close() })

	limiter := NewRedisLimiter(client, "actfb:test:outage:")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	allowed, _, err := limiter.Allow(ctx, "key", 1, time.Minute)
	if err == nil {
		t.Fatal("Allow() error = nil, want Redis outage error")
	}
	if allowed {
		t.Fatal("Allow() allowed = true during Redis outage, want fail closed")
	}
}

func TestRedisLimiterValidatesWindowAndKey(t *testing.T) {
	client, prefix := redisTestClient(t)
	limiter := NewRedisLimiter(client, prefix)
	ctx := context.Background()

	if _, _, err := limiter.Allow(ctx, "", 1, time.Minute); err == nil {
		t.Fatal("empty key error = nil, want validation error")
	}
	if _, _, err := limiter.Allow(ctx, "key", 1, MaxWindow+time.Second); err == nil {
		t.Fatal("oversized window error = nil, want validation error")
	}
	if _, _, err := NewRedisLimiter(nil, prefix).Allow(ctx, "key", 1, time.Minute); err == nil {
		t.Fatal("nil client error = nil, want initialization error")
	}
}
