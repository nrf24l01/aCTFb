package boards

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/events"
)

const submissionColumns = `s.id, s.board_id, s.module_id, s.task_id, s.user_id,
	u.username, s.subject_type, s.subject_id,
	COALESCE(CASE WHEN s.subject_type='TEAM' THEN (SELECT t.name FROM teams t WHERE t.id=s.subject_id) ELSE u.username END, u.username),
	s.submitted_by_user_id, s.status, s.score_awarded, s.message, s.idempotency_key,
	s.created_at, s.reviewed_at, s.reviewed_by_user_id, s.review_decision, s.request_hash,
	(to_jsonb(s)->>'submitted_text')`

func scanSubmissionRow(row boardScanner) (*Submission, error) {
	var submission Submission
	var score *int
	var message *string
	if err := row.Scan(
		&submission.ID,
		&submission.BoardID,
		&submission.ModuleID,
		&submission.TaskID,
		&submission.UserID,
		&submission.Username,
		&submission.Subject.Type,
		&submission.Subject.ID,
		&submission.SubjectName,
		&submission.SubmittedByUserID,
		&submission.Status,
		&score,
		&message,
		&submission.IdempotencyKey,
		&submission.CreatedAt,
		&submission.ReviewedAt,
		&submission.ReviewedByUserID,
		&submission.ReviewDecision,
		&submission.requestHash,
		&submission.SubmittedText,
	); err != nil {
		return nil, err
	}
	submission.ScoreAwarded = score
	submission.Message = message
	if submission.ResourceType == "" {
		submission.ResourceType = SubmissionResourceTask
	}
	return &submission, nil
}

func submissionQuery(where string) string {
	return `SELECT ` + submissionColumns + ` FROM submissions s
		JOIN users u ON u.id = s.user_id WHERE ` + where
}

