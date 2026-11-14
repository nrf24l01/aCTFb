package attackdefense

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/events"
)

// VerdictResult is one server-validated vulnerability result. Points come from
// ad_vulnerabilities, never from checker JSON.
type VerdictResult struct {
	VulnerabilityID uuid.UUID
	Key             string
	Status          string
	SafeVerdict     json.RawMessage
	AwardPoints     int
	Award           bool
}

// VerdictInput is the sanitized checker outcome for one run.
type VerdictInput struct {
	RunID   uuid.UUID
	Status  string // PASSED or FAILED
	Results []VerdictResult
	Logs    []string
	Audit   AuditEvent
	Event   events.Event
}

// ApplyVerdict writes results, idempotent awards, logs, audit and the outbox row
// in one transaction. A double commit is impossible: the run row lock plus the
// unique award key make replay a no-op.
func (s *Store) ApplyVerdict(ctx context.Context, in VerdictInput) (*Run, bool, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := s.runByID(ctx, tx, in.RunID, true)
	if err != nil {
		return nil, false, err
	}
	if err := appendLogsTx(ctx, tx, in.RunID, in.Logs); err != nil {
		return nil, false, err
	}
	if run.Status != RunStatusRunning {
		// Redelivery after completion or cancellation is a harmless no-op.
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("attackdefense: commit verdict no-op: %w", err)
		}
		return run, false, nil
	}
	if in.Status != RunStatusPassed && in.Status != RunStatusFailed {
		return nil, false, ErrInvalidVerdict
	}
	totalAwarded := 0
	for _, result := range in.Results {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ad_run_results (run_id, vulnerability_id, status, safe_verdict, score_awarded)
			VALUES ($1,$2,$3,$4,NULL)
			ON CONFLICT (run_id, vulnerability_id) DO UPDATE SET
				status=EXCLUDED.status, safe_verdict=EXCLUDED.safe_verdict
		`, in.RunID, result.VulnerabilityID, result.Status, []byte(result.SafeVerdict)); err != nil {
			return nil, false, fmt.Errorf("attackdefense: insert run result: %w", err)
		}
		if !result.Award || result.Status != ResultStatusPassed || result.AwardPoints <= 0 {
			continue
		}
		var points int
		err := tx.QueryRow(ctx, `
			INSERT INTO ad_awards (module_id, subject_type, subject_id, vulnerability_id, kind, run_id, points)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (subject_type, subject_id, vulnerability_id, kind) DO NOTHING
			RETURNING points
		`, run.ModuleID, run.SubjectType, run.SubjectID, result.VulnerabilityID, run.Kind, run.ID, result.AwardPoints).Scan(&points)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("attackdefense: insert award: %w", err)
		}
		totalAwarded += points
		if _, err := tx.Exec(ctx, `
			UPDATE ad_run_results SET score_awarded=$3
			WHERE run_id=$1 AND vulnerability_id=$2
		`, in.RunID, result.VulnerabilityID, points); err != nil {
			return nil, false, fmt.Errorf("attackdefense: update run result score: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ad_runs SET status=$2, finished_at=NOW(), error=NULL WHERE id=$1
	`, in.RunID, in.Status); err != nil {
		return nil, false, fmt.Errorf("attackdefense: finish run: %w", err)
	}
	if in.Audit.Action == "" {
		in.Audit.Action = "ad.run.completed"
	}
	in.Audit.ModuleID = &run.ModuleID
	in.Audit.ResourceID = &run.ID
	if in.Audit.Details == nil {
		in.Audit.Details = map[string]any{}
	}
	in.Audit.Details["status"] = in.Status
	in.Audit.Details["pointsAwarded"] = totalAwarded
	if err := writeAudit(ctx, tx, in.Audit); err != nil {
		return nil, false, err
	}
	var boardID uuid.UUID
	if in.Event.BoardID != nil {
		boardID = *in.Event.BoardID
	} else if in.Audit.BoardID != nil {
		boardID = *in.Audit.BoardID
	} else {
		if err := tx.QueryRow(ctx, `SELECT board_id FROM modules WHERE id=$1`, run.ModuleID).Scan(&boardID); err != nil {
			return nil, false, fmt.Errorf("attackdefense: resolve event board: %w", err)
		}
	}
	if len(in.Event.Payload) == 0 {
		payload, marshalErr := json.Marshal(struct {
			RunUUID       uuid.UUID `json:"runUuid"`
			BoardUUID     uuid.UUID `json:"boardUuid"`
			ModuleUUID    uuid.UUID `json:"moduleUuid"`
			SubjectType   string    `json:"subjectType"`
			SubjectUUID   uuid.UUID `json:"subjectUuid"`
			Kind          string    `json:"kind"`
			Status        string    `json:"status"`
			CommitSHA     string    `json:"commitSha"`
			PointsAwarded int       `json:"pointsAwarded"`
			OccurredAt    time.Time `json:"occurredAt"`
		}{
			RunUUID: run.ID, BoardUUID: boardID, ModuleUUID: run.ModuleID,
			SubjectType: run.SubjectType, SubjectUUID: run.SubjectID, Kind: run.Kind,
			Status: in.Status, CommitSHA: run.CommitSHA, PointsAwarded: totalAwarded,
			OccurredAt: time.Now().UTC(),
		})
		if marshalErr != nil {
			return nil, false, fmt.Errorf("attackdefense: encode run event: %w", marshalErr)
		}
		in.Event.Payload = payload
	}
	if in.Event.ID == uuid.Nil {
		in.Event.ID = uuid.New()
	}
	if in.Event.Type == "" {
		in.Event.Type = EventADRunCompleted
	}
	if in.Event.OccurredAt.IsZero() {
		in.Event.OccurredAt = time.Now().UTC()
	}
	in.Event.ModuleID = &run.ModuleID
	in.Event.SubjectType = run.SubjectType
	in.Event.SubjectID = &run.SubjectID
	in.Event.BoardID = &boardID
	outbox := events.NewOutbox(s.db, events.WithRegistry(EventRegistry()))
	if err := outbox.Enqueue(ctx, tx, in.Event); err != nil {
		return nil, false, fmt.Errorf("attackdefense: enqueue run event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("attackdefense: commit verdict: %w", err)
	}
	finished, err := s.RunByID(ctx, in.RunID)
	if err != nil {
		return nil, false, err
	}
	return finished, true, nil
}

