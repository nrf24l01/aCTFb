// Package authorization implements the live, database-backed scoped RBAC
// service used by both HTTP handlers and business services.
//
// The permission catalog and role matrix live in this file. Database rows are
// never trusted as a permission map: every Authorize call reloads the user,
// active board role bindings, and participation state from PostgreSQL.
package authorization

import (
	"sort"
	"strings"
)

// Permission is a stable, namespaced capability code. HTTP route names are not
// permissions; the catalog is intentionally small and centralized.
type Permission string

const (
	PermissionBoardCreate  Permission = "board.create"
	PermissionBoardRead    Permission = "board.read"
	PermissionBoardUpdate  Permission = "board.update"
	PermissionBoardPublish Permission = "board.publish"
	PermissionBoardDelete  Permission = "board.delete"

	PermissionBoardStaffManage        Permission = "board.staff.manage"
	PermissionBoardParticipantsManage Permission = "board.participants.manage"
	PermissionBoardRatingManage       Permission = "board.rating.manage"
	PermissionBoardAuditRead          Permission = "board.audit.read"
	PermissionBoardAudiencesManage    Permission = "board.audiences.manage"

	PermissionModuleCreate            Permission = "module.create"
	PermissionModuleUpdate            Permission = "module.update"
	PermissionModuleDelete            Permission = "module.delete"
	PermissionModuleTasksManage       Permission = "module.tasks.manage"
	PermissionModuleSubmissionsRead   Permission = "module.submissions.read"
	PermissionModuleSubmissionsReview Permission = "module.submissions.review"

	PermissionSubmissionCreateOwn Permission = "submission.create_own"
	PermissionSubmissionReadOwn   Permission = "submission.read_own"

	PermissionPlatformUsersManage                Permission = "platform.users.manage"
	PermissionPlatformAccessGroupsManage         Permission = "platform.access_groups.manage"
	PermissionPlatformIntegrationsManage         Permission = "platform.integrations.manage"
	PermissionPlatformAuditRead                  Permission = "platform.audit.read"
	PermissionPlatformDeploymentTemplatesPublish Permission = "platform.deployment_templates.publish"
	PermissionPluginsManage                      Permission = "plugins.manage"

	PermissionDeploymentTemplatesUse Permission = "deployment_templates.use"
	PermissionModuleInstancesAdmin   Permission = "module.instances.admin"
	PermissionInstanceStartOwn       Permission = "instance.start_own"
	PermissionInstanceManageOwn      Permission = "instance.manage_own"
	PermissionTeamManageOwn          Permission = "team.manage_own"
)

// Role is a scoped assignable role. Global roles (user/admin) remain in
// users.roles and are deliberately not represented here.
type Role string

const (
	RoleBoardOwner  Role = "board_owner"
	RoleBoardEditor Role = "board_editor"
	RoleReviewer    Role = "reviewer"
)

// AllPermissions is the exact first-slice permission catalog in stable order.
// Adding a permission requires updating the role matrix and its tests.
var AllPermissions = []Permission{
	PermissionBoardCreate,
	PermissionBoardRead,
	PermissionBoardUpdate,
	PermissionBoardPublish,
	PermissionBoardDelete,
	PermissionBoardStaffManage,
	PermissionBoardParticipantsManage,
	PermissionBoardRatingManage,
	PermissionBoardAuditRead,
	PermissionBoardAudiencesManage,
	PermissionModuleCreate,
	PermissionModuleUpdate,
	PermissionModuleDelete,
	PermissionModuleTasksManage,
	PermissionModuleSubmissionsRead,
	PermissionModuleSubmissionsReview,
	PermissionSubmissionCreateOwn,
	PermissionSubmissionReadOwn,
	PermissionPlatformUsersManage,
	PermissionPlatformAccessGroupsManage,
	PermissionPlatformIntegrationsManage,
	PermissionPlatformAuditRead,
	PermissionPlatformDeploymentTemplatesPublish,
	PermissionPluginsManage,
	PermissionDeploymentTemplatesUse,
	PermissionModuleInstancesAdmin,
	PermissionInstanceStartOwn,
	PermissionInstanceManageOwn,
	PermissionTeamManageOwn,
}

var scopedRoles = []Role{RoleBoardOwner, RoleBoardEditor, RoleReviewer}

