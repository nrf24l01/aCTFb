package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nrf24l01/actfb/internal/authorization"
)

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type rowsQuerier interface {
	rowQuerier
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type liveUser struct {
	ID       uuid.UUID
	Disabled bool
	Roles    []string
}

func (u liveUser) isAdmin() bool {
	for _, role := range u.Roles {
		if strings.EqualFold(strings.TrimSpace(role), "admin") {
			return true
		}
	}
	return false
}

func (s *AccessService) ready() error {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return ErrNoDatabase
	}
	return nil
}

func loadLiveUser(ctx context.Context, q rowQuerier, userID uuid.UUID) (liveUser, error) {
	if userID == uuid.Nil {
		return liveUser{}, ErrUnauthorized
	}
	return scanLiveUser(q.QueryRow(ctx, `
		SELECT id, disabled, COALESCE(roles, ARRAY['user']::text[])
		FROM users WHERE id = $1
	`, userID))
}

func loadLiveUserForShare(ctx context.Context, q rowQuerier, userID uuid.UUID) (liveUser, error) {
	if userID == uuid.Nil {
		return liveUser{}, ErrUnauthorized
	}
	return scanLiveUser(q.QueryRow(ctx, `
		SELECT id, disabled, COALESCE(roles, ARRAY['user']::text[])
		FROM users WHERE id = $1 FOR SHARE
	`, userID))
}

func scanLiveUser(row pgx.Row) (liveUser, error) {
	var user liveUser
	err := row.Scan(&user.ID, &user.Disabled, &user.Roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return liveUser{}, ErrUnauthorized
	}
	if err != nil {
		return liveUser{}, fmt.Errorf("access: load live user: %w", err)
	}
	if user.Disabled {
		return liveUser{}, ErrUserDisabled
	}
	return user, nil
}

func (s *AccessService) requireGlobalAdmin(ctx context.Context, q rowQuerier, principal authorization.Principal, lock bool) (liveUser, error) {
	if s == nil {
		return liveUser{}, ErrNoDatabase
	}
	var (
		user liveUser
		err  error
	)
	if lock {
		user, err = loadLiveUserForShare(ctx, q, principal.UserID)
	} else {
		user, err = loadLiveUser(ctx, q, principal.UserID)
	}
	if err != nil {
		return liveUser{}, err
	}
	if !user.isAdmin() {
		return liveUser{}, ErrForbidden
	}
	return user, nil
}

func (s *AccessService) requirePermission(ctx context.Context, principal authorization.Principal, permission authorization.Permission, scope authorization.Scope) error {
	if s == nil || s.auth == nil {
		return ErrNoAuthorizer
	}
	decision, err := s.auth.Authorize(ctx, principal, permission, scope)
	if err != nil {
		switch {
		case errors.Is(err, authorization.ErrUnauthorized):
			return ErrUnauthorized
		case errors.Is(err, authorization.ErrUserDisabled):
			return ErrUserDisabled
		case errors.Is(err, authorization.ErrForbidden):
			return ErrForbidden
		case errors.Is(err, authorization.ErrBoardNotFound):
			return ErrBoardNotFound
		case errors.Is(err, authorization.ErrModuleNotFound):
			return ErrModuleNotFound
		default:
			return err
		}
	}
	if !decision.Allowed {
		return ErrForbidden
	}
	return nil
}

type boardRecord struct {
	ID                uuid.UUID
	Visibility        string
	RegistrationMode  string
	ParticipationMode string
	Status            string
	StartsAt          *time.Time
	EndsAt            *time.Time
}

func loadBoard(ctx context.Context, q rowQuerier, boardID uuid.UUID) (boardRecord, error) {
	var board boardRecord
	var startsAt, endsAt pgtype.Timestamptz
	err := q.QueryRow(ctx, `
		SELECT id, visibility, registration_mode, participation_mode, status, starts_at, ends_at
		FROM boards WHERE id = $1
	`, boardID).Scan(&board.ID, &board.Visibility, &board.RegistrationMode, &board.ParticipationMode, &board.Status, &startsAt, &endsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return boardRecord{}, ErrBoardNotFound
	}
	if err != nil {
		return boardRecord{}, fmt.Errorf("access: load board: %w", err)
	}
	if startsAt.Valid {
		value := startsAt.Time
		board.StartsAt = &value
	}
	if endsAt.Valid {
		value := endsAt.Time
		board.EndsAt = &value
	}
	return board, nil
}

