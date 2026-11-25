package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	accesshttp "github.com/nrf24l01/actfb/internal/access/http"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/events"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/users"
)

type accessModeHTTPFixture struct {
	db         *postgres.DB
	router     *echo.Echo
	adminToken string
	adminUser  *users.User
	userToken  string
	userID     string
}

// newAccessModeHTTPFixture builds the real board HTTP stack over an isolated
// schema with a real access service, sealer and user service.
func newAccessModeHTTPFixture(t *testing.T) accessModeHTTPFixture {
	t.Helper()
	dsn := testHTTPDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the organizer UX HTTP tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	db, cleanup := openHTTPTestDB(t, ctx, dsn)
	t.Cleanup(cleanup)

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "ux-http-access-secret-value", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "ux-http-refresh-secret-value", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	userService := users.NewUserService(db, cfg, nil)
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	sealer, err := secrets.NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)), 1)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	service := boards.NewService(boards.NewPostgresStore(db), authz, boards.SystemClock{},
		ratelimit.NewMemoryLimiter(512),
		boards.WithArgon2Config(cfg.Argon2id), boards.WithIdempotencySecret("ux-http-secret"),
		boards.WithAccessPolicy(accessSvc), boards.WithAccessService(accessSvc),
		boards.WithSealer(sealer), boards.WithOutbox(events.NewOutbox(db)))
	router := httpapi.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error { return nil })
	NewHandler(service, userService).RegisterRoutes(router)
	// The global access-group routes live in the access HTTP adapter.
	accesshttp.NewHandler(accessSvc, userService).RegisterRoutes(router)

	adminUser, adminToken := registerHTTPUser(t, ctx, userService, db, "ux_http_admin", true)
	user, userToken := registerHTTPUser(t, ctx, userService, db, "ux_http_user", false)
	return accessModeHTTPFixture{
		db: db, router: router, adminToken: adminToken, adminUser: adminUser,
		userToken: userToken, userID: user.ID.String(),
	}
}