var permissionRank = func() map[Permission]int {
	rank := make(map[Permission]int, len(AllPermissions))
	for index, permission := range AllPermissions {
		rank[permission] = index
	}
	return rank
}()

var roleRank = func() map[Role]int {
	rank := make(map[Role]int, len(scopedRoles))
	for index, role := range scopedRoles {
		rank[role] = index
	}
	return rank
}()

// GlobalPermissions never require a board/module scope.
var globalPermissions = map[Permission]struct{}{
	PermissionBoardCreate:                        {},
	PermissionPlatformUsersManage:                {},
	PermissionPlatformAccessGroupsManage:         {},
	PermissionPlatformIntegrationsManage:         {},
	PermissionPlatformAuditRead:                  {},
	PermissionPlatformDeploymentTemplatesPublish: {},
	PermissionPluginsManage:                      {},
}

// BoardPermissionsForRole returns the board-scoped permissions of a role.
// Module-scoped grants intentionally do not receive these permissions.
func boardPermissionsForRole(role Role) []Permission {
	switch role {
	case RoleBoardOwner:
		return []Permission{
			PermissionBoardRead,
			PermissionBoardUpdate,
			PermissionBoardPublish,
			PermissionBoardStaffManage,
			PermissionBoardParticipantsManage,
			PermissionBoardRatingManage,
			PermissionBoardAuditRead,
			PermissionBoardAudiencesManage,
			PermissionModuleCreate,
			PermissionModuleUpdate,
			PermissionModuleDelete,
			PermissionModuleTasksManage,
			PermissionModuleSubmissionsRead,
			PermissionModuleSubmissionsReview,
			PermissionDeploymentTemplatesUse,
			PermissionModuleInstancesAdmin,
		}
	case RoleBoardEditor:
		// Main correction: a board-wide editor may create/update/delete
		// modules on its own board. A module-scoped editor may only update or
		// delete its assigned module and manage tasks there.
		return []Permission{
			PermissionBoardRead,
			PermissionModuleCreate,
			PermissionModuleUpdate,
			PermissionModuleDelete,
			PermissionModuleTasksManage,
			PermissionModuleSubmissionsRead,
		}
	case RoleReviewer:
		return []Permission{
			PermissionBoardRead,
			PermissionModuleSubmissionsRead,
			PermissionModuleSubmissionsReview,
		}
	default:
		return nil
	}
}

// modulePermissionsForRole returns permissions that are meaningful inside one
// module scope. Board-wide roles are evaluated through this set when a request
// supplies a module UUID.
func modulePermissionsForRole(role Role) []Permission {
	switch role {
	case RoleBoardOwner:
		return []Permission{
			PermissionModuleUpdate,
			PermissionModuleDelete,
			PermissionModuleTasksManage,
			PermissionModuleSubmissionsRead,
			PermissionModuleSubmissionsReview,
			PermissionModuleInstancesAdmin,
		}
	case RoleBoardEditor:
		return []Permission{
			PermissionModuleUpdate,
			PermissionModuleDelete,
			PermissionModuleTasksManage,
			PermissionModuleSubmissionsRead,
		}
	case RoleReviewer:
		return []Permission{
			PermissionModuleSubmissionsRead,
			PermissionModuleSubmissionsReview,
		}
	default:
		return nil
	}
}

// ValidPermission reports whether permission is part of the approved catalog.
func ValidPermission(permission Permission) bool {
	_, ok := permissionRank[permission]
	return ok
}

// ValidRole reports whether role is an assignable scoped role.
func ValidRole(role Role) bool {
	_, ok := roleRank[role]
	return ok
}

// ParseRole normalizes and validates a role supplied by an API request.
func ParseRole(raw string) (Role, bool) {
	role := Role(strings.ToLower(strings.TrimSpace(raw)))
	if !ValidRole(role) {
		return "", false
	}
	return role, true
}

func isAdminRoles(roles []string) bool {
	for _, role := range roles {
		if strings.EqualFold(strings.TrimSpace(role), "admin") {
			return true
		}
	}
	return false
}

func isGlobalPermission(permission Permission) bool {
	_, ok := globalPermissions[permission]
	return ok
}

