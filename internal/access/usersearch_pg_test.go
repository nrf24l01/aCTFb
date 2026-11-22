package access

import (
	"context"
	"errors"
	"testing"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func TestSearchUsersMatchesUUIDUsernameAndEmailLiterally(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	svc := NewService(db, nil)
	admin := authorization.Principal{UserID: f.Admin}

	byUsername, err := svc.SearchUsers(context.Background(), admin, ListQuery{Query: "USER-ONE"})
	if err != nil {
		t.Fatalf("search by username: %v", err)
	}
	if len(byUsername.Items) != 1 || byUsername.Items[0].UserID != f.User1 {
		t.Fatalf("username search = %#v, want user-one", byUsername.Items)
	}

	byEmail, err := svc.SearchUsers(context.Background(), admin, ListQuery{Query: "user-two@example.test"})
	if err != nil {
		t.Fatalf("search by email: %v", err)
	}
	if len(byEmail.Items) != 1 || byEmail.Items[0].UserID != f.User2 {
		t.Fatalf("email search = %#v, want user-two", byEmail.Items)
	}

	byUUID, err := svc.SearchUsers(context.Background(), admin, ListQuery{Query: f.User3.String()})
	if err != nil {
		t.Fatalf("search by uuid: %v", err)
	}
	if len(byUUID.Items) != 1 || byUUID.Items[0].UserID != f.User3 {
		t.Fatalf("uuid search = %#v, want user-three", byUUID.Items)
	}
}

func TestSearchUsersEscapesWildcardsAndPaginates(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	svc := NewService(db, nil)
	admin := authorization.Principal{UserID: f.Admin}
	ctx := context.Background()

	// A bare percent must be a literal, not a match-all wildcard.
	page, err := svc.SearchUsers(ctx, admin, ListQuery{Query: "%"})
	if err != nil {
		t.Fatalf("search wildcard: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("literal %% search matched %d users", len(page.Items))
	}

	first, err := svc.SearchUsers(ctx, admin, ListQuery{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 2 || first.Total < 5 {
		t.Fatalf("first page = %#v total=%d", first.Items, first.Total)
	}
	second, err := svc.SearchUsers(ctx, admin, ListQuery{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 2 {
		t.Fatalf("second page = %#v", second.Items)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.UserID.String()] {
			t.Fatalf("duplicate user across pages: %s", item.UserID)
		}
		seen[item.UserID.String()] = true
	}
}

func TestSearchUsersRejectsNonAdminAndDisabled(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	svc := NewService(db, nil)
	ctx := context.Background()

	if _, err := svc.SearchUsers(ctx, authorization.Principal{UserID: f.User1}, ListQuery{Query: "user"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("normal user search err = %v, want forbidden", err)
	}
	if _, err := svc.SearchUsers(ctx, authorization.Principal{UserID: f.Disabled}, ListQuery{Query: "user"}); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled search err = %v, want disabled", err)
	}
}

func TestBoardUserSearchRequiresLiveParticipantsManage(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	authorizer := authorization.NewAuthorizer(db)
	svc := NewService(db, authorizer)
	ctx := context.Background()

	// user-one is a board_owner on BoardPrivate, so the scoped permission is live.
	owner := authorization.Principal{UserID: f.User1}
	page, err := svc.SearchBoardUsers(ctx, owner, f.BoardPrivate, "PARTICIPANTS", ListQuery{Query: "user-three"})
	if err != nil {
		t.Fatalf("board search as owner: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].UserID != f.User3 {
		t.Fatalf("board search = %#v, want user-three", page.Items)
	}
	var audited int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events WHERE action = 'user_search.board' AND board_id = $1
	`, f.BoardPrivate).Scan(&audited); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audited == 0 {
		t.Fatal("board user search was not audited")
	}

	// An empty board-scoped query must not dump the directory.
	empty, err := svc.SearchBoardUsers(ctx, owner, f.BoardPrivate, "PARTICIPANTS", ListQuery{})
	if err != nil {
		t.Fatalf("empty board search: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("empty board search returned %d users", len(empty.Items))
	}

	// An outsider participant cannot enumerate users.
	if _, err := svc.SearchBoardUsers(ctx, authorization.Principal{UserID: f.User3}, f.BoardPrivate, "PARTICIPANTS", ListQuery{Query: "user"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider board search err = %v, want forbidden", err)
	}
}

func TestAccessGroupMembersAddListRemove(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	svc := NewService(db, nil)
	admin := authorization.Principal{UserID: f.Admin}
	ctx := context.Background()

	if err := svc.AddAccessGroupMember(ctx, admin, f.Group1, f.User3); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := svc.AddAccessGroupMember(ctx, admin, f.Group1, f.User3); !errors.Is(err, ErrAccessGroupMemberExists) {
		t.Fatalf("duplicate add err = %v, want member exists", err)
	}
	page, err := svc.ListAccessGroupMembers(ctx, admin, f.Group1, ListQuery{})
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	found := false
	for _, member := range page.Items {
		if member.UserID == f.User3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("added member missing from %#v", page.Items)
	}
	if err := svc.RemoveAccessGroupMember(ctx, admin, f.Group1, f.User3); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if err := svc.RemoveAccessGroupMember(ctx, admin, f.Group1, f.User3); !errors.Is(err, ErrAccessGroupMemberNotFound) {
		t.Fatalf("second remove err = %v, want member not found", err)
	}
}
