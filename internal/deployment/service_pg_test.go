package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

func openDeploymentTestDB(t *testing.T, ctx context.Context) *postgres.DB {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL deployment template tests")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "actfb_deployment_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	})
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
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	db := &postgres.DB{Pool: pool, SQL: sqlDB}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		pool.Close()
	})
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.RunMigrations(ctx, sqlDB, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		t.Fatalf("apply migrations 001-011: %v", err)
	}
	return db
}

func createDeploymentUser(t *testing.T, ctx context.Context, db *postgres.DB, roles []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	username := "deploy-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password, roles, email_verified)
		VALUES ($1,$2,$3,'test-hash',$4,true)
	`, id, username, username+"@example.test", roles); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDeploymentTemplateServicePostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := openDeploymentTestDB(t, ctx)
	adminID := createDeploymentUser(t, ctx, db, []string{"admin"})
	playerID := createDeploymentUser(t, ctx, db, []string{"user"})
	authz := authorization.NewAuthorizer(db)
	provider := fullContainerProvider()
	service, err := NewService(db, authz, nil, WithDriverMetadataProvider(provider))
	if err != nil {
		t.Fatal(err)
	}
	admin := authorization.Principal{UserID: adminID}
	player := authorization.Principal{UserID: playerID}

	if _, _, err := service.ListTemplates(ctx, player, 50, 0); err != ErrForbidden {
		t.Fatalf("non-admin list error = %v", err)
	}

	template, err := service.CreateTemplate(ctx, admin, CreateInput{
		ManifestYAML: []byte(sampleManifest()),
		Files:        encodeFiles(sampleFiles()),
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if template.Slug != "web-security-lab" {
		t.Fatalf("slug = %q", template.Slug)
	}

	validation, err := service.ValidateDraft(ctx, admin, template.UUID)
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("validation findings: %#v", validation.Findings)
	}
	preview, previewResult, err := service.PreviewDraft(ctx, admin, template.UUID)
	if err != nil {
		t.Fatalf("PreviewDraft: %v", err)
	}
	if previewResult == nil || !previewResult.Valid || preview == nil {
		t.Fatalf("preview result=%#v preview=%#v", previewResult, preview)
	}
	if provider.applyCalls != 0 {
		t.Fatalf("preview applied infrastructure: %d", provider.applyCalls)
	}

	first, err := service.Publish(ctx, admin, template.UUID)
	if err != nil {
		t.Fatalf("Publish v1: %v", err)
	}
	if first.Version != 1 || !strings.HasPrefix(first.Digest, "sha256:") {
		t.Fatalf("first version = %#v", first)
	}

	files := sampleFiles()
	files["files/app-config.yml.tmpl"] = []byte("difficulty: {{.difficulty}}\nchanged: true\n")
	if _, err := service.SaveDraft(ctx, admin, template.UUID, []byte(sampleManifest()), encodeFiles(files)); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	second, err := service.Publish(ctx, admin, template.UUID)
	if err != nil {
		t.Fatalf("Publish v2: %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("second version = %d", second.Version)
	}
	old, err := service.GetVersion(ctx, admin, template.UUID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(old.Files["files/app-config.yml.tmpl"]) != string(sampleFiles()["files/app-config.yml.tmpl"]) {
		t.Fatal("published v1 files changed after v2 publish")
	}
	diff, err := service.DiffVersions(ctx, admin, template.UUID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) == 0 {
		t.Fatal("expected a file diff")
	}
	encodedDiff, _ := json.Marshal(diff)
	if strings.Contains(string(encodedDiff), "changed: true") || strings.Contains(string(encodedDiff), "difficulty:") {
		t.Fatalf("diff leaked file content: %s", encodedDiff)
	}

	metadata, err := service.PublishedMetadata(ctx, admin, template.UUID, 1, TemplateScope{})
	if err != nil {
		t.Fatal(err)
	}
	encodedMetadata, _ := json.Marshal(metadata)
	if strings.Contains(string(encodedMetadata), "app-config") || strings.Contains(string(encodedMetadata), "difficulty:") {
		t.Fatalf("published metadata leaked files: %s", encodedMetadata)
	}
	for _, parameter := range metadata.Parameters {
		if parameter.Key == "db_password" && parameter.Default != nil {
			t.Fatal("secret parameter default leaked")
		}
	}

	var storedManifest string
	if err := db.Pool.QueryRow(ctx, `
		SELECT manifest::text FROM deployment_template_versions
		WHERE template_id = $1 AND version = 1
	`, template.UUID).Scan(&storedManifest); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedManifest, "s3cret") || strings.Contains(storedManifest, "BEGIN PRIVATE KEY") {
		t.Fatalf("published manifest contains a secret: %s", storedManifest)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE deployment_template_versions SET digest = 'tampered' WHERE template_id = $1 AND version = 1
	`, template.UUID); err == nil {
		t.Fatal("012 immutability trigger did not reject a published version update")
	}

	secondTemplate, err := service.CreateTemplate(ctx, admin, CreateInput{
		ManifestYAML: []byte(strings.Replace(sampleManifest(), "slug: web-security-lab", "slug: web-security-lab-2", 1)),
		Files:        encodeFiles(sampleFiles()),
	})
	if err != nil {
		t.Fatal(err)
	}
	var waitGroup sync.WaitGroup
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, publishErr := service.Publish(ctx, admin, secondTemplate.UUID)
			results <- publishErr
		}()
	}
	waitGroup.Wait()
	close(results)
	for publishErr := range results {
		if publishErr != nil {
			t.Fatalf("concurrent publish: %v", publishErr)
		}
	}
	versions, total, err := service.ListVersions(ctx, admin, secondTemplate.UUID, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(versions) != 2 {
		t.Fatalf("concurrent versions total=%d items=%d", total, len(versions))
	}
	if versions[0].Version == versions[1].Version {
		t.Fatalf("duplicate concurrent version allocation: %#v", versions)
	}

	oversized := strings.Repeat("a", MaxManifestBytes+1)
	if _, err := service.SaveDraft(ctx, admin, template.UUID, []byte(oversized), nil); err == nil {
		t.Fatal("expected oversized manifest rejection")
	}
	if _, err := service.Publish(ctx, admin, uuid.New()); err != ErrNotFound {
		t.Fatalf("missing template publish error = %v", err)
	}
}

func TestDeploymentAuthorizationAndValidationPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := openDeploymentTestDB(t, ctx)
	adminID := createDeploymentUser(t, ctx, db, []string{"admin"})
	service, err := NewService(db, authorization.NewAuthorizer(db), nil, WithDriverMetadataProvider(fullContainerProvider()))
	if err != nil {
		t.Fatal(err)
	}
	admin := authorization.Principal{UserID: adminID}
	template, err := service.CreateTemplate(ctx, admin, CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetTemplate(ctx, authorization.Principal{}, template.UUID); err != ErrUnauthorized {
		t.Fatalf("unauthenticated error = %v", err)
	}
	result, err := service.ValidateDraft(ctx, admin, template.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatal("bodyless skeleton draft must not validate")
	}
	requireFindingCode(t, result.Findings, "NODES_REQUIRED")
	if _, err := service.Publish(ctx, admin, template.UUID); err == nil {
		t.Fatal("invalid draft must not publish")
	}
	// A bodyless create uses a generated skeleton slug; replacing the draft must
	// be able to adopt the manifest slug before publication.
	if _, err := service.SaveDraft(ctx, admin, template.UUID, []byte(sampleManifest()), encodeFiles(sampleFiles())); err != nil {
		t.Fatalf("SaveDraft after skeleton: %v", err)
	}
	updated, err := service.GetTemplate(ctx, admin, template.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Slug != "web-security-lab" {
		t.Fatalf("draft slug update = %q", updated.Slug)
	}
	validation, err := service.ValidateDraft(ctx, admin, template.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if !validation.Valid {
		t.Fatalf("draft after slug adoption invalid: %#v", validation.Findings)
	}
}
