package attackdefense

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

const testRunnerImage = "registry.example/ad-checker@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type fixture struct {
	db              *postgres.DB
	service         *Service
	sealer          secrets.Sealer
	authz           authorization.Authorizer
	admin           authorization.Principal
	player          authorization.Principal
	boardID         uuid.UUID
	moduleID        uuid.UUID
	sourceBinding   uuid.UUID
	exploitBinding  uuid.UUID
	integrationID   uuid.UUID
	assignmentID    uuid.UUID
	vulnerabilityID uuid.UUID
	webhookSecret   string
}

func openADTestDB(t *testing.T) (*postgres.DB, func()) {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL attack-defense tests")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	schema := "actfb_ad_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	cleanupSchema := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	poolConfig, err := pgxpool.ParseConfig(parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	db := &postgres.DB{Pool: pool, SQL: sqlDB}
	cleanupDB := func() {
		_ = sqlDB.Close()
		pool.Close()
		cleanupSchema()
	}
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.RunMigrations(context.Background(), sqlDB, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		cleanupDB()
		t.Fatalf("apply migrations: %v", err)
	}
	return db, cleanupDB
}

func newADFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, cleanup := openADTestDB(t)
	t.Cleanup(cleanup)

	playerID := createADUser(t, ctx, db, []string{"user"})
	adminID := createADUser(t, ctx, db, []string{"admin"})
	boardID := uuid.New()
	moduleID := uuid.New()
	now := time.Now().UTC()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO boards (id, slug, name, description, participation_mode, registration_mode,
			visibility, status, starts_at, ends_at, created_by)
		VALUES ($1,$2,'AD','', 'INDIVIDUAL','OPEN','PUBLIC','ACTIVE',$3,$4,$5)
	`, boardID, "ad-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:12], now.Add(-time.Hour), now.Add(time.Hour), adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, enabled, created_by)
		VALUES ($1,$2,'Attack+Defense','ATTACK_DEFENSE',ARRAY['EXPLOIT','PATCH'],TRUE,$3)
	`, moduleID, boardID, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id,user_id) VALUES ($1,$2)`, boardID, playerID); err != nil {
		t.Fatal(err)
	}
	sealer, err := secrets.NewCipher(base64Key(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	integrationID := uuid.New()
	credentials, err := sealer.Seal([]byte(`{"token":"git-token"}`), CredentialAAD(integrationID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO integrations (id, category, type, name, base_url, credentials_ciphertext,
			credentials_key_version, status, created_by)
		VALUES ($1,'GIT','GITEA',$2,'http://127.0.0.1:3300',$3,1,'CONNECTED',$4)
	`, integrationID, "gitea-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:8], credentials, adminID); err != nil {
		t.Fatal(err)
	}
	webhookSecret := "webhook-secret-" + uuid.NewString()
	sourceBinding := insertBinding(t, ctx, db, sealer, integrationID, "team/source", "main", webhookSecret, adminID)
	exploitBinding := insertBinding(t, ctx, db, sealer, integrationID, "team/exploit", "exploit", webhookSecret, adminID)
	assignmentID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO ad_repository_assignments
			(id, module_id, subject_type, subject_id, source_integration_id, source_repository, source_branch,
			 exploit_integration_id, exploit_repository, exploit_branch, source_binding_id, exploit_binding_id, created_by)
		VALUES ($1,$2,'USER',$3,$4,'team/source','main',$4,'team/exploit','exploit',$5,$6,$7)
	`, assignmentID, moduleID, playerID, integrationID, sourceBinding, exploitBinding, adminID); err != nil {
		t.Fatal(err)
	}
	vulnerabilityID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO ad_vulnerabilities (id, module_id, key, title, description, exploit_points,
			patch_points, exploit_test_selector, patch_test_selector)
		VALUES ($1,$2,'sqli','SQL injection','',100,150,'TestSQLiExploit','TestSQLiPatch')
	`, vulnerabilityID, moduleID); err != nil {
		t.Fatal(err)
	}

	authz := authorization.NewAuthorizer(db)
	service, err := NewService(db, authz, sealer,
		WithRunnerImageAllowlist([]string{testRunnerImage}),
		WithGitProviderFactory(&GitProviderFactory{AllowInsecureLoopback: true}),
		WithAllowInsecureWebhookTransport(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	if _, err := service.AdminPutConfig(ctx, admin, boardID, moduleID, Config{
		DefaultSourceBranch: "main", DefaultExploitBranch: "exploit",
		RunnerImage: testRunnerImage, RunTimeoutSeconds: 120,
		MaxConcurrentRunsPerSubject: 1, ManualRerunEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	return &fixture{
		db: db, service: service, sealer: sealer, authz: authz, admin: admin,
		player:  authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}},
		boardID: boardID, moduleID: moduleID, sourceBinding: sourceBinding,
		exploitBinding: exploitBinding, integrationID: integrationID,
		assignmentID: assignmentID, vulnerabilityID: vulnerabilityID, webhookSecret: webhookSecret,
	}
}

func base64Key(t *testing.T) string {
	t.Helper()
	// 32 zero bytes are a valid key for local tests only; never a production key.
	return base64.StdEncoding.EncodeToString(make([]byte, 32))
}

func createADUser(t *testing.T, ctx context.Context, db *postgres.DB, roles []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	username := "ad-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password, roles, email_verified)
		VALUES ($1,$2,$3,'test-hash',$4,true)
	`, id, username, username+"@example.test", roles); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertBinding(t *testing.T, ctx context.Context, db *postgres.DB, sealer secrets.Sealer, integrationID uuid.UUID, repository, branch, secret string, actor uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ciphertext, err := sealer.Seal([]byte(secret), RepositoryWebhookSecretAAD(id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO repository_bindings (id, integration_id, repository_ref, branch,
			webhook_secret_ciphertext, webhook_secret_key_version, created_by)
		VALUES ($1,$2,$3,$4,$5,1,$6)
	`, id, integrationID, repository, branch, ciphertext, actor); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) pushBody(repository, branch, sha string) []byte {
	return []byte(`{"ref":"refs/heads/` + branch + `","after":"` + sha + `","repository":{"full_name":"` + repository + `"}}`)
}

func giteaHeaders(secret, delivery, body string) map[string][]string {
	return map[string][]string{
		"X-Gitea-Event":     {"push"},
		"X-Gitea-Delivery":  {delivery},
		"X-Gitea-Signature": {sign(secret, []byte(body))},
		"Content-Type":      {"application/json"},
	}
}
