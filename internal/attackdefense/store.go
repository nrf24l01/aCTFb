package attackdefense

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store is the PostgreSQL persistence surface of the AD feature.
type Store struct {
	db *postgres.DB
}

// NewStore constructs the AD store.
func NewStore(db *postgres.DB) (*Store, error) {
	if db == nil || db.Pool == nil {
		return nil, ErrNoDatabase
	}
	return &Store{db: db}, nil
}

func (s *Store) begin(ctx context.Context) (pgx.Tx, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, ErrNoDatabase
	}
	return s.db.Pool.Begin(ctx)
}

// moduleContext loads the board/module window state used for authorization.
// The same query is used by participant and worker paths so no stale client
// state can approve a run.
func (s *Store) moduleContext(ctx context.Context, q querier, boardID, moduleID uuid.UUID, forUpdate bool) (*ModuleContext, error) {
	if q == nil {
		return nil, ErrNoDatabase
	}
	query := `
		SELECT b.id, m.id, b.participation_mode, b.status, b.starts_at, b.ends_at,
		       m.type, m.enabled, m.starts_at, m.ends_at, m.allowed_task_types
		FROM modules m
		JOIN boards b ON b.id = m.board_id
		WHERE b.id = $1 AND m.id = $2`
	if forUpdate {
		query += ` FOR UPDATE OF m, b`
	}
	var record ModuleContext
	err := q.QueryRow(ctx, query, boardID, moduleID).Scan(
		&record.BoardID, &record.ModuleID, &record.ParticipationMode, &record.BoardStatus,
		&record.BoardStartsAt, &record.BoardEndsAt, &record.ModuleType, &record.ModuleEnabled,
		&record.ModuleStartsAt, &record.ModuleEndsAt, &record.AllowedTaskTypes,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if existsErr := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM boards WHERE id=$1)`, boardID).Scan(&exists); existsErr != nil {
			return nil, fmt.Errorf("attackdefense: read board existence: %w", existsErr)
		}
		if !exists {
			return nil, ErrBoardNotFound
		}
		return nil, ErrModuleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read module context: %w", err)
	}
	if record.ModuleType != ModuleTypeAttackDefense {
		return nil, ErrModuleTypeMismatch
	}
	return &record, nil
}

// ModuleContext exposes the trusted module snapshot to worker callers.
func (s *Store) ModuleContext(ctx context.Context, boardID, moduleID uuid.UUID) (*ModuleContext, error) {
	return s.moduleContext(ctx, s.db.Pool, boardID, moduleID, false)
}

// resolveSubject derives the scoring subject and trusted actor user for a
// participant. It rejects users who are not board participants and TEAM users
// without a team.
func (s *Store) resolveSubject(ctx context.Context, q querier, boardID, userID uuid.UUID) (*TrustedActor, error) {
	var participationMode string
	var participant bool
	err := q.QueryRow(ctx, `
		SELECT b.participation_mode,
		       EXISTS (SELECT 1 FROM board_participants p WHERE p.board_id=b.id AND p.user_id=$2)
		FROM boards b
		WHERE b.id=$1
	`, boardID, userID).Scan(&participationMode, &participant)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBoardNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: resolve board participation: %w", err)
	}
	if !participant {
		return nil, ErrBoardAccessDenied
	}
	switch participationMode {
	case "INDIVIDUAL":
		return &TrustedActor{Subject: Subject{Type: SubjectTypeUser, ID: userID}, RequestedBy: userID}, nil
	case "TEAM":
		var teamID, captainID uuid.UUID
		err := q.QueryRow(ctx, `
			SELECT t.id, t.captain_user_id
			FROM team_members tm
			JOIN teams t ON t.board_id=tm.board_id AND t.id=tm.team_id
			WHERE tm.board_id=$1 AND tm.user_id=$2
		`, boardID, userID).Scan(&teamID, &captainID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBoardAccessDenied
		}
		if err != nil {
			return nil, fmt.Errorf("attackdefense: resolve team subject: %w", err)
		}
		return &TrustedActor{Subject: Subject{Type: SubjectTypeTeam, ID: teamID}, RequestedBy: captainID}, nil
	default:
		return nil, ErrBoardAccessDenied
	}
}

// subjectFromExplicit is used by the admin assignment path. The subject must
// match the board participation mode and exist.
func (s *Store) subjectFromExplicit(ctx context.Context, q querier, moduleContext *ModuleContext, subjectType string, subjectID uuid.UUID) (*TrustedActor, error) {
	if subjectID == uuid.Nil {
		return nil, Invalid("subjectUuid", "must be a participant subject", "INVALID_VALUE")
	}
	switch subjectType {
	case SubjectTypeUser:
		if moduleContext.ParticipationMode != "INDIVIDUAL" {
			return nil, Invalid("subjectType", "must match board participation mode", "INVALID_VALUE")
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM board_participants WHERE board_id=$1 AND user_id=$2)`, moduleContext.BoardID, subjectID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("attackdefense: check participant: %w", err)
		}
		if !exists {
			return nil, Invalid("subjectUuid", "subject must be a board participant", "INVALID_VALUE")
		}
		return &TrustedActor{Subject: Subject{Type: SubjectTypeUser, ID: subjectID}, RequestedBy: subjectID}, nil
	case SubjectTypeTeam:
		if moduleContext.ParticipationMode != "TEAM" {
			return nil, Invalid("subjectType", "must match board participation mode", "INVALID_VALUE")
		}
		var captainID uuid.UUID
		err := q.QueryRow(ctx, `SELECT captain_user_id FROM teams WHERE board_id=$1 AND id=$2`, moduleContext.BoardID, subjectID).Scan(&captainID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, Invalid("subjectUuid", "subject team must exist on the board", "INVALID_VALUE")
		}
		if err != nil {
			return nil, fmt.Errorf("attackdefense: check team subject: %w", err)
		}
		return &TrustedActor{Subject: Subject{Type: SubjectTypeTeam, ID: subjectID}, RequestedBy: captainID}, nil
	default:
		return nil, Invalid("subjectType", "must be USER or TEAM", "INVALID_VALUE")
	}
}

