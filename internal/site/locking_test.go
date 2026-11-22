package site

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// captureQuerier records the SQL passed to QueryRow.
type captureQuerier struct {
	query string
}

func (c *captureQuerier) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	c.query = sql
	return stubRow{}
}

type stubRow struct{}

func (stubRow) Scan(...any) error { return pgx.ErrNoRows }

// TestUpdateReadsSingletonUnderRowLock is the deterministic guard for the lost
// update fix: the write path must lock the singleton row before reading it.
func TestUpdateReadsSingletonUnderRowLock(t *testing.T) {
	locked := &captureQuerier{}
	if _, err := loadSettingsForUpdate(context.Background(), locked); err != nil {
		t.Fatalf("loadSettingsForUpdate: %v", err)
	}
	if !strings.Contains(strings.ToUpper(locked.query), "FOR UPDATE") {
		t.Fatalf("write-path read does not lock the row: %q", locked.query)
	}

	plain := &captureQuerier{}
	if _, err := loadSettings(context.Background(), plain); err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if strings.Contains(strings.ToUpper(plain.query), "FOR UPDATE") {
		t.Fatalf("read-only path must not take a write lock: %q", plain.query)
	}
}