func permissionSet(permissions ...[]Permission) map[Permission]struct{} {
	set := make(map[Permission]struct{}, len(AllPermissions))
	for _, group := range permissions {
		for _, permission := range group {
			if ValidPermission(permission) {
				set[permission] = struct{}{}
			}
		}
	}
	return set
}

func addPermissions(set map[Permission]struct{}, permissions []Permission) {
	for _, permission := range permissions {
		if ValidPermission(permission) {
			set[permission] = struct{}{}
		}
	}
}

func sortedPermissions(set map[Permission]struct{}) []Permission {
	permissions := make([]Permission, 0, len(set))
	for permission := range set {
		permissions = append(permissions, permission)
	}
	sort.Slice(permissions, func(i, j int) bool {
		left, leftKnown := permissionRank[permissions[i]]
		right, rightKnown := permissionRank[permissions[j]]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown && left != right {
			return left < right
		}
		return permissions[i] < permissions[j]
	})
	return permissions
}

func sortedRoles(roles map[Role]struct{}) []Role {
	result := make([]Role, 0, len(roles))
	for role := range roles {
		result = append(result, role)
	}
	sort.Slice(result, func(i, j int) bool {
		return roleRank[result[i]] < roleRank[result[j]]
	})
	return result
}

// boardPermissions computes effective board-scope permissions. Module-scoped
// bindings are deliberately excluded by the caller.
func boardPermissions(boardRoles []Role, admin, participant bool) []Permission {
	set := make(map[Permission]struct{}, len(AllPermissions))
	if admin {
		addPermissions(set, AllPermissions)
		// Own-submission permissions are a participation state, not an admin
		// entitlement. Remove them unless the user is joined below.
		delete(set, PermissionSubmissionCreateOwn)
		delete(set, PermissionSubmissionReadOwn)
		delete(set, PermissionInstanceStartOwn)
		delete(set, PermissionInstanceManageOwn)
		delete(set, PermissionTeamManageOwn)
	}
	for _, role := range boardRoles {
		addPermissions(set, boardPermissionsForRole(role))
	}
	if participant {
		// Joined participants can read their private board and submit/read
		// only their own submissions.
		set[PermissionBoardRead] = struct{}{}
		set[PermissionSubmissionCreateOwn] = struct{}{}
		set[PermissionSubmissionReadOwn] = struct{}{}
		set[PermissionInstanceStartOwn] = struct{}{}
		set[PermissionInstanceManageOwn] = struct{}{}
	}
	return sortedPermissions(set)
}

// modulePermissions computes effective module-scope permissions. Board-wide
// roles are reduced to their module-relevant subset; module-scoped roles add
// only their own module permissions.
func modulePermissions(boardRoles, moduleRoles []Role, admin, participant bool) []Permission {
	set := make(map[Permission]struct{}, len(AllPermissions))
	if admin {
		addPermissions(set, AllPermissions)
		delete(set, PermissionSubmissionCreateOwn)
		delete(set, PermissionSubmissionReadOwn)
		delete(set, PermissionInstanceStartOwn)
		delete(set, PermissionInstanceManageOwn)
		delete(set, PermissionTeamManageOwn)
	}
	for _, role := range boardRoles {
		addPermissions(set, modulePermissionsForRole(role))
	}
	for _, role := range moduleRoles {
		addPermissions(set, modulePermissionsForRole(role))
	}
	// Module scope may read the assigned module, but board-scope checks remain
	// denied for module-only actors. Services must use board scope for board
	// overview and module scope for a specific assigned module.
	if len(boardRoles) > 0 || len(moduleRoles) > 0 || participant {
		set[PermissionBoardRead] = struct{}{}
	}
	if participant {
		set[PermissionSubmissionCreateOwn] = struct{}{}
		set[PermissionSubmissionReadOwn] = struct{}{}
		set[PermissionInstanceStartOwn] = struct{}{}
		set[PermissionInstanceManageOwn] = struct{}{}
	}
	return sortedPermissions(set)
}

func globalPermissionSet(admin bool) []Permission {
	if !admin {
		return nil
	}
	set := make(map[Permission]struct{}, len(globalPermissions))
	for permission := range globalPermissions {
		set[permission] = struct{}{}
	}
	return sortedPermissions(set)
}
