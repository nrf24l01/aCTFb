package authorization

import (
	"testing"
)

func TestCatalogIsExactFirstSliceSet(t *testing.T) {
	if len(AllPermissions) != 29 {
		t.Fatalf("permission count = %d, want 29", len(AllPermissions))
	}
	seen := make(map[Permission]struct{}, len(AllPermissions))
	for _, permission := range AllPermissions {
		if !ValidPermission(permission) {
			t.Fatalf("catalog permission %q is not valid", permission)
		}
		if _, exists := seen[permission]; exists {
			t.Fatalf("duplicate permission %q", permission)
		}
		seen[permission] = struct{}{}
	}
	for _, role := range scopedRoles {
		if !ValidRole(role) {
			t.Fatalf("catalog role %q is not valid", role)
		}
	}
	for _, raw := range []string{"board_owner", " BOARD_EDITOR ", "reviewer"} {
		if _, ok := ParseRole(raw); !ok {
			t.Fatalf("ParseRole(%q) failed", raw)
		}
	}
	if _, ok := ParseRole("admin"); ok {
		t.Fatal("global admin must not parse as a scoped role")
	}
}

func TestBoardPermissionMatrix(t *testing.T) {
	owner := permissionSetList(boardPermissions([]Role{RoleBoardOwner}, false, false))
	requirePermissions(t, owner,
		PermissionBoardRead, PermissionBoardUpdate, PermissionBoardPublish,
		PermissionBoardStaffManage, PermissionBoardParticipantsManage,
		PermissionBoardRatingManage, PermissionBoardAuditRead,
		PermissionModuleCreate, PermissionModuleUpdate, PermissionModuleDelete,
		PermissionModuleTasksManage, PermissionModuleSubmissionsRead,
		PermissionModuleSubmissionsReview,
	)
	denyPermissions(t, owner,
		PermissionBoardCreate, PermissionBoardDelete,
		PermissionSubmissionCreateOwn, PermissionSubmissionReadOwn,
		PermissionPlatformUsersManage, PermissionPlatformAuditRead,
	)

	editor := permissionSetList(boardPermissions([]Role{RoleBoardEditor}, false, false))
	// Main correction: board-wide editor can create/update/delete modules.
	requirePermissions(t, editor,
		PermissionBoardRead, PermissionModuleCreate, PermissionModuleUpdate,
		PermissionModuleDelete, PermissionModuleTasksManage,
		PermissionModuleSubmissionsRead,
	)
	denyPermissions(t, editor,
		PermissionBoardUpdate, PermissionBoardPublish, PermissionBoardStaffManage,
		PermissionModuleSubmissionsReview, PermissionBoardDelete,
	)

	reviewer := permissionSetList(boardPermissions([]Role{RoleReviewer}, false, false))
	requirePermissions(t, reviewer,
		PermissionBoardRead, PermissionModuleSubmissionsRead, PermissionModuleSubmissionsReview,
	)
	denyPermissions(t, reviewer, PermissionModuleTasksManage, PermissionModuleUpdate, PermissionModuleCreate)

	participant := permissionSetList(boardPermissions(nil, false, true))
	requirePermissions(t, participant,
		PermissionBoardRead, PermissionSubmissionCreateOwn, PermissionSubmissionReadOwn,
	)
	denyPermissions(t, participant, PermissionModuleSubmissionsRead, PermissionModuleTasksManage)

	admin := permissionSetList(boardPermissions(nil, true, false))
	requirePermissions(t, admin, PermissionBoardCreate, PermissionBoardDelete, PermissionPlatformAuditRead, PermissionBoardRead)
	denyPermissions(t, admin, PermissionSubmissionCreateOwn, PermissionSubmissionReadOwn)

	adminParticipant := permissionSetList(boardPermissions(nil, true, true))
	requirePermissions(t, adminParticipant, PermissionSubmissionCreateOwn, PermissionSubmissionReadOwn)
}

