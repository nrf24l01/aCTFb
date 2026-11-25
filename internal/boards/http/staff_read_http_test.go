package http

import (
	"bytes"
	"context"
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
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/users"
)

// staffReadHTTPFixture is the production authorization stack over an isolated
// schema: real access policy (access groups + audiences), real authorizer and
// real JWT-authenticated HTTP routes.
type staffReadHTTPFixture struct {
	db      *postgres.DB
	router  *echo.Echo
	ctx     context.Context
	users   *users.UserService
	userCfg *config.Config
}

type staffReadBoard struct {
	boardID    string
	moduleID   string
	siblingID  string
	taskID     string
	draftID    string
	groupID    string
	adminToken string
}

func newStaffReadHTTPFixture(t *testing.T) staffReadHTTPFixture {
	t.Helper()
	dsn := testHTTPDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the staff read HTTP tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	db, cleanup := openHTTPTestDB(t, ctx, dsn)
	t.Cleanup(cleanup)

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "staff-read-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "staff-read-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	userService := users.NewUserService(db, cfg, nil)
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	service := boards.NewService(boards.NewPostgresStore(db), authz, boards.SystemClock{},
		ratelimit.NewMemoryLimiter(512),
		boards.WithArgon2Config(cfg.Argon2id), boards.WithIdempotencySecret("staff-read-secret"),
		boards.WithAccessPolicy(accessSvc), boards.WithAccessService(accessSvc))
	router := httpapi.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error { return nil })
	NewHandler(service, userService).RegisterRoutes(router)
	accesshttp.NewHandler(accessSvc, userService).RegisterRoutes(router)
	return staffReadHTTPFixture{db: db, router: router, ctx: ctx, users: userService, userCfg: cfg}
}

// seedStaffReadBoard creates a PRIVATE, access-group gated, ACTIVE board with two
// TASKS modules and one visible + one draft FLAG task in module A. The admin is
// never a participant and is not a member of the board access group.
func seedStaffReadBoard(t *testing.T, f staffReadHTTPFixture, slug string) staffReadBoard {
	t.Helper()
	_, adminToken := registerHTTPUser(t, f.ctx, f.users, f.db, "admin_"+slug, true)

	group := decode[oapimodels.AccessGroup](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/access-groups", adminToken, map[string]any{"name": "gated " + slug}, nil,
		http.StatusCreated), http.StatusCreated)

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)
	board := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards", adminToken,
		map[string]any{
			"slug": slug, "name": "Staff read " + slug, "participationMode": "INDIVIDUAL",
			"registrationMode": "OPEN", "visibility": "PRIVATE", "accessMode": "AUDIENCE",
			"accessGroupUuids": []string{string(*group.UUID)},
			"startsAt":         start, "endsAt": end,
			"rating": map[string]any{"visibility": "PUBLIC"},
		}, nil, http.StatusCreated), http.StatusCreated)
	boardID := string(*board.UUID)

	module := decode[oapimodels.AdminModule](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules", adminToken, map[string]any{
			"name": "Flag module", "type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
		}, nil, http.StatusCreated), http.StatusCreated)
	sibling := decode[oapimodels.AdminModule](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules", adminToken, map[string]any{
			"name": "Sibling module", "type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
		}, nil, http.StatusCreated), http.StatusCreated)

	task := decode[oapimodels.AdminTask](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+string(*module.UUID)+"/tasks", adminToken,
		map[string]any{
			"title": "Visible flag", "type": "FLAG", "category": "web", "description": "staff preview",
			"points": 100, "visible": true,
			"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
			"acceptedAnswers": []string{"flag{staff-preview-secret}"},
			"instance":        map[string]any{"enabled": false},
		}, nil, http.StatusCreated), http.StatusCreated)
	draft := decode[oapimodels.AdminTask](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/modules/"+string(*module.UUID)+"/tasks", adminToken,
		map[string]any{
			"title": "Draft flag", "type": "FLAG", "category": "web", "description": "unpublished",
			"points": 50, "visible": false,
			"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
			"acceptedAnswers": []string{"flag{draft-secret}"},
			"instance":        map[string]any{"enabled": false},
		}, nil, http.StatusCreated), http.StatusCreated)

	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/actions", adminToken,
		map[string]any{"action": "PUBLISH"}, nil, http.StatusOK)

	return staffReadBoard{
		boardID: boardID, moduleID: string(*module.UUID), siblingID: string(*sibling.UUID),
		taskID: string(*task.UUID), draftID: string(*draft.UUID), groupID: string(*group.UUID),
		adminToken: adminToken,
	}
}

func (b staffReadBoard) modulePath(moduleID string) string {
	return "/api/v2/boards/" + b.boardID + "/modules/" + moduleID
}

