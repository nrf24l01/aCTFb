package files

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const fileColumns = `
	id, board_id, module_id, task_id, draft_id, uploader_user_id, purpose,
	filename, content_type, size_bytes, sha256, quarantine_bucket,
	quarantine_key, clean_bucket, clean_key, status, rejection_code,
	created_at, validated_at, attached_submission_id`

type store struct {
	db *postgres.DB
}

func newStore(db *postgres.DB) *store {
	return &store{db: db}
}

func nullableUUID(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return *value
}

func uuidPtr(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}

func textPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func timePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

func scanFileRecord(row pgx.Row) (*FileRecord, error) {
	var (
		record                           FileRecord
		taskID, draftID, attached        pgtype.UUID
		cleanBucket, cleanKey, rejection pgtype.Text
		status                           string
		validatedAt                      pgtype.Timestamptz
	)
	err := row.Scan(
		&record.ID, &record.BoardID, &record.ModuleID, &taskID, &draftID,
		&record.UploaderUserID, &record.Purpose, &record.Filename,
		&record.ContentType, &record.Size, &record.SHA256,
		&record.QuarantineBucket, &record.QuarantineKey, &cleanBucket,
		&cleanKey, &status, &rejection, &record.CreatedAt, &validatedAt,
		&attached,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("files: scan file row: %w", err)
	}
	record.TaskID = uuidPtr(taskID)
	record.DraftID = uuidPtr(draftID)
	record.CleanBucket = textPtr(cleanBucket)
	record.CleanKey = textPtr(cleanKey)
	record.Status = Status(status)
	record.RejectionCode = textPtr(rejection)
	record.ValidatedAt = timePtr(validatedAt)
	record.AttachedSubmission = uuidPtr(attached)
	return &record, nil
}