func TestModulePermissionMatrix(t *testing.T) {
	moduleEditor := permissionSetList(modulePermissions(nil, []Role{RoleBoardEditor}, false, false))
	requirePermissions(t, moduleEditor,
		PermissionModuleUpdate, PermissionModuleDelete,
		PermissionModuleTasksManage, PermissionModuleSubmissionsRead,
		PermissionBoardRead,
	)
	denyPermissions(t, moduleEditor,
		PermissionModuleCreate, PermissionModuleSubmissionsReview, PermissionSubmissionCreateOwn,
	)

	moduleReviewer := permissionSetList(modulePermissions(nil, []Role{RoleReviewer}, false, false))
	requirePermissions(t, moduleReviewer, PermissionModuleSubmissionsRead, PermissionModuleSubmissionsReview, PermissionBoardRead)
	denyPermissions(t, moduleReviewer, PermissionModuleTasksManage, PermissionModuleUpdate, PermissionModuleCreate)

	boardWideOwner := permissionSetList(modulePermissions([]Role{RoleBoardOwner}, nil, false, false))
	requirePermissions(t, boardWideOwner,
		PermissionModuleUpdate, PermissionModuleDelete,
		PermissionModuleTasksManage, PermissionModuleSubmissionsRead,
		PermissionModuleSubmissionsReview, PermissionBoardRead,
	)

	moduleScopedEditorWithBoardRole := permissionSetList(modulePermissions([]Role{RoleBoardEditor}, []Role{RoleReviewer}, false, false))
	requirePermissions(t, moduleScopedEditorWithBoardRole,
		PermissionModuleUpdate, PermissionModuleDelete, PermissionModuleTasksManage,
		PermissionModuleSubmissionsRead, PermissionModuleSubmissionsReview,
	)
	denyPermissions(t, moduleScopedEditorWithBoardRole, PermissionModuleCreate)

	admin := permissionSetList(modulePermissions(nil, nil, true, false))
	requirePermissions(t, admin, PermissionBoardCreate, PermissionModuleUpdate, PermissionPlatformUsersManage)
	denyPermissions(t, admin, PermissionSubmissionCreateOwn)
}

func TestGlobalPermissionsAreAdminOnly(t *testing.T) {
	for _, permission := range []Permission{
		PermissionBoardCreate,
		PermissionPlatformUsersManage,
		PermissionPlatformAccessGroupsManage,
		PermissionPlatformIntegrationsManage,
		PermissionPlatformAuditRead,
	} {
		if !isGlobalPermission(permission) {
			t.Fatalf("%s should be global", permission)
		}
		requirePermissions(t, permissionSetList(globalPermissionSet(true)), permission)
		denyPermissions(t, permissionSetList(globalPermissionSet(false)), permission)
	}
	for _, permission := range []Permission{
		PermissionBoardRead, PermissionBoardDelete, PermissionModuleUpdate, PermissionSubmissionCreateOwn,
	} {
		if isGlobalPermission(permission) {
			t.Fatalf("%s must not be global", permission)
		}
	}
}

func permissionSetList(permissions []Permission) map[Permission]struct{} {
	set := make(map[Permission]struct{}, len(permissions))
	for _, permission := range permissions {
		set[permission] = struct{}{}
	}
	return set
}

func requirePermissions(t *testing.T, set map[Permission]struct{}, permissions ...Permission) {
	t.Helper()
	for _, permission := range permissions {
		if _, ok := set[permission]; !ok {
			t.Fatalf("missing expected permission %s", permission)
		}
	}
}

func denyPermissions(t *testing.T, set map[Permission]struct{}, permissions ...Permission) {
	t.Helper()
	for _, permission := range permissions {
		if _, ok := set[permission]; ok {
			t.Fatalf("unexpected permission %s", permission)
		}
	}
}

func TestPrincipalFromUserCopiesOnlyIdentity(t *testing.T) {
	principal := PrincipalFromUser(nil)
	if principal.UserID.String() != "00000000-0000-0000-0000-000000000000" || len(principal.GlobalRoles) != 0 {
		t.Fatalf("nil user principal = %+v", principal)
	}
}
