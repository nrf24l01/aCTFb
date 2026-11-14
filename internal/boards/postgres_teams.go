package boards

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const teamSelect = `
	SELECT t.id, t.board_id, t.name, t.captain_user_id,
		(SELECT count(*)::int FROM team_members tm WHERE tm.board_id=t.board_id AND tm.team_id=t.id),
		COALESCE(b.team_max_members, 100)
	FROM teams t JOIN boards b ON b.id=t.board_id`

func scanTeam(row boardScanner) (*Team, error) {
	var team Team
	if err := row.Scan(&team.ID, &team.BoardID, &team.Name, &team.CaptainUserID, &team.MembersCount, &team.MaxMembers); err != nil {
		return nil, err
	}
	return &team, nil
}

func (s *PostgresStore) CreateTeam(ctx context.Context, in CreateTeamParams, audit AuditEventParams) (*Team, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create team: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "p1-team:"+in.BoardID.String()); err != nil {
		return nil, fmt.Errorf("lock team create: %w", err)
	}
	participant, err := isParticipantTx(ctx, tx, in.BoardID, in.CaptainUserID)
	if err != nil {
		return nil, err
	}
	if !participant {
		return nil, ErrBoardNotJoined
	}
	var existing bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM team_members WHERE board_id=$1 AND user_id=$2)`, in.BoardID, in.CaptainUserID).Scan(&existing); err != nil {
		return nil, fmt.Errorf("check existing team: %w", err)
	}
	if existing {
		return nil, ErrUserAlreadyInTeam
	}
	var teamID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO teams (board_id,name,captain_user_id) VALUES ($1,$2,$3) RETURNING id`,
		in.BoardID, in.Name, in.CaptainUserID).Scan(&teamID); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTeamNameExists
		}
		return nil, fmt.Errorf("insert team: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO team_members (board_id,team_id,user_id) VALUES ($1,$2,$3)`,
		in.BoardID, teamID, in.CaptainUserID); err != nil {
		return nil, fmt.Errorf("insert captain membership: %w", err)
	}
	audit.BoardID = &in.BoardID
	audit.ResourceID = &teamID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create team: %w", err)
	}
	return s.GetTeam(ctx, in.BoardID, teamID)
}

func (s *PostgresStore) GetTeam(ctx context.Context, boardID, teamID uuid.UUID) (*Team, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	team, err := scanTeam(p.pool.QueryRow(ctx, teamSelect+` WHERE t.board_id=$1 AND t.id=$2`, boardID, teamID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTeamNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get team: %w", err)
	}
	return team, nil
}

func (s *PostgresStore) GetTeamByMember(ctx context.Context, boardID, userID uuid.UUID) (*Team, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	team, err := scanTeam(p.pool.QueryRow(ctx, teamSelect+` JOIN team_members tm ON tm.board_id=t.board_id AND tm.team_id=t.id WHERE t.board_id=$1 AND tm.user_id=$2`, boardID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTeamNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get team by member: %w", err)
	}
	return team, nil
}

func (s *PostgresStore) ListTeams(ctx context.Context, boardID uuid.UUID, limit, offset int) ([]Team, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM teams WHERE board_id=$1`, boardID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count teams: %w", err)
	}
	if total == 0 {
		return []Team{}, 0, nil
	}
	rows, err := p.pool.Query(ctx, teamSelect+` WHERE t.board_id=$1 ORDER BY t.name,t.id LIMIT $2 OFFSET $3`, boardID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()
	items := make([]Team, 0, limit)
	for rows.Next() {
		team, err := scanTeam(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan team: %w", err)
		}
		items = append(items, *team)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate teams: %w", err)
	}
	return items, total, nil
}

func (s *PostgresStore) ListTeamMembers(ctx context.Context, boardID, teamID uuid.UUID, limit, offset int) ([]TeamMember, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teams WHERE board_id=$1 AND id=$2)`, boardID, teamID).Scan(&exists); err != nil {
		return nil, 0, fmt.Errorf("check team: %w", err)
	}
	if !exists {
		return nil, 0, ErrTeamNotFound
	}
	var total int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM team_members WHERE board_id=$1 AND team_id=$2`, boardID, teamID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count team members: %w", err)
	}
	if total == 0 {
		return []TeamMember{}, 0, nil
	}
	rows, err := p.pool.Query(ctx, `
		SELECT tm.user_id, u.username, tm.joined_at
		FROM team_members tm JOIN users u ON u.id=tm.user_id
		WHERE tm.board_id=$1 AND tm.team_id=$2 ORDER BY tm.joined_at, tm.user_id LIMIT $3 OFFSET $4`,
		boardID, teamID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list team members: %w", err)
	}
	defer rows.Close()
	items := make([]TeamMember, 0, limit)
	for rows.Next() {
		var member TeamMember
		if err := rows.Scan(&member.UserID, &member.Username, &member.JoinedAt); err != nil {
			return nil, 0, fmt.Errorf("scan team member: %w", err)
		}
		items = append(items, member)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate team members: %w", err)
	}
	return items, total, nil
}

func (s *PostgresStore) UpdateTeam(ctx context.Context, boardID, teamID uuid.UUID, name string, audit AuditEventParams) (*Team, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin update team: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE teams SET name=$3, updated_at=NOW() WHERE board_id=$1 AND id=$2`, boardID, teamID, name)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTeamNameExists
		}
		return nil, fmt.Errorf("update team: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrTeamNotFound
	}
	audit.BoardID = &boardID
	audit.ResourceID = &teamID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update team: %w", err)
	}
	return s.GetTeam(ctx, boardID, teamID)
}

