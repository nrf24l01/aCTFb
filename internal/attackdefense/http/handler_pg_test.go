package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/attackdefense"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/users"
)

type fakeAuthenticator struct{ user *users.User }

func (f fakeAuthenticator) AuthenticateAccessToken(context.Context, string) (*users.User, error) {
	return f.user, nil
}

func openHTTPTestDB(t *testing.T) *postgres.DB {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real HTTP attack-defense tests")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "actfb_ad_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	})
	parsed, _ := url.Parse(dsn)
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
	t.Cleanup(func() { _ = sqlDB.Close(); pool.Close() })
	migrationDir, _ := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err := postgres.RunMigrations(context.Background(), sqlDB, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		t.Fatal(err)
	}
	return &postgres.DB{Pool: pool, SQL: sqlDB}
}

func TestWebhookHTTPBoundariesAndRouteCount(t *testing.T) {
	ctx := context.Background()
	db := openHTTPTestDB(t)
	sealer, err := secrets.NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)), 1)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	username := "http-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password, roles, email_verified)
		VALUES ($1,$2,$3,'hash',ARRAY['admin'],true)
	`, actor, username, username+"@example.test"); err != nil {
		t.Fatal(err)
	}
	boardID, moduleID, integrationID, bindingID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO boards (id, slug, name, participation_mode, registration_mode, visibility, status, starts_at, ends_at, created_by)
		VALUES ($1,$2,'AD','INDIVIDUAL','OPEN','PUBLIC','ACTIVE',$3,$4,$5)
	`, boardID, "http-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:10], now.Add(-time.Hour), now.Add(time.Hour), actor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, enabled, created_by)
		VALUES ($1,$2,'AD','ATTACK_DEFENSE',ARRAY['EXPLOIT','PATCH'],TRUE,$3)
	`, moduleID, boardID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO integrations (id, category, type, name, base_url, created_by)
		VALUES ($1,'GIT','GITEA',$2,'http://127.0.0.1:3300',$3)
	`, integrationID, "http-gitea-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:8], actor); err != nil {
		t.Fatal(err)
	}
	secret := "http-webhook-secret"
	ciphertext, err := sealer.Seal([]byte(secret), attackdefense.RepositoryWebhookSecretAAD(bindingID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO repository_bindings (id, integration_id, repository_ref, branch,
			webhook_secret_ciphertext, webhook_secret_key_version, created_by)
		VALUES ($1,$2,'team/source','main',$3,1,$4)
	`, bindingID, integrationID, ciphertext, actor); err != nil {
		t.Fatal(err)
	}

	service, err := attackdefense.NewService(db, nil, sealer,
		attackdefense.WithGitProviderFactory(&attackdefense.GitProviderFactory{AllowInsecureLoopback: true}),
		attackdefense.WithAllowInsecureWebhookTransport(true))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service, fakeAuthenticator{user: &users.User{ID: actor}})
	e := echo.New()
	handler.RegisterRoutes(e)
	if routes := e.Router().Routes(); len(routes) != 22 {
		t.Fatalf("registered routes = %d, want 22", len(routes))
	}

	sha := "dddddddddddddddddddddddddddddddddddddddd"
	body := []byte(`{"ref":"refs/heads/main","after":"` + sha + `","repository":{"full_name":"team/source"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))
	post := func(delivery, contentType string, payload []byte, signature string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v2/webhooks/git/"+bindingID.String(), bytes.NewReader(payload))
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("X-Gitea-Event", "push")
		request.Header.Set("X-Gitea-Delivery", delivery)
		if signature != "" {
			request.Header.Set("X-Gitea-Signature", signature)
		}
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		return recorder
	}
	if recorder := post("http-delivery-1", "application/json", body, signature); recorder.Code != http.StatusAccepted {
		t.Fatalf("valid webhook status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := post("http-delivery-1", "application/json", body, signature); recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate webhook status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := post("http-delivery-2", "application/json", body, ""); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature status = %d", recorder.Code)
	}
	if recorder := post("http-delivery-3", "text/plain", body, signature); recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content-type status = %d", recorder.Code)
	}
	oversized := make([]byte, attackdefense.MaxWebhookBodyBytes+1)
	if recorder := post("http-delivery-4", "application/json", oversized, signature); recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status = %d", recorder.Code)
	}
	unknown := httptest.NewRequest(http.MethodPost, "/api/v2/webhooks/git/"+uuid.NewString(), bytes.NewReader(body))
	unknown.Header.Set("Content-Type", "application/json")
	unknown.Header.Set("X-Gitea-Event", "push")
	unknown.Header.Set("X-Gitea-Delivery", "unknown")
	unknown.Header.Set("X-Gitea-Signature", signature)
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, unknown)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown binding status = %d", recorder.Code)
	}
	unauthorized := httptest.NewRequest(http.MethodGet,
		"/api/v2/boards/"+boardID.String()+"/modules/"+moduleID.String()+"/attack-defense/progress", nil)
	unauthorizedRecorder := httptest.NewRecorder()
	e.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("participant route without token status = %d", unauthorizedRecorder.Code)
	}
	// Malformed JSON must be rejected by the binding helper instead of
	// continuing with a zero request.
	malformed := httptest.NewRequest(http.MethodPost,
		"/api/v2/boards/"+boardID.String()+"/modules/"+moduleID.String()+"/attack-defense/runs",
		strings.NewReader("{"))
	malformed.Header.Set("Content-Type", "application/json")
	malformed.Header.Set("Authorization", "Bearer test-token")
	malformed.Header.Set("Idempotency-Key", "manual-key-000000000001")
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, malformed)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var errorBody struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(unauthorizedRecorder.Body.Bytes(), &errorBody)
	if errorBody.Code != "UNAUTHORIZED" {
		t.Fatalf("error code = %s", errorBody.Code)
	}
}
