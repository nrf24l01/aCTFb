package boards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const taskColumns = `t.id, t.board_id, t.module_id, t.title, t.category, t.description,
	t.type, t.points, t.visible, t.submission_mode, t.submission_checker,
	COALESCE(t.extension_key, ''), COALESCE(t.checker_key, ''), t.instance_enabled,
	t.submission_text_policy, t.submission_files_policy,
	t.created_by, t.created_at, t.updated_at,
	t.instance_ttl_seconds, t.instance_max_ttl_seconds, t.instance_extension_enabled,
	t.instance_extension_seconds, t.instance_extension_max_count,
	(SELECT m.task_instance_settings FROM modules m WHERE m.board_id = t.board_id AND m.id = t.module_id),
	(SELECT count(*) FROM task_flags f WHERE f.task_id = t.id)`

// taskScanBuffers holds the nullable lifetime columns plus the JSON policy
// blobs that are decoded after scanning.
type taskScanBuffers struct {
	textRaw, filesRaw, moduleSettingsRaw                 []byte
	initialTTL, maxTTL, extensionSeconds, extensionMaxCount *int
	extensionEnabled                                     *bool
}

// taskScanDest returns the scan destinations for taskColumns, in column order.
func taskScanDest(task *Task, buf *taskScanBuffers) []any {
	return []any{
		&task.ID,
		&task.BoardID,
		&task.ModuleID,
		&task.Title,
		&task.Category,
		&task.Description,
		&task.Type,
		&task.Points,
		&task.Visible,
		&task.SubmissionMode,
		&task.SubmissionChecker,
		&task.ExtensionKey,
		&task.CheckerKey,
		&task.InstanceEnabled,
		&buf.textRaw,
		&buf.filesRaw,
		&task.CreatedBy,
		&task.CreatedAt,
		&task.UpdatedAt,
		&buf.initialTTL,
		&buf.maxTTL,
		&buf.extensionEnabled,
		&buf.extensionSeconds,
		&buf.extensionMaxCount,
		&buf.moduleSettingsRaw,
		&task.AcceptedAnswersCount,
	}
}

// applyTaskScanBuffers finishes a scan: it copies the nullable overrides and
// decodes the parent module policy and submission policies.
func applyTaskScanBuffers(task *Task, buf *taskScanBuffers) error {
	task.InstanceTTLSeconds = buf.initialTTL
	task.InstanceMaxTTLSeconds = buf.maxTTL
	task.InstanceExtensionEnabled = buf.extensionEnabled
	task.InstanceExtensionSeconds = buf.extensionSeconds
	task.InstanceExtensionMaxCount = buf.extensionMaxCount
	settings, err := decodeTaskInstanceSettings(buf.moduleSettingsRaw)
	if err != nil {
		return fmt.Errorf("decode module instance settings: %w", err)
	}
	task.ModuleInstanceSettings = settings
	return applyTaskPolicies(task, buf.textRaw, buf.filesRaw)
}

func scanTaskRow(row boardScanner) (*Task, error) {
	task := &Task{}
	buf := &taskScanBuffers{}
	if err := row.Scan(taskScanDest(task, buf)...); err != nil {
		return nil, err
	}
	if err := applyTaskScanBuffers(task, buf); err != nil {
		return nil, err
	}
	return task, nil
}

// scanTaskRowInto scans taskColumns plus caller-supplied trailing destinations
// (for example the solved/solves_count projections) into task.
func scanTaskRowInto(row boardScanner, task *Task, extras ...any) error {
	buf := &taskScanBuffers{}
	dest := append(taskScanDest(task, buf), extras...)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	return applyTaskScanBuffers(task, buf)
}

