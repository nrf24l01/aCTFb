package boards

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SetBoardJoinCode stores the hash of a normalized enrollment code. The
// plaintext never reaches PostgreSQL. A hash is globally unique so one code
// cannot resolve to two boards.
func (s *PostgresStore) SetBoardJoinCode(ctx context.Context, boardID, actorID uuid.UUID, hash string, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin set board join code: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM boards WHERE id=$1)`, boardID).Scan(&exists); err != nil {
		return fmt.Errorf("check board for join code: %w", err)
	}
	if !exists {
		return ErrBoardNotFound
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO board_join_codes (board_id, code_hash, created_by, updated_by)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (board_id) DO UPDATE
		SET code_hash = EXCLUDED.code_hash,
		    updated_by = EXCLUDED.updated_by,
		    updated_at = clock_timestamp()
	`, boardID, hash, actorID)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrJoinCodeConflict
		}
		if isForeignKeyViolation(err) {
			return ErrBoardNotFound
		}
		return fmt.Errorf("set board join code: %w", err)
	}
	audit.BoardID = &boardID
	audit.ResourceID = &boardID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit set board join code: %w", err)
	}
	return nil
}

// BoardJoinCodeStatus reports whether a code exists and when it last changed.
// It never returns code material.
func (s *PostgresStore) BoardJoinCodeStatus(ctx context.Context, boardID uuid.UUID) (bool, *time.Time, error) {
	p, err := s.pool()
	if err != nil {
		return false, nil, err
	}
	var updatedAt time.Time
	err = p.pool.QueryRow(ctx, `
		SELECT updated_at FROM board_join_codes WHERE board_id = $1
	`, boardID).Scan(&updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("read board join code status: %w", err)
	}
	return true, &updatedAt, nil
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