// TestGlobalAdminReadsModuleAndTasksWithoutJoining is the exact reported
// production bug: GET /modules returned 200 while GET /modules/:id/tasks
// returned 403 BOARD_ACCESS_DENIED for a global admin that never joined the
// access-group gated board.
func TestGlobalAdminReadsModuleAndTasksWithoutJoining(t *testing.T) {
	f := newStaffReadHTTPFixture(t)
	b := seedStaffReadBoard(t, f, "staff-read-admin")

	doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+b.boardID, b.adminToken, nil, nil, http.StatusOK)
	doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+b.boardID+"/modules", b.adminToken, nil, nil, http.StatusOK)
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID), b.adminToken, nil, nil, http.StatusOK)

	page := decode[oapimodels.TaskListResponse](t, doJSON(t, f.router, http.MethodGet,
		b.modulePath(b.moduleID)+"/tasks?limit=50&offset=0", b.adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if page.Total == nil || *page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("staff preview tasks total=%v len=%d, want 2/2 (visible + draft for module.tasks.manage)", page.Total, len(page.Items))
	}

	// The participant projection must never expose accepted answers, hashes or
	// instance credentials, even to an admin preview.
	raw := doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks/"+b.taskID, b.adminToken, nil, nil, http.StatusOK)
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	for _, forbidden := range []string{"acceptedAnswers", "answerHash", "hashes", "secretParameters", "credentials"} {
		if _, present := decoded[forbidden]; present {
			t.Fatalf("staff preview leaked %q: %s", forbidden, raw)
		}
	}
	if containsBytes(raw, "flag{staff-preview-secret}") {
		t.Fatalf("staff preview leaked the accepted answer: %s", raw)
	}
	detail := decode[oapimodels.Task](t, raw, http.StatusOK)
	if detail.Title == nil || *detail.Title != "Visible flag" {
		t.Fatalf("staff preview task = %+v", detail)
	}

	// Preview is a read capability only: participation-gated actions stay denied.
	submitPath := b.modulePath(b.moduleID) + "/tasks/" + b.taskID + "/submissions"
	doJSON(t, f.router, http.MethodPost, submitPath, b.adminToken,
		map[string]any{"text": "flag{staff-preview-secret}"},
		map[string]string{"Idempotency-Key": "staff-read-submit-0001"}, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet, submitPath+"/me", b.adminToken, nil, nil, http.StatusForbidden)
	// Instance start is not a preview capability. With runtime instances
	// disabled the route is not registered at all (404); when it is registered
	// the participation check denies it (403). Neither is a success.
	if status := doJSONStatus(t, f.router, http.MethodPost,
		b.modulePath(b.moduleID)+"/tasks/"+b.taskID+"/instance", b.adminToken, nil,
		map[string]string{"Idempotency-Key": "staff-read-instance-0001"}); status != http.StatusForbidden &&
		status != http.StatusNotFound {
		t.Fatalf("non-participant staff instance start status = %d, want 403 or 404", status)
	}
}