func (s *PostgresStore) FindIdempotentSubmission(ctx context.Context, userID, taskID uuid.UUID, key string) (*Submission, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	submission, err := scanSubmissionRow(p.pool.QueryRow(ctx,
		submissionQuery(`s.user_id=$1 AND s.task_id=$2 AND s.idempotency_key=$3`), userID, taskID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubmissionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find idempotent submission: %w", err)
	}
	return submission, nil
}

func (s *PostgresStore) CreateSubmission(ctx context.Context, in SubmissionParams, audit AuditEventParams) (*SubmissionCreateResult, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin submission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize all submissions for one participant/task. This makes the
	// idempotency and one-accepted checks deterministic across processes.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("p1-submit:%s:%s", in.UserID, in.TaskID)); err != nil {
		return nil, fmt.Errorf("lock submission: %w", err)
	}
	if s.beforeSubmissionWriteCheck != nil {
		s.beforeSubmissionWriteCheck()
	}

	// Idempotent replay is resolved before the deadline/module recheck. A
	// submission already committed inside the window must replay the same result
	// after the window closes without a new write.
	existing, err := scanSubmissionRow(tx.QueryRow(ctx,
		submissionQuery(`s.user_id=$1 AND s.task_id=$2 AND s.idempotency_key=$3`),
		in.UserID, in.TaskID, in.IdempotencyKey))
	if err == nil {
		if subtle.ConstantTimeCompare(existing.requestHash, in.RequestHash) == 1 {
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("commit idempotent replay: %w", err)
			}
			return &SubmissionCreateResult{Submission: existing, AlreadyExisted: true}, nil
		}
		return nil, ErrIdempotencyKeyReused
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check idempotency: %w", err)
	}

	// Authoritative eligibility recheck inside the locked transaction. The
	// service precheck happened before Argon2 verification; only this DB clock
	// and persisted board/module/task state may decide whether a new verdict is
	// written. NOW() is deliberately not used because it is transaction-start
	// time; clock_timestamp() is the actual decision instant.
	var boardStatus, participationMode string
	var boardStarts, boardEnds, moduleStarts, moduleEnds *time.Time
	var moduleEnabled bool
	var moduleType, taskType string
	var taskVisible, joined bool
	var taskPoints int
	var effectiveAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT b.status, b.starts_at, b.ends_at, b.participation_mode, m.enabled, m.type,
			m.starts_at, m.ends_at, t.visible, t.type, t.points,
			EXISTS (SELECT 1 FROM board_participants bp WHERE bp.board_id=b.id AND bp.user_id=$4),
			clock_timestamp()
		FROM tasks t
		JOIN modules m ON m.board_id=t.board_id AND m.id=t.module_id
		JOIN boards b ON b.id=t.board_id
		WHERE t.board_id=$1 AND t.module_id=$2 AND t.id=$3
		FOR SHARE OF t, m, b`,
		in.BoardID, in.ModuleID, in.TaskID, in.UserID,
	).Scan(&boardStatus, &boardStarts, &boardEnds, &participationMode, &moduleEnabled, &moduleType,
		&moduleStarts, &moduleEnds, &taskVisible, &taskType, &taskPoints, &joined, &effectiveAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTaskNotFound
		}
		return nil, fmt.Errorf("recheck submission eligibility: %w", err)
	}
	if !joined {
		return nil, ErrBoardNotJoined
	}
	if err := authoritativeSubmissionState(boardStatus, boardStarts, boardEnds,
		moduleEnabled, moduleType, moduleStarts, moduleEnds, taskType, taskVisible, effectiveAt); err != nil {
		return nil, err
	}
	if in.Status == SubmissionStatusAccepted {
		// Core freezes the authoritative task points inside the same locked
		// transaction. A concurrent task edit cannot silently change the score,
		// and an addon-supplied score never enters this field.
		if taskPoints < 0 || taskPoints > 1_000_000_000 || in.ScoreAwarded == nil || *in.ScoreAwarded != taskPoints {
			return nil, ErrBoardStateConflict
		}
	}
	subject := ScoreSubject{Type: SubjectTypeUser, ID: in.UserID}
	if participationMode == ParticipationTeam {
		var teamID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT team_id FROM team_members WHERE board_id=$1 AND user_id=$2`, in.BoardID, in.UserID).Scan(&teamID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrTeamRequired
			}
			return nil, fmt.Errorf("resolve submission team: %w", err)
		}
		subject = ScoreSubject{Type: SubjectTypeTeam, ID: teamID}
	}

	var solved bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM submissions WHERE subject_type=$1 AND subject_id=$2 AND task_id=$3 AND status='ACCEPTED')`,
		subject.Type, subject.ID, in.TaskID).Scan(&solved); err != nil {
		return nil, fmt.Errorf("check solved: %w", err)
	}
	if solved {
		return nil, ErrTaskAlreadySolved
	}

	var draftPolicy DraftPolicy
	if in.DraftID != nil {
		policy, draftErr := lockAndValidateDraftTx(ctx, tx, in, effectiveAt)
		if draftErr != nil {
			return nil, draftErr
		}
		draftPolicy = policy
	}
	_ = draftPolicy

	var score any
	if in.ScoreAwarded != nil {
		score = *in.ScoreAwarded
	}
	columns := `board_id, module_id, task_id, user_id, subject_type, subject_id,
			submitted_by_user_id, status, score_awarded, message, idempotency_key, request_hash, created_at`
	values := `$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13`
	args := []any{in.BoardID, in.ModuleID, in.TaskID, in.UserID, subject.Type, subject.ID,
		in.UserID, in.Status, score, in.Message, in.IdempotencyKey, in.RequestHash, effectiveAt}
	if in.SubmittedText != nil && *in.SubmittedText != "" {
		// MANUAL text is durable data, not an optional decoration. Silently
		// dropping it when migration 013 has not been applied would corrupt
		// the submission. Fail closed instead.
		if !s.hasSubmittedTextColumn(ctx) {
			return nil, fmt.Errorf("submissions.submitted_text column is missing; apply migration 013_manual_text.sql")
		}
		columns += `, submitted_text`
		values += `,$14`
		args = append(args, *in.SubmittedText)
	}
	_, err = tx.Exec(ctx, `INSERT INTO submissions (`+columns+`) VALUES (`+values+`)`, args...)
	if err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			if strings.Contains(fmt.Sprint(err), "submissions_one_accepted") {
				return nil, ErrTaskAlreadySolved
			}
			return nil, ErrIdempotencyKeyReused
		}
		return nil, fmt.Errorf("insert submission: %w", err)
	}

	created, err := scanSubmissionRow(tx.QueryRow(ctx,
		submissionQuery(`s.user_id=$1 AND s.task_id=$2 AND s.idempotency_key=$3`),
		in.UserID, in.TaskID, in.IdempotencyKey))
	if err != nil {
		return nil, fmt.Errorf("reload submission: %w", err)
	}
	if in.DraftID != nil {
		refs, attachErr := attachDraftFilesTx(ctx, tx, *in.DraftID, created.ID, effectiveAt)
		if attachErr != nil {
			return nil, attachErr
		}
		created.Files = refs
	}

	if in.Outbox != nil && in.Status != SubmissionStatusPendingRev {
		payload, marshalErr := json.Marshal(events.SubmissionJudgedPayload{
			SubmissionUUID: created.ID, BoardUUID: in.BoardID, ModuleUUID: in.ModuleID,
			TaskUUID: in.TaskID, SubjectType: subject.Type, SubjectUUID: subject.ID,
			Status: in.Status, ScoreAwarded: in.ScoreAwarded, OccurredAt: effectiveAt,
		})
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal submission event: %w", marshalErr)
		}
		if err := in.Outbox.Enqueue(ctx, tx, events.Event{
			ID: uuid.New(), Type: events.EventSubmissionJudged, OccurredAt: effectiveAt,
			BoardID: &in.BoardID, ModuleID: &in.ModuleID,
			SubjectType: subject.Type, SubjectID: &subject.ID, Payload: payload,
		}); err != nil {
			return nil, err
		}
	}
	audit.BoardID = &in.BoardID
	audit.ModuleID = &in.ModuleID
	audit.ResourceID = &created.ID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit submission: %w", err)
	}
	return &SubmissionCreateResult{Submission: created}, nil
}

func authoritativeSubmissionState(
	boardStatus string,
	boardStarts, boardEnds *time.Time,
	moduleEnabled bool,
	moduleType string,
	moduleStarts, moduleEnds *time.Time,
	taskType string,
	taskVisible bool,
	now time.Time,
) error {
	switch BoardStatus(boardStatus) {
	case BoardStatusDraft:
		return ErrBoardNotStarted
	case BoardStatusScheduled:
		if boardStarts == nil || now.Before(*boardStarts) {
			return ErrBoardNotStarted
		}
	case BoardStatusActive:
		if boardStarts != nil && now.Before(*boardStarts) {
			return ErrBoardNotStarted
		}
	case BoardStatusFinished, BoardStatusArchived:
		return ErrBoardFinished
	default:
		return ErrBoardNotStarted
	}
	if boardEnds != nil && !now.Before(*boardEnds) {
		return ErrBoardFinished
	}
	if !isTaskModuleType(moduleType) {
		return ErrModuleTypeMismatch
	}
	if !moduleEnabled {
		return ErrModuleNotAvailable
	}
	effectiveModuleStart := moduleStarts
	if effectiveModuleStart == nil {
		effectiveModuleStart = boardStarts
	}
	if effectiveModuleStart != nil && now.Before(*effectiveModuleStart) {
		return ErrModuleNotAvailable
	}
	effectiveModuleEnd := moduleEnds
	if effectiveModuleEnd == nil {
		effectiveModuleEnd = boardEnds
	}
	if effectiveModuleEnd != nil && !now.Before(*effectiveModuleEnd) {
		return ErrModuleNotAvailable
	}
	if (taskType != TaskTypeFlag && taskType != TaskTypeManual && taskType != TaskTypeInstance && taskType != TaskTypeExtension) || !taskVisible {
		return ErrTaskNotAvailable
	}
	return nil
}

func (s *PostgresStore) GetSubmission(ctx context.Context, boardID, submissionID uuid.UUID) (*Submission, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	submission, err := scanSubmissionRow(p.pool.QueryRow(ctx,
		submissionQuery(`s.board_id=$1 AND s.id=$2`), boardID, submissionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubmissionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get submission: %w", err)
	}
	return submission, nil
}

func (s *PostgresStore) ListSubmissions(ctx context.Context, filter SubmissionFilter) ([]Submission, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}
	where := []string{"s.board_id=$1"}
	args := []any{filter.BoardID}
	if filter.ModuleID != nil {
		args = append(args, *filter.ModuleID)
		where = append(where, fmt.Sprintf("s.module_id=$%d", len(args)))
	}
	if len(filter.ModuleIDs) > 0 {
		parts := make([]string, 0, len(filter.ModuleIDs))
		for _, moduleID := range filter.ModuleIDs {
			args = append(args, moduleID)
			parts = append(parts, fmt.Sprintf("s.module_id=$%d", len(args)))
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if filter.TaskID != nil {
		args = append(args, *filter.TaskID)
		where = append(where, fmt.Sprintf("s.task_id=$%d", len(args)))
	}
	if filter.UserID != nil {
		args = append(args, *filter.UserID)
		where = append(where, fmt.Sprintf("s.user_id=$%d", len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = append(where, fmt.Sprintf("s.status=$%d", len(args)))
	}
	baseWhere := strings.Join(where, " AND ")

	var total int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM submissions s WHERE `+baseWhere, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list submissions count: %w", err)
	}
	if total == 0 {
		return []Submission{}, 0, nil
	}

	listArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)
	rows, err := p.pool.Query(ctx, submissionQuery(baseWhere)+
		` ORDER BY s.created_at DESC, s.id DESC LIMIT $`+fmt.Sprint(len(args)+1)+
		` OFFSET $`+fmt.Sprint(len(args)+2), listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list submissions: %w", err)
	}
	defer rows.Close()
	items := make([]Submission, 0, filter.Limit)
	for rows.Next() {
		submission, err := scanSubmissionRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan submission: %w", err)
		}
		items = append(items, *submission)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate submissions: %w", err)
	}
	return items, total, nil
}

