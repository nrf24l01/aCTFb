package authretention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const day = 24 * time.Hour

// TestRunDeletesOnlyAgedExpiredRows is the primary real-PostgreSQL gate. It
// inserts expired, boundary, revoked, used, and unexpired rows, runs retention,
// and proves that only rows older than the seven-day grace period disappear.
func TestRunDeletesOnlyAgedExpiredRows(t *testing.T) {
	ctx, db := openRetentionTestDB(t)
	fixture := seedRetentionFixture(t, ctx, db)

	ids := struct {
		sessionOldActive       string
		sessionOldRevoked      string
		sessionGraceExpired    string
		sessionBoundaryOlder   string
		sessionBoundaryNewer   string
		sessionUnexpiredActive string
		sessionUnexpiredRevoke string
		verifyOldUnused        string
		verifyOldUsed          string
		verifyGraceUsed        string
		verifyUnexpiredUsed    string
		verifyBoundaryOlder    string
		resetOldUnused         string
		resetOldUsed           string
		resetGraceUsed         string
		resetUnexpiredUsed     string
		resetBoundaryOlder     string
	}{
		sessionOldActive:       uuid.NewString(),
		sessionOldRevoked:      uuid.NewString(),
		sessionGraceExpired:    uuid.NewString(),
		sessionBoundaryOlder:   uuid.NewString(),
		sessionBoundaryNewer:   uuid.NewString(),
		sessionUnexpiredActive: uuid.NewString(),
		sessionUnexpiredRevoke: uuid.NewString(),
		verifyOldUnused:        uuid.NewString(),
		verifyOldUsed:          uuid.NewString(),
		verifyGraceUsed:        uuid.NewString(),
		verifyUnexpiredUsed:    uuid.NewString(),
		verifyBoundaryOlder:    uuid.NewString(),
		resetOldUnused:         uuid.NewString(),
		resetOldUsed:           uuid.NewString(),
		resetGraceUsed:         uuid.NewString(),
		resetUnexpiredUsed:     uuid.NewString(),
		resetBoundaryOlder:     uuid.NewString(),
	}

	revokedAt := fixture.now.Add(-9 * day)
	usedAt := fixture.now.Add(-9 * day)
	recent := fixture.now.Add(-6 * day)
	olderThanGrace := fixture.now.Add(-(7*day + time.Hour))
	newerThanGrace := fixture.now.Add(-(7*day - time.Hour))
	future := fixture.now.Add(time.Hour)

	insertSession(t, ctx, db, fixture.userID, ids.sessionOldActive, fixture.now.Add(-8*day), nil)
	insertSession(t, ctx, db, fixture.userID, ids.sessionOldRevoked, fixture.now.Add(-8*day), &revokedAt)
	insertSession(t, ctx, db, fixture.userID, ids.sessionGraceExpired, recent, nil)
	insertSession(t, ctx, db, fixture.userID, ids.sessionBoundaryOlder, olderThanGrace, nil)
	insertSession(t, ctx, db, fixture.userID, ids.sessionBoundaryNewer, newerThanGrace, nil)
	insertSession(t, ctx, db, fixture.userID, ids.sessionUnexpiredActive, future, nil)
	insertSession(t, ctx, db, fixture.userID, ids.sessionUnexpiredRevoke, future, &fixture.now)

	insertVerification(t, ctx, db, fixture.userID, ids.verifyOldUnused, fixture.now.Add(-8*day), nil)
	insertVerification(t, ctx, db, fixture.userID, ids.verifyOldUsed, fixture.now.Add(-8*day), &usedAt)
	insertVerification(t, ctx, db, fixture.userID, ids.verifyGraceUsed, recent, &recent)
	insertVerification(t, ctx, db, fixture.userID, ids.verifyUnexpiredUsed, future, &fixture.now)
	insertVerification(t, ctx, db, fixture.userID, ids.verifyBoundaryOlder, olderThanGrace, nil)

	insertPasswordReset(t, ctx, db, fixture.userID, ids.resetOldUnused, fixture.now.Add(-8*day), nil)
	insertPasswordReset(t, ctx, db, fixture.userID, ids.resetOldUsed, fixture.now.Add(-8*day), &usedAt)
	insertPasswordReset(t, ctx, db, fixture.userID, ids.resetGraceUsed, recent, &recent)
	insertPasswordReset(t, ctx, db, fixture.userID, ids.resetUnexpiredUsed, future, &fixture.now)
	insertPasswordReset(t, ctx, db, fixture.userID, ids.resetBoundaryOlder, olderThanGrace, nil)

	usersBefore := readUserSnapshot(t, ctx, db, fixture.userID)

	var beforeRun time.Time
	if err := db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&beforeRun); err != nil {
		t.Fatalf("read time before run: %v", err)
	}

	result, err := Run(ctx, db, Options{BatchSize: 3, MaxBatches: 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var afterRun time.Time
	if err := db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&afterRun); err != nil {
		t.Fatalf("read time after run: %v", err)
	}

	if result.RetentionDays != DefaultRetentionDays {
		t.Fatalf("result retention days = %d, want %d", result.RetentionDays, DefaultRetentionDays)
	}
	if result.Deleted != 9 {
		t.Fatalf("result deleted = %d, want 9", result.Deleted)
	}
	if result.Truncated {
		t.Fatal("result unexpectedly truncated")
	}

	wantCounts := map[string]int64{
		"auth_sessions":            3,
		"auth_email_verifications": 3,
		"auth_password_resets":     3,
	}
	if len(result.Tables) != len(wantCounts) {
		t.Fatalf("result tables = %+v, want %d entries", result.Tables, len(wantCounts))
	}
	for _, table := range result.Tables {
		if got := wantCounts[table.Table]; got != table.Deleted {
			t.Fatalf("%s deleted = %d, want %d", table.Table, table.Deleted, got)
		}
		if table.Batches != 1 {
			t.Fatalf("%s batches = %d, want 1", table.Table, table.Batches)
		}
		if table.Truncated {
			t.Fatalf("%s unexpectedly truncated", table.Table)
		}
	}

	minCutoff := beforeRun.Add(-DefaultRetentionDays*day - time.Minute)
	maxCutoff := afterRun.Add(-DefaultRetentionDays*day + time.Minute)
	if result.Cutoff.Before(minCutoff) || result.Cutoff.After(maxCutoff) {
		t.Fatalf("cutoff %s outside [%s, %s]", result.Cutoff, minCutoff, maxCutoff)
	}

	assertOnlyIDs(t, ctx, db, "auth_sessions", []string{
		ids.sessionGraceExpired,
		ids.sessionBoundaryNewer,
		ids.sessionUnexpiredActive,
		ids.sessionUnexpiredRevoke,
	})
	assertOnlyIDs(t, ctx, db, "auth_email_verifications", []string{
		ids.verifyGraceUsed,
		ids.verifyUnexpiredUsed,
	})
	assertOnlyIDs(t, ctx, db, "auth_password_resets", []string{
		ids.resetGraceUsed,
		ids.resetUnexpiredUsed,
	})

	usersAfter := readUserSnapshot(t, ctx, db, fixture.userID)
	if usersBefore != usersAfter {
		t.Fatalf("users row changed: before=%+v after=%+v", usersBefore, usersAfter)
	}

	// A second run must be a no-op: used/revoked rows that are still unexpired
	// remain, and everything older than the cutoff is already gone.
	second, err := Run(ctx, db, Options{BatchSize: 3, MaxBatches: 10})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Deleted != 0 {
		t.Fatalf("second run deleted = %d, want 0", second.Deleted)
	}
	for _, table := range second.Tables {
		if table.Deleted != 0 || table.Batches != 0 {
			t.Fatalf("second run %s = %+v, want no work", table.Table, table)
		}
	}
}

