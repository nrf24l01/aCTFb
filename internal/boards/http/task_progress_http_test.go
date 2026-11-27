package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/config"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/users"
)

// TestStudentTaskProgressHTTP exercises the real Echo routes and generated
// models: optional omission for preview/unjoined reads, explicit 0/0 for an
// eligible empty board, the completion filter before pagination, and the
// existing 422 validation envelope for an unknown filter value.
func TestStudentTaskProgressHTTP(t *testing.T) {
	dsn := httpTestDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the student task progress HTTP test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, cleanup := openHTTPTestDB(t, ctx, dsn)
	defer cleanup()

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "progress-http-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "progress-http-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	userService := users.NewUserService(db, cfg, nil)
	authz := authorization.NewAuthorizer(db)
	service := boards.NewService(boards.NewPostgresStore(db), authz, boards.SystemClock{},
		ratelimit.NewMemoryLimiter(1024),
		boards.WithArgon2Config(cfg.Argon2id),
		boards.WithIdempotencySecret("progress-http-idempotency-secret"),
		boards.WithAccessPolicy(access.NewService(db, authz)))
	router := httpapi.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error { return nil })
	NewHandler(service, userService).RegisterRoutes(router)

	adminUser, adminToken := registerHTTPUser(t, ctx, userService, db, "progress_http_admin", true)
	_, playerToken := registerHTTPUser(t, ctx, userService, db, "progress_http_player", false)
	_, staffToken := registerHTTPUser(t, ctx, userService, db, "progress_http_staff", false)
	_, outsiderToken := registerHTTPUser(t, ctx, userService, db, "progress_http_outsider", false)

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)
	board := decode[oapimodels.Board](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken, map[string]any{
		"slug": "progress-http", "name": "Progress HTTP", "participationMode": "INDIVIDUAL",
		"registrationMode": "OPEN", "visibility": "PRIVATE",
		"startsAt": start, "endsAt": end, "rating": map[string]any{"visibility": "PUBLIC"},
	}, nil, http.StatusCreated), http.StatusCreated)
	boardID := string(*board.UUID)
	module := decode[oapimodels.AdminModule](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules", adminToken, map[string]any{
			"name": "Tasks", "type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
		}, nil, http.StatusCreated), http.StatusCreated)
	moduleID := string(*module.UUID)
	emptyModule := decode[oapimodels.AdminModule](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules", adminToken, map[string]any{
			"name": "Empty", "type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
		}, nil, http.StatusCreated), http.StatusCreated)
	emptyModuleID := string(*emptyModule.UUID)
	first := decode[oapimodels.AdminTask](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks", adminToken, map[string]any{
			"title": "First", "type": "FLAG", "category": "web", "description": "",
			"points": 10, "visible": true,
			"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
			"acceptedAnswers": []string{"flag{http-first}"},
			"instance":        map[string]any{"enabled": false},
		}, nil, http.StatusCreated), http.StatusCreated)
	firstID := string(*first.UUID)
	doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks", adminToken, map[string]any{
			"title": "Second", "type": "FLAG", "category": "web", "description": "",
			"points": 10, "visible": true,
			"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
			"acceptedAnswers": []string{"flag{http-second}"},
			"instance":        map[string]any{"enabled": false},
		}, nil, http.StatusCreated)
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/actions", adminToken,
		map[string]any{"action": "PUBLISH"}, nil, http.StatusOK)
	doJSON(t, router, http.MethodPost, "/api/v2/boards/"+boardID+"/join", playerToken,
		map[string]any{"code": issueJoinCode(t, router, boardID, adminToken)}, nil, http.StatusOK)
	doJSON(t, router, http.MethodPost,
		"/api/v2/boards/"+boardID+"/modules/"+moduleID+"/tasks/"+firstID+"/submissions", playerToken,
		map[string]any{"text": "flag{http-first}"}, map[string]string{"Idempotency-Key": "progress-http-accept-1"}, http.StatusOK)

	// Participant reads expose the authoritative projection.
	meBody := doJSON(t, router, http.MethodGet, "/api/v2/boards/"+boardID+"/me", playerToken, nil, nil, http.StatusOK)
	me := decode[oapimodels.BoardMe](t, meBody, http.StatusOK)
	if me.TaskProgress == nil || me.TaskProgress.SolvedTasks == nil || me.TaskProgress.TotalTasks == nil ||
		*me.TaskProgress.SolvedTasks != 1 || *me.TaskProgress.TotalTasks != 2 {
		t.Fatalf("HTTP board me progress = %+v, want 1/2", me.TaskProgress)
	}

	modules := decode[oapimodels.ModuleListResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards/"+boardID+"/modules?limit=50&offset=0", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if len(modules.Items) != 2 {
		t.Fatalf("HTTP module list len = %d, want 2", len(modules.Items))
	}
	for _, item := range modules.Items {
		switch string(*item.UUID) {
		case moduleID:
			if item.TaskProgress == nil || *item.TaskProgress.SolvedTasks != 1 || *item.TaskProgress.TotalTasks != 2 {
				t.Fatalf("HTTP module list progress = %+v, want 1/2", item.TaskProgress)
			}
		case emptyModuleID:
			if item.TaskProgress == nil || *item.TaskProgress.SolvedTasks != 0 || *item.TaskProgress.TotalTasks != 0 {
				t.Fatalf("HTTP empty module progress = %+v, want explicit 0/0", item.TaskProgress)
			}
		}
	}
	detail := decode[oapimodels.Module](t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards/"+boardID+"/modules/"+moduleID, playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if detail.TaskProgress == nil || *detail.TaskProgress.SolvedTasks != 1 || *detail.TaskProgress.TotalTasks != 2 {
		t.Fatalf("HTTP module detail progress = %+v, want 1/2", detail.TaskProgress)
	}

	// Default list behavior is unchanged; the filter applies before pagination.
	tasksPath := "/api/v2/boards/" + boardID + "/modules/" + moduleID + "/tasks"
	all := decode[oapimodels.TaskListResponse](t, doJSON(t, router, http.MethodGet,
		tasksPath+"?limit=50&offset=0", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if all.Total == nil || *all.Total != 2 || len(all.Items) != 2 {
		t.Fatalf("HTTP unfiltered tasks total=%v len=%d, want 2/2", all.Total, len(all.Items))
	}
	solved := decode[oapimodels.TaskListResponse](t, doJSON(t, router, http.MethodGet,
		tasksPath+"?limit=50&offset=0&completion=SOLVED", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if solved.Total == nil || *solved.Total != 1 || len(solved.Items) != 1 || !*solved.Items[0].Solved {
		t.Fatalf("HTTP solved filter total=%v len=%d, want 1/1 solved", solved.Total, len(solved.Items))
	}
	unsolved := decode[oapimodels.TaskListResponse](t, doJSON(t, router, http.MethodGet,
		tasksPath+"?limit=50&offset=0&completion=UNSOLVED", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if unsolved.Total == nil || *unsolved.Total != 1 || len(unsolved.Items) != 1 {
		t.Fatalf("HTTP unsolved filter total=%v len=%d, want 1/1", unsolved.Total, len(unsolved.Items))
	}
	paged := decode[oapimodels.TaskListResponse](t, doJSON(t, router, http.MethodGet,
		tasksPath+"?limit=1&offset=0&completion=SOLVED", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if paged.Total == nil || *paged.Total != 1 || len(paged.Items) != 1 {
		t.Fatalf("HTTP paged solved total=%v len=%d, want 1/1", paged.Total, len(paged.Items))
	}
	invalidBody := doJSON(t, router, http.MethodGet, tasksPath+"?limit=50&offset=0&completion=BOGUS",
		playerToken, nil, nil, http.StatusUnprocessableEntity)
	invalid := decode[oapimodels.Http422ValidationFailedError](t, invalidBody, http.StatusUnprocessableEntity)
	if invalid.Code == nil || *invalid.Code != "VALIDATION_FAILED" || len(invalid.FieldErrors) == 0 {
		t.Fatalf("HTTP invalid completion error = %+v", invalid)
	}
	foundField := false
	for _, field := range invalid.FieldErrors {
		if field.Field != nil && *field.Field == "query.completion" {
			foundField = true
		}
	}
	if !foundField {
		t.Fatalf("HTTP invalid completion fieldErrors = %+v, want query.completion", invalid.FieldErrors)
	}

	// An eligible board with no readable active tasks is an explicit 0/0, not
	// an omitted field and never a fabricated 100%.
	emptyBoard := decode[oapimodels.Board](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken, map[string]any{
		"slug": "progress-http-empty", "name": "Progress HTTP Empty", "participationMode": "INDIVIDUAL",
		"registrationMode": "OPEN", "visibility": "PRIVATE",
		"startsAt": start, "endsAt": end, "rating": map[string]any{"visibility": "PUBLIC"},
	}, nil, http.StatusCreated), http.StatusCreated)
	emptyBoardID := string(*emptyBoard.UUID)
	// Publishing needs one enabled task module, so the empty state is produced
	// by a module window that has not started yet: the module exists but no task
	// is currently readable, which is exactly the 0/0 summary case.
	upcomingStart := time.Now().UTC().Add(2 * time.Hour)
	upcomingEnd := time.Now().UTC().Add(3 * time.Hour)
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+emptyBoardID+"/modules", adminToken, map[string]any{
		"name": "Upcoming", "type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
		"startsAt": upcomingStart, "endsAt": upcomingEnd,
	}, nil, http.StatusCreated)
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+emptyBoardID+"/actions", adminToken,
		map[string]any{"action": "PUBLISH"}, nil, http.StatusOK)
	doJSON(t, router, http.MethodPost, "/api/v2/boards/"+emptyBoardID+"/join", playerToken,
		map[string]any{"code": issueJoinCode(t, router, emptyBoardID, adminToken)}, nil, http.StatusOK)
	emptyMe := decode[oapimodels.BoardMe](t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards/"+emptyBoardID+"/me", playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if emptyMe.TaskProgress == nil || *emptyMe.TaskProgress.SolvedTasks != 0 || *emptyMe.TaskProgress.TotalTasks != 0 {
		t.Fatalf("HTTP empty board progress = %+v, want explicit 0/0", emptyMe.TaskProgress)
	}

	// Staff preview and unjoined reads omit the field entirely.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1, (SELECT id FROM users WHERE username='progress_http_staff'), 'board_editor', $2)`,
		boardID, adminUser.ID); err != nil {
		t.Fatalf("grant staff: %v", err)
	}
	staffMe := decodeRawJSON(t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards/"+boardID+"/me", staffToken, nil, nil, http.StatusOK))
	if _, present := staffMe["taskProgress"]; present {
		t.Fatalf("staff preview /me exposed taskProgress: %s", string(mustJSON(t, staffMe)))
	}
	staffModule := decodeRawJSON(t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards/"+boardID+"/modules/"+moduleID, staffToken, nil, nil, http.StatusOK))
	if _, present := staffModule["taskProgress"]; present {
		t.Fatalf("staff preview module exposed taskProgress: %s", string(mustJSON(t, staffModule)))
	}
	if preview, ok := staffModule["preview"].(bool); !ok || !preview {
		t.Fatalf("staff preview flag = %v, want true", staffModule["preview"])
	}
	doJSON(t, router, http.MethodGet, "/api/v2/boards/"+boardID+"/modules/"+moduleID, outsiderToken,
		nil, nil, http.StatusForbidden)
	doJSON(t, router, http.MethodGet, "/api/v2/boards", outsiderToken, nil, nil, http.StatusOK)
}

func httpTestDSN() string {
	if value := os.Getenv("ACTFB_TEST_POSTGRES_DSN"); value != "" {
		return value
	}
	return os.Getenv("ACTFB_TEST_DATABASE_URL")
}

func decodeRawJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode raw response: %v: %s", err, string(raw))
	}
	return value
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	return encoded
}
