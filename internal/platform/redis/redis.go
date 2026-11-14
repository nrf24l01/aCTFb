package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/redis/go-redis/v9"
)

type RedisClient struct {
	client *redis.Client
}

func NewRedisClient(cfg *config.RedisConfig) (*RedisClient, error) {
	if cfg == nil {
		return nil, errors.New("redis config is nil")
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Host + fmt.Sprintf(":%d", cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,

		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()

		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	return &RedisClient{
		client: client,
	}, nil
}

func (r *RedisClient) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

// Client exposes the underlying go-redis client so shared components such as
// the auth rate limiter reuse the already verified connection pool.
func (r *RedisClient) Client() redis.UniversalClient {
	if r == nil {
		return nil
	}
	return r.client
}

// Check performs a real Redis round-trip for readiness.
func (r *RedisClient) Check(ctx context.Context) error {
	if r == nil || r.client == nil {
		return errors.New("redis client is not initialized")
	}

	checkCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		checkCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
	}
	defer cancel()

	if err := r.client.Ping(checkCtx).Err(); err != nil {
		return fmt.Errorf("check redis: %w", err)
	}

	return nil
}