// TestRunHonorsBatchAndBatchBudget proves the work budget is bounded per table
// and that a later run continues safely without double counting.
func TestRunHonorsBatchAndBatchBudget(t *testing.T) {
	ctx, db := openRetentionTestDB(t)
	fixture := seedRetentionFixture(t, ctx, db)
	insertSession(t, ctx, db, fixture.userID, uuid.NewString(), fixture.now.Add(time.Hour), nil)

	const seededOldSessions = 23
	for i := 0; i < seededOldSessions; i++ {
		insertSession(t, ctx, db, fixture.userID, uuid.NewString(), fixture.now.Add(-8*day), nil)
	}

	result, err := Run(ctx, db, Options{BatchSize: 5, MaxBatches: 2})
	if err != nil {
		t.Fatalf("bounded Run: %v", err)
	}
	if result.Deleted != 10 {
		t.Fatalf("bounded run deleted = %d, want 10", result.Deleted)
	}
	if !result.Truncated {
		t.Fatal("bounded run did not report truncation")
	}
	sessions := findTableResult(t, result, "auth_sessions")
	if sessions.Batches != 2 || sessions.Deleted != 10 || !sessions.Truncated {
		t.Fatalf("auth_sessions bounded result = %+v, want 2 batches/10 deleted/truncated", sessions)
	}
	assertTableCount(t, ctx, db, "auth_sessions", seededOldSessions+1-10)
	assertTableCount(t, ctx, db, "users", 1)

	continued, err := Run(ctx, db, Options{BatchSize: 5, MaxBatches: 10})
	if err != nil {
		t.Fatalf("continuation Run: %v", err)
	}
	if continued.Deleted != 13 {
		t.Fatalf("continuation deleted = %d, want 13", continued.Deleted)
	}
	if continued.Truncated {
		t.Fatal("continuation unexpectedly truncated")
	}
	assertTableCount(t, ctx, db, "auth_sessions", 1)
	assertTableCount(t, ctx, db, "users", 1)

	final, err := Run(ctx, db, Options{BatchSize: 5, MaxBatches: 10})
	if err != nil {
		t.Fatalf("final Run: %v", err)
	}
	if final.Deleted != 0 {
		t.Fatalf("final run deleted = %d, want 0", final.Deleted)
	}
}

