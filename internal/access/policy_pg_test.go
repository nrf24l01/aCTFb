package access

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func TestPolicyAccessGroupsAudiencesAndJoin(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	authz := authorization.NewAuthorizer(db)
	svc := NewService(db, authz)
	ctx := context.Background()

	principal := func(id uuid.UUID) authorization.Principal { return authorization.Principal{UserID: id} }

	// PRIVATE board has two groups; membership in either group is sufficient
	// (OR, not AND).
	for name, userID := range map[string]uuid.UUID{"user-one": f.User1, "user-two": f.User2} {
		allowed, err := svc.CanReadBoard(ctx, principal(userID), f.BoardPrivate)
		if err != nil || !allowed {
			t.Fatalf("%s CanReadBoard = %v, %v; want true", name, allowed, err)
		}
	}
	if allowed, err := svc.CanReadBoard(ctx, principal(f.User3), f.BoardPrivate); err != nil || allowed {
		t.Fatalf("user-three CanReadBoard = %v, %v; want false", allowed, err)
	}
	if allowed, err := svc.CanReadBoard(ctx, principal(f.Admin), f.BoardPrivate); err != nil || !allowed {
		t.Fatalf("admin CanReadBoard = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanReadBoard(ctx, principal(f.User3), f.BoardPublic); err != nil || !allowed {
		t.Fatalf("public CanReadBoard = %v, %v; want true", allowed, err)
	}

	// Live staff who are not participants may read for management.
	staff := insertAccessUser(t, db, "staff-only", false, false)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO board_role_bindings (board_id,user_id,role,granted_by) VALUES ($1,$2,'board_owner',$3)`, f.BoardPrivate, staff, f.Admin); err != nil {
		t.Fatalf("insert staff binding: %v", err)
	}
	if allowed, err := svc.CanReadBoard(ctx, principal(staff), f.BoardPrivate); err != nil || !allowed {
		t.Fatalf("staff-only CanReadBoard = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(staff), f.BoardPrivate, f.ModulePrivate); err != nil || !allowed {
		t.Fatalf("staff-only CanAccessModule = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(staff), f.BoardPublic, f.ModulePrivate); !errors.Is(err, ErrModuleNotFound) || allowed {
		t.Fatalf("staff mismatched module = %v, %v; want ErrModuleNotFound", allowed, err)
	}
	moduleStaff := insertAccessUser(t, db, "module-staff", false, false)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id,module_id,user_id,role,granted_by)
		VALUES ($1,$2,$3,'board_editor',$4)
	`, f.BoardPrivate, f.ModulePrivate, moduleStaff, f.Admin); err != nil {
		t.Fatalf("insert module staff binding: %v", err)
	}
	if allowed, err := svc.CanReadBoard(ctx, principal(moduleStaff), f.BoardPrivate); err != nil || !allowed {
		t.Fatalf("module-staff board shell = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(moduleStaff), f.BoardPrivate, f.ModulePrivate); err != nil || !allowed {
		t.Fatalf("module-staff assigned module = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(moduleStaff), f.BoardPrivate, f.ModuleAudience); err != nil || allowed {
		t.Fatalf("module-staff sibling module = %v, %v; want false", allowed, err)
	}

	// A participant whose group membership is revoked loses participant read
	// immediately even with a live board role and an explicit grant.
	if _, err := authorization.NewGrantStore(db).Grant(ctx, authorization.GrantParams{
		UserID: f.User1, Permission: authorization.PermissionBoardRead,
		Scope: authorization.Scope{BoardID: f.BoardPrivate}, GrantedBy: f.Admin,
	}); err != nil {
		t.Fatalf("grant board.read to participant: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM access_group_members WHERE group_id=$1 AND user_id=$2`, f.Group1, f.User1); err != nil {
		t.Fatalf("remove group membership: %v", err)
	}
	if allowed, err := svc.CanReadBoard(ctx, principal(f.User1), f.BoardPrivate); err != nil || allowed {
		t.Fatalf("revoked-group participant CanReadBoard = %v, %v; want false", allowed, err)
	}
	if allowed, err := svc.CanJoinBoard(ctx, principal(f.User1), f.BoardPrivate); err != nil || allowed {
		t.Fatalf("revoked-group participant CanJoinBoard = %v, %v; want false", allowed, err)
	}
	if _, err := svc.MyAudiences(ctx, principal(f.User1), f.BoardPrivate); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked-group MyAudiences = %v, want ErrForbidden", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO access_group_members (group_id,user_id) VALUES ($1,$2)`, f.Group1, f.User1); err != nil {
		t.Fatalf("restore group membership: %v", err)
	}

	// Join gates: private group, public open, closed self-join.
	// Board-owner can replace the board access-group set; public boards reject
	// groups, and a non-staff participant cannot manage them.
	if err := svc.SetBoardAccessGroups(ctx, principal(f.User1), f.BoardPrivate, []uuid.UUID{f.Group1, f.Group2}); err != nil {
		t.Fatalf("owner set board access groups: %v", err)
	}
	groups, err := svc.BoardAccessGroups(ctx, f.BoardPrivate)
	if err != nil || len(groups) != 2 {
		t.Fatalf("board access groups = %v, %v; want 2", groups, err)
	}
	if err := svc.SetBoardAccessGroups(ctx, principal(f.Admin), f.BoardPublic, []uuid.UUID{f.Group1}); !errors.Is(err, ErrPublicBoardGroups) {
		t.Fatalf("public board group set = %v, want ErrPublicBoardGroups", err)
	}
	if err := svc.SetBoardAccessGroups(ctx, principal(f.User3), f.BoardPrivate, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-staff group set = %v, want ErrForbidden", err)
	}

	if allowed, err := svc.CanJoinBoard(ctx, principal(f.User1), f.BoardPrivate); err != nil || !allowed {
		t.Fatalf("private CanJoinBoard = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanJoinBoard(ctx, principal(f.User3), f.BoardPrivate); err != nil || allowed {
		t.Fatalf("private non-member CanJoinBoard = %v, %v; want false", allowed, err)
	}
	if allowed, err := svc.CanJoinBoard(ctx, principal(f.User3), f.BoardPublic); err != nil || !allowed {
		t.Fatalf("public CanJoinBoard = %v, %v; want true", allowed, err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM board_participants WHERE board_id=$1 AND user_id=$2`, f.BoardClosed, f.User1); err != nil {
		t.Fatalf("remove closed-board participant: %v", err)
	}
	if allowed, err := svc.CanJoinBoard(ctx, principal(f.User1), f.BoardClosed); err != nil || allowed {
		t.Fatalf("closed CanJoinBoard = %v, %v; want false", allowed, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE boards SET ends_at = clock_timestamp() - INTERVAL '1 minute' WHERE id=$1`, f.BoardPrivate); err != nil {
		t.Fatalf("close board window: %v", err)
	}
	if allowed, err := svc.CanJoinBoard(ctx, principal(f.User1), f.BoardPrivate); err != nil || allowed {
		t.Fatalf("ended CanJoinBoard = %v, %v; want false", allowed, err)
	}

	// Audience OR membership for a participant: user-one is in aud-one,
	// user-two in aud-two; either grants access to the module.
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User1), f.BoardPrivate, f.ModuleAudience); err != nil || !allowed {
		t.Fatalf("audience OR user-one = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User2), f.BoardPrivate, f.ModuleAudience); err != nil || !allowed {
		t.Fatalf("audience OR user-two = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User3), f.BoardPrivate, f.ModuleAudience); err != nil || allowed {
		t.Fatalf("audience missing user-three = %v, %v; want false", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User1), f.BoardPrivate, f.ModulePrivate); err != nil || !allowed {
		t.Fatalf("ungated module = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User1), f.BoardPublic, f.ModulePrivate); !errors.Is(err, ErrModuleNotFound) || allowed {
		t.Fatalf("mismatched module parent = %v, %v; want ErrModuleNotFound", allowed, err)
	}

	// Audience removal immediately denies the module.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM audience_members WHERE audience_id=$1 AND subject_type='USER' AND subject_id=$2`, f.Audience1, f.User1); err != nil {
		t.Fatalf("remove audience membership: %v", err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User1), f.BoardPrivate, f.ModuleAudience); err != nil || allowed {
		t.Fatalf("removed audience member = %v, %v; want false", allowed, err)
	}

	// TEAM subject: current team resolves to TEAM, not USER.
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User1), f.BoardTeam, f.ModuleTeam); err != nil || !allowed {
		t.Fatalf("team captain module access = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User2), f.BoardTeam, f.ModuleTeam); err != nil || !allowed {
		t.Fatalf("team member module access = %v, %v; want true", allowed, err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User3), f.BoardTeam, f.ModuleTeam); err != nil || allowed {
		t.Fatalf("no-team module access = %v, %v; want false", allowed, err)
	}
	my, err := svc.MyAudiences(ctx, principal(f.User1), f.BoardTeam)
	if err != nil || len(my) != 1 || my[0].ID != f.AudienceTeam {
		t.Fatalf("team MyAudiences = %+v, %v; want team audience", my, err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM team_members WHERE board_id=$1 AND team_id=$2 AND user_id=$3`, f.BoardTeam, f.TeamA, f.User2); err != nil {
		t.Fatalf("remove team member: %v", err)
	}
	if allowed, err := svc.CanAccessModule(ctx, principal(f.User2), f.BoardTeam, f.ModuleTeam); err != nil || allowed {
		t.Fatalf("removed team member module access = %v, %v; want false", allowed, err)
	}

	// PUBLIC + CLOSED still rejects self-join for a non-participant.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM board_participants WHERE board_id=$1 AND user_id=$2`, f.BoardPublic, f.User3); err != nil {
		t.Fatalf("remove public participant: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE boards SET registration_mode='CLOSED' WHERE id=$1`, f.BoardPublic); err != nil {
		t.Fatalf("close public board: %v", err)
	}
	if allowed, err := svc.CanJoinBoard(ctx, principal(f.User3), f.BoardPublic); err != nil || allowed {
		t.Fatalf("public closed CanJoinBoard = %v, %v; want false", allowed, err)
	}
}
