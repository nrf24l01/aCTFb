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
	return r.client.Close()
}
