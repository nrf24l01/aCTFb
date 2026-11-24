package boards

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// PostgresStore is the production Store implementation.
type PostgresStore struct {
	db *postgres.DB
	// beforeSubmissionWriteCheck is a test seam used only by the same package's
	// real-PostgreSQL regression tests. Production leaves it nil.
	beforeSubmissionWriteCheck func()
	submittedTextOnce          sync.Once
	submittedTextOK            bool
}

// NewPostgresStore constructs the PostgreSQL-backed board store.
func NewPostgresStore(db *postgres.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) pool() (*pgxpoolWrapper, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, errors.New("boards: postgres store is not configured")
	}
	return &pgxpoolWrapper{pool: s.db.Pool}, nil
}

// pgxpoolWrapper keeps the call sites small without exposing the pgx pool in
// the Store surface.
type pgxpoolWrapper struct {
	pool interface {
		Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
		QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
		Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
		Begin(ctx context.Context) (pgx.Tx, error)
	}
}

const boardColumns = `b.id, b.slug, b.name, b.description, b.participation_mode,
	b.registration_mode, b.visibility, b.status, b.rating_visibility,
	b.starts_at, b.ends_at, b.created_by, b.created_at, b.updated_at,
	b.team_min_members, b.team_max_members, b.team_allow_self_create,
	b.team_allow_leave_after_start, b.invite_code_expires_at,
	(SELECT count(*) FROM modules m WHERE m.board_id = b.id),
	COALESCE((SELECT array_agg(DISTINCT m.type ORDER BY m.type) FROM modules m WHERE m.board_id = b.id), '{}'),
	(SELECT count(*) FROM board_access_groups bag WHERE bag.board_id = b.id)`

type boardScanner interface {
	Scan(dest ...any) error
}