func (s *PostgresStore) DeleteTeam(ctx context.Context, boardID, teamID uuid.UUID, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete team: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	audit.BoardID = &boardID
	audit.ResourceID = &teamID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM teams WHERE board_id=$1 AND id=$2`, boardID, teamID)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTeamNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete team: %w", err)
	}
	return nil
}

func (s *PostgresStore) JoinTeam(ctx context.Context, in JoinTeamParams, audit AuditEventParams) (*Team, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin join team: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "p1-team:"+in.BoardID.String()); err != nil {
		return nil, fmt.Errorf("lock team join: %w", err)
	}
	participant, err := isParticipantTx(ctx, tx, in.BoardID, in.UserID)
	if err != nil {
		return nil, err
	}
	if !participant {
		return nil, ErrBoardNotJoined
	}
	var teamID uuid.UUID
	var maxMembers int
	if err := tx.QueryRow(ctx, `SELECT id, COALESCE((SELECT team_max_members FROM boards WHERE id=$1),100) FROM teams WHERE board_id=$1 AND id=$2 FOR UPDATE`, in.BoardID, in.TeamID).Scan(&teamID, &maxMembers); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTeamNotFound
		}
		return nil, fmt.Errorf("load team: %w", err)
	}
	var existing bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM team_members WHERE board_id=$1 AND user_id=$2)`, in.BoardID, in.UserID).Scan(&existing); err != nil {
		return nil, fmt.Errorf("check team membership: %w", err)
	}
	if existing {
		return nil, ErrUserAlreadyInTeam
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM team_members WHERE board_id=$1 AND team_id=$2`, in.BoardID, in.TeamID).Scan(&count); err != nil {
		return nil, fmt.Errorf("count team members: %w", err)
	}
	if maxMembers > 0 && count >= maxMembers {
		return nil, ErrTeamFull
	}
	if _, err := tx.Exec(ctx, `INSERT INTO team_members (board_id,team_id,user_id) VALUES ($1,$2,$3)`, in.BoardID, in.TeamID, in.UserID); err != nil {
		return nil, fmt.Errorf("join team: %w", err)
	}
	audit.BoardID = &in.BoardID
	audit.ResourceID = &in.TeamID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit join team: %w", err)
	}
	return s.GetTeam(ctx, in.BoardID, in.TeamID)
}

func (s *PostgresStore) LeaveTeam(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin leave team: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var teamID uuid.UUID
	var captain uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT t.id,t.captain_user_id FROM teams t JOIN team_members tm ON tm.board_id=t.board_id AND tm.team_id=t.id WHERE t.board_id=$1 AND tm.user_id=$2 FOR UPDATE`, boardID, userID).Scan(&teamID, &captain); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTeamMemberNotFound
		}
		return fmt.Errorf("load team membership: %w", err)
	}
	if captain == userID {
		return ErrTeamCaptainMustTransfer
	}
	if _, err := tx.Exec(ctx, `DELETE FROM team_members WHERE board_id=$1 AND user_id=$2`, boardID, userID); err != nil {
		return fmt.Errorf("leave team: %w", err)
	}
	audit.BoardID = &boardID
	audit.ResourceID = &teamID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit leave team: %w", err)
	}
	return nil
}

func (s *PostgresStore) RemoveTeamMember(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error {
	return s.LeaveTeam(ctx, boardID, userID, audit)
}

func (s *PostgresStore) TransferCaptain(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) (*Team, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transfer captain: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var teamID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT team_id FROM team_members WHERE board_id=$1 AND user_id=$2`, boardID, userID).Scan(&teamID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTeamMemberNotFound
		}
		return nil, fmt.Errorf("load target team: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE teams SET captain_user_id=$3, updated_at=NOW() WHERE board_id=$1 AND id=$2`, boardID, teamID, userID); err != nil {
		return nil, fmt.Errorf("transfer captain: %w", err)
	}
	audit.BoardID = &boardID
	audit.ResourceID = &teamID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transfer captain: %w", err)
	}
	return s.GetTeam(ctx, boardID, teamID)
}