func boardWindowOpen(ctx context.Context, q rowQuerier, board boardRecord) (bool, error) {
	var now time.Time
	if err := q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return false, fmt.Errorf("access: read database clock: %w", err)
	}
	if board.Status == "FINISHED" || board.Status == "ARCHIVED" {
		return false, nil
	}
	if board.EndsAt != nil && !board.EndsAt.After(now) {
		return false, nil
	}
	return true, nil
}

func isBoardParticipant(ctx context.Context, q rowQuerier, boardID, userID uuid.UUID) (bool, error) {
	var joined bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM board_participants WHERE board_id = $1 AND user_id = $2)
	`, boardID, userID).Scan(&joined); err != nil {
		return false, fmt.Errorf("access: check board participation: %w", err)
	}
	return joined, nil
}

func hasAnyScopedStaff(ctx context.Context, q rowQuerier, boardID, userID uuid.UUID) (bool, error) {
	var staff bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM board_role_bindings
			WHERE board_id = $1 AND user_id = $2
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at > clock_timestamp())
		)
	`, boardID, userID).Scan(&staff)
	if err != nil {
		return false, fmt.Errorf("access: check scoped staff: %w", err)
	}
	return staff, nil
}

func hasModuleStaff(ctx context.Context, q rowQuerier, boardID, moduleID, userID uuid.UUID) (bool, error) {
	var staff bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM board_role_bindings
			WHERE board_id = $1 AND user_id = $2
			  AND (module_id IS NULL OR module_id = $3)
			  AND revoked_at IS NULL
			  AND (expires_at IS NULL OR expires_at > clock_timestamp())
		)
	`, boardID, userID, moduleID).Scan(&staff)
	if err != nil {
		return false, fmt.Errorf("access: check module staff: %w", err)
	}
	return staff, nil
}

func participantGroupAllowed(ctx context.Context, q rowQuerier, board boardRecord, userID uuid.UUID) (bool, error) {
	if board.Visibility == "PUBLIC" {
		return true, nil
	}
	var groups int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM board_access_groups WHERE board_id = $1`, board.ID).Scan(&groups); err != nil {
		if isUndefinedTable(err) {
			return true, nil
		}
		return false, fmt.Errorf("access: count board access groups: %w", err)
	}
	if groups == 0 {
		return true, nil
	}
	var member bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM board_access_groups bag
			JOIN access_group_members agm ON agm.group_id = bag.group_id
			WHERE bag.board_id = $1 AND agm.user_id = $2
		)
	`, board.ID, userID).Scan(&member); err != nil {
		return false, fmt.Errorf("access: check access-group membership: %w", err)
	}
	return member, nil
}

func resolveSubject(ctx context.Context, q rowQuerier, board boardRecord, userID uuid.UUID) (Subject, bool, error) {
	switch board.ParticipationMode {
	case "INDIVIDUAL":
		joined, err := isBoardParticipant(ctx, q, board.ID, userID)
		if err != nil || !joined {
			return Subject{}, false, err
		}
		return Subject{Type: "USER", ID: userID}, true, nil
	case "TEAM":
		var teamID uuid.UUID
		err := q.QueryRow(ctx, `
			SELECT team_id FROM team_members WHERE board_id = $1 AND user_id = $2
		`, board.ID, userID).Scan(&teamID)
		if errors.Is(err, pgx.ErrNoRows) {
			// The captain column is authoritative if the captain row has not
			// yet been materialized in team_members.
			err = q.QueryRow(ctx, `
				SELECT id FROM teams WHERE board_id = $1 AND captain_user_id = $2
			`, board.ID, userID).Scan(&teamID)
			if errors.Is(err, pgx.ErrNoRows) {
				return Subject{}, false, nil
			}
		}
		if err != nil {
			if isUndefinedTable(err) {
				return Subject{}, false, nil
			}
			return Subject{}, false, fmt.Errorf("access: resolve team subject: %w", err)
		}
		return Subject{Type: "TEAM", ID: teamID}, true, nil
	default:
		return Subject{}, false, ErrInvalidSubject
	}
}

func isTeamCaptain(ctx context.Context, q rowQuerier, boardID, userID uuid.UUID) (bool, error) {
	var captain bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM teams WHERE board_id = $1 AND captain_user_id = $2)
	`, boardID, userID).Scan(&captain)
	if err != nil {
		if isUndefinedTable(err) {
			return false, nil
		}
		return false, fmt.Errorf("access: check team captain: %w", err)
	}
	return captain, nil
}

