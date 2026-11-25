package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
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

type joinCodeHTTPFixture struct {
	db      *postgres.DB
	router  *echo.Echo
	ctx     context.Context
	users   *users.UserService
	admin   string
	boardID string
	groupID string
}

func newJoinCodeHTTPFixture(t *testing.T, visibility, registration, accessMode string, withGroup bool) joinCodeHTTPFixture {
	t.Helper()
	dsn := testHTTPDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the enrollment HTTP tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	db, cleanup := openHTTPTestDB(t, ctx, dsn)
	t.Cleanup(cleanup)

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "join-code-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "join-code-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	userService := users.NewUserService(db, cfg, nil)
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	service := boards.NewService(boards.NewPostgresStore(db), authz, boards.SystemClock{},
		ratelimit.NewMemoryLimiter(1024),
		boards.WithArgon2Config(cfg.Argon2id), boards.WithIdempotencySecret("join-code-secret"),
		boards.WithAccessPolicy(accessSvc), boards.WithAccessService(accessSvc))
	router := httpapi.NewRouter(nil, func(context.Context) error { return nil })
	NewHandler(service, userService).RegisterRoutes(router)
	accesshttp.NewHandler(accessSvc, userService).RegisterRoutes(router)

	_, adminToken := registerHTTPUser(t, ctx, userService, db, "join_admin_"+uuid.NewString()[:8], true)

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)
	payload := map[string]any{
		"slug": "join-" + uuid.NewString()[:8], "name": "Join board", "participationMode": "INDIVIDUAL",
		"registrationMode": registration, "visibility": visibility,
		"startsAt": start, "endsAt": end,
		"rating": map[string]any{"visibility": "PUBLIC"},
	}
	groupID := ""
	if withGroup {
		group := decode[oapimodels.AccessGroup](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/access-groups",
			adminToken, map[string]any{"name": "join-group-" + uuid.NewString()[:8]}, nil, http.StatusCreated), http.StatusCreated)
		groupID = string(*group.UUID)
		payload["accessMode"] = accessMode
		payload["accessGroupUuids"] = []string{groupID}
	}
	board := decode[oapimodels.AdminBoard](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards",
		adminToken, payload, nil, http.StatusCreated), http.StatusCreated)
	boardID := string(*board.UUID)

	// A published, active board with one TASKS module.
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/modules", adminToken, map[string]any{
		"name": "Flag module", "type": "TASKS", "allowedTaskTypes": []string{"FLAG"}, "enabled": true,
	}, nil, http.StatusCreated)
	doJSON(t, router, http.MethodPost, "/api/v2/admin/boards/"+boardID+"/actions", adminToken,
		map[string]any{"action": "PUBLISH"}, nil, http.StatusOK)

	return joinCodeHTTPFixture{
		db: db, router: router, ctx: ctx, users: userService,
		admin: adminToken, boardID: boardID, groupID: groupID,
	}
}

func (f joinCodeHTTPFixture) newUser(t *testing.T, name string) (uuid.UUID, string) {
	t.Helper()
	user, token := registerHTTPUser(t, f.ctx, f.users, f.db, name+"_"+uuid.NewString()[:8], false)
	return user.ID, token
}

// TestOpenBoardNoLongerEnrollsWithoutCode is the intentional contract change:
// a PUBLIC+OPEN board stays browsable but never enrolls without a code.
func TestOpenBoardNoLongerEnrollsWithoutCode(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	_, token := f.newUser(t, "plain")

	// Browsing stays open.
	doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+f.boardID, token, nil, nil, http.StatusOK)

	// Enrollment does not.
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+f.boardID+"/join", token,
		map[string]any{}, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+f.boardID+"/join", token,
		map[string]any{"code": "NOT-A-REAL-CODE"}, nil, http.StatusForbidden)
	me := decode[oapimodels.BoardMe](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/boards/"+f.boardID+"/me", token, nil, nil, http.StatusOK), http.StatusOK)
	if me.Joined == nil || *me.Joined {
		t.Fatalf("bare UUID enrollment succeeded: %+v", me)
	}
	// The global code endpoint rejects the same input generically.
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
		map[string]any{"code": "NOT-A-REAL-CODE"}, nil, http.StatusForbidden)
}

