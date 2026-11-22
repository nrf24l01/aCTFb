package access

import (
	"context"
	"errors"
	"testing"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func TestBoardTeamSearchRequiresRosterPermissionAndStaysInBoard(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	authorizer := authorization.NewAuthorizer(db)
	svc := NewService(db, authorizer)
	ctx := context.Background()

	// Grant a live board_owner binding on the TEAM board; that role carries
	// board.participants.manage and board.audiences.manage.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1, $2, 'board_owner', $3)
	`, f.BoardTeam, f.User1, f.Admin); err != nil {
		t.Fatalf("grant board owner: %v", err)
	}
	owner := authorization.Principal{UserID: f.User1}
	page, err := svc.SearchBoardTeams(ctx, owner, f.BoardTeam, ListQuery{Query: "team-a"})
	if err != nil {
		t.Fatalf("owner team search: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].TeamID != f.TeamA {
		t.Fatalf("team search = %#v, want TeamA", page.Items)
	}

	// A team from another board must never appear.
	other, err := svc.SearchBoardTeams(ctx, owner, f.BoardPrivate, ListQuery{Query: "team-a"})
	if err != nil {
		t.Fatalf("cross-board team search: %v", err)
	}
	if len(other.Items) != 0 {
		t.Fatalf("cross-board team search leaked %#v", other.Items)
	}

	// An ordinary participant without a roster permission is refused.
	if _, err := svc.SearchBoardTeams(ctx, authorization.Principal{UserID: f.User3}, f.BoardTeam, ListQuery{Query: "team"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("participant team search err = %v, want forbidden", err)
	}

	// An empty term never dumps the board roster.
	empty, err := svc.SearchBoardTeams(ctx, owner, f.BoardTeam, ListQuery{})
	if err != nil {
		t.Fatalf("empty team search: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("empty team search returned %d teams", len(empty.Items))
	}
}

func TestAudienceManagerCanSearchBoardUsers(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	authorizer := authorization.NewAuthorizer(db)
	svc := NewService(db, authorizer)
	ctx := context.Background()

	// user-one is board_owner on BoardPrivate, which also satisfies audiences.manage.
	page, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User1}, f.BoardPrivate, ListQuery{Query: "user-two"})
	if err != nil {
		t.Fatalf("audience-scoped user search: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].UserID != f.User2 {
		t.Fatalf("audience-scoped search = %#v", page.Items)
	}
}

func TestCaptainRosterSearchIsCaptainOnlyAndHidesEmail(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	authorizer := authorization.NewAuthorizer(db)
	svc := NewService(db, authorizer)
	ctx := context.Background()

	// user-one captains TeamA on BoardTeam. user-three is a board participant
	// with no team, so it is eligible.
	captain := authorization.Principal{UserID: f.User1}
	page, err := svc.SearchCaptainRoster(ctx, captain, f.BoardTeam, ListQuery{Query: "user-three"})
	if err != nil {
		t.Fatalf("captain roster search: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].UserID != f.User3 {
		t.Fatalf("captain roster = %#v, want user-three", page.Items)
	}
	if page.Items[0].Username == "" {
		t.Fatal("captain roster must expose the username")
	}

	// Email matching works server-side but never discloses the address.
	byEmail, err := svc.SearchCaptainRoster(ctx, captain, f.BoardTeam, ListQuery{Query: "user-three@example.test"})
	if err != nil {
		t.Fatalf("captain email match: %v", err)
	}
	if len(byEmail.Items) != 1 || byEmail.Items[0].UserID != f.User3 {
		t.Fatalf("captain email match = %#v", byEmail.Items)
	}

	// A non-captain participant is refused even though it is a board member.
	if _, err := svc.SearchCaptainRoster(ctx, authorization.Principal{UserID: f.User3}, f.BoardTeam, ListQuery{Query: "user"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-captain roster err = %v, want forbidden", err)
	}

	// A captain of a different board cannot search this roster.
	if _, err := svc.SearchCaptainRoster(ctx, captain, f.BoardPrivate, ListQuery{Query: "user"}); err == nil {
		t.Fatal("captain of another board must not search a non-TEAM board roster")
	}

	// The read is audited and never stores the search term.
	var audited int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events WHERE action = 'user_search.captain_roster' AND board_id = $1
	`, f.BoardTeam).Scan(&audited); err != nil {
		t.Fatalf("count captain audit: %v", err)
	}
	if audited < 2 {
		t.Fatalf("captain roster audit rows = %d, want at least 2", audited)
	}
	var leaked int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = 'user_search.captain_roster' AND details::text LIKE '%user-three@example.test%'
	`).Scan(&leaked); err != nil {
		t.Fatalf("check audit leak: %v", err)
	}
	if leaked != 0 {
		t.Fatal("captain roster audit stored the search term")
	}

	// Already-teamed participants are excluded from the eligible roster.
	member, err := svc.SearchCaptainRoster(ctx, captain, f.BoardTeam, ListQuery{Query: "user-two"})
	if err != nil {
		t.Fatalf("search teamed participant: %v", err)
	}
	if len(member.Items) != 0 {
		t.Fatalf("already-teamed participant appeared in eligible roster: %#v", member.Items)
	}
}