func applyTaskPolicies(task *Task, textRaw, filesRaw []byte) error {
	if len(textRaw) > 0 {
		var rawPolicy struct {
			Required  bool `json:"required"`
			MinLength int  `json:"minLength"`
			MaxLength int  `json:"maxLength"`
		}
		if err := json.Unmarshal(textRaw, &rawPolicy); err != nil {
			return fmt.Errorf("decode task text policy: %w", err)
		}
		task.TextPolicy = &DraftPolicy{TextRequired: rawPolicy.Required, TextMinLength: rawPolicy.MinLength, TextMaxLength: rawPolicy.MaxLength}
	}
	if len(filesRaw) > 0 {
		var rawPolicy struct {
			Required            bool     `json:"required"`
			MinCount            int      `json:"minCount"`
			MaxCount            int      `json:"maxCount"`
			MaxSizeBytes        int64    `json:"maxSizeBytes"`
			AllowedContentTypes []string `json:"allowedContentTypes"`
		}
		if err := json.Unmarshal(filesRaw, &rawPolicy); err != nil {
			return fmt.Errorf("decode task files policy: %w", err)
		}
		task.FilesPolicy = &DraftPolicy{
			FilesRequired: rawPolicy.Required, FilesMinCount: rawPolicy.MinCount,
			FilesMaxCount: rawPolicy.MaxCount, FilesMaxSize: rawPolicy.MaxSizeBytes,
			AllowedTypes: rawPolicy.AllowedContentTypes,
		}
	}
	return nil
}

func (s *PostgresStore) ListTasks(ctx context.Context, boardID, moduleID uuid.UUID, filter TaskFilter) ([]Task, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}

	where := []string{"t.board_id=$1", "t.module_id=$2"}
	args := []any{boardID, moduleID}
	if !filter.IncludeHidden {
		where = append(where, "t.visible = TRUE")
	}
	if filter.Query != "" {
		args = append(args, filter.Query)
		where = append(where, fmt.Sprintf("t.title ILIKE '%%' || $%d || '%%'", len(args)))
	}
	baseWhere := strings.Join(where, " AND ")

	var total int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM tasks t WHERE `+baseWhere, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list tasks count: %w", err)
	}
	if total == 0 {
		return []Task{}, 0, nil
	}

	subjectType, subjectID := SubjectTypeUser, filter.UserID
	if filter.Subject != nil {
		subjectType, subjectID = filter.Subject.Type, filter.Subject.ID
	}
	args = append(args, subjectType, subjectID)
	typeParam, idParam := len(args)-1, len(args)
	listArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)
	query := `SELECT ` + taskColumns + `,
		EXISTS (SELECT 1 FROM submissions s WHERE s.subject_type=$` + fmt.Sprint(typeParam) +
		` AND s.subject_id=$` + fmt.Sprint(idParam) +
		` AND s.task_id=t.id AND s.status='ACCEPTED') AS solved,
		(SELECT count(*) FROM submissions s WHERE s.task_id=t.id AND s.status='ACCEPTED') AS solves_count
		FROM tasks t WHERE ` + baseWhere + ` ORDER BY t.created_at ASC, t.id ASC LIMIT $` +
		fmt.Sprint(len(args)+1) + ` OFFSET $` + fmt.Sprint(len(args)+2)
	rows, err := p.pool.Query(ctx, query, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	items := make([]Task, 0, filter.Limit)
	for rows.Next() {
		task := Task{}
		if err := scanTaskRowInto(rows, &task, &task.Solved, &task.SolvesCount); err != nil {
			return nil, 0, fmt.Errorf("scan task: %w", err)
		}
		items = append(items, task)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate tasks: %w", err)
	}
	return items, total, nil
}

func (s *PostgresStore) GetTask(ctx context.Context, boardID, moduleID, taskID uuid.UUID) (*Task, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	task, err := scanTaskRow(p.pool.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM tasks t WHERE t.board_id=$1 AND t.module_id=$2 AND t.id=$3`,
		boardID, moduleID, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

