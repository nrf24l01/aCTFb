package access

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

func newTestDB(t *testing.T) *postgres.DB {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL access tests")
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
	schema := "actfb_access_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if len(schema) > 63 {
		admin.Close()
		t.Fatalf("test schema name too long: %q", schema)
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
	poolCfg.MaxConns = 4
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "actfb_access_test"
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("open isolated pool: %v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
		pool.Close()
	})

	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(migrationDir, "011_attack_defense.sql")); err != nil {
		t.Fatalf("migration 011 is missing: %v", err)
	}
	if err := goose.UpContext(ctx, sqlDB, migrationDir); err != nil {
		t.Fatalf("apply migrations 001-011: %v", err)
	}
	return &postgres.DB{Pool: pool, SQL: sqlDB}
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

type accessFixture struct {
	Admin    uuid.UUID
	User1    uuid.UUID
	User2    uuid.UUID
	User3    uuid.UUID
	Disabled uuid.UUID

	BoardPrivate   uuid.UUID
	BoardPublic    uuid.UUID
	BoardClosed    uuid.UUID
	BoardTeam      uuid.UUID
	ModulePrivate  uuid.UUID
	ModuleAudience uuid.UUID
	ModuleOther    uuid.UUID
	ModuleTeam     uuid.UUID
	Group1         uuid.UUID
	Group2         uuid.UUID
	Audience1      uuid.UUID
	Audience2      uuid.UUID
	AudienceTeam   uuid.UUID
	TeamA          uuid.UUID
}

func seedAccessFixture(t *testing.T, db *postgres.DB) accessFixture {
	t.Helper()
	ctx := context.Background()
	var f accessFixture
	f.Admin = insertAccessUser(t, db, "admin", true, false)
	f.User1 = insertAccessUser(t, db, "user-one", false, false)
	f.User2 = insertAccessUser(t, db, "user-two", false, false)
	f.User3 = insertAccessUser(t, db, "user-three", false, false)
	f.Disabled = insertAccessUser(t, db, "disabled", false, true)

	f.Group1 = insertGroup(t, db, "group-one")
	f.Group2 = insertGroup(t, db, "group-two")
	if _, err := db.Pool.Exec(ctx, `INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2),($3,$4)`, f.Group1, f.User1, f.Group2, f.User2); err != nil {
		t.Fatalf("insert group members: %v", err)
	}

	f.BoardPrivate = insertBoard(t, db, "private-board", "PRIVATE", "OPEN", "INDIVIDUAL", f.Admin)
	f.BoardPublic = insertBoard(t, db, "public-board", "PUBLIC", "OPEN", "INDIVIDUAL", f.Admin)
	f.BoardClosed = insertBoard(t, db, "closed-board", "PRIVATE", "CLOSED", "INDIVIDUAL", f.Admin)
	f.BoardTeam = insertTeamBoard(t, db, "team-board", f.Admin)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO board_access_groups (board_id, group_id) VALUES ($1,$2),($1,$3)`, f.BoardPrivate, f.Group1, f.Group2); err != nil {
		t.Fatalf("insert board access groups: %v", err)
	}
	for _, boardID := range []uuid.UUID{f.BoardPrivate, f.BoardPublic, f.BoardClosed, f.BoardTeam} {
		for _, userID := range []uuid.UUID{f.User1, f.User2, f.User3} {
			if _, err := db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2)`, boardID, userID); err != nil {
				t.Fatalf("insert participant: %v", err)
			}
		}
	}
	// Participant with a live scoped role still cannot bypass a revoked access
	// group for participant reads.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO board_role_bindings (board_id, user_id, role, granted_by) VALUES ($1,$2,'board_owner',$3)`, f.BoardPrivate, f.User1, f.Admin); err != nil {
		t.Fatalf("insert board owner: %v", err)
	}

	f.ModulePrivate = insertModule(t, db, f.BoardPrivate, "private-module", f.Admin)
	f.ModuleAudience = insertModule(t, db, f.BoardPrivate, "audience-module", f.Admin)
	f.ModuleOther = insertModule(t, db, f.BoardPublic, "other-module", f.Admin)
	f.ModuleTeam = insertModule(t, db, f.BoardTeam, "team-module", f.Admin)

	f.Audience1 = insertAudience(t, db, f.BoardPrivate, "aud-one")
	f.Audience2 = insertAudience(t, db, f.BoardPrivate, "aud-two")
	if _, err := db.Pool.Exec(ctx, `INSERT INTO audience_members (audience_id, subject_type, subject_id) VALUES ($1,'USER',$2),($3,'USER',$4)`, f.Audience1, f.User1, f.Audience2, f.User2); err != nil {
		t.Fatalf("insert audience members: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO module_audiences (board_id, module_id, audience_id) VALUES ($1,$2,$3),($1,$2,$4)`, f.BoardPrivate, f.ModuleAudience, f.Audience1, f.Audience2); err != nil {
		t.Fatalf("insert module audiences: %v", err)
	}

	f.TeamA = insertTeam(t, db, f.BoardTeam, "team-a", f.User1)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO team_members (board_id, team_id, user_id) VALUES ($1,$2,$3),($1,$2,$4)`, f.BoardTeam, f.TeamA, f.User1, f.User2); err != nil {
		t.Fatalf("insert team members: %v", err)
	}
	f.AudienceTeam = insertAudience(t, db, f.BoardTeam, "team-audience")
	if _, err := db.Pool.Exec(ctx, `INSERT INTO audience_members (audience_id, subject_type, subject_id) VALUES ($1,'TEAM',$2)`, f.AudienceTeam, f.TeamA); err != nil {
		t.Fatalf("insert team audience member: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO module_audiences (board_id, module_id, audience_id) VALUES ($1,$2,$3)`, f.BoardTeam, f.ModuleTeam, f.AudienceTeam); err != nil {
		t.Fatalf("insert team module audience: %v", err)
	}
	return f
}

