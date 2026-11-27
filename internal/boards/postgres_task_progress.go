package boards

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// TaskProgressByModule returns, for each requested module, the number of
// currently visible tasks and how many of them have an ACCEPTED submission for
// the given subject.
//
// It intentionally contains no participation, access-group, audience or
// schedule policy: callers pass module IDs that the service already qualified
// through the canonical read gates. The single board constraint below is a
// parent-scope guard, not an authorization decision. The unique partial index
// on ACCEPTED submissions guarantees at most one accepted row per subject/task,
// so attempts and duplicate submissions cannot inflate the solved count.
func (s *PostgresStore) TaskProgressByModule(
	ctx context.Context,
	boardID uuid.UUID,
	moduleIDs []uuid.UUID,
	subject ScoreSubject,
) (map[uuid.UUID]TaskProgress, error) {
	progress := make(map[uuid.UUID]TaskProgress, len(moduleIDs))
	if len(moduleIDs) == 0 {
		return progress, nil
	}
	if subject.ID == uuid.Nil || (subject.Type != SubjectTypeUser && subject.Type != SubjectTypeTeam) {
		return nil, fmt.Errorf("task progress: invalid subject")
	}
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `
		SELECT t.module_id,
			count(*) AS total_tasks,
			count(*) FILTER (WHERE EXISTS (
				SELECT 1 FROM submissions s
				WHERE s.subject_type = $3 AND s.subject_id = $4
				  AND s.task_id = t.id AND s.status = 'ACCEPTED'
			)) AS solved_tasks
		FROM tasks t
		WHERE t.board_id = $1
		  AND t.module_id = ANY($2::uuid[])
		  AND t.visible = TRUE
		GROUP BY t.module_id`,
		boardID, moduleIDs, subject.Type, subject.ID)
	if err != nil {
		return nil, fmt.Errorf("task progress by module: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var moduleID uuid.UUID
		var item TaskProgress
		if err := rows.Scan(&moduleID, &item.TotalTasks, &item.SolvedTasks); err != nil {
			return nil, fmt.Errorf("scan task progress: %w", err)
		}
		progress[moduleID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task progress: %w", err)
	}
	return progress, nil
}