func (s *PostgresStore) CreateTask(ctx context.Context, in CreateTaskParams, audit AuditEventParams) (*Task, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	task, err := scanTaskRow(tx.QueryRow(ctx, `
		INSERT INTO tasks AS t (board_id, module_id, title, category, description, type,
			points, visible, submission_mode, submission_checker, extension_key, checker_key,
			instance_enabled, submission_text_policy, submission_files_policy, created_by,
			instance_ttl_seconds, instance_max_ttl_seconds, instance_extension_enabled,
			instance_extension_seconds, instance_extension_max_count)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		RETURNING `+taskColumns,
		in.BoardID, in.ModuleID, in.Title, in.Category, in.Description, in.Type,
		in.Points, in.Visible, in.SubmissionMode, in.SubmissionChecker,
		emptyToNil(in.ExtensionKey), emptyToNil(in.CheckerKey), in.InstanceEnabled,
		taskTextPolicyJSON(in.TextPolicy), taskFilesPolicyJSON(in.FilesPolicy), in.CreatedBy,
		in.InstanceTTLSeconds, in.InstanceMaxTTLSeconds, in.InstanceExtensionEnabled,
		in.InstanceExtensionSeconds, in.InstanceExtensionMaxCount,
	))
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, ErrModuleNotFound
		}
		return nil, fmt.Errorf("create task: %w", err)
	}
	for _, hash := range in.AnswerHashes {
		if _, err := tx.Exec(ctx, `INSERT INTO task_flags (task_id, answer_hash) VALUES ($1,$2)`, task.ID, hash); err != nil {
			return nil, fmt.Errorf("insert task flag: %w", err)
		}
	}
	audit.BoardID = &task.BoardID
	audit.ModuleID = &task.ModuleID
	audit.ResourceID = &task.ID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create task: %w", err)
	}
	task.AcceptedAnswersCount = len(in.AnswerHashes)
	return task, nil
}

func (s *PostgresStore) UpdateTask(ctx context.Context, boardID, moduleID, taskID uuid.UUID, in UpdateTaskParams, audit AuditEventParams) (*Task, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin update task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	task, err := scanTaskRow(tx.QueryRow(ctx, `
		UPDATE tasks AS t SET title=$4, category=$5, description=$6, points=$7,
			visible=$8, submission_mode=$9, submission_checker=$10,
			extension_key=$11, checker_key=$12, instance_enabled=$13,
			submission_text_policy=$14, submission_files_policy=$15,
			instance_ttl_seconds=$16, instance_max_ttl_seconds=$17,
			instance_extension_enabled=$18, instance_extension_seconds=$19,
			instance_extension_max_count=$20, updated_at=NOW()
		WHERE board_id=$1 AND module_id=$2 AND id=$3
		RETURNING `+taskColumns,
		boardID, moduleID, taskID, in.Title, in.Category, in.Description,
		in.Points, in.Visible, in.SubmissionMode, in.SubmissionChecker,
		emptyToNil(in.ExtensionKey), emptyToNil(in.CheckerKey), in.InstanceEnabled,
		taskTextPolicyJSON(in.TextPolicy), taskFilesPolicyJSON(in.FilesPolicy),
		in.InstanceTTLSeconds, in.InstanceMaxTTLSeconds, in.InstanceExtensionEnabled,
		in.InstanceExtensionSeconds, in.InstanceExtensionMaxCount,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	if in.ReplaceAnswers {
		if _, err := tx.Exec(ctx, `DELETE FROM task_flags WHERE task_id=$1`, taskID); err != nil {
			return nil, fmt.Errorf("delete task flags: %w", err)
		}
		for _, hash := range in.AnswerHashes {
			if _, err := tx.Exec(ctx, `INSERT INTO task_flags (task_id, answer_hash) VALUES ($1,$2)`, taskID, hash); err != nil {
				return nil, fmt.Errorf("insert task flag: %w", err)
			}
		}
		task.AcceptedAnswersCount = len(in.AnswerHashes)
	}
	audit.BoardID = &boardID
	audit.ModuleID = &moduleID
	audit.ResourceID = &taskID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update task: %w", err)
	}
	return task, nil
}

func (s *PostgresStore) DeleteTask(ctx context.Context, boardID, moduleID, taskID uuid.UUID, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	audit.BoardID = &boardID
	audit.ModuleID = &moduleID
	audit.ResourceID = &taskID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM tasks WHERE board_id=$1 AND module_id=$2 AND id=$3`, boardID, moduleID, taskID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrTaskInUse
		}
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTaskNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete task: %w", err)
	}
	return nil
}

func taskTextPolicyJSON(policy *DraftPolicy) any {
	if policy == nil {
		return nil
	}
	encoded, err := json.Marshal(struct {
		Required  bool `json:"required"`
		MinLength int  `json:"minLength"`
		MaxLength int  `json:"maxLength"`
	}{policy.TextRequired, policy.TextMinLength, policy.TextMaxLength})
	if err != nil {
		return nil
	}
	return encoded
}

func taskFilesPolicyJSON(policy *DraftPolicy) any {
	if policy == nil {
		return nil
	}
	encoded, err := json.Marshal(struct {
		Required            bool     `json:"required"`
		MinCount            int      `json:"minCount"`
		MaxCount            int      `json:"maxCount"`
		MaxSizeBytes        int64    `json:"maxSizeBytes"`
		AllowedContentTypes []string `json:"allowedContentTypes"`
	}{policy.FilesRequired, policy.FilesMinCount, policy.FilesMaxCount, policy.FilesMaxSize, policy.AllowedTypes})
	if err != nil {
		return nil
	}
	return encoded
}

func (s *PostgresStore) TaskAnswerHashes(ctx context.Context, taskID uuid.UUID) ([]string, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT answer_hash FROM task_flags WHERE task_id=$1 ORDER BY answer_hash`, taskID)
	if err != nil {
		return nil, fmt.Errorf("load task flags: %w", err)
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, fmt.Errorf("scan task flag: %w", err)
		}
		hashes = append(hashes, hash)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task flags: %w", err)
	}
	return hashes, nil
}

