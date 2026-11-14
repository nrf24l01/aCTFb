package boards

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
)

// TestP1ListBoardsStoreRegression reproduces the adminListBoards 500: an admin
// request with no filters had two bind arguments but zero placeholders in the
// count query. It then exercises every filter/paging combination and participant
// / module-staff scoping through the real PostgreSQL store.
func TestP1ListBoardsStoreRegression(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the P1 ListBoards store regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	authz := authorization.NewAuthorizer(db)
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(128),
		WithArgon2Config(argon), WithIdempotencySecret("list-boards-regression-secret"))
	store := NewPostgresStore(db)

	adminID := createBoardUser(t, ctx, db, "list_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "list_player", []string{"user"})
	staffID := createBoardUser(t, ctx, db, "list_staff", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}

	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(24 * time.Hour)
	later := time.Now().UTC().Add(48 * time.Hour)
	laterEnd := time.Now().UTC().Add(72 * time.Hour)

	create := func(slug, name string, startsAt, endsAt *time.Time) *Board {
		board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
			Slug: slug, Name: name, ParticipationMode: ParticipationIndividual,
			RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
			StartsAt: startsAt, EndsAt: endsAt,
		})
		if err != nil {
			t.Fatalf("create board %s: %v", slug, err)
		}
		return board
	}
	createModule := func(board *Board) *Module {
		module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
			Name: "Tasks", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
		})
		if err != nil {
			t.Fatalf("create module for %s: %v", board.Slug, err)
		}
		return module
	}

	alpha := create("list-alpha", "Alpha Active", &past, &future)
	createModule(alpha)
	if _, err := service.AdminBoardAction(ctx, admin, alpha.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish alpha: %v", err)
	}
	beta := create("list-beta", "Beta Scheduled", &later, &laterEnd)
	createModule(beta)
	if _, err := service.AdminBoardAction(ctx, admin, beta.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish beta: %v", err)
	}
	gamma := create("list-gamma", "Gamma Active", &past, &future)
	createModule(gamma)
	if _, err := service.AdminBoardAction(ctx, admin, gamma.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish gamma: %v", err)
	}
	delta := create("list-delta", "Delta No Module", &past, &future)
	if _, err := db.Pool.Exec(ctx, `UPDATE boards SET status='ACTIVE', participation_mode='TEAM' WHERE id=$1`, delta.ID); err != nil {
		t.Fatalf("prepare delta: %v", err)
	}
	fifth := create("list-fifth", "Fifth Active", &past, &future)
	createModule(fifth)
	if _, err := service.AdminBoardAction(ctx, admin, fifth.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish fifth: %v", err)
	}

	if _, err := service.JoinBoard(ctx, player, alpha.ID); err != nil {
		t.Fatalf("join alpha: %v", err)
	}
	grantBinding(t, ctx, db, gamma.ID, nil, staffID, adminID, "board_editor", nil)

	// This exact call returned 500 before the placeholder fix.
	boards, total, err := store.ListBoards(ctx, BoardFilter{Limit: 50, Offset: 0, Admin: true, UserID: adminID})
	if err != nil {
		t.Fatalf("admin unfiltered ListBoards: %v", err)
	}
	if total != 5 || len(boards) != 5 {
		t.Fatalf("admin unfiltered total=%d len=%d, want 5/5", total, len(boards))
	}

	assertList := func(name string, filter BoardFilter, wantTotal int) []Board {
		t.Helper()
		items, count, err := store.ListBoards(ctx, filter)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if count != wantTotal || len(items) != wantTotal {
			t.Fatalf("%s count=%d len=%d, want %d", name, count, len(items), wantTotal)
		}
		return items
	}

	active := assertList("admin status filter", BoardFilter{
		Limit: 50, Offset: 0, Admin: true, Status: string(BoardStatusActive), UserID: adminID,
	}, 4)
	for _, board := range active {
		if board.Status != BoardStatusActive {
			t.Fatalf("status filter returned %s", board.Status)
		}
	}

	assertList("admin module filter", BoardFilter{
		Limit: 50, Offset: 0, Admin: true, ModuleType: ModuleTypeTasks, UserID: adminID,
	}, 4)
	assertList("admin query filter", BoardFilter{
		Limit: 50, Offset: 0, Admin: true, Query: "Alpha", UserID: adminID,
	}, 1)
	assertList("admin participation filter", BoardFilter{
		Limit: 50, Offset: 0, Admin: true, ParticipationMode: ParticipationIndividual, UserID: adminID,
	}, 4)
	assertList("admin team filter", BoardFilter{
		Limit: 50, Offset: 0, Admin: true, ParticipationMode: ParticipationTeam, UserID: adminID,
	}, 1)

	page, pageTotal, err := store.ListBoards(ctx, BoardFilter{Limit: 2, Offset: 1, Admin: true, UserID: adminID})
	if err != nil {
		t.Fatalf("admin paging: %v", err)
	}
	if pageTotal != 5 || len(page) != 2 {
		t.Fatalf("admin paging total=%d len=%d, want 5/2", pageTotal, len(page))
	}

	participantBoards := assertList("participant scoped", BoardFilter{
		Limit: 50, Offset: 0, UserID: playerID,
	}, 1)
	if participantBoards[0].ID != alpha.ID || !participantBoards[0].Joined {
		t.Fatalf("participant board = %+v, want joined alpha", participantBoards[0])
	}

	staffBoards := assertList("staff scoped", BoardFilter{
		Limit: 50, Offset: 0, UserID: staffID,
	}, 1)
	if staffBoards[0].ID != gamma.ID || staffBoards[0].Joined {
		t.Fatalf("staff board = %+v, want unjoined assigned gamma", staffBoards[0])
	}

	// No sibling/other-board data leaks through scoped filters.
	for _, board := range staffBoards {
		if board.ID == alpha.ID || board.ID == beta.ID || board.ID == delta.ID || board.ID == fifth.ID {
			t.Fatalf("staff received unrelated board %s", board.Slug)
		}
	}

	// Participant + status exercises the combined placeholder path.
	assertList("participant status filter", BoardFilter{
		Limit: 50, Offset: 0, UserID: playerID, Status: string(BoardStatusActive),
	}, 1)
	// Admin status + paging exercises count/list placeholder reuse.
	_, total, err = store.ListBoards(ctx, BoardFilter{
		Limit: 1, Offset: 0, Admin: true, UserID: adminID, Status: string(BoardStatusActive),
	})
	if err != nil || total != 4 {
		t.Fatalf("admin status paging total=%d err=%v, want 4", total, err)
	}
}

func testDSN() string {
	if value := os.Getenv("ACTFB_TEST_POSTGRES_DSN"); value != "" {
		return value
	}
	return os.Getenv("ACTFB_TEST_DATABASE_URL")
}
