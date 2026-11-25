package http

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/events"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/users"
)

func TestP1RHTTPRoutes(t *testing.T) {
	dsn := testHTTPDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run P1R HTTP integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, cleanup := openHTTPTestDB(t, ctx, dsn)
	defer cleanup()
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE submissions ADD COLUMN IF NOT EXISTS submitted_text TEXT`); err != nil {
		t.Fatalf("add submitted_text test column: %v", err)
	}

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "p1r-http-access", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "p1r-http-refresh", Expiry: 3600},
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
		boards.WithArgon2Config(cfg.Argon2id), boards.WithIdempotencySecret("p1r-http-secret"),
		boards.WithAccessPolicy(accessSvc), boards.WithAccessService(accessSvc),
		boards.WithSealer(sealer), boards.WithOutbox(events.NewOutbox(db)))
	router := httpapi.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error { return nil })
	NewHandler(service, userService).RegisterRoutes(router)

	adminUser, adminToken := registerHTTPUser(t, ctx, userService, db, "p1r_http_admin", true)
	_, p1Token := registerHTTPUser(t, ctx, userService, db, "p1r_http_p1", false)
	_, p2Token := registerHTTPUser(t, ctx, userService, db, "p1r_http_p2", false)

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	board := decode[oapimodels.AdminBoard](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken, map[string]any{
		"slug": "p1r-http-team", "name": "P1R HTTP Team", "participationMode": "TEAM",
		"registrationMode": "OPEN", "visibility": "PUBLIC", "startsAt": start, "endsAt": end,
		"rating":       map[string]any{"visibility": "PUBLIC"},
		"teamSettings": map[string]any{"minMembers": 1, "maxMembers": 3, "allowSelfCreate": true, "allowLeaveAfterStart": true},
	}, nil, http.StatusCreated), http.StatusCreated)
	boardID := string(*board.UUID)
	boardUUID, err := uuid.Parse(boardID)
	if err != nil {
		t.Fatalf("parse board uuid: %v", err)
	}
	if board.TeamSettings == nil || board.TeamSettings.MaxMembers == nil || *board.TeamSettings.MaxMembers != 3 {
		t.Fatalf("AdminBoard teamSettings = %+v", board.TeamSettings)
	}
	// Seed a legacy mixed-format module directly: existing data predates the
	// single-format creation rule and must remain usable for MANUAL+FLAG tasks.
	module, err := boards.NewPostgresStore(db).CreateModule(ctx, boards.CreateModuleParams{
		BoardID: boardUUID, Name: "Tasks", Type: boards.ModuleTypeTasks,
		AllowedTaskTypes: []string{boards.TaskTypeFlag, boards.TaskTypeManual},
		Enabled:          true, CreatedBy: adminUser.ID,
	}, boards.AuditEventParams{Action: "test.module.seed", ResourceType: "module", ActorUserID: adminUser.ID})
	if err != nil {
		t.Fatalf("seed legacy mixed module: %v", err)
	}
	moduleID := module.ID.String()
	manualTask := decode[oapimodels.AdminTask](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks", adminToken, map[string]any{
			"title": "Manual", "type": "MANUAL", "category": "web", "description": "manual",
			"points": 50, "visible": true,
			"submission": map[string]any{"mode": "TEXT", "checker": "MANUAL", "text": map[string]any{"required": true, "minLength": 1, "maxLength": 1024}},
			"instance":   map[string]any{"enabled": false},
		}, nil, http.StatusCreated), http.StatusCreated)
	manualTaskID := string(*manualTask.UUID)
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/actions", adminToken,
		map[string]any{"action": "PUBLISH"}, nil, http.StatusOK)
	boardJoinCode := issueJoinCode(t, router, boardID, adminToken)
	doJSON(t, router, http.MethodPost, "/api/v2/boards/"+boardID+"/join", p1Token, map[string]any{"code": boardJoinCode}, nil, http.StatusOK)
	doJSON(t, router, http.MethodPost, "/api/v2/boards/"+boardID+"/join", p2Token, map[string]any{"code": boardJoinCode}, nil, http.StatusOK)

	team := decode[oapimodels.Team](t, doJSON(t, router, http.MethodPost, "/api/v2/boards/"+boardID+"/teams", p1Token,
		map[string]any{"name": "HTTP Team"}, nil, http.StatusCreated), http.StatusCreated)
	teamID := string(*team.UUID)
	invite := decode[oapimodels.TeamInviteResponse](t, doJSON(t, router, http.MethodPost,
		"/api/v2/boards/"+boardID+"/team/invite-key/rotate", p1Token, nil, nil, http.StatusOK), http.StatusOK)
	doJSON(t, router, http.MethodPost, "/api/v2/boards/"+boardID+"/teams/join", p2Token,
		map[string]any{"inviteKey": *invite.InviteKey}, nil, http.StatusOK)
	doJSON(t, router, http.MethodGet, "/api/v2/boards/"+boardID+"/team", p2Token, nil, nil, http.StatusOK)
	teams := decode[teamListResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/teams?limit=50&offset=0", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if teams.Total == nil || *teams.Total != 1 {
		t.Fatalf("admin teams total=%v, want 1", teams.Total)
	}

	audience := decode[oapimodels.Audience](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/audiences", adminToken,
		map[string]any{"name": "Finalists", "description": "final"}, nil, http.StatusCreated), http.StatusCreated)
	audienceID := string(*audience.UUID)
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/audiences/"+audienceID+"/members",
		adminToken, map[string]any{"subjectType": "TEAM", "subjectUuid": teamID}, nil, http.StatusCreated)
	myAudiences := decode[oapimodels.AudienceListResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards/"+boardID+"/audiences/me", p1Token, nil, nil, http.StatusOK), http.StatusOK)
	if len(myAudiences.Items) != 1 {
		t.Fatalf("my audiences = %d, want 1", len(myAudiences.Items))
	}

	submitPath := "/api/v2/boards/" + boardID + "/modules/" + moduleID + "/tasks/" + manualTaskID + "/submissions"
	pending := decode[oapimodels.Submission](t, doJSON(t, router, http.MethodPost, submitPath, p1Token,
		map[string]any{"text": "manual answer"}, map[string]string{"Idempotency-Key": "p1r-http-manual-01"}, http.StatusOK), http.StatusOK)
	if pending.Status == nil || *pending.Status != oapimodels.SubmissionStatusPENDINGREVIEW || pending.ScoreAwarded != nil {
		t.Fatalf("manual pending = %+v", pending)
	}
	if pending.Text == nil || *pending.Text != "manual answer" {
		t.Fatalf("manual pending text = %v", pending.Text)
	}
	reviewBody := doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/submissions/"+string(*pending.UUID)+"/review", adminToken,
		map[string]any{"decision": "ACCEPT"}, map[string]string{"Idempotency-Key": "p1r-http-review-01"}, http.StatusOK)
	reviewed := decode[oapimodels.AdminSubmission](t, reviewBody, http.StatusOK)
	if reviewed.Status == nil || *reviewed.Status != oapimodels.SubmissionStatusACCEPTED || reviewed.ScoreAwarded == nil || *reviewed.ScoreAwarded != 50 {
		t.Fatalf("reviewed = %+v", reviewed)
	}
	if reviewed.Text == nil || *reviewed.Text != "manual answer" {
		t.Fatalf("reviewed text = %v", reviewed.Text)
	}
	replayed := decode[oapimodels.AdminSubmission](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/submissions/"+string(*pending.UUID)+"/review", adminToken,
		map[string]any{"decision": "ACCEPT"}, map[string]string{"Idempotency-Key": "p1r-http-review-01"}, http.StatusOK), http.StatusOK)
	if replayed.UUID == nil || *replayed.UUID != *reviewed.UUID {
		t.Fatalf("review replay = %+v vs %+v", replayed, reviewed)
	}
	var reviewAudits, judgedEvents int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='submission.review' AND resource_id=$1`, *pending.UUID).Scan(&reviewAudits); err != nil {
		t.Fatalf("count review audits: %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE type='submission.judged' AND payload->>'submissionUuid'=$1`, string(*pending.UUID)).Scan(&judgedEvents); err != nil {
		t.Fatalf("count judged events: %v", err)
	}
	if reviewAudits != 1 || judgedEvents != 1 {
		t.Fatalf("review audit/event counts = %d/%d, want 1/1", reviewAudits, judgedEvents)
	}

	adjustment := decode[oapimodels.ScoreAdjustment](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/score-adjustments", adminToken, map[string]any{
			"subjectType": "TEAM", "subjectUuid": teamID, "delta": 7, "reason": "manual bonus",
		}, nil, http.StatusCreated), http.StatusCreated)
	if adjustment.Delta == nil || *adjustment.Delta != 7 {
		t.Fatalf("adjustment = %+v", adjustment)
	}
	adjustments := decode[oapimodels.ScoreAdjustmentsResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/score-adjustments?limit=50&offset=0", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if adjustments.Total == nil || *adjustments.Total != 1 {
		t.Fatalf("adjustments total=%v, want 1", adjustments.Total)
	}

	// INVITE_ONLY board code round trip.
	inviteBoard := decode[oapimodels.AdminBoard](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken, map[string]any{
		"slug": "p1r-http-invite", "name": "P1R Invite", "participationMode": "INDIVIDUAL",
		"registrationMode": "INVITE_ONLY", "visibility": "PRIVATE",
		"startsAt": start, "endsAt": end, "rating": map[string]any{"visibility": "PUBLIC"},
	}, nil, http.StatusCreated), http.StatusCreated)
	inviteBoardID := string(*inviteBoard.UUID)
	code := decode[oapimodels.BoardInviteResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+inviteBoardID+"/invite-code", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if code.InviteCode == nil || len(*code.InviteCode) < 16 {
		t.Fatalf("board invite code = %+v", code)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE boards SET status='ACTIVE' WHERE id=$1`, inviteBoardID); err != nil {
		t.Fatalf("activate invite board: %v", err)
	}
	doJSON(t, router, http.MethodPost, "/api/v2/boards/"+inviteBoardID+"/join", p1Token,
		map[string]any{"inviteCode": *code.InviteCode}, nil, http.StatusOK)

	// Anonymous browse: only published PUBLIC boards are listed/readable.
	publicList := decode[oapimodels.BoardListResponse](t, doJSON(t, router, http.MethodGet,
		"/api/v2/boards?limit=50&offset=0", "", nil, nil, http.StatusOK), http.StatusOK)
	foundPublic := false
	for _, item := range publicList.Items {
		if item.UUID != nil && string(*item.UUID) == boardID {
			foundPublic = true
		}
		if item.UUID != nil && string(*item.UUID) == inviteBoardID {
			t.Fatalf("anonymous list leaked private invite board %s", inviteBoardID)
		}
	}
	if !foundPublic {
		t.Fatalf("anonymous list did not include public board %s", boardID)
	}
	doJSON(t, router, http.MethodGet, "/api/v2/boards/"+boardID, "", nil, nil, http.StatusOK)
	doJSON(t, router, http.MethodGet, "/api/v2/boards/"+inviteBoardID, "", nil, nil, http.StatusNotFound)

	// Files routes fail closed while the files service is not installed.
	doJSON(t, router, http.MethodPost,
		"/api/v2/boards/"+boardID+"/modules/"+moduleID+"/tasks/"+manualTaskID+"/submission-drafts",
		p1Token, nil, nil, http.StatusServiceUnavailable)
	_ = adminUser
}

func testHTTPDSN() string {
	if value := os.Getenv("ACTFB_TEST_POSTGRES_DSN"); value != "" {
		return value
	}
	return os.Getenv("ACTFB_TEST_DATABASE_URL")
}
