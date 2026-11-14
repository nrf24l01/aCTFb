package attackdefense

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// InsertRunParams is the server-derived run creation input. CommitSHA must
// already be normalized and the subject/actor already trusted.
type InsertRunParams struct {
	ModuleID               uuid.UUID
	RepositoryAssignmentID uuid.UUID
	SubjectType            string
	SubjectID              uuid.UUID
	Kind                   string
	CommitSHA              string
	ProviderDeliveryID     *string
	RequestedBy            uuid.UUID
}

// InsertRun creates one logical run or returns the existing run for the same
// (module, subject, kind, lower(commit_sha)). `created` is false on redelivery.
func (s *Store) InsertRun(ctx context.Context, q querier, params InsertRunParams) (*Run, bool, error) {
	if q == nil {
		return nil, false, ErrNoDatabase
	}
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO ad_runs (module_id, repository_assignment_id, subject_type, subject_id,
			kind, commit_sha, status, provider_delivery_id, requested_by)
		VALUES ($1,$2,$3,$4,$5,$6,'QUEUED',$7,$8)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, params.ModuleID, params.RepositoryAssignmentID, params.SubjectType, params.SubjectID,
		params.Kind, params.CommitSHA, params.ProviderDeliveryID, params.RequestedBy).Scan(&id)
	if err == nil {
		run, getErr := s.runByID(ctx, q, id, false)
		if getErr != nil {
			return nil, false, getErr
		}
		return run, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("attackdefense: insert run: %w", err)
	}
	run, findErr := s.findLogicalRun(ctx, q, params.ModuleID, params.SubjectType, params.SubjectID, params.Kind, params.CommitSHA)
	if findErr != nil {
		return nil, false, findErr
	}
	return run, false, nil
}

func (s *Store) findLogicalRun(ctx context.Context, q querier, moduleID uuid.UUID, subjectType string, subjectID uuid.UUID, kind, sha string) (*Run, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT id FROM ad_runs
		WHERE module_id=$1 AND subject_type=$2 AND subject_id=$3
		  AND kind=$4 AND lower(commit_sha)=lower($5)
	`, moduleID, subjectType, subjectID, kind, sha).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: find logical run: %w", err)
	}
	return s.runByID(ctx, q, id, false)
}

func (s *Store) runByID(ctx context.Context, q querier, runID uuid.UUID, forUpdate bool) (*Run, error) {
	query := `
		SELECT id, module_id, repository_assignment_id, subject_type, subject_id, kind,
		       lower(commit_sha), status, provider_delivery_id, requested_by,
		       created_at, started_at, finished_at, error
		FROM ad_runs WHERE id=$1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var run Run
	err := q.QueryRow(ctx, query, runID).Scan(
		&run.ID, &run.ModuleID, &run.RepositoryAssignmentID, &run.SubjectType, &run.SubjectID,
		&run.Kind, &run.CommitSHA, &run.Status, &run.ProviderDeliveryID, &run.RequestedBy,
		&run.CreatedAt, &run.StartedAt, &run.FinishedAt, &run.Error,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read run: %w", err)
	}
	return &run, nil
}

// RunByID is the participant/admin read including sanitized results.
func (s *Store) RunByID(ctx context.Context, runID uuid.UUID) (*Run, error) {
	run, err := s.runByID(ctx, s.db.Pool, runID, false)
	if err != nil {
		return nil, err
	}
	results, err := s.runResults(ctx, s.db.Pool, runID)
	if err != nil {
		return nil, err
	}
	run.Results = results
	return run, nil
}