// audienceAllows applies the module_audiences gate exactly like boards.
func (s *Store) audienceAllows(ctx context.Context, q querier, moduleID uuid.UUID, subject Subject) (bool, error) {
	var gated bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM module_audiences WHERE module_id=$1)`, moduleID).Scan(&gated); err != nil {
		return false, fmt.Errorf("attackdefense: read module audiences: %w", err)
	}
	if !gated {
		return true, nil
	}
	var allowed bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM module_audiences ma
			JOIN audience_members am ON am.audience_id=ma.audience_id
			WHERE ma.module_id=$1 AND am.subject_type=$2 AND am.subject_id=$3
		)
	`, moduleID, subject.Type, subject.ID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("attackdefense: read audience membership: %w", err)
	}
	return allowed, nil
}

// participantContext resolves the actor, subject, module and audience gate in
// one trusted call.
func (s *Store) participantContext(ctx context.Context, boardID, moduleID, userID uuid.UUID) (*ModuleContext, *TrustedActor, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	module, err := s.moduleContext(ctx, tx, boardID, moduleID, false)
	if err != nil {
		return nil, nil, err
	}
	actor, err := s.resolveSubject(ctx, tx, boardID, userID)
	if err != nil {
		return nil, nil, err
	}
	allowed, err := s.audienceAllows(ctx, tx, moduleID, actor.Subject)
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, ErrModuleNotAvailable
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("attackdefense: commit participant context: %w", err)
	}
	return module, actor, nil
}

