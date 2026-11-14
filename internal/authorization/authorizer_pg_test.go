package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestAuthorizerLiveScopedMatrix(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	ctx := context.Background()

	principal := func(userID uuid.UUID) Principal {
		return Principal{UserID: userID}
	}
	module := func(id uuid.UUID) *uuid.UUID { return &id }

	checks := []struct {
		name       string
		principal  Principal
		permission Permission
		scope      Scope
		allowed    bool
	}{
		{"owner can manage module in own board", principal(f.OwnerA), PermissionModuleTasksManage, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, true},
		{"owner cannot manage module in other board", principal(f.OwnerA), PermissionModuleTasksManage, Scope{BoardID: f.BoardB, ModuleID: module(f.ModuleZ)}, false},
		{"owner can update own board", principal(f.OwnerA), PermissionBoardUpdate, Scope{BoardID: f.BoardA}, true},
		{"owner cannot delete board", principal(f.OwnerA), PermissionBoardDelete, Scope{BoardID: f.BoardA}, false},
		{"owner cannot create board", principal(f.OwnerA), PermissionBoardCreate, Scope{}, false},
		{"owner cannot platform audit", principal(f.OwnerA), PermissionPlatformAuditRead, Scope{}, false},
		{"owner can manage staff", principal(f.OwnerA), PermissionBoardStaffManage, Scope{BoardID: f.BoardA}, true},

		{"board-wide editor can create module", principal(f.EditorA), PermissionModuleCreate, Scope{BoardID: f.BoardA}, true},
		{"board-wide editor can delete sibling module", principal(f.EditorA), PermissionModuleDelete, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleY)}, true},
		{"board-wide editor cannot review submissions", principal(f.EditorA), PermissionModuleSubmissionsReview, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, false},
		{"board-wide editor cannot update board policy", principal(f.EditorA), PermissionBoardUpdate, Scope{BoardID: f.BoardA}, false},
		{"board-wide editor can read board", principal(f.EditorA), PermissionBoardRead, Scope{BoardID: f.BoardA}, true},

		{"module editor can manage own module", principal(f.ModuleEditor), PermissionModuleTasksManage, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, true},
		{"module editor can delete own module", principal(f.ModuleEditor), PermissionModuleDelete, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, true},
		{"module editor cannot touch sibling module", principal(f.ModuleEditor), PermissionModuleUpdate, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleY)}, false},
		{"module editor cannot create another module", principal(f.ModuleEditor), PermissionModuleCreate, Scope{BoardID: f.BoardA}, false},
		{"module editor cannot read broad submissions", principal(f.ModuleEditor), PermissionModuleSubmissionsRead, Scope{BoardID: f.BoardA}, false},
		{"module editor cannot board overview", principal(f.ModuleEditor), PermissionBoardRead, Scope{BoardID: f.BoardA}, false},
		{"module editor can read assigned module", principal(f.ModuleEditor), PermissionBoardRead, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, true},
		{"module editor cannot read sibling module", principal(f.ModuleEditor), PermissionBoardRead, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleY)}, false},
		{"module editor can read own module submissions", principal(f.ModuleEditor), PermissionModuleSubmissionsRead, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, true},

		{"module reviewer can read assigned module", principal(f.ReviewerX), PermissionModuleSubmissionsRead, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, true},
		{"module reviewer can review assigned module", principal(f.ReviewerX), PermissionModuleSubmissionsReview, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, true},
		{"module reviewer cannot read sibling module", principal(f.ReviewerX), PermissionModuleSubmissionsRead, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleY)}, false},
		{"module reviewer cannot manage tasks", principal(f.ReviewerX), PermissionModuleTasksManage, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, false},
		{"module reviewer cannot board overview", principal(f.ReviewerX), PermissionBoardRead, Scope{BoardID: f.BoardA}, false},

		{"board reviewer can review any board module", principal(f.ReviewerAll), PermissionModuleSubmissionsReview, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleY)}, true},
		{"board reviewer can read board", principal(f.ReviewerAll), PermissionBoardRead, Scope{BoardID: f.BoardA}, true},
		{"board reviewer cannot manage tasks", principal(f.ReviewerAll), PermissionModuleTasksManage, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, false},

		{"participant can read joined board", principal(f.Participant), PermissionBoardRead, Scope{BoardID: f.BoardA}, true},
		{"participant can submit own", principal(f.Participant), PermissionSubmissionCreateOwn, Scope{BoardID: f.BoardA}, true},
		{"participant cannot read broad submissions", principal(f.Participant), PermissionModuleSubmissionsRead, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, false},
		{"participant cannot manage tasks", principal(f.Participant), PermissionModuleTasksManage, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, false},

		{"admin can create board", principal(f.Admin), PermissionBoardCreate, Scope{}, true},
		{"admin can delete any board", principal(f.Admin), PermissionBoardDelete, Scope{BoardID: f.BoardB}, true},
		{"admin can platform audit", principal(f.Admin), PermissionPlatformAuditRead, Scope{}, true},
		{"admin can manage any module", principal(f.Admin), PermissionModuleUpdate, Scope{BoardID: f.BoardB, ModuleID: module(f.ModuleZ)}, true},
		{"admin still needs participation for own submissions", principal(f.Admin), PermissionSubmissionCreateOwn, Scope{BoardID: f.BoardA}, false},

		{"outsider cannot read board", principal(f.Outsider), PermissionBoardRead, Scope{BoardID: f.BoardA}, false},
		{"outsider cannot manage module", principal(f.Outsider), PermissionModuleUpdate, Scope{BoardID: f.BoardA, ModuleID: module(f.ModuleX)}, false},
	}

	for _, check := range checks {
		check := check
		t.Run(check.name, func(t *testing.T) {
			decision, err := authz.Authorize(ctx, check.principal, check.permission, check.scope)
			if err != nil {
				t.Fatalf("Authorize error: %v", err)
			}
			if decision.Allowed != check.allowed {
				t.Fatalf("allowed = %v, want %v (roles=%v permissions=%v reason=%s)",
					decision.Allowed, check.allowed, decision.Roles, decision.Permissions, decision.Reason)
			}
		})
	}
}

