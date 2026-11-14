package authorization

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *RoleService) requirePermission(
	ctx context.Context,
	actor Principal,
	permission Permission,
	boardID uuid.UUID,
) (Decision, liveUser, error) {
	if err := s.ready(); err != nil {
		return Decision{}, liveUser{}, err
	}
	if boardID == uuid.Nil {
		return Decision{}, liveUser{}, ErrInvalidScope
	}
	user, err := loadLiveUser(ctx, s.db.Pool, actor.UserID)
	if err != nil {
		return Decision{}, liveUser{}, err
	}
	decision, err := s.auth.Authorize(ctx, actor, permission, Scope{BoardID: boardID})
	if err != nil {
		return Decision{}, liveUser{}, err
	}
	if !decision.Allowed {
		return decision, user, ErrForbidden
	}
	return decision, user, nil
}

const roleAdvisoryLockPrefix = "p1-roles:"

// beginRoleMutation serializes every scoped role mutation for one board, then
// performs the authoritative authorization inside that same transaction.
//
// Lock order is fixed: board advisory lock -> actor users row FOR SHARE ->
// target users row FOR SHARE (Grant) / binding row FOR UPDATE (Update/Revoke).
// An external UPDATE users or role-binding write therefore cannot interleave
// between the live check and the mutation.
func (s *RoleService) beginRoleMutation(
	ctx context.Context,
	actor Principal,
	boardID uuid.UUID,
) (pgx.Tx, liveUser, error) {
	if err := s.ready(); err != nil {
		return nil, liveUser{}, err
	}
	if boardID == uuid.Nil {
		return nil, liveUser{}, ErrInvalidScope
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, liveUser{}, fmt.Errorf("authorization: begin role mutation: %w", err)
	}
	fail := func(err error) (pgx.Tx, liveUser, error) {
		_ = tx.Rollback(ctx)
		return nil, liveUser{}, err
	}

	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
	`, roleAdvisoryLockPrefix+boardID.String()); err != nil {
		return fail(fmt.Errorf("authorization: lock board role mutations: %w", err))
	}

	// Re-read the actor inside the lock. Principal.GlobalRoles and any earlier
	// Decision are deliberately ignored; disabled state, live global roles,
	// active bindings, and expiry are all re-evaluated by the tx querier.
	actorUser, err := loadLiveUserForShare(ctx, tx, actor.UserID)
	if err != nil {
		return fail(err)
	}
	if err := ensureRoleStaffManageFresh(ctx, s.auth, tx, actorUser, boardID); err != nil {
		return fail(err)
	}
	return tx, actorUser, nil
}

type txDecisionAuthorizer interface {
	decide(context.Context, rowsQuerier, liveUser, Permission, Scope) (Decision, error)
}

// ensureRoleStaffManageFresh re-evaluates live staff permission using the
// transaction querier and the actual database wall clock. Callers invoke it
// again after every potentially waiting target/binding lock and before the
// role mutation so natural expiry during a lock wait cannot be missed.
func ensureRoleStaffManageFresh(
	ctx context.Context,
	auth Authorizer,
	q rowsQuerier,
	actorUser liveUser,
	boardID uuid.UUID,
) error {
	var (
		decision Decision
		err      error
	)
	if txAuthorizer, ok := auth.(txDecisionAuthorizer); ok {
		decision, err = txAuthorizer.decide(ctx, q, actorUser, PermissionBoardStaffManage, Scope{BoardID: boardID})
	} else {
		decision, err = effectiveDecision(ctx, q, actorUser, PermissionBoardStaffManage, Scope{BoardID: boardID})
	}
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return ErrForbidden
	}
	return nil
}

// ensureRequestedExpiryFuture validates a requested grant/update deadline with
// the database clock after lock waits. The Go process clock is never used for
// this authoritative decision.
func ensureRequestedExpiryFuture(ctx context.Context, q rowQuerier, expiresAt *time.Time) error {
	if expiresAt == nil {
		return nil
	}
	now, err := freshDBTime(ctx, q)
	if err != nil {
		return err
	}
	if !expiresAt.After(now) {
		return ErrExpiryNotFuture
	}
	return nil
}