func scanBoard(row boardScanner) (*Board, error) {
	var board Board
	var moduleTypes []string
	var teamMin, teamMax *int
	var teamSelf, teamLeave bool
	err := row.Scan(
		&board.ID,
		&board.Slug,
		&board.Name,
		&board.Description,
		&board.ParticipationMode,
		&board.RegistrationMode,
		&board.Visibility,
		&board.Status,
		&board.RatingVisibility,
		&board.StartsAt,
		&board.EndsAt,
		&board.CreatedBy,
		&board.CreatedAt,
		&board.UpdatedAt,
		&teamMin,
		&teamMax,
		&teamSelf,
		&teamLeave,
		&board.InviteExpiresAt,
		&board.ModulesCount,
		&moduleTypes,
		&board.AccessGroupCount,
	)
	if err != nil {
		return nil, err
	}
	board.ModuleTypes = moduleTypes
	if teamMin != nil || teamMax != nil || teamSelf || teamLeave {
		board.TeamSettings = &TeamSettings{
			MinMembers: valueOrZero(teamMin), MaxMembers: valueOrZero(teamMax),
			AllowSelfCreate: teamSelf, AllowLeaveAfterStart: teamLeave,
		}
	}
	return &board, nil
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func effectiveBoardStatusSQL(nowParam string) string {
	return `CASE
		WHEN b.status = 'ARCHIVED' THEN 'ARCHIVED'
		WHEN b.status = 'SCHEDULED' AND b.starts_at IS NOT NULL AND b.starts_at <= ` + nowParam + ` THEN
			CASE WHEN b.ends_at IS NOT NULL AND b.ends_at <= ` + nowParam + ` THEN 'FINISHED' ELSE 'ACTIVE' END
		WHEN b.status = 'ACTIVE' AND b.ends_at IS NOT NULL AND b.ends_at <= ` + nowParam + ` THEN 'FINISHED'
		ELSE b.status
	END`
}

func (s *PostgresStore) ListBoards(ctx context.Context, filter BoardFilter) ([]Board, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}

	where := []string{"TRUE"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if !filter.Admin {
		add(`((EXISTS (SELECT 1 FROM board_participants bp WHERE bp.board_id = b.id AND bp.user_id = $%[1]d)
				AND (b.visibility = 'PUBLIC'
					OR NOT EXISTS (SELECT 1 FROM board_access_groups bag WHERE bag.board_id=b.id)
					OR EXISTS (SELECT 1 FROM board_access_groups bag
						JOIN access_group_members agm ON agm.group_id=bag.group_id
						WHERE bag.board_id=b.id AND agm.user_id=$%[1]d)))
			OR EXISTS (SELECT 1 FROM board_role_bindings bb WHERE bb.board_id = b.id AND bb.user_id = $%[1]d
				AND bb.revoked_at IS NULL AND (bb.expires_at IS NULL OR bb.expires_at > NOW()))
			OR (b.visibility = 'PUBLIC' AND b.status NOT IN ('DRAFT','ARCHIVED')))`, filter.UserID)
	}
	if filter.Query != "" {
		add(`b.name ILIKE '%%' || $%d || '%%'`, filter.Query)
	}
	if filter.ModuleType != "" {
		add(`EXISTS (SELECT 1 FROM modules m WHERE m.board_id = b.id AND m.type = $%d)`, filter.ModuleType)
	}
	if filter.ParticipationMode != "" {
		add(`b.participation_mode = $%d`, filter.ParticipationMode)
	}

	// The count query only receives parameters that its WHERE clause actually
	// references. Adding the clock parameter unconditionally here was the P1
	// adminListBoards 500: an admin with no filters had zero placeholders and
	// two bind arguments.
	countArgs := append([]any{}, args...)
	nowIndex := 0
	if filter.Status != "" {
		countArgs = append(countArgs, nowUTC())
		nowIndex = len(countArgs)
		statusSQL := effectiveBoardStatusSQL(fmt.Sprintf("$%d", nowIndex))
		countArgs = append(countArgs, filter.Status)
		where = append(where, statusSQL+" = $"+fmt.Sprint(len(countArgs)))
	}

	baseWhere := strings.Join(where, " AND ")

	var total int
	countSQL := `SELECT count(*) FROM boards b WHERE ` + baseWhere
	if err := p.pool.QueryRow(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list boards count: %w", err)
	}
	if total == 0 {
		return []Board{}, 0, nil
	}

	// The list query always needs a JOINED probe and an effective-status clock.
	// Reuse the count clock parameter when a status filter supplied one;
	// otherwise append it now. Parameters are appended in the exact order their
	// placeholders appear in the final SQL.
	listArgs := append([]any{}, countArgs...)
	joinedIndex := len(listArgs) + 1
	listArgs = append(listArgs, filter.UserID)
	if nowIndex == 0 {
		listArgs = append(listArgs, nowUTC())
		nowIndex = len(listArgs)
	}
	statusSQL := effectiveBoardStatusSQL(fmt.Sprintf("$%d", nowIndex))
	listArgs = append(listArgs, filter.Limit, filter.Offset)
	query := `SELECT ` + boardColumns + `, ` + statusSQL + `,
		EXISTS (SELECT 1 FROM board_participants bp WHERE bp.board_id=b.id AND bp.user_id=$` +
		fmt.Sprint(joinedIndex) + `) AS joined
		FROM boards b WHERE ` + baseWhere +
		` ORDER BY b.created_at DESC, b.id ASC LIMIT $` + fmt.Sprint(len(listArgs)-1) +
		` OFFSET $` + fmt.Sprint(len(listArgs))
	rows, err := p.pool.Query(ctx, query, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list boards: %w", err)
	}
	defer rows.Close()

	items := make([]Board, 0, filter.Limit)
	for rows.Next() {
		var board Board
		var moduleTypes []string
		var effective string
		var teamMin, teamMax *int
		var teamSelf, teamLeave bool
		if err := rows.Scan(
			&board.ID,
			&board.Slug,
			&board.Name,
			&board.Description,
			&board.ParticipationMode,
			&board.RegistrationMode,
			&board.Visibility,
			&board.Status,
			&board.RatingVisibility,
			&board.StartsAt,
			&board.EndsAt,
			&board.CreatedBy,
			&board.CreatedAt,
			&board.UpdatedAt,
			&teamMin,
			&teamMax,
			&teamSelf,
			&teamLeave,
			&board.InviteExpiresAt,
			&board.ModulesCount,
			&moduleTypes,
			&board.AccessGroupCount,
			&effective,
			&board.Joined,
		); err != nil {
			return nil, 0, fmt.Errorf("scan board: %w", err)
		}
		board.ModuleTypes = moduleTypes
		if teamMin != nil || teamMax != nil || teamSelf || teamLeave {
			board.TeamSettings = &TeamSettings{
				MinMembers: valueOrZero(teamMin), MaxMembers: valueOrZero(teamMax),
				AllowSelfCreate: teamSelf, AllowLeaveAfterStart: teamLeave,
			}
		}
		board.Status = BoardStatus(effective)
		items = append(items, board)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate boards: %w", err)
	}
	return items, total, nil
}

func (s *PostgresStore) GetBoard(ctx context.Context, id uuid.UUID) (*Board, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	board, err := scanBoard(p.pool.QueryRow(ctx, `SELECT `+boardColumns+` FROM boards b WHERE b.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBoardNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get board: %w", err)
	}
	return board, nil
}

func (s *PostgresStore) CreateBoard(ctx context.Context, in CreateBoardParams, audit AuditEventParams) (*Board, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create board: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var teamMin, teamMax any
	var teamSelf, teamLeave bool
	if in.TeamSettings != nil {
		teamMin, teamMax = in.TeamSettings.MinMembers, in.TeamSettings.MaxMembers
		teamSelf, teamLeave = in.TeamSettings.AllowSelfCreate, in.TeamSettings.AllowLeaveAfterStart
	}
	board, err := scanBoard(tx.QueryRow(ctx, `
		INSERT INTO boards (id, slug, name, description, participation_mode, registration_mode,
			visibility, status, rating_visibility, starts_at, ends_at, created_by,
			team_min_members, team_max_members, team_allow_self_create, team_allow_leave_after_start,
			invite_code_hash, invite_code_ciphertext, invite_code_expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING id, slug, name, description, participation_mode, registration_mode,
			visibility, status, rating_visibility, starts_at, ends_at, created_by,
			created_at, updated_at, team_min_members, team_max_members,
			team_allow_self_create, team_allow_leave_after_start, invite_code_expires_at,
			0, '{}'::text[], 0`,
		in.ID, in.Slug, in.Name, in.Description, in.ParticipationMode, in.RegistrationMode,
		in.Visibility, in.Status, in.RatingVisibility, in.StartsAt, in.EndsAt, in.CreatedBy,
		teamMin, teamMax, teamSelf, teamLeave, nullIfEmpty(in.InviteHash), in.InviteCiphertext, in.InviteExpiresAt,
	))
	if isUniqueViolation(err) {
		return nil, ErrBoardSlugAlreadyExists
	}
	if err != nil {
		return nil, fmt.Errorf("create board: %w", err)
	}
	if in.AccessGroupUUIDs != nil {
		if err := replaceBoardAccessGroupsTx(ctx, tx, board.ID, in.AccessGroupUUIDs); err != nil {
			return nil, err
		}
	}
	audit.BoardID = &board.ID
	audit.ResourceID = &board.ID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create board: %w", err)
	}
	return board, nil
}

func (s *PostgresStore) UpdateBoard(ctx context.Context, id uuid.UUID, in UpdateBoardParams, audit AuditEventParams) (*Board, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin update board: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var teamMin, teamMax any
	var teamSelf, teamLeave bool
	if in.TeamSettings != nil {
		teamMin, teamMax = in.TeamSettings.MinMembers, in.TeamSettings.MaxMembers
		teamSelf, teamLeave = in.TeamSettings.AllowSelfCreate, in.TeamSettings.AllowLeaveAfterStart
	}
	board, err := scanBoard(tx.QueryRow(ctx, `
		UPDATE boards SET slug=$2, name=$3, description=$4, participation_mode=$5,
			registration_mode=$6, visibility=$7, rating_visibility=$8,
			starts_at=$9, ends_at=$10,
			team_min_members=CASE WHEN $11 THEN $12 ELSE team_min_members END,
			team_max_members=CASE WHEN $11 THEN $13 ELSE team_max_members END,
			team_allow_self_create=CASE WHEN $11 THEN $14 ELSE team_allow_self_create END,
			team_allow_leave_after_start=CASE WHEN $11 THEN $15 ELSE team_allow_leave_after_start END,
			invite_code_hash=CASE WHEN $16 THEN $17 ELSE invite_code_hash END,
			invite_code_ciphertext=CASE WHEN $16 THEN $18 ELSE invite_code_ciphertext END,
			invite_code_expires_at=CASE WHEN $16 THEN $19 ELSE invite_code_expires_at END,
			updated_at=NOW()
		WHERE id=$1
		RETURNING id, slug, name, description, participation_mode, registration_mode,
			visibility, status, rating_visibility, starts_at, ends_at, created_by,
			created_at, updated_at, team_min_members, team_max_members,
			team_allow_self_create, team_allow_leave_after_start, invite_code_expires_at,
			(SELECT count(*) FROM modules m WHERE m.board_id = boards.id),
			COALESCE((SELECT array_agg(DISTINCT m.type ORDER BY m.type) FROM modules m WHERE m.board_id = boards.id), '{}'),
			(SELECT count(*) FROM board_access_groups bag WHERE bag.board_id = boards.id)`,
		id, in.Slug, in.Name, in.Description, in.ParticipationMode, in.RegistrationMode,
		in.Visibility, in.RatingVisibility, in.StartsAt, in.EndsAt,
		in.TeamSettingsSet, teamMin, teamMax, teamSelf, teamLeave,
		in.SetInvite, nullIfEmpty(in.InviteHash), in.InviteCiphertext, in.InviteExpiresAt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBoardNotFound
	}
	if isUniqueViolation(err) {
		return nil, ErrBoardSlugAlreadyExists
	}
	if err != nil {
		return nil, fmt.Errorf("update board: %w", err)
	}
	if in.ReplaceAccessGroups {
		if err := replaceBoardAccessGroupsTx(ctx, tx, id, in.AccessGroupUUIDs); err != nil {
			return nil, err
		}
	}
	audit.BoardID = &id
	audit.ResourceID = &id
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update board: %w", err)
	}
	return board, nil
}

func (s *PostgresStore) DeleteBoard(ctx context.Context, id uuid.UUID, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete board: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	audit.BoardID = &id
	audit.ResourceID = &id
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM boards WHERE id=$1`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrBoardDeleteNotAllowed
		}
		return fmt.Errorf("delete board: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBoardNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete board: %w", err)
	}
	return nil
}

func (s *PostgresStore) BoardModules(ctx context.Context, boardID uuid.UUID) ([]Module, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT `+moduleColumns+` FROM modules m WHERE m.board_id=$1 ORDER BY m.sort_order, m.id`, boardID)
	if err != nil {
		return nil, fmt.Errorf("load board modules: %w", err)
	}
	defer rows.Close()
	var modules []Module
	for rows.Next() {
		module, err := scanModuleRow(rows)
		if err != nil {
			return nil, err
		}
		modules = append(modules, *module)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate board modules: %w", err)
	}
	return modules, nil
}

func (s *PostgresStore) UpdateBoardStatus(ctx context.Context, id uuid.UUID, status BoardStatus, audit AuditEventParams) (*Board, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin update board status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	board, err := scanBoard(tx.QueryRow(ctx, `UPDATE boards SET status=$2, updated_at=NOW() WHERE id=$1
		RETURNING id, slug, name, description, participation_mode, registration_mode,
			visibility, status, rating_visibility, starts_at, ends_at, created_by,
			created_at, updated_at, team_min_members, team_max_members,
			team_allow_self_create, team_allow_leave_after_start, invite_code_expires_at,
			(SELECT count(*) FROM modules m WHERE m.board_id = boards.id),
			COALESCE((SELECT array_agg(DISTINCT m.type ORDER BY m.type) FROM modules m WHERE m.board_id = boards.id), '{}'),
			(SELECT count(*) FROM board_access_groups bag WHERE bag.board_id = boards.id)`,
		id, status))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBoardNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update board status: %w", err)
	}
	audit.BoardID = &id
	audit.ResourceID = &id
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update board status: %w", err)
	}
	return board, nil
}

func writeAuditTx(ctx context.Context, tx pgx.Tx, in AuditEventParams) error {
	if in.Action == "" {
		return nil
	}
	details := in.Details
	if details == nil {
		details = map[string]any{}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, module_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		in.ActorUserID, in.BoardID, in.ModuleID, in.Action, in.ResourceType, in.ResourceID, details)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func (s *PostgresStore) Audit(ctx context.Context, in AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	details := in.Details
	if details == nil {
		details = map[string]any{}
	}
	_, err = p.pool.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, module_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		in.ActorUserID, in.BoardID, in.ModuleID, in.Action, in.ResourceType, in.ResourceID, details)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func replaceBoardAccessGroupsTx(ctx context.Context, tx pgx.Tx, boardID uuid.UUID, groupIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM board_access_groups WHERE board_id=$1`, boardID); err != nil {
		return fmt.Errorf("clear board access groups: %w", err)
	}
	for _, groupID := range groupIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO board_access_groups (board_id, group_id) VALUES ($1,$2)`, boardID, groupID); err != nil {
			if isForeignKeyViolation(err) {
				return ErrAccessGroupNotFound
			}
			return fmt.Errorf("insert board access group: %w", err)
		}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == pgerrcode.ForeignKeyViolation || pgErr.Code == pgerrcode.RestrictViolation
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	out := value.String
	return &out
}

// emptyToNil stores an empty optional string as SQL NULL so CHECK constraints
// only see a real extension/checker key when one is configured.
func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// instanceSettingsJSON stores a nil policy as SQL NULL so "no module settings"
// stays distinguishable from an explicit zero-valued document.
func instanceSettingsJSON(settings *TaskInstanceSettings) any {
	raw, err := encodeTaskInstanceSettings(settings)
	if err != nil || len(raw) == 0 {
		return nil
	}
	return string(raw)
}