func (s *Store) runResults(ctx context.Context, q querier, runID uuid.UUID) ([]RunResult, error) {
	rows, err := q.Query(ctx, `
		SELECT rr.run_id, rr.vulnerability_id, v.key, v.title, rr.status,
		       rr.safe_verdict, rr.score_awarded
		FROM ad_run_results rr
		JOIN ad_vulnerabilities v ON v.id = rr.vulnerability_id
		WHERE rr.run_id=$1
		ORDER BY v.key ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("attackdefense: list run results: %w", err)
	}
	defer rows.Close()
	results := make([]RunResult, 0)
	for rows.Next() {
		var result RunResult
		var verdict []byte
		if err := rows.Scan(&result.RunID, &result.VulnerabilityID, &result.Key, &result.Title,
			&result.Status, &verdict, &result.ScoreAwarded); err != nil {
			return nil, fmt.Errorf("attackdefense: scan run result: %w", err)
		}
		result.SafeVerdict = append(json.RawMessage(nil), verdict...)
		results = append(results, result)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("attackdefense: list run results: %w", rows.Err())
	}
	return results, nil
}

// ListRuns returns runs scoped to the module and optionally one subject.
func (s *Store) ListRuns(ctx context.Context, moduleID uuid.UUID, subject *Subject, filter RunFilter) ([]Run, int, error) {
	where := []string{"module_id=$1"}
	args := []any{moduleID}
	if subject != nil {
		args = append(args, subject.Type, subject.ID)
		where = append(where, fmt.Sprintf("subject_type=$%d AND subject_id=$%d", len(args)-1, len(args)))
	}
	if filter.Kind != "" {
		args = append(args, filter.Kind)
		where = append(where, fmt.Sprintf("kind=$%d", len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = append(where, fmt.Sprintf("status=$%d", len(args)))
	}
	base := " FROM ad_runs WHERE " + joinWithAnd(where)
	var total int
	if err := s.db.Pool.QueryRow(ctx, "SELECT count(*)"+base, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("attackdefense: count runs: %w", err)
	}
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, module_id, repository_assignment_id, subject_type, subject_id, kind,
		       lower(commit_sha), status, provider_delivery_id, requested_by,
		       created_at, started_at, finished_at, error`+base+fmt.Sprintf(`
		ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("attackdefense: list runs: %w", err)
	}
	defer rows.Close()
	items := make([]Run, 0)
	for rows.Next() {
		var run Run
		if err := rows.Scan(&run.ID, &run.ModuleID, &run.RepositoryAssignmentID, &run.SubjectType,
			&run.SubjectID, &run.Kind, &run.CommitSHA, &run.Status, &run.ProviderDeliveryID,
			&run.RequestedBy, &run.CreatedAt, &run.StartedAt, &run.FinishedAt, &run.Error); err != nil {
			return nil, 0, fmt.Errorf("attackdefense: scan run: %w", err)
		}
		items = append(items, run)
	}
	if rows.Err() != nil {
		return nil, 0, fmt.Errorf("attackdefense: list runs: %w", rows.Err())
	}
	return items, total, nil
}

// Logs returns sanitized run logs newest bounded by limit.
func (s *Store) Logs(ctx context.Context, runID uuid.UUID, limit int) ([]RunLog, bool, error) {
	if limit <= 0 || limit > MaxCheckerLogLines {
		limit = MaxCheckerLogLines
	}
	var total int
	if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM ad_run_logs WHERE run_id=$1`, runID).Scan(&total); err != nil {
		return nil, false, fmt.Errorf("attackdefense: count run logs: %w", err)
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT seq, line, created_at FROM ad_run_logs
		WHERE run_id=$1 ORDER BY seq ASC LIMIT $2
	`, runID, limit)
	if err != nil {
		return nil, false, fmt.Errorf("attackdefense: read run logs: %w", err)
	}
	defer rows.Close()
	logs := make([]RunLog, 0)
	for rows.Next() {
		var entry RunLog
		if err := rows.Scan(&entry.Seq, &entry.Line, &entry.CreatedAt); err != nil {
			return nil, false, fmt.Errorf("attackdefense: scan run log: %w", err)
		}
		logs = append(logs, entry)
	}
	if rows.Err() != nil {
		return nil, false, fmt.Errorf("attackdefense: read run logs: %w", rows.Err())
	}
	return logs, total > limit, nil
}

// AppendLogs inserts sanitized, bounded lines with monotonic sequence numbers.
func (s *Store) AppendLogs(ctx context.Context, runID uuid.UUID, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	if len(lines) > MaxCheckerLogLines {
		lines = lines[:MaxCheckerLogLines]
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var start int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(seq)+1, 0) FROM ad_run_logs WHERE run_id=$1`, runID).Scan(&start); err != nil {
		return fmt.Errorf("attackdefense: next log sequence: %w", err)
	}
	for index, line := range lines {
		if len(line) > MaxCheckerLogLine {
			line = trimTo(line, MaxCheckerLogLine)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ad_run_logs (run_id, seq, line) VALUES ($1,$2,$3)
			ON CONFLICT (run_id, seq) DO NOTHING
		`, runID, start+index, line); err != nil {
			return fmt.Errorf("attackdefense: append run log: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("attackdefense: commit run logs: %w", err)
	}
	return nil
}

// ClaimRun atomically moves QUEUED -> RUNNING. A redelivered job returns false
// without executing anything.
func (s *Store) ClaimRun(ctx context.Context, runID uuid.UUID) (bool, error) {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE ad_runs SET status='RUNNING', started_at=NOW(), error=NULL
		WHERE id=$1 AND status='QUEUED'
	`, runID)
	if err != nil {
		return false, fmt.Errorf("attackdefense: claim run: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RequeueRun returns an interrupted RUNNING run to QUEUED without producing a
// failure verdict. It is used only for trusted worker shutdown/context
// cancellation, never for a checker outcome.
func (s *Store) RequeueRun(ctx context.Context, runID uuid.UUID) error {
	if _, err := s.db.Pool.Exec(ctx, `
		UPDATE ad_runs SET status='QUEUED', started_at=NULL, finished_at=NULL, error=NULL
		WHERE id=$1 AND status='RUNNING'
	`, runID); err != nil {
		return fmt.Errorf("attackdefense: requeue run: %w", err)
	}
	return nil
}

// MarkRunError records a safe technical failure.
func (s *Store) MarkRunError(ctx context.Context, runID uuid.UUID, code string) (*Run, error) {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE ad_runs SET status='ERROR', finished_at=NOW(), error=$2
		WHERE id=$1 AND status='RUNNING'
	`, runID, trimTo(code, 512))
	if err != nil {
		return nil, fmt.Errorf("attackdefense: mark run error: %w", err)
	}
	return s.RunByID(ctx, runID)
}

// CancelRun cancels a queued/running run.
func (s *Store) CancelRun(ctx context.Context, moduleID, runID, actor uuid.UUID, audit AuditEvent) (*Run, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := s.runByID(ctx, tx, runID, true)
	if err != nil {
		return nil, err
	}
	if run.ModuleID != moduleID {
		return nil, ErrRunNotFound
	}
	if run.Status != RunStatusQueued && run.Status != RunStatusRunning {
		return nil, ErrRunActionNotAllowed
	}
	if _, err := tx.Exec(ctx, `UPDATE ad_runs SET status='CANCELED', finished_at=NOW() WHERE id=$1`, runID); err != nil {
		return nil, fmt.Errorf("attackdefense: cancel run: %w", err)
	}
	audit.ResourceID = &runID
	if err := writeAudit(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("attackdefense: commit cancel: %w", err)
	}
	return s.RunByID(ctx, runID)
}

// ResetRunForRetry requeues a terminal failed/error/canceled run. Previous
// results are removed; awards are never removed because they are idempotent
// score evidence.
func (s *Store) ResetRunForRetry(ctx context.Context, moduleID, runID, actor uuid.UUID, audit AuditEvent) (*Run, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := s.runByID(ctx, tx, runID, true)
	if err != nil {
		return nil, err
	}
	if run.ModuleID != moduleID {
		return nil, ErrRunNotFound
	}
	switch run.Status {
	case RunStatusFailed, RunStatusError, RunStatusCanceled:
	default:
		return nil, ErrRunActionNotAllowed
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ad_run_results WHERE run_id=$1`, runID); err != nil {
		return nil, fmt.Errorf("attackdefense: clear run results: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ad_runs SET status='QUEUED', started_at=NULL, finished_at=NULL, error=NULL
		WHERE id=$1
	`, runID); err != nil {
		return nil, fmt.Errorf("attackdefense: reset run: %w", err)
	}
	audit.ResourceID = &runID
	if err := writeAudit(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("attackdefense: commit retry: %w", err)
	}
	return s.RunByID(ctx, runID)
}

// CountActiveRuns enforces max_concurrent_runs_per_subject.
func (s *Store) CountActiveRuns(ctx context.Context, q querier, moduleID uuid.UUID, subject Subject) (int, error) {
	var count int
	err := q.QueryRow(ctx, `
		SELECT count(*) FROM ad_runs
		WHERE module_id=$1 AND subject_type=$2 AND subject_id=$3
		  AND status IN ('QUEUED','RUNNING')
	`, moduleID, subject.Type, subject.ID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("attackdefense: count active runs: %w", err)
	}
	return count, nil
}

// QueuedRuns returns reconciliation candidates. Jobs are always re-read from
// the database; NATS messages are only wake-ups.
func (s *Store) QueuedRuns(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id FROM ad_runs
		WHERE status='QUEUED'
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("attackdefense: list queued runs: %w", err)
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("attackdefense: scan queued run: %w", err)
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("attackdefense: list queued runs: %w", rows.Err())
	}
	return ids, nil
}

// Execution is the worker's trusted snapshot. Credentials are deliberately not
// part of this record; the worker resolves them only when it is about to fetch.
type Execution struct {
	Run             Run
	Module          ModuleContext
	Assignment      Assignment
	Config          Config
	SourceBinding   *BindingCiphertext
	ExploitBinding  *BindingCiphertext
	Vulnerabilities []Vulnerability
}

// ExecutionByRunID loads everything needed to validate and execute one run.
func (s *Store) ExecutionByRunID(ctx context.Context, runID uuid.UUID) (*Execution, error) {
	run, err := s.runByID(ctx, s.db.Pool, runID, false)
	if err != nil {
		return nil, err
	}
	var boardID uuid.UUID
	if err := s.db.Pool.QueryRow(ctx, `SELECT board_id FROM modules WHERE id=$1`, run.ModuleID).Scan(&boardID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrModuleNotFound
		}
		return nil, fmt.Errorf("attackdefense: read run board: %w", err)
	}
	module, err := s.moduleContext(ctx, s.db.Pool, boardID, run.ModuleID, false)
	if err != nil {
		return nil, err
	}
	assignment, err := s.assignmentByID(ctx, s.db.Pool, run.ModuleID, run.RepositoryAssignmentID, false)
	if err != nil {
		return nil, err
	}
	config, err := s.Config(ctx, run.ModuleID)
	if err != nil {
		return nil, err
	}
	bindings, err := s.BindingsByIDs(ctx, assignment.SourceBindingID, assignment.ExploitBindingID)
	if err != nil {
		return nil, err
	}
	vulnerabilities, _, err := s.ListVulnerabilities(ctx, run.ModuleID, MaxVulnerabilities, 0)
	if err != nil {
		return nil, err
	}
	return &Execution{
		Run: *run, Module: *module, Assignment: *assignment, Config: *config,
		SourceBinding: bindings["source"], ExploitBinding: bindings["exploit"],
		Vulnerabilities: vulnerabilities,
	}, nil
}

// IdempotencyLookup finds a prior audit record for the same principal, module,
// action and key. The audit table is the durable key ledger so no extra table or
// column is introduced.
func (s *Store) IdempotencyLookup(ctx context.Context, q querier, actor, moduleID uuid.UUID, action, key string) (map[string]any, bool, error) {
	if q == nil {
		return nil, false, ErrNoDatabase
	}
	var raw []byte
	err := q.QueryRow(ctx, `
		SELECT details FROM audit_events
		WHERE actor_user_id=$1 AND module_id=$2 AND action=$3
		  AND details->>'idempotencyKey'=$4
		ORDER BY created_at DESC
		LIMIT 1
	`, actor, moduleID, action, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("attackdefense: read idempotency record: %w", err)
	}
	details := map[string]any{}
	if err := json.Unmarshal(raw, &details); err != nil {
		return nil, false, fmt.Errorf("attackdefense: decode idempotency record: %w", err)
	}
	return details, true, nil
}

// LatestPatchValidation returns whether the newest PATCH run result for a
// vulnerability is valid and when the patch award happened.
func (s *Store) LatestPatchValidation(ctx context.Context, moduleID uuid.UUID, subject Subject, vulnerabilityID uuid.UUID) (bool, *time.Time, error) {
	var valid bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT rr.status='PASSED'
		FROM ad_run_results rr
		JOIN ad_runs r ON r.id=rr.run_id
		WHERE r.module_id=$1 AND r.subject_type=$2 AND r.subject_id=$3
		  AND r.kind='PATCH' AND rr.vulnerability_id=$4
		ORDER BY r.finished_at DESC NULLS LAST, r.created_at DESC
		LIMIT 1
	`, moduleID, subject.Type, subject.ID, vulnerabilityID).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("attackdefense: read latest patch validation: %w", err)
	}
	return valid, nil, nil
}

func joinWithAnd(parts []string) string {
	if len(parts) == 0 {
		return "TRUE"
	}
	result := parts[0]
	for _, part := range parts[1:] {
		result += " AND " + part
	}
	return result
}
