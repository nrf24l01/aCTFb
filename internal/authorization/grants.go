package authorization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const (
	AuditActionPermissionGrantCreate = "permission_grant.create"
	AuditActionPermissionGrantRevoke = "permission_grant.revoke"
	auditResourcePermissionGrant     = "permission_grant"
	permissionGrantAdvisoryLockKey   = "p1-permission-grants"
)

// PermissionGrant is one explicit supplemental permission grant. Core role
// bindings remain separate; grants never edit the role matrix.
type PermissionGrant struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Permission Permission
	BoardID    *uuid.UUID
	ModuleID   *uuid.UUID
	GrantedBy  uuid.UUID
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

// GrantParams is the validated input for GrantStore.Grant.
type GrantParams struct {
	UserID     uuid.UUID
	Permission Permission
	Scope      Scope
	GrantedBy  uuid.UUID
	ExpiresAt  *time.Time
}

// PermissionGrantChecker is the minimal live grant lookup used by Authorize.
type PermissionGrantChecker interface {
	HasGrant(ctx context.Context, userID uuid.UUID, permission Permission, scope Scope) (bool, error)
}

// GrantStore implements live permission-grant storage and lookup.
type GrantStore struct {
	db       *postgres.DB
	registry PermissionRegistry
}

// GrantStoreOption configures a GrantStore.
type GrantStoreOption func(*GrantStore)

// WithGrantRegistry uses the same registry as the authorizer/plugin manager.
func WithGrantRegistry(registry PermissionRegistry) GrantStoreOption {
	return func(store *GrantStore) {
		if registry != nil {
			store.registry = registry
		}
	}
}

// NewGrantStore constructs a grant store. The default registry contains core
// permissions; plugin grants require WithGrantRegistry(pluginRegistry).
func NewGrantStore(db *postgres.DB, opts ...GrantStoreOption) *GrantStore {
	store := &GrantStore{db: db, registry: NewRegistry()}
	for _, option := range opts {
		if option != nil {
			option(store)
		}
	}
	return store
}

var _ PermissionGrantChecker = (*GrantStore)(nil)

// HasGrant returns true only for an active grant that covers the requested
// scope. Expiry is evaluated with clock_timestamp(); revocation is live.
func (s *GrantStore) HasGrant(ctx context.Context, userID uuid.UUID, permission Permission, scope Scope) (bool, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return false, ErrNoDatabase
	}
	if userID == uuid.Nil || permission == "" {
		return false, nil
	}

	var exists bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM permission_grants
			WHERE user_id = $1
			  AND permission = $2
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at > clock_timestamp())
			  AND (
				($3::uuid IS NULL AND board_id IS NULL AND module_id IS NULL)
				OR ($3::uuid IS NOT NULL AND board_id = $3 AND (
					($4::uuid IS NULL AND module_id IS NULL)
					OR ($4::uuid IS NOT NULL AND (module_id IS NULL OR module_id = $4))
				))
			  )
		)
	`, userID, string(permission), nullableScopeUUID(scope.BoardID), nullableScopePointer(scope.ModuleID)).Scan(&exists)
	if err != nil {
		if isUndefinedTable(err) {
			// Source-compatible with isolated P1 schemas that applied only 001-006.
			return false, nil
		}
		return false, fmt.Errorf("authorization: check permission grant: %w", err)
	}
	return exists, nil
}

// ActiveGrantsForUserBoard returns global and board-scoped active grants for the
// permission-context endpoint. Module filtering is applied by the caller.
func (s *GrantStore) ActiveGrantsForUserBoard(ctx context.Context, userID, boardID uuid.UUID) ([]PermissionGrant, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, user_id, permission, board_id, module_id, granted_by, created_at, expires_at, revoked_at
		FROM permission_grants
		WHERE user_id = $1
		  AND (board_id IS NULL OR board_id = $2)
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > clock_timestamp())
		ORDER BY created_at DESC, id
	`, userID, boardID)
	if err != nil {
		if isUndefinedTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("authorization: list permission grants for context: %w", err)
	}
	defer rows.Close()
	return scanGrants(rows)
}