func (s *store) createDraft(ctx context.Context, in CreateDraftInput, expiresAt time.Time) (*Draft, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("files: begin draft: %w", err)
	}
	defer tx.Rollback(ctx)

	// Serialize one owner's drafts for the exact resource. The schema has no
	// partial unique index for OPEN drafts, so an advisory transaction lock is
	// the race-free guard and is released automatically on commit/rollback.
	lockKey := fmt.Sprintf("%s|%s|%s|%s|%v", in.Actor.UserID, in.BoardID, in.ModuleID, in.ResourceType, in.TaskID)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return nil, fmt.Errorf("files: lock draft scope: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM submission_drafts
			WHERE created_by_user_id = $1 AND board_id = $2 AND module_id = $3
			  AND resource_type = $4
			  AND (($5::uuid IS NULL AND task_id IS NULL) OR task_id = $5::uuid)
			  AND status = 'OPEN' AND expires_at > clock_timestamp()
		)
	`, in.Actor.UserID, in.BoardID, in.ModuleID, in.ResourceType, nullableUUID(in.TaskID)).Scan(&exists); err != nil {
		return nil, fmt.Errorf("files: check open draft: %w", err)
	}
	if exists {
		return nil, ErrDraftAlreadyOpen
	}

	var (
		id      uuid.UUID
		created time.Time
	)
	if err := tx.QueryRow(ctx, `
		INSERT INTO submission_drafts
			(id, board_id, module_id, resource_type, task_id, created_by_user_id, status, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'OPEN', clock_timestamp(), $7)
		RETURNING id, created_at
	`, uuid.New(), in.BoardID, in.ModuleID, in.ResourceType, nullableUUID(in.TaskID), in.Actor.UserID, expiresAt).Scan(&id, &created); err != nil {
		return nil, fmt.Errorf("files: create draft: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("files: commit draft: %w", err)
	}
	return &Draft{
		UUID: id, BoardID: in.BoardID, ModuleID: in.ModuleID,
		ResourceType: in.ResourceType, TaskID: in.TaskID, Status: DraftOpen,
		CreatedAt: created.UTC(), ExpiresAt: expiresAt.UTC(), Files: []PendingFile{},
	}, nil
}

func (s *store) getDraft(ctx context.Context, draftID uuid.UUID, ownerID *uuid.UUID, forUpdate bool) (*Draft, error) {
	query := `
		SELECT id, board_id, module_id, resource_type, task_id, created_by_user_id,
		       status, created_at, expires_at
		FROM submission_drafts
		WHERE id = $1`
	args := []any{draftID}
	if ownerID != nil {
		query += ` AND created_by_user_id = $2`
		args = append(args, *ownerID)
	}
	if forUpdate {
		query += ` FOR UPDATE`
	}

	var (
		draft       Draft
		taskID      pgtype.UUID
		status      string
		ownerUserID uuid.UUID
	)
	err := s.db.Pool.QueryRow(ctx, query, args...).Scan(
		&draft.UUID, &draft.BoardID, &draft.ModuleID, &draft.ResourceType,
		&taskID, &ownerUserID, &status, &draft.CreatedAt, &draft.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDraftNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("files: load draft: %w", err)
	}
	draft.TaskID = uuidPtr(taskID)
	draft.Status = DraftStatus(status)
	draft.CreatedAt = draft.CreatedAt.UTC()
	draft.ExpiresAt = draft.ExpiresAt.UTC()

	files, err := s.listDraftFiles(ctx, draftID)
	if err != nil {
		return nil, err
	}
	draft.Files = files
	return &draft, nil
}

func (s *store) listDraftFiles(ctx context.Context, draftID uuid.UUID) ([]PendingFile, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT `+fileColumns+`
		FROM file_objects
		WHERE draft_id = $1
		ORDER BY created_at, id
	`, draftID)
	if err != nil {
		return nil, fmt.Errorf("files: list draft files: %w", err)
	}
	defer rows.Close()

	files := make([]PendingFile, 0, 4)
	for rows.Next() {
		record, err := scanFileRecord(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, PendingFile{
			Ref: Ref{
				UUID: record.ID, Filename: record.Filename,
				ContentType: record.ContentType, Size: record.Size, SHA256: record.SHA256,
			},
			Status: record.Status, RejectionCode: record.RejectionCode,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("files: iterate draft files: %w", err)
	}
	return files, nil
}

func (s *store) createFile(ctx context.Context, record FileRecord) (*FileRecord, error) {
	var id uuid.UUID
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO file_objects
			(id, board_id, module_id, task_id, draft_id, uploader_user_id, purpose,
			 filename, content_type, size_bytes, sha256, quarantine_bucket,
			 quarantine_key, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'AWAITING_UPLOAD',clock_timestamp())
		RETURNING id
	`,
		uuid.New(), record.BoardID, record.ModuleID, nullableUUID(record.TaskID),
		nullableUUID(record.DraftID), record.UploaderUserID, record.Purpose,
		record.Filename, record.ContentType, record.Size, record.SHA256,
		record.QuarantineBucket, record.QuarantineKey,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("files: create file record: %w", err)
	}
	return s.getFile(ctx, id)
}

// createFileWithLimit locks the draft row, re-checks OPEN/unexpired, enforces
// the trusted max count and inserts the quarantine record in one transaction.
func (s *store) createFileWithLimit(ctx context.Context, record FileRecord, maxCount int) (*FileRecord, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("files: begin presign record: %w", err)
	}
	defer tx.Rollback(ctx)

	if record.DraftID == nil {
		return nil, fmt.Errorf("files: draft file requires a draft")
	}
	var (
		status  string
		expired bool
	)
	err = tx.QueryRow(ctx, `
		SELECT status, expires_at <= clock_timestamp()
		FROM submission_drafts WHERE id = $1 FOR UPDATE
	`, *record.DraftID).Scan(&status, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDraftNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("files: lock draft for presign: %w", err)
	}
	if DraftStatus(status) == DraftConsumed {
		return nil, ErrDraftAlreadyUsed
	}
	if DraftStatus(status) == DraftExpired || expired {
		return nil, ErrDraftExpired
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM file_objects WHERE draft_id = $1`, *record.DraftID).Scan(&count); err != nil {
		return nil, fmt.Errorf("files: count draft files: %w", err)
	}
	if count >= maxCount {
		return nil, ErrFileLimitReached
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO file_objects
			(id, board_id, module_id, task_id, draft_id, uploader_user_id, purpose,
			 filename, content_type, size_bytes, sha256, quarantine_bucket,
			 quarantine_key, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'AWAITING_UPLOAD',clock_timestamp())
		RETURNING id
	`,
		uuid.New(), record.BoardID, record.ModuleID, nullableUUID(record.TaskID),
		nullableUUID(record.DraftID), record.UploaderUserID, record.Purpose,
		record.Filename, record.ContentType, record.Size, record.SHA256,
		record.QuarantineBucket, record.QuarantineKey,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("files: create file record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("files: commit presign record: %w", err)
	}
	return s.getFile(ctx, id)
}

func (s *store) getFile(ctx context.Context, fileID uuid.UUID) (*FileRecord, error) {
	return scanFileRecord(s.db.Pool.QueryRow(ctx, `
		SELECT `+fileColumns+` FROM file_objects WHERE id = $1
	`, fileID))
}

func (s *store) getDraftFile(ctx context.Context, draftID, fileID uuid.UUID) (*FileRecord, error) {
	record, err := scanFileRecord(s.db.Pool.QueryRow(ctx, `
		SELECT `+fileColumns+`
		FROM file_objects
		WHERE id = $1 AND draft_id = $2
	`, fileID, draftID))
	if errors.Is(err, ErrFileNotFound) {
		return nil, ErrFileNotFound
	}
	return record, err
}

func (s *store) markFileValidating(ctx context.Context, fileID, draftID uuid.UUID) (bool, error) {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE file_objects
		SET status = 'VALIDATING', rejection_code = NULL
		WHERE id = $1 AND draft_id = $2 AND status = 'AWAITING_UPLOAD'
	`, fileID, draftID)
	if err != nil {
		return false, fmt.Errorf("files: mark file validating: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *store) markFileValidatingWithoutDraft(ctx context.Context, fileID uuid.UUID) (bool, error) {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE file_objects
		SET status = 'VALIDATING', rejection_code = NULL
		WHERE id = $1 AND draft_id IS NULL AND status = 'AWAITING_UPLOAD'
	`, fileID)
	if err != nil {
		return false, fmt.Errorf("files: mark attachment validating: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *store) markFileAwaiting(ctx context.Context, fileID uuid.UUID) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE file_objects SET status = 'AWAITING_UPLOAD'
		WHERE id = $1 AND status = 'VALIDATING'
	`, fileID)
	if err != nil {
		return fmt.Errorf("files: revert file state: %w", err)
	}
	return nil
}

func (s *store) markScannerUnavailable(ctx context.Context, fileID uuid.UUID) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE file_objects
		SET rejection_code = 'SCANNER_UNAVAILABLE'
		WHERE id = $1 AND status = 'VALIDATING'
	`, fileID)
	if err != nil {
		return fmt.Errorf("files: record scanner unavailable: %w", err)
	}
	return nil
}

func (s *store) markFileRejected(ctx context.Context, fileID uuid.UUID, code string) (bool, error) {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE file_objects
		SET status = 'REJECTED', rejection_code = $2, validated_at = clock_timestamp()
		WHERE id = $1 AND status = 'VALIDATING'
	`, fileID, code)
	if err != nil {
		return false, fmt.Errorf("files: reject file: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// markFileReadyConditional never overwrites ATTACHED, CONSUMED or any state
// changed by another scanner attempt. It also refuses to publish a READY file
// for an expired/consumed draft.
func (s *store) markFileReadyConditional(ctx context.Context, fileID uuid.UUID, detectedType, cleanBucket, cleanKey, sha256 string) (bool, error) {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE file_objects
		SET status = 'READY', content_type = $2, clean_bucket = $3,
		    clean_key = $4, sha256 = $5, rejection_code = NULL,
		    validated_at = clock_timestamp()
		WHERE id = $1
		  AND status = 'VALIDATING'
		  AND (
		    draft_id IS NULL OR EXISTS (
		      SELECT 1 FROM submission_drafts d
		      WHERE d.id = file_objects.draft_id
		        AND d.status = 'OPEN'
		        AND d.expires_at > clock_timestamp()
		    )
		  )
	`, fileID, detectedType, cleanBucket, cleanKey, sha256)
	if err != nil {
		return false, fmt.Errorf("files: mark file ready: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// attachAdministrativeFile marks a clean admin attachment ATTACHED and writes
// task_attachments atomically. Submission draft files never use this method.
func (s *store) attachAdministrativeFile(ctx context.Context, fileID uuid.UUID) (bool, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("files: begin attach: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		purpose string
		taskID  pgtype.UUID
		status  string
	)
	err = tx.QueryRow(ctx, `
		SELECT purpose, task_id, status
		FROM file_objects
		WHERE id = $1
		FOR UPDATE
	`, fileID).Scan(&purpose, &taskID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrFileNotFound
	}
	if err != nil {
		return false, fmt.Errorf("files: lock attachment: %w", err)
	}
	if status == string(StatusAttached) {
		return false, tx.Commit(ctx)
	}
	if status != string(StatusReady) {
		return false, nil
	}
	if purpose != PurposeTaskAttachment && purpose != PurposePentestAttachment {
		return false, nil
	}
	if purpose == PurposeTaskAttachment {
		if !taskID.Valid {
			return false, ErrFileContextMismatch
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_attachments (task_id, file_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, uuid.UUID(taskID.Bytes), fileID); err != nil {
			return false, fmt.Errorf("files: attach task file: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE file_objects SET status = 'ATTACHED', validated_at = COALESCE(validated_at, clock_timestamp())
		WHERE id = $1 AND status = 'READY'
	`, fileID); err != nil {
		return false, fmt.Errorf("files: mark attachment attached: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("files: commit attachment: %w", err)
	}
	return true, nil
}

func (s *store) deleteFile(ctx context.Context, fileID uuid.UUID) (*FileRecord, error) {
	record, err := s.getFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if record.Status == StatusAttached {
		return nil, ErrFileAlreadyAttached
	}
	tag, err := s.db.Pool.Exec(ctx, `DELETE FROM file_objects WHERE id = $1 AND status <> 'ATTACHED'`, fileID)
	if err != nil {
		return nil, fmt.Errorf("files: delete file: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrFileAlreadyAttached
	}
	return record, nil
}

func (s *store) deleteAttachment(ctx context.Context, fileID uuid.UUID) (*FileRecord, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("files: begin delete attachment: %w", err)
	}
	defer tx.Rollback(ctx)

	record, err := scanFileRecord(tx.QueryRow(ctx, `
		SELECT `+fileColumns+` FROM file_objects WHERE id = $1 FOR UPDATE
	`, fileID))
	if err != nil {
		return nil, err
	}
	if record.Purpose != PurposeTaskAttachment && record.Purpose != PurposePentestAttachment {
		return nil, ErrFileContextMismatch
	}
	var inUse bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM submission_files WHERE file_id = $1)
		    OR EXISTS (SELECT 1 FROM pentest_submission_files WHERE file_id = $1)
	`, fileID).Scan(&inUse); err != nil {
		return nil, fmt.Errorf("files: check attachment use: %w", err)
	}
	if inUse {
		return nil, ErrFileInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM task_attachments WHERE file_id = $1`, fileID); err != nil {
		return nil, fmt.Errorf("files: unlink attachment: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM file_objects WHERE id = $1`, fileID); err != nil {
		return nil, fmt.Errorf("files: delete attachment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("files: commit delete attachment: %w", err)
	}
	return record, nil
}

func (s *store) deleteDraft(ctx context.Context, draftID, ownerID uuid.UUID) (*Draft, []FileRecord, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("files: begin delete draft: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		draft  Draft
		status string
		taskID pgtype.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT id, board_id, module_id, resource_type, task_id, status, created_at, expires_at
		FROM submission_drafts
		WHERE id = $1 AND created_by_user_id = $2
		FOR UPDATE
	`, draftID, ownerID).Scan(
		&draft.UUID, &draft.BoardID, &draft.ModuleID, &draft.ResourceType,
		&taskID, &status, &draft.CreatedAt, &draft.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrDraftNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("files: lock draft: %w", err)
	}
	if DraftStatus(status) == DraftConsumed {
		return nil, nil, ErrDraftAlreadyUsed
	}
	draft.TaskID = uuidPtr(taskID)
	draft.Status = DraftStatus(status)

	rows, err := tx.Query(ctx, `
		SELECT `+fileColumns+` FROM file_objects WHERE draft_id = $1
	`, draftID)
	if err != nil {
		return nil, nil, fmt.Errorf("files: list draft cleanup rows: %w", err)
	}
	files := make([]FileRecord, 0, 4)
	for rows.Next() {
		record, err := scanFileRecord(rows)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		files = append(files, *record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("files: iterate cleanup rows: %w", err)
	}
	rows.Close()

	if _, err := tx.Exec(ctx, `DELETE FROM submission_drafts WHERE id = $1`, draftID); err != nil {
		return nil, nil, fmt.Errorf("files: delete draft: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("files: commit delete draft: %w", err)
	}
	return &draft, files, nil
}

func (s *store) taskScope(ctx context.Context, boardID, moduleID, taskID uuid.UUID) (*taskScopeRow, error) {
	var row taskScopeRow
	err := s.db.Pool.QueryRow(ctx, `
		SELECT t.id, t.board_id, t.module_id, t.visible, t.type,
		       t.submission_mode, t.submission_files_policy,
		       m.type, m.enabled, m.starts_at, m.ends_at,
		       b.status, b.starts_at, b.ends_at
		FROM tasks t
		JOIN modules m ON m.id = t.module_id AND m.board_id = t.board_id
		JOIN boards b ON b.id = t.board_id
		WHERE t.id = $3 AND t.module_id = $2 AND t.board_id = $1
	`, boardID, moduleID, taskID).Scan(
		&row.TaskID, &row.BoardID, &row.ModuleID, &row.TaskVisible, &row.TaskType,
		&row.SubmissionMode, &row.FilesPolicy, &row.ModuleType, &row.ModuleEnabled,
		&row.ModuleStartsAt, &row.ModuleEndsAt, &row.BoardStatus,
		&row.BoardStartsAt, &row.BoardEndsAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("files: load task scope: %w", err)
	}
	return &row, nil
}

type taskScopeRow struct {
	TaskID         uuid.UUID
	BoardID        uuid.UUID
	ModuleID       uuid.UUID
	TaskVisible    bool
	TaskType       string
	SubmissionMode string
	FilesPolicy    []byte
	ModuleType     string
	ModuleEnabled  bool
	ModuleStartsAt *time.Time
	ModuleEndsAt   *time.Time
	BoardStatus    string
	BoardStartsAt  *time.Time
	BoardEndsAt    *time.Time
}

func (s *store) pentestScope(ctx context.Context, boardID, moduleID uuid.UUID) (*pentestScopeRow, error) {
	var row pentestScopeRow
	err := s.db.Pool.QueryRow(ctx, `
		SELECT m.id, m.board_id, m.type, m.enabled, m.starts_at, m.ends_at,
		       b.status, b.starts_at, b.ends_at, pc.submission_policy
		FROM modules m
		JOIN boards b ON b.id = m.board_id
		LEFT JOIN pentest_configs pc ON pc.module_id = m.id
		WHERE m.id = $2 AND m.board_id = $1
	`, boardID, moduleID).Scan(
		&row.ModuleID, &row.BoardID, &row.ModuleType, &row.ModuleEnabled,
		&row.ModuleStartsAt, &row.ModuleEndsAt, &row.BoardStatus,
		&row.BoardStartsAt, &row.BoardEndsAt, &row.Policy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrModuleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("files: load pentest scope: %w", err)
	}
	return &row, nil
}

type pentestScopeRow struct {
	ModuleID       uuid.UUID
	BoardID        uuid.UUID
	ModuleType     string
	ModuleEnabled  bool
	ModuleStartsAt *time.Time
	ModuleEndsAt   *time.Time
	BoardStatus    string
	BoardStartsAt  *time.Time
	BoardEndsAt    *time.Time
	Policy         []byte
}

// participantMayAccessModule enforces live participation, module/board windows,
// module type availability and audience membership. Scoped staff bypass is
// handled by the caller only for management routes.
func (s *store) participantMayAccessModule(ctx context.Context, userID, boardID, moduleID uuid.UUID, wantType string) (bool, error) {
	var allowed bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM boards b
			JOIN modules m ON m.board_id = b.id
			JOIN board_participants bp ON bp.board_id = b.id AND bp.user_id = $1
			WHERE b.id = $2 AND m.id = $3
			  AND b.status NOT IN ('DRAFT','ARCHIVED','FINISHED')
			  AND (b.starts_at IS NULL OR b.starts_at <= clock_timestamp())
			  AND (b.ends_at IS NULL OR b.ends_at > clock_timestamp())
			  AND m.enabled
			  AND (m.starts_at IS NULL OR m.starts_at <= clock_timestamp())
			  AND (m.ends_at IS NULL OR m.ends_at > clock_timestamp())
			  AND ($4 = '' OR m.type = $4)
			  AND (
			    NOT EXISTS (SELECT 1 FROM module_audiences ma WHERE ma.module_id = m.id)
			    OR EXISTS (
			      SELECT 1
			      FROM module_audiences ma
			      JOIN audience_members am ON am.audience_id = ma.audience_id
			      WHERE ma.module_id = m.id
			        AND (
			          (am.subject_type = 'USER' AND am.subject_id = $1)
			          OR (am.subject_type = 'TEAM' AND am.subject_id IN (
			            SELECT tm.team_id FROM team_members tm
			            WHERE tm.board_id = $2 AND tm.user_id = $1
			          ))
			        )
			    )
			  )
		)
	`, userID, boardID, moduleID, wantType).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("files: check module access: %w", err)
	}
	return allowed, nil
}

func (s *store) isBoardParticipant(ctx context.Context, userID, boardID uuid.UUID) (bool, error) {
	var joined bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM board_participants WHERE board_id = $1 AND user_id = $2)
	`, boardID, userID).Scan(&joined)
	if err != nil {
		return false, fmt.Errorf("files: check participant: %w", err)
	}
	return joined, nil
}

// attachmentInScope proves the file, task, module and board are the exact
// stored immutable hierarchy, not merely a supplied file UUID.
func (s *store) attachmentInScope(ctx context.Context, fileID, boardID, moduleID uuid.UUID, taskID *uuid.UUID, resourceType string) (*FileRecord, error) {
	record, err := s.getFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if record.BoardID != boardID || record.ModuleID != moduleID {
		return nil, ErrFileContextMismatch
	}
	switch resourceType {
	case ResourceTask:
		if record.Purpose != PurposeTaskAttachment || record.TaskID == nil ||
			taskID == nil || *record.TaskID != *taskID {
			return nil, ErrFileContextMismatch
		}
	case ResourcePentest:
		if record.Purpose != PurposePentestAttachment || record.TaskID != nil || taskID != nil {
			return nil, ErrFileContextMismatch
		}
	default:
		return nil, ErrFileContextMismatch
	}
	return record, nil
}

// submissionFileInScope resolves the immutable submission -> file link.
func (s *store) submissionFileInScope(ctx context.Context, fileID, submissionID, boardID, moduleID uuid.UUID, resourceType string, taskID *uuid.UUID, actorID uuid.UUID, admin bool) (*FileRecord, error) {
	var (
		record      *FileRecord
		err         error
		subjectType string
		subjectID   uuid.UUID
		submittedBy uuid.UUID
		linked      bool
	)
	record, err = s.getFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if record.BoardID != boardID || record.ModuleID != moduleID {
		return nil, ErrFileContextMismatch
	}
	if record.Purpose != PurposeSubmissionDraft {
		return nil, ErrFileContextMismatch
	}

	if resourceType == ResourcePentest {
		err = s.db.Pool.QueryRow(ctx, `
			SELECT ps.subject_type, ps.subject_id, ps.id IS NOT NULL
			FROM pentest_submissions ps
			JOIN pentest_submission_files psf ON psf.submission_id = ps.id
			WHERE ps.id = $1 AND psf.file_id = $2
			  AND ps.board_id = $3 AND ps.module_id = $4
		`, submissionID, fileID, boardID, moduleID).Scan(&subjectType, &subjectID, &linked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubmissionNotFound
		}
	} else {
		err = s.db.Pool.QueryRow(ctx, `
			SELECT s.subject_type, s.subject_id, s.submitted_by_user_id, s.id IS NOT NULL
			FROM submissions s
			JOIN submission_files sf ON sf.submission_id = s.id
			WHERE s.id = $1 AND sf.file_id = $2
			  AND s.board_id = $3 AND s.module_id = $4
			  AND ($5::uuid IS NULL OR s.task_id = $5::uuid)
		`, submissionID, fileID, boardID, moduleID, nullableUUID(taskID)).Scan(&subjectType, &subjectID, &submittedBy, &linked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubmissionNotFound
		}
	}
	if err != nil {
		return nil, fmt.Errorf("files: load submission file: %w", err)
	}
	if record.Status != StatusAttached {
		return nil, ErrFileNotReady
	}
	if admin {
		return record, nil
	}
	if subjectType == "USER" && subjectID == actorID {
		return record, nil
	}
	if subjectType == "TEAM" {
		var member bool
		if err := s.db.Pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM team_members
				WHERE board_id = $1 AND team_id = $2 AND user_id = $3
			)
		`, boardID, subjectID, actorID).Scan(&member); err != nil {
			return nil, fmt.Errorf("files: check team membership: %w", err)
		}
		if member {
			return record, nil
		}
	}
	return nil, ErrForbidden
}

