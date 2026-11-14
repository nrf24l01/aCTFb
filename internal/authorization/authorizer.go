package authorization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// Scope identifies the exact board/module pair being authorized. ModuleID is
// optional; when present it must belong to BoardID, even for a global admin.
type Scope struct {
	BoardID  uuid.UUID
	ModuleID *uuid.UUID
}

// Decision is the effective result of one authorization check.
type Decision struct {
	Allowed     bool
	Roles       []Role
	Permissions []Permission
	Reason      string
}

// BoardPermissions is the effective context returned by
// GET /api/v2/boards/{boardUuid}/me/permissions.
type BoardPermissions struct {
	BoardID     uuid.UUID
	Roles       []Role
	Permissions []Permission
	Modules     []ModulePermissions
}

// ModulePermissions is the effective module-scoped context for one module.
type ModulePermissions struct {
	ModuleID    uuid.UUID
	Roles       []Role
	Permissions []Permission
}

// Authorizer is the single authorization contract used by HTTP adapters and
// business services.
type Authorizer interface {
	Authorize(ctx context.Context, principal Principal, permission Permission, scope Scope) (Decision, error)
	Permissions(ctx context.Context, principal Principal, boardID uuid.UUID) (BoardPermissions, error)
}

// AuthorizerService implements Authorizer from live PostgreSQL state.
type AuthorizerService struct {
	db       *postgres.DB
	registry PermissionRegistry
	grants   PermissionGrantChecker
}

// AuthorizerOption configures registry/grant sources without breaking the
// existing NewAuthorizer(db) call sites.
type AuthorizerOption func(*AuthorizerService)

// WithPermissionRegistry replaces the default core registry (for plugin-aware
// boot or tests).
func WithPermissionRegistry(registry PermissionRegistry) AuthorizerOption {
	return func(service *AuthorizerService) {
		if registry != nil {
			service.registry = registry
		}
	}
}

// WithGrantChecker replaces the default live permission-grant lookup.
func WithGrantChecker(checker PermissionGrantChecker) AuthorizerOption {
	return func(service *AuthorizerService) {
		if checker != nil {
			service.grants = checker
		}
	}
}

// NewAuthorizer constructs the live authorizer. The default registry contains
// all core permissions and the default grant checker reads permission_grants
// live. Unknown permissions are denied.
func NewAuthorizer(db *postgres.DB, opts ...AuthorizerOption) *AuthorizerService {
	service := &AuthorizerService{db: db, registry: NewRegistry()}
	for _, option := range opts {
		if option != nil {
			option(service)
		}
	}
	if service.registry == nil {
		service.registry = NewRegistry()
	}
	if service.grants == nil {
		service.grants = NewGrantStore(db, WithGrantRegistry(service.registry))
	}
	return service
}

var _ Authorizer = (*AuthorizerService)(nil)

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type rowsQuerier interface {
	rowQuerier
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type liveUser struct {
	ID       uuid.UUID
	Disabled bool
	Roles    []string
}

func (u liveUser) isAdmin() bool {
	return isAdminRoles(u.Roles)
}

func loadLiveUser(ctx context.Context, q rowQuerier, userID uuid.UUID) (liveUser, error) {
	if userID == uuid.Nil {
		return liveUser{}, ErrUnauthorized
	}
	return scanLiveUser(q.QueryRow(ctx, `
		SELECT id, disabled, COALESCE(roles, ARRAY['user']::text[])
		FROM users
		WHERE id = $1
	`, userID))
}

// loadLiveUserForShare locks the user row FOR SHARE. Role mutations use this
// inside their transaction so a concurrent external disabled/admin-role update
// cannot commit between the authoritative check and the mutation.
func loadLiveUserForShare(ctx context.Context, q rowQuerier, userID uuid.UUID) (liveUser, error) {
	if userID == uuid.Nil {
		return liveUser{}, ErrUnauthorized
	}
	return scanLiveUser(q.QueryRow(ctx, `
		SELECT id, disabled, COALESCE(roles, ARRAY['user']::text[])
		FROM users
		WHERE id = $1
		FOR SHARE
	`, userID))
}

func scanLiveUser(row pgx.Row) (liveUser, error) {
	var user liveUser
	err := row.Scan(&user.ID, &user.Disabled, &user.Roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return liveUser{}, ErrUnauthorized
	}
	if err != nil {
		return liveUser{}, fmt.Errorf("authorization: load live user: %w", err)
	}
	if user.Disabled {
		return liveUser{}, ErrUserDisabled
	}
	return user, nil
}

func boardExists(ctx context.Context, q rowQuerier, boardID uuid.UUID) error {
	if boardID == uuid.Nil {
		return ErrInvalidScope
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM boards WHERE id = $1)`, boardID).Scan(&exists); err != nil {
		return fmt.Errorf("authorization: check board: %w", err)
	}
	if !exists {
		return ErrBoardNotFound
	}
	return nil
}

func moduleBelongsToBoard(ctx context.Context, q rowQuerier, boardID, moduleID uuid.UUID) error {
	if boardID == uuid.Nil || moduleID == uuid.Nil {
		return ErrInvalidScope
	}
	var exists bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM modules WHERE id = $1 AND board_id = $2
		)
	`, moduleID, boardID).Scan(&exists); err != nil {
		return fmt.Errorf("authorization: check module parent: %w", err)
	}
	if !exists {
		return ErrModuleNotFound
	}
	return nil
}