// Grant writes one explicit grant and its audit row atomically. Only a live
// global admin may grant; the actor row is re-read and locked in this
// transaction, never trusted from Principal.GlobalRoles.
func (s *GrantStore) Grant(ctx context.Context, p GrantParams) (*PermissionGrant, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, ErrNoDatabase
	}
	if s.registry == nil {
		return nil, ErrNoAuthorizer
	}
	if p.UserID == uuid.Nil {
		return nil, ErrUserNotFound
	}
	if p.GrantedBy == uuid.Nil {
		return nil, ErrUnauthorized
	}
	definition, ok := s.registry.Lookup(p.Permission)
	if !ok {
		return nil, ErrPermissionNotRegistered
	}
	scope := p.Scope
	if scope.ModuleID != nil && scope.BoardID == uuid.Nil {
		return nil, ErrGrantScopeNotAllowed
	}
	permissionScope := PermissionScopeGlobal
	switch {
	case scope.BoardID == uuid.Nil:
		permissionScope = PermissionScopeGlobal
	case scope.ModuleID == nil:
		permissionScope = PermissionScopeBoard
	default:
		permissionScope = PermissionScopeModule
	}
	if !DefinitionAllowsScope(definition, permissionScope) {
		return nil, ErrGrantScopeNotAllowed
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("authorization: begin permission grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
	`, permissionGrantAdvisoryLockKey); err != nil {
		return nil, fmt.Errorf("authorization: lock permission grants: %w", err)
	}

	actor, err := loadLiveUserForShare(ctx, tx, p.GrantedBy)
	if err != nil {
		return nil, err
	}
	if !actor.isAdmin() {
		return nil, ErrForbidden
	}
	if scope.BoardID != uuid.Nil {
		if err := boardExists(ctx, tx, scope.BoardID); err != nil {
			return nil, err
		}
		if scope.ModuleID != nil {
			if err := moduleBelongsToBoard(ctx, tx, scope.BoardID, *scope.ModuleID); err != nil {
				return nil, err
			}
		}
	}

	var targetDisabled bool
	err = tx.QueryRow(ctx, `SELECT disabled FROM users WHERE id = $1 FOR SHARE`, p.UserID).Scan(&targetDisabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authorization: load permission grant target: %w", err)
	}
	if targetDisabled {
		return nil, ErrTargetUserDisabled
	}
	// Target lock may have waited; revalidate the requested deadline with the
	// database wall clock, not transaction-start NOW() or Go time.
	if err := ensureRequestedExpiryFuture(ctx, tx, p.ExpiresAt); err != nil {
		return nil, err
	}

	var grant PermissionGrant
	var boardID, moduleID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO permission_grants (user_id, permission, board_id, module_id, granted_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, permission, board_id, module_id, granted_by, created_at, expires_at, revoked_at
	`, p.UserID, string(p.Permission), nullableScopeUUID(scope.BoardID), nullableScopePointer(scope.ModuleID), p.GrantedBy, p.ExpiresAt).
		Scan(&grant.ID, &grant.UserID, (*string)(&grant.Permission), &boardID, &moduleID, &grant.GrantedBy, &grant.CreatedAt, &grant.ExpiresAt, &grant.RevokedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrGrantExists
		}
		return nil, fmt.Errorf("authorization: insert permission grant: %w", err)
	}
	grant.BoardID = uuidFromPGType(boardID)
	grant.ModuleID = uuidFromPGType(moduleID)

	// The insert can wait on the active unique index; reject an already-expired
	// requested deadline before audit/commit instead of persisting it.
	if err := ensureRequestedExpiryFuture(ctx, tx, p.ExpiresAt); err != nil {
		return nil, err
	}
	details := map[string]any{
		"permission": string(p.Permission),
		"userId":     p.UserID.String(),
		"boardId":    uuidStringValue(grant.BoardID),
		"moduleId":   uuidStringValue(grant.ModuleID),
	}
	if p.ExpiresAt != nil {
		details["expiresAt"] = p.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if err := writeNullableAudit(ctx, tx, p.GrantedBy, grant.BoardID, grant.ModuleID, AuditActionPermissionGrantCreate, auditResourcePermissionGrant, &grant.ID, details); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("authorization: commit permission grant: %w", err)
	}
	return &grant, nil
}

