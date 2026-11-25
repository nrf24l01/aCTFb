package boards

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// BoardJoinCodeRecord is the committed state after a successful write.
type BoardJoinCodeRecord struct {
	Revision  int64
	UpdatedAt time.Time
}

// SetBoardJoinCode stores the hash of a normalized enrollment code. The
// plaintext never reaches PostgreSQL.
//
// Concurrency: the board row is locked FOR UPDATE for the whole transaction, so
// rotations of one board serialize. A replace must name the revision it read
// (`expectedRevision`); a stale revision is a conflict instead of a silent
// overwrite, and the returned revision/timestamp are the committed values from
// the same statement rather than a later read that could observe another
// writer.
func (s *PostgresStore) SetBoardJoinCode(
	ctx context.Context,
	boardID, actorID uuid.UUID,
	hash string,
	expectedRevision *int64,
	auth authorization.Authorizer,
	audit AuditEventParams,
) (*BoardJoinCodeRecord, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin set board join code: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize per board. This also proves the board exists.
	var lockedID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM boards WHERE id=$1 FOR UPDATE`, boardID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBoardNotFound
		}
		return nil, fmt.Errorf("lock board for join code: %w", err)
	}
	// Re-check live board.update inside the transaction, after the lock, using
	// this transaction's querier. A demotion or binding revocation during the
	// wait therefore denies before the write instead of committing stale claims.
	if err := authorization.CheckPermissionTx(ctx, auth, tx, actorID,
		authorization.PermissionBoardUpdate, authorization.Scope{BoardID: boardID}); err != nil {
		if errors.Is(err, authorization.ErrForbidden) {
			return nil, ErrForbidden
		}
		if errors.Is(err, authorization.ErrUnauthorized) || errors.Is(err, authorization.ErrUserDisabled) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("recheck join code permission: %w", err)
	}

	var current int64
	err = tx.QueryRow(ctx, `SELECT revision FROM board_join_codes WHERE board_id=$1`, boardID).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read board join code revision: %w", err)
	}
	exists := err == nil
	switch {
	case !exists:
		if expectedRevision != nil && *expectedRevision != 0 {
			return nil, ErrJoinCodeStale
		}
	case expectedRevision == nil:
		// Replacing a live code without naming its revision is refused: it would
		// be a blind overwrite of another organizer's code.
		return nil, ErrJoinCodePreconditionRequired
	case *expectedRevision != current:
		return nil, ErrJoinCodeStale
	}

	record := &BoardJoinCodeRecord{}
	err = tx.QueryRow(ctx, `
		INSERT INTO board_join_codes (board_id, code_hash, created_by, updated_by)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (board_id) DO UPDATE
		SET code_hash = EXCLUDED.code_hash,
		    revision = board_join_codes.revision + 1,
		    updated_by = EXCLUDED.updated_by,
		    updated_at = clock_timestamp()
		RETURNING revision, updated_at
	`, boardID, hash, actorID).Scan(&record.Revision, &record.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrJoinCodeConflict
		}
		if isForeignKeyViolation(err) {
			return nil, ErrBoardNotFound
		}
		return nil, fmt.Errorf("set board join code: %w", err)
	}
	audit.BoardID = &boardID
	audit.ResourceID = &boardID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit set board join code: %w", err)
	}
	return record, nil
}

// BoardJoinCodeStatus reports whether a code exists, its revision and when it
// last changed. It never returns code material.
func (s *PostgresStore) BoardJoinCodeStatus(ctx context.Context, boardID uuid.UUID) (bool, int64, *time.Time, error) {
	p, err := s.pool()
	if err != nil {
		return false, 0, nil, err
	}
	var revision int64
	var updatedAt time.Time
	err = p.pool.QueryRow(ctx, `
		SELECT revision, updated_at FROM board_join_codes WHERE board_id = $1
	`, boardID).Scan(&revision, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil, nil
	}
	if err != nil {
		return false, 0, nil, fmt.Errorf("read board join code status: %w", err)
	}
	return true, revision, &updatedAt, nil
}

// BoardByJoinCodeHash resolves a code hash to its board. A miss is a generic
// ErrJoinCodeInvalid so the endpoint is not a board directory.
func (s *PostgresStore) BoardByJoinCodeHash(ctx context.Context, hash string) (*Board, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	var boardID uuid.UUID
	err = p.pool.QueryRow(ctx, `SELECT board_id FROM board_join_codes WHERE code_hash = $1`, hash).Scan(&boardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrJoinCodeInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("resolve board join code: %w", err)
	}
	return s.GetBoard(ctx, boardID)
}