func (s *PostgresStore) SetTeamInvite(ctx context.Context, in SetTeamInviteParams, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin set team invite: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE teams SET invite_key_hash=$3, invite_key_ciphertext=$4, invite_key_expires_at=$5, updated_at=NOW() WHERE board_id=$1 AND id=$2`,
		in.BoardID, in.TeamID, in.InviteKeyHash, in.Ciphertext, in.ExpiresAt)
	if err != nil {
		return fmt.Errorf("set team invite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTeamNotFound
	}
	audit.BoardID = &in.BoardID
	audit.ResourceID = &in.TeamID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit set team invite: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetTeamInvite(ctx context.Context, boardID, teamID uuid.UUID) (*SetTeamInviteParams, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	var out SetTeamInviteParams
	out.BoardID, out.TeamID = boardID, teamID
	var hash *string
	if err := p.pool.QueryRow(ctx, `SELECT invite_key_hash, invite_key_ciphertext, invite_key_expires_at FROM teams WHERE board_id=$1 AND id=$2`, boardID, teamID).Scan(&hash, &out.Ciphertext, &out.ExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTeamNotFound
		}
		return nil, fmt.Errorf("get team invite: %w", err)
	}
	if hash != nil {
		out.InviteKeyHash = *hash
	}
	return &out, nil
}

func (s *PostgresStore) TeamByInviteHash(ctx context.Context, boardID uuid.UUID, hash string, now time.Time) (*Team, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	team, err := scanTeam(p.pool.QueryRow(ctx, teamSelect+` WHERE t.board_id=$1 AND t.invite_key_hash=$2 AND (t.invite_key_expires_at IS NULL OR t.invite_key_expires_at > $3)`, boardID, hash, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidTeamInviteKey
	}
	if err != nil {
		return nil, fmt.Errorf("find team invite: %w", err)
	}
	return team, nil
}

func (s *PostgresStore) SetBoardInvite(ctx context.Context, boardID uuid.UUID, hash string, ciphertext []byte, expiresAt *time.Time, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin set board invite: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE boards SET invite_code_hash=$2, invite_code_ciphertext=$3, invite_code_expires_at=$4, updated_at=NOW() WHERE id=$1`, boardID, hash, ciphertext, expiresAt)
	if err != nil {
		return fmt.Errorf("set board invite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBoardNotFound
	}
	audit.BoardID = &boardID
	audit.ResourceID = &boardID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit set board invite: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetBoardInvite(ctx context.Context, boardID uuid.UUID) (string, []byte, *time.Time, error) {
	p, err := s.pool()
	if err != nil {
		return "", nil, nil, err
	}
	var hash *string
	var ciphertext []byte
	var expiresAt *time.Time
	if err := p.pool.QueryRow(ctx, `SELECT invite_code_hash, invite_code_ciphertext, invite_code_expires_at FROM boards WHERE id=$1`, boardID).Scan(&hash, &ciphertext, &expiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, nil, ErrBoardNotFound
		}
		return "", nil, nil, fmt.Errorf("get board invite: %w", err)
	}
	if hash == nil {
		return "", nil, nil, ErrInvalidInviteCode
	}
	return *hash, ciphertext, expiresAt, nil
}

func (s *PostgresStore) ResolveSubject(ctx context.Context, boardID, userID uuid.UUID) (ScoreSubject, error) {
	p, err := s.pool()
	if err != nil {
		return ScoreSubject{}, err
	}
	return resolveSubjectQuerier(ctx, p.pool, boardID, userID)
}

type subjectQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func resolveSubjectQuerier(ctx context.Context, q subjectQuerier, boardID, userID uuid.UUID) (ScoreSubject, error) {
	var mode string
	if err := q.QueryRow(ctx, `SELECT participation_mode FROM boards WHERE id=$1`, boardID).Scan(&mode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ScoreSubject{}, ErrBoardNotFound
		}
		return ScoreSubject{}, fmt.Errorf("resolve board subject: %w", err)
	}
	if mode != ParticipationTeam {
		return ScoreSubject{Type: SubjectTypeUser, ID: userID}, nil
	}
	var teamID uuid.UUID
	if err := q.QueryRow(ctx, `SELECT team_id FROM team_members WHERE board_id=$1 AND user_id=$2`, boardID, userID).Scan(&teamID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ScoreSubject{}, ErrTeamRequired
		}
		return ScoreSubject{}, fmt.Errorf("resolve team subject: %w", err)
	}
	return ScoreSubject{Type: SubjectTypeTeam, ID: teamID}, nil
}

func (s *PostgresStore) SubjectSolvedTask(ctx context.Context, subject ScoreSubject, taskID uuid.UUID) (bool, error) {
	p, err := s.pool()
	if err != nil {
		return false, err
	}
	var solved bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM submissions WHERE subject_type=$1 AND subject_id=$2 AND task_id=$3 AND status='ACCEPTED')`, subject.Type, subject.ID, taskID).Scan(&solved); err != nil {
		return false, fmt.Errorf("check subject solved: %w", err)
	}
	return solved, nil
}

func isParticipantTx(ctx context.Context, tx pgx.Tx, boardID, userID uuid.UUID) (bool, error) {
	var joined bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM board_participants WHERE board_id=$1 AND user_id=$2)`, boardID, userID).Scan(&joined); err != nil {
		return false, fmt.Errorf("check participant: %w", err)
	}
	return joined, nil
}