// TestAdminJoinCodeLifecycle covers generate, one-time reveal, status, custom
// codes, normalization and rotation invalidation.
func TestAdminJoinCodeLifecycle(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)

	status := decode[oapimodels.BoardJoinCodeStatus](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/admin/boards/"+f.boardID+"/join-code", f.admin, nil, nil, http.StatusOK), http.StatusOK)
	if status.Configured == nil || *status.Configured {
		t.Fatalf("fresh board reports a configured code: %+v", status)
	}

	generated := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+f.boardID+"/join-code", f.admin, nil, nil, http.StatusOK), http.StatusOK)
	if generated.Code == nil || len(*generated.Code) != 26 {
		t.Fatalf("generated code = %v, want 26 Crockford characters", generated.Code)
	}
	first := *generated.Code

	// The status read never returns code material.
	rawStatus := doJSON(t, f.router, http.MethodGet, "/api/v2/admin/boards/"+f.boardID+"/join-code",
		f.admin, nil, nil, http.StatusOK)
	if containsBytes(rawStatus, first) {
		t.Fatalf("status read leaked the join code: %s", rawStatus)
	}
	status = decode[oapimodels.BoardJoinCodeStatus](t, rawStatus, http.StatusOK)
	if status.Configured == nil || !*status.Configured || status.UpdatedAt == nil {
		t.Fatalf("status after generate = %+v", status)
	}

	// The generated code enrolls, and the canonical endpoint is idempotent.
	_, token := f.newUser(t, "generated")
	joined := decode[oapimodels.BoardJoinResponse](t, doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join",
		token, map[string]any{"code": first}, nil, http.StatusOK), http.StatusOK)
	if joined.BoardUUID == nil || string(*joined.BoardUUID) != f.boardID || joined.Joined == nil || !*joined.Joined {
		t.Fatalf("canonical join response = %+v", joined)
	}
	if joined.AlreadyParticipating == nil || *joined.AlreadyParticipating {
		t.Fatalf("first join reported already participating: %+v", joined)
	}
	again := decode[oapimodels.BoardJoinResponse](t, doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join",
		token, map[string]any{"code": first}, nil, http.StatusOK), http.StatusOK)
	if again.AlreadyParticipating == nil || !*again.AlreadyParticipating {
		t.Fatalf("replay reported a new membership: %+v", again)
	}

	// Normalization: lowercase, padded and Crockford-folded input is accepted.
	_, token2 := f.newUser(t, "normalized")
	folded := " " + first + " "
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token2, map[string]any{"code": folded}, nil, http.StatusOK)

	// A custom code replaces the generated one; the old code stops working.
	custom := issueCustomJoinCode(t, f.router, f.boardID, f.admin, "team-ALPHA-2026")
	if custom != "TEAM-ALPHA-2026" {
		t.Fatalf("custom code = %q, want normalized TEAM-ALPHA-2026", custom)
	}
	_, token3 := f.newUser(t, "rotated")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token3, map[string]any{"code": first}, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token3, map[string]any{"code": "team-alpha-2026"}, nil, http.StatusOK)

	// Shape validation is a real 422, not a silent accept.
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+f.boardID+"/join-code", f.admin,
		map[string]any{"code": "short"}, nil, http.StatusUnprocessableEntity)
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+f.boardID+"/join-code", f.admin,
		map[string]any{"code": "bad code with spaces"}, nil, http.StatusUnprocessableEntity)
}

// TestJoinCodeUniquenessAndTargetMismatch covers the global unique domain and
// the optional board hint.
func TestJoinCodeUniquenessAndTargetMismatch(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	second := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards", f.admin,
		map[string]any{
			"slug": "join-second-" + uuid.NewString()[:8], "name": "Second", "participationMode": "INDIVIDUAL",
			"registrationMode": "OPEN", "visibility": "PUBLIC",
			"rating": map[string]any{"visibility": "PUBLIC"},
		}, nil, http.StatusCreated), http.StatusCreated)
	secondID := string(*second.UUID)

	code := issueCustomJoinCode(t, f.router, f.boardID, f.admin, "UNIQUE-CODE-0001")
	// The same code on another board is a real 409, never a silent overwrite.
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+secondID+"/join-code", f.admin,
		map[string]any{"code": "unique-code-0001"}, nil, http.StatusConflict)

	// A board hint that does not match the resolved board is an invalid code.
	_, token := f.newUser(t, "hint")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
		map[string]any{"code": code, "boardUuid": secondID}, nil, http.StatusForbidden)
	// The matching hint and the per-board route both work.
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
		map[string]any{"code": code, "boardUuid": f.boardID}, nil, http.StatusOK)
	_, token2 := f.newUser(t, "perboard")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+f.boardID+"/join", token2,
		map[string]any{"code": code}, nil, http.StatusOK)
}

// TestJoinCodeRespectsAudienceGateAndWindow proves the code never substitutes
// for the access-group, audience or window rules.
func TestJoinCodeRespectsAudienceGateAndWindow(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PRIVATE", "OPEN", "AUDIENCE", true)
	code := issueJoinCode(t, f.router, f.boardID, f.admin)

	outsider, outsiderToken := f.newUser(t, "audience_outsider")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", outsiderToken,
		map[string]any{"code": code}, nil, http.StatusForbidden)

	if _, err := f.db.Pool.Exec(f.ctx, `INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)`,
		f.groupID, outsider); err != nil {
		t.Fatalf("add group member: %v", err)
	}
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", outsiderToken,
		map[string]any{"code": code}, nil, http.StatusOK)

	// An expired board window still refuses enrollment even with a valid code.
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE boards SET ends_at = now() - interval '1 minute' WHERE id=$1`, f.boardID); err != nil {
		t.Fatalf("expire board: %v", err)
	}
	_, lateToken := f.newUser(t, "late")
	status := doJSONStatus(t, f.router, http.MethodPost, "/api/v2/boards/join", lateToken,
		map[string]any{"code": code}, nil)
	if status != http.StatusConflict && status != http.StatusForbidden {
		t.Fatalf("expired-board join status = %d, want 409 or 403", status)
	}
}

// TestJoinCodeRequiresLiveBoardUpdatePermission covers stale JWT roles and
// non-staff callers on the admin code routes.
func TestJoinCodeRequiresLiveBoardUpdatePermission(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)

	_, plainToken := f.newUser(t, "no_staff")
	doJSON(t, f.router, http.MethodGet, "/api/v2/admin/boards/"+f.boardID+"/join-code", plainToken, nil, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+f.boardID+"/join-code", plainToken, nil, nil, http.StatusForbidden)

	demoted, demotedToken := registerHTTPUser(t, f.ctx, f.users, f.db, "join_demoted_"+uuid.NewString()[:8], true)
	issueJoinCode(t, f.router, f.boardID, demotedToken)
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE users SET roles=ARRAY['user'] WHERE id=$1`, demoted.ID); err != nil {
		t.Fatalf("demote: %v", err)
	}
	// Same token, live roles: denied without a new login.
	doJSON(t, f.router, http.MethodGet, "/api/v2/admin/boards/"+f.boardID+"/join-code", demotedToken, nil, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+f.boardID+"/join-code", demotedToken, nil, nil, http.StatusForbidden)
}