// TestRunConcurrentNoDoubleCount runs two retention passes against the same
// rows. Reported deletes must equal actual removals, never exceed them, and
// users must survive.
func TestRunConcurrentNoDoubleCount(t *testing.T) {
	ctx, db := openRetentionTestDB(t)
	fixture := seedRetentionFixture(t, ctx, db)
	db.SetMaxOpenConns(4)

	const seeded = 40
	for i := 0; i < seeded; i++ {
		insertSession(t, ctx, db, fixture.userID, uuid.NewString(), fixture.now.Add(-8*day), nil)
	}

	before := countRows(t, ctx, db, "auth_sessions")

	var wg sync.WaitGroup
	results := make(chan Result, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := Run(ctx, db, Options{BatchSize: 4, MaxBatches: 20})
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Run: %v", err)
		}
	}
	var reported int64
	for result := range results {
		reported += result.Deleted
	}

	after := countRows(t, ctx, db, "auth_sessions")
	actual := int64(before - after)
	if reported != actual {
		t.Fatalf("reported deletes = %d, actual removals = %d", reported, actual)
	}
	if actual > seeded {
		t.Fatalf("removed %d sessions, seeded %d", actual, seeded)
	}

	// Rows may be skipped while the other run holds their locks; a follow-up
	// pass must remove exactly the remainder and still not touch users.
	remainder, err := Run(ctx, db, Options{BatchSize: 50, MaxBatches: 20})
	if err != nil {
		t.Fatalf("cleanup Run: %v", err)
	}
	if remainder.Deleted != int64(seeded)-actual {
		t.Fatalf("cleanup deleted = %d, want %d", remainder.Deleted, int64(seeded)-actual)
	}
	assertTableCount(t, ctx, db, "auth_sessions", 0)
	assertTableCount(t, ctx, db, "users", 1)
}