func (s *PostgresStore) TaskAcceptedCount(ctx context.Context, taskID uuid.UUID) (int, error) {
	p, err := s.pool()
	if err != nil {
		return 0, err
	}
	var count int
	if err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM submissions WHERE task_id=$1 AND status='ACCEPTED'`, taskID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count task solves: %w", err)
	}
	return count, nil
}

func (s *PostgresStore) UserSolvedTask(ctx context.Context, userID, taskID uuid.UUID) (bool, error) {
	p, err := s.pool()
	if err != nil {
		return false, err
	}
	var solved bool
	if err := p.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM submissions WHERE user_id=$1 AND task_id=$2 AND status='ACCEPTED')`,
		userID, taskID).Scan(&solved); err != nil {
		return false, fmt.Errorf("check task solved: %w", err)
	}
	return solved, nil
}

func (s *PostgresStore) AllBoardTasks(ctx context.Context, boardID uuid.UUID) ([]Task, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE t.board_id=$1 ORDER BY t.created_at, t.id`, boardID)
	if err != nil {
		return nil, fmt.Errorf("load board tasks: %w", err)
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan board task: %w", err)
		}
		tasks = append(tasks, *task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate board tasks: %w", err)
	}
	return tasks, nil
}

func (s *PostgresStore) SetTasksVisibility(ctx context.Context, boardID, moduleID uuid.UUID, taskIDs []uuid.UUID, visible bool, audit AuditEventParams) (int, error) {
	p, err := s.pool()
	if err != nil {
		return 0, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin batch visibility: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE tasks SET visible=$4, updated_at=NOW()
		WHERE board_id=$1 AND module_id=$2 AND id = ANY($3::uuid[])`,
		boardID, moduleID, taskIDs, visible)
	if err != nil {
		return 0, fmt.Errorf("batch task visibility: %w", err)
	}
	if int(tag.RowsAffected()) != len(taskIDs) {
		return 0, ErrTaskNotFound
	}
	audit.BoardID = &boardID
	audit.ModuleID = &moduleID
	audit.ResourceType = "task"
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit batch visibility: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
