package boards

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *PostgresStore) ListScoreAdjustments(ctx context.Context, boardID uuid.UUID, moduleID *uuid.UUID, limit, offset int) ([]ScoreAdjustment, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}
	where := `a.board_id=$1 AND a.action='score.adjustment'`
	args := []any{boardID}
	if moduleID != nil {
		args = append(args, *moduleID)
		where += fmt.Sprintf(" AND a.module_id=$%d", len(args))
	}
	var total int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events a WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count score adjustments: %w", err)
	}
	if total == 0 {
		return []ScoreAdjustment{}, 0, nil
	}
	args = append(args, limit, offset)
	rows, err := p.pool.Query(ctx, `
		SELECT a.id, a.board_id, a.module_id, a.details, u.id, u.username, a.created_at
		FROM audit_events a JOIN users u ON u.id=a.actor_user_id
		WHERE `+where+fmt.Sprintf(" ORDER BY a.created_at DESC, a.id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list score adjustments: %w", err)
	}
	defer rows.Close()
	items := make([]ScoreAdjustment, 0, limit)
	for rows.Next() {
		var item ScoreAdjustment
		var module *uuid.UUID
		var raw []byte
		if err := rows.Scan(&item.ID, &item.BoardID, &module, &raw, &item.Actor.ID, &item.Actor.Username, &item.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan score adjustment: %w", err)
		}
		item.ModuleID = module
		var details struct {
			SubjectType string `json:"subjectType"`
			SubjectUUID string `json:"subjectUuid"`
			Delta       int    `json:"delta"`
			Reason      string `json:"reason"`
		}
		if err := json.Unmarshal(raw, &details); err != nil {
			return nil, 0, fmt.Errorf("decode score adjustment: %w", err)
		}
		subjectID, err := uuid.Parse(details.SubjectUUID)
		if err != nil {
			return nil, 0, fmt.Errorf("decode score adjustment subject: %w", err)
		}
		item.Subject = ScoreSubject{Type: details.SubjectType, ID: subjectID}
		item.Delta = details.Delta
		item.Reason = details.Reason
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate score adjustments: %w", err)
	}
	return items, total, nil
}

func (s *PostgresStore) CreateScoreAdjustment(ctx context.Context, in ScoreAdjustmentInput, actor UserSummary, audit AuditEventParams) (*ScoreAdjustment, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin score adjustment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	details := map[string]any{
		"subjectType": in.Subject.Type,
		"subjectUuid": in.Subject.ID.String(),
		"delta":       in.Delta,
		"reason":      in.Reason,
	}
	audit.BoardID = &in.BoardID
	audit.ModuleID = in.ModuleID
	audit.Action = "score.adjustment"
	audit.ResourceType = "score_adjustment"
	audit.Details = details
	var id uuid.UUID
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, module_id, action, resource_type, details)
		VALUES ($1,$2,$3,'score.adjustment','score_adjustment',$4) RETURNING id, created_at`,
		audit.ActorUserID, in.BoardID, in.ModuleID, details).Scan(&id, &createdAt); err != nil {
		return nil, fmt.Errorf("insert score adjustment: %w", err)
	}
	if actor.Username == "" {
		if err := tx.QueryRow(ctx, `SELECT username FROM users WHERE id=$1`, actor.ID).Scan(&actor.Username); err != nil {
			return nil, fmt.Errorf("load score adjustment actor: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit score adjustment: %w", err)
	}
	return &ScoreAdjustment{
		ID: id, BoardID: in.BoardID, ModuleID: in.ModuleID, Subject: in.Subject,
		Delta: in.Delta, Reason: in.Reason, Actor: actor, CreatedAt: createdAt,
	}, nil
}

func (s *PostgresStore) ListMyAudiences(ctx context.Context, boardID uuid.UUID, subject ScoreSubject) ([]AudienceSummary, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `
		SELECT a.id, a.name
		FROM audiences a
		JOIN audience_members am ON am.audience_id=a.id
		WHERE a.board_id=$1 AND am.subject_type=$2 AND am.subject_id=$3
		ORDER BY a.name, a.id`, boardID, subject.Type, subject.ID)
	if err != nil {
		return nil, fmt.Errorf("list my audiences: %w", err)
	}
	defer rows.Close()
	items := make([]AudienceSummary, 0)
	for rows.Next() {
		var item AudienceSummary
		if err := rows.Scan(&item.UUID, &item.Name); err != nil {
			return nil, fmt.Errorf("scan my audience: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate my audiences: %w", err)
	}
	return items, nil
}

func (s *PostgresStore) AudiencesBelongToBoard(ctx context.Context, boardID uuid.UUID, audienceIDs []uuid.UUID) error {
	if len(audienceIDs) == 0 {
		return nil
	}
	p, err := s.pool()
	if err != nil {
		return err
	}
	var count int
	if err := p.pool.QueryRow(ctx, `
		SELECT count(*) FROM audiences
		WHERE board_id=$1 AND id = ANY($2::uuid[])`, boardID, audienceIDs).Scan(&count); err != nil {
		return fmt.Errorf("validate module audiences: %w", err)
	}
	if count != len(audienceIDs) {
		return ErrAudienceNotFound
	}
	return nil
}
