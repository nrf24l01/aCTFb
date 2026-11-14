package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/loads"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/users"
)

const httpTestManifest = `apiVersion: actfb.dev/v1alpha1
kind: DeploymentTemplate
metadata:
  name: HTTP lab
  slug: http-lab
  description: HTTP integration
nodes:
  - key: web
    name: Web
    kind: CONTAINER
    source:
      type: OCI_IMAGE
      ref: registry.example/ctf/web@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    resources:
      cpuMillicores: 100
      memoryMiB: 128
      diskMiB: 256
    endpoints:
      - key: http
        protocol: HTTP
        targetPort: 8080
        exposure: PRIVATE
checks:
  - kind: tcp
    node: web
    endpoint: http
lifecycle:
  reset: RECREATE
`

func openHTTPTestDB(t *testing.T, ctx context.Context) *postgres.DB {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL deployment HTTP tests")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "actfb_deploy_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.RunMigrations(ctx, sqlDB, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func TestDeploymentTemplateHTTPPostgresCanonical(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := openHTTPTestDB(t, ctx)
	adminID := uuid.New()
	username := "http-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password, roles, email_verified)
		VALUES ($1,$2,$3,'test-hash',ARRAY['admin'],true)
	`, adminID, username, username+"@example.test"); err != nil {
		t.Fatal(err)
	}
	service, err := deployment.NewService(db, authorization.NewAuthorizer(db), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service, fakeAuth{user: &users.User{ID: adminID, Roles: []string{"admin"}}})
	e := echo.New()
	handler.RegisterRoutes(e)

	specPath, err := filepath.Abs(filepath.Join("..", "..", "..", "docs", "swagger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := loads.Spec(specPath)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := doc.Expanded()
	if err != nil {
		t.Fatal(err)
	}
	schemas := expanded.Spec().Definitions
	assertSchema := func(t *testing.T, schemaName string, body []byte) {
		t.Helper()
		schema, ok := schemas[schemaName]
		if !ok {
			t.Fatalf("missing schema %s", schemaName)
		}
		var decoded any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode %s: %v", schemaName, err)
		}
		if err := validate.AgainstSchema(&schema, decoded, strfmt.Default); err != nil {
			t.Fatalf("%s schema validation: %v\n%s", schemaName, err, body)
		}
	}

	requestJSON := func(t *testing.T, method, path, body string, status int, schemaName string) []byte {
		t.Helper()
		request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		request.Header.Set("Authorization", "Bearer token")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		e.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("%s %s status=%d want %d body=%s", method, path, response.Code, status, response.Body.String())
		}
		assertSchema(t, schemaName, response.Body.Bytes())
		return response.Body.Bytes()
	}

	base := "/api/v2/admin/deployment-templates"
	createBody, _ := json.Marshal(map[string]any{
		"manifestYaml": httpTestManifest,
		"files":        []any{},
	})
	created := requestJSON(t, http.MethodPost, base, string(createBody), http.StatusCreated, "DeploymentTemplate")
	var templateModel struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(created, &templateModel); err != nil {
		t.Fatal(err)
	}
	templatePath := base + "/" + templateModel.UUID

	requestJSON(t, http.MethodGet, base, "", http.StatusOK, "DeploymentTemplateListResponse")
	requestJSON(t, http.MethodGet, templatePath, "", http.StatusOK, "DeploymentTemplate")
	requestJSON(t, http.MethodGet, templatePath+"/draft", "", http.StatusOK, "DeploymentTemplateDraft")
	requestJSON(t, http.MethodPut, templatePath+"/draft", string(createBody), http.StatusOK, "DeploymentTemplateDraft")
	requestJSON(t, http.MethodPost, templatePath+"/draft/validate", "", http.StatusOK, "DeploymentTemplateValidation")
	requestJSON(t, http.MethodPost, templatePath+"/draft/preview", "", http.StatusOK, "DeploymentTemplatePreview")
	requestJSON(t, http.MethodPost, templatePath+"/publish", `{"versionNote":"one"}`, http.StatusCreated, "DeploymentTemplateVersion")
	requestJSON(t, http.MethodPost, templatePath+"/publish", `{"versionNote":"two"}`, http.StatusCreated, "DeploymentTemplateVersion")
	requestJSON(t, http.MethodGet, templatePath+"/versions", "", http.StatusOK, "DeploymentTemplateVersionListResponse")
	requestJSON(t, http.MethodGet, templatePath+"/versions/1", "", http.StatusOK, "DeploymentTemplateVersion")
	requestJSON(t, http.MethodGet, templatePath+"/versions/2/diff?from=1", "", http.StatusOK, "DeploymentTemplateDiff")
}

func TestDeploymentTemplateHTTPPostgresForbidden(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := openHTTPTestDB(t, ctx)
	userID := uuid.New()
	username := "http-user-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password, roles, email_verified)
		VALUES ($1,$2,$3,'test-hash',ARRAY['user'],true)
	`, userID, username, username+"@example.test"); err != nil {
		t.Fatal(err)
	}
	service, err := deployment.NewService(db, authorization.NewAuthorizer(db), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service, fakeAuth{user: &users.User{ID: userID, Roles: []string{"user"}}})
	e := echo.New()
	handler.RegisterRoutes(e)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/deployment-templates", nil)
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}
