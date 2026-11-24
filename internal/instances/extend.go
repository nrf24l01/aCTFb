package instances

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// extensionRequestHash is the domain-separated hash of an EXTEND request. The
// request has no body, so the instance id and the action are the whole payload;
// reuse by another actor is rejected separately through the stored actor id.
func extensionRequestHash(instanceID uuid.UUID) string {
	sum := sha256.Sum256([]byte("actfb:instance-extend:v1:" + instanceID.String()))
	return hex.EncodeToString(sum[:])
}

// extendOutcome is the durable result of one extension request. It is replayed
// verbatim for a retried Idempotency-Key so a double click cannot consume two
// credits.
type extendOutcome struct {
	InstanceID     uuid.UUID
	GrantedSeconds int64
	ExtensionCount int
	ExpiresAt      *time.Time
	Replay         bool
}

type extendRow struct {
	ID             uuid.UUID
	Status         Status
	ExpiresAt      *time.Time
	ExtensionCount int
	CreatedAt      time.Time
	RemovedAt      *time.Time
	SubjectType    string
	SubjectID      uuid.UUID
	Snapshot       DesiredSnapshot
	BoardStatus    string
	BoardStartsAt  *time.Time
	BoardEndsAt    *time.Time
	ModuleEnabled  bool
	ModuleStartsAt *time.Time
	ModuleEndsAt   *time.Time
}

// extendInstanceAtomic applies one extension inside a single transaction.
//
// The instance row is locked first, then the database clock is read, so a
// concurrent DESTROY, reset or second extension is serialized and a last-second
// request can never resurrect a removed or expired instance.
func (s *store) extendInstanceAtomic(ctx context.Context, instanceID uuid.UUID, requestKey string, actorUserID uuid.UUID, options Options) (*extendOutcome, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	row := &extendRow{}
	var snapshotRaw []byte
	if err := tx.QueryRow(ctx, `
		SELECT i.id, i.status, i.expires_at, i.extension_count, i.created_at, i.removed_at,
		       i.subject_type, i.subject_id, i.desired_snapshot,
		       b.status, b.starts_at, b.ends_at, m.enabled, m.starts_at, m.ends_at
		FROM instances i
		JOIN boards b ON b.id = i.board_id
		JOIN modules m ON m.board_id = i.board_id AND m.id = i.module_id
		WHERE i.id = $1
		FOR UPDATE OF i
	`, instanceID).Scan(
		&row.ID, &row.Status, &row.ExpiresAt, &row.ExtensionCount, &row.CreatedAt, &row.RemovedAt,
		&row.SubjectType, &row.SubjectID, &snapshotRaw,
		&row.BoardStatus, &row.BoardStartsAt, &row.BoardEndsAt,
		&row.ModuleEnabled, &row.ModuleStartsAt, &row.ModuleEndsAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound(CodeNotFound)
		}
		return nil, err
	}
	if err := json.Unmarshal(snapshotRaw, &row.Snapshot); err != nil {
		return nil, instanceError(CodeInternal, "stored instance snapshot is invalid", 500)
	}

	// The clock is read after the lock wait, so it reflects the real serialized
	// position rather than a pre-lock timestamp.
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return nil, err
	}

	// A retried request must be answered with the original outcome, before any
	// new credit check, and must never consume a second credit.
	hash := extensionRequestHash(instanceID)
	var storedHash string
	var storedActor *uuid.UUID
	var storedGranted int64
	var storedCount int
	var storedExpiry *time.Time
	replayErr := tx.QueryRow(ctx, `
		SELECT request_hash, actor_user_id, granted_seconds, extension_count_after, expires_at_after
		FROM instance_extension_requests WHERE instance_id = $1 AND request_key = $2
	`, instanceID, requestKey).Scan(&storedHash, &storedActor, &storedGranted, &storedCount, &storedExpiry)
	switch {
	case replayErr == nil:
		if storedHash != hash {
			return nil, conflict(CodeIdempotencyReused, "Idempotency-Key was reused with a different request")
		}
		if storedActor != nil && actorUserID != uuid.Nil && *storedActor != actorUserID {
			return nil, conflict(CodeIdempotencyReused, "Idempotency-Key belongs to another actor")
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &extendOutcome{
			InstanceID: instanceID, GrantedSeconds: storedGranted, ExtensionCount: storedCount,
			ExpiresAt: storedExpiry, Replay: true,
		}, nil
	case !errors.Is(replayErr, pgx.ErrNoRows):
		return nil, replayErr
	}

	if row.RemovedAt != nil || row.Status == StatusExpired || row.Status == StatusDestroying {
		return nil, conflict(CodeActionNotAllowed, "instance can no longer be extended")
	}
	if row.Status != StatusRunning {
		return nil, conflict(CodeActionNotAllowed, "only a running instance can be extended")
	}
	if row.ExpiresAt == nil || !row.ExpiresAt.After(now) {
		return nil, conflict(CodeExpired, "instance has expired")
	}
	// The board/module window is re-read under the same lock, with the same
	// post-wait clock: a paused board or disabled module must not let a credit be
	// spent on time the worker would refuse to provision.
	if err := row.liveWindowError(now); err != nil {
		return nil, err
	}
	policy := policyFor(row.Snapshot, options)
	if !policy.ExtendEnabled {
		return nil, conflict(CodeActionNotAllowed, "student extensions are disabled for this instance")
	}
	if row.ExtensionCount >= policy.ExtendMaxCount {
		return nil, conflict(CodeExtensionLimit, "instance extension limit reached")
	}
	deadline := hardDeadline(row.CreatedAt, time.Duration(policy.MaxTotalTTLSeconds)*time.Second, row.BoardEndsAt, row.ModuleEndsAt)
	next := row.ExpiresAt.Add(time.Duration(policy.ExtendSeconds) * time.Second)
	if deadline.Before(next) {
		next = deadline
	}
	if !next.After(*row.ExpiresAt) {
		// No remaining budget: reject without consuming a credit.
		return nil, conflict(CodeMaxLifetime, "the instance has reached its maximum lifetime")
	}
	granted := int64(next.Sub(*row.ExpiresAt) / time.Second)
	if granted <= 0 {
		return nil, conflict(CodeMaxLifetime, "the instance has reached its maximum lifetime")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE instances SET expires_at = $2, extension_count = extension_count + 1
		WHERE id = $1 AND removed_at IS NULL
	`, instanceID, next); err != nil {
		return nil, err
	}
	count := row.ExtensionCount + 1
	var actorArg any
	if actorUserID != uuid.Nil {
		actorArg = actorUserID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO instance_extension_requests
			(instance_id, request_key, request_hash, actor_user_id, subject_type, subject_id,
			 extension_count_after, expires_at_after, granted_seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, instanceID, requestKey, hash, actorArg, row.SubjectType, row.SubjectID, count, next, granted); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, module_id, action, resource_type, resource_id, details)
		SELECT $1, i.board_id, i.module_id, 'instance.extend', 'instance', i.id, jsonb_build_object('grantedSeconds', $3::bigint)
		FROM instances i WHERE i.id = $2
	`, actorArg, instanceID, granted); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &extendOutcome{
		InstanceID: instanceID, GrantedSeconds: granted, ExtensionCount: count, ExpiresAt: &next,
	}, nil
}

