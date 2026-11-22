package boards

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ListPublicBoards is the anonymous browse path. It returns only PUBLIC boards
// that are not DRAFT or ARCHIVED and never applies participant/staff joins.
func (s *Service) ListPublicBoards(ctx context.Context, query ListBoardsQuery) (Page[Board], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[Board]{}, err
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	boards, total, err := s.store.ListBoards(ctx, BoardFilter{
		Limit:             query.Limit,
		Offset:            query.Offset,
		Query:             query.Query,
		ModuleType:        query.ModuleType,
		Status:            query.Status,
		ParticipationMode: query.ParticipationMode,
		UserID:            uuid.Nil,
		Admin:             false,
	})
	if err != nil {
		return Page[Board]{}, err
	}
	return Page[Board]{Items: boards, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// GetPublicBoard is the anonymous detail path. Private/legacy-hidden boards
// return the same not-found error as a missing board so anonymous callers
// cannot probe for existence.
func (s *Service) GetPublicBoard(ctx context.Context, boardID uuid.UUID) (*Board, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		if errors.Is(err, ErrBoardNotFound) {
			return nil, ErrBoardNotFound
		}
		return nil, err
	}
	board.Status = board.EffectiveStatus(s.clock.Now())
	if board.Visibility != VisibilityPublic || board.Status == BoardStatusDraft || board.Status == BoardStatusArchived {
		return nil, ErrBoardNotFound
	}
	board.Joined = false
	return board, nil
}
