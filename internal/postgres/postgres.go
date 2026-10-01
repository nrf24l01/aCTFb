package postgres

import (
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nrf24l01/actfb/internal/config"
)

type DB struct {
	Pool *pgxpool.Pool
	SQL  *sql.DB
}

func NewDB(ctx context.Context, pg_cfg *config.Config.Datasources.Postgres) (*DB, error) {
	link := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&timezone=%s",
		pg_cfg.User,
		pg_cfg.Password,
		pg_cfg.Host,
		pg_cfg.Port,
		pg_cfg.DBName,
		pg_cfg.SSLMode,
		pg_cfg.Timezone,
	)

	cfg, err := pgxpool.ParseConfig(link)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	sqlDB := stdlib.OpenDBFromPool(pool)

	return &DB{
		Pool: pool,
		SQL:  sqlDB,
	}, nil
}