package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/labstack/echo/v5"
	"github.com/pressly/goose/v3"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/files"
	"github.com/nrf24l01/actfb/internal/instances"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/users"
)

type httpTestFixture struct {
	db            *postgres.DB
	service       *instances.Service
	userID        uuid.UUID
	boardID       uuid.UUID
	moduleID      uuid.UUID
	templateID    uuid.UUID
	versionID     uuid.UUID
	integrationID uuid.UUID
}

type noopPublisher struct{}

func (noopPublisher) PublishJob(context.Context, instances.Job) error { return nil }

type readyVerifier struct{ refs []files.Ref }

func (v readyVerifier) VerifyDraft(context.Context, files.VerifyDraftInput) (*files.VerifiedDraft, error) {
	return &files.VerifiedDraft{Ready: v.refs}, nil
}

type testAuth struct{ userID uuid.UUID }

func (a testAuth) AuthenticateAccessToken(context.Context, string) (*users.User, error) {
	return &users.User{ID: a.userID}, nil
}

func newHTTPTestFixture(t *testing.T, fileRefs []files.Ref) *httpTestFixture {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL HTTP tests")
	}
	schema := "ih_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schemaDSN := dsn
	if strings.Contains(dsn, "?") {
		schemaDSN += "&search_path=" + schema
	} else {
		schemaDSN += "?search_path=" + schema
	}
	sqlDB, err := sql.Open("pgx", schemaDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(sqlDB, findHTTPMigrations(t)); err != nil {
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
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	})

	fixture := &httpTestFixture{db: db, userID: uuid.New(), boardID: uuid.New(), moduleID: uuid.New(),
		templateID: uuid.New(), versionID: uuid.New(), integrationID: uuid.New()}
	taskID := uuid.New()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO users (id, username, email, password, roles) VALUES ($1,$2,$3,'x',ARRAY['admin']::text[])`,
		fixture.userID, "admin-"+fixture.userID.String()[:8], fixture.userID.String()[:8]+"@test.local")
	exec(`INSERT INTO boards (id, slug, name, participation_mode, registration_mode, visibility, status, starts_at, ends_at, created_by)
	      VALUES ($1,$2,'B','INDIVIDUAL','OPEN','PUBLIC','ACTIVE',NOW()-INTERVAL '1 hour',NOW()+INTERVAL '1 day',$3)`,
		fixture.boardID, "b-"+fixture.boardID.String()[:8], fixture.userID)
	// Participant-own pentest actions require a live participant row.
	exec(`INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2)`, fixture.boardID, fixture.userID)
	exec(`INSERT INTO modules (id, board_id, name, type, allowed_task_types, created_by)
	      VALUES ($1,$2,'Pentest','PENTEST',ARRAY['PENTEST_REPORT']::text[],$3)`,
		fixture.moduleID, fixture.boardID, fixture.userID)
	exec(`INSERT INTO integrations (id, category, type, kind, driver, name, base_url, settings, publication_policy, status, created_by)
	      VALUES ($1,'COMPUTE','DOCKER','CONTAINER','docker',$2,'', '{"network":{"kind":"network"}}',
	              '{"publicTcpHost":"127.0.0.1","publicPortFrom":42000,"publicPortTo":42010}','UNKNOWN',$3)`,
		fixture.integrationID, "docker-"+fixture.integrationID.String()[:8], fixture.userID)
	exec(`INSERT INTO deployment_templates (id, slug, name, created_by) VALUES ($1,$2,'t',$3)`,
		fixture.templateID, "t-"+fixture.templateID.String()[:8], fixture.userID)
	manifest := `{"apiVersion":"actfb.dev/v1alpha1","kind":"DeploymentTemplate","metadata":{"name":"t","slug":"t","description":""},"parameters":[{"key":"flag","type":"string","label":"Flag","required":true,"secret":true}],"generated":[{"key":"password","kind":"password","length":20,"rotateOnReset":true}],"nodes":[{"key":"web","name":"web","kind":"CONTAINER","source":{"type":"OCI_IMAGE","ref":"nginx@sha256:713fc2ebfd92e8217db451477182ee91944b123d2b1ee9fd5a8c6e91ca6c3a11"},"resources":{"cpuMillicores":200,"memoryMiB":64,"diskMiB":64},"endpoints":[{"key":"web","protocol":"http","targetPort":8080,"exposure":"PUBLIC"}]}]}`
	exec(`INSERT INTO deployment_template_versions (id, template_id, version, digest, manifest, files, published_by)
	      VALUES ($1,$2,1,'digest-http',$3,'[]',$4)`, fixture.versionID, fixture.templateID, []byte(manifest), fixture.userID)
	var bindingID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO deployment_bindings (board_id, module_id, task_id, template_version_id, parameters, lifecycle, created_by)
		VALUES ($1,$2,NULL,$3,'{"flag":"public"}','{}',$4) RETURNING id
	`, fixture.boardID, fixture.moduleID, fixture.versionID, fixture.userID).Scan(&bindingID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO deployment_binding_targets (binding_id, kind, integration_id) VALUES ($1,'CONTAINER',$2)`, bindingID, fixture.integrationID)
	var allowlistID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO deployment_template_allowlist (template_version_id, board_id, module_id, approved_by)
		VALUES ($1,$2,$3,$4) RETURNING id`, fixture.versionID, fixture.boardID, fixture.moduleID, fixture.userID).Scan(&allowlistID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO deployment_template_allowlist_targets (allowlist_id, kind, integration_id) VALUES ($1,'CONTAINER',$2)`, allowlistID, fixture.integrationID)
	exec(`INSERT INTO pentest_configs (module_id, deployment_binding_id, reset_enabled, max_resets, submission_policy, created_by)
	      VALUES ($1,$2,TRUE,2,'{"mode":"TEXT_AND_FILES","checker":"MANUAL","files":{"required":true,"maxCount":3}}',$3)`,
		fixture.moduleID, bindingID, fixture.userID)

	registry, err := runtime.NewRegistry(&httpFakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	key := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	sealer, err := secrets.NewCipher(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	service, err := instances.NewService(db, authorization.NewAuthorizer(db), registry, sealer, 1,
		instances.WithPublisher(noopPublisher{}), instances.WithDraftVerifier(readyVerifier{refs: fileRefs}))
	if err != nil {
		t.Fatal(err)
	}
	fixture.service = service
	_ = taskID
	return fixture
}

func findHTTPMigrations(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
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

type httpFakeDriver struct{}

func (d *httpFakeDriver) Name() string          { return "docker" }
func (d *httpFakeDriver) Kind() deployment.Kind { return deployment.KindContainer }
func (d *httpFakeDriver) Capabilities(context.Context, runtime.Target) (deployment.Capabilities, error) {
	return deployment.Capabilities{Kind: deployment.KindContainer}, nil
}
func (d *httpFakeDriver) Validate(context.Context, runtime.Target, deployment.Node) []deployment.Finding {
	return nil
}
func (d *httpFakeDriver) Plan(context.Context, runtime.Target, deployment.Node, *runtime.ProviderResource) (runtime.Plan, error) {
	return runtime.Plan{Action: "CREATE"}, nil
}
func (d *httpFakeDriver) Apply(context.Context, runtime.Target, deployment.Node, runtime.Operation) (runtime.ProviderResource, error) {
	return runtime.ProviderResource{ProviderID: "fake", State: runtime.NodeStateRunning}, nil
}
func (d *httpFakeDriver) Inspect(context.Context, runtime.Target, string) (runtime.ProviderResource, error) {
	return runtime.ProviderResource{State: runtime.NodeStateRunning}, nil
}
func (d *httpFakeDriver) Destroy(context.Context, runtime.Target, string, runtime.Operation) error {
	return nil
}

func (f *httpTestFixture) router(t *testing.T) *echo.Echo {
	t.Helper()
	e := echo.New()
	NewHandler(f.service, testAuth{userID: f.userID}).RegisterRoutes(e)
	return e
}

func (f *httpTestFixture) boardBase(moduleID uuid.UUID) string {
	return "/api/v2/boards/" + f.boardID.String() + "/modules/" + moduleID.String()
}

func seedHTTPUser(t *testing.T, db *postgres.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO users (id, username, email, password, roles) VALUES ($1,$2,$3,'x',ARRAY['user']::text[])
	`, id, "u-"+id.String()[:8], id.String()[:8]+"@test.local"); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertHTTPFile(t *testing.T, f *httpTestFixture, filename string) files.Ref {
	t.Helper()
	id := uuid.New()
	sha := strings.Repeat("b", 64)
	if _, err := f.db.Pool.Exec(context.Background(), `
		INSERT INTO file_objects
			(id, board_id, module_id, task_id, uploader_user_id, purpose, filename, content_type,
			 size_bytes, sha256, quarantine_bucket, quarantine_key, clean_bucket, clean_key, status, validated_at)
		VALUES ($1,$2,$3,NULL,$4,'PENTEST_ATTACHMENT',$5,'text/plain',9,$6,'q',$7,'c',$8,'READY',NOW())
	`, id, f.boardID, f.moduleID, f.userID, filename, sha, "qh-"+id.String(), "ch-"+id.String()); err != nil {
		t.Fatal(err)
	}
	return files.Ref{UUID: id, Filename: filename, ContentType: "text/plain", Size: 9, SHA256: sha}
}

