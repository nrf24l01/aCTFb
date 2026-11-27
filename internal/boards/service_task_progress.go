package boards

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// boardTaskProgressModules qualifies the board-wide module set used for the
// caller's own task summary. It applies the same participant availability gate
// as the module/task reads (enabled TASKS/EXTENSION module, ACTIVE effective
// status) plus the module audience rule over the caller's own resolved
// audiences. Staff management scope is deliberately never consulted here.
func boardTaskProgressModules(board *Board, modules []Module, audiences []AudienceSummary, now time.Time) []uuid.UUID {
	audienceSet := make(map[uuid.UUID]struct{}, len(audiences))
	for _, audience := range audiences {
		audienceSet[audience.UUID] = struct{}{}
	}
	ids := make([]uuid.UUID, 0, len(modules))
	for _, module := range modules {
		if !module.Enabled || !isTaskModuleType(module.Type) {
			continue
		}
		if effectiveModuleStatus(board, module, now) != ModuleStatusActive {
			continue
		}
		if !moduleAudienceVisible(module, audienceSet) {
			continue
		}
		ids = append(ids, module.ID)
	}
	return ids
}

// moduleAudienceVisible applies the existing module audience union rule: no
// listed audience means every joined subject; otherwise membership in at least
// one listed audience is required.
func moduleAudienceVisible(module Module, audiences map[uuid.UUID]struct{}) bool {
	if len(module.AudienceUUIDs) == 0 {
		return true
	}
	for _, audienceID := range module.AudienceUUIDs {
		if _, ok := audiences[audienceID]; ok {
			return true
		}
	}
	return false
}

// boardTaskProgress sums one batched aggregate over every module currently
// readable as a participant. It loads all board modules (no page cap) so the
// summary can never be limited to the first module page.
func (s *Service) boardTaskProgress(
	ctx context.Context,
	board *Board,
	subject ScoreSubject,
	audiences []AudienceSummary,
	now time.Time,
) (TaskProgress, error) {
	modules, err := s.store.BoardModules(ctx, board.ID)
	if err != nil {
		return TaskProgress{}, err
	}
	ids := boardTaskProgressModules(board, modules, audiences, now)
	progressByModule, err := s.store.TaskProgressByModule(ctx, board.ID, ids, subject)
	if err != nil {
		return TaskProgress{}, err
	}
	var total TaskProgress
	for _, id := range ids {
		item := progressByModule[id]
		total.SolvedTasks += item.SolvedTasks
		total.TotalTasks += item.TotalTasks
	}
	return total, nil
}

// moduleTaskProgress returns the own projection for exactly one module. The
// caller has already resolved live participant eligibility and ACTIVE status;
// a module without visible tasks is an explicit 0/0.
func (s *Service) moduleTaskProgress(ctx context.Context, boardID, moduleID uuid.UUID, subject ScoreSubject) (TaskProgress, error) {
	progressByModule, err := s.store.TaskProgressByModule(ctx, boardID, []uuid.UUID{moduleID}, subject)
	if err != nil {
		return TaskProgress{}, err
	}
	return progressByModule[moduleID], nil
}

// isOwnTaskModule reports whether a resolved module read may carry the caller's
// own progress: participant-eligible, ACTIVE and task-bearing.
func isOwnTaskModule(module Module) bool {
	return module.CanParticipate && module.Status == ModuleStatusActive && isTaskModuleType(module.Type)
}
