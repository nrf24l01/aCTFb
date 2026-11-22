package access

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

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
	page, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User1}, f.BoardPrivate, "PARTICIPANTS", ListQuery{Query: "user-two"})
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

// TestBoardUserSearchScopeIsExplicit is the I7 guard: the default scope only
// returns current board participants, an outside user is invisible to it, and
// the broader directory scope needs participant/staff management.
func TestBoardUserSearchScopeIsExplicit(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	authorizer := authorization.NewAuthorizer(db)
	svc := NewService(db, authorizer)
	ctx := context.Background()

	// The fixture already makes user-one board_owner on BoardPrivate, which
	// carries participants + audiences management.
	// An outsider is a real user but not a participant of BoardPrivate.
	outsideID := insertAccessUser(t, db, "outside-participant", false, false)

	// Default scope (empty string) is PARTICIPANTS and never returns the outsider.
	defaultScope, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User1}, f.BoardPrivate, "", ListQuery{Query: "outside-participant"})
	if err != nil {
		t.Fatalf("default scope search: %v", err)
	}
	if len(defaultScope.Items) != 0 {
		t.Fatalf("default scope returned an outside user: %#v", defaultScope.Items)
	}
	participantScope, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User1}, f.BoardPrivate, "PARTICIPANTS", ListQuery{Query: "outside-participant"})
	if err != nil {
		t.Fatalf("participants scope search: %v", err)
	}
	if len(participantScope.Items) != 0 {
		t.Fatalf("PARTICIPANTS scope returned an outside user: %#v", participantScope.Items)
	}

	// A real participant is found by the participant scope.
	inside, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User1}, f.BoardPrivate, "PARTICIPANTS", ListQuery{Query: "user-two"})
	if err != nil {
		t.Fatalf("participant search: %v", err)
	}
	if len(inside.Items) != 1 || inside.Items[0].UserID != f.User2 {
		t.Fatalf("participant search = %#v, want user-two", inside.Items)
	}

	// DIRECTORY is the add-a-participant path and does see the outsider.
	directory, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User1}, f.BoardPrivate, "DIRECTORY", ListQuery{Query: "outside-participant"})
	if err != nil {
		t.Fatalf("directory search: %v", err)
	}
	if len(directory.Items) != 1 || directory.Items[0].UserID != outsideID {
		t.Fatalf("directory search = %#v, want the outside user", directory.Items)
	}

	// An invalid scope is rejected rather than silently defaulting.
	if _, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User1}, f.BoardPrivate, "EVERYONE", ListQuery{Query: "x"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid scope err = %v, want invalid input", err)
	}
}

// TestAudienceOnlyManagerCannotUseDirectoryScope is the I7 authorization edge:
// audiences.manage is enough for the participant roster but not for the global
// directory.
func TestAudienceOnlyManagerCannotUseDirectoryScope(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	authorizer := authorization.NewAuthorizer(db)
	svc := NewService(db, authorizer)
	ctx := context.Background()

	// A reviewer binding carries no participant/staff management, but grant the
	// audience permission explicitly so only that one permission is present.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1, $2, 'reviewer', $3)
	`, f.BoardPrivate, f.User3, f.Admin); err != nil {
		t.Fatalf("grant reviewer: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO permission_grants (user_id, permission, board_id, granted_by)
		VALUES ($1, 'board.audiences.manage', $2, $3)
	`, f.User3, f.BoardPrivate, f.Admin); err != nil {
		t.Fatalf("grant audiences permission: %v", err)
	}

	// The participant roster is allowed for an audience manager.
	if _, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User3}, f.BoardPrivate, "PARTICIPANTS", ListQuery{Query: "user-two"}); err != nil {
		t.Fatalf("audience manager participant search: %v", err)
	}
	// The global directory is not.
	if _, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User3}, f.BoardPrivate, "DIRECTORY", ListQuery{Query: "user-two"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("audience manager directory err = %v, want forbidden", err)
	}
}

// TestBoardTeamSearchExcludesEmptyTeams keeps the picker from offering a team
// the server would reject as an audience subject.
func TestBoardTeamSearchExcludesEmptyTeams(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	authorizer := authorization.NewAuthorizer(db)
	svc := NewService(db, authorizer)
	ctx := context.Background()

	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1, $2, 'board_owner', $3)
	`, f.BoardTeam, f.User1, f.Admin); err != nil {
		t.Fatalf("grant board owner: %v", err)
	}
	// An empty team in the same board.
	var emptyTeam uuid.UUID
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO teams (board_id, name, captain_user_id) VALUES ($1,'empty-team',$2) RETURNING id
	`, f.BoardTeam, f.User1).Scan(&emptyTeam); err != nil {
		t.Fatalf("insert empty team: %v", err)
	}

	page, err := svc.SearchBoardTeams(ctx, authorization.Principal{UserID: f.User1}, f.BoardTeam, ListQuery{Query: "team"})
	if err != nil {
		t.Fatalf("team search: %v", err)
	}
	for _, item := range page.Items {
		if item.TeamID == emptyTeam {
			t.Fatal("team search offered an empty team")
		}
	}
	if len(page.Items) == 0 {
		t.Fatal("team search returned no eligible team")
	}
}

// TestCaptainRosterRevocationIsImmediate proves the captain roster uses live
// state: a revoked group member and a transferred captain both lose access on
// the next call.
func TestCaptainRosterRevocationIsImmediate(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	authorizer := authorization.NewAuthorizer(db)
	svc := NewService(db, authorizer)
	ctx := context.Background()

	// Put the TEAM board behind an access group so membership is a live gate.
	group := insertGroup(t, db, "captain-gate")
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_access_groups (board_id, group_id) VALUES ($1,$2)
	`, f.BoardTeam, group); err != nil {
		t.Fatalf("gate team board: %v", err)
	}
	for _, userID := range []uuid.UUID{f.User1, f.User2, f.User3} {
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)
			ON CONFLICT DO NOTHING
		`, group, userID); err != nil {
			t.Fatalf("add group member: %v", err)
		}
	}

	captain := authorization.Principal{UserID: f.User1}
	if _, err := svc.SearchCaptainRoster(ctx, captain, f.BoardTeam, ListQuery{Query: "user-three"}); err != nil {
		t.Fatalf("captain roster before revocation: %v", err)
	}

	// Revoking the captain's group membership denies the roster immediately.
	if _, err := db.Pool.Exec(ctx, `
		DELETE FROM access_group_members WHERE group_id = $1 AND user_id = $2
	`, group, f.User1); err != nil {
		t.Fatalf("revoke group membership: %v", err)
	}
	if _, err := svc.SearchCaptainRoster(ctx, captain, f.BoardTeam, ListQuery{Query: "user-three"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("roster after group revocation err = %v, want forbidden", err)
	}

	// Restore the gate, then transfer captaincy: the old captain loses it.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)
	`, group, f.User1); err != nil {
		t.Fatalf("restore group membership: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE teams SET captain_user_id = $2 WHERE board_id = $1 AND captain_user_id = $3
	`, f.BoardTeam, f.User2, f.User1); err != nil {
		t.Fatalf("transfer captaincy: %v", err)
	}
	if _, err := svc.SearchCaptainRoster(ctx, captain, f.BoardTeam, ListQuery{Query: "user-three"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("roster after captain transfer err = %v, want forbidden", err)
	}
	newCaptain := authorization.Principal{UserID: f.User2}
	if _, err := svc.SearchCaptainRoster(ctx, newCaptain, f.BoardTeam, ListQuery{Query: "user-three"}); err != nil {
		t.Fatalf("new captain roster: %v", err)
	}
}
