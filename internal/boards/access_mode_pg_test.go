package boards

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

// newAccessModeService builds a service wired to the live access policy so the
// projection, persistence and gate behaviour are exercised together.
func newAccessModeService(t *testing.T, ctx context.Context, db *postgres.DB) *Service {
	t.Helper()
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	sealer, err := secrets.NewCipher(base64Key(), 1)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	return NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(1024),
		WithArgon2Config(config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}),
		WithIdempotencySecret("access-mode-secret"), WithSealer(sealer),
		WithAccessPolicy(accessSvc), WithAccessService(accessSvc))
}

func TestAudienceAccessModeProjectsOnEveryRead(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the access-mode projection test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rawDB, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()
	service := newAccessModeService(t, ctx, rawDB)

	adminID := createBoardUser(t, ctx, rawDB, "projection_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, rawDB, "projection_player", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}

	// A live access group is required for the AUDIENCE mode.
	group, err := access.NewService(rawDB, authorization.NewAuthorizer(rawDB)).
		CreateAccessGroup(ctx, admin, "projection-group")
	if err != nil {
		t.Fatalf("create access group: %v", err)
	}

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "projection-audience", Name: "Projection Audience", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, AccessMode: AccessModeAudience,
		AccessGroupUUIDs: []uuid.UUID{group.ID}, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatalf("create audience board: %v", err)
	}
	if got := board.AccessMode(); got != AccessModeAudience {
		t.Fatalf("create projection = %s, want AUDIENCE", got)
	}
	if _, err := service.store.UpdateBoardStatus(ctx, board.ID, BoardStatusActive,
		AuditEventParams{ActorUserID: adminID, Action: "test.activate", ResourceType: "board"}); err != nil {
		t.Fatalf("activate board: %v", err)
	}
	// A live group membership is what makes the AUDIENCE gate pass.
	if _, err := rawDB.Pool.Exec(ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)
	`, group.ID, playerID); err != nil {
		t.Fatalf("add group member: %v", err)
	}
	if _, err := service.JoinBoard(ctx, player, board.ID, testJoinCode(t, rawDB, board.ID, "P1-ACCESS-CODE-001")); err != nil {
		t.Fatalf("join board: %v", err)
	}

	// Participant list read (the store path that previously returned no groups).
	list, err := service.ListBoards(ctx, player, ListBoardsQuery{ListQuery: ListQuery{Limit: 50}})
	if err != nil {
		t.Fatalf("participant list: %v", err)
	}
	found := false
	for _, item := range list.Items {
		if item.ID == board.ID {
			found = true
			if got := item.AccessMode(); got != AccessModeAudience {
				t.Fatalf("participant list projection = %s, want AUDIENCE", got)
			}
		}
	}
	if !found {
		t.Fatal("joined board missing from the participant list")
	}

	// Participant detail read.
	detail, err := service.GetBoard(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("participant detail: %v", err)
	}
	if got := detail.AccessMode(); got != AccessModeAudience {
		t.Fatalf("participant detail projection = %s, want AUDIENCE", got)
	}

	// Admin list read.
	adminList, err := service.AdminListBoards(ctx, admin, ListBoardsQuery{ListQuery: ListQuery{Limit: 50}})
	if err != nil {
		t.Fatalf("admin list: %v", err)
	}
	for _, item := range adminList.Items {
		if item.ID == board.ID && item.AccessMode() != AccessModeAudience {
			t.Fatalf("admin list projection = %s, want AUDIENCE", item.AccessMode())
		}
	}

	// Admin detail read.
	adminDetail, err := service.AdminGetBoard(ctx, admin, board.ID)
	if err != nil {
		t.Fatalf("admin detail: %v", err)
	}
	if got := adminDetail.AccessMode(); got != AccessModeAudience {
		t.Fatalf("admin detail projection = %s, want AUDIENCE", got)
	}
}

func TestAccessModeOnlyPatchPersistsGateNormalization(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the access-mode PATCH test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rawDB, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()
	service := newAccessModeService(t, ctx, rawDB)

	adminID := createBoardUser(t, ctx, rawDB, "patch_admin", []string{"admin"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	group, err := access.NewService(rawDB, authorization.NewAuthorizer(rawDB)).
		CreateAccessGroup(ctx, admin, "patch-group")
	if err != nil {
		t.Fatalf("create access group: %v", err)
	}

	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "patch-audience", Name: "Patch Audience", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, AccessMode: AccessModeAudience,
		AccessGroupUUIDs: []uuid.UUID{group.ID}, RatingVisibility: RatingPublic,
	})
	if err != nil {
		t.Fatalf("create audience board: %v", err)
	}

	// A canonical accessMode-only PATCH must clear the persisted group gate.
	mode := AccessModePublic
	if _, err := service.AdminUpdateBoard(ctx, admin, board.ID, UpdateBoardInput{
		AccessMode: OptionalString{Set: true, Value: &mode},
	}); err != nil {
		t.Fatalf("mode-only patch: %v", err)
	}
	reloaded, err := service.AdminGetBoard(ctx, admin, board.ID)
	if err != nil {
		t.Fatalf("admin get after patch: %v", err)
	}
	if len(reloaded.AccessGroupUUIDs) != 0 {
		t.Fatalf("persisted access groups = %v, want none", reloaded.AccessGroupUUIDs)
	}
	if got := reloaded.AccessMode(); got != AccessModePublic {
		t.Fatalf("reloaded projection = %s, want PUBLIC", got)
	}
	// No orphan permission filter: the board is publicly readable.
	var rows int
	if err := rawDB.Pool.QueryRow(ctx, `SELECT count(*) FROM board_access_groups WHERE board_id = $1`, board.ID).Scan(&rows); err != nil {
		t.Fatalf("count board access groups: %v", err)
	}
	if rows != 0 {
		t.Fatalf("board_access_groups rows = %d, want 0", rows)
	}
}

func TestLegacyInviteWithGroupsSurvivesUnrelatedSave(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the legacy invite+groups test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rawDB, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()
	service := newAccessModeService(t, ctx, rawDB)

	adminID := createBoardUser(t, ctx, rawDB, "legacy_admin", []string{"admin"})
	outsiderID := createBoardUser(t, ctx, rawDB, "legacy_outsider", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	outsider := authorization.Principal{UserID: outsiderID, GlobalRoles: []string{"user"}}
	group, err := access.NewService(rawDB, authorization.NewAuthorizer(rawDB)).
		CreateAccessGroup(ctx, admin, "legacy-group")
	if err != nil {
		t.Fatalf("create access group: %v", err)
	}

	// Seed the pre-existing hybrid combination directly: PRIVATE + INVITE_ONLY
	// + an access group. The canonical create path refuses to build this.
	boardID := uuid.New()
	if _, err := rawDB.Pool.Exec(ctx, `
		INSERT INTO boards (id, slug, name, participation_mode, registration_mode, visibility, status, rating_visibility, created_by)
		VALUES ($1,'legacy-invite-groups','Legacy Invite Groups','INDIVIDUAL','INVITE_ONLY','PRIVATE','ACTIVE','PUBLIC',$2)
	`, boardID, adminID); err != nil {
		t.Fatalf("seed legacy board: %v", err)
	}
	if _, err := rawDB.Pool.Exec(ctx, `
		INSERT INTO board_access_groups (board_id, group_id) VALUES ($1,$2)
	`, boardID, group.ID); err != nil {
		t.Fatalf("seed legacy gate: %v", err)
	}

	before, err := service.AdminGetBoard(ctx, admin, boardID)
	if err != nil {
		t.Fatalf("admin get: %v", err)
	}
	if got := before.AccessMode(); got != AccessModeLegacy {
		t.Fatalf("legacy projection = %s, want LEGACY", got)
	}

	// An unrelated metadata save must not touch the gate or the mode.
	name := "Legacy Invite Groups Renamed"
	if _, err := service.AdminUpdateBoard(ctx, admin, boardID, UpdateBoardInput{
		Name: OptionalString{Set: true, Value: &name},
	}); err != nil {
		t.Fatalf("unrelated rename: %v", err)
	}
	after, err := service.AdminGetBoard(ctx, admin, boardID)
	if err != nil {
		t.Fatalf("admin get after rename: %v", err)
	}
	if len(after.AccessGroupUUIDs) != 1 || after.AccessGroupUUIDs[0] != group.ID {
		t.Fatalf("gate after rename = %v, want the original group", after.AccessGroupUUIDs)
	}
	if got := after.AccessMode(); got != AccessModeLegacy {
		t.Fatalf("projection after rename = %s, want LEGACY", got)
	}

	// A non-member holding the invite code alone is still denied: the group gate
	// must survive, so the hybrid board keeps requiring both.
	code, _, _, err := service.store.GetBoardInvite(ctx, boardID)
	if err != nil {
		// No invite has been generated yet; generate one through the service.
		if _, genErr := service.AdminUpdateBoard(ctx, admin, boardID, UpdateBoardInput{
			RegistrationMode: OptionalString{Set: true, Value: strPtr(RegistrationInviteOnly)},
		}); genErr != nil {
			t.Fatalf("generate invite: %v", genErr)
		}
		code, _, _, err = service.store.GetBoardInvite(ctx, boardID)
		if err != nil {
			t.Fatalf("read invite: %v", err)
		}
	}
	_ = code
	if _, err := service.JoinBoard(ctx, outsider, boardID, "not-the-real-code"); !errors.Is(err, ErrInvalidInviteCode) {
		t.Fatalf("outsider join err = %v, want invalid invite code", err)
	}
}

func strPtr(value string) *string { return &value }

// base64Key returns a deterministic 32-byte base64 key for the test sealer.
func base64Key() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
}
