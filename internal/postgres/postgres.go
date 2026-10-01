package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/nrf24l01/actfb/internal/config"
)

type DB struct {
	Pool *pgxpool.Pool
	SQL  *sql.DB
}

func NewPostgresClient(ctx context.Context, pgCfg *config.PostgresConfig) (*DB, error) {
	if pgCfg == nil {
		return nil, errors.New("postgres config is nil")
	}

	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(pgCfg.User, pgCfg.Password),
		Host: net.JoinHostPort(
			pgCfg.Host,
			strconv.Itoa(pgCfg.Port),
		),
		Path: pgCfg.DBName,
	}

	query := u.Query()
	query.Set("sslmode", pgCfg.SSLMode)
	query.Set("timezone", pgCfg.Timezone)
	u.RawQuery = query.Encode()

	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)

	return &DB{
		Pool: pool,
		SQL:  sqlDB,
	}, nil
}

func (db *DB) Close() error {
	err := db.SQL.Close()
	db.Pool.Close()

	return err
}
