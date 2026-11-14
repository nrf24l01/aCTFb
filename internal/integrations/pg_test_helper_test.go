package integrations

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

func testIntegrationDB(t *testing.T) (*postgres.DB, func()) {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL integration tests")
	}
	schema := "ig_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schemaDSN := dsn
	if strings.Contains(dsn, "?") {
		schemaDSN += "&search_path=" + schema
	} else {
		schemaDSN += "?search_path=" + schema
	}
	sqlDB, err := sql.Open("pgx", schemaDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	dir := findIntegrationMigrations(t)
	if err := goose.Up(sqlDB, dir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	config, err := pgxpool.ParseConfig(schemaDSN)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	db := &postgres.DB{Pool: pool, SQL: stdlib.OpenDBFromPool(pool)}
	return db, func() {
		pool.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	}
}

func findIntegrationMigrations(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for depth := 0; depth < 8; depth++ {
		candidate := filepath.Join(dir, "migrations")
		if _, err := os.Stat(filepath.Join(candidate, "012_runtime_security.sql")); err == nil {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("migrations directory not found")
	return ""
}