// FailRun records a technical failure and its sanitized logs atomically. A
// canceled run is not overwritten.
func (s *Store) FailRun(ctx context.Context, runID uuid.UUID, code string, logs []string, audit AuditEvent) (*Run, bool, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := s.runByID(ctx, tx, runID, true)
	if err != nil {
		return nil, false, err
	}
	if err := appendLogsTx(ctx, tx, runID, logs); err != nil {
		return nil, false, err
	}
	if run.Status != RunStatusRunning {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("attackdefense: commit failure no-op: %w", err)
		}
		return run, false, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ad_runs SET status='ERROR', finished_at=NOW(), error=$2 WHERE id=$1
	`, runID, trimTo(code, 512)); err != nil {
		return nil, false, fmt.Errorf("attackdefense: fail run: %w", err)
	}
	if audit.Action == "" {
		audit.Action = "ad.run.failed"
	}
	audit.ModuleID = &run.ModuleID
	audit.ResourceID = &run.ID
	if audit.Details == nil {
		audit.Details = map[string]any{}
	}
	audit.Details["error"] = trimTo(code, 128)
	if err := writeAudit(ctx, tx, audit); err != nil {
		return nil, false, err
	}
	var boardID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT board_id FROM modules WHERE id=$1`, run.ModuleID).Scan(&boardID); err != nil {
		return nil, false, fmt.Errorf("attackdefense: resolve failure event board: %w", err)
	}
	payload, err := json.Marshal(struct {
		RunUUID     uuid.UUID `json:"runUuid"`
		BoardUUID   uuid.UUID `json:"boardUuid"`
		ModuleUUID  uuid.UUID `json:"moduleUuid"`
		SubjectType string    `json:"subjectType"`
		SubjectUUID uuid.UUID `json:"subjectUuid"`
		Kind        string    `json:"kind"`
		Status      string    `json:"status"`
		CommitSHA   string    `json:"commitSha"`
		OccurredAt  time.Time `json:"occurredAt"`
	}{
		RunUUID: run.ID, BoardUUID: boardID, ModuleUUID: run.ModuleID,
		SubjectType: run.SubjectType, SubjectUUID: run.SubjectID, Kind: run.Kind,
		Status: RunStatusError, CommitSHA: run.CommitSHA, OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		return nil, false, fmt.Errorf("attackdefense: encode failure event: %w", err)
	}
	outbox := events.NewOutbox(s.db, events.WithRegistry(EventRegistry()))
	if err := outbox.Enqueue(ctx, tx, events.Event{
		ID: uuid.New(), Type: EventADRunCompleted, OccurredAt: time.Now().UTC(),
		BoardID: &boardID, ModuleID: &run.ModuleID, SubjectType: run.SubjectType,
		SubjectID: &run.SubjectID, Payload: payload,
	}); err != nil {
		return nil, false, fmt.Errorf("attackdefense: enqueue failure event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("attackdefense: commit failure: %w", err)
	}
	finished, err := s.RunByID(ctx, runID)
	if err != nil {
		return nil, false, err
	}
	return finished, true, nil
}

func appendLogsTx(ctx context.Context, tx pgx.Tx, runID uuid.UUID, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	if len(lines) > MaxCheckerLogLines {
		lines = lines[:MaxCheckerLogLines]
	}
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
	return nil
}

// Progress computes the participant view from awards and the latest patch
// validation. Hidden vulnerabilities are only counted.
func (s *Store) Progress(ctx context.Context, moduleID uuid.UUID, subject Subject) (*Progress, error) {
	progress := &Progress{Vulnerabilities: make([]VulnerabilityProgress, 0)}
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM ad_vulnerabilities WHERE module_id=$1
	`, moduleID).Scan(&progress.TotalVulnerabilities); err != nil {
		return nil, fmt.Errorf("attackdefense: count vulnerabilities: %w", err)
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT v.id, v.key, v.title, fe.awarded_at, fp.awarded_at,
		       (SELECT rr.status='PASSED'
		        FROM ad_run_results rr
		        JOIN ad_runs r ON r.id=rr.run_id
		        WHERE r.module_id=v.module_id AND r.subject_type=$2 AND r.subject_id=$3
		          AND r.kind='PATCH' AND rr.vulnerability_id=v.id
		        ORDER BY r.finished_at DESC NULLS LAST, r.created_at DESC
		        LIMIT 1) AS current_patch_valid
		FROM ad_vulnerabilities v
		JOIN ad_awards fe ON fe.vulnerability_id=v.id AND fe.kind='EXPLOIT'
			AND fe.subject_type=$2 AND fe.subject_id=$3
		LEFT JOIN ad_awards fp ON fp.vulnerability_id=v.id AND fp.kind='PATCH'
			AND fp.subject_type=$2 AND fp.subject_id=$3
		WHERE v.module_id=$1
		ORDER BY fe.awarded_at ASC
	`, moduleID, subject.Type, subject.ID)
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read progress: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item VulnerabilityProgress
		var foundAt time.Time
		var patchedAt *time.Time
		var currentValid *bool
		if err := rows.Scan(&item.ID, &item.Key, &item.Title, &foundAt, &patchedAt, &currentValid); err != nil {
			return nil, fmt.Errorf("attackdefense: scan progress: %w", err)
		}
		item.FoundAt = &foundAt
		item.PatchedAt = patchedAt
		item.CurrentPatchValid = currentValid
		if patchedAt != nil {
			item.Status = ProgressPatched
		} else {
			item.Status = ProgressFound
		}
		progress.Vulnerabilities = append(progress.Vulnerabilities, item)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("attackdefense: read progress: %w", rows.Err())
	}
	progress.FoundCount = len(progress.Vulnerabilities)
	for _, item := range progress.Vulnerabilities {
		if item.Status == ProgressPatched {
			progress.PatchedCount++
		}
	}
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT COALESCE(sum(points),0) FROM ad_awards
		WHERE module_id=$1 AND subject_type=$2 AND subject_id=$3
	`, moduleID, subject.Type, subject.ID).Scan(&progress.Score); err != nil {
		return nil, fmt.Errorf("attackdefense: sum progress score: %w", err)
	}
	return progress, nil
}

// SubjectTotal returns the AD award total for one subject across a board.
func (s *Store) SubjectTotal(ctx context.Context, boardID uuid.UUID, subject Subject) (int, error) {
	var total int
	err := s.db.Pool.QueryRow(ctx, `
		SELECT COALESCE(sum(a.points),0)
		FROM ad_awards a
		JOIN modules m ON m.id=a.module_id
		WHERE m.board_id=$1 AND a.subject_type=$2 AND a.subject_id=$3
	`, boardID, subject.Type, subject.ID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("attackdefense: sum subject score: %w", err)
	}
	return total, nil
}

// BoardScores returns per-subject award totals for the read-only ScoreSource
// adapter. Names are resolved from users/teams for display.
func (s *Store) BoardScores(ctx context.Context, boardID uuid.UUID) ([]SubjectScore, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT a.subject_type, a.subject_id, COALESCE(u.username, t.name, ''),
		       sum(a.points)::int
		FROM ad_awards a
		JOIN modules m ON m.id=a.module_id
		LEFT JOIN users u ON a.subject_type='USER' AND u.id=a.subject_id
		LEFT JOIN teams t ON a.subject_type='TEAM' AND t.id=a.subject_id
		WHERE m.board_id=$1
		GROUP BY a.subject_type, a.subject_id, u.username, t.name
		ORDER BY a.subject_type, a.subject_id
	`, boardID)
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read board scores: %w", err)
	}
	defer rows.Close()
	scores := make([]SubjectScore, 0)
	for rows.Next() {
		var score SubjectScore
		if err := rows.Scan(&score.SubjectType, &score.SubjectID, &score.Name, &score.Points); err != nil {
			return nil, fmt.Errorf("attackdefense: scan board score: %w", err)
		}
		scores = append(scores, score)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("attackdefense: read board scores: %w", rows.Err())
	}
	return scores, nil
}

func (a AuditEvent) boardIDValue() uuid.UUID {
	if a.BoardID == nil {
		return uuid.Nil
	}
	return *a.BoardID
}

// EventADRunCompleted is the typed AD run event. It carries counts/status only,
// never selectors, logs, repository credentials or raw checker output.
const EventADRunCompleted = "ad.run.completed"

// EventRegistry registers the AD event types on a copy of the core registry so
// the events package is not edited by this lane.
func EventRegistry() *events.Registry {
	registry := events.CoreRegistry()
	_ = registry.RegisterCore(EventADRunCompleted)
	return registry
}