func (s *PostgresStore) ListOwnTaskSubmissions(ctx context.Context, userID uuid.UUID, path TaskPath, limit, offset int) ([]Submission, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := p.pool.QueryRow(ctx, `
		SELECT count(*) FROM submissions
		WHERE user_id=$1 AND board_id=$2 AND module_id=$3 AND task_id=$4`,
		userID, path.BoardID, path.ModuleID, path.TaskID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count own submissions: %w", err)
	}
	if total == 0 {
		return []Submission{}, 0, nil
	}
	rows, err := p.pool.Query(ctx, submissionQuery(
		`s.user_id=$1 AND s.board_id=$2 AND s.module_id=$3 AND s.task_id=$4`)+
		` ORDER BY s.created_at DESC, s.id DESC LIMIT $5 OFFSET $6`,
		userID, path.BoardID, path.ModuleID, path.TaskID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list own submissions: %w", err)
	}
	defer rows.Close()
	items := make([]Submission, 0, limit)
	for rows.Next() {
		submission, err := scanSubmissionRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan own submission: %w", err)
		}
		items = append(items, *submission)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate own submissions: %w", err)
	}
	return items, total, nil
}

func (s *PostgresStore) ListSubjectTaskSubmissions(ctx context.Context, subject ScoreSubject, path TaskPath, limit, offset int) ([]Submission, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := p.pool.QueryRow(ctx, `
		SELECT count(*) FROM submissions
		WHERE subject_type=$1 AND subject_id=$2 AND board_id=$3 AND module_id=$4 AND task_id=$5`,
		subject.Type, subject.ID, path.BoardID, path.ModuleID, path.TaskID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count subject submissions: %w", err)
	}
	if total == 0 {
		return []Submission{}, 0, nil
	}
	rows, err := p.pool.Query(ctx, submissionQuery(
		`s.subject_type=$1 AND s.subject_id=$2 AND s.board_id=$3 AND s.module_id=$4 AND s.task_id=$5`)+
		` ORDER BY s.created_at DESC, s.id DESC LIMIT $6 OFFSET $7`,
		subject.Type, subject.ID, path.BoardID, path.ModuleID, path.TaskID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list subject submissions: %w", err)
	}
	defer rows.Close()
	items := make([]Submission, 0, limit)
	for rows.Next() {
		submission, err := scanSubmissionRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan subject submission: %w", err)
		}
		items = append(items, *submission)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate subject submissions: %w", err)
	}
	return items, total, nil
}

