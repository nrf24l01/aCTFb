package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultPrefix namespaces auth limiter keys. Callers may provide a
	// deployment-specific prefix, but raw client identifiers are never used as
	// Redis keys.
	DefaultPrefix = "actfb:ratelimit:v1:"

	// DefaultMemoryMaxKeys bounds the in-process fallback map.
	DefaultMemoryMaxKeys = 4096

	// MaxWindow bounds TTLs so a configuration mistake cannot create
	// effectively permanent limiter keys.
	MaxWindow = 24 * time.Hour
)

// Limiter is the fixed-window contract used by auth handlers.
//
// A nil error with allowed=false means the quota is exhausted; retryAfter is
// the remaining window. Any non-nil error means the limiter failed and the auth
// path must fail closed (HTTP 500).
type Limiter interface {
	Allow(
		ctx context.Context,
		key string,
		limit int,
		window time.Duration,
	) (allowed bool, retryAfter time.Duration, err error)
}

// Resetter is implemented by production limiter backends so successful account
// actions can clear the relevant bucket. It is optional at the Limiter
// interface level to keep auth handler unit tests simple.
type Resetter interface {
	Reset(ctx context.Context, key string) error
}

var errNilLimiter = errors.New("ratelimit: limiter is nil")

func hashedKey(prefix, key string) string {
	prefix = normalizePrefix(prefix)
	sum := sha256.Sum256([]byte(key))
	return prefix + hex.EncodeToString(sum[:])
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = DefaultPrefix
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	return prefix
}

func validateRequest(key string, limit int, window time.Duration) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("ratelimit: key is empty")
	}
	if limit < 1 {
		return fmt.Errorf("ratelimit: limit must be positive, got %d", limit)
	}
	if window < time.Millisecond {
		return fmt.Errorf("ratelimit: window must be at least 1ms, got %s", window)
	}
	if window > MaxWindow {
		return fmt.Errorf("ratelimit: window must not exceed %s, got %s", MaxWindow, window)
	}
	return nil
}
