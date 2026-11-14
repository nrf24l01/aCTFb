package plugins

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// GrantInput is one explicit plugin permission grant. It is valid only for a
// permission declared by that exact plugin and a scope the manifest registered.
type GrantInput struct {
	UserID     uuid.UUID
	Permission authorization.Permission
	BoardID    *uuid.UUID
	ModuleID   *uuid.UUID
	ExpiresAt  *time.Time
}

// ListPermissionGrants returns only grants in this plugin's namespace. The
// underlying permission_grants table is shared, but this endpoint never exposes
// another plugin's or a core grant.
func (m *Manager) ListPermissionGrants(ctx context.Context, principal authorization.Principal, id ID, limit, offset int) ([]authorization.PermissionGrant, int, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, 0, err
	}
	if _, err := m.loadInstalled(ctx, id); err != nil {
		return nil, 0, err
	}
	limit, offset = clampPage(limit, offset)
	prefix := "plugin." + string(id) + "."
	rows, err := m.db.Pool.Query(ctx, `
		SELECT id, user_id, permission, board_id, module_id, granted_by, created_at, expires_at, revoked_at
		FROM permission_grants
		WHERE permission LIKE $1 ESCAPE '\'
		ORDER BY created_at DESC, id
		LIMIT $2 OFFSET $3
	`, escapeLike(prefix)+"%", limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("plugins: list permission grants: %w", err)
	}
	defer rows.Close()
	grants := make([]authorization.PermissionGrant, 0)
	for rows.Next() {
		var grant authorization.PermissionGrant
		var boardID, moduleID pgtype.UUID
		if err := rows.Scan(&grant.ID, &grant.UserID, (*string)(&grant.Permission), &boardID, &moduleID,
			&grant.GrantedBy, &grant.CreatedAt, &grant.ExpiresAt, &grant.RevokedAt); err != nil {
			return nil, 0, err
		}
		grant.BoardID = uuidFromPG(boardID)
		grant.ModuleID = uuidFromPG(moduleID)
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := m.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM permission_grants WHERE permission LIKE $1 ESCAPE '\'
	`, escapeLike(prefix)+"%").Scan(&total); err != nil {
		return nil, 0, err
	}
	return grants, total, nil
}

// CreatePermissionGrant delegates the write to authorization.GrantStore after
// checking the permission belongs to this plugin and the scope is declared.
func (m *Manager) CreatePermissionGrant(ctx context.Context, principal authorization.Principal, id ID, in GrantInput) (*authorization.PermissionGrant, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, err
	}
	if err := m.validateGrantTarget(ctx, id, in); err != nil {
		return nil, err
	}
	scope := authorization.Scope{}
	if in.BoardID != nil {
		scope.BoardID = *in.BoardID
	}
	scope.ModuleID = in.ModuleID
	grant, err := m.grants.Grant(ctx, authorization.GrantParams{
		UserID:     in.UserID,
		Permission: in.Permission,
		Scope:      scope,
		GrantedBy:  principal.UserID,
		ExpiresAt:  in.ExpiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("plugins: create permission grant: %w", err)
	}
	return grant, nil
}

// RevokePermissionGrant revokes only a grant that belongs to this plugin's
// permission namespace. The authorization store performs the admin/audit write.
func (m *Manager) RevokePermissionGrant(ctx context.Context, principal authorization.Principal, id ID, grantID uuid.UUID) error {
	if err := m.requireManage(ctx, principal); err != nil {
		return err
	}
	var permission string
	err := m.db.Pool.QueryRow(ctx, `
		SELECT permission FROM permission_grants WHERE id = $1
	`, grantID).Scan(&permission)
	if errors.Is(err, pgx.ErrNoRows) {
		return authorization.ErrGrantNotFound
	}
	if err != nil {
		return fmt.Errorf("plugins: load permission grant: %w", err)
	}
	if !strings.HasPrefix(permission, "plugin."+string(id)+".") {
		return authorization.ErrGrantNotFound
	}
	if err := m.grants.Revoke(ctx, principal, grantID); err != nil {
		return fmt.Errorf("plugins: revoke permission grant: %w", err)
	}
	return nil
}

func (m *Manager) validateGrantTarget(ctx context.Context, id ID, in GrantInput) error {
	if in.UserID == uuid.Nil {
		return fmt.Errorf("%w: userUuid is required", ErrValidation)
	}
	permission := string(in.Permission)
	if !strings.HasPrefix(permission, "plugin."+string(id)+".") {
		return fmt.Errorf("%w: permission must be namespaced under this plugin", ErrValidation)
	}
	definition, registered := m.registry.Lookup(in.Permission)
	if !registered || definition.Source != string(id) {
		return fmt.Errorf("%w: permission is not registered by this plugin", ErrValidation)
	}
	var scope authorization.PermissionScope
	switch {
	case in.BoardID == nil && in.ModuleID == nil:
		scope = authorization.PermissionScopeGlobal
	case in.BoardID != nil && in.ModuleID == nil:
		scope = authorization.PermissionScopeBoard
	case in.BoardID != nil && in.ModuleID != nil:
		scope = authorization.PermissionScopeModule
	default:
		return fmt.Errorf("%w: moduleUuid requires boardUuid", ErrValidation)
	}
	if !authorization.DefinitionAllowsScope(definition, scope) {
		return fmt.Errorf("%w: scope is not declared for this permission", ErrValidation)
	}
	var exists bool
	if err := m.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM plugin_permissions WHERE plugin_id = $1 AND permission = $2
		)
	`, string(id), permission).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: permission is not declared by the installed manifest", ErrValidation)
	}
	return nil
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}