func lockAndValidateDraftTx(ctx context.Context, tx pgx.Tx, in SubmissionParams, now time.Time) (DraftPolicy, error) {
	var status, resourceType string
	var expiresAt time.Time
	var ownerID, draftBoard, draftModule uuid.UUID
	var draftTask *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT status, expires_at, created_by_user_id, board_id, module_id, task_id, resource_type
		FROM submission_drafts WHERE id=$1 FOR UPDATE`, *in.DraftID,
	).Scan(&status, &expiresAt, &ownerID, &draftBoard, &draftModule, &draftTask, &resourceType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DraftPolicy{}, ErrDraftNotFound
		}
		return DraftPolicy{}, fmt.Errorf("lock submission draft: %w", err)
	}
	if status == "CONSUMED" {
		return DraftPolicy{}, ErrDraftNotReady
	}
	if status != "OPEN" {
		return DraftPolicy{}, ErrDraftForbidden
	}
	if !now.Before(expiresAt) {
		return DraftPolicy{}, ErrDraftExpired
	}
	if ownerID != in.SubmittedByUserID || draftBoard != in.BoardID || draftModule != in.ModuleID ||
		draftTask == nil || *draftTask != in.TaskID || resourceType != SubmissionResourceTask {
		return DraftPolicy{}, ErrDraftForbidden
	}
	var filesPolicy []byte
	if err := tx.QueryRow(ctx, `SELECT submission_files_policy FROM tasks WHERE board_id=$1 AND module_id=$2 AND id=$3`,
		in.BoardID, in.ModuleID, in.TaskID).Scan(&filesPolicy); err != nil {
		return DraftPolicy{}, fmt.Errorf("load task files policy: %w", err)
	}
	policy := DraftPolicy{FilesRequired: true, FilesMinCount: 1, FilesMaxCount: 100, FilesMaxSize: 1 << 30}
	if len(filesPolicy) > 0 {
		var rawPolicy struct {
			Required            bool     `json:"required"`
			MinCount            int      `json:"minCount"`
			MaxCount            int      `json:"maxCount"`
			MaxSizeBytes        int64    `json:"maxSizeBytes"`
			AllowedContentTypes []string `json:"allowedContentTypes"`
		}
		if err := json.Unmarshal(filesPolicy, &rawPolicy); err != nil {
			return DraftPolicy{}, fmt.Errorf("decode task files policy: %w", err)
		}
		policy = DraftPolicy{FilesRequired: rawPolicy.Required, FilesMinCount: rawPolicy.MinCount, FilesMaxCount: rawPolicy.MaxCount, FilesMaxSize: rawPolicy.MaxSizeBytes, AllowedTypes: rawPolicy.AllowedContentTypes}
	}
	var total, ready, rejected int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status='READY'), count(*) FILTER (WHERE status='REJECTED')
		FROM file_objects WHERE draft_id=$1`, *in.DraftID).Scan(&total, &ready, &rejected); err != nil {
		return DraftPolicy{}, fmt.Errorf("inspect draft files: %w", err)
	}
	if rejected > 0 || ready != total {
		return DraftPolicy{}, ErrDraftNotReady
	}
	if total < policy.FilesMinCount || total > policy.FilesMaxCount {
		return DraftPolicy{}, ErrDraftNotReady
	}
	return policy, nil
}