// TestModuleScopedStaffReadsOnlyAssignedModule proves scoped staff preview is
// limited to the exact module binding and does not leak sibling modules.
func TestModuleScopedStaffReadsOnlyAssignedModule(t *testing.T) {
	f := newStaffReadHTTPFixture(t)
	b := seedStaffReadBoard(t, f, "staff-read-scoped")

	editor, editorToken := registerHTTPUser(t, f.ctx, f.users, f.db, "scoped_editor", false)
	_ = editor
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
		VALUES ($1, $2, (SELECT id FROM users WHERE username='scoped_editor'), 'board_editor',
		        (SELECT id FROM users WHERE username='admin_staff-read-scoped'))`,
		b.boardID, b.moduleID); err != nil {
		t.Fatalf("grant module editor: %v", err)
	}

	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID), editorToken, nil, nil, http.StatusOK)
	page := decode[oapimodels.TaskListResponse](t, doJSON(t, f.router, http.MethodGet,
		b.modulePath(b.moduleID)+"/tasks?limit=50&offset=0", editorToken, nil, nil, http.StatusOK), http.StatusOK)
	if page.Total == nil || *page.Total < 1 {
		t.Fatalf("scoped staff tasks total=%v, want at least the visible task", page.Total)
	}
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks/"+b.taskID, editorToken, nil, nil, http.StatusOK)

	// Sibling module is outside the binding: module detail and task list deny.
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.siblingID), editorToken, nil, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.siblingID)+"/tasks", editorToken, nil, nil, http.StatusForbidden)

	// Revoking the binding denies the next request without a token change.
	if _, err := f.db.Pool.Exec(f.ctx, `
		UPDATE board_role_bindings SET revoked_at = clock_timestamp()
		WHERE board_id=$1 AND user_id=(SELECT id FROM users WHERE username='scoped_editor')`, b.boardID); err != nil {
		t.Fatalf("revoke binding: %v", err)
	}
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks", editorToken, nil, nil, http.StatusForbidden)
}

// TestParticipantReadStillRequiresLiveGroupAndAudience is the negative half:
// staff preview must not waive access-group revocation, module audiences or
// participation for ordinary users.
func TestParticipantReadStillRequiresLiveGroupAndAudience(t *testing.T) {
	f := newStaffReadHTTPFixture(t)
	b := seedStaffReadBoard(t, f, "staff-read-participant")

	player, playerToken := registerHTTPUser(t, f.ctx, f.users, f.db, "plain_player", false)
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)`, b.groupID, player.ID); err != nil {
		t.Fatalf("join group: %v", err)
	}
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+b.boardID+"/join", playerToken,
		map[string]any{"code": issueJoinCode(t, f.router, b.boardID, b.adminToken)}, nil, http.StatusOK)
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks", playerToken, nil, nil, http.StatusOK)

	// Access-group revocation is immediate even with a scoped role.
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1, $2, 'board_editor', (SELECT id FROM users WHERE username='admin_staff-read-participant'))`,
		b.boardID, player.ID); err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	if _, err := f.db.Pool.Exec(f.ctx, `
		DELETE FROM access_group_members WHERE group_id=$1 AND user_id=$2`, b.groupID, player.ID); err != nil {
		t.Fatalf("revoke group: %v", err)
	}
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks", playerToken, nil, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks/"+b.taskID, playerToken, nil, nil, http.StatusForbidden)

	// An unrelated private board stays invisible to an outsider.
	other := seedStaffReadBoard(t, f, "staff-read-other")
	outsider, outsiderToken := registerHTTPUser(t, f.ctx, f.users, f.db, "outsider", false)
	_ = outsider
	doJSON(t, f.router, http.MethodGet, other.modulePath(other.moduleID)+"/tasks", outsiderToken, nil, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet, other.modulePath(other.moduleID)+"/tasks/"+other.taskID, outsiderToken, nil, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet, other.modulePath(other.moduleID), outsiderToken, nil, nil, http.StatusForbidden)
}

// TestDemotedAdminLosesPreviewWithoutNewToken covers the stale-JWT direction:
// the authorizer re-reads users.roles, so a demoted admin's existing token must
// stop granting the staff preview.
func TestDemotedAdminLosesPreviewWithoutNewToken(t *testing.T) {
	f := newStaffReadHTTPFixture(t)
	b := seedStaffReadBoard(t, f, "staff-read-demoted")

	demoted, demotedToken := registerHTTPUser(t, f.ctx, f.users, f.db, "demoted_admin", true)
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks", demotedToken, nil, nil, http.StatusOK)

	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE users SET roles=ARRAY['user'] WHERE id=$1`, demoted.ID); err != nil {
		t.Fatalf("demote: %v", err)
	}
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks", demotedToken, nil, nil, http.StatusForbidden)
}

// TestParticipantAdminKeepsPreviewButNotOwnActions covers the mixed case: an
// admin who joined, then lost the access group, still reads the preview but
// cannot submit as a participant.
func TestParticipantAdminKeepsPreviewButNotOwnActions(t *testing.T) {
	f := newStaffReadHTTPFixture(t)
	b := seedStaffReadBoard(t, f, "staff-read-mixed")

	mixed, mixedToken := registerHTTPUser(t, f.ctx, f.users, f.db, "mixed_admin", false)
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)`, b.groupID, mixed.ID); err != nil {
		t.Fatalf("join group: %v", err)
	}
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+b.boardID+"/join", mixedToken,
		map[string]any{"code": issueJoinCode(t, f.router, b.boardID, b.adminToken)}, nil, http.StatusOK)
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE users SET roles=ARRAY['user','admin'] WHERE id=$1`, mixed.ID); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := f.db.Pool.Exec(f.ctx, `
		DELETE FROM access_group_members WHERE group_id=$1 AND user_id=$2`, b.groupID, mixed.ID); err != nil {
		t.Fatalf("revoke group: %v", err)
	}

	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks", mixedToken, nil, nil, http.StatusOK)
	doJSON(t, f.router, http.MethodPost, b.modulePath(b.moduleID)+"/tasks/"+b.taskID+"/submissions", mixedToken,
		map[string]any{"text": "flag{staff-preview-secret}"},
		map[string]string{"Idempotency-Key": "mixed-admin-submit-0001"}, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet,
		b.modulePath(b.moduleID)+"/tasks/"+b.taskID+"/submissions/me", mixedToken, nil, nil, http.StatusForbidden)
}

// doJSONStatus performs a request and returns the status without asserting it.
func doJSONStatus(t *testing.T, router *echo.Echo, method, path, token string, body any, headers map[string]string) int {
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
	return recorder.Code
}

func containsBytes(haystack []byte, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack []byte, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return i
		}
	}
	return -1
}
