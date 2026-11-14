package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryLimiterFixedWindowAndReset(t *testing.T) {
	ctx := context.Background()
	limiter := NewMemoryLimiter(10)

	allowed, retry, err := limiter.Allow(ctx, "user@example.com", 2, time.Minute)
	if err != nil || !allowed || retry != 0 {
		t.Fatalf("first Allow = (%t, %s, %v), want (true, 0, nil)", allowed, retry, err)
	}

	allowed, retry, err = limiter.Allow(ctx, "user@example.com", 2, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("second Allow = (%t, %s, %v), want allowed", allowed, retry, err)
	}

	allowed, retry, err = limiter.Allow(ctx, "user@example.com", 2, time.Minute)
	if err != nil || allowed || retry <= 0 {
		t.Fatalf("third Allow = (%t, %s, %v), want denied with retry", allowed, retry, err)
	}

	if err := limiter.Reset(ctx, "user@example.com"); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}

	allowed, _, err = limiter.Allow(ctx, "user@example.com", 2, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("Allow after Reset = (%t, %v), want allowed", allowed, err)
	}
}

func TestMemoryLimiterWindowExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1000, 0)
	limiter := NewMemoryLimiter(10)
	limiter.now = func() time.Time { return now }

	allowed, _, err := limiter.Allow(ctx, "ip", 1, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("first Allow = (%t, %v), want allowed", allowed, err)
	}

	allowed, _, err = limiter.Allow(ctx, "ip", 1, time.Minute)
	if err != nil || allowed {
		t.Fatalf("second Allow = (%t, %v), want denied", allowed, err)
	}

	now = now.Add(time.Minute)
	allowed, _, err = limiter.Allow(ctx, "ip", 1, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("Allow after window = (%t, %v), want allowed", allowed, err)
	}
}

func TestMemoryLimiterBoundedKeys(t *testing.T) {
	ctx := context.Background()
	limiter := NewMemoryLimiter(2)

	for _, key := range []string{"one", "two", "three"} {
		if allowed, _, err := limiter.Allow(ctx, key, 1, time.Minute); err != nil || !allowed {
			t.Fatalf("Allow(%q) = (%t, %v), want allowed", key, allowed, err)
		}
	}

	if got := limiter.size(); got > 2 {
		t.Fatalf("memory limiter retained %d keys, want <= 2", got)
	}
}

func TestMemoryLimiterConcurrent(t *testing.T) {
	ctx := context.Background()
	limiter := NewMemoryLimiter(10)
	const limit = 10

	var allowedCount atomic.Int64
	var errorCount atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, err := limiter.Allow(ctx, "same-key", limit, time.Minute)
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

func TestMemoryLimiterRejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	limiter := NewMemoryLimiter(10)

	tests := []struct {
		name   string
		key    string
		limit  int
		window time.Duration
	}{
		{name: "empty key", key: "", limit: 1, window: time.Minute},
		{name: "zero limit", key: "k", limit: 0, window: time.Minute},
		{name: "zero window", key: "k", limit: 1, window: 0},
		{name: "window too large", key: "k", limit: 1, window: MaxWindow + time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := limiter.Allow(ctx, tt.key, tt.limit, tt.window); err == nil {
				t.Fatal("Allow() error = nil, want validation error")
			}
		})
	}
}
