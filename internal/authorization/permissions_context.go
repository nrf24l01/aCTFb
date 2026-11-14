package authorization

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func (a *AuthorizerService) Permissions(ctx context.Context, principal Principal, boardID uuid.UUID) (BoardPermissions, error) {
	if a == nil || a.db == nil || a.db.Pool == nil {
		return BoardPermissions{}, ErrNoDatabase
	}
	if principal.UserID == uuid.Nil {
		return BoardPermissions{}, ErrUnauthorized
	}
	if boardID == uuid.Nil {
		return BoardPermissions{}, ErrInvalidScope
	}

	user, err := loadLiveUser(ctx, a.db.Pool, principal.UserID)
	if err != nil {
		return BoardPermissions{}, err
	}
	if err := boardExists(ctx, a.db.Pool, boardID); err != nil {
		return BoardPermissions{}, err
	}
	bindings, err := loadActiveBindings(ctx, a.db.Pool, principal.UserID, boardID)
	if err != nil {
		return BoardPermissions{}, err
	}
	participant, err := isParticipant(ctx, a.db.Pool, principal.UserID, boardID)
	if err != nil {
		return BoardPermissions{}, err
	}

	admin := user.isAdmin()
	boardRoles, _ := splitRoles(bindings, nil)
	moduleRoleMap := make(map[uuid.UUID]map[Role]struct{})
	for _, binding := range bindings {
		if binding.ModuleID == nil {
			continue
		}
		set, ok := moduleRoleMap[*binding.ModuleID]
		if !ok {
			set = make(map[Role]struct{})
			moduleRoleMap[*binding.ModuleID] = set
		}
		set[binding.Role] = struct{}{}
	}

	result := BoardPermissions{
		BoardID:     boardID,
		Roles:       boardRoles,
		Permissions: boardPermissions(boardRoles, admin, participant),
		Modules:     make([]ModulePermissions, 0, len(moduleRoleMap)),
	}

	rows, err := a.db.Pool.Query(ctx, `
		SELECT id FROM modules WHERE board_id = $1 ORDER BY sort_order, id
	`, boardID)
	if err != nil {
		return BoardPermissions{}, fmt.Errorf("authorization: list modules for permissions: %w", err)
	}
	defer rows.Close()

	broad := admin || len(boardRoles) > 0
	for rows.Next() {
		var moduleID uuid.UUID
		if err := rows.Scan(&moduleID); err != nil {
			return BoardPermissions{}, fmt.Errorf("authorization: scan module for permissions: %w", err)
		}
		moduleRoleSet, hasModuleRoles := moduleRoleMap[moduleID]
		if !broad && !hasModuleRoles {
			continue
		}
		moduleRoles := sortedRoles(moduleRoleSet)
		result.Modules = append(result.Modules, ModulePermissions{
			ModuleID:    moduleID,
			Roles:       mergeRoles(boardRoles, moduleRoles),
			Permissions: modulePermissions(boardRoles, moduleRoles, admin, participant),
		})
	}
	if err := rows.Err(); err != nil {
		return BoardPermissions{}, fmt.Errorf("authorization: iterate modules for permissions: %w", err)
	}
	return a.augmentPermissionContext(ctx, user.ID, boardID, admin, participant, result)
}

type grantContextLister interface {
	ActiveGrantsForUserBoard(ctx context.Context, userID, boardID uuid.UUID) ([]PermissionGrant, error)
}

func (a *AuthorizerService) augmentPermissionContext(
	ctx context.Context,
	userID, boardID uuid.UUID,
	admin, participant bool,
	result BoardPermissions,
) (BoardPermissions, error) {
	if a.registry == nil {
		return result, nil
	}
	if captain, err := isTeamCaptain(ctx, a.db.Pool, boardID, userID); err != nil {
		return BoardPermissions{}, err
	} else if captain {
		result.Permissions = appendPermission(result.Permissions, PermissionTeamManageOwn)
	}

	lister, ok := a.grants.(grantContextLister)
	if !ok {
		return result, nil
	}
	grants, err := lister.ActiveGrantsForUserBoard(ctx, userID, boardID)
	if err != nil {
		return BoardPermissions{}, err
	}
	moduleIndex := make(map[uuid.UUID]int, len(result.Modules))
	for index := range result.Modules {
		moduleIndex[result.Modules[index].ModuleID] = index
	}
	for _, grant := range grants {
		if isParticipationOnlyPermission(grant.Permission) || grant.BoardID == nil {
			continue
		}
		definition, registered := a.registry.Lookup(grant.Permission)
		if !registered {
			continue
		}
		if definition.Source != "core" {
			enabled, err := pluginEnabled(ctx, a.db.Pool, definition.Source)
			if err != nil {
				return BoardPermissions{}, err
			}
			if !enabled {
				continue
			}
		}
		if grant.ModuleID == nil {
			result.Permissions = appendPermission(result.Permissions, grant.Permission)
			for index := range result.Modules {
				result.Modules[index].Permissions = appendPermission(result.Modules[index].Permissions, grant.Permission)
			}
			continue
		}
		index, exists := moduleIndex[*grant.ModuleID]
		if !exists {
			result.Modules = append(result.Modules, ModulePermissions{
				ModuleID:    *grant.ModuleID,
				Permissions: modulePermissions(nil, nil, admin, participant),
			})
			index = len(result.Modules) - 1
			moduleIndex[*grant.ModuleID] = index
		}
		result.Modules[index].Permissions = appendPermission(result.Modules[index].Permissions, grant.Permission)
	}
	return result, nil
}
