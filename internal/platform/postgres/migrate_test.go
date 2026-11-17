package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/pressly/goose/v3"
)

// TestMigrationsCleanSchema is opt-in: it needs a real PostgreSQL server and never
// touches the public schema or any existing data. It creates a uniquely named
// actfb_test_* schema, applies migrations 001-003, inserts legacy sessions, applies
// 004/005 and checks family/reason plus password-reset/auth-version backfill,
// exercises the users/auth_sessions/auth_email_verifications/auth_password_resets
// contracts, verifies a second Up is a no-op, checks 003/004/005 down/up, and rolls
// all migrations down and up again inside the isolated schema.
//
// Run with:
//
//	ACTFB_TEST_POSTGRES_DSN='postgres://user:pass@127.0.0.1:55432/actfb?sslmode=disable' \
//	  go test ./internal/platform/postgres -run TestMigrationsCleanSchema -v
func TestMigrationsCleanSchema(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the real PostgreSQL migration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL (check ACTFB_TEST_POSTGRES_DSN): %v", err)
	}

	schema := "actfb_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(schema, "actfb_test_") || len(schema) > 63 {
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

	scopedDSN, err := dsnWithSearchPath(dsn, schema)
	if err != nil {
		t.Fatalf("build scoped DSN: %v", err)
	}

	db, err := sql.Open("pgx", scopedDSN)
	if err != nil {
		t.Fatalf("open isolated schema connection: %v", err)
	}
	// A single connection guarantees search_path and the goose version table stay
	// in the isolated schema for the whole test.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping isolated schema: %v", err)
	}

	var currentSchema string
	if err := db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil {
		t.Fatalf("read current_schema: %v", err)
	}
	if currentSchema != schema {
		t.Fatalf("search_path not applied: current_schema=%q want %q", currentSchema, schema)
	}

	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(migrationDir, "001_init.sql")); err != nil {
		t.Fatalf("migrations dir %s is missing 001_init.sql: %v", migrationDir, err)
	}

	pgCfg := &config.PostgresConfig{MigrationsPath: migrationDir}

	// Apply only through 003 first so migration 004 can be tested against
	// existing active and revoked session rows.
	if err := goose.UpToContext(ctx, db, migrationDir, 3); err != nil {
		t.Fatalf("apply migrations through 003: %v", err)
	}
	preVersion, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		t.Fatalf("read goose version before 004: %v", err)
	}
	if preVersion != 3 {
		t.Fatalf("goose version before 004 = %d, want 3", preVersion)
	}

	var legacyUserID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO users (username, email, password)
		VALUES ('legacybackfill', 'legacybackfill@example.test', 'argon2id-hash')
		RETURNING id::text
	`).Scan(&legacyUserID); err != nil {
		t.Fatalf("insert pre-004 user: %v", err)
	}

	legacySessionIDs := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO auth_sessions (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour')
	`, legacySessionIDs[0], legacyUserID, []byte{0x20}); err != nil {
		t.Fatalf("insert pre-004 active session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO auth_sessions (id, user_id, token_hash, expires_at, revoked_at)
		VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour', NOW() - INTERVAL '1 hour')
	`, legacySessionIDs[1], legacyUserID, []byte{0x21}); err != nil {
		t.Fatalf("insert pre-004 revoked session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO auth_sessions (id, user_id, token_hash, expires_at, revoked_at)
		VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour', NOW() - INTERVAL '2 hours')
	`, legacySessionIDs[2], legacyUserID, []byte{0x22}); err != nil {
		t.Fatalf("insert second pre-004 revoked session: %v", err)
	}

	if err := RunMigrations(ctx, db, pgCfg); err != nil {
		t.Fatalf("apply migrations 004+ to clean schema: %v", err)
	}

	version, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	if version < 6 {
		t.Fatalf("goose version = %d, want at least 6 (001-006)", version)
	}

	t.Run("005 backfills existing users and creates reset schema", func(t *testing.T) {
		var users, nonZero int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*), count(*) FILTER (WHERE auth_version <> 0) FROM users
		`).Scan(&users, &nonZero); err != nil {
			t.Fatalf("check users.auth_version backfill: %v", err)
		}
		if users == 0 || nonZero != 0 {
			t.Fatalf("auth_version backfill = %d nonzero of %d users, want 0 nonzero", nonZero, users)
		}

		var resetTable sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", "auth_password_resets").Scan(&resetTable); err != nil {
			t.Fatalf("check auth_password_resets: %v", err)
		}
		if !resetTable.Valid {
			t.Fatal("auth_password_resets missing after 005")
		}

		expectPgError(t, db, pgerrcode.CheckViolation, `
			UPDATE users SET auth_version = -1 WHERE username = 'legacybackfill'
		`)
	})

	t.Run("004 backfills legacy sessions", func(t *testing.T) {
		var familyMatches int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM auth_sessions
			WHERE user_id = $1 AND family_id = id
		`, legacyUserID).Scan(&familyMatches); err != nil {
			t.Fatalf("count legacy family backfill: %v", err)
		}
		if familyMatches != 3 {
			t.Fatalf("legacy family backfill count = %d, want 3", familyMatches)
		}

		var activeReason sql.NullString
		if err := db.QueryRowContext(ctx, `
			SELECT revoked_reason FROM auth_sessions WHERE id = $1
		`, legacySessionIDs[0]).Scan(&activeReason); err != nil {
			t.Fatalf("read active session reason: %v", err)
		}
		if activeReason.Valid {
			t.Fatalf("active legacy session revoked_reason = %q, want NULL", activeReason.String)
		}

		var legacyReasons int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM auth_sessions
			WHERE user_id = $1 AND revoked_at IS NOT NULL AND revoked_reason = 'legacy'
		`, legacyUserID).Scan(&legacyReasons); err != nil {
			t.Fatalf("count legacy revocation reasons: %v", err)
		}
		if legacyReasons != 2 {
			t.Fatalf("legacy revocation reason count = %d, want 2", legacyReasons)
		}
	})

	t.Run("user defaults and role constraint", func(t *testing.T) {
		var userID string
		var createdAt time.Time
		var disabled bool
		var roles string

		err := db.QueryRowContext(ctx, `
			INSERT INTO users (username, email, password)
			VALUES ('alice', 'alice@example.test', 'argon2id-hash')
			RETURNING id::text, created_at, disabled, array_to_string(roles, ',')
		`).Scan(&userID, &createdAt, &disabled, &roles)
		if err != nil {
			t.Fatalf("insert default user: %v", err)
		}
		if userID == "" || createdAt.IsZero() {
			t.Fatalf("user id/created_at not populated: id=%q created_at=%v", userID, createdAt)
		}
		if disabled {
			t.Fatal("new user must not be disabled")
		}
		if roles != "user" {
			t.Fatalf("default roles = %q, want %q", roles, "user")
		}

		var adminRoles string
		err = db.QueryRowContext(ctx, `
			INSERT INTO users (username, email, password, roles)
			VALUES ('root', 'root@example.test', 'argon2id-hash', ARRAY['admin'])
			RETURNING array_to_string(roles, ',')
		`).Scan(&adminRoles)
		if err != nil {
			t.Fatalf("insert admin user: %v", err)
		}
		if adminRoles != "admin" {
			t.Fatalf("admin roles = %q, want %q", adminRoles, "admin")
		}

		expectPgError(t, db, pgerrcode.CheckViolation, `
			INSERT INTO users (username, email, password, roles)
			VALUES ('teacher', 'teacher@example.test', 'argon2id-hash', ARRAY['teacher'])
		`)
		expectPgError(t, db, pgerrcode.CheckViolation, `
			INSERT INTO users (username, email, password, roles)
			VALUES ('emptyroles', 'emptyroles@example.test', 'argon2id-hash', ARRAY[]::TEXT[])
		`)
	})

	t.Run("auth session constraints and cascade", func(t *testing.T) {
		var userID string
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM users WHERE username = 'alice'`).Scan(&userID); err != nil {
			t.Fatalf("load alice id: %v", err)
		}

		familyID := uuid.NewString()
		tokenHash := []byte{0xde, 0xad, 0xbe, 0xef}
		var sessionID string
		var createdAt time.Time
		var revokedAt sql.NullTime
		var gotFamilyID string
		var revokedReason sql.NullString

		err := db.QueryRowContext(ctx, `
			INSERT INTO auth_sessions (user_id, token_hash, expires_at, family_id)
			VALUES ($1, $2, NOW() + INTERVAL '1 hour', $3)
			RETURNING id::text, created_at, revoked_at, family_id::text, revoked_reason
		`, userID, tokenHash, familyID).Scan(&sessionID, &createdAt, &revokedAt, &gotFamilyID, &revokedReason)
		if err != nil {
			t.Fatalf("insert auth session: %v", err)
		}
		if sessionID == "" || createdAt.IsZero() {
			t.Fatalf("session id/created_at not populated: id=%q created_at=%v", sessionID, createdAt)
		}
		if gotFamilyID != familyID {
			t.Fatalf("session family_id = %q, want %q", gotFamilyID, familyID)
		}
		if revokedAt.Valid {
			t.Fatal("new auth session must not be revoked")
		}
		if revokedReason.Valid {
			t.Fatalf("new auth session revoked_reason = %q, want NULL", revokedReason.String)
		}

		if _, err := db.ExecContext(ctx, `
			UPDATE auth_sessions SET revoked_at = NOW(), revoked_reason = 'logout' WHERE id = $1
		`, sessionID); err != nil {
			t.Fatalf("revoke auth session with reason: %v", err)
		}
		var logoutReason string
		if err := db.QueryRowContext(ctx, `
			SELECT revoked_reason FROM auth_sessions WHERE id = $1
		`, sessionID).Scan(&logoutReason); err != nil {
			t.Fatalf("read logout reason: %v", err)
		}
		if logoutReason != "logout" {
			t.Fatalf("revoked_reason = %q, want logout", logoutReason)
		}

		var rotatedFamilyID, rotatedReason string
		err = db.QueryRowContext(ctx, `
			INSERT INTO auth_sessions (user_id, token_hash, expires_at, family_id, revoked_at, revoked_reason)
			VALUES ($1, $2, NOW() + INTERVAL '1 hour', $3, NOW(), 'rotated')
			RETURNING family_id::text, revoked_reason
		`, userID, []byte{0x05, 0x06}, familyID).Scan(&rotatedFamilyID, &rotatedReason)
		if err != nil {
			t.Fatalf("insert rotated session: %v", err)
		}
		if rotatedFamilyID != familyID || rotatedReason != "rotated" {
			t.Fatalf("rotated session family/reason = %q/%q, want %q/rotated", rotatedFamilyID, rotatedReason, familyID)
		}

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_sessions (user_id, token_hash, expires_at, family_id)
			VALUES ($1, $2, NOW() + INTERVAL '1 hour', NULL)
		`, userID, []byte{0x07})

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_sessions (user_id, token_hash, expires_at, family_id)
			VALUES ($1, NULL, NOW() + INTERVAL '1 hour', $2)
		`, userID, uuid.NewString())

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_sessions (user_id, token_hash, expires_at, family_id)
			VALUES ($1, $2, NULL, $3)
		`, userID, []byte{0x04}, uuid.NewString())

		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO auth_sessions (user_id, token_hash, expires_at, family_id)
			VALUES ($1, $2, NOW() + INTERVAL '2 hours', $3)
		`, userID, tokenHash, uuid.NewString())

		expectPgError(t, db, pgerrcode.ForeignKeyViolation, `
			INSERT INTO auth_sessions (user_id, token_hash, expires_at, family_id)
			VALUES ($1, $2, NOW() + INTERVAL '1 hour', $3)
		`, uuid.NewString(), []byte{0x01}, uuid.NewString())

		var cascadeUserID string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO users (username, email, password)
			VALUES ('cascade', 'cascade@example.test', 'argon2id-hash')
			RETURNING id::text
		`).Scan(&cascadeUserID); err != nil {
			t.Fatalf("insert cascade user: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO auth_sessions (user_id, token_hash, expires_at, family_id)
			VALUES ($1, $2, NOW() + INTERVAL '1 hour', $3)
		`, cascadeUserID, []byte{0x02, 0x03}, uuid.NewString()); err != nil {
			t.Fatalf("insert cascade session: %v", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, cascadeUserID); err != nil {
			t.Fatalf("delete cascade user: %v", err)
		}

		var sessionCount int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM auth_sessions WHERE user_id = $1`, cascadeUserID).Scan(&sessionCount); err != nil {
			t.Fatalf("count cascade sessions: %v", err)
		}
		if sessionCount != 0 {
			t.Fatalf("ON DELETE CASCADE left %d sessions", sessionCount)
		}
	})

	t.Run("email verification token constraints and cascade", func(t *testing.T) {
		var userID string
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM users WHERE username = 'alice'`).Scan(&userID); err != nil {
			t.Fatalf("load alice id: %v", err)
		}

		verificationID := uuid.NewString()
		tokenHash := []byte{0xaa, 0xbb, 0xcc, 0xdd}
		var createdAt time.Time
		var usedAt sql.NullTime

		err := db.QueryRowContext(ctx, `
			INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour')
			RETURNING id::text, created_at, used_at
		`, verificationID, userID, tokenHash).Scan(&verificationID, &createdAt, &usedAt)
		if err != nil {
			t.Fatalf("insert email verification: %v", err)
		}
		if verificationID == "" || createdAt.IsZero() {
			t.Fatalf("verification id/created_at not populated: id=%q created_at=%v", verificationID, createdAt)
		}
		if usedAt.Valid {
			t.Fatal("new email verification must not be used")
		}

		if _, err := db.ExecContext(ctx, `
			UPDATE auth_email_verifications SET used_at = NOW() WHERE id = $1
		`, verificationID); err != nil {
			t.Fatalf("mark email verification used: %v", err)
		}
		var usedAtValue time.Time
		if err := db.QueryRowContext(ctx, `
			SELECT used_at FROM auth_email_verifications WHERE id = $1
		`, verificationID).Scan(&usedAtValue); err != nil {
			t.Fatalf("read used_at: %v", err)
		}
		if usedAtValue.IsZero() {
			t.Fatal("used_at was not persisted")
		}

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
			VALUES (NULL, $1, $2, NOW() + INTERVAL '1 hour')
		`, userID, []byte{0x10})

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
			VALUES ($1, NULL, $2, NOW() + INTERVAL '1 hour')
		`, uuid.NewString(), []byte{0x11})

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, NULL, NOW() + INTERVAL '1 hour')
		`, uuid.NewString(), userID)

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NULL)
		`, uuid.NewString(), userID, []byte{0x12})

		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NOW() + INTERVAL '2 hours')
		`, uuid.NewString(), userID, tokenHash)

		expectPgError(t, db, pgerrcode.ForeignKeyViolation, `
			INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour')
		`, uuid.NewString(), uuid.NewString(), []byte{0x13})

		var cascadeUserID string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO users (username, email, password)
			VALUES ('verifycascade', 'verifycascade@example.test', 'argon2id-hash')
			RETURNING id::text
		`).Scan(&cascadeUserID); err != nil {
			t.Fatalf("insert verification cascade user: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour')
		`, uuid.NewString(), cascadeUserID, []byte{0x14}); err != nil {
			t.Fatalf("insert verification cascade token: %v", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, cascadeUserID); err != nil {
			t.Fatalf("delete verification cascade user: %v", err)
		}

		var verificationCount int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM auth_email_verifications WHERE user_id = $1
		`, cascadeUserID).Scan(&verificationCount); err != nil {
			t.Fatalf("count cascade verifications: %v", err)
		}
		if verificationCount != 0 {
			t.Fatalf("ON DELETE CASCADE left %d verification tokens", verificationCount)
		}
	})

	t.Run("password reset token constraints and cascade", func(t *testing.T) {
		var userID string
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM users WHERE username = 'alice'`).Scan(&userID); err != nil {
			t.Fatalf("load alice id: %v", err)
		}

		resetID := uuid.NewString()
		tokenHash := []byte{0x31, 0x32, 0x33, 0x34}
		var createdAt time.Time
		var usedAt sql.NullTime

		err := db.QueryRowContext(ctx, `
			INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour')
			RETURNING id::text, created_at, used_at
		`, resetID, userID, tokenHash).Scan(&resetID, &createdAt, &usedAt)
		if err != nil {
			t.Fatalf("insert password reset: %v", err)
		}
		if resetID == "" || createdAt.IsZero() {
			t.Fatalf("reset id/created_at not populated: id=%q created_at=%v", resetID, createdAt)
		}
		if usedAt.Valid {
			t.Fatal("new password reset must not be used")
		}

		if _, err := db.ExecContext(ctx, `
			UPDATE auth_password_resets SET used_at = NOW() WHERE id = $1
		`, resetID); err != nil {
			t.Fatalf("mark password reset used: %v", err)
		}
		var usedAtValue time.Time
		if err := db.QueryRowContext(ctx, `
			SELECT used_at FROM auth_password_resets WHERE id = $1
		`, resetID).Scan(&usedAtValue); err != nil {
			t.Fatalf("read reset used_at: %v", err)
		}
		if usedAtValue.IsZero() {
			t.Fatal("reset used_at was not persisted")
		}

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
			VALUES (NULL, $1, $2, NOW() + INTERVAL '1 hour')
		`, userID, []byte{0x35})

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
			VALUES ($1, NULL, $2, NOW() + INTERVAL '1 hour')
		`, uuid.NewString(), []byte{0x36})

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, NULL, NOW() + INTERVAL '1 hour')
		`, uuid.NewString(), userID)

		expectPgError(t, db, pgerrcode.NotNullViolation, `
			INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NULL)
		`, uuid.NewString(), userID, []byte{0x37})

		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NOW() + INTERVAL '2 hours')
		`, uuid.NewString(), userID, tokenHash)

		expectPgError(t, db, pgerrcode.ForeignKeyViolation, `
			INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour')
		`, uuid.NewString(), uuid.NewString(), []byte{0x38})

		var cascadeUserID string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO users (username, email, password)
			VALUES ('resetcascade', 'resetcascade@example.test', 'argon2id-hash')
			RETURNING id::text
		`).Scan(&cascadeUserID); err != nil {
			t.Fatalf("insert reset cascade user: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
			VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour')
		`, uuid.NewString(), cascadeUserID, []byte{0x39}); err != nil {
			t.Fatalf("insert reset cascade token: %v", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, cascadeUserID); err != nil {
			t.Fatalf("delete reset cascade user: %v", err)
		}

		var resetCount int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM auth_password_resets WHERE user_id = $1
		`, cascadeUserID).Scan(&resetCount); err != nil {
			t.Fatalf("count cascade resets: %v", err)
		}
		if resetCount != 0 {
			t.Fatalf("ON DELETE CASCADE left %d reset tokens", resetCount)
		}
	})

	t.Run("auth session indexes and constraints", func(t *testing.T) {
		rows, err := db.QueryContext(ctx, `
			SELECT indexname FROM pg_indexes
			WHERE schemaname = current_schema() AND tablename = 'auth_sessions'
		`)
		if err != nil {
			t.Fatalf("list auth_sessions indexes: %v", err)
		}
		defer rows.Close()

		indexes := make(map[string]bool)
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatalf("scan index name: %v", err)
			}
			indexes[name] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate indexes: %v", err)
		}

		for _, want := range []string{
			"auth_sessions_token_hash_key",
			"auth_sessions_user_id_idx",
			"auth_sessions_expires_at_idx",
			"auth_sessions_active_user_idx",
			"auth_sessions_family_id_idx",
		} {
			if !indexes[want] {
				t.Errorf("missing index %s (have %v)", want, indexes)
			}
		}

		var constraints int
		err = db.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_constraint
			WHERE conname = 'users_roles_allowed' AND conrelid = 'users'::regclass
		`).Scan(&constraints)
		if err != nil {
			t.Fatalf("check users_roles_allowed constraint: %v", err)
		}
		if constraints != 1 {
			t.Fatalf("users_roles_allowed constraint count = %d, want 1", constraints)
		}

		verificationIndexes, err := indexesForTable(ctx, db, "auth_email_verifications")
		if err != nil {
			t.Fatalf("list auth_email_verifications indexes: %v", err)
		}
		for _, want := range []string{
			"auth_email_verifications_token_hash_key",
			"auth_email_verifications_user_id_idx",
			"auth_email_verifications_expires_at_idx",
		} {
			if !verificationIndexes[want] {
				t.Errorf("missing index %s (have %v)", want, verificationIndexes)
			}
		}

		resetIndexes, err := indexesForTable(ctx, db, "auth_password_resets")
		if err != nil {
			t.Fatalf("list auth_password_resets indexes: %v", err)
		}
		for _, want := range []string{
			"auth_password_resets_token_hash_key",
			"auth_password_resets_user_id_idx",
			"auth_password_resets_expires_at_idx",
		} {
			if !resetIndexes[want] {
				t.Errorf("missing index %s (have %v)", want, resetIndexes)
			}
		}

		var authVersionConstraint int
		err = db.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_constraint
			WHERE conname = 'users_auth_version_nonnegative' AND conrelid = 'users'::regclass
		`).Scan(&authVersionConstraint)
		if err != nil {
			t.Fatalf("check users_auth_version_nonnegative constraint: %v", err)
		}
		if authVersionConstraint != 1 {
			t.Fatalf("users_auth_version_nonnegative constraint count = %d, want 1", authVersionConstraint)
		}
	})

	t.Run("rerun is idempotent", func(t *testing.T) {
		before, err := goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("version before rerun: %v", err)
		}
		if err := RunMigrations(ctx, db, pgCfg); err != nil {
			t.Fatalf("rerun migrations: %v", err)
		}
		after, err := goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("version after rerun: %v", err)
		}
		if before != after {
			t.Fatalf("rerun changed version %d -> %d", before, after)
		}

		var users int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM users WHERE username IN ('alice', 'root')
		`).Scan(&users); err != nil {
			t.Fatalf("count users after rerun: %v", err)
		}
		if users != 2 {
			t.Fatalf("rerun altered data: found %d users, want 2", users)
		}
	})

	t.Run("006 schema composite FKs, bindings and score constraints", func(t *testing.T) {
		var aliceID, rootID string
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM users WHERE username = 'alice'`).Scan(&aliceID); err != nil {
			t.Fatalf("load alice: %v", err)
		}
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM users WHERE username = 'root'`).Scan(&rootID); err != nil {
			t.Fatalf("load root: %v", err)
		}

		var boardA, boardB string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility, created_by)
			VALUES ('p1-board-a', 'P1 Board A', 'INDIVIDUAL', 'OPEN', 'PRIVATE', $1)
			RETURNING id::text
		`, aliceID).Scan(&boardA); err != nil {
			t.Fatalf("insert board A: %v", err)
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility, created_by)
			VALUES ('p1-board-b', 'P1 Board B', 'INDIVIDUAL', 'OPEN', 'PRIVATE', $1)
			RETURNING id::text
		`, aliceID).Scan(&boardB); err != nil {
			t.Fatalf("insert board B: %v", err)
		}

		var moduleA, moduleB string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO modules (board_id, name, type, allowed_task_types, created_by)
			VALUES ($1, 'Module A', 'TASKS', ARRAY['FLAG'], $2)
			RETURNING id::text
		`, boardA, aliceID).Scan(&moduleA); err != nil {
			t.Fatalf("insert module A: %v", err)
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO modules (board_id, name, type, allowed_task_types, created_by)
			VALUES ($1, 'Module B', 'TASKS', ARRAY['FLAG'], $2)
			RETURNING id::text
		`, boardB, aliceID).Scan(&moduleB); err != nil {
			t.Fatalf("insert module B: %v", err)
		}

		var taskA string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO tasks (board_id, module_id, title, category, type, points, submission_mode, submission_checker, created_by)
			VALUES ($1, $2, 'Flag A', 'web', 'FLAG', 100, 'TEXT', 'AUTOMATIC', $3)
			RETURNING id::text
		`, boardA, moduleA, aliceID).Scan(&taskA); err != nil {
			t.Fatalf("insert task A: %v", err)
		}
		expectPgError(t, db, pgerrcode.ForeignKeyViolation, `
			INSERT INTO tasks (board_id, module_id, title, category, type, points, submission_mode, submission_checker, created_by)
			VALUES ($1, $2, 'Cross board', 'web', 'FLAG', 10, 'TEXT', 'AUTOMATIC', $3)
		`, boardA, moduleB, aliceID)
		expectPgError(t, db, pgerrcode.CheckViolation, `
			INSERT INTO tasks (board_id, module_id, title, category, type, points, submission_mode, submission_checker, created_by)
			VALUES ($1, $2, 'Bad flag', 'web', 'FLAG', 10, 'NONE', 'AUTOMATIC', $3)
		`, boardA, moduleA, aliceID)

		if _, err := db.ExecContext(ctx, `
			INSERT INTO task_flags (task_id, answer_hash) VALUES ($1, '$argon2id$test')
		`, taskA); err != nil {
			t.Fatalf("insert task flag hash: %v", err)
		}
		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO task_flags (task_id, answer_hash) VALUES ($1, '$argon2id$test')
		`, taskA)

		if _, err := db.ExecContext(ctx, `
			INSERT INTO board_participants (board_id, user_id) VALUES ($1, $2)
		`, boardA, aliceID); err != nil {
			t.Fatalf("insert board A participant: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO board_participants (board_id, user_id) VALUES ($1, $2)
		`, boardB, aliceID); err != nil {
			t.Fatalf("insert board B participant: %v", err)
		}

		// Idempotency uniqueness and one accepted score per user/task.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, idempotency_key, request_hash)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'REJECTED', 'idem-key-rejected-01', $5)
		`, boardA, moduleA, taskA, aliceID, []byte{0x01}); err != nil {
			t.Fatalf("insert rejected submission: %v", err)
		}
		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, idempotency_key, request_hash)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'REJECTED', 'idem-key-rejected-01', $5)
		`, boardA, moduleA, taskA, aliceID, []byte{0x02})

		if _, err := db.ExecContext(ctx, `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, score_awarded, idempotency_key, request_hash)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'ACCEPTED', 100, 'idem-key-accepted-01', $5)
		`, boardA, moduleA, taskA, aliceID, []byte{0x03}); err != nil {
			t.Fatalf("insert accepted submission: %v", err)
		}
		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, score_awarded, idempotency_key, request_hash)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'ACCEPTED', 100, 'idem-key-accepted-02', $5)
		`, boardA, moduleA, taskA, aliceID, []byte{0x04})

		// Participant ownership and composite parent FKs.
		expectPgError(t, db, pgerrcode.ForeignKeyViolation, `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, idempotency_key, request_hash)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'REJECTED', 'idem-key-not-joined', $5)
		`, boardA, moduleA, taskA, rootID, []byte{0x05})
		expectPgError(t, db, pgerrcode.ForeignKeyViolation, `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, idempotency_key, request_hash)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'REJECTED', 'idem-key-cross-task', $5)
		`, boardB, moduleB, taskA, aliceID, []byte{0x06})

		// Owner binding is board-wide; module owner is rejected.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
			VALUES ($1, $2, 'board_owner', $2)
		`, boardA, aliceID); err != nil {
			t.Fatalf("insert owner binding: %v", err)
		}
		expectPgError(t, db, pgerrcode.CheckViolation, `
			INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
			VALUES ($1, $2, $3, 'board_owner', $3)
		`, boardA, moduleA, aliceID)

		// Active binding uniqueness; expired-but-unrevoked still blocks until revoked.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
			VALUES ($1, $2, $3, 'board_editor', $3)
		`, boardA, moduleA, aliceID); err != nil {
			t.Fatalf("insert editor binding: %v", err)
		}
		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
			VALUES ($1, $2, $3, 'board_editor', $3)
		`, boardA, moduleA, aliceID)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by, expires_at)
			VALUES ($1, $2, $3, 'reviewer', $3, NOW() + INTERVAL '1 hour')
		`, boardA, moduleA, aliceID); err != nil {
			t.Fatalf("insert reviewer binding: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE board_role_bindings
			SET created_at = NOW() - INTERVAL '2 hours', expires_at = NOW() - INTERVAL '1 hour'
			WHERE board_id = $1 AND module_id = $2 AND user_id = $3 AND role = 'reviewer'
		`, boardA, moduleA, aliceID); err != nil {
			t.Fatalf("expire reviewer binding: %v", err)
		}
		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
			VALUES ($1, $2, $3, 'reviewer', $3)
		`, boardA, moduleA, aliceID)
		if _, err := db.ExecContext(ctx, `
			UPDATE board_role_bindings SET revoked_at = NOW(), revoked_by = $3
			WHERE board_id = $1 AND module_id = $2 AND user_id = $3 AND role = 'reviewer'
		`, boardA, moduleA, aliceID); err != nil {
			t.Fatalf("revoke reviewer binding: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
			VALUES ($1, $2, $3, 'reviewer', $3)
		`, boardA, moduleA, aliceID); err != nil {
			t.Fatalf("regrant reviewer after revoke: %v", err)
		}
		expectPgError(t, db, pgerrcode.ForeignKeyViolation, `
			INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
			VALUES ($1, $2, $3, 'reviewer', $3)
		`, boardA, moduleB, aliceID)
	})

	t.Run("006 down preserves P0 rows and up restores schema", func(t *testing.T) {
		count := func(query string) int {
			t.Helper()
			var n int
			if err := db.QueryRowContext(ctx, query).Scan(&n); err != nil {
				t.Fatalf("count query %q: %v", query, err)
			}
			return n
		}
		usersBefore := count(`SELECT count(*) FROM users`)
		sessionsBefore := count(`SELECT count(*) FROM auth_sessions`)
		resetsBefore := count(`SELECT count(*) FROM auth_password_resets`)
		verificationsBefore := count(`SELECT count(*) FROM auth_email_verifications`)

		if err := goose.DownToContext(ctx, db, migrationDir, 5); err != nil {
			t.Fatalf("roll back 006: %v", err)
		}
		version, err := goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("read version after 006 down: %v", err)
		}
		if version != 5 {
			t.Fatalf("goose version after 006 down = %d, want 5", version)
		}
		for _, table := range []string{"boards", "modules", "tasks", "task_flags", "board_participants", "submissions", "board_role_bindings", "audit_events"} {
			var reg sql.NullString
			if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", table).Scan(&reg); err != nil {
				t.Fatalf("check %s after 006 down: %v", table, err)
			}
			if reg.Valid {
				t.Fatalf("table %s still exists after 006 down", table)
			}
		}
		if got := count(`SELECT count(*) FROM users`); got != usersBefore {
			t.Fatalf("006 down changed users %d -> %d", usersBefore, got)
		}
		if got := count(`SELECT count(*) FROM auth_sessions`); got != sessionsBefore {
			t.Fatalf("006 down changed auth_sessions %d -> %d", sessionsBefore, got)
		}
		if got := count(`SELECT count(*) FROM auth_password_resets`); got != resetsBefore {
			t.Fatalf("006 down changed auth_password_resets %d -> %d", resetsBefore, got)
		}
		if got := count(`SELECT count(*) FROM auth_email_verifications`); got != verificationsBefore {
			t.Fatalf("006 down changed auth_email_verifications %d -> %d", verificationsBefore, got)
		}

		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-apply 006: %v", err)
		}
		version, err = goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("read version after 006 up: %v", err)
		}
		if version != 17 {
			t.Fatalf("goose version after 006 up = %d, want 17", version)
		}
		for _, table := range []string{"boards", "modules", "tasks", "task_flags", "board_participants", "submissions", "board_role_bindings", "audit_events"} {
			var reg sql.NullString
			if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", table).Scan(&reg); err != nil {
				t.Fatalf("check %s after 006 up: %v", table, err)
			}
			if !reg.Valid {
				t.Fatalf("table %s missing after 006 up", table)
			}
		}
	})

	t.Run("005 down preserves users and up restores schema", func(t *testing.T) {
		var usersBefore int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&usersBefore); err != nil {
			t.Fatalf("count users before 005 down: %v", err)
		}

		if err := goose.DownToContext(ctx, db, migrationDir, 4); err != nil {
			t.Fatalf("roll back 005: %v", err)
		}
		version, err := goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("read version after 005 down: %v", err)
		}
		if version != 4 {
			t.Fatalf("goose version after 005 down = %d, want 4", version)
		}

		var resetTable sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", "auth_password_resets").Scan(&resetTable); err != nil {
			t.Fatalf("check auth_password_resets after 005 down: %v", err)
		}
		if resetTable.Valid {
			t.Fatal("auth_password_resets still exists after 005 down")
		}
		var hasAuthVersion bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema()
				  AND table_name = 'users'
				  AND column_name = 'auth_version'
			)
		`).Scan(&hasAuthVersion); err != nil {
			t.Fatalf("check users.auth_version after 005 down: %v", err)
		}
		if hasAuthVersion {
			t.Fatal("users.auth_version still exists after 005 down")
		}

		var usersAfter int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&usersAfter); err != nil {
			t.Fatalf("count users after 005 down: %v", err)
		}
		if usersAfter != usersBefore {
			t.Fatalf("005 down changed user count %d -> %d", usersBefore, usersAfter)
		}
		var legacyExists bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM auth_sessions WHERE id = $1)
		`, legacySessionIDs[0]).Scan(&legacyExists); err != nil {
			t.Fatalf("check legacy session after 005 down: %v", err)
		}
		if !legacyExists {
			t.Fatal("005 down removed the legacy session row")
		}

		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-apply 005: %v", err)
		}
		version, err = goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("read version after 005 up: %v", err)
		}
		if version != 17 {
			t.Fatalf("goose version after 005 up = %d, want 17", version)
		}
		var nonZero int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM users WHERE auth_version <> 0
		`).Scan(&nonZero); err != nil {
			t.Fatalf("check auth_version after 005 up: %v", err)
		}
		if nonZero != 0 {
			t.Fatalf("005 up left %d users with nonzero auth_version", nonZero)
		}
		if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", "auth_password_resets").Scan(&resetTable); err != nil {
			t.Fatalf("check auth_password_resets after 005 up: %v", err)
		}
		if !resetTable.Valid {
			t.Fatal("auth_password_resets missing after 005 up")
		}
	})

	t.Run("004 down preserves core session rows and up restores", func(t *testing.T) {
		var beforeCount int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM auth_sessions`).Scan(&beforeCount); err != nil {
			t.Fatalf("count sessions before 004 down: %v", err)
		}
		if beforeCount == 0 {
			t.Fatal("expected at least one session before 004 down")
		}

		if err := goose.DownToContext(ctx, db, migrationDir, 3); err != nil {
			t.Fatalf("roll back 004: %v", err)
		}

		version, err := goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("read version after 004 down: %v", err)
		}
		if version != 3 {
			t.Fatalf("goose version after 004 down = %d, want 3", version)
		}

		for _, column := range []string{"family_id", "revoked_reason"} {
			var exists bool
			if err := db.QueryRowContext(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.columns
					WHERE table_schema = current_schema()
					  AND table_name = 'auth_sessions'
					  AND column_name = $1
				)
			`, column).Scan(&exists); err != nil {
				t.Fatalf("check auth_sessions.%s after 004 down: %v", column, err)
			}
			if exists {
				t.Fatalf("auth_sessions.%s still exists after 004 down", column)
			}
		}

		var indexExists bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_indexes
				WHERE schemaname = current_schema()
				  AND tablename = 'auth_sessions'
				  AND indexname = 'auth_sessions_family_id_idx'
			)
		`).Scan(&indexExists); err != nil {
			t.Fatalf("check family index after 004 down: %v", err)
		}
		if indexExists {
			t.Fatal("auth_sessions_family_id_idx still exists after 004 down")
		}

		var afterCount int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM auth_sessions`).Scan(&afterCount); err != nil {
			t.Fatalf("count sessions after 004 down: %v", err)
		}
		if afterCount != beforeCount {
			t.Fatalf("004 down changed session row count %d -> %d", beforeCount, afterCount)
		}
		var legacyExists bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM auth_sessions WHERE id = $1)
		`, legacySessionIDs[0]).Scan(&legacyExists); err != nil {
			t.Fatalf("check legacy session after 004 down: %v", err)
		}
		if !legacyExists {
			t.Fatal("004 down removed the legacy session row")
		}

		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-apply 004: %v", err)
		}
		version, err = goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("read version after 004 up: %v", err)
		}
		if version != 17 {
			t.Fatalf("goose version after 004 up = %d, want 17", version)
		}

		var totalSessions, familyMatches, revokedSessions, legacyReasons int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*), count(*) FILTER (WHERE family_id = id)
			FROM auth_sessions
		`).Scan(&totalSessions, &familyMatches); err != nil {
			t.Fatalf("check 004 family backfill after up: %v", err)
		}
		if familyMatches != totalSessions {
			t.Fatalf("family backfill after 004 up = %d/%d rows", familyMatches, totalSessions)
		}
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FILTER (WHERE revoked_at IS NOT NULL),
			       count(*) FILTER (WHERE revoked_at IS NOT NULL AND revoked_reason = 'legacy')
			FROM auth_sessions
		`).Scan(&revokedSessions, &legacyReasons); err != nil {
			t.Fatalf("check 004 revocation backfill after up: %v", err)
		}
		if legacyReasons != revokedSessions {
			t.Fatalf("revocation reason backfill after 004 up = %d/%d rows", legacyReasons, revokedSessions)
		}

		var familyNullable string
		if err := db.QueryRowContext(ctx, `
			SELECT is_nullable FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'auth_sessions'
			  AND column_name = 'family_id'
		`).Scan(&familyNullable); err != nil {
			t.Fatalf("check family_id nullability after 004 up: %v", err)
		}
		if familyNullable != "NO" {
			t.Fatalf("auth_sessions.family_id is_nullable = %q, want NO", familyNullable)
		}
	})

	t.Run("003 down and up", func(t *testing.T) {
		if err := goose.DownToContext(ctx, db, migrationDir, 2); err != nil {
			t.Fatalf("roll back 003: %v", err)
		}

		var verificationReg sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", "auth_email_verifications").Scan(&verificationReg); err != nil {
			t.Fatalf("check auth_email_verifications after 003 down: %v", err)
		}
		if verificationReg.Valid {
			t.Fatal("auth_email_verifications still exists after 003 down")
		}
		for _, table := range []string{"users", "auth_sessions"} {
			var reg sql.NullString
			if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", table).Scan(&reg); err != nil {
				t.Fatalf("check %s after 003 down: %v", table, err)
			}
			if !reg.Valid {
				t.Fatalf("table %s was removed by 003 down", table)
			}
		}

		version, err := goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("read version after 003 down: %v", err)
		}
		if version != 2 {
			t.Fatalf("goose version after 003 down = %d, want 2", version)
		}

		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-apply 003/004: %v", err)
		}
		version, err = goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("read version after 003 up: %v", err)
		}
		if version != 17 {
			t.Fatalf("goose version after 003 up = %d, want 17", version)
		}
		if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", "auth_email_verifications").Scan(&verificationReg); err != nil {
			t.Fatalf("check auth_email_verifications after 003 up: %v", err)
		}
		if !verificationReg.Valid {
			t.Fatal("auth_email_verifications missing after 003 up")
		}
	})

	t.Run("down and up in isolated schema", func(t *testing.T) {
		if err := goose.DownToContext(ctx, db, migrationDir, 0); err != nil {
			t.Fatalf("roll back migrations: %v", err)
		}

		for _, table := range []string{"users", "auth_sessions", "auth_email_verifications", "auth_password_resets", "boards", "modules", "tasks", "task_flags", "board_participants", "submissions", "board_role_bindings", "audit_events"} {
			var reg sql.NullString
			if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", table).Scan(&reg); err != nil {
				t.Fatalf("check %s after down: %v", table, err)
			}
			if reg.Valid {
				t.Fatalf("table %s still exists after down", table)
			}
		}

		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-apply migrations after down: %v", err)
		}

		var hasCreatedAt bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema()
				  AND table_name = 'users'
				  AND column_name = 'created_at'
			)
		`).Scan(&hasCreatedAt); err != nil {
			t.Fatalf("check users.created_at after re-up: %v", err)
		}
		if !hasCreatedAt {
			t.Fatal("users.created_at missing after re-up")
		}

		var hasVerificationTable bool
		if err := db.QueryRowContext(ctx, `
			SELECT to_regclass('auth_email_verifications') IS NOT NULL
		`).Scan(&hasVerificationTable); err != nil {
			t.Fatalf("check auth_email_verifications after re-up: %v", err)
		}
		if !hasVerificationTable {
			t.Fatal("auth_email_verifications missing after re-up")
		}

		var hasFamilyColumn, hasReasonColumn bool
		if err := db.QueryRowContext(ctx, `
			SELECT
				EXISTS (SELECT 1 FROM information_schema.columns
					WHERE table_schema = current_schema() AND table_name = 'auth_sessions' AND column_name = 'family_id'),
				EXISTS (SELECT 1 FROM information_schema.columns
					WHERE table_schema = current_schema() AND table_name = 'auth_sessions' AND column_name = 'revoked_reason')
		`).Scan(&hasFamilyColumn, &hasReasonColumn); err != nil {
			t.Fatalf("check 004 columns after re-up: %v", err)
		}
		if !hasFamilyColumn || !hasReasonColumn {
			t.Fatalf("004 columns after re-up: family_id=%v revoked_reason=%v", hasFamilyColumn, hasReasonColumn)
		}

		var hasAuthVersion, hasResetTable bool
		if err := db.QueryRowContext(ctx, `
			SELECT
				EXISTS (SELECT 1 FROM information_schema.columns
					WHERE table_schema = current_schema() AND table_name = 'users' AND column_name = 'auth_version'),
				to_regclass('auth_password_resets') IS NOT NULL
		`).Scan(&hasAuthVersion, &hasResetTable); err != nil {
			t.Fatalf("check 005 schema after re-up: %v", err)
		}
		if !hasAuthVersion || !hasResetTable {
			t.Fatalf("005 schema after re-up: auth_version=%v auth_password_resets=%v", hasAuthVersion, hasResetTable)
		}

		for _, table := range []string{"boards", "modules", "tasks", "submissions", "board_role_bindings", "audit_events"} {
			var reg sql.NullString
			if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", table).Scan(&reg); err != nil {
				t.Fatalf("check %s after 006 re-up: %v", table, err)
			}
			if !reg.Valid {
				t.Fatalf("table %s missing after 006 re-up", table)
			}
		}
	})
}

func dsnWithSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", errors.New("ACTFB_TEST_POSTGRES_DSN must use postgres:// or postgresql:// URL form")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func expectPgError(t *testing.T, db *sql.DB, sqlState string, query string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), query, args...)
	if err == nil {
		t.Fatalf("query unexpectedly succeeded, wanted SQLSTATE %s", sqlState)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected PostgreSQL error %s, got %T: %v", sqlState, err, err)
	}
	if pgErr.Code != sqlState {
		t.Fatalf("got SQLSTATE %s (%s), want %s", pgErr.Code, pgErr.Message, sqlState)
	}
}

func indexesForTable(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = current_schema() AND tablename = $1
	`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	indexes := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		indexes[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return indexes, nil
}