// Config returns the module configuration, or ErrConfigNotFound.
func (s *Store) Config(ctx context.Context, moduleID uuid.UUID) (*Config, error) {
	record := &Config{ModuleID: moduleID}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT default_source_branch, default_exploit_branch, runner_image,
		       run_timeout_seconds, max_concurrent_runs_per_subject, manual_rerun_enabled,
		       created_by, updated_at
		FROM ad_configs WHERE module_id=$1
	`, moduleID).Scan(
		&record.DefaultSourceBranch, &record.DefaultExploitBranch, &record.RunnerImage,
		&record.RunTimeoutSeconds, &record.MaxConcurrentRunsPerSubject, &record.ManualRerunEnabled,
		&record.CreatedBy, &record.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConfigNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read ad config: %w", err)
	}
	return record, nil
}

// UpsertConfig writes the admin config and audit row in one transaction.
func (s *Store) UpsertConfig(ctx context.Context, actor uuid.UUID, record *Config, audit AuditEvent) (*Config, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var out Config
	err = tx.QueryRow(ctx, `
		INSERT INTO ad_configs (module_id, default_source_branch, default_exploit_branch,
			runner_image, run_timeout_seconds, max_concurrent_runs_per_subject,
			manual_rerun_enabled, created_by, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (module_id) DO UPDATE SET
			default_source_branch=EXCLUDED.default_source_branch,
			default_exploit_branch=EXCLUDED.default_exploit_branch,
			runner_image=EXCLUDED.runner_image,
			run_timeout_seconds=EXCLUDED.run_timeout_seconds,
			max_concurrent_runs_per_subject=EXCLUDED.max_concurrent_runs_per_subject,
			manual_rerun_enabled=EXCLUDED.manual_rerun_enabled,
			updated_at=NOW()
		RETURNING default_source_branch, default_exploit_branch, runner_image,
		          run_timeout_seconds, max_concurrent_runs_per_subject, manual_rerun_enabled,
		          created_by, updated_at
	`, record.ModuleID, record.DefaultSourceBranch, record.DefaultExploitBranch, record.RunnerImage,
		record.RunTimeoutSeconds, record.MaxConcurrentRunsPerSubject, record.ManualRerunEnabled, actor,
	).Scan(&out.DefaultSourceBranch, &out.DefaultExploitBranch, &out.RunnerImage,
		&out.RunTimeoutSeconds, &out.MaxConcurrentRunsPerSubject, &out.ManualRerunEnabled,
		&out.CreatedBy, &out.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("attackdefense: upsert ad config: %w", err)
	}
	out.ModuleID = record.ModuleID
	if err := writeAudit(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("attackdefense: commit ad config: %w", err)
	}
	return &out, nil
}

// ListVulnerabilities returns all hidden vulnerability definitions.
func (s *Store) ListVulnerabilities(ctx context.Context, moduleID uuid.UUID, limit, offset int) ([]Vulnerability, int, error) {
	var total int
	if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM ad_vulnerabilities WHERE module_id=$1`, moduleID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("attackdefense: count vulnerabilities: %w", err)
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, module_id, key, title, description, exploit_points, patch_points,
		       exploit_test_selector, patch_test_selector, created_at, updated_at
		FROM ad_vulnerabilities
		WHERE module_id=$1
		ORDER BY key ASC
		LIMIT $2 OFFSET $3
	`, moduleID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("attackdefense: list vulnerabilities: %w", err)
	}
	defer rows.Close()
	items := make([]Vulnerability, 0)
	for rows.Next() {
		var item Vulnerability
		if err := rows.Scan(&item.ID, &item.ModuleID, &item.Key, &item.Title, &item.Description,
			&item.ExploitPoints, &item.PatchPoints, &item.ExploitTestSelector, &item.PatchTestSelector,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("attackdefense: scan vulnerability: %w", err)
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, 0, fmt.Errorf("attackdefense: list vulnerabilities: %w", rows.Err())
	}
	return items, total, nil
}

// VulnerabilityByID reads one vulnerability scoped to the module.
func (s *Store) VulnerabilityByID(ctx context.Context, moduleID, id uuid.UUID) (*Vulnerability, error) {
	return s.vulnerabilityByID(ctx, s.db.Pool, moduleID, id, false)
}

func (s *Store) vulnerabilityByID(ctx context.Context, q querier, moduleID, id uuid.UUID, forUpdate bool) (*Vulnerability, error) {
	query := `
		SELECT id, module_id, key, title, description, exploit_points, patch_points,
		       exploit_test_selector, patch_test_selector, created_at, updated_at
		FROM ad_vulnerabilities WHERE module_id=$1 AND id=$2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var item Vulnerability
	err := q.QueryRow(ctx, query, moduleID, id).Scan(&item.ID, &item.ModuleID, &item.Key, &item.Title,
		&item.Description, &item.ExploitPoints, &item.PatchPoints, &item.ExploitTestSelector,
		&item.PatchTestSelector, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVulnerabilityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read vulnerability: %w", err)
	}
	return &item, nil
}

// CreateVulnerability inserts a hidden test definition and audits it.
func (s *Store) CreateVulnerability(ctx context.Context, item *Vulnerability, audit AuditEvent) (*Vulnerability, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var out Vulnerability
	err = tx.QueryRow(ctx, `
		INSERT INTO ad_vulnerabilities (module_id, key, title, description, exploit_points,
			patch_points, exploit_test_selector, patch_test_selector)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, module_id, key, title, description, exploit_points, patch_points,
		          exploit_test_selector, patch_test_selector, created_at, updated_at
	`, item.ModuleID, item.Key, item.Title, item.Description, item.ExploitPoints,
		item.PatchPoints, item.ExploitTestSelector, item.PatchTestSelector,
	).Scan(&out.ID, &out.ModuleID, &out.Key, &out.Title, &out.Description, &out.ExploitPoints,
		&out.PatchPoints, &out.ExploitTestSelector, &out.PatchTestSelector, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrVulnerabilityKeyExists
		}
		return nil, fmt.Errorf("attackdefense: insert vulnerability: %w", err)
	}
	if err := writeAudit(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("attackdefense: commit vulnerability: %w", err)
	}
	return &out, nil
}

