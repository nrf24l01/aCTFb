package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/config"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/users"
)

// TestPostgresAuthHTTPIntegration is opt-in. It exercises the real HTTP
// handlers, cookie policy, bearer authentication and Postgres-backed sessions.
// Set ACTFB_TEST_DATABASE_URL to a disposable database. The test creates its
// own isolated schema, applies all migrations, and drops only that schema.
func TestPostgresAuthHTTPIntegration(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthHTTPDB(t, ctx, dsn)
	defer cleanup()

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: strings.Repeat("a", 32), Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: strings.Repeat("b", 32), Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
		WebApp: config.WebAppConfig{
			AllowedOrigins: []string{"http://localhost:8080"},
		},
	}

	service := users.NewUserService(db, cfg, nil)
	service.SetLogger(nil)

	router := echo.New()
	NewUserHandler(service, cfg).RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &stdhttp.Client{Jar: jar, Timeout: 10 * time.Second}

	suffix := uuid.NewString()[:8]
	username := "http_" + suffix
	email := "http_" + suffix + "@example.test"

	registerBody := `{"email":"` + email + `","username":"` + username + `","password":"integration password"}`
	response := doJSON(t, client, stdhttp.MethodPost, server.URL+"/api/v2/auth/register", registerBody, "")
	if response.StatusCode != stdhttp.StatusCreated {
		t.Fatalf("register status = %d; body=%s", response.StatusCode, readBody(t, response))
	}
	register := decodeBody[oapimodels.RegisterResponse](t, response)
	if register.User == nil || register.AccessToken == nil || *register.AccessToken == "" {
		t.Fatal("register response missing user or access token")
	}
	userID := uuid.MustParse(string(*register.User.UUID))
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM auth_sessions WHERE user_id = $1`, userID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	}()

	oldRefresh := response.Cookies()
	if len(oldRefresh) == 0 || oldRefresh[0].Name != refreshCookieName || oldRefresh[0].Value == "" {
		t.Fatalf("register did not set refresh cookie: %+v", oldRefresh)
	}
	if !oldRefresh[0].HttpOnly || oldRefresh[0].Path != refreshCookiePath || oldRefresh[0].SameSite != stdhttp.SameSiteStrictMode {
		t.Fatalf("unexpected refresh cookie attributes: %+v", oldRefresh[0])
	}

	meResponse := doJSON(t, client, stdhttp.MethodGet, server.URL+"/api/v2/me", "", "Bearer "+*register.AccessToken)
	if meResponse.StatusCode != stdhttp.StatusOK {
		t.Fatalf("me status = %d; body=%s", meResponse.StatusCode, readBody(t, meResponse))
	}
	me := decodeBody[oapimodels.Me](t, meResponse)
	if me.UUID == nil || *me.UUID != *register.User.UUID {
		t.Fatalf("me uuid = %+v", me.UUID)
	}

	refreshResponse := doJSON(t, client, stdhttp.MethodPost, server.URL+"/api/v2/auth/refresh", "", "", map[string]string{
		"Origin": "http://localhost:8080",
	})
	if refreshResponse.StatusCode != stdhttp.StatusOK {
		t.Fatalf("refresh status = %d; body=%s", refreshResponse.StatusCode, readBody(t, refreshResponse))
	}
	rotated := decodeBody[oapimodels.TokenResponse](t, refreshResponse)
	if rotated.AccessToken == nil || *rotated.AccessToken == "" {
		t.Fatal("refresh response missing access token")
	}
	if len(refreshResponse.Cookies()) == 0 || refreshResponse.Cookies()[0].Value == oldRefresh[0].Value {
		t.Fatal("refresh cookie did not rotate")
	}

	// Replay the old cookie with a client that does not carry the new jar value.
	replayClient := &stdhttp.Client{Timeout: 10 * time.Second}
	replayRequest, err := stdhttp.NewRequest(stdhttp.MethodPost, server.URL+"/api/v2/auth/refresh", nil)
	if err != nil {
		t.Fatalf("replay request: %v", err)
	}
	replayRequest.Header.Set("Origin", "http://localhost:8080")
	replayRequest.AddCookie(&stdhttp.Cookie{Name: refreshCookieName, Value: oldRefresh[0].Value})
	replayResponse, err := replayClient.Do(replayRequest)
	if err != nil {
		t.Fatalf("replay refresh: %v", err)
	}
	defer replayResponse.Body.Close()
	if replayResponse.StatusCode != stdhttp.StatusUnauthorized {
		t.Fatalf("replay status = %d; body=%s", replayResponse.StatusCode, readBody(t, replayResponse))
	}
	replayEnvelope := decodeBody[responseEnvelope](t, replayResponse)
	if replayEnvelope.Code != "REFRESH_TOKEN_INVALID" {
		t.Fatalf("replay code = %q", replayEnvelope.Code)
	}

	logoutResponse := doJSON(t, client, stdhttp.MethodPost, server.URL+"/api/v2/auth/logout", "", "", map[string]string{
		"Origin": "http://localhost:8080",
	})
	if logoutResponse.StatusCode != stdhttp.StatusNoContent {
		t.Fatalf("logout status = %d; body=%s", logoutResponse.StatusCode, readBody(t, logoutResponse))
	}

	afterLogout, err := stdhttp.NewRequest(stdhttp.MethodPost, server.URL+"/api/v2/auth/refresh", nil)
	if err != nil {
		t.Fatalf("after-logout request: %v", err)
	}
	afterLogout.Header.Set("Origin", "http://localhost:8080")
	afterLogout.AddCookie(&stdhttp.Cookie{Name: refreshCookieName, Value: refreshResponse.Cookies()[0].Value})
	afterLogoutResponse, err := replayClient.Do(afterLogout)
	if err != nil {
		t.Fatalf("after-logout refresh: %v", err)
	}
	defer afterLogoutResponse.Body.Close()
	if afterLogoutResponse.StatusCode != stdhttp.StatusUnauthorized {
		t.Fatalf("after-logout refresh status = %d; body=%s", afterLogoutResponse.StatusCode, readBody(t, afterLogoutResponse))
	}

	// The same account can log in again after logout.
	loginResponse := doJSON(t, client, stdhttp.MethodPost, server.URL+"/api/v2/auth/login",
		`{"username":"`+username+`","password":"integration password"}`, "")
	if loginResponse.StatusCode != stdhttp.StatusOK {
		t.Fatalf("login status = %d; body=%s", loginResponse.StatusCode, readBody(t, loginResponse))
	}
}

func doJSON(t *testing.T, client *stdhttp.Client, method, url, body string, authorization string, headers ...map[string]string) *stdhttp.Response {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	request, err := stdhttp.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	for _, values := range headers {
		for key, value := range values {
			request.Header.Set(key, value)
		}
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return response
}

func decodeBody[T any](t *testing.T, response *stdhttp.Response) T {
	t.Helper()
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode response %q: %v", string(body), err)
	}
	return value
}

func readBody(t *testing.T, response *stdhttp.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "<read error: " + err.Error() + ">"
	}
	return string(body)
}

func openIsolatedAuthHTTPDB(t *testing.T, ctx context.Context, dsn string) (*postgres.DB, func()) {
	t.Helper()

	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}

	schema := "actfb_auth_http_it_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		adminPool.Close()
		t.Fatalf("create isolated schema: %v", err)
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
		t.Fatalf("parse postgres dsn: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
		t.Fatalf("connect isolated postgres schema: %v", err)
	}

	db := &postgres.DB{Pool: pool, SQL: stdlib.OpenDBFromPool(pool)}
	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		_ = db.Close()
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
		t.Fatalf("resolve migrations path: %v", err)
	}
	if err := postgres.RunMigrations(ctx, db.SQL, &config.PostgresConfig{MigrationsPath: migrationsPath}); err != nil {
		_ = db.Close()
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
		t.Fatalf("apply migrations: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
	}
	return db, cleanup
}
