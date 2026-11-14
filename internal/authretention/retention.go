// Package authretention removes aged, expired authentication records.
//
// Retention is intentionally narrow: only the three allowlisted tables below
// are touched, only rows whose expires_at is older than a single per-run
// cutoff are deleted, and every batch is bounded by a LIMIT inside its own
// transaction. Unexpired rows are never deleted, even when they are revoked or
// already used, because replay checks must remain authoritative until expiry.
package authretention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRetentionDays = 7
	MinRetentionDays     = 1
	MaxRetentionDays     = 365

	DefaultBatchSize = 500
	MaxBatchSize     = 5000

	DefaultMaxBatches = 100
	MaxMaxBatches     = 1000
)

// Options bounds one retention run. Zero values select the documented
// defaults; explicit values outside the accepted ranges are rejected.
type Options struct {
	// RetentionDays is the grace period after expires_at before a row may be
	// removed.
	RetentionDays int
	// BatchSize is the maximum number of rows deleted per transaction.
	BatchSize int
	// MaxBatches caps transactions per table per run.
	MaxBatches int
}

// TableResult reports the bounded work performed for one allowlisted table.
type TableResult struct {
	Table     string
	Deleted   int64
	Batches   int
	Truncated bool
}

// Result describes one completed retention run.
type Result struct {
	Cutoff        time.Time
	RetentionDays int
	Tables        []TableResult
	Deleted       int64
	Truncated     bool
}

// tableSpec is a compile-time allowlist entry. The SQL is fixed in source; no
// table name is ever read from flags, environment, or the database.
type tableSpec struct {
	name      string
	deleteSQL string
}

var retentionTables = []tableSpec{
	{
		name: "auth_sessions",
		deleteSQL: `WITH victims AS (
			SELECT id
			FROM auth_sessions
			WHERE expires_at < $1
			ORDER BY expires_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM auth_sessions AS target
		USING victims
		WHERE target.id = victims.id`,
	},
	{
		name: "auth_email_verifications",
		deleteSQL: `WITH victims AS (
			SELECT id
			FROM auth_email_verifications
			WHERE expires_at < $1
			ORDER BY expires_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM auth_email_verifications AS target
		USING victims
		WHERE target.id = victims.id`,
	},
	{
		name: "auth_password_resets",
		deleteSQL: `WITH victims AS (
			SELECT id
			FROM auth_password_resets
			WHERE expires_at < $1
			ORDER BY expires_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM auth_password_resets AS target
		USING victims
		WHERE target.id = victims.id`,
	},
}

// NormalizeOptions applies defaults and validates all bounds. It is exported so
// command-line operators fail before opening a database connection.
func NormalizeOptions(opts Options) (Options, error) {
	if opts.RetentionDays == 0 {
		opts.RetentionDays = DefaultRetentionDays
	}
	if opts.RetentionDays < MinRetentionDays || opts.RetentionDays > MaxRetentionDays {
		return Options{}, fmt.Errorf(
			"retention days must be between %d and %d, got %d",
			MinRetentionDays, MaxRetentionDays, opts.RetentionDays,
		)
	}

	if opts.BatchSize == 0 {
		opts.BatchSize = DefaultBatchSize
	}
	if opts.BatchSize < 1 || opts.BatchSize > MaxBatchSize {
		return Options{}, fmt.Errorf(
			"batch size must be between 1 and %d, got %d",
			MaxBatchSize, opts.BatchSize,
		)
	}

	if opts.MaxBatches == 0 {
		opts.MaxBatches = DefaultMaxBatches
	}
	if opts.MaxBatches < 1 || opts.MaxBatches > MaxMaxBatches {
		return Options{}, fmt.Errorf(
			"max batches must be between 1 and %d, got %d",
			MaxMaxBatches, opts.MaxBatches,
		)
	}

	return opts, nil
}

// ResolveRetentionDays parses AUTH_RETENTION_DAYS. An empty value selects the
// documented default; explicit values must be between 1 and 365 days.
func ResolveRetentionDays(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultRetentionDays, nil
	}

	days, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("AUTH_RETENTION_DAYS must be an integer number of days: %w", err)
	}
	if days < MinRetentionDays || days > MaxRetentionDays {
		return 0, fmt.Errorf(
			"AUTH_RETENTION_DAYS must be between %d and %d, got %d",
			MinRetentionDays, MaxRetentionDays, days,
		)
	}

	return days, nil
}

// Run performs one bounded retention pass. The cutoff is computed once by the
// database (NOW() - retention interval) and reused for every table and batch, so
// all comparisons share one clock and one boundary.
//
// A table stops after MaxBatches transactions or when no expired row remains.
// The function is idempotent; a second run removes only rows that crossed the
// cutoff in the meantime. Callers own cancellation and database closure.
func Run(ctx context.Context, db *sql.DB, opts Options) (Result, error) {
	if db == nil {
		return Result{}, errors.New("retention database is nil")
	}

	normalized, err := NormalizeOptions(opts)
	if err != nil {
		return Result{}, err
	}

	var cutoff time.Time
	if err := db.QueryRowContext(ctx,
		`SELECT NOW() - make_interval(days => $1::int)`,
		normalized.RetentionDays,
	).Scan(&cutoff); err != nil {
		return Result{}, fmt.Errorf("compute retention cutoff: %w", err)
	}

	result := Result{
		Cutoff:        cutoff,
		RetentionDays: normalized.RetentionDays,
		Tables:        make([]TableResult, 0, len(retentionTables)),
	}

	for _, spec := range retentionTables {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		tableResult := TableResult{Table: spec.name}
		lastBatchFull := false

		for tableResult.Batches < normalized.MaxBatches {
			deleted, err := deleteBatch(ctx, db, spec.deleteSQL, cutoff, normalized.BatchSize)
			if err != nil {
				result.Tables = append(result.Tables, tableResult)
				return result, fmt.Errorf("retention %s batch %d: %w", spec.name, tableResult.Batches+1, err)
			}
			if deleted == 0 {
				lastBatchFull = false
				break
			}

			tableResult.Deleted += deleted
			tableResult.Batches++
			result.Deleted += deleted
			lastBatchFull = deleted == int64(normalized.BatchSize)

			if !lastBatchFull {
				break
			}
		}

		if tableResult.Batches == normalized.MaxBatches && lastBatchFull {
			// A full final batch may mean more eligible rows remain; report it
			// conservatively so operators can tune the budget.
			tableResult.Truncated = true
			result.Truncated = true
		}

		result.Tables = append(result.Tables, tableResult)
	}

	return result, nil
}

func deleteBatch(ctx context.Context, db *sql.DB, query string, cutoff time.Time, limit int) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin batch transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	res, err := tx.ExecContext(ctx, query, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired rows: %w", err)
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read deleted row count: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit batch transaction: %w", err)
	}

	return deleted, nil
}