// TestAccessModeProjectionOnListAndDetail asserts the canonical mode on the real
// wire for a reloaded board, which the create-response-only check missed.
func TestAccessModeProjectionOnListAndDetail(t *testing.T) {
	f := newAccessModeHTTPFixture(t)
	ctx := context.Background()

	group := decode[oapimodels.AccessGroup](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/access-groups", f.adminToken, map[string]any{"name": "HTTP Audience Group"}, nil,
		http.StatusCreated), http.StatusCreated)

	board := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards", f.adminToken,
		map[string]any{
			"slug": "http-audience", "name": "HTTP Audience", "participationMode": "INDIVIDUAL",
			"registrationMode": "OPEN", "visibility": "PRIVATE", "accessMode": "AUDIENCE",
			"accessGroupUuids": []string{string(*group.UUID)},
			"rating":           map[string]any{"visibility": "PUBLIC"},
		}, nil, http.StatusCreated), http.StatusCreated)
	boardID := string(*board.UUID)

	adminList := decode[oapimodels.AdminBoardsResponse](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/admin/boards?limit=50&offset=0", f.adminToken, nil, nil, http.StatusOK), http.StatusOK)
	found := false
	for _, item := range adminList.Items {
		if item.UUID != nil && string(*item.UUID) == boardID {
			found = true
			if string(item.AccessMode) != "AUDIENCE" {
				t.Fatalf("admin list accessMode = %v, want AUDIENCE", item.AccessMode)
			}
		}
	}
	if !found {
		t.Fatal("board missing from the admin list")
	}

	adminDetail := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID, f.adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if string(adminDetail.AccessMode) != "AUDIENCE" {
		t.Fatalf("admin detail accessMode = %v, want AUDIENCE", adminDetail.AccessMode)
	}

	// A joined member sees AUDIENCE on the participant read, and that response
	// must not leak the group identifiers.
	if _, err := f.db.Pool.Exec(ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)
	`, string(*group.UUID), f.adminUser.ID); err != nil {
		t.Fatalf("add group member: %v", err)
	}
	// A board needs one enabled task module before it can be published.
	module := decode[oapimodels.AdminModule](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules", f.adminToken, map[string]any{
			"name": "Flag module", "type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
		}, nil, http.StatusCreated), http.StatusCreated)
	doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+string(*module.UUID)+"/tasks", f.adminToken,
		map[string]any{
			"title": "Flag", "type": "FLAG", "category": "web", "description": "flag",
			"points": 10, "visible": true,
			"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
			"acceptedAnswers": []string{"flag{audience}"},
			"instance":        map[string]any{"enabled": false},
		}, nil, http.StatusCreated)
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/actions", f.adminToken,
		map[string]any{"action": "PUBLISH"}, nil, http.StatusOK)
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+boardID+"/join", f.adminToken,
		map[string]any{"code": issueJoinCode(t, f.router, boardID, f.adminToken)}, nil, http.StatusOK)
	// Re-joining an existing participant stays idempotent even without a code.
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+boardID+"/join", f.adminToken,
		map[string]any{}, nil, http.StatusOK)

	participantDetail := decode[oapimodels.Board](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/boards/"+boardID, f.adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if string(participantDetail.AccessMode) != "AUDIENCE" {
		t.Fatalf("participant detail accessMode = %v, want AUDIENCE", participantDetail.AccessMode)
	}
	raw := doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+boardID, f.adminToken, nil, nil, http.StatusOK)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode participant board: %v", err)
	}
	if _, leaked := payload["accessGroupUuids"]; leaked {
		t.Fatal("participant board response leaked accessGroupUuids")
	}
}

// TestManualOnlyModulePublishHTTP is the regression for the browser-found
// publish bug: a MANUAL-only single-format module must actually publish.
func TestManualOnlyModulePublishHTTP(t *testing.T) {
	f := newAccessModeHTTPFixture(t)

	board := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards", f.adminToken,
		map[string]any{
			"slug": "http-manual-publish", "name": "HTTP Manual Publish", "participationMode": "INDIVIDUAL",
			"registrationMode": "OPEN", "visibility": "PUBLIC", "accessMode": "PUBLIC",
			"rating": map[string]any{"visibility": "PUBLIC"},
		}, nil, http.StatusCreated), http.StatusCreated)
	boardID := string(*board.UUID)

	module := decode[oapimodels.AdminModule](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules", f.adminToken, map[string]any{
			"name": "Manual only", "type": "TASKS", "allowedTaskTypes": []string{"MANUAL"}, "enabled": true,
		}, nil, http.StatusCreated), http.StatusCreated)
	moduleID := string(*module.UUID)
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks", f.adminToken,
		map[string]any{
			"title": "Report", "type": "MANUAL", "category": "web", "description": "manual",
			"points": 10, "visible": true,
			"submission": map[string]any{
				"mode": "TEXT", "checker": "MANUAL",
				"text": map[string]any{"required": true, "minLength": 1, "maxLength": 1000},
			},
			"instance": map[string]any{"enabled": false},
		}, nil, http.StatusCreated)

	published := decode[oapimodels.Board](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/actions", f.adminToken,
		map[string]any{"action": "PUBLISH"}, nil, http.StatusOK), http.StatusOK)
	if published.Status == nil || string(*published.Status) != "ACTIVE" {
		t.Fatalf("published status = %v, want ACTIVE", published.Status)
	}
}

// TestAnonymousBoardReadBoundaries covers the three authorization edges the
// review asked to pin down.
func TestAnonymousBoardReadBoundaries(t *testing.T) {
	f := newAccessModeHTTPFixture(t)

	// A present but invalid bearer is still 401, never treated as anonymous.
	request := httptest.NewRequest(http.MethodGet, "/api/v2/boards", nil)
	request.Header.Set(echo.HeaderAuthorization, "Bearer not-a-real-token")
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer status = %d, want 401", recorder.Code)
	}

	// A PUBLIC DRAFT board is invisible to anonymous callers, both in the list
	// and by direct id (404 rather than a metadata leak).
	draft := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards", f.adminToken,
		map[string]any{
			"slug": "http-anon-draft", "name": "HTTP Anon Draft", "participationMode": "INDIVIDUAL",
			"registrationMode": "OPEN", "visibility": "PUBLIC", "accessMode": "PUBLIC",
			"rating": map[string]any{"visibility": "PUBLIC"},
		}, nil, http.StatusCreated), http.StatusCreated)
	draftID := string(*draft.UUID)

	list := decode[oapimodels.BoardListResponse](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/boards?limit=100&offset=0", "", nil, nil, http.StatusOK), http.StatusOK)
	for _, item := range list.Items {
		if item.UUID != nil && string(*item.UUID) == draftID {
			t.Fatal("anonymous list exposed a DRAFT board")
		}
	}
	doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+draftID, "", nil, nil, http.StatusNotFound)
}
