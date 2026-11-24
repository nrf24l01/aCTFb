package instances

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/runtime"
)

// liveDeadline is the authoritative deadline state re-read from PostgreSQL
// before the worker touches a provider. The NATS job and the frozen authorized
// context are never trusted for it.
type liveDeadline struct {
	Status         Status
	RemovedAt      *time.Time
	ExpiresAt      *time.Time
	BoardStatus    string
	BoardStartsAt  *time.Time
	BoardEndsAt    *time.Time
	ModuleEnabled  bool
	ModuleStartsAt *time.Time
	ModuleEndsAt   *time.Time
}

func (s *store) liveDeadline(ctx context.Context, instanceID uuid.UUID) (*liveDeadline, error) {
	state := &liveDeadline{}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT i.status, i.removed_at, i.expires_at,
		       b.status, b.starts_at, b.ends_at,
		       m.enabled, m.starts_at, m.ends_at
		FROM instances i
		JOIN boards b ON b.id = i.board_id
		JOIN modules m ON m.board_id = i.board_id AND m.id = i.module_id
		WHERE i.id = $1
	`, instanceID).Scan(
		&state.Status, &state.RemovedAt, &state.ExpiresAt,
		&state.BoardStatus, &state.BoardStartsAt, &state.BoardEndsAt,
		&state.ModuleEnabled, &state.ModuleStartsAt, &state.ModuleEndsAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound(CodeNotFound)
		}
		return nil, err
	}
	return state, nil
}

// blocked reports why the worker must not provision or reset right now. Destroy
// is always allowed so cleanup can never be blocked by a deadline.
func (d *liveDeadline) blocked(now time.Time) error {
	if d.RemovedAt != nil || d.Status == StatusExpired || d.Status == StatusDestroying {
		return conflict(CodeExpired, "instance is no longer live")
	}
	if d.ExpiresAt != nil && !now.Before(*d.ExpiresAt) {
		return conflict(CodeExpired, "instance lifetime has ended")
	}
	if d.BoardStatus != "ACTIVE" && d.BoardStatus != "SCHEDULED" {
		return conflict(CodeBoardNotActive, "board is not active")
	}
	if d.BoardStartsAt != nil && now.Before(*d.BoardStartsAt) {
		return conflict(CodeBoardNotActive, "board has not started")
	}
	if d.BoardEndsAt != nil && !now.Before(*d.BoardEndsAt) {
		return conflict(CodeExpired, "board has ended")
	}
	if !d.ModuleEnabled {
		return instanceError(CodeForbidden, "module is not available", 403)
	}
	if d.ModuleStartsAt != nil && now.Before(*d.ModuleStartsAt) {
		return instanceError(CodeForbidden, "module is not available", 403)
	}
	if d.ModuleEndsAt != nil && !now.Before(*d.ModuleEndsAt) {
		return conflict(CodeExpired, "module has ended")
	}
	return nil
}

// abortExpired stops a job whose deadline already passed. It performs zero
// provider calls. Provider-confirmed resources are queued for destroy instead of
// being forgotten; the 30 s reconcile loop performs that cleanup.
func (o *Orchestrator) abortExpired(ctx context.Context, instance *workerInstance, operations []Operation, cause error) error {
	for _, operation := range operations {
		if operation.Kind == runtime.OperationDestroy {
			continue
		}
		// CANCELED, not FAILED: the operation never ran, and its resources (if
		// any survived a crash) still need the destroy path.
		_ = o.store.markOperation(ctx, operation.ID, "CANCELED", errorCode(cause))
	}
	providerIDs, err := o.store.nodeProviderIDs(ctx, instance.ID)
	if err != nil {
		return err
	}
	for _, providerID := range providerIDs {
		if providerID != "" {
			// Provider objects exist: queue DESTROY. PendingJobs picks it up on
			// the next reconcile pass and leases/ports are released only after
			// the provider confirms destroy.
			_, _, err := o.store.destroyInstance(ctx, instance.ID, AuthorizedContext{System: true, Permission: "system.expire"})
			return err
		}
	}
	return o.store.releaseResources(ctx, instance.ID)
}