// TestMigrationsRemainingStages is opt-in: it applies 001-006 first, seeds
// P0/P1 rows, then applies all remaining additive migrations and exercises
// backfill/down/up for 007-011 while proving the seeded rows survive.
//
// Run with:
//
//	ACTFB_TEST_POSTGRES_DSN='postgres://user:pass@127.0.0.1:55441/actfb?sslmode=disable' \
//	  go test ./internal/platform/postgres -run TestMigrationsRemainingStages -v
func TestMigrationsRemainingStages(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the real PostgreSQL migration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	schema := "actfb_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(schema, "actfb_test_") || len(schema) > 63 {
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

	scopedDSN, err := dsnWithSearchPath(dsn, schema)
	if err != nil {
		t.Fatalf("build scoped DSN: %v", err)
	}
	db, err := sql.Open("pgx", scopedDSN)
	if err != nil {
		t.Fatalf("open scoped connection: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping scoped schema: %v", err)
	}

	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if err := goose.UpToContext(ctx, db, migrationDir, 6); err != nil {
		t.Fatalf("apply migrations through 006: %v", err)
	}
	if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 6 {
		t.Fatalf("version before remaining migrations = %d (%v), want 6", v, err)
	}

	var aliceID, bobID, boardID, moduleID, taskID, sessionID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO users (username, email, password)
		VALUES ('remaining-alice', 'remaining-alice@example.test', 'argon2id-hash')
		RETURNING id::text
	`).Scan(&aliceID); err != nil {
		t.Fatalf("insert alice: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO users (username, email, password)
		VALUES ('remaining-bob', 'remaining-bob@example.test', 'argon2id-hash')
		RETURNING id::text
	`).Scan(&bobID); err != nil {
		t.Fatalf("insert bob: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO auth_sessions (user_id, token_hash, expires_at, family_id)
		VALUES ($1, $2, NOW() + INTERVAL '1 hour', $3)
		RETURNING id::text
	`, aliceID, []byte{0x71, 0x72, 0x73}, uuid.NewString()).Scan(&sessionID); err != nil {
		t.Fatalf("insert P0 session: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility, created_by)
		VALUES ('remaining-board', 'Remaining Board', 'INDIVIDUAL', 'OPEN', 'PRIVATE', $1)
		RETURNING id::text
	`, aliceID).Scan(&boardID); err != nil {
		t.Fatalf("insert board: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO modules (board_id, name, type, allowed_task_types, created_by)
		VALUES ($1, 'Remaining Module', 'TASKS', ARRAY['FLAG'], $2)
		RETURNING id::text
	`, boardID, aliceID).Scan(&moduleID); err != nil {
		t.Fatalf("insert module: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO tasks (board_id, module_id, title, category, type, points, submission_mode, submission_checker, created_by)
		VALUES ($1, $2, 'Remaining Task', 'web', 'FLAG', 100, 'TEXT', 'AUTOMATIC', $3)
		RETURNING id::text
	`, boardID, moduleID, aliceID).Scan(&taskID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO board_participants (board_id, user_id) VALUES ($1, $2)`, boardID, aliceID); err != nil {
		t.Fatalf("insert participant: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO submissions (board_id, module_id, task_id, user_id, status, score_awarded, idempotency_key, request_hash)
		VALUES ($1, $2, $3, $4, 'ACCEPTED', 100, 'remaining-accepted-001', $5)
	`, boardID, moduleID, taskID, aliceID, []byte{0x74}); err != nil {
		t.Fatalf("insert accepted submission: %v", err)
	}

	if err := goose.UpContext(ctx, db, migrationDir); err != nil {
		t.Fatalf("apply remaining migrations 007-011: %v", err)
	}
	if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 17 {
		t.Fatalf("version after remaining migrations = %d (%v), want 17", v, err)
	}

	columnExists := func(table, column string) bool {
		t.Helper()
		var exists bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2
			)
		`, table, column).Scan(&exists); err != nil {
			t.Fatalf("check column %s.%s: %v", table, column, err)
		}
		return exists
	}
	tableExists := func(table string) bool {
		t.Helper()
		var reg sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT to_regclass($1)::text", table).Scan(&reg); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		return reg.Valid
	}
	assertSeededRows := func(t *testing.T) {
		t.Helper()
		var subjectType, subjectID, submittedBy, session, status string
		var score int
		if err := db.QueryRowContext(ctx, `
			SELECT subject_type, subject_id::text, submitted_by_user_id::text, status, score_awarded
			FROM submissions WHERE idempotency_key = 'remaining-accepted-001'
		`).Scan(&subjectType, &subjectID, &submittedBy, &status, &score); err != nil {
			t.Fatalf("read seeded submission: %v", err)
		}
		if subjectType != "USER" || subjectID != aliceID || submittedBy != aliceID || status != "ACCEPTED" || score != 100 {
			t.Fatalf("seeded submission changed: type=%s subject=%s submitted=%s status=%s score=%d", subjectType, subjectID, submittedBy, status, score)
		}
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM auth_sessions WHERE id = $1`, sessionID).Scan(&session); err != nil || session != sessionID {
			t.Fatalf("P0 session changed: %q %v", session, err)
		}
	}

	t.Run("007 backfill and constraints", func(t *testing.T) {
		for _, table := range []string{"access_groups", "access_group_members", "board_access_groups", "audiences", "audience_members", "module_audiences", "teams", "team_members", "permission_grants"} {
			if !tableExists(table) {
				t.Fatalf("%s missing after 007", table)
			}
		}
		for _, col := range []string{"team_min_members", "team_max_members", "team_allow_self_create", "team_allow_leave_after_start", "invite_code_hash", "invite_code_ciphertext", "invite_code_expires_at"} {
			if !columnExists("boards", col) {
				t.Fatalf("boards.%s missing after 007", col)
			}
		}
		for _, col := range []string{"subject_type", "subject_id", "submitted_by_user_id"} {
			if !columnExists("submissions", col) {
				t.Fatalf("submissions.%s missing after 007", col)
			}
		}
		if !columnExists("teams", "invite_key_ciphertext") {
			t.Fatal("teams.invite_key_ciphertext missing after 007")
		}
		assertSeededRows(t)
		// Captain/team-member FKs must reject non-participants.
		expectPgError(t, db, pgerrcode.ForeignKeyViolation, `
			INSERT INTO teams (board_id, name, captain_user_id)
			VALUES ($1, 'nonmember-team', $2)
		`, boardID, bobID)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO audiences (board_id, name) VALUES ($1, 'audience-a')
		`, boardID); err != nil {
			t.Fatalf("insert audience: %v", err)
		}
		var audienceID string
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM audiences WHERE board_id = $1 AND name = 'audience-a'`, boardID).Scan(&audienceID); err != nil {
			t.Fatalf("read audience: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO module_audiences (board_id, module_id, audience_id) VALUES ($1, $2, $3)
		`, boardID, moduleID, audienceID); err != nil {
			t.Fatalf("insert module audience: %v", err)
		}
	})

	t.Run("008 file/manual/events schema", func(t *testing.T) {
		for _, table := range []string{"submission_drafts", "file_objects", "submission_files", "task_attachments", "events_outbox"} {
			if !tableExists(table) {
				t.Fatalf("%s missing after 008", table)
			}
		}
		for _, col := range []string{"submission_text_policy", "submission_files_policy"} {
			if !columnExists("tasks", col) {
				t.Fatalf("tasks.%s missing after 008", col)
			}
		}
		for _, col := range []string{"reviewed_by_user_id", "review_decision"} {
			if !columnExists("submissions", col) {
				t.Fatalf("submissions.%s missing after 008", col)
			}
		}
		assertSeededRows(t)
	})

	t.Run("009 deployment/runtime/pentest schema", func(t *testing.T) {
		for _, table := range []string{"integrations", "deployment_templates", "deployment_template_drafts", "deployment_template_versions", "deployment_bindings", "deployment_binding_targets", "instances", "instance_nodes", "instance_operations", "instance_endpoints", "instance_secret_values", "pentest_configs", "pentest_environments", "pentest_submissions", "pentest_submission_files"} {
			if !tableExists(table) {
				t.Fatalf("%s missing after 009", table)
			}
		}
		for _, col := range []string{"deployment_binding_id"} {
			if !columnExists("tasks", col) || !columnExists("modules", col) {
				t.Fatalf("deployment_binding_id missing on tasks/modules after 009")
			}
		}
		assertSeededRows(t)
	})

	t.Run("010 plugin schema", func(t *testing.T) {
		for _, table := range []string{"plugins", "plugin_versions", "plugin_permissions", "plugin_event_deliveries", "plugin_settings", "plugin_migrations", "plugin_delegations"} {
			if !tableExists(table) {
				t.Fatalf("%s missing after 010", table)
			}
		}
		for _, col := range []string{"extension_key"} {
			if !columnExists("modules", col) || !columnExists("tasks", col) {
				t.Fatalf("%s missing on modules/tasks after 010", col)
			}
		}
		if !columnExists("tasks", "checker_key") {
			t.Fatal("tasks.checker_key missing after 010")
		}
		expectPgError(t, db, pgerrcode.CheckViolation, `
			INSERT INTO modules (board_id, name, type, allowed_task_types, created_by)
			VALUES ($1, 'ext-without-key', 'EXTENSION', ARRAY['EXTENSION'], $2)
		`, boardID, aliceID)
		var extModule string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO modules (board_id, name, type, allowed_task_types, extension_key, created_by)
			VALUES ($1, 'ext-module', 'EXTENSION', ARRAY['EXTENSION'], 'example.module', $2)
			RETURNING id::text
		`, boardID, aliceID).Scan(&extModule); err != nil {
			t.Fatalf("insert EXTENSION module: %v", err)
		}
		expectPgError(t, db, pgerrcode.CheckViolation, `
			INSERT INTO tasks (board_id, module_id, title, category, type, points, submission_mode, submission_checker, created_by)
			VALUES ($1, $2, 'ext-without-key', 'web', 'EXTENSION', 10, 'NONE', 'AUTOMATIC', $3)
		`, boardID, extModule, aliceID)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO tasks (board_id, module_id, title, category, type, points, submission_mode, submission_checker, extension_key, checker_key, created_by)
			VALUES ($1, $2, 'ext-task', 'web', 'EXTENSION', 10, 'TEXT', 'PLUGIN', 'example.task', 'example.checker', $3)
		`, boardID, extModule, aliceID); err != nil {
			t.Fatalf("insert EXTENSION/PLUGIN task: %v", err)
		}
		expectPgError(t, db, pgerrcode.CheckViolation, `
			INSERT INTO tasks (board_id, module_id, title, category, type, points, submission_mode, submission_checker, extension_key, created_by)
			VALUES ($1, $2, 'plugin-without-key', 'web', 'EXTENSION', 10, 'TEXT', 'PLUGIN', 'example.task', $3)
		`, boardID, extModule, aliceID)
		assertSeededRows(t)
	})

	t.Run("011 attack-defense schema", func(t *testing.T) {
		for _, table := range []string{"ad_configs", "ad_vulnerabilities", "ad_repository_assignments", "ad_runs", "ad_run_logs", "git_webhook_deliveries"} {
			if !tableExists(table) {
				t.Fatalf("%s missing after 011", table)
			}
		}
		assertSeededRows(t)
	})

	t.Run("012 runtime security invariants", func(t *testing.T) {
		for _, table := range []string{"deployment_template_allowlist", "deployment_template_allowlist_targets", "instance_network_leases", "repository_bindings", "ad_run_results", "ad_awards"} {
			if !tableExists(table) {
				t.Fatalf("%s missing after 012", table)
			}
		}
		for _, col := range []string{"binding_id", "template_version_id", "desired_snapshot", "request_actor_id", "removed_at"} {
			if !columnExists("instances", col) {
				t.Fatalf("instances.%s missing after 012", col)
			}
		}
		for _, col := range []string{"requested_by", "authorized_context", "desired_snapshot"} {
			if !columnExists("instance_operations", col) {
				t.Fatalf("instance_operations.%s missing after 012", col)
			}
		}
		if !columnExists("file_objects", "attached_pentest_submission_id") {
			t.Fatal("file_objects.attached_pentest_submission_id missing after 012")
		}

		var integrationID string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO integrations (category, type, kind, driver, name, created_by)
			VALUES ('COMPUTE','PROXMOX','VM','proxmox','allowlist-provider', $1)
			RETURNING id::text
		`, aliceID).Scan(&integrationID); err != nil {
			t.Fatalf("insert integration: %v", err)
		}
		var templateID, versionID, allowlistID string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO deployment_templates (slug, name, description, created_by)
			VALUES ('allowlist-template','Allowlist Template','', $1)
			RETURNING id::text
		`, aliceID).Scan(&templateID); err != nil {
			t.Fatalf("insert template: %v", err)
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO deployment_template_versions (template_id, version, digest, manifest, files, published_by)
			VALUES ($1, 1, 'digest-1', '{}', '[]', $2)
			RETURNING id::text
		`, templateID, aliceID).Scan(&versionID); err != nil {
			t.Fatalf("insert template version: %v", err)
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO deployment_template_allowlist (template_version_id, board_id, approved_by)
			VALUES ($1, $2, $3)
			RETURNING id::text
		`, versionID, boardID, aliceID).Scan(&allowlistID); err != nil {
			t.Fatalf("insert allowlist: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO deployment_template_allowlist_targets (allowlist_id, kind, integration_id)
			VALUES ($1, 'VM', $2)
		`, allowlistID, integrationID); err != nil {
			t.Fatalf("insert allowlist target: %v", err)
		}
		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO deployment_template_allowlist (template_version_id, board_id, approved_by)
			VALUES ($1, $2, $3)
		`, versionID, boardID, aliceID)

		var boardB, moduleB string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility, created_by)
			VALUES ('allowlist-other','Other','INDIVIDUAL','OPEN','PRIVATE',$1)
			RETURNING id::text
		`, aliceID).Scan(&boardB); err != nil {
			t.Fatalf("insert other board: %v", err)
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO modules (board_id, name, type, allowed_task_types, created_by)
			VALUES ($1,'Other Module','TASKS',ARRAY['FLAG'],$2)
			RETURNING id::text
		`, boardB, aliceID).Scan(&moduleB); err != nil {
			t.Fatalf("insert other module: %v", err)
		}
		expectPgError(t, db, pgerrcode.ForeignKeyViolation, `
			INSERT INTO deployment_template_allowlist (template_version_id, board_id, module_id, approved_by)
			VALUES ($1, $2, $3, $4)
		`, versionID, boardID, moduleB, aliceID)

		// Published versions are immutable at the DB layer.
		expectPgError(t, db, "P0001", `
			UPDATE deployment_template_versions SET digest = 'mutated' WHERE id = $1
		`, versionID)

		var instanceID string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO instances (board_id, module_id, scope, scope_uuid, subject_type, subject_id, status, idempotency_key, binding_id, template_version_id, desired_snapshot, request_actor_id)
			VALUES ($1, $2, 'TASK', $3, 'USER', $4, 'PROVISIONING', 'allowlist-instance-0001', NULL, $5, '{}', $4)
			RETURNING id::text
		`, boardID, moduleID, taskID, aliceID, versionID).Scan(&instanceID); err != nil {
			t.Fatalf("insert instance: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO instance_network_leases (instance_id, integration_id, kind, allocation_key, claim)
			VALUES ($1, $2, 'VM', 'lease-a', '{}')
		`, instanceID, integrationID); err != nil {
			t.Fatalf("insert network lease: %v", err)
		}
		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO instance_network_leases (instance_id, integration_id, kind, allocation_key, claim)
			VALUES ($1, $2, 'VM', 'lease-a', '{}')
		`, instanceID, integrationID)
		if _, err := db.ExecContext(ctx, `
			UPDATE instance_network_leases SET released_at = NOW()
			WHERE instance_id = $1 AND integration_id = $2 AND allocation_key = 'lease-a'
		`, instanceID, integrationID); err != nil {
			t.Fatalf("release lease: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO instance_network_leases (instance_id, integration_id, kind, allocation_key, claim)
			VALUES ($1, $2, 'VM', 'lease-a', '{}')
		`, instanceID, integrationID); err != nil {
			t.Fatalf("reuse released lease: %v", err)
		}
		expectPgError(t, db, pgerrcode.RestrictViolation, `
			DELETE FROM boards WHERE id = $1
		`, boardID)

		var bindingID, assignmentID, vulnerabilityID, runID string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO repository_bindings (integration_id, repository_ref, branch, created_by)
			VALUES ($1, 'group/repo', 'main', $2)
			RETURNING id::text
		`, integrationID, aliceID).Scan(&bindingID); err != nil {
			t.Fatalf("insert repository binding: %v", err)
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO ad_repository_assignments (module_id, subject_type, subject_id, source_integration_id, source_repository, source_branch, exploit_integration_id, exploit_repository, exploit_branch, source_binding_id, exploit_binding_id, created_by)
			VALUES ($1, 'USER', $2, $3, 'group/repo', 'main', $3, 'group/exploit', 'main', $4, $4, $2)
			RETURNING id::text
		`, moduleID, aliceID, integrationID, bindingID).Scan(&assignmentID); err != nil {
			t.Fatalf("insert AD assignment: %v", err)
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO ad_vulnerabilities (module_id, key, title, exploit_points, patch_points, exploit_test_selector, patch_test_selector)
			VALUES ($1, 'v1', 'V1', 10, 10, 'exploit', 'patch')
			RETURNING id::text
		`, moduleID).Scan(&vulnerabilityID); err != nil {
			t.Fatalf("insert AD vulnerability: %v", err)
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO ad_runs (module_id, repository_assignment_id, subject_type, subject_id, kind, commit_sha, status, requested_by)
			VALUES ($1, $2, 'USER', $3, 'EXPLOIT', $4, 'QUEUED', $3)
			RETURNING id::text
		`, moduleID, assignmentID, aliceID, strings.Repeat("a", 40)).Scan(&runID); err != nil {
			t.Fatalf("insert AD run: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO ad_run_results (run_id, vulnerability_id, status, safe_verdict, score_awarded)
			VALUES ($1, $2, 'PASSED', '{}', 10)
		`, runID, vulnerabilityID); err != nil {
			t.Fatalf("insert AD result: %v", err)
		}
		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO ad_run_results (run_id, vulnerability_id, status, safe_verdict, score_awarded)
			VALUES ($1, $2, 'PASSED', '{}', 10)
		`, runID, vulnerabilityID)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO ad_awards (module_id, subject_type, subject_id, vulnerability_id, kind, run_id, points)
			VALUES ($1, 'USER', $2, $3, 'EXPLOIT', $4, 10)
		`, moduleID, aliceID, vulnerabilityID, runID); err != nil {
			t.Fatalf("insert AD award: %v", err)
		}
		expectPgError(t, db, pgerrcode.UniqueViolation, `
			INSERT INTO ad_awards (module_id, subject_type, subject_id, vulnerability_id, kind, run_id, points)
			VALUES ($1, 'USER', $2, $3, 'EXPLOIT', $4, 10)
		`, moduleID, aliceID, vulnerabilityID, runID)
		assertSeededRows(t)
	})

	t.Run("013 durable MANUAL text invariants", func(t *testing.T) {
		for _, col := range []string{"submitted_text", "task_type"} {
			if !columnExists("submissions", col) {
				t.Fatalf("submissions.%s missing after 013", col)
			}
		}
		var p1Type string
		var p1Text sql.NullString
		if err := db.QueryRowContext(ctx, `
			SELECT task_type, submitted_text FROM submissions WHERE idempotency_key = 'remaining-accepted-001'
		`).Scan(&p1Type, &p1Text); err != nil {
			t.Fatalf("read P1 subject row: %v", err)
		}
		if p1Type != "FLAG" || p1Text.Valid {
			t.Fatalf("P1 row type/text = %s/%v, want FLAG/null", p1Type, p1Text)
		}

		var manualTaskID string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO tasks (board_id, module_id, title, category, type, points, submission_mode, submission_checker, created_by)
			VALUES ($1, $2, 'Manual Text', 'web', 'MANUAL', 25, 'TEXT', 'MANUAL', $3)
			RETURNING id::text
		`, boardID, moduleID, aliceID).Scan(&manualTaskID); err != nil {
			t.Fatalf("insert MANUAL task: %v", err)
		}
		var manualSubmissionID string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, idempotency_key, request_hash, submitted_text)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'PENDING_REVIEW', 'manual-text-key-0001', $5, 'manual answer')
			RETURNING id::text
		`, boardID, moduleID, manualTaskID, aliceID, []byte{0x75}).Scan(&manualSubmissionID); err != nil {
			t.Fatalf("insert MANUAL text submission: %v", err)
		}
		var manualType, manualText string
		if err := db.QueryRowContext(ctx, `
			SELECT task_type, submitted_text FROM submissions WHERE id = $1
		`, manualSubmissionID).Scan(&manualType, &manualText); err != nil {
			t.Fatalf("read MANUAL submission: %v", err)
		}
		if manualType != "MANUAL" || manualText != "manual answer" {
			t.Fatalf("MANUAL submission = %s/%q", manualType, manualText)
		}
		expectPgError(t, db, "P0001", `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, idempotency_key, request_hash, submitted_text)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'REJECTED', 'flag-text-key-0001', $5, 'should reject')
		`, boardID, moduleID, taskID, aliceID, []byte{0x76})
		expectPgError(t, db, "P0001", `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, idempotency_key, request_hash, submitted_text, task_type)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'REJECTED', 'flag-spoof-key-0001', $5, 'spoof', 'MANUAL')
		`, boardID, moduleID, taskID, aliceID, []byte{0x77})
		expectPgError(t, db, "P0001", `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id, status, idempotency_key, request_hash, submitted_text)
			VALUES ($1, $2, $3, $4, 'USER', $4, $4, 'PENDING_REVIEW', 'manual-too-large-01', $5, $6)
		`, boardID, moduleID, manualTaskID, aliceID, []byte{0x78}, strings.Repeat("x", 4000001))
		expectPgError(t, db, "P0001", `
			UPDATE submissions SET task_type = 'FLAG' WHERE id = $1
		`, manualSubmissionID)
		if _, err := db.ExecContext(ctx, `UPDATE tasks SET type = 'FLAG', submission_checker = 'AUTOMATIC' WHERE id = $1`, manualTaskID); err != nil {
			t.Fatalf("change manual task type: %v", err)
		}
		if err := db.QueryRowContext(ctx, `SELECT task_type, submitted_text FROM submissions WHERE id = $1`, manualSubmissionID).Scan(&manualType, &manualText); err != nil || manualType != "MANUAL" || manualText != "manual answer" {
			t.Fatalf("snapshot after task type change = %s/%q (%v)", manualType, manualText, err)
		}
		assertSeededRows(t)
	})

	downUp := func(t *testing.T, target int64, absent []string, present []string) {
		t.Helper()
		if err := goose.DownToContext(ctx, db, migrationDir, target); err != nil {
			t.Fatalf("down to %d: %v", target, err)
		}
		if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != target {
			t.Fatalf("version after down to %d = %d (%v)", target, v, err)
		}
		for _, table := range absent {
			if tableExists(table) {
				t.Fatalf("%s still exists after down to %d", table, target)
			}
		}
		assertSeededRows(t)
		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-up from %d: %v", target, err)
		}
		if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 17 {
			t.Fatalf("version after re-up from %d = %d (%v)", target, v, err)
		}
		for _, table := range present {
			if !tableExists(table) {
				t.Fatalf("%s missing after re-up from %d", table, target)
			}
		}
		assertSeededRows(t)
	}

	t.Run("013 down preserves P0/P1 and up restores", func(t *testing.T) {
		if err := goose.DownToContext(ctx, db, migrationDir, 12); err != nil {
			t.Fatalf("down to 12: %v", err)
		}
		for _, col := range []string{"submitted_text", "task_type"} {
			if columnExists("submissions", col) {
				t.Fatalf("submissions.%s still exists after 013 down", col)
			}
		}
		var p1Type, session string
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM submissions WHERE idempotency_key = 'remaining-accepted-001'`).Scan(&p1Type); err != nil {
			t.Fatalf("P1 submission lost on 013 down: %v", err)
		}
		if p1Type == "" {
			t.Fatal("empty P1 submission id")
		}
		if err := db.QueryRowContext(ctx, `SELECT id::text FROM auth_sessions WHERE id = $1`, sessionID).Scan(&session); err != nil || session != sessionID {
			t.Fatalf("P0 session changed on 013 down: %q %v", session, err)
		}
		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-up from 12: %v", err)
		}
		if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 17 {
			t.Fatalf("version after latest re-up = %d (%v)", v, err)
		}
		assertSeededRows(t)
	})
	t.Run("014 down preserves plugin rows and up restores approval column", func(t *testing.T) {
		if err := goose.DownToContext(ctx, db, migrationDir, 13); err != nil {
			t.Fatalf("down to 13: %v", err)
		}
		if columnExists("plugins", "trusted_js_approved") {
			t.Fatal("plugins.trusted_js_approved still exists after 014 down")
		}
		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-up from 13: %v", err)
		}
		if !columnExists("plugins", "trusted_js_approved") {
			t.Fatal("plugins.trusted_js_approved missing after 014 re-up")
		}
		if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 17 {
			t.Fatalf("version after 014 re-up = %d (%v)", v, err)
		}
		assertSeededRows(t)
	})
	t.Run("015 down preserves plugin rows and up restores identity table", func(t *testing.T) {
		if err := goose.DownToContext(ctx, db, migrationDir, 14); err != nil {
			t.Fatalf("down to 14: %v", err)
		}
		if tableExists("plugin_private_identities") {
			t.Fatal("plugin_private_identities still exists after 015 down")
		}
		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-up from 14: %v", err)
		}
		if !tableExists("plugin_private_identities") {
			t.Fatal("plugin_private_identities missing after 015 re-up")
		}
		if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 17 {
			t.Fatalf("version after 015 re-up = %d (%v)", v, err)
		}
		assertSeededRows(t)
	})
	t.Run("016 down preserves MANUAL/INSTANCE text and up restores", func(t *testing.T) {
		if err := goose.DownToContext(ctx, db, migrationDir, 15); err != nil {
			t.Fatalf("down to 15: %v", err)
		}
		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-up from 15: %v", err)
		}
		if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 17 {
			t.Fatalf("version after 016 re-up = %d (%v)", v, err)
		}
		var constraintCount int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_constraint c
			JOIN pg_class t ON t.oid=c.conrelid
			JOIN pg_namespace n ON n.oid=t.relnamespace
			WHERE n.nspname=current_schema() AND t.relname='submissions' AND c.conname='submissions_manual_text_check'
		`).Scan(&constraintCount); err != nil || constraintCount != 1 {
			t.Fatalf("submissions_manual_text_check count=%d err=%v", constraintCount, err)
		}
		assertSeededRows(t)
	})
	t.Run("017 down/up restores CLI audit nullability", func(t *testing.T) {
		var nullable string
		if err := db.QueryRowContext(ctx, `
			SELECT is_nullable FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name='audit_events' AND column_name='actor_user_id'
		`).Scan(&nullable); err != nil || nullable != "YES" {
			t.Fatalf("audit_events.actor_user_id nullable=%q err=%v, want YES", nullable, err)
		}
		if err := goose.DownToContext(ctx, db, migrationDir, 16); err != nil {
			t.Fatalf("down to 16: %v", err)
		}
		if err := db.QueryRowContext(ctx, `
			SELECT is_nullable FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name='audit_events' AND column_name='actor_user_id'
		`).Scan(&nullable); err != nil || nullable != "NO" {
			t.Fatalf("audit_events.actor_user_id nullable=%q err=%v after 017 down, want NO", nullable, err)
		}
		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-up from 16: %v", err)
		}
		if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 17 {
			t.Fatalf("version after 017 re-up = %d (%v), want 17", v, err)
		}
		assertSeededRows(t)
	})
	t.Run("012 down preserves P0/P1 and up restores", func(t *testing.T) {
		downUp(t, 11, []string{"deployment_template_allowlist", "instance_network_leases", "repository_bindings", "ad_run_results", "ad_awards"}, []string{"deployment_template_allowlist", "instance_network_leases", "repository_bindings", "ad_run_results", "ad_awards"})
	})
	t.Run("011 down preserves P0/P1 and up restores", func(t *testing.T) {
		downUp(t, 10, []string{"ad_configs", "ad_runs", "git_webhook_deliveries"}, []string{"ad_configs", "ad_runs", "git_webhook_deliveries"})
	})
	t.Run("010 down preserves P0/P1 and up restores", func(t *testing.T) {
		downUp(t, 9, []string{"plugins", "plugin_delegations"}, []string{"plugins", "plugin_delegations"})
	})
	t.Run("009 down preserves P0/P1 and up restores", func(t *testing.T) {
		downUp(t, 8, []string{"integrations", "instances", "pentest_configs"}, []string{"integrations", "instances", "pentest_configs"})
	})
	t.Run("008 down preserves P0/P1 and up restores", func(t *testing.T) {
		downUp(t, 7, []string{"submission_drafts", "file_objects", "events_outbox"}, []string{"submission_drafts", "file_objects", "events_outbox"})
	})
	t.Run("007 down preserves P0/P1 and up restores TEAM subject", func(t *testing.T) {
		if err := goose.DownToContext(ctx, db, migrationDir, 6); err != nil {
			t.Fatalf("down to 6: %v", err)
		}
		for _, table := range []string{"teams", "team_members", "audiences", "audience_members", "module_audiences", "permission_grants", "access_groups"} {
			if tableExists(table) {
				t.Fatalf("%s still exists after 007 down", table)
			}
		}
		for _, col := range []string{"subject_type", "subject_id", "submitted_by_user_id"} {
			if columnExists("submissions", col) {
				t.Fatalf("submissions.%s still exists after 007 down", col)
			}
		}
		var oldIndex bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'submissions_one_accepted_per_user_task')
		`).Scan(&oldIndex); err != nil || !oldIndex {
			t.Fatalf("old accepted index missing after 007 down: %v", err)
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM submissions WHERE idempotency_key = 'remaining-accepted-001'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("P1 submission lost on 007 down: %d %v", count, err)
		}
		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("re-up from 6: %v", err)
		}
		if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 17 {
			t.Fatalf("version after re-up from 6 = %d (%v)", v, err)
		}
		assertSeededRows(t)
	})

	t.Run("rerun is idempotent", func(t *testing.T) {
		before, err := goose.GetDBVersionContext(ctx, db)
		if err != nil {
			t.Fatalf("version before rerun: %v", err)
		}
		if err := goose.UpContext(ctx, db, migrationDir); err != nil {
			t.Fatalf("rerun migrations: %v", err)
		}
		after, err := goose.GetDBVersionContext(ctx, db)
		if err != nil || before != after || after != 17 {
			t.Fatalf("rerun version %d -> %d (%v)", before, after, err)
		}
		assertSeededRows(t)
	})
}
