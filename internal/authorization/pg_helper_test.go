package authorization

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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// newTestDB creates a uniquely named isolated schema, applies migrations
// 001-006 with Goose, and returns a live pgx pool. Tests never touch the public
// schema. Set ACTFB_TEST_POSTGRES_DSN to opt in.
func newTestDB(t *testing.T) *postgres.DB {
	t.Helper()

	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL authorization tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}

	schema := "actfb_authz_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(schema, "actfb_authz_test_") || len(schema) > 63 {
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
	poolCfg, err := pgxpool.ParseConfig(scopedDSN)
	if err != nil {
		t.Fatalf("parse scoped DSN: %v", err)
	}
	// Concurrency/barrier tests need independent sessions for the lock holder
	// and the role mutation waiting on the board advisory lock.
	poolCfg.MaxConns = 4
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "actfb_authz_test"
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("open isolated pool: %v", err)
	}
	sqlDB := stdlibOpenDBFromPool(pool)
	t.Cleanup(func() {
		_ = sqlDB.Close()
		pool.Close()
	})

	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(migrationDir, "006_p1_core.sql")); err != nil {
		t.Fatalf("migration 006_p1_core.sql is missing: %v", err)
	}
	if err := goose.UpContext(ctx, sqlDB, migrationDir); err != nil {
		t.Fatalf("apply migrations 001-006: %v", err)
	}

	return &postgres.DB{Pool: pool, SQL: sqlDB}
}

func stdlibOpenDBFromPool(pool *pgxpool.Pool) *sql.DB {
	db := stdlib.OpenDBFromPool(pool)
	db.SetMaxOpenConns(1)
	return db
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

type fixture struct {
	Admin        uuid.UUID
	OwnerA       uuid.UUID
	OwnerB       uuid.UUID
	EditorA      uuid.UUID
	ModuleEditor uuid.UUID
	ReviewerX    uuid.UUID
	ReviewerAll  uuid.UUID
	Participant  uuid.UUID
	Outsider     uuid.UUID
	Disabled     uuid.UUID

	BoardA  uuid.UUID
	BoardB  uuid.UUID
	ModuleX uuid.UUID
	ModuleY uuid.UUID
	ModuleZ uuid.UUID
}

func seedFixture(t *testing.T, db *postgres.DB) fixture {
	t.Helper()
	ctx := context.Background()

	var f fixture
	f.Admin = insertUser(t, db, "admin", true, false)
	f.OwnerA = insertUser(t, db, "owner-a", false, false)
	f.OwnerB = insertUser(t, db, "owner-b", false, false)
	f.EditorA = insertUser(t, db, "editor-a", false, false)
	f.ModuleEditor = insertUser(t, db, "module-editor", false, false)
	f.ReviewerX = insertUser(t, db, "reviewer-x", false, false)
	f.ReviewerAll = insertUser(t, db, "reviewer-all", false, false)
	f.Participant = insertUser(t, db, "participant", false, false)
	f.Outsider = insertUser(t, db, "outsider", false, false)
	f.Disabled = insertUser(t, db, "disabled", false, true)

	f.BoardA = insertBoard(t, db, "board-a", f.Admin)
	f.BoardB = insertBoard(t, db, "board-b", f.Admin)
	f.ModuleX = insertModule(t, db, f.BoardA, "module-x", f.Admin)
	f.ModuleY = insertModule(t, db, f.BoardA, "module-y", f.Admin)
	f.ModuleZ = insertModule(t, db, f.BoardB, "module-z", f.Admin)

	insertBinding(t, db, f.BoardA, nil, f.OwnerA, RoleBoardOwner, f.Admin, nil)
	insertBinding(t, db, f.BoardB, nil, f.OwnerB, RoleBoardOwner, f.Admin, nil)
	insertBinding(t, db, f.BoardA, nil, f.EditorA, RoleBoardEditor, f.Admin, nil)
	insertBinding(t, db, f.BoardA, &f.ModuleX, f.ModuleEditor, RoleBoardEditor, f.Admin, nil)
	insertBinding(t, db, f.BoardA, &f.ModuleX, f.ReviewerX, RoleReviewer, f.Admin, nil)
	insertBinding(t, db, f.BoardA, nil, f.ReviewerAll, RoleReviewer, f.Admin, nil)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_participants (board_id, user_id) VALUES ($1, $2)
	`, f.BoardA, f.Participant); err != nil {
		t.Fatalf("insert participant: %v", err)
	}

	return f
}

func insertUser(t *testing.T, db *postgres.DB, username string, admin, disabled bool) uuid.UUID {
	t.Helper()
	roles := `ARRAY['user']::text[]`
	if admin {
		roles = `ARRAY['admin','user']::text[]`
	}
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO users (username, email, password, roles, disabled)
		VALUES ($1, $2, 'argon2id-test', `+roles+`, $3)
		RETURNING id
	`, username, username+"@example.test", disabled).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	return id
}

func insertBoard(t *testing.T, db *postgres.DB, slug string, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility, created_by)
		VALUES ($1, $2, 'INDIVIDUAL', 'OPEN', 'PRIVATE', $3)
		RETURNING id
	`, slug, slug, createdBy).Scan(&id); err != nil {
		t.Fatalf("insert board %s: %v", slug, err)
	}
	return id
}

func insertModule(t *testing.T, db *postgres.DB, boardID uuid.UUID, name string, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO modules (board_id, name, type, allowed_task_types, created_by)
		VALUES ($1, $2, 'TASKS', ARRAY['FLAG'], $3)
		RETURNING id
	`, boardID, name, createdBy).Scan(&id); err != nil {
		t.Fatalf("insert module %s: %v", name, err)
	}
	return id
}

func insertBinding(
	t *testing.T,
	db *postgres.DB,
	boardID uuid.UUID,
	moduleID *uuid.UUID,
	userID uuid.UUID,
	role Role,
	grantedBy uuid.UUID,
	expiresAt *time.Time,
) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, boardID, moduleID, userID, string(role), grantedBy, expiresAt).Scan(&id); err != nil {
		t.Fatalf("insert binding: %v", err)
	}
	return id
}