// UpdateVulnerability applies a full or partial update with the current row
// locked. Points and selectors remain server-validated.
func (s *Store) UpdateVulnerability(ctx context.Context, item *Vulnerability, audit AuditEvent) (*Vulnerability, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.vulnerabilityByID(ctx, tx, item.ModuleID, item.ID, true); err != nil {
		return nil, err
	}
	var out Vulnerability
	err = tx.QueryRow(ctx, `
		UPDATE ad_vulnerabilities SET key=$3, title=$4, description=$5, exploit_points=$6,
			patch_points=$7, exploit_test_selector=$8, patch_test_selector=$9, updated_at=NOW()
		WHERE module_id=$1 AND id=$2
		RETURNING id, module_id, key, title, description, exploit_points, patch_points,
		          exploit_test_selector, patch_test_selector, created_at, updated_at
	`, item.ModuleID, item.ID, item.Key, item.Title, item.Description, item.ExploitPoints,
		item.PatchPoints, item.ExploitTestSelector, item.PatchTestSelector,
	).Scan(&out.ID, &out.ModuleID, &out.Key, &out.Title, &out.Description, &out.ExploitPoints,
		&out.PatchPoints, &out.ExploitTestSelector, &out.PatchTestSelector, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrVulnerabilityKeyExists
		}
		return nil, fmt.Errorf("attackdefense: update vulnerability: %w", err)
	}
	if err := writeAudit(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("attackdefense: commit vulnerability update: %w", err)
	}
	return &out, nil
}

// DeleteVulnerability refuses to delete a definition that already produced
// awards; those are score evidence, not editable configuration.
func (s *Store) DeleteVulnerability(ctx context.Context, moduleID, id uuid.UUID, audit AuditEvent) error {
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.vulnerabilityByID(ctx, tx, moduleID, id, true); err != nil {
		return err
	}
	var awards int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ad_awards WHERE vulnerability_id=$1`, id).Scan(&awards); err != nil {
		return fmt.Errorf("attackdefense: count vulnerability awards: %w", err)
	}
	if awards > 0 {
		return ErrVulnerabilityInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ad_vulnerabilities WHERE module_id=$1 AND id=$2`, moduleID, id); err != nil {
		return fmt.Errorf("attackdefense: delete vulnerability: %w", err)
	}
	if err := writeAudit(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("attackdefense: commit vulnerability delete: %w", err)
	}
	return nil
}

// AuditEvent is the bounded audit row written in the same transaction as the
// mutation. Details never contain selectors, credentials, tokens or logs.
type AuditEvent struct {
	ActorUserID  uuid.UUID
	BoardID      *uuid.UUID
	ModuleID     *uuid.UUID
	Action       string
	ResourceType string
	ResourceID   *uuid.UUID
	Details      map[string]any
}

func writeAudit(ctx context.Context, tx pgx.Tx, event AuditEvent) error {
	if tx == nil || event.Action == "" || event.ActorUserID == uuid.Nil {
		return nil
	}
	details := event.Details
	if details == nil {
		details = map[string]any{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("attackdefense: encode audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, module_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, event.ActorUserID, event.BoardID, event.ModuleID, event.Action, event.ResourceType,
		event.ResourceID, json.RawMessage(encoded)); err != nil {
		return fmt.Errorf("attackdefense: write audit: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func trimTo(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return strings.ToValidUTF8(value[:max], "")
}