func moduleBelongsToBoard(ctx context.Context, q rowQuerier, boardID, moduleID uuid.UUID) error {
	var exists bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM modules WHERE id = $1 AND board_id = $2)
	`, moduleID, boardID).Scan(&exists); err != nil {
		return fmt.Errorf("access: check module parent: %w", err)
	}
	if !exists {
		return ErrModuleNotFound
	}
	return nil
}

func moduleAudienceAllowed(ctx context.Context, q rowQuerier, boardID, moduleID uuid.UUID, subject Subject) (bool, error) {
	var audiences int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM module_audiences WHERE board_id = $1 AND module_id = $2`, boardID, moduleID).Scan(&audiences); err != nil {
		return false, fmt.Errorf("access: count module audiences: %w", err)
	}
	if audiences == 0 {
		return true, nil
	}
	var member bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM module_audiences ma
			JOIN audience_members am ON am.audience_id = ma.audience_id
			WHERE ma.board_id = $1 AND ma.module_id = $2
			  AND am.subject_type = $3 AND am.subject_id = $4
		)
	`, boardID, moduleID, subject.Type, subject.ID).Scan(&member); err != nil {
		return false, fmt.Errorf("access: check module audience: %w", err)
	}
	return member, nil
}

func clampPage(limit, offset int) (int, int) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

// CanJoinBoard checks static join eligibility. For INVITE_ONLY, the board
// service must validate the invite code before calling this method; this method
// still enforces window, CLOSED rejection, and the access-group gate.
func (s *AccessService) CanJoinBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	user, err := loadLiveUser(ctx, s.db.Pool, principal.UserID)
	if err != nil {
		return false, err
	}
	board, err := loadBoard(ctx, s.db.Pool, boardID)
	if err != nil {
		return false, err
	}
	if open, err := boardWindowOpen(ctx, s.db.Pool, board); err != nil || !open {
		return false, err
	}
	joined, err := isBoardParticipant(ctx, s.db.Pool, boardID, user.ID)
	if err != nil {
		return false, err
	}
	if !joined && board.RegistrationMode == "CLOSED" {
		return false, nil
	}
	if board.Visibility == "PUBLIC" {
		return true, nil
	}
	groupAllowed, err := participantGroupAllowed(ctx, s.db.Pool, board, user.ID)
	if err != nil || !groupAllowed {
		return false, err
	}
	return true, nil
}

// CanReadBoard is the read gate. PUBLIC boards have no access-group
// restriction. PRIVATE participant reads require current group membership when
// the board has groups. Live board-wide staff may read for management; own
// participant endpoints still require participation + ABAC in their business
// services.
func (s *AccessService) CanReadBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	user, err := loadLiveUser(ctx, s.db.Pool, principal.UserID)
	if err != nil {
		return false, err
	}
	board, err := loadBoard(ctx, s.db.Pool, boardID)
	if err != nil {
		return false, err
	}
	if user.isAdmin() || board.Visibility == "PUBLIC" {
		return true, nil
	}
	joined, err := isBoardParticipant(ctx, s.db.Pool, boardID, user.ID)
	if err != nil {
		return false, err
	}
	if joined {
		// Participant reads require current group membership. A scoped role or
		// explicit grant never overrides a revoked group for participant reads.
		return participantGroupAllowed(ctx, s.db.Pool, board, user.ID)
	}
	if staff, err := hasAnyScopedStaff(ctx, s.db.Pool, boardID, user.ID); err != nil {
		return false, err
	} else if staff {
		// Board shell for non-participant staff with a live scoped role; module
		// details still require CanAccessModule for the exact module.
		return true, nil
	}
	return false, nil
}

// CanAccessModule gates participant module visibility by joined subject and
// audience membership. Scoped staff bypass applies only to management of their
// assigned module.
func (s *AccessService) CanAccessModule(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	user, err := loadLiveUser(ctx, s.db.Pool, principal.UserID)
	if err != nil {
		return false, err
	}
	board, err := loadBoard(ctx, s.db.Pool, boardID)
	if err != nil {
		return false, err
	}
	if err := moduleBelongsToBoard(ctx, s.db.Pool, boardID, moduleID); err != nil {
		return false, err
	}
	if user.isAdmin() {
		return true, nil
	}
	joined, err := isBoardParticipant(ctx, s.db.Pool, boardID, user.ID)
	if err != nil {
		return false, err
	}
	if joined {
		return s.participantModuleAllowed(ctx, board, moduleID, user.ID)
	}
	if staff, err := hasModuleStaff(ctx, s.db.Pool, boardID, moduleID, user.ID); err != nil {
		return false, err
	} else if staff {
		// Management bypass only for a non-participant with a live scoped role
		// for this exact module/board.
		return true, nil
	}
	return false, nil
}

// CanReadBoardAsParticipant applies only the participant board rules: a joined
// participant with live access-group membership. It deliberately has no
// global-admin shortcut, so own-progress reads cannot bypass a revoked group.
func (s *AccessService) CanReadBoardAsParticipant(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	user, err := loadLiveUser(ctx, s.db.Pool, principal.UserID)
	if err != nil {
		return false, err
	}
	board, err := loadBoard(ctx, s.db.Pool, boardID)
	if err != nil {
		return false, err
	}
	joined, err := isBoardParticipant(ctx, s.db.Pool, boardID, user.ID)
	if err != nil || !joined {
		return false, err
	}
	return participantGroupAllowed(ctx, s.db.Pool, board, user.ID)
}

// CanAccessModuleAsParticipant applies only the participant rules: a joined
// subject with live board access-group membership and module audience
// membership. It deliberately has no global-admin shortcut, so an admin who
// joined a board cannot bypass a revoked access group or module audience while
// acting in a participant context (own submission/history). A non-participant
// is denied.
func (s *AccessService) CanAccessModuleAsParticipant(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	user, err := loadLiveUser(ctx, s.db.Pool, principal.UserID)
	if err != nil {
		return false, err
	}
	board, err := loadBoard(ctx, s.db.Pool, boardID)
	if err != nil {
		return false, err
	}
	if err := moduleBelongsToBoard(ctx, s.db.Pool, boardID, moduleID); err != nil {
		return false, err
	}
	joined, err := isBoardParticipant(ctx, s.db.Pool, boardID, user.ID)
	if err != nil || !joined {
		return false, err
	}
	return s.participantModuleAllowed(ctx, board, moduleID, user.ID)
}

// participantModuleAllowed is the shared participant visibility rule: current
// access-group membership plus current audience membership for the resolved
// subject. Revocation is immediate.
func (s *AccessService) participantModuleAllowed(
	ctx context.Context,
	board boardRecord,
	moduleID uuid.UUID,
	userID uuid.UUID,
) (bool, error) {
	groupAllowed, err := participantGroupAllowed(ctx, s.db.Pool, board, userID)
	if err != nil || !groupAllowed {
		return false, err
	}
	subject, ok, err := resolveSubject(ctx, s.db.Pool, board, userID)
	if err != nil || !ok {
		return false, err
	}
	return moduleAudienceAllowed(ctx, s.db.Pool, board.ID, moduleID, subject)
}

func (s *AccessService) IsSubjectInAudience(ctx context.Context, boardID, audienceID uuid.UUID, subject Subject) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if !validSubjectType(subject.Type) || subject.ID == uuid.Nil {
		return false, ErrInvalidSubject
	}
	var exists bool
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM audiences a
			JOIN audience_members am ON am.audience_id = a.id
			WHERE a.id = $1 AND a.board_id = $2
			  AND am.subject_type = $3 AND am.subject_id = $4
		)
	`, audienceID, boardID, subject.Type, subject.ID).Scan(&exists); err != nil {
		return false, fmt.Errorf("access: check audience membership: %w", err)
	}
	return exists, nil
}

func validSubjectType(subjectType string) bool {
	return subjectType == "USER" || subjectType == "TEAM"
}