func attachDraftFilesTx(ctx context.Context, tx pgx.Tx, draftID, submissionID uuid.UUID, now time.Time) ([]FileRef, error) {
	rows, err := tx.Query(ctx, `
		UPDATE file_objects SET status='ATTACHED', attached_submission_id=$2
		WHERE draft_id=$1 AND status='READY'
		RETURNING id, filename, content_type, size_bytes, sha256`, draftID, submissionID)
	if err != nil {
		return nil, fmt.Errorf("attach draft files: %w", err)
	}
	defer rows.Close()
	var refs []FileRef
	for rows.Next() {
		var ref FileRef
		if err := rows.Scan(&ref.UUID, &ref.Filename, &ref.ContentType, &ref.Size, &ref.SHA256); err != nil {
			return nil, fmt.Errorf("scan attached file: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attached files: %w", err)
	}
	for _, ref := range refs {
		if _, err := tx.Exec(ctx, `INSERT INTO submission_files (submission_id,file_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, submissionID, ref.UUID); err != nil {
			return nil, fmt.Errorf("link submission file: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE submission_drafts SET status='CONSUMED', consumed_at=$2 WHERE id=$1 AND status='OPEN'`, draftID, now)
	if err != nil {
		return nil, fmt.Errorf("consume submission draft: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrDraftNotReady
	}
	return refs, nil
}

func (s *PostgresStore) ReviewSubmission(ctx context.Context, in ReviewParams, audit AuditEventParams) (*Submission, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin review: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
		fmt.Sprintf("p1-review:%s", in.SubmissionID)); err != nil {
		return nil, fmt.Errorf("lock review: %w", err)
	}

	if in.IdempotencyKey != "" {
		var storedHash string
		err := tx.QueryRow(ctx, `
			SELECT details->>'requestHash' FROM audit_events
			WHERE action='submission.review' AND resource_id=$1
			  AND details->>'idempotencyKey'=$2
			ORDER BY created_at DESC LIMIT 1`, in.SubmissionID, in.IdempotencyKey).Scan(&storedHash)
		if err == nil {
			if storedHash != fmt.Sprintf("%x", in.RequestHash) {
				return nil, ErrIdempotencyKeyReused
			}
			existing, scanErr := scanSubmissionRow(tx.QueryRow(ctx,
				submissionQuery(`s.board_id=$1 AND s.id=$2`), in.BoardID, in.SubmissionID))
			if scanErr != nil {
				return nil, scanErr
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return nil, commitErr
			}
			return existing, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("check review idempotency: %w", err)
		}
	}

	var checker, status string
	err = tx.QueryRow(ctx, `
		SELECT t.submission_checker, s.status
		FROM submissions s JOIN tasks t ON t.board_id=s.board_id AND t.module_id=s.module_id AND t.id=s.task_id
		WHERE s.board_id=$1 AND s.id=$2 FOR UPDATE OF s`, in.BoardID, in.SubmissionID,
	).Scan(&checker, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubmissionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load submission for review: %w", err)
	}
	if checker != SubmissionCheckerManual {
		return nil, ErrSubmissionReviewNotAllowed
	}
	if status != SubmissionStatusPendingRev {
		return nil, ErrSubmissionAlreadyReviewed
	}

	var score any
	if in.ScoreAwarded != nil {
		score = *in.ScoreAwarded
	}
	if _, err := tx.Exec(ctx, `
		UPDATE submissions SET status=$3, score_awarded=$4, message=$5, reviewed_at=$6,
			reviewed_by_user_id=$7, review_decision=$8
		WHERE board_id=$1 AND id=$2`, in.BoardID, in.SubmissionID, in.Status, score, in.Message,
		in.ReviewedAt, in.ReviewedByUserID, in.ReviewDecision); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTaskAlreadySolved
		}
		return nil, fmt.Errorf("update review: %w", err)
	}
	reviewed, err := scanSubmissionRow(tx.QueryRow(ctx,
		submissionQuery(`s.board_id=$1 AND s.id=$2`), in.BoardID, in.SubmissionID))
	if err != nil {
		return nil, fmt.Errorf("reload reviewed submission: %w", err)
	}
	if in.Outbox != nil {
		payload, marshalErr := json.Marshal(events.SubmissionJudgedPayload{
			SubmissionUUID: reviewed.ID, BoardUUID: reviewed.BoardID, ModuleUUID: reviewed.ModuleID,
			TaskUUID: reviewed.TaskID, SubjectType: reviewed.Subject.Type, SubjectUUID: reviewed.Subject.ID,
			Status: reviewed.Status, ScoreAwarded: reviewed.ScoreAwarded, OccurredAt: in.ReviewedAt,
		})
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal review event: %w", marshalErr)
		}
		if err := in.Outbox.Enqueue(ctx, tx, events.Event{
			ID: uuid.New(), Type: events.EventSubmissionJudged, OccurredAt: in.ReviewedAt,
			BoardID: &reviewed.BoardID, ModuleID: &reviewed.ModuleID,
			SubjectType: reviewed.Subject.Type, SubjectID: &reviewed.Subject.ID, Payload: payload,
		}); err != nil {
			return nil, err
		}
	}
	audit.BoardID = &in.BoardID
	audit.ResourceID = &in.SubmissionID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit review: %w", err)
	}
	return reviewed, nil
}