type bindingRow struct {
	Role      Role
	ModuleID  *uuid.UUID
	ExpiresAt *time.Time
}

// loadActiveBindings reads the actor's active bindings and locks the matching
// rows FOR SHARE. expiries are carried out of the locked records and filtered
// again against clock_timestamp() after the rows have been acquired, because a
// FOR SHARE wait can finish after the WHERE predicate was evaluated.
func loadActiveBindings(ctx context.Context, q rowsQuerier, userID, boardID uuid.UUID) ([]bindingRow, error) {
	rows, err := q.Query(ctx, `
		SELECT role, module_id, expires_at
		FROM board_role_bindings
		WHERE user_id = $1
		  AND board_id = $2
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > clock_timestamp())
		FOR SHARE
	`, userID, boardID)
	if err != nil {
		return nil, fmt.Errorf("authorization: load active bindings: %w", err)
	}
	defer rows.Close()

	bindings := make([]bindingRow, 0, 4)
	for rows.Next() {
		var rawRole string
		var moduleID pgtype.UUID
		var expiresAt pgtype.Timestamptz
		if err := rows.Scan(&rawRole, &moduleID, &expiresAt); err != nil {
			return nil, fmt.Errorf("authorization: scan active binding: %w", err)
		}
		role, ok := ParseRole(rawRole)
		if !ok {
			// A corrupt/unknown role grants nothing.
			continue
		}
		binding := bindingRow{Role: role}
		if moduleID.Valid {
			id := uuid.UUID(moduleID.Bytes)
			binding.ModuleID = &id
		}
		if expiresAt.Valid {
			value := expiresAt.Time
			binding.ExpiresAt = &value
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("authorization: iterate active bindings: %w", err)
	}
	if len(bindings) == 0 {
		return bindings, nil
	}

	// NOW() is pinned to transaction start and can predate a long advisory/user/
	// row lock wait. clock_timestamp() is the actual database wall clock at this
	// point, after the locked binding rows were acquired.
	freshNow, err := freshDBTime(ctx, q)
	if err != nil {
		return nil, err
	}
	fresh := bindings[:0]
	for _, binding := range bindings {
		if binding.ExpiresAt != nil && !binding.ExpiresAt.After(freshNow) {
			continue
		}
		fresh = append(fresh, binding)
	}
	return fresh, nil
}

// freshDBTime returns clock_timestamp(), not transaction-start NOW().
func freshDBTime(ctx context.Context, q rowQuerier) (time.Time, error) {
	var now time.Time
	if err := q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("authorization: read database clock: %w", err)
	}
	return now, nil
}