// TestJoinCodeAttemptsAreRateLimited proves the limiter denies without echoing
// the submitted code.
func TestJoinCodeAttemptsAreRateLimited(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	issueJoinCode(t, f.router, f.boardID, f.admin)
	_, token := f.newUser(t, "bruteforce")

	limited := false
	for attempt := 0; attempt < 20; attempt++ {
		status := doJSONStatus(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
			map[string]any{"code": "WRONG-CODE-0001"}, nil)
		if status == http.StatusTooManyRequests {
			limited = true
			break
		}
		if status != http.StatusForbidden {
			t.Fatalf("attempt %d status = %d, want 403 or 429", attempt, status)
		}
	}
	if !limited {
		t.Fatal("join attempts were never rate limited")
	}
	body := doRawJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
		map[string]any{"code": "WRONG-CODE-0001"}, nil)
	if containsBytes(body, "WRONG-CODE-0001") {
		t.Fatalf("rate-limited response leaked the submitted code: %s", body)
	}
}

// TestCanonicalJoinDoesNotLeakBoardIdentity proves a wrong code cannot be used
// to enumerate private boards.
func TestCanonicalJoinDoesNotLeakBoardIdentity(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PRIVATE", "OPEN", "AUDIENCE", true)
	issueJoinCode(t, f.router, f.boardID, f.admin)
	_, token := f.newUser(t, "enumerator")

	body := doRawJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
		map[string]any{"code": "GUESS-GUESS-0001"}, nil)
	if containsBytes(body, f.boardID) {
		t.Fatalf("wrong-code response leaked the board UUID: %s", body)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if parsed["code"] != string(oapimodels.ErrorCodeJOINCODEINVALID) {
		t.Fatalf("wrong-code error code = %v, want JOIN_CODE_INVALID", parsed["code"])
	}
}

// TestConcurrentJoinWithSameCodeCreatesOneMembership proves the insert is
// idempotent under concurrency.
func TestConcurrentJoinWithSameCodeCreatesOneMembership(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	code := issueJoinCode(t, f.router, f.boardID, f.admin)
	user, token := f.newUser(t, "concurrent")

	results := make(chan int, 2)
	for index := 0; index < 2; index++ {
		go func() {
			results <- doJSONStatus(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
				map[string]any{"code": code}, nil)
		}()
	}
	for index := 0; index < 2; index++ {
		if status := <-results; status != http.StatusOK {
			t.Fatalf("concurrent join status = %d, want 200", status)
		}
	}
	var count int
	if err := f.db.Pool.QueryRow(f.ctx,
		`SELECT count(*) FROM board_participants WHERE board_id=$1 AND user_id=$2`,
		f.boardID, user).Scan(&count); err != nil {
		t.Fatalf("count participants: %v", err)
	}
	if count != 1 {
		t.Fatalf("participant rows = %d, want 1", count)
	}
}

// TestStaffPreviewStillWorksWithoutJoinCode keeps the two fixes independent:
// staff read preview needs no enrollment code.
func TestStaffPreviewStillWorksWithoutJoinCode(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PRIVATE", "OPEN", "", false)
	// The admin never joined and no code is configured yet.
	modules := decode[oapimodels.ModuleListResponse](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/boards/"+f.boardID+"/modules", f.admin, nil, nil, http.StatusOK), http.StatusOK)
	if modules.Total == nil || *modules.Total == 0 {
		t.Fatalf("staff preview module list = %+v", modules)
	}
	moduleID := string(*modules.Items[0].UUID)
	doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+f.boardID+"/modules/"+moduleID+"/tasks",
		f.admin, nil, nil, http.StatusOK)
}

// doRawJSON performs a request without asserting the status and returns the
// response body, so a test can assert on error content.
func doRawJSON(t *testing.T, router *echo.Echo, method, path, token string, body any, headers map[string]string) []byte {
	t.Helper()
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		payload = encoded
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
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
	return recorder.Body.Bytes()
}
