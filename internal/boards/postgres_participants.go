package boards

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) JoinBoard(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) (*Participant, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin join board: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		boardID, userID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, ErrBoardNotFound
		}
		return nil, fmt.Errorf("join board: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrAlreadyJoined
	}
	audit.BoardID = &boardID
	audit.ResourceID = &userID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit join board: %w", err)
	}
	return s.GetParticipant(ctx, boardID, userID)
}

func (s *PostgresStore) LeaveBoard(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin leave board: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	audit.BoardID = &boardID
	audit.ResourceID = &userID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM board_participants WHERE board_id=$1 AND user_id=$2`, boardID, userID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrBoardLeaveNotAllowed
		}
		return fmt.Errorf("leave board: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBoardNotJoined
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit leave board: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetParticipant(ctx context.Context, boardID, userID uuid.UUID) (*Participant, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	var participant Participant
	err = p.pool.QueryRow(ctx, `
		SELECT bp.board_id, bp.user_id, u.username, bp.joined_at
		FROM board_participants bp JOIN users u ON u.id = bp.user_id
		WHERE bp.board_id=$1 AND bp.user_id=$2`, boardID, userID,
	).Scan(&participant.BoardID, &participant.UserID, &participant.Username, &participant.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBoardNotJoined
	}
	if err != nil {
		return nil, fmt.Errorf("get participant: %w", err)
	}
	return &participant, nil
}

func (s *PostgresStore) ListParticipants(ctx context.Context, boardID uuid.UUID, limit, offset int) ([]Participant, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM board_participants WHERE board_id=$1`, boardID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count participants: %w", err)
	}
	if total == 0 {
		return []Participant{}, 0, nil
	}
	rows, err := p.pool.Query(ctx, `
		SELECT bp.board_id, bp.user_id, u.username, bp.joined_at
		FROM board_participants bp JOIN users u ON u.id = bp.user_id
		WHERE bp.board_id=$1
		ORDER BY bp.joined_at ASC, bp.user_id ASC
		LIMIT $2 OFFSET $3`, boardID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list participants: %w", err)
	}
	defer rows.Close()
	items := make([]Participant, 0, limit)
	for rows.Next() {
		var participant Participant
		if err := rows.Scan(&participant.BoardID, &participant.UserID, &participant.Username, &participant.JoinedAt); err != nil {
			return nil, 0, fmt.Errorf("scan participant: %w", err)
		}
		items = append(items, participant)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate participants: %w", err)
	}
	return items, total, nil
}

func (s *PostgresStore) AddParticipant(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin add participant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		boardID, userID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrUserNotFound
		}
		return fmt.Errorf("add participant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyJoined
	}
	audit.BoardID = &boardID
	audit.ResourceID = &userID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit add participant: %w", err)
	}
	return nil
}

func (s *PostgresStore) RemoveParticipant(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin remove participant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	audit.BoardID = &boardID
	audit.ResourceID = &userID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM board_participants WHERE board_id=$1 AND user_id=$2`, boardID, userID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrBoardStateConflict
		}
		return fmt.Errorf("remove participant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit remove participant: %w", err)
	}
	return nil
}

func (s *PostgresStore) ParticipantHasSubmissions(ctx context.Context, boardID, userID uuid.UUID) (bool, error) {
	p, err := s.pool()
	if err != nil {
		return false, err
	}
	var exists bool
	if err := p.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM submissions WHERE board_id=$1 AND user_id=$2)`,
		boardID, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check participant submissions: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) UserExists(ctx context.Context, userID uuid.UUID) (bool, error) {
	p, err := s.pool()
	if err != nil {
		return false, err
	}
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id=$1)`, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check user: %w", err)
	}
	return exists, nil
}
