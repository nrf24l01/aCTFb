package boards

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func (s *Service) ListMyAudiences(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) ([]AudienceSummary, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requireTeamParticipant(ctx, principal, boardID); err != nil {
		return nil, err
	}
	subject, err := s.store.ResolveSubject(ctx, boardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	return s.store.ListMyAudiences(ctx, boardID, subject)
}

func (s *Service) AdminListScoreAdjustments(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, moduleID *uuid.UUID, query ListQuery) (Page[ScoreAdjustment], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardRatingManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return Page[ScoreAdjustment]{}, err
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	items, total, err := s.store.ListScoreAdjustments(ctx, boardID, moduleID, query.Limit, query.Offset)
	if err != nil {
		return Page[ScoreAdjustment]{}, err
	}
	return Page[ScoreAdjustment]{Items: items, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

func (s *Service) AdminCreateScoreAdjustment(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, input ScoreAdjustmentInput) (*ScoreAdjustment, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardRatingManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if input.Delta == 0 || input.Delta < -1_000_000_000 || input.Delta > 1_000_000_000 {
		return nil, Invalid("body.delta", "delta must be non-zero and bounded", "OUT_OF_RANGE")
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || len([]rune(input.Reason)) > 1000 {
		return nil, Invalid("body.reason", "reason must be 1..1000 characters", "REQUIRED")
	}
	if input.ModuleID != nil {
		if _, err := s.store.GetModule(ctx, boardID, *input.ModuleID); err != nil {
			return nil, err
		}
	}
	if board.ParticipationMode == ParticipationTeam {
		if input.Subject.Type != SubjectTypeTeam {
			return nil, Invalid("body.subjectType", "TEAM boards require a TEAM subject", "INVALID_VALUE")
		}
		team, teamErr := s.store.GetTeam(ctx, boardID, input.Subject.ID)
		if teamErr != nil {
			return nil, teamErr
		}
		_ = team
	} else {
		if input.Subject.Type != SubjectTypeUser {
			return nil, Invalid("body.subjectType", "INDIVIDUAL boards require a USER subject", "INVALID_VALUE")
		}
		if _, err := s.store.GetParticipant(ctx, boardID, input.Subject.ID); err != nil {
			return nil, ErrUserNotFound
		}
	}
	input.BoardID = boardID
	return s.store.CreateScoreAdjustment(ctx, input, UserSummary{ID: principal.UserID},
		auditEvent(principal, &boardID, input.ModuleID, "score.adjustment", "score_adjustment", nil, map[string]any{}))
}