// extendInstance is the participant/admin EXTEND path. The duration is always
// the approved snapshotted step; the caller never chooses it.
func (s *Service) extendInstance(ctx context.Context, actorUserID uuid.UUID, instanceID uuid.UUID, idempotencyKey string) (*Instance, error) {
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 128 {
		return nil, validation("Idempotency-Key", "Idempotency-Key must be between 16 and 128 characters")
	}
	if _, err := s.store.extendInstanceAtomic(ctx, instanceID, idempotencyKey, actorUserID, s.options); err != nil {
		return nil, err
	}
	return s.store.getInstance(ctx, instanceID)
}

// liveWindowError reports whether the board/module is currently usable for a new
// provisioning attempt. It is evaluated after the row lock and the database
// clock read, so it cannot be based on stale pre-lock state.
func (r *extendRow) liveWindowError(now time.Time) error {
	if r.BoardStatus != "ACTIVE" && r.BoardStatus != "SCHEDULED" {
		return conflict(CodeBoardNotActive, "board is not active")
	}
	if r.BoardStartsAt != nil && now.Before(*r.BoardStartsAt) {
		return conflict(CodeBoardNotActive, "board has not started")
	}
	if r.BoardEndsAt != nil && !now.Before(*r.BoardEndsAt) {
		return conflict(CodeExpired, "board has ended")
	}
	if !r.ModuleEnabled {
		return instanceError(CodeForbidden, "module is not available", 403)
	}
	if r.ModuleStartsAt != nil && now.Before(*r.ModuleStartsAt) {
		return instanceError(CodeForbidden, "module is not available", 403)
	}
	if r.ModuleEndsAt != nil && !now.Before(*r.ModuleEndsAt) {
		return conflict(CodeExpired, "module has ended")
	}
	return nil
}
