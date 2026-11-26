package http

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	accesshttp "github.com/nrf24l01/actfb/internal/access/http"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/users"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

// noSealerBoardFixture is the default production profile for this bug: every
// optional feature (deployment, plugins, files, integrations, A+D) is disabled,
// so `deployment.credential_key` is empty and no credential sealer exists.
// Board creation must still work, because the canonical join code is hash-only.
type noSealerBoardFixture struct {
	db     *postgres.DB
	router *echo.Echo
	ctx    context.Context
	users  *users.UserService
	admin  string
}

func newNoSealerBoardFixture(t *testing.T) noSealerBoardFixture {
	t.Helper()
	return newNoSealerBoardFixtureWith(t, nil)
}

// newNoSealerBoardFixtureWith allows the logging tests to capture the router's
// structured output; every other test keeps the discarding default.
func newNoSealerBoardFixtureWith(t *testing.T, logger *slog.Logger) noSealerBoardFixture {
	t.Helper()
	dsn := testHTTPDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the board save HTTP tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	db, cleanup := openHTTPTestDB(t, ctx, dsn)
	t.Cleanup(cleanup)

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "board-save-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "board-save-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	userService := users.NewUserService(db, cfg, nil)
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	// No boards.WithSealer: exactly what boot does when credential_key is empty.
	service := boards.NewService(boards.NewPostgresStore(db), authz, boards.SystemClock{},
		ratelimit.NewMemoryLimiter(1024),
		boards.WithArgon2Config(cfg.Argon2id), boards.WithIdempotencySecret("board-save-secret"),
		boards.WithAccessPolicy(accessSvc), boards.WithAccessService(accessSvc),
		boards.WithOutbox(events.NewOutbox(db)))
	router := httpapi.NewRouter(logger, func(context.Context) error { return nil })
	NewHandler(service, userService).RegisterRoutes(router)
	accesshttp.NewHandler(accessSvc, userService).RegisterRoutes(router)

	_, adminToken := registerHTTPUser(t, ctx, userService, db, "board_save_admin_"+uuid.NewString()[:8], true)
	return noSealerBoardFixture{db: db, router: router, ctx: ctx, users: userService, admin: adminToken}
}

func (f noSealerBoardFixture) createBoard(t *testing.T, body map[string]any, wantStatus int) []byte {
	t.Helper()
	return doJSON(t, f.router, "POST", "/api/v2/admin/boards", f.admin, body, nil, wantStatus)
}

func (f noSealerBoardFixture) newUser(t *testing.T, name string) string {
	t.Helper()
	_, token := registerHTTPUser(t, f.ctx, f.users, f.db, name+"_"+uuid.NewString()[:8], false)
	return token
}

func inviteBoardPayload(slug string) map[string]any {
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)
	return map[string]any{
		"slug": slug, "name": "Invite board", "participationMode": "INDIVIDUAL",
		"accessMode": "INVITE", "registrationMode": "INVITE_ONLY", "visibility": "PRIVATE",
		"startsAt": start, "endsAt": end, "rating": map[string]any{"visibility": "PUBLIC"},
	}
}

