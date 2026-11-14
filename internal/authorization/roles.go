package authorization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const (
	AuditActionRoleBindingGrant        = "role_binding.grant"
	AuditActionRoleBindingUpdateExpiry = "role_binding.update_expiry"
	AuditActionRoleBindingRevoke       = "role_binding.revoke"
	auditResourceRoleBinding           = "board_role_binding"
)

// RoleBinding is a scoped role grant. UserUsername/GrantedByUsername are joined
// for HTTP UserSummary projections and are not stored on the binding row.
type RoleBinding struct {
	ID        uuid.UUID
	BoardID   uuid.UUID
	ModuleID  *uuid.UUID
	UserID    uuid.UUID
	Role      Role
	GrantedBy uuid.UUID
	ExpiresAt *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time

	UserUsername      string
	GrantedByUsername string
}

// GrantRoleRequest is the validated input for Grant.
type GrantRoleRequest struct {
	BoardID   uuid.UUID
	ModuleID  *uuid.UUID
	UserID    uuid.UUID
	Role      Role
	ExpiresAt *time.Time
}

// RoleService owns scoped role binding CRUD and its audit writes. Every
// operation authorizes in this business service, not only in HTTP middleware.
type RoleService struct {
	db   *postgres.DB
	auth Authorizer
}

// NewRoleService constructs the live role service.
func NewRoleService(db *postgres.DB, auth Authorizer) *RoleService {
	return &RoleService{db: db, auth: auth}
}

func (s *RoleService) ready() error {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return ErrNoDatabase
	}
	if s.auth == nil {
		return ErrNoAuthorizer
	}
	return nil
}

// Grant creates a role binding and records the grant in audit_events inside the
// same transaction. Owner grants are limited to board_editor/reviewer; only a
// live global admin may grant board_owner.
func (s *RoleService) Grant(ctx context.Context, actor Principal, req GrantRoleRequest) (*RoleBinding, error) {
	if req.BoardID == uuid.Nil {
		return nil, ErrInvalidScope
	}
	if req.UserID == uuid.Nil {
		return nil, ErrUserNotFound
	}

	// Pure input validation happens before the transaction; no database state
	// is consulted outside the board lock.
	role, err := normalizeRole(string(req.Role))
	if err != nil {
		return nil, err
	}
	if role == RoleBoardOwner && req.ModuleID != nil {
		return nil, ErrOwnerModuleGrant
	}

	tx, actorUser, err := s.beginRoleMutation(ctx, actor, req.BoardID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Owner/admin checks use the actor row re-read and locked in this
	// transaction, never Principal.GlobalRoles or an earlier Decision.
	if role == RoleBoardOwner && !actorUser.isAdmin() {
		return nil, ErrGrantNotAllowed
	}
	if req.ModuleID != nil {
		if err := moduleBelongsToBoard(ctx, tx, req.BoardID, *req.ModuleID); err != nil {
			if errors.Is(err, ErrModuleNotFound) {
				return nil, ErrModuleBoardMismatch
			}
			return nil, err
		}
	}

	var targetDisabled bool
	err = tx.QueryRow(ctx, `SELECT disabled FROM users WHERE id = $1 FOR SHARE`, req.UserID).Scan(&targetDisabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authorization: load grant target: %w", err)
	}
	if targetDisabled {
		return nil, ErrTargetUserDisabled
	}

	// Target-user FOR SHARE may have waited. Re-check live actor permission and
	// the requested deadline with the database clock before inserting.
	if err := ensureRoleStaffManageFresh(ctx, s.auth, tx, actorUser, req.BoardID); err != nil {
		return nil, err
	}
	if err := ensureRequestedExpiryFuture(ctx, tx, req.ExpiresAt); err != nil {
		return nil, err
	}

	var bindingID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, req.BoardID, req.ModuleID, req.UserID, string(role), actor.UserID, req.ExpiresAt).Scan(&bindingID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrBindingExists
		}
		return nil, fmt.Errorf("authorization: insert role binding: %w", err)
	}

	// The insert itself may wait on the unique index. Roll back instead of
	// committing an already-expired grant if the actor/request deadline passed
	// during that wait.
	if err := ensureRoleStaffManageFresh(ctx, s.auth, tx, actorUser, req.BoardID); err != nil {
		return nil, err
	}
	if err := ensureRequestedExpiryFuture(ctx, tx, req.ExpiresAt); err != nil {
		return nil, err
	}

	binding, err := loadBinding(ctx, tx, req.BoardID, bindingID)
	if err != nil {
		return nil, err
	}
	details := map[string]any{
		"role":     string(role),
		"userId":   req.UserID.String(),
		"moduleId": moduleIDString(req.ModuleID),
	}
	if req.ExpiresAt != nil {
		details["expiresAt"] = req.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if err := writeAudit(ctx, tx, actor.UserID, req.BoardID, req.ModuleID, AuditActionRoleBindingGrant, auditResourceRoleBinding, &binding.ID, details); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("authorization: commit grant: %w", err)
	}
	return binding, nil
}