func insertAccessUser(t *testing.T, db *postgres.DB, username string, admin, disabled bool) uuid.UUID {
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

func insertGroup(t *testing.T, db *postgres.DB, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `INSERT INTO access_groups (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert group %s: %v", name, err)
	}
	return id
}

func insertBoard(t *testing.T, db *postgres.DB, slug, visibility, registration, participation string, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility, created_by)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id
	`, slug, slug, participation, registration, visibility, createdBy).Scan(&id); err != nil {
		t.Fatalf("insert board %s: %v", slug, err)
	}
	return id
}

func insertTeamBoard(t *testing.T, db *postgres.DB, slug string, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility,
		                    team_min_members, team_max_members, team_allow_self_create, created_by)
		VALUES ($1,$2,'TEAM','OPEN','PRIVATE',1,10,TRUE,$3)
		RETURNING id
	`, slug, slug, createdBy).Scan(&id); err != nil {
		t.Fatalf("insert team board %s: %v", slug, err)
	}
	return id
}

func insertModule(t *testing.T, db *postgres.DB, boardID uuid.UUID, name string, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO modules (board_id, name, type, allowed_task_types, created_by)
		VALUES ($1,$2,'TASKS',ARRAY['FLAG'],$3)
		RETURNING id
	`, boardID, name, createdBy).Scan(&id); err != nil {
		t.Fatalf("insert module %s: %v", name, err)
	}
	return id
}

func insertAudience(t *testing.T, db *postgres.DB, boardID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `INSERT INTO audiences (board_id, name) VALUES ($1,$2) RETURNING id`, boardID, name).Scan(&id); err != nil {
		t.Fatalf("insert audience %s: %v", name, err)
	}
	return id
}

func insertTeam(t *testing.T, db *postgres.DB, boardID uuid.UUID, name string, captain uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO teams (board_id, name, captain_user_id) VALUES ($1,$2,$3) RETURNING id
	`, boardID, name, captain).Scan(&id); err != nil {
		t.Fatalf("insert team %s: %v", name, err)
	}
	return id
}
