package boards

import (
	"context"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func (s *Service) AdminGetModule(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) (*Module, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requireBoardRead(ctx, principal, boardID); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	module, err := s.store.GetModule(ctx, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	admin, staff, moduleIDs, err := s.staffContext(ctx, principal, boardID)
	if err != nil {
		return nil, err
	}
	if !staff {
		return nil, ErrForbidden
	}
	if !admin && !s.moduleInScope(moduleIDs, moduleID) {
		return nil, ErrForbidden
	}
	module.Status = effectiveModuleStatus(board, *module, s.clock.Now())
	return module, nil
}

func (s *Service) AdminGetTask(ctx context.Context, principal authorization.Principal, boardID, moduleID, taskID uuid.UUID) (*Task, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleTasksManage,
		authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		return nil, err
	}
	board, module, err := s.loadBoardModule(ctx, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	if !isTaskModuleType(module.Type) {
		return nil, ErrModuleTypeMismatch
	}
	task, err := s.store.GetTask(ctx, boardID, moduleID, taskID)
	if err != nil {
		return nil, err
	}
	task.Status = taskStatus(*task)
	_ = board
	return task, nil
}

func (s *Service) AdminGetScoreboard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*Scoreboard, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardRatingManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	if _, err := s.store.GetBoard(ctx, boardID); err != nil {
		return nil, err
	}
	items, err := s.mergedScoreboard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	return &Scoreboard{Items: items, UpdatedAt: s.clock.Now()}, nil
}

func (s *Service) AdminBatchTaskVisibility(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID, taskIDs []uuid.UUID, visible bool) (int, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleTasksManage,
		authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		return 0, err
	}
	if _, _, err := s.loadBoardModule(ctx, boardID, moduleID); err != nil {
		return 0, err
	}
	if len(taskIDs) < 1 || len(taskIDs) > 1000 {
		return 0, Invalid("body.taskUuids", "must contain 1..1000 task UUIDs", "OUT_OF_RANGE")
	}
	return s.store.SetTasksVisibility(ctx, boardID, moduleID, taskIDs, visible,
		auditEvent(principal, &boardID, &moduleID, "task.visibility.batch", "task", nil,
			map[string]any{"visible": visible, "count": len(taskIDs)}))
}