func (s *store) attachmentLinked(ctx context.Context, fileID uuid.UUID) (bool, error) {
	var linked bool
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM task_attachments WHERE file_id = $1)
	`, fileID).Scan(&linked); err != nil {
		return false, fmt.Errorf("files: check attachment link: %w", err)
	}
	return linked, nil
}

func (s *store) fileInUse(ctx context.Context, fileID uuid.UUID) (bool, error) {
	var inUse bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM submission_files WHERE file_id = $1)
		    OR EXISTS (SELECT 1 FROM pentest_submission_files WHERE file_id = $1)
		    OR EXISTS (SELECT 1 FROM task_attachments WHERE file_id = $1)
	`, fileID).Scan(&inUse)
	if err != nil {
		return false, fmt.Errorf("files: check file use: %w", err)
	}
	return inUse, nil
}

func (s *store) scanCandidate(ctx context.Context, fileID uuid.UUID) (*FileRecord, error) {
	return s.getFile(ctx, fileID)
}

func (s *store) expireDrafts(ctx context.Context, limit int) ([]Draft, []FileRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("files: begin maintenance: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, board_id, module_id, resource_type, task_id, status, created_at, expires_at
		FROM submission_drafts
		WHERE status = 'OPEN' AND expires_at <= clock_timestamp()
		ORDER BY expires_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("files: list expired drafts: %w", err)
	}
	drafts := make([]Draft, 0, limit)
	ids := make([]uuid.UUID, 0, limit)
	for rows.Next() {
		var (
			draft  Draft
			status string
			taskID pgtype.UUID
		)
		if err := rows.Scan(&draft.UUID, &draft.BoardID, &draft.ModuleID, &draft.ResourceType, &taskID, &status, &draft.CreatedAt, &draft.ExpiresAt); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("files: scan expired draft: %w", err)
		}
		draft.TaskID = uuidPtr(taskID)
		draft.Status = DraftStatus(status)
		draft.CreatedAt = draft.CreatedAt.UTC()
		draft.ExpiresAt = draft.ExpiresAt.UTC()
		drafts = append(drafts, draft)
		ids = append(ids, draft.UUID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("files: iterate expired drafts: %w", err)
	}
	rows.Close()
	if len(ids) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, fmt.Errorf("files: commit maintenance: %w", err)
		}
		return drafts, nil, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE submission_drafts SET status = 'EXPIRED' WHERE id = ANY($1::uuid[])
	`, ids); err != nil {
		return nil, nil, fmt.Errorf("files: expire drafts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE file_objects
		SET status = 'EXPIRED', rejection_code = 'DRAFT_EXPIRED'
		WHERE draft_id = ANY($1::uuid[]) AND status NOT IN ('ATTACHED')
	`, ids); err != nil {
		return nil, nil, fmt.Errorf("files: expire draft files: %w", err)
	}
	fileRows, err := tx.Query(ctx, `
		SELECT `+fileColumns+` FROM file_objects
		WHERE draft_id = ANY($1::uuid[]) AND status = 'EXPIRED'
	`, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("files: list expired files: %w", err)
	}
	expiredFiles := make([]FileRecord, 0, 8)
	for fileRows.Next() {
		record, err := scanFileRecord(fileRows)
		if err != nil {
			fileRows.Close()
			return nil, nil, err
		}
		expiredFiles = append(expiredFiles, *record)
	}
	if err := fileRows.Err(); err != nil {
		fileRows.Close()
		return nil, nil, fmt.Errorf("files: iterate expired files: %w", err)
	}
	fileRows.Close()
	if _, err := tx.Exec(ctx, `
		DELETE FROM file_objects
		WHERE draft_id = ANY($1::uuid[]) AND status = 'EXPIRED'
	`, ids); err != nil {
		return nil, nil, fmt.Errorf("files: remove expired draft files: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("files: commit maintenance: %w", err)
	}
	return drafts, expiredFiles, nil
}

func (s *store) loadReadyDraftFiles(ctx context.Context, actorID, draftID uuid.UUID) (*Draft, []FileRecord, error) {
	draft, err := s.getDraft(ctx, draftID, &actorID, false)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT `+fileColumns+` FROM file_objects
		WHERE draft_id = $1 ORDER BY created_at, id
	`, draftID)
	if err != nil {
		return nil, nil, fmt.Errorf("files: load draft files: %w", err)
	}
	defer rows.Close()
	files := make([]FileRecord, 0, len(draft.Files))
	for rows.Next() {
		record, err := scanFileRecord(rows)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, *record)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("files: iterate draft verification rows: %w", err)
	}
	return draft, files, nil
}

func (s *store) submissionResourceType(ctx context.Context, submissionID, boardID uuid.UUID) (string, error) {
	var pentest bool
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pentest_submissions WHERE id = $1 AND board_id = $2
		)
	`, submissionID, boardID).Scan(&pentest); err != nil {
		return "", fmt.Errorf("files: resolve submission type: %w", err)
	}
	if pentest {
		return ResourcePentest, nil
	}
	return ResourceTask, nil
}
