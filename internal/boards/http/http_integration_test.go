package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/config"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/users"
)

// TestP1BoardHTTPIntegration is opt-in and exercises the real Echo routes,
// generated request/response models, shared RequireUser middleware, and the
// service/authorizer against an isolated PostgreSQL schema.
func TestP1BoardHTTPIntegration(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("ACTFB_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the P1 HTTP integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, cleanup := openHTTPTestDB(t, ctx, dsn)
	defer cleanup()

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "p1-http-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "p1-http-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	userService := users.NewUserService(db, cfg, nil)
	authz := authorization.NewAuthorizer(db)
	service := boards.NewService(boards.NewPostgresStore(db), authz, boards.SystemClock{},
		ratelimit.NewMemoryLimiter(1024),
		boards.WithArgon2Config(cfg.Argon2id),
		boards.WithIdempotencySecret("p1-http-idempotency-secret"))

	router := httpapi.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error { return nil })
	NewHandler(service, userService).RegisterRoutes(router)

	adminUser, adminToken := registerHTTPUser(t, ctx, userService, db, "http_admin", true)
	_, playerToken := registerHTTPUser(t, ctx, userService, db, "http_player", false)
	_, editorToken := registerHTTPUser(t, ctx, userService, db, "http_editor", false)
	_ = adminUser

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)
	board := decode[oapimodels.Board](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken, map[string]any{
		"slug": "http-board", "name": "HTTP Board", "participationMode": "INDIVIDUAL",
		"registrationMode": "OPEN", "visibility": "PRIVATE",
		"startsAt": start, "endsAt": end, "rating": map[string]any{"visibility": "PUBLIC"},
	}, nil, http.StatusCreated), http.StatusCreated)
	boardID := string(*board.UUID)

	module := decode[oapimodels.AdminModule](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/modules", adminToken, map[string]any{
		"name": "Tasks", "type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
	}, nil, http.StatusCreated), http.StatusCreated)
	moduleID := string(*module.UUID)

	modulePage := decode[oapimodels.AdminModulesResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/modules?limit=1&offset=0&query=Tasks", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if modulePage.Total == nil || *modulePage.Total != 1 || len(modulePage.Items) != 1 {
		t.Fatalf("adminListModules query total=%v len=%d, want 1/1", modulePage.Total, len(modulePage.Items))
	}
	emptyModules := decode[oapimodels.AdminModulesResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/modules?limit=50&offset=0&query=NoSuchModule", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if emptyModules.Total == nil || *emptyModules.Total != 0 || len(emptyModules.Items) != 0 {
		t.Fatalf("adminListModules missing query total=%v len=%d, want 0/0", emptyModules.Total, len(emptyModules.Items))
	}

	task := decode[oapimodels.AdminTask](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks", adminToken, map[string]any{
			"title": "HTTP flag", "type": "FLAG", "category": "web", "description": "secret description",
			"points": 75, "visible": true,
			"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
			"acceptedAnswers": []string{"flag{http}"},
			"instance":        map[string]any{"enabled": false},
		}, nil, http.StatusCreated), http.StatusCreated)
	taskID := string(*task.UUID)

	adminTasks := decode[oapimodels.AdminTasksResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks?limit=50&offset=0", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if adminTasks.Total == nil || *adminTasks.Total < 1 || len(adminTasks.Items) < 1 {
		t.Fatalf("adminListTasks total=%v len=%d, want at least 1", adminTasks.Total, len(adminTasks.Items))
	}
	adminModule := decode[oapimodels.AdminModule](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID, adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if adminModule.UUID == nil || string(*adminModule.UUID) != moduleID {
		t.Fatalf("adminGetModule = %+v", adminModule)
	}
	adminTask := decode[oapimodels.AdminTask](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks/"+taskID, adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if adminTask.UUID == nil || string(*adminTask.UUID) != taskID {
		t.Fatalf("adminGetTask = %+v", adminTask)
	}
	batch := decode[oapimodels.BatchUpdatedResponse](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks/batch-visibility", adminToken,
		map[string]any{"taskUuids": []string{taskID}, "visible": false}, nil, http.StatusOK), http.StatusOK)
	if batch.Updated == nil || *batch.Updated != 1 {
		t.Fatalf("batch visibility updated=%v", batch.Updated)
	}
	doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks/batch-visibility", adminToken,
		map[string]any{"taskUuids": []string{taskID}, "visible": true}, nil, http.StatusOK)
	adminScoreboard := decode[oapimodels.ScoreboardResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/scoreboard", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if adminScoreboard.UpdatedAt == nil {
		t.Fatal("admin scoreboard missing updatedAt")
	}

	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/actions", adminToken,
		map[string]any{"action": "PUBLISH"}, nil, http.StatusOK)
	doJSON(t, router, http.MethodPost, "/api/v2/boards/"+boardID+"/join", playerToken,
		map[string]any{}, nil, http.StatusOK)

	// Reject every unsupported first-slice policy explicitly rather than
	// accepting and ignoring it.
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken, map[string]any{
		"slug": "http-team", "name": "Team", "participationMode": "TEAM",
		"registrationMode": "OPEN", "visibility": "PRIVATE",
		"rating": map[string]any{"visibility": "PUBLIC"},
	}, nil, http.StatusUnprocessableEntity)
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks", adminToken, map[string]any{
		"title": "Instance", "type": "INSTANCE", "category": "web", "description": "",
		"points": 1, "visible": true,
		"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
		"acceptedAnswers": []string{"x"},
		"instance":        map[string]any{"enabled": true},
	}, nil, http.StatusUnprocessableEntity)

	submitPath := "/api/v2/boards/" + boardID + "/modules/" + moduleID + "/tasks/" + taskID + "/submissions"
	wrong := decode[oapimodels.Submission](t, doJSON(t, router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{wrong}"}, map[string]string{"Idempotency-Key": "http-wrong-key-0001"}, http.StatusOK), http.StatusOK)
	if wrong.Status == nil || *wrong.Status != oapimodels.SubmissionStatusREJECTED || wrong.ScoreAwarded != nil {
		t.Fatalf("wrong HTTP verdict = %+v", wrong)
	}
	right := decode[oapimodels.Submission](t, doJSON(t, router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{http}"}, map[string]string{"Idempotency-Key": "http-right-key-0001"}, http.StatusOK), http.StatusOK)
	if right.Status == nil || *right.Status != oapimodels.SubmissionStatusACCEPTED || right.ScoreAwarded == nil || *right.ScoreAwarded != 75 {
		t.Fatalf("right HTTP verdict = %+v", right)
	}

	history := decode[oapimodels.SubmissionListResponse](t, doJSON(t, router, http.MethodGet,
		submitPath+"/me", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if history.Total == nil || *history.Total != 2 {
		t.Fatalf("HTTP history total = %v, want 2", history.Total)
	}
	for _, item := range history.Items {
		if item.Text != nil {
			t.Fatalf("HTTP history leaked text: %q", *item.Text)
		}
	}
	scoreboard := decode[oapimodels.ScoreboardResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards/"+boardID+"/scoreboard", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if len(scoreboard.Items) != 1 || scoreboard.Items[0].Score == nil || *scoreboard.Items[0].Score != 75 {
		t.Fatalf("HTTP scoreboard = %+v", scoreboard)
	}

	// Same key/body is idempotent; a new key after solve conflicts.
	replay := decode[oapimodels.Submission](t, doJSON(t, router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{http}"}, map[string]string{"Idempotency-Key": "http-right-key-0001"}, http.StatusOK), http.StatusOK)
	if replay.UUID == nil || right.UUID == nil || *replay.UUID != *right.UUID {
		t.Fatalf("HTTP replay changed submission: %+v vs %+v", replay, right)
	}
	doJSON(t, router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{http}"}, map[string]string{"Idempotency-Key": "http-right-key-0002"}, http.StatusConflict)

	// Invalid idempotency header and unsupported draft UUID.
	doJSON(t, router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{http}"}, map[string]string{"Idempotency-Key": "short"}, http.StatusUnprocessableEntity)
	doJSON(t, router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{http}", "draftUuid": uuid.NewString()}, map[string]string{"Idempotency-Key": "http-draft-key-0001"}, http.StatusUnprocessableEntity)

	// PATCH nullable dates can be explicitly cleared through raw presence.
	doJSON(t, router, http.MethodPatch, "/api/v2/admin/boards/"+boardID, adminToken,
		map[string]any{"startsAt": nil, "endsAt": nil}, nil, http.StatusOK)

	// Unknown field gets the standard 422 validation envelope.
	doJSON(t, router, http.MethodPatch, "/api/v2/admin/boards/"+boardID, adminToken,
		map[string]any{"unknown": true}, nil, http.StatusUnprocessableEntity)

	// Admin submission read is scoped and does not return flags.
	subs := decode[oapimodels.AdminSubmissionListResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/submissions", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if subs.Total == nil || *subs.Total != 2 {
		t.Fatalf("admin HTTP submissions total = %v, want 2", subs.Total)
	}
	for _, item := range subs.Items {
		if item.Text != nil {
			t.Fatalf("admin HTTP submission leaked text: %q", *item.Text)
		}
	}

	// Editor on board A cannot mutate board B (IDOR).
	boardB := decode[oapimodels.Board](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken, map[string]any{
		"slug": "http-board-b", "name": "HTTP Board B", "participationMode": "INDIVIDUAL",
		"registrationMode": "OPEN", "visibility": "PRIVATE",
		"rating": map[string]any{"visibility": "PUBLIC"},
	}, nil, http.StatusCreated), http.StatusCreated)
	boardBID := string(*boardB.UUID)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1, (SELECT id FROM users WHERE username='http_editor'), 'board_editor', $2)`,
		boardID, adminUser.ID); err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	doJSON(t, router, http.MethodPatch, "/api/v2/admin/boards/"+boardBID, editorToken,
		map[string]any{"name": "Hijacked"}, nil, http.StatusForbidden)

	// Regression: GET /admin/boards?limit=50&offset=0 previously returned 500
	// because the count query had bind args but no placeholders. Verify admin,
	// filters, paging, participant scope and module-staff scope over real HTTP.
	adminPage := decode[oapimodels.AdminBoardsResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards?limit=50&offset=0", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if adminPage.Total == nil || *adminPage.Total != 2 || len(adminPage.Items) != 2 {
		t.Fatalf("adminListBoards unfiltered total=%v len=%d, want 2/2", adminPage.Total, len(adminPage.Items))
	}
	filteredPage := decode[oapimodels.AdminBoardsResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards?limit=1&offset=0&status=ACTIVE&moduleType=TASKS&query="+url.QueryEscape("HTTP Board"),
		adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if filteredPage.Total == nil || *filteredPage.Total != 1 || len(filteredPage.Items) != 1 {
		t.Fatalf("adminListBoards filtered total=%v len=%d, want 1/1", filteredPage.Total, len(filteredPage.Items))
	}
	pagedPage := decode[oapimodels.AdminBoardsResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards?limit=1&offset=1&participationMode=INDIVIDUAL", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if pagedPage.Total == nil || *pagedPage.Total != 2 || len(pagedPage.Items) != 1 {
		t.Fatalf("adminListBoards paged total=%v len=%d, want 2/1", pagedPage.Total, len(pagedPage.Items))
	}
	participantPage := decode[oapimodels.BoardListResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards?limit=50&offset=0", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if participantPage.Total == nil || *participantPage.Total != 1 || len(participantPage.Items) != 1 {
		t.Fatalf("participant listBoards total=%v len=%d, want 1/1", participantPage.Total, len(participantPage.Items))
	}
	if got := string(*participantPage.Items[0].UUID); got != boardID {
		t.Fatalf("participant listBoards returned %s, want joined board %s", got, boardID)
	}
	staffPage := decode[oapimodels.BoardListResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards?limit=50&offset=0", editorToken, nil, nil, http.StatusOK), http.StatusOK)
	if staffPage.Total == nil || *staffPage.Total != 1 || len(staffPage.Items) != 1 {
		t.Fatalf("staff listBoards total=%v len=%d, want 1/1", staffPage.Total, len(staffPage.Items))
	}
	if got := string(*staffPage.Items[0].UUID); got != boardID {
		t.Fatalf("staff listBoards returned %s, want assigned board %s", got, boardID)
	}

	// P1-05: joined participant cannot read UPCOMING module description, but
	// still receives minimal list metadata without a description field.
	futureStart := time.Now().UTC().Add(2 * time.Hour)
	futureEnd := time.Now().UTC().Add(4 * time.Hour)
	upcoming := decode[oapimodels.Board](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken, map[string]any{
		"slug": "http-upcoming", "name": "HTTP Upcoming", "participationMode": "INDIVIDUAL",
		"registrationMode": "OPEN", "visibility": "PRIVATE",
		"startsAt": futureStart, "endsAt": futureEnd,
		"rating": map[string]any{"visibility": "PUBLIC"},
	}, nil, http.StatusCreated), http.StatusCreated)
	upcomingID := string(*upcoming.UUID)
	upcomingModule := decode[oapimodels.AdminModule](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+upcomingID+"/modules", adminToken, map[string]any{
			"name": "Upcoming Tasks", "description": "pre-start secret module description",
			"type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
		}, nil, http.StatusCreated), http.StatusCreated)
	upcomingModuleID := string(*upcomingModule.UUID)
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+upcomingID+"/actions", adminToken,
		map[string]any{"action": "PUBLISH"}, nil, http.StatusOK)
	doJSON(t, router, http.MethodPost, "/api/v2/boards/"+upcomingID+"/join", playerToken,
		map[string]any{}, nil, http.StatusOK)
	doJSON(t, router, http.MethodGet,
		"/api/v2/boards/"+upcomingID+"/modules/"+upcomingModuleID, playerToken, nil, nil, http.StatusForbidden)
	upcomingList := decode[oapimodels.ModuleListResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards/"+upcomingID+"/modules?limit=50&offset=0", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if upcomingList.Total == nil || *upcomingList.Total != 1 || len(upcomingList.Items) != 1 {
		t.Fatalf("upcoming module list total=%v len=%d, want 1/1", upcomingList.Total, len(upcomingList.Items))
	}
	if upcomingList.Items[0].Status == nil || *upcomingList.Items[0].Status != oapimodels.ModuleStatusUPCOMING {
		t.Fatalf("upcoming module list status = %v", upcomingList.Items[0].Status)
	}

	// L2: module-scoped reviewer sees uniform 404 for a valid sibling
	// submission and a random UUID; no forbidden metadata and no read audit.
	siblingModule := decode[oapimodels.AdminModule](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules", adminToken, map[string]any{
			"name": "Sibling", "type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
		}, nil, http.StatusCreated), http.StatusCreated)
	siblingModuleID := string(*siblingModule.UUID)
	siblingTask := decode[oapimodels.AdminTask](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+siblingModuleID+"/tasks", adminToken, map[string]any{
			"title": "Sibling flag", "type": "FLAG", "category": "web", "description": "sibling",
			"points": 5, "visible": true,
			"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
			"acceptedAnswers": []string{"flag{sibling}"},
			"instance":        map[string]any{"enabled": false},
		}, nil, http.StatusCreated), http.StatusCreated)
	siblingTaskID := string(*siblingTask.UUID)
	siblingSubmission := decode[oapimodels.Submission](t, doJSON(t, router, http.MethodPost,
		"/api/v2/boards/"+boardID+"/modules/"+siblingModuleID+"/tasks/"+siblingTaskID+"/submissions",
		playerToken, map[string]any{"text": "flag{sibling}"},
		map[string]string{"Idempotency-Key": "http-sibling-key-01"}, http.StatusOK), http.StatusOK)
	siblingSubmissionID := string(*siblingSubmission.UUID)

	reviewerUser, reviewerToken := registerHTTPUser(t, ctx, userService, db, "http_reviewer", false)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
		VALUES ($1,$2,$3,'reviewer',$4)`, boardID, moduleID, reviewerUser.ID, adminUser.ID); err != nil {
		t.Fatalf("grant reviewer: %v", err)
	}
	siblingBody := doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/submissions/"+siblingSubmissionID, reviewerToken, nil, nil,
		http.StatusNotFound)
	randomBody := doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/submissions/"+uuid.NewString(), reviewerToken, nil, nil,
		http.StatusNotFound)
	siblingError := decode[oapimodels.APIError](t, siblingBody, http.StatusNotFound)
	randomError := decode[oapimodels.APIError](t, randomBody, http.StatusNotFound)
	if siblingError.Code == nil || randomError.Code == nil ||
		*siblingError.Code != oapimodels.ErrorCodeSUBMISSIONNOTFOUND || *randomError.Code != *siblingError.Code {
		t.Fatalf("L2 codes sibling=%v random=%v, want identical SUBMISSION_NOT_FOUND", siblingError.Code, randomError.Code)
	}
	var unauthorizedReads int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events WHERE actor_user_id=$1 AND action='submission.read'`,
		reviewerUser.ID).Scan(&unauthorizedReads); err != nil {
		t.Fatalf("count unauthorized read audit: %v", err)
	}
	if unauthorizedReads != 0 {
		t.Fatalf("unauthorized sibling read wrote %d audit rows", unauthorizedReads)
	}

	// Unauthenticated and badly formed bearer requests fail with 401.
	doJSON(t, router, http.MethodGet, "/api/v2/boards", "", nil, nil, http.StatusUnauthorized)
	doJSON(t, router, http.MethodGet, "/api/v2/boards", "not-a-token", nil, nil, http.StatusUnauthorized)

	// Wrong content type is 415.
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/boards", strings.NewReader(`{}`))
	request.Header.Set(echo.HeaderContentType, "text/plain")
	request.Header.Set(echo.HeaderAuthorization, "Bearer "+adminToken)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type status = %d, want 415: %s", recorder.Code, recorder.Body.String())
	}
}

func registerHTTPUser(t *testing.T, ctx context.Context, service *users.UserService, db *postgres.DB, username string, admin bool) (*users.User, string) {
	t.Helper()
	result, err := service.Register(ctx, users.RegisterInput{
		Username: username, Email: username + "@example.test", Password: "integration-password",
	})
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	if admin {
		if _, err := db.Pool.Exec(ctx, `UPDATE users SET roles=ARRAY['user','admin'] WHERE id=$1`, result.User.ID); err != nil {
			t.Fatalf("promote admin: %v", err)
		}
	}
	return result.User, result.AccessToken
}

func doJSON(t *testing.T, router *echo.Echo, method, path, token string, body any, headers map[string]string, wantStatus int) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set(echo.HeaderContentType, "application/json")
	}
	if token != "" {
		request.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d: %s", method, path, recorder.Code, wantStatus, recorder.Body.String())
	}
	return recorder.Body.Bytes()
}

func decode[T any](t *testing.T, raw []byte, wantStatus int) T {
	t.Helper()
	_ = wantStatus
	var value T
	if len(raw) == 0 {
		return value
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode response: %v: %s", err, string(raw))
	}
	return value
}

func openHTTPTestDB(t *testing.T, ctx context.Context, dsn string) (*postgres.DB, func()) {
	t.Helper()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	schema := "actfb_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(schema, "actfb_test_") || len(schema) > 63 {
		admin.Close()
		t.Fatalf("unsafe schema %q", schema)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	}
	scoped, err := httpDSNWithSearchPath(dsn, schema)
	if err != nil {
		cleanup()
		t.Fatalf("scoped DSN: %v", err)
	}
	poolCfg, err := pgxpool.ParseConfig(scoped)
	if err != nil {
		cleanup()
		t.Fatalf("parse pool: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		cleanup()
		t.Fatalf("create pool: %v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		cleanup()
		t.Fatalf("resolve migrations: %v", err)
	}
	if err := postgres.RunMigrations(ctx, sqlDB, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		cleanup()
		t.Fatalf("migrate: %v", err)
	}
	return &postgres.DB{Pool: pool, SQL: sqlDB}, func() {
		cleanup()
		_ = sqlDB.Close()
		pool.Close()
	}
}

func httpDSNWithSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", errors.New("DSN must use postgres:// or postgresql:// URL form")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	return u.String(), nil
}
