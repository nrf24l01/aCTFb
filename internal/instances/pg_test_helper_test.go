package instances

import (
	"context"
	"database/sql"
	"fmt"
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

// testDB provisions an isolated schema with migrations 001..012 applied.
func testDB(t *testing.T) (*postgres.DB, func()) {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL instance tests")
	}
	schema := "it_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	migrationsDir := findMigrations(t)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	schemaDSN := dsnWithSearchPath(t, dsn, schema)
	sqlDB, err := sql.Open("pgx", schemaDSN)
	if err != nil {
		t.Fatalf("open schema connection: %v", err)
	}
	if err := goose.Up(sqlDB, migrationsDir); err != nil {
		sqlDB.Close()
		admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
		t.Fatalf("apply migrations: %v", err)
	}
	poolConfig, err := pgxpool.ParseConfig(schemaDSN)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	db := &postgres.DB{Pool: pool, SQL: stdlib.OpenDBFromPool(pool)}
	cleanup := func() {
		pool.Close()
		admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	}
	return db, cleanup
}

func dsnWithSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	if !strings.Contains(dsn, "?") {
		return dsn + "?search_path=" + schema
	}
	return dsn + "&search_path=" + schema
}

func findMigrations(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
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

type seedIDs struct {
	UserID        uuid.UUID
	BoardID       uuid.UUID
	ModuleID      uuid.UUID
	TaskID        uuid.UUID
	IntegrationID uuid.UUID
	TemplateID    uuid.UUID
	VersionID     uuid.UUID
	BindingID     uuid.UUID
}

func seedInstanceFixture(t *testing.T, db *postgres.DB) seedIDs {
	t.Helper()
	ctx := context.Background()
	ids := seedIDs{
		UserID: uuid.New(), BoardID: uuid.New(), ModuleID: uuid.New(), TaskID: uuid.New(),
		IntegrationID: uuid.New(), TemplateID: uuid.New(), VersionID: uuid.New(), BindingID: uuid.New(),
	}
	now := time.Now().UTC()
	exec := func(query string, args ...any) {
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	exec(`INSERT INTO users (id, username, email, password, roles) VALUES ($1,$2,$3,'x',ARRAY['admin']::text[])`,
		ids.UserID, "admin-"+ids.UserID.String()[:8], ids.UserID.String()[:8]+"@example.test")
	exec(`INSERT INTO boards (id, slug, name, participation_mode, registration_mode, visibility, status, starts_at, ends_at, created_by)
	      VALUES ($1,$2,'Board','INDIVIDUAL','OPEN','PUBLIC','ACTIVE',$3,$4,$5)`,
		ids.BoardID, "b-"+ids.BoardID.String()[:8], now.Add(-time.Hour), now.Add(24*time.Hour), ids.UserID)
	exec(`INSERT INTO modules (id, board_id, name, type, allowed_task_types, created_by)
	      VALUES ($1,$2,'Tasks','TASKS',ARRAY['FLAG']::text[],$3)`,
		ids.ModuleID, ids.BoardID, ids.UserID)
	exec(`INSERT INTO tasks (id, board_id, module_id, title, category, type, points, submission_mode, submission_checker, instance_enabled, created_by)
	      VALUES ($1,$2,$3,'Task','cat','FLAG',100,'TEXT','AUTOMATIC',TRUE,$4)`,
		ids.TaskID, ids.BoardID, ids.ModuleID, ids.UserID)
	exec(`INSERT INTO integrations (id, category, type, kind, driver, name, base_url, credentials_ciphertext, credentials_key_version, settings, publication_policy, status, created_by)
	      VALUES ($1,'COMPUTE','DOCKER','CONTAINER','docker',$2,'',NULL,NULL,$3,$4,'UNKNOWN',$5)`,
		ids.IntegrationID, "docker-"+ids.IntegrationID.String()[:8],
		[]byte(`{"network":{"kind":"network"}}`),
		[]byte(fmt.Sprintf(`{"publicTcpHost":"127.0.0.1","publicPortFrom":41000,"publicPortTo":41020}`)),
		ids.UserID)
	exec(`INSERT INTO deployment_templates (id, slug, name, created_by) VALUES ($1,$2,'tmpl',$3)`,
		ids.TemplateID, "tmpl-"+ids.TemplateID.String()[:8], ids.UserID)
	manifest := `{"apiVersion":"actfb.dev/v1alpha1","kind":"DeploymentTemplate","metadata":{"name":"t","slug":"t","description":""},"parameters":[{"key":"flag","type":"string","label":"Flag","required":true,"secret":true}],"generated":[{"key":"password","kind":"password","length":24,"rotateOnReset":true}],"nodes":[{"key":"web","name":"web","kind":"CONTAINER","source":{"type":"OCI_IMAGE","ref":"nginx@sha256:713fc2ebfd92e8217db451477182ee91944b123d2b1ee9fd5a8c6e91ca6c3a11"},"resources":{"cpuMillicores":200,"memoryMiB":64,"diskMiB":64},"endpoints":[{"key":"web","protocol":"http","targetPort":8080,"exposure":"PUBLIC"}],"driverOptions":{"docker":{"tmpfs":{"/tmp":8}}}}]}`
	exec(`INSERT INTO deployment_template_versions (id, template_id, version, digest, manifest, files, published_by)
	      VALUES ($1,$2,1,'digest-1',$3,'[]',$4)`,
		ids.VersionID, ids.TemplateID, []byte(manifest), ids.UserID)
	exec(`INSERT INTO deployment_bindings (id, board_id, module_id, task_id, template_version_id, parameters, lifecycle, created_by)
	      VALUES ($1,$2,$3,$4,$5,'{"flag":"public"}','{}',$6)`,
		ids.BindingID, ids.BoardID, ids.ModuleID, ids.TaskID, ids.VersionID, ids.UserID)
	exec(`UPDATE tasks SET deployment_binding_id = $2 WHERE id = $1`, ids.TaskID, ids.BindingID)
	exec(`INSERT INTO deployment_binding_targets (binding_id, kind, integration_id) VALUES ($1,'CONTAINER',$2)`,
		ids.BindingID, ids.IntegrationID)
	var allowlistID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `INSERT INTO deployment_template_allowlist (template_version_id, board_id, module_id, approved_by)
	      VALUES ($1,$2,$3,$4) RETURNING id`, ids.VersionID, ids.BoardID, ids.ModuleID, ids.UserID).Scan(&allowlistID); err != nil {
		t.Fatalf("seed allowlist: %v", err)
	}
	exec(`INSERT INTO deployment_template_allowlist_targets (allowlist_id, kind, integration_id) VALUES ($1,'CONTAINER',$2)`,
		allowlistID, ids.IntegrationID)
	return ids
}

type pentestSeed struct {
	ModuleID  uuid.UUID
	BindingID uuid.UUID
}

func seedPentestModule(t *testing.T, db *postgres.DB, ids seedIDs, resetEnabled bool, maxResets int) pentestSeed {
	t.Helper()
	ctx := context.Background()
	seed := pentestSeed{ModuleID: uuid.New(), BindingID: uuid.New()}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, created_by)
		VALUES ($1,$2,'Pentest','PENTEST',ARRAY['PENTEST_REPORT']::text[],$3)
	`, seed.ModuleID, ids.BoardID, ids.UserID); err != nil {
		t.Fatal(err)
	}
	var allowlistID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO deployment_template_allowlist (template_version_id, board_id, module_id, approved_by)
		VALUES ($1,$2,$3,$4) RETURNING id
	`, ids.VersionID, ids.BoardID, seed.ModuleID, ids.UserID).Scan(&allowlistID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO deployment_template_allowlist_targets (allowlist_id, kind, integration_id)
		VALUES ($1,'CONTAINER',$2)
	`, allowlistID, ids.IntegrationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO deployment_bindings (id, board_id, module_id, template_version_id, parameters, lifecycle, created_by)
		VALUES ($1,$2,$3,$4,'{"flag":"public"}','{}',$5)
	`, seed.BindingID, ids.BoardID, seed.ModuleID, ids.VersionID, ids.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO deployment_binding_targets (binding_id, kind, integration_id)
		VALUES ($1,'CONTAINER',$2)
	`, seed.BindingID, ids.IntegrationID); err != nil {
		t.Fatal(err)
	}
	policy := []byte(`{"mode":"TEXT_AND_FILES","checker":"MANUAL","text":{"minLength":1,"maxLength":1000},"files":{"required":true,"maxCount":3,"maxSizeBytes":1048576}}`)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO pentest_configs (module_id, deployment_binding_id, reset_enabled, max_resets, submission_policy, created_by)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, seed.ModuleID, seed.BindingID, resetEnabled, maxResets, policy, ids.UserID); err != nil {
		t.Fatal(err)
	}
	return seed
}

func seedUser(t *testing.T, db *postgres.DB, roles []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO users (id, username, email, password, roles) VALUES ($1,$2,$3,'x',$4)
	`, id, "u-"+id.String()[:8], id.String()[:8]+"@example.test", roles); err != nil {
		t.Fatal(err)
	}
	return id
}