func TestAuthorizerLiveAdminRolesAndDisabled(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	ctx := context.Background()

	// A stale/forged principal claiming admin must not be trusted.
	forged := Principal{UserID: f.Outsider, GlobalRoles: []string{"admin"}}
	decision, err := authz.Authorize(ctx, forged, PermissionBoardRead, Scope{BoardID: f.BoardA})
	if err != nil {
		t.Fatalf("forged admin authorize: %v", err)
	}
	if decision.Allowed {
		t.Fatal("forged Principal.GlobalRoles granted access")
	}

	// A live role change is observed on the next call even with a stale principal.
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET roles = ARRAY['admin','user']::text[] WHERE id = $1`, f.Outsider); err != nil {
		t.Fatalf("grant live admin: %v", err)
	}
	decision, err = authz.Authorize(ctx, forged, PermissionPlatformAuditRead, Scope{})
	if err != nil {
		t.Fatalf("live admin authorize: %v", err)
	}
	if !decision.Allowed {
		t.Fatal("live admin role was not observed")
	}

	// Removing the live role immediately removes global access.
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET roles = ARRAY['user']::text[] WHERE id = $1`, f.Outsider); err != nil {
		t.Fatalf("remove live admin: %v", err)
	}
	decision, err = authz.Authorize(ctx, forged, PermissionPlatformAuditRead, Scope{})
	if err != nil {
		t.Fatalf("removed admin authorize: %v", err)
	}
	if decision.Allowed {
		t.Fatal("stale admin access survived live role removal")
	}

	// Disabled accounts fail closed.
	_, err = authz.Authorize(ctx, Principal{UserID: f.Disabled}, PermissionBoardRead, Scope{BoardID: f.BoardA})
	if !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled user error = %v, want ErrUserDisabled", err)
	}
	// Unknown users fail closed.
	_, err = authz.Authorize(ctx, Principal{UserID: uuid.New()}, PermissionBoardRead, Scope{BoardID: f.BoardA})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown user error = %v, want ErrUnauthorized", err)
	}
}