// UpdateExpiry changes only expires_at. Role and module are immutable; changing
// them requires revoke + grant. A board_owner binding may only be changed by a
// live global admin.
func (s *RoleService) UpdateExpiry(
	ctx context.Context,
	actor Principal,
	boardID, bindingID uuid.UUID,
	expiresAt *time.Time,
) (*RoleBinding, error) {
	if bindingID == uuid.Nil {
		return nil, ErrBindingNotFound
	}

	tx, actorUser, err := s.beginRoleMutation(ctx, actor, boardID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	binding, err := loadBindingForUpdate(ctx, tx, boardID, bindingID)
	if err != nil {
		return nil, err
	}
	if binding.RevokedAt != nil {
		return nil, ErrBindingNotFound
	}
	if binding.Role == RoleBoardOwner && !actorUser.isAdmin() {
		return nil, ErrGrantNotAllowed
	}
	// Binding FOR UPDATE may have waited. Use the post-lock database clock for
	// both the live actor check and the requested expiry.
	if err := ensureRoleStaffManageFresh(ctx, s.auth, tx, actorUser, boardID); err != nil {
		return nil, err
	}
	if err := ensureRequestedExpiryFuture(ctx, tx, expiresAt); err != nil {
		return nil, err
	}
	var updatedExpiry pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `
		UPDATE board_role_bindings
		SET expires_at = $1
		WHERE id = $2 AND board_id = $3 AND revoked_at IS NULL
		RETURNING expires_at
	`, expiresAt, binding.ID, boardID).Scan(&updatedExpiry); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBindingNotFound
		}
		return nil, fmt.Errorf("authorization: update binding expiry: %w", err)
	}
	if updatedExpiry.Valid {
		value := updatedExpiry.Time
		binding.ExpiresAt = &value
	} else {
		binding.ExpiresAt = nil
	}
	details := map[string]any{
		"role":     string(binding.Role),
		"userId":   binding.UserID.String(),
		"moduleId": moduleIDString(binding.ModuleID),
	}
	if expiresAt != nil {
		details["expiresAt"] = expiresAt.UTC().Format(time.RFC3339)
	} else {
		details["expiresAt"] = nil
	}
	if err := writeAudit(ctx, tx, actor.UserID, boardID, binding.ModuleID, AuditActionRoleBindingUpdateExpiry, auditResourceRoleBinding, &binding.ID, details); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("authorization: commit expiry update: %w", err)
	}
	return binding, nil
}

// Revoke marks a binding revoked. The authorizer's next call already observes
// the revocation because it queries revoked_at on every Authorize call.
func (s *RoleService) Revoke(ctx context.Context, actor Principal, boardID, bindingID uuid.UUID) error {
	if bindingID == uuid.Nil {
		return ErrBindingNotFound
	}

	tx, actorUser, err := s.beginRoleMutation(ctx, actor, boardID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	binding, err := loadBindingForUpdate(ctx, tx, boardID, bindingID)
	if err != nil {
		return err
	}
	if binding.RevokedAt != nil {
		return ErrBindingNotFound
	}
	if binding.Role == RoleBoardOwner && !actorUser.isAdmin() {
		return ErrGrantNotAllowed
	}
	// Binding FOR UPDATE may have waited; do not revoke with an actor binding
	// that naturally expired during that wait.
	if err := ensureRoleStaffManageFresh(ctx, s.auth, tx, actorUser, boardID); err != nil {
		return err
	}
	var revokedAt time.Time
	if err := tx.QueryRow(ctx, `
		UPDATE board_role_bindings
		SET revoked_at = NOW(), revoked_by = $1
		WHERE id = $2 AND board_id = $3 AND revoked_at IS NULL
		RETURNING revoked_at
	`, actor.UserID, binding.ID, boardID).Scan(&revokedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBindingNotFound
		}
		return fmt.Errorf("authorization: revoke binding: %w", err)
	}
	details := map[string]any{
		"role":     string(binding.Role),
		"userId":   binding.UserID.String(),
		"moduleId": moduleIDString(binding.ModuleID),
	}
	if err := writeAudit(ctx, tx, actor.UserID, boardID, binding.ModuleID, AuditActionRoleBindingRevoke, auditResourceRoleBinding, &binding.ID, details); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("authorization: commit revoke: %w", err)
	}
	return nil
}

// List returns every binding for a board, including revoked bindings so the
// admin endpoint can show history. The SQL filters by board_id, not only by the
// binding UUID supplied by a client.
func (s *RoleService) List(ctx context.Context, actor Principal, boardID uuid.UUID) ([]RoleBinding, error) {
	if _, _, err := s.requirePermission(ctx, actor, PermissionBoardStaffManage, boardID); err != nil {
		return nil, err
	}
	bindings, _, err := s.queryBindings(ctx, boardID, 0, 0, false)
	return bindings, err
}

// ListPage is the HTTP pagination helper. It keeps List's exact signature while
// clamping caller-supplied limit/offset.
func (s *RoleService) ListPage(
	ctx context.Context,
	actor Principal,
	boardID uuid.UUID,
	limit, offset int,
) ([]RoleBinding, int, error) {
	if _, _, err := s.requirePermission(ctx, actor, PermissionBoardStaffManage, boardID); err != nil {
		return nil, 0, err
	}
	limit, offset = clampPage(limit, offset)
	return s.queryBindings(ctx, boardID, limit, offset, true)
}