func isParticipant(ctx context.Context, q rowQuerier, userID, boardID uuid.UUID) (bool, error) {
	var joined bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM board_participants
			WHERE board_id = $1 AND user_id = $2
		)
	`, boardID, userID).Scan(&joined); err != nil {
		return false, fmt.Errorf("authorization: check participation: %w", err)
	}
	return joined, nil
}

func splitRoles(bindings []bindingRow, scopeModuleID *uuid.UUID) (boardRoles []Role, moduleRoles []Role) {
	boardSet := make(map[Role]struct{}, 3)
	moduleSet := make(map[Role]struct{}, 3)
	for _, binding := range bindings {
		if binding.ModuleID == nil {
			boardSet[binding.Role] = struct{}{}
			continue
		}
		if scopeModuleID != nil && *binding.ModuleID == *scopeModuleID {
			moduleSet[binding.Role] = struct{}{}
		}
	}
	return sortedRoles(boardSet), sortedRoles(moduleSet)
}

func mergeRoles(boardRoles, moduleRoles []Role) []Role {
	set := make(map[Role]struct{}, len(boardRoles)+len(moduleRoles))
	for _, role := range boardRoles {
		set[role] = struct{}{}
	}
	for _, role := range moduleRoles {
		set[role] = struct{}{}
	}
	return sortedRoles(set)
}

// Authorize checks one permission against live database state. Denials return a
// Decision with Allowed=false and a nil error; storage/programming failures
// return typed errors so callers fail closed.
func (a *AuthorizerService) Authorize(ctx context.Context, principal Principal, permission Permission, scope Scope) (Decision, error) {
	if a == nil || a.db == nil || a.db.Pool == nil {
		return Decision{}, ErrNoDatabase
	}
	if principal.UserID == uuid.Nil {
		return Decision{}, ErrUnauthorized
	}

	user, err := loadLiveUser(ctx, a.db.Pool, principal.UserID)
	if err != nil {
		return Decision{}, err
	}
	return a.decide(ctx, a.db.Pool, user, permission, scope)
}

// decide validates the registry, evaluates the core role matrix/participation,
// team-captain semantics, then explicit grants. It is transaction-aware so role
// mutation guards can reuse the same authoritative check.
func (a *AuthorizerService) decide(
	ctx context.Context,
	q rowsQuerier,
	user liveUser,
	permission Permission,
	scope Scope,
) (Decision, error) {
	if a == nil || a.registry == nil {
		return Decision{}, ErrNoAuthorizer
	}
	definition, ok := a.registry.Lookup(permission)
	if !ok {
		return Decision{}, ErrInvalidPermission
	}
	if definition.Source != "core" {
		return a.decidePlugin(ctx, q, user, definition, scope)
	}
	var decision Decision
	if scope.BoardID == uuid.Nil && !isGlobalPermission(permission) {
		if !DefinitionAllowsScope(definition, PermissionScopeGlobal) {
			return Decision{}, ErrInvalidScope
		}
		decision = Decision{Reason: "denied"}
	} else {
		var err error
		decision, err = effectiveCoreDecision(ctx, q, user, permission, scope)
		if err != nil {
			return Decision{}, err
		}
	}
	if permission == PermissionTeamManageOwn {
		// Own-team management is captain-only, never all participants and never
		// satisfied by an explicit grant.
		captain, err := isTeamCaptain(ctx, q, scope.BoardID, user.ID)
		if err != nil {
			return Decision{}, err
		}
		decision.Allowed = captain
		if captain {
			decision.Permissions = appendPermission(decision.Permissions, permission)
			decision.Reason = "allowed"
		} else {
			decision.Reason = "denied"
		}
		return decision, nil
	}
	if a.grants != nil && !isParticipationOnlyPermission(permission) {
		hasGrant, err := a.grants.HasGrant(ctx, user.ID, permission, scope)
		if err != nil {
			return Decision{}, err
		}
		if hasGrant {
			decision.Allowed = true
			decision.Permissions = appendPermission(decision.Permissions, permission)
			decision.Reason = "allowed"
		}
	}
	return decision, nil
}

// decidePlugin enforces the master rule that plugin permissions are effective
// only through an explicit live grant and only while the owning plugin is
// enabled. The native admin role matrix is deliberately skipped even for a
// global admin; core plugins.manage remains a core global permission.
func (a *AuthorizerService) decidePlugin(
	ctx context.Context,
	q rowsQuerier,
	user liveUser,
	definition PermissionDefinition,
	scope Scope,
) (Decision, error) {
	switch {
	case scope.BoardID == uuid.Nil:
		if !DefinitionAllowsScope(definition, PermissionScopeGlobal) {
			return Decision{}, ErrInvalidScope
		}
	case scope.ModuleID == nil:
		if !DefinitionAllowsScope(definition, PermissionScopeBoard) {
			return Decision{}, ErrInvalidScope
		}
		if err := boardExists(ctx, q, scope.BoardID); err != nil {
			return Decision{}, err
		}
	default:
		if !DefinitionAllowsScope(definition, PermissionScopeModule) {
			return Decision{}, ErrInvalidScope
		}
		if err := boardExists(ctx, q, scope.BoardID); err != nil {
			return Decision{}, err
		}
		if err := moduleBelongsToBoard(ctx, q, scope.BoardID, *scope.ModuleID); err != nil {
			return Decision{}, err
		}
	}

	enabled, err := pluginEnabled(ctx, q, definition.Source)
	if err != nil {
		return Decision{}, err
	}
	if !enabled {
		return Decision{Allowed: false, Reason: "plugin disabled"}, nil
	}
	allowed := false
	if a.grants != nil {
		allowed, err = a.grants.HasGrant(ctx, user.ID, definition.Permission, scope)
		if err != nil {
			return Decision{}, err
		}
	}
	decision := Decision{Allowed: allowed, Reason: decisionReason(allowed)}
	if allowed {
		decision.Permissions = []Permission{definition.Permission}
	}
	return decision, nil
}

// pluginEnabled reads only the tiny plugin status row for plugin permissions.
// Core P1 checks never execute this query. A missing table or plugin row fails
// closed.
func pluginEnabled(ctx context.Context, q rowQuerier, pluginID string) (bool, error) {
	if pluginID == "" {
		return false, nil
	}
	var enabled bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM plugins WHERE id = $1 AND status = 'enabled'
		)
	`, pluginID).Scan(&enabled)
	if err != nil {
		if isUndefinedTable(err) {
			return false, nil
		}
		return false, fmt.Errorf("authorization: check plugin enabled: %w", err)
	}
	return enabled, nil
}