func TestAuthorizerParentIntegrityBeforeAdmin(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	ctx := context.Background()

	_, err := authz.Authorize(ctx, Principal{UserID: f.Admin}, PermissionModuleUpdate, Scope{
		BoardID:  f.BoardB,
		ModuleID: &f.ModuleX, // module X belongs to board A
	})
	if !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("mismatched parent error = %v, want ErrModuleNotFound", err)
	}

	_, err = authz.Authorize(ctx, Principal{UserID: f.Admin}, PermissionBoardRead, Scope{BoardID: uuid.New()})
	if !errors.Is(err, ErrBoardNotFound) {
		t.Fatalf("unknown board error = %v, want ErrBoardNotFound", err)
	}

	_, err = authz.Authorize(ctx, Principal{UserID: f.Admin}, PermissionBoardRead, Scope{})
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("missing board scope error = %v, want ErrInvalidScope", err)
	}

	_, err = authz.Authorize(ctx, Principal{UserID: f.Admin}, Permission("board.unknown"), Scope{BoardID: f.BoardA})
	if !errors.Is(err, ErrInvalidPermission) {
		t.Fatalf("unknown permission error = %v, want ErrInvalidPermission", err)
	}
}

func TestAuthorizerPermissionsContext(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	ctx := context.Background()

	owner, err := authz.Permissions(ctx, Principal{UserID: f.OwnerA}, f.BoardA)
	if err != nil {
		t.Fatalf("owner permissions: %v", err)
	}
	if len(owner.Roles) != 1 || owner.Roles[0] != RoleBoardOwner {
		t.Fatalf("owner roles = %v, want [board_owner]", owner.Roles)
	}
	if len(owner.Modules) != 2 {
		t.Fatalf("owner module contexts = %d, want 2", len(owner.Modules))
	}
	requirePermissions(t, permissionSetList(owner.Permissions), PermissionBoardStaffManage, PermissionModuleCreate)

	moduleEditor, err := authz.Permissions(ctx, Principal{UserID: f.ModuleEditor}, f.BoardA)
	if err != nil {
		t.Fatalf("module editor permissions: %v", err)
	}
	if len(moduleEditor.Roles) != 0 {
		t.Fatalf("module-only roles leaked to board level: %v", moduleEditor.Roles)
	}
	if len(moduleEditor.Permissions) != 0 {
		t.Fatalf("module-only permissions leaked to board level: %v", moduleEditor.Permissions)
	}
	if len(moduleEditor.Modules) != 1 || moduleEditor.Modules[0].ModuleID != f.ModuleX {
		t.Fatalf("module editor contexts = %+v, want only module X", moduleEditor.Modules)
	}
	requirePermissions(t, permissionSetList(moduleEditor.Modules[0].Permissions),
		PermissionModuleUpdate, PermissionModuleDelete, PermissionModuleTasksManage,
		PermissionModuleSubmissionsRead, PermissionBoardRead)
	denyPermissions(t, permissionSetList(moduleEditor.Modules[0].Permissions), PermissionModuleCreate)

	participant, err := authz.Permissions(ctx, Principal{UserID: f.Participant}, f.BoardA)
	if err != nil {
		t.Fatalf("participant permissions: %v", err)
	}
	requirePermissions(t, permissionSetList(participant.Permissions),
		PermissionBoardRead, PermissionSubmissionCreateOwn, PermissionSubmissionReadOwn)
	if len(participant.Modules) != 0 {
		t.Fatalf("participant received module contexts: %+v", participant.Modules)
	}

	admin, err := authz.Permissions(ctx, Principal{UserID: f.Admin}, f.BoardA)
	if err != nil {
		t.Fatalf("admin permissions: %v", err)
	}
	requirePermissions(t, permissionSetList(admin.Permissions),
		PermissionBoardCreate, PermissionBoardDelete, PermissionPlatformUsersManage)
	if len(admin.Modules) != 2 {
		t.Fatalf("admin module contexts = %d, want 2", len(admin.Modules))
	}

	outsider, err := authz.Permissions(ctx, Principal{UserID: f.Outsider}, f.BoardA)
	if err != nil {
		t.Fatalf("outsider permissions: %v", err)
	}
	if len(outsider.Permissions) != 0 || len(outsider.Modules) != 0 || len(outsider.Roles) != 0 {
		t.Fatalf("outsider received access: %+v", outsider)
	}

	if _, err := authz.Permissions(ctx, Principal{UserID: f.Outsider}, uuid.New()); !errors.Is(err, ErrBoardNotFound) {
		t.Fatalf("unknown board permissions error = %v, want ErrBoardNotFound", err)
	}
}
