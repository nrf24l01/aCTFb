package ratelimit

import (
	"context"
	"sync"
	"time"
)

type memoryBucket struct {
	windowStart time.Time
	lastSeen    time.Time
	window      time.Duration
	count       int
}

// MemoryLimiter is the single-process fallback and default development backend.
// Keys are hashed and the map is bounded to prevent unbounded growth.
type MemoryLimiter struct {
	mu      sync.Mutex
	buckets map[string]*memoryBucket
	maxKeys int
	prefix  string
	now     func() time.Time
}

var (
	_ Limiter  = (*MemoryLimiter)(nil)
	_ Resetter = (*MemoryLimiter)(nil)
)

func NewMemoryLimiter(maxKeys int) *MemoryLimiter {
	if maxKeys < 1 {
		maxKeys = DefaultMemoryMaxKeys
	}

	return &MemoryLimiter{
		buckets: make(map[string]*memoryBucket),
		maxKeys: maxKeys,
		prefix:  DefaultPrefix,
		now:     time.Now,
	}
}

func (l *MemoryLimiter) Allow(
	_ context.Context,
	key string,
	limit int,
	window time.Duration,
) (bool, time.Duration, error) {
	if l == nil {
		return false, 0, errNilLimiter
	}
	if err := validateRequest(key, limit, window); err != nil {
		return false, 0, err
	}

	now := l.now()
	hashed := hashedKey(l.prefix, key)

	l.mu.Lock()
	defer l.mu.Unlock()

	if bucket, ok := l.buckets[hashed]; ok {
		bucket.lastSeen = now
		if bucket.window != window || now.Sub(bucket.windowStart) >= bucket.window {
			bucket.windowStart = now
			bucket.window = window
			bucket.count = 0
		}
		if bucket.count >= limit {
			return false, retryAfter(bucket, now), nil
		}
		bucket.count++
		return true, 0, nil
	}

	l.pruneLocked(now)
	if len(l.buckets) >= l.maxKeys {
		l.evictOldestLocked()
	}

	l.buckets[hashed] = &memoryBucket{
		windowStart: now,
		lastSeen:    now,
		window:      window,
		count:       1,
	}

	return true, 0, nil
}

func (l *MemoryLimiter) Reset(_ context.Context, key string) error {
	if l == nil {
		return errNilLimiter
	}
	if key == "" {
		return nil
	}

	l.mu.Lock()
	delete(l.buckets, hashedKey(l.prefix, key))
	l.mu.Unlock()

	return nil
}

func (l *MemoryLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func (l *MemoryLimiter) pruneLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.lastSeen) > 2*bucket.window {
			delete(l.buckets, key)
		}
	}
}

func (l *MemoryLimiter) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time

	for key, bucket := range l.buckets {
		if oldestKey == "" || bucket.lastSeen.Before(oldest) {
			oldestKey = key
			oldest = bucket.lastSeen
		}
	}

	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

func retryAfter(bucket *memoryBucket, now time.Time) time.Duration {
	remaining := bucket.windowStart.Add(bucket.window).Sub(now)
	if remaining <= 0 {
		return time.Millisecond
	}
	return remaining
}