// effectiveDecision is the source-compatible core-only fallback used by role
// mutation callers that provide a plain Authorizer/test double.
func effectiveDecision(
	ctx context.Context,
	q rowsQuerier,
	user liveUser,
	permission Permission,
	scope Scope,
) (Decision, error) {
	if !ValidPermission(permission) {
		return Decision{}, ErrInvalidPermission
	}
	return effectiveCoreDecision(ctx, q, user, permission, scope)
}

// effectiveCoreDecision evaluates the core role matrix and live participation.
// Plugin permissions are allowed only through explicit grants later.
func effectiveCoreDecision(
	ctx context.Context,
	q rowsQuerier,
	user liveUser,
	permission Permission,
	scope Scope,
) (Decision, error) {
	admin := user.isAdmin()

	if isGlobalPermission(permission) {
		permissions := globalPermissionSet(admin)
		allowed := containsPermission(permissions, permission)
		return Decision{
			Allowed:     allowed,
			Permissions: permissions,
			Reason:      decisionReason(allowed),
		}, nil
	}

	if scope.BoardID == uuid.Nil {
		return Decision{}, ErrInvalidScope
	}
	// Parent integrity is checked before evaluating any role, including admin.
	if err := boardExists(ctx, q, scope.BoardID); err != nil {
		return Decision{}, err
	}
	if scope.ModuleID != nil {
		if err := moduleBelongsToBoard(ctx, q, scope.BoardID, *scope.ModuleID); err != nil {
			return Decision{}, err
		}
	}

	bindings, err := loadActiveBindings(ctx, q, user.ID, scope.BoardID)
	if err != nil {
		return Decision{}, err
	}
	participant, err := isParticipant(ctx, q, user.ID, scope.BoardID)
	if err != nil {
		return Decision{}, err
	}

	boardRoles, moduleRoles := splitRoles(bindings, scope.ModuleID)
	var permissions []Permission
	if scope.ModuleID == nil {
		permissions = boardPermissions(boardRoles, admin, participant)
	} else {
		permissions = modulePermissions(boardRoles, moduleRoles, admin, participant)
	}

	allowed := containsPermission(permissions, permission)
	return Decision{
		Allowed:     allowed,
		Roles:       mergeRoles(boardRoles, moduleRoles),
		Permissions: permissions,
		Reason:      decisionReason(allowed),
	}, nil
}

// Permissions returns the effective board and module context for a principal.
// Module-scoped actors only receive their assigned modules; board-wide actors
// and admins receive every module in the board.

func containsPermission(permissions []Permission, target Permission) bool {
	for _, permission := range permissions {
		if permission == target {
			return true
		}
	}
	return false
}

func appendPermission(permissions []Permission, permission Permission) []Permission {
	if containsPermission(permissions, permission) {
		return permissions
	}
	set := make(map[Permission]struct{}, len(permissions)+1)
	for _, existing := range permissions {
		set[existing] = struct{}{}
	}
	set[permission] = struct{}{}
	return sortedPermissions(set)
}

// isParticipationOnlyPermission cannot be satisfied by an explicit grant; the
// business endpoint must also verify current participation and ABAC.
func isParticipationOnlyPermission(permission Permission) bool {
	switch permission {
	case PermissionSubmissionCreateOwn,
		PermissionSubmissionReadOwn,
		PermissionInstanceStartOwn,
		PermissionInstanceManageOwn,
		PermissionTeamManageOwn:
		return true
	default:
		return false
	}
}

// isTeamCaptain is true only for the current captain of a team on this board.
// A missing teams table is tolerated for source-compatible isolated P1 schemas.
func isTeamCaptain(ctx context.Context, q rowQuerier, boardID, userID uuid.UUID) (bool, error) {
	if boardID == uuid.Nil || userID == uuid.Nil {
		return false, nil
	}
	var captain bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM teams
			WHERE board_id = $1 AND captain_user_id = $2
		)
	`, boardID, userID).Scan(&captain)
	if err != nil {
		if isUndefinedTable(err) {
			return false, nil
		}
		return false, fmt.Errorf("authorization: check team captain: %w", err)
	}
	return captain, nil
}

func decisionReason(allowed bool) string {
	if allowed {
		return "allowed"
	}
	return "denied"
}

// normalizeRole is used by the role service for request validation while
// keeping the catalog's role spelling authoritative.
func normalizeRole(raw string) (Role, error) {
	role, ok := ParseRole(strings.TrimSpace(raw))
	if !ok {
		return "", ErrInvalidRole
	}
	return role, nil
}
