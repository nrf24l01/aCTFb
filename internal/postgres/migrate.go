package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/pressly/goose/v3"
)

func RunMigrations(
	ctx context.Context,
	db *sql.DB,
	pgCfg *config.PostgresConfig,
) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, pgCfg.MigrationsPath); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}
