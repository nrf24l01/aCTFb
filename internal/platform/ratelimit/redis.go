package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisAllowScript = `
local current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
if current > tonumber(ARGV[2]) then
  return {0, ttl}
end
return {1, ttl}
`

// RedisLimiter is the shared production backend. It uses one atomic Lua script
// for INCR + PEXPIRE/PTTL so multiple instances share a strict fixed-window
// quota. Keys are namespaced and SHA-256 hashed; raw IPs, usernames, and emails
// never appear in Redis.
type RedisLimiter struct {
	client redis.UniversalClient
	prefix string
}

var (
	_ Limiter  = (*RedisLimiter)(nil)
	_ Resetter = (*RedisLimiter)(nil)
)

func NewRedisLimiter(client redis.UniversalClient, prefix string) *RedisLimiter {
	return &RedisLimiter{
		client: client,
		prefix: normalizePrefix(prefix),
	}
}

func (l *RedisLimiter) Allow(
	ctx context.Context,
	key string,
	limit int,
	window time.Duration,
) (bool, time.Duration, error) {
	if l == nil || l.client == nil {
		return false, 0, errors.New("ratelimit: redis client is not initialized")
	}
	if err := validateRequest(key, limit, window); err != nil {
		return false, 0, err
	}

	windowMS := window.Milliseconds()
	if windowMS < 1 {
		windowMS = 1
	}

	result, err := l.client.Eval(
		ctx,
		redisAllowScript,
		[]string{hashedKey(l.prefix, key)},
		windowMS,
		limit,
	).Result()
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit: redis allow: %w", err)
	}

	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return false, 0, errors.New("ratelimit: unexpected redis response")
	}

	allowedValue, ok := values[0].(int64)
	if !ok {
		return false, 0, errors.New("ratelimit: invalid redis allowed flag")
	}

	retryValue, ok := values[1].(int64)
	if !ok {
		return false, 0, errors.New("ratelimit: invalid redis TTL")
	}

	if allowedValue == 1 {
		return true, 0, nil
	}

	retry := time.Duration(retryValue) * time.Millisecond
	if retry <= 0 {
		retry = window
	}

	return false, retry, nil
}

func (l *RedisLimiter) Reset(ctx context.Context, key string) error {
	if l == nil || l.client == nil {
		return errors.New("ratelimit: redis client is not initialized")
	}
	if key == "" {
		return nil
	}

	if err := l.client.Del(ctx, hashedKey(l.prefix, key)).Err(); err != nil {
		return fmt.Errorf("ratelimit: redis reset: %w", err)
	}

	return nil
}