// TestRunConfigurableRetentionDays verifies the operator-selected grace period
// is applied by the database cutoff instead of a hardcoded interval.
func TestRunConfigurableRetentionDays(t *testing.T) {
	ctx, db := openRetentionTestDB(t)
	fixture := seedRetentionFixture(t, ctx, db)

	old := uuid.NewString()
	recent := uuid.NewString()
	insertSession(t, ctx, db, fixture.userID, old, fixture.now.Add(-48*time.Hour), nil)
	insertSession(t, ctx, db, fixture.userID, recent, fixture.now.Add(-12*time.Hour), nil)

	result, err := Run(ctx, db, Options{RetentionDays: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Deleted != 1 || result.RetentionDays != 1 {
		t.Fatalf("result = %+v, want 1 deleted with 1-day retention", result)
	}
	assertOnlyIDs(t, ctx, db, "auth_sessions", []string{recent})
	assertTableCount(t, ctx, db, "users", 1)
}

// TestRunCanceledContext proves cancellation is surfaced as an error and does
// not delete rows.
func TestRunCanceledContext(t *testing.T) {
	ctx, db := openRetentionTestDB(t)
	fixture := seedRetentionFixture(t, ctx, db)
	insertSession(t, ctx, db, fixture.userID, uuid.NewString(), fixture.now.Add(-8*day), nil)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Run(canceled, db, Options{})
	if err == nil {
		t.Fatal("Run with canceled context returned nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	assertTableCount(t, ctx, db, "auth_sessions", 1)
}

// TestDeleteBatchStrictCutoff pins the strict `<` boundary without relying on
// wall-clock movement between the cutoff query and the delete.
func TestDeleteBatchStrictCutoff(t *testing.T) {
	ctx, db := openRetentionTestDB(t)
	fixture := seedRetentionFixture(t, ctx, db)

	cutoff := fixture.now
	exact := uuid.NewString()
	newer := uuid.NewString()
	older := uuid.NewString()
	insertSession(t, ctx, db, fixture.userID, exact, cutoff, nil)
	insertSession(t, ctx, db, fixture.userID, newer, cutoff.Add(time.Millisecond), nil)
	insertSession(t, ctx, db, fixture.userID, older, cutoff.Add(-time.Millisecond), nil)

	deleted, err := deleteBatch(ctx, db, retentionTables[0].deleteSQL, cutoff, 10)
	if err != nil {
		t.Fatalf("deleteBatch: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleteBatch deleted = %d, want 1", deleted)
	}
	assertOnlyIDs(t, ctx, db, "auth_sessions", []string{exact, newer})
}

type retentionFixture struct {
	now    time.Time
	userID string
}

func seedRetentionFixture(t *testing.T, ctx context.Context, db *sql.DB) retentionFixture {
	t.Helper()

	var now time.Time
	if err := db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
		t.Fatalf("read fixture time: %v", err)
	}

	var userID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO users (username, email, password)
		VALUES ('retention-user', 'retention-user@example.test', 'argon2id-hash')
		RETURNING id::text
	`).Scan(&userID); err != nil {
		t.Fatalf("insert retention user: %v", err)
	}

	return retentionFixture{now: now, userID: userID}
}

func insertSession(t *testing.T, ctx context.Context, db *sql.DB, userID, id string, expiresAt time.Time, revokedAt *time.Time) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		INSERT INTO auth_sessions (id, user_id, token_hash, expires_at, revoked_at, family_id)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $1::uuid)
	`, id, userID, []byte(id), expiresAt, nullableTime(revokedAt))
	if err != nil {
		t.Fatalf("insert auth session %s: %v", id, err)
	}
}

func insertVerification(t *testing.T, ctx context.Context, db *sql.DB, userID, id string, expiresAt time.Time, usedAt *time.Time) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at, used_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
	`, id, userID, []byte(id), expiresAt, nullableTime(usedAt))
	if err != nil {
		t.Fatalf("insert email verification %s: %v", id, err)
	}
}

func insertPasswordReset(t *testing.T, ctx context.Context, db *sql.DB, userID, id string, expiresAt time.Time, usedAt *time.Time) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at, used_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
	`, id, userID, []byte(id), expiresAt, nullableTime(usedAt))
	if err != nil {
		t.Fatalf("insert password reset %s: %v", id, err)
	}
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

type userSnapshot struct {
	Email       string
	Password    string
	AuthVersion int64
	Roles       string
}

func readUserSnapshot(t *testing.T, ctx context.Context, db *sql.DB, userID string) userSnapshot {
	t.Helper()
	var snapshot userSnapshot
	if err := db.QueryRowContext(ctx, `
		SELECT email, password, auth_version, array_to_string(roles, ',')
		FROM users
		WHERE id = $1::uuid
	`, userID).Scan(&snapshot.Email, &snapshot.Password, &snapshot.AuthVersion, &snapshot.Roles); err != nil {
		t.Fatalf("read user snapshot: %v", err)
	}
	return snapshot
}

func assertOnlyIDs(t *testing.T, ctx context.Context, db *sql.DB, table string, want []string) {
	t.Helper()

	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT id::text FROM %s ORDER BY id`, table))
	if err != nil {
		t.Fatalf("query %s ids: %v", table, err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan %s id: %v", table, err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s ids: %v", table, err)
	}

	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s ids = %v, want %v", table, got, want)
	}
}

func assertTableCount(t *testing.T, ctx context.Context, db *sql.DB, table string, want int) {
	t.Helper()
	if got := countRows(t, ctx, db, table); got != want {
		t.Fatalf("%s row count = %d, want %d", table, got, want)
	}
}

func countRows(t *testing.T, ctx context.Context, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, table)).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func findTableResult(t *testing.T, result Result, table string) TableResult {
	t.Helper()
	for _, entry := range result.Tables {
		if entry.Table == table {
			return entry
		}
	}
	t.Fatalf("table %s missing from result %+v", table, result.Tables)
	return TableResult{}
}

func openRetentionTestDB(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()

	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the real PostgreSQL retention tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL (check ACTFB_TEST_POSTGRES_DSN): %v", err)
	}

	schema := "actfb_test_retention_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(schema, "actfb_test_retention_") || len(schema) > 63 {
		admin.Close()
		t.Fatalf("refusing unsafe test schema name %q", schema)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create isolated schema: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema %s: %v", schema, err)
		}
		admin.Close()
	})

	scopedDSN := dsnWithSearchPath(t, dsn, schema)
	db, err := sql.Open("pgx", scopedDSN)
	if err != nil {
		t.Fatalf("open isolated schema connection: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(4)

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping isolated schema: %v", err)
	}

	var currentSchema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&currentSchema); err != nil {
		t.Fatalf("read current_schema: %v", err)
	}
	if currentSchema != schema {
		t.Fatalf("search_path not applied: current_schema=%q want %q", currentSchema, schema)
	}

	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(migrationDir, "005_auth_password_resets.sql")); err != nil {
		t.Fatalf("migrations dir %s is missing 005: %v", migrationDir, err)
	}

	if err := postgres.RunMigrations(ctx, db, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		t.Fatalf("apply migrations 001-005: %v", err)
	}

	return ctx, db
}

func dsnWithSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse ACTFB_TEST_POSTGRES_DSN: %v", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		t.Fatalf("ACTFB_TEST_POSTGRES_DSN must use postgres:// or postgresql:// URL form")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	return u.String()
}