// TestBoardCreateInviteModeWithoutCredentialKey is the exact production
// reproduction: an INVITE board created on the default profile (no credential
// key) used to answer 503 DEPENDENCY_UNAVAILABLE, because board creation still
// auto-generated a legacy encrypted invite code. The canonical join code is
// hash-only, so creation must succeed and the code must be issued explicitly.
func TestBoardCreateInviteModeWithoutCredentialKey(t *testing.T) {
	f := newNoSealerBoardFixture(t)

	body := f.createBoard(t, inviteBoardPayload("invite-no-key-"+uuid.NewString()[:8]), 201)
	board := decode[oapimodels.AdminBoard](t, body, 201)
	boardID := string(*board.UUID)
	if board.Status == nil || *board.Status != "DRAFT" {
		t.Fatalf("status = %v, want DRAFT", board.Status)
	}
	if board.AccessMode != "INVITE" {
		t.Fatalf("accessMode = %v, want INVITE", board.AccessMode)
	}

	// No legacy encrypted invite is auto-generated any more.
	var legacyHash, legacyCipher *string
	if err := f.db.Pool.QueryRow(f.ctx,
		`SELECT invite_code_hash, encode(invite_code_ciphertext,'escape') FROM boards WHERE id=$1`,
		boardID).Scan(&legacyHash, &legacyCipher); err != nil {
		t.Fatalf("read legacy invite columns: %v", err)
	}
	if legacyHash != nil || legacyCipher != nil {
		t.Fatalf("create auto-generated a legacy invite: hash=%v cipher=%v", legacyHash, legacyCipher)
	}

	// The organizer issues the canonical code explicitly, from a fresh board.
	status := decode[oapimodels.BoardJoinCodeStatus](t, doJSON(t, f.router, "GET",
		"/api/v2/admin/boards/"+boardID+"/join-code", f.admin, nil, nil, 200), 200)
	if status.Configured == nil || *status.Configured {
		t.Fatalf("fresh board reports a configured code: %+v", status)
	}
	code := issueJoinCode(t, f.router, boardID, f.admin)

	// The canonical status now reports a configured code, still without any
	// legacy credential or deployment key.
	after := decode[oapimodels.BoardJoinCodeStatus](t, doJSON(t, f.router, "GET",
		"/api/v2/admin/boards/"+boardID+"/join-code", f.admin, nil, nil, 200), 200)
	if after.Configured == nil || !*after.Configured || after.Revision == nil || *after.Revision < 1 {
		t.Fatalf("status after issuing a code = %+v", after)
	}

	// Publish and enroll a real participant with the canonical code only.
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE boards SET status='ACTIVE' WHERE id=$1`, boardID); err != nil {
		t.Fatalf("activate board: %v", err)
	}
	participant := f.newUser(t, "invite_participant")
	joined := decode[oapimodels.BoardJoinResponse](t, doJSON(t, f.router, "POST", "/api/v2/boards/join",
		participant, map[string]any{"code": code}, nil, 200), 200)
	if joined.BoardUUID == nil || string(*joined.BoardUUID) != boardID {
		t.Fatalf("join resolved the wrong board: %+v", joined)
	}
	me := decode[oapimodels.BoardMe](t, doJSON(t, f.router, "GET",
		"/api/v2/boards/"+boardID+"/me", participant, nil, nil, 200), 200)
	if me.Joined == nil || !*me.Joined {
		t.Fatalf("participant is not a member after a valid code: %+v", me)
	}

	// The per-board route accepts the same canonical code, and a second
	// participant enrolls through it.
	second := f.newUser(t, "invite_second")
	doJSON(t, f.router, "POST", "/api/v2/boards/"+boardID+"/join", second,
		map[string]any{"code": code}, nil, 200)

	// A wrong code stays generic and never enrolls.
	outsider := f.newUser(t, "invite_outsider")
	doJSON(t, f.router, "POST", "/api/v2/boards/join", outsider,
		map[string]any{"code": "NOT-A-REAL-CODE-0001"}, nil, 403)
}

// TestBoardCreateAllAccessModesWithoutSealer pins that no access mode needs a
// credential key, and that AUDIENCE keeps its group gate.
func TestBoardCreateAllAccessModesWithoutSealer(t *testing.T) {
	f := newNoSealerBoardFixture(t)

	group := decode[oapimodels.AccessGroup](t, doJSON(t, f.router, "POST", "/api/v2/admin/access-groups",
		f.admin, map[string]any{"name": "board-save-group-" + uuid.NewString()[:8]}, nil, 201), 201)
	groupID := string(*group.UUID)

	cases := []struct {
		name string
		body map[string]any
		mode string
	}{
		{"public", map[string]any{
			"slug": "save-public-" + uuid.NewString()[:8], "name": "Public", "participationMode": "INDIVIDUAL",
			"accessMode": "PUBLIC", "registrationMode": "OPEN", "visibility": "PUBLIC",
			"accessGroupUuids": []string{}, "rating": map[string]any{"visibility": "PUBLIC"},
		}, "PUBLIC"},
		{"audience", map[string]any{
			"slug": "save-audience-" + uuid.NewString()[:8], "name": "Audience", "participationMode": "INDIVIDUAL",
			"accessMode": "AUDIENCE", "registrationMode": "OPEN", "visibility": "PRIVATE",
			"accessGroupUuids": []string{groupID}, "rating": map[string]any{"visibility": "PUBLIC"},
		}, "AUDIENCE"},
		{"invite", inviteBoardPayload("save-invite-" + uuid.NewString()[:8]), "INVITE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			board := decode[oapimodels.AdminBoard](t, f.createBoard(t, tc.body, 201), 201)
			if string(board.AccessMode) != tc.mode {
				t.Fatalf("accessMode = %v, want %s", board.AccessMode, tc.mode)
			}
			if tc.mode == "AUDIENCE" && len(board.AccessGroupUuids) != 1 {
				t.Fatalf("audience gate = %v, want the selected group", board.AccessGroupUuids)
			}
		})
	}
}

// TestBoardUpdateToInviteModeWithoutSealer covers the same obsolete auto-seal on
// the update path, plus legacy ciphertext preservation for pre-existing boards.
func TestBoardUpdateToInviteModeWithoutSealer(t *testing.T) {
	f := newNoSealerBoardFixture(t)

	created := decode[oapimodels.AdminBoard](t, f.createBoard(t, map[string]any{
		"slug": "save-switch-" + uuid.NewString()[:8], "name": "Switch", "participationMode": "INDIVIDUAL",
		"accessMode": "PUBLIC", "registrationMode": "OPEN", "visibility": "PUBLIC",
		"accessGroupUuids": []string{}, "rating": map[string]any{"visibility": "PUBLIC"},
	}, 201), 201)
	boardID := string(*created.UUID)

	updated := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, "PATCH", "/api/v2/admin/boards/"+boardID,
		f.admin, map[string]any{
			"accessMode": "INVITE", "registrationMode": "INVITE_ONLY", "visibility": "PRIVATE",
			"accessGroupUuids": []string{},
		}, nil, 200), 200)
	if updated.AccessMode != "INVITE" {
		t.Fatalf("accessMode after switch = %v, want INVITE", updated.AccessMode)
	}
	var legacyHash *string
	if err := f.db.Pool.QueryRow(f.ctx,
		`SELECT invite_code_hash FROM boards WHERE id=$1`, boardID).Scan(&legacyHash); err != nil {
		t.Fatalf("read legacy invite hash: %v", err)
	}
	if legacyHash != nil {
		t.Fatalf("switching to INVITE auto-generated a legacy invite: %v", *legacyHash)
	}

	// A pre-020 board keeps its sealed legacy ciphertext through an unrelated
	// metadata save; the fix must not delete or rewrite legacy material.
	if _, err := f.db.Pool.Exec(f.ctx,
		`UPDATE boards SET invite_code_hash=$2, invite_code_ciphertext=$3 WHERE id=$1`,
		boardID, "legacy-hash-value", []byte("legacy-ciphertext")); err != nil {
		t.Fatalf("seed legacy invite: %v", err)
	}
	name := "Switch renamed"
	doJSON(t, f.router, "PATCH", "/api/v2/admin/boards/"+boardID, f.admin,
		map[string]any{"name": name}, nil, 200)
	var cipher string
	if err := f.db.Pool.QueryRow(f.ctx,
		`SELECT invite_code_hash, encode(invite_code_ciphertext,'escape') FROM boards WHERE id=$1`,
		boardID).Scan(&legacyHash, &cipher); err != nil {
		t.Fatalf("read legacy invite after rename: %v", err)
	}
	if legacyHash == nil || *legacyHash != "legacy-hash-value" || cipher != "legacy-ciphertext" {
		t.Fatalf("legacy invite changed by a metadata save: hash=%v cipher=%q", legacyHash, cipher)
	}
}
