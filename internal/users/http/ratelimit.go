package http

import (
	"math"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	registerRateLimit            = 5
	registerRateWindow           = 15 * time.Minute
	loginRateLimit               = 10
	loginRateWindow              = time.Minute
	loginAccountRateLimit        = 10
	loginAccountRateWindow       = 15 * time.Minute
	refreshRateLimit             = 60
	refreshRateWindow            = time.Minute
	logoutRateLimit              = 30
	logoutRateWindow             = time.Minute
	emailRequestRateLimit        = 5
	emailRequestRateWindow       = 15 * time.Minute
	emailConfirmRateLimit        = 10
	emailConfirmRateWindow       = time.Minute
	passwordRateLimit            = 5
	passwordRateWindow           = 15 * time.Minute
	passwordResetRequestIPLimit  = 5
	passwordResetRequestIPWindow = 15 * time.Minute
	passwordResetEmailLimit      = 3
	passwordResetEmailWindow     = 15 * time.Minute
	passwordResetConfirmIPLimit  = 10
	passwordResetConfirmIPWindow = 15 * time.Minute
)

type rateBucket struct {
	windowStart time.Time
	lastSeen    time.Time
	window      time.Duration
	count       int
}

type authRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	maxKeys int
	now     func() time.Time
}

func newAuthRateLimiter(maxKeys int) *authRateLimiter {
	if maxKeys < 1 {
		maxKeys = 1
	}
	return &authRateLimiter{
		buckets: make(map[string]*rateBucket),
		maxKeys: maxKeys,
		now:     time.Now,
	}
}

// allow applies a fixed-window limit and returns the Retry-After seconds when
// rejected. The map is bounded; expired buckets are pruned and, if necessary,
// the least-recently-used bucket is evicted before a new key is admitted.
func (l *authRateLimiter) allow(key string, limit int, window time.Duration) (bool, int) {
	if limit < 1 || window <= 0 || key == "" {
		return true, 0
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if bucket, ok := l.buckets[key]; ok {
		bucket.lastSeen = now
		if now.Sub(bucket.windowStart) >= bucket.window {
			bucket.windowStart = now
			bucket.count = 0
		}
		if bucket.count >= limit {
			return false, retryAfterSeconds(bucket, now)
		}
		bucket.count++
		return true, 0
	}

	if len(l.buckets) >= l.maxKeys {
		l.cleanupLocked(now)
	}
	if len(l.buckets) >= l.maxKeys {
		l.evictOldestLocked()
	}

	l.buckets[key] = &rateBucket{
		windowStart: now,
		lastSeen:    now,
		window:      window,
		count:       1,
	}
	return true, 0
}

func (l *authRateLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

func (l *authRateLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func (l *authRateLimiter) cleanupLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.lastSeen) > 2*bucket.window {
			delete(l.buckets, key)
		}
	}
}

func (l *authRateLimiter) evictOldestLocked() {
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

func retryAfterSeconds(bucket *rateBucket, now time.Time) int {
	remaining := bucket.windowStart.Add(bucket.window).Sub(now)
	if remaining <= 0 {
		return 1
	}
	return int(math.Ceil(remaining.Seconds()))
}

// remoteIP uses only the transport peer address. X-Forwarded-For and similar
// client-controlled headers are deliberately ignored until a trusted proxy
// policy exists.
func remoteIP(remoteAddr string) string {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

func retryAfterCeilSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}