// Revoke marks one grant revoked and writes the audit row in the same
// transaction. Only a live global admin may revoke.
func (s *GrantStore) Revoke(ctx context.Context, actor Principal, grantID uuid.UUID) error {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return ErrNoDatabase
	}
	if grantID == uuid.Nil {
		return ErrGrantNotFound
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("authorization: begin permission revoke: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
	`, permissionGrantAdvisoryLockKey); err != nil {
		return fmt.Errorf("authorization: lock permission grants: %w", err)
	}
	liveActor, err := loadLiveUserForShare(ctx, tx, actor.UserID)
	if err != nil {
		return err
	}
	if !liveActor.isAdmin() {
		return ErrForbidden
	}

	var grant PermissionGrant
	var boardID, moduleID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, permission, board_id, module_id, granted_by, created_at, expires_at, revoked_at
		FROM permission_grants
		WHERE id = $1
		FOR UPDATE
	`, grantID).Scan(&grant.ID, &grant.UserID, (*string)(&grant.Permission), &boardID, &moduleID, &grant.GrantedBy, &grant.CreatedAt, &grant.ExpiresAt, &grant.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrGrantNotFound
	}
	if err != nil {
		return fmt.Errorf("authorization: load permission grant: %w", err)
	}
	if grant.RevokedAt != nil {
		return ErrGrantNotFound
	}
	grant.BoardID = uuidFromPGType(boardID)
	grant.ModuleID = uuidFromPGType(moduleID)

	var revokedAt time.Time
	if err := tx.QueryRow(ctx, `
		UPDATE permission_grants SET revoked_at = NOW() WHERE id = $1 RETURNING revoked_at
	`, grantID).Scan(&revokedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGrantNotFound
		}
		return fmt.Errorf("authorization: revoke permission grant: %w", err)
	}
	details := map[string]any{
		"permission": string(grant.Permission),
		"userId":     grant.UserID.String(),
		"boardId":    uuidStringValue(grant.BoardID),
		"moduleId":   uuidStringValue(grant.ModuleID),
	}
	if err := writeNullableAudit(ctx, tx, actor.UserID, grant.BoardID, grant.ModuleID, AuditActionPermissionGrantRevoke, auditResourcePermissionGrant, &grant.ID, details); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("authorization: commit permission revoke: %w", err)
	}
	return nil
}

// List returns grants for the global-admin permission-grant endpoint.
func (s *GrantStore) List(ctx context.Context, actor Principal, limit, offset int) ([]PermissionGrant, int, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, 0, ErrNoDatabase
	}
	user, err := loadLiveUser(ctx, s.db.Pool, actor.UserID)
	if err != nil {
		return nil, 0, err
	}
	if !user.isAdmin() {
		return nil, 0, ErrForbidden
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, user_id, permission, board_id, module_id, granted_by, created_at, expires_at, revoked_at
		FROM permission_grants
		ORDER BY created_at DESC, id
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("authorization: list permission grants: %w", err)
	}
	defer rows.Close()
	grants, err := scanGrants(rows)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM permission_grants`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("authorization: count permission grants: %w", err)
	}
	return grants, total, nil
}

func scanGrants(rows pgx.Rows) ([]PermissionGrant, error) {
	grants := make([]PermissionGrant, 0)
	for rows.Next() {
		var grant PermissionGrant
		var boardID, moduleID pgtype.UUID
		if err := rows.Scan(&grant.ID, &grant.UserID, (*string)(&grant.Permission), &boardID, &moduleID, &grant.GrantedBy, &grant.CreatedAt, &grant.ExpiresAt, &grant.RevokedAt); err != nil {
			return nil, fmt.Errorf("authorization: scan permission grant: %w", err)
		}
		grant.BoardID = uuidFromPGType(boardID)
		grant.ModuleID = uuidFromPGType(moduleID)
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("authorization: iterate permission grants: %w", err)
	}
	return grants, nil
}

func writeNullableAudit(
	ctx context.Context,
	tx pgx.Tx,
	actorID uuid.UUID,
	boardID, moduleID *uuid.UUID,
	action, resourceType string,
	resourceID *uuid.UUID,
	details map[string]any,
) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("authorization: encode audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, module_id, action, resource_type, resource_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, actorID, boardID, moduleID, action, resourceType, resourceID, payload); err != nil {
		return fmt.Errorf("authorization: write audit event: %w", err)
	}
	return nil
}

func nullableScopeUUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}

func nullableScopePointer(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return *value
}

func uuidFromPGType(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}

func uuidStringValue(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return value.String()
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}
