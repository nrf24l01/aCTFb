package main

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// openMigratedTestDB gives a cmd/backend test its own isolated, migrated schema.
//
// These tests exercise the real PostgreSQL-backed handlers, so they need the
// tables. Creating the schema and applying migrations here keeps the test
// independent of whatever happens to be in the shared database's public schema:
// CI applies migrations only inside per-test schemas, so a test that assumed an
// ambient migrated public schema failed there with `relation "users" does not
// exist` while passing on a developer machine that had run the backend once.
func openMigratedTestDB(t *testing.T, ctx context.Context) (*postgres.DB, func()) {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("ACTFB_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the real PostgreSQL cmd/backend tests")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	schema := "actfb_cmdb_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(schema) > 63 {
		admin.Close()
		t.Fatalf("unsafe schema name %q", schema)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		cleanup()
		t.Fatalf("parse DSN: %v", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		cleanup()
		t.Fatalf("DSN must use postgres:// or postgresql:// URL form")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()

	poolConfig, err := pgxpool.ParseConfig(parsed.String())
	if err != nil {
		cleanup()
		t.Fatalf("parse pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		cleanup()
		t.Fatalf("create pool: %v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		pool.Close()
		_ = sqlDB.Close()
		cleanup()
		t.Fatalf("resolve migrations: %v", err)
	}
	if err := postgres.RunMigrations(ctx, sqlDB, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		pool.Close()
		_ = sqlDB.Close()
		cleanup()
		t.Fatalf("apply migrations: %v", err)
	}
	return &postgres.DB{Pool: pool, SQL: sqlDB}, func() {
		pool.Close()
		_ = sqlDB.Close()
		cleanup()
	}
}