func TestPentestAndAllowlistRoutesHTTP(t *testing.T) {
	fixture := newHTTPTestFixture(t, nil)
	fileRef := insertHTTPFile(t, fixture, "evidence.txt")
	fixture.service = mustServiceWithRefs(t, fixture, []files.Ref{fileRef})
	router := fixture.router(t)
	base := fixture.boardBase(fixture.moduleID)

	start := requestJSON(t, router, http.MethodPost, base+"/pentest/environment", "token", nil,
		map[string]string{"Idempotency-Key": "http-start-key-0001"})
	if start.Code != http.StatusAccepted {
		t.Fatalf("start environment: %d %s", start.Code, start.Body.String())
	}
	environment := requestJSON(t, router, http.MethodGet, base+"/pentest/environment", "token", nil, nil)
	if environment.Code != http.StatusOK {
		t.Fatalf("get environment: %d %s", environment.Code, environment.Body.String())
	}

	draftID := uuid.New()
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		INSERT INTO submission_drafts (id, board_id, module_id, resource_type, created_by_user_id, status, expires_at)
		VALUES ($1,$2,$3,'PENTEST',$4,'OPEN',NOW()+INTERVAL '1 hour')
	`, draftID, fixture.boardID, fixture.moduleID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	submit := requestJSON(t, router, http.MethodPost, base+"/pentest/submissions", "token",
		map[string]any{"text": "http report", "draftUuid": draftID.String()},
		map[string]string{"Idempotency-Key": "http-submit-key-0001"})
	if submit.Code != http.StatusAccepted {
		t.Fatalf("submit: %d %s", submit.Code, submit.Body.String())
	}
	list := requestJSON(t, router, http.MethodGet, base+"/pentest/submissions", "token", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("own list: %d %s", list.Code, list.Body.String())
	}
	payload := decodeJSONMap(t, list)
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("own items: %d", len(items))
	}
	item := items[0].(map[string]any)
	if item["text"] != "http report" {
		t.Fatalf("text missing: %v", item)
	}
	if files, _ := item["files"].([]any); len(files) != 1 {
		t.Fatalf("files missing: %v", item["files"])
	}
	if strings.Contains(list.Body.String(), "qh-") || strings.Contains(list.Body.String(), "ch-") {
		t.Fatalf("HTTP response leaked storage keys: %s", list.Body.String())
	}

	// Privacy: another participant sees an empty list.
	other := seedHTTPUser(t, fixture.db)
	if _, err := fixture.db.Pool.Exec(context.Background(), `INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2)`, fixture.boardID, other); err != nil {
		t.Fatal(err)
	}
	otherRouter := echo.New()
	NewHandler(fixture.service, testAuth{userID: other}).RegisterRoutes(otherRouter)
	otherList := requestJSON(t, otherRouter, http.MethodGet, base+"/pentest/submissions", "token", nil, nil)
	if otherList.Code != http.StatusOK {
		t.Fatalf("other list: %d %s", otherList.Code, otherList.Body.String())
	}
	if items, _ := decodeJSONMap(t, otherList)["items"].([]any); len(items) != 0 {
		t.Fatal("other participant saw private reports")
	}

	// Missing Idempotency-Key is a validation error.
	missing := requestJSON(t, router, http.MethodPost, base+"/pentest/environment", "token", nil, nil)
	if missing.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing idempotency: %d %s", missing.Code, missing.Body.String())
	}

	// Allowlist routes: admin create/list/revoke; duplicate conflict; non-admin denied.
	templatePath := "/api/v2/admin/deployment-templates/" + fixture.templateID.String() + "/allowlist"
	createBody := map[string]any{
		"templateVersionUuid": fixture.versionID.String(),
		"targets":             []map[string]any{{"integrationUuid": fixture.integrationID.String(), "kind": "CONTAINER"}},
	}
	created := requestJSON(t, router, http.MethodPost, templatePath, "token", createBody, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create allowlist: %d %s", created.Code, created.Body.String())
	}
	createdPayload := decodeJSONMap(t, created)
	allowlistID := createdPayload["uuid"].(string)
	if strings.Contains(created.Body.String(), "credentials") || strings.Contains(created.Body.String(), "templateVersion") && strings.Contains(created.Body.String(), "contentBase64") {
		t.Fatalf("allowlist leaked private data: %s", created.Body.String())
	}
	if duplicate := requestJSON(t, router, http.MethodPost, templatePath, "token", createBody, nil); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate allowlist: %d %s", duplicate.Code, duplicate.Body.String())
	}
	listed := requestJSON(t, router, http.MethodGet, templatePath, "token", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list allowlist: %d %s", listed.Code, listed.Body.String())
	}
	if items, _ := decodeJSONMap(t, listed)["items"].([]any); len(items) < 1 {
		t.Fatalf("allowlist list empty: %s", listed.Body.String())
	}
	nonAdminRouter := echo.New()
	NewHandler(fixture.service, testAuth{userID: other}).RegisterRoutes(nonAdminRouter)
	if forbidden := requestJSON(t, nonAdminRouter, http.MethodGet, templatePath, "token", nil, nil); forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin allowlist list: %d %s", forbidden.Code, forbidden.Body.String())
	}
	if revoked := requestJSON(t, router, http.MethodDelete, templatePath+"/"+allowlistID, "token", nil, nil); revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke allowlist: %d %s", revoked.Code, revoked.Body.String())
	}
	if again := requestJSON(t, router, http.MethodDelete, templatePath+"/"+allowlistID, "token", nil, nil); again.Code != http.StatusNoContent {
		t.Fatalf("idempotent revoke: %d %s", again.Code, again.Body.String())
	}
}

func mustServiceWithRefs(t *testing.T, fixture *httpTestFixture, refs []files.Ref) *instances.Service {
	t.Helper()
	registry, err := runtime.NewRegistry(&httpFakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	key := "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	sealer, err := secrets.NewCipher(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	service, err := instances.NewService(fixture.db, authorization.NewAuthorizer(fixture.db), registry, sealer, 1,
		instances.WithPublisher(noopPublisher{}), instances.WithDraftVerifier(readyVerifier{refs: refs}))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func requestJSON(t *testing.T, e *echo.Echo, method, path, token string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, request)
	return recorder
}

func decodeJSONMap(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	payload := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, recorder.Body.String())
	}
	return payload
}

var _ = deployment.KindContainer