func (s *PostgresStore) Scoreboard(ctx context.Context, boardID uuid.UUID) ([]ScoreboardItem, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `
		WITH bm AS (
			SELECT participation_mode FROM boards WHERE id=$1
		), subjects AS (
			SELECT 'USER'::text AS subject_type, p.user_id AS subject_id, u.username AS name
			FROM board_participants p JOIN users u ON u.id=p.user_id
			CROSS JOIN bm WHERE p.board_id=$1 AND bm.participation_mode='INDIVIDUAL'
			UNION ALL
			SELECT 'TEAM'::text, t.id, t.name
			FROM teams t CROSS JOIN bm WHERE t.board_id=$1 AND bm.participation_mode='TEAM'
		), accepted AS (
			SELECT subject_type, subject_id, SUM(score_awarded)::bigint AS score,
				MIN(created_at) AS first_accepted
			FROM submissions
			WHERE board_id=$1 AND status='ACCEPTED'
			GROUP BY subject_type, subject_id
		), adjustments AS (
			SELECT details->>'subjectType' AS subject_type,
				(details->>'subjectUuid')::uuid AS subject_id,
				COALESCE(SUM((details->>'delta')::bigint),0) AS delta
			FROM audit_events
			WHERE board_id=$1 AND action='score.adjustment'
			GROUP BY details->>'subjectType', (details->>'subjectUuid')::uuid
		)
		SELECT s.subject_type, s.subject_id, s.name,
			COALESCE(a.score,0) + COALESCE(adj.delta,0) AS score,
			a.first_accepted
		FROM subjects s
		LEFT JOIN accepted a ON a.subject_type=s.subject_type AND a.subject_id=s.subject_id
		LEFT JOIN adjustments adj ON adj.subject_type=s.subject_type AND adj.subject_id=s.subject_id
		ORDER BY score DESC, a.first_accepted ASC NULLS LAST, s.subject_id ASC`, boardID)
	if err != nil {
		return nil, fmt.Errorf("load scoreboard: %w", err)
	}
	defer rows.Close()
	var items []ScoreboardItem
	for rows.Next() {
		var item ScoreboardItem
		var score int64
		var first *time.Time
		if err := rows.Scan(&item.Subject.Type, &item.Subject.ID, &item.Name, &score, &first); err != nil {
			return nil, fmt.Errorf("scan scoreboard: %w", err)
		}
		item.Score = int(score)
		item.FirstAt = first
		if item.Subject.Type == SubjectTypeUser {
			item.UserID = item.Subject.ID
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scoreboard: %w", err)
	}
	for i := range items {
		if i > 0 && items[i].Score == items[i-1].Score {
			items[i].Place = items[i-1].Place
		} else {
			items[i].Place = i + 1
		}
	}
	return items, nil
}

func (s *PostgresStore) BoardMe(ctx context.Context, boardID, userID uuid.UUID) (*BoardMe, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	board, err := s.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	var joinedAt time.Time
	err = p.pool.QueryRow(ctx, `
		SELECT joined_at FROM board_participants WHERE board_id=$1 AND user_id=$2`, boardID, userID,
	).Scan(&joinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &BoardMe{ParticipationMode: board.ParticipationMode, Audiences: []AudienceSummary{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load board me: %w", err)
	}
	me := &BoardMe{Joined: true, JoinedAt: &joinedAt, ParticipationMode: board.ParticipationMode, Audiences: []AudienceSummary{}}
	if board.ParticipationMode == ParticipationTeam {
		team, teamErr := s.GetTeamByMember(ctx, boardID, userID)
		if errors.Is(teamErr, ErrTeamNotFound) {
			return me, nil
		}
		if teamErr != nil {
			return nil, teamErr
		}
		me.Team = team
	}
	subject, err := s.ResolveSubject(ctx, boardID, userID)
	if err != nil {
		return nil, err
	}
	items, err := s.Scoreboard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Subject.Type == subject.Type && item.Subject.ID == subject.ID {
			score := item.Score
			place := item.Place
			me.Score = &score
			me.Place = &place
			break
		}
	}
	return me, nil
}

func (s *PostgresStore) hasSubmittedTextColumn(ctx context.Context) bool {
	s.submittedTextOnce.Do(func() {
		p, err := s.pool()
		if err != nil {
			return
		}
		if err := p.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema=current_schema() AND table_name='submissions' AND column_name='submitted_text'
			)`).Scan(&s.submittedTextOK); err != nil {
			s.submittedTextOK = false
		}
	})
	return s.submittedTextOK
}
