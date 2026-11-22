package access

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// UserDirectoryEntry is the minimal identity projection returned by the
// reusable user picker. It never includes password hashes, tokens or roles.
type UserDirectoryEntry struct {
	UserID   uuid.UUID
	Username string
	Email    string
}

// UserDirectoryPage is a bounded server-side search page.
type UserDirectoryPage struct {
	Items []UserDirectoryEntry
	Total int
}

// TeamDirectoryEntry is the minimal team projection for the audience picker.
// It exposes only the team name and UUID; no captain or membership identity.
type TeamDirectoryEntry struct {
	TeamID uuid.UUID
	Name   string
}

// TeamDirectoryPage is a bounded server-side team search page.
type TeamDirectoryPage struct {
	Items []TeamDirectoryEntry
	Total int
}

// CaptainRosterEntry is the minimal projection a team captain may see while
// searching for an eligible board participant. Email is deliberately absent:
// captains can match on it but the response never discloses it.
type CaptainRosterEntry struct {
	UserID   uuid.UUID
	Username string
}

// CaptainRosterPage is a bounded captain-scoped roster search page.
type CaptainRosterPage struct {
	Items []CaptainRosterEntry
	Total int
}

// escapeLike makes user input a literal substring/prefix match. The caller must
// use ESCAPE '\' in the SQL so backslash, percent and underscore stay literal.
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func uuidPrefix(value string) (string, bool) {
	trimmed := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	if trimmed == "" {
		return "", false
	}
	for _, character := range trimmed {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", false
		}
	}
	if len(trimmed) < 4 {
		return "", false
	}
	return trimmed, true
}

// SearchUsers is the global-admin directory search used by the admin user page
// and the reusable combobox. Matching is literal (wildcards escaped) over
// username and email, with UUID exact/prefix support.
func (s *AccessService) SearchUsers(ctx context.Context, principal authorization.Principal, q ListQuery) (UserDirectoryPage, error) {
	if err := s.ready(); err != nil {
		return UserDirectoryPage{}, err
	}
	if _, err := s.requireGlobalAdmin(ctx, s.db.Pool, principal, false); err != nil {
		return UserDirectoryPage{}, err
	}
	return s.searchUsers(ctx, s.db.Pool, q)
}

// authorizeBoardRoster requires one live board-scoped permission that justifies
// reading a board's identity roster. It never grants a global directory.
func (s *AccessService) authorizeBoardRoster(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) error {
	if s.auth == nil {
		return ErrNoAuthorizer
	}
	permissions := []authorization.Permission{
		authorization.PermissionBoardParticipantsManage,
		authorization.PermissionBoardStaffManage,
		authorization.PermissionBoardAudiencesManage,
	}
	for _, permission := range permissions {
		decision, err := s.auth.Authorize(ctx, principal, permission, authorization.Scope{BoardID: boardID})
		if err != nil {
			return err
		}
		if decision.Allowed {
			return nil
		}
	}
	return ErrForbidden
}

// SearchBoardUsers is the board-scoped picker. It requires a live board roster
// permission on the exact board and returns only minimal identity fields. It is
// paginated, requires a non-empty term and is never an anonymous directory.
func (s *AccessService) SearchBoardUsers(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, q ListQuery) (UserDirectoryPage, error) {
	if err := s.ready(); err != nil {
		return UserDirectoryPage{}, err
	}
	if boardID == uuid.Nil {
		return UserDirectoryPage{}, ErrBoardNotFound
	}
	if err := s.authorizeBoardRoster(ctx, principal, boardID); err != nil {
		return UserDirectoryPage{}, err
	}
	if strings.TrimSpace(q.Query) == "" {
		// Never allow an empty board-scoped query to dump the global directory.
		return UserDirectoryPage{Items: []UserDirectoryEntry{}}, nil
	}
	page, err := s.searchUsers(ctx, s.db.Pool, q)
	if err != nil {
		return UserDirectoryPage{}, err
	}
	if err := s.auditBoardSearch(ctx, principal.UserID, boardID, "user_search.board"); err != nil {
		return UserDirectoryPage{}, err
	}
	return page, nil
}

// SearchBoardTeams is the board-scoped team picker used by team audiences. It
// returns only team name and UUID for teams in the exact board.
func (s *AccessService) SearchBoardTeams(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, q ListQuery) (TeamDirectoryPage, error) {
	if err := s.ready(); err != nil {
		return TeamDirectoryPage{}, err
	}
	if boardID == uuid.Nil {
		return TeamDirectoryPage{}, ErrBoardNotFound
	}
	if err := s.authorizeBoardRoster(ctx, principal, boardID); err != nil {
		return TeamDirectoryPage{}, err
	}
	if strings.TrimSpace(q.Query) == "" {
		return TeamDirectoryPage{Items: []TeamDirectoryEntry{}}, nil
	}
	limit, offset := clampPage(q.Limit, q.Offset)
	query := strings.TrimSpace(q.Query)
	escaped := escapeLike(query)
	var exact uuid.UUID
	if parsed, err := uuid.Parse(query); err == nil {
		exact = parsed
	}
	prefix, _ := uuidPrefix(query)

	predicate := `
		WHERE t.board_id = $1 AND (
			t.id = $2
			OR ($3 <> '' AND replace(t.id::text, '-', '') LIKE $3 || '%')
			OR t.name ILIKE '%' || $4 || '%' ESCAPE '\'
		)
	`
	rows, err := s.db.Pool.Query(ctx, `
		SELECT t.id, t.name FROM teams t
	`+predicate+`
		ORDER BY (t.id = $2) DESC, t.name, t.id
		LIMIT $5 OFFSET $6
	`, boardID, exact, prefix, escaped, limit, offset)
	if err != nil {
		return TeamDirectoryPage{}, fmt.Errorf("access: search board teams: %w", err)
	}
	defer rows.Close()
	page := TeamDirectoryPage{Items: make([]TeamDirectoryEntry, 0)}
	for rows.Next() {
		var entry TeamDirectoryEntry
		if err := rows.Scan(&entry.TeamID, &entry.Name); err != nil {
			return TeamDirectoryPage{}, fmt.Errorf("access: scan team search row: %w", err)
		}
		page.Items = append(page.Items, entry)
	}
	if err := rows.Err(); err != nil {
		return TeamDirectoryPage{}, fmt.Errorf("access: iterate team search: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM teams t
	`+predicate, boardID, exact, prefix, escaped).Scan(&page.Total); err != nil {
		return TeamDirectoryPage{}, fmt.Errorf("access: count team search: %w", err)
	}
	if err := s.auditBoardSearch(ctx, principal.UserID, boardID, "team_search.board"); err != nil {
		return TeamDirectoryPage{}, err
	}
	return page, nil
}

// SearchCaptainRoster lets a live team captain find an eligible board
// participant to invite. It requires the caller to be a joined captain of a
// TEAM board whose window is open and whose own group access is live; no staff
// permission is required or granted. Matching covers username, email and UUID,
// but the response never contains an email address.
func (s *AccessService) SearchCaptainRoster(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, q ListQuery) (CaptainRosterPage, error) {
	if err := s.ready(); err != nil {
		return CaptainRosterPage{}, err
	}
	if boardID == uuid.Nil {
		return CaptainRosterPage{}, ErrBoardNotFound
	}
	user, err := loadLiveUser(ctx, s.db.Pool, principal.UserID)
	if err != nil {
		return CaptainRosterPage{}, err
	}
	board, err := loadBoard(ctx, s.db.Pool, boardID)
	if err != nil {
		return CaptainRosterPage{}, err
	}
	if board.ParticipationMode != "TEAM" {
		return CaptainRosterPage{}, ErrInvalidSubject
	}
	open, err := boardWindowOpen(ctx, s.db.Pool, board)
	if err != nil {
		return CaptainRosterPage{}, err
	}
	if !open {
		return CaptainRosterPage{}, ErrForbidden
	}
	joined, err := isBoardParticipant(ctx, s.db.Pool, boardID, user.ID)
	if err != nil {
		return CaptainRosterPage{}, err
	}
	if !joined {
		return CaptainRosterPage{}, ErrForbidden
	}
	groupAllowed, err := participantGroupAllowed(ctx, s.db.Pool, board, user.ID)
	if err != nil {
		return CaptainRosterPage{}, err
	}
	if !groupAllowed {
		return CaptainRosterPage{}, ErrForbidden
	}
	captain, err := isTeamCaptain(ctx, s.db.Pool, boardID, user.ID)
	if err != nil {
		return CaptainRosterPage{}, err
	}
	if !captain {
		return CaptainRosterPage{}, ErrForbidden
	}
	if strings.TrimSpace(q.Query) == "" {
		return CaptainRosterPage{Items: []CaptainRosterEntry{}}, nil
	}

	limit, offset := clampPage(q.Limit, q.Offset)
	if limit > 20 {
		limit = 20
	}
	query := strings.TrimSpace(q.Query)
	escaped := escapeLike(query)
	var exact uuid.UUID
	if parsed, err := uuid.Parse(query); err == nil {
		exact = parsed
	}
	prefix, _ := uuidPrefix(query)

	predicate := `
		WHERE bp.board_id = $1
		  AND NOT EXISTS (
			SELECT 1 FROM team_members tm WHERE tm.board_id = bp.board_id AND tm.user_id = bp.user_id
		  )
		  AND (
			u.id = $2
			OR ($3 <> '' AND replace(u.id::text, '-', '') LIKE $3 || '%')
			OR u.username ILIKE '%' || $4 || '%' ESCAPE '\'
			OR u.email ILIKE '%' || $4 || '%' ESCAPE '\'
		  )
	`
	rows, err := s.db.Pool.Query(ctx, `
		SELECT u.id, u.username
		FROM board_participants bp JOIN users u ON u.id = bp.user_id
	`+predicate+`
		ORDER BY (u.id = $2) DESC, u.username, u.id
		LIMIT $5 OFFSET $6
	`, boardID, exact, prefix, escaped, limit, offset)
	if err != nil {
		return CaptainRosterPage{}, fmt.Errorf("access: search captain roster: %w", err)
	}
	defer rows.Close()
	page := CaptainRosterPage{Items: make([]CaptainRosterEntry, 0)}
	for rows.Next() {
		var entry CaptainRosterEntry
		if err := rows.Scan(&entry.UserID, &entry.Username); err != nil {
			return CaptainRosterPage{}, fmt.Errorf("access: scan captain roster row: %w", err)
		}
		page.Items = append(page.Items, entry)
	}
	if err := rows.Err(); err != nil {
		return CaptainRosterPage{}, fmt.Errorf("access: iterate captain roster: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM board_participants bp JOIN users u ON u.id = bp.user_id
	`+predicate, boardID, exact, prefix, escaped).Scan(&page.Total); err != nil {
		return CaptainRosterPage{}, fmt.Errorf("access: count captain roster: %w", err)
	}
	if err := s.auditBoardSearch(ctx, principal.UserID, boardID, "user_search.captain_roster"); err != nil {
		return CaptainRosterPage{}, err
	}
	return page, nil
}

const userSearchPredicate = `
	WHERE (
		u.id = $1
		OR ($2 <> '' AND replace(u.id::text, '-', '') LIKE $2 || '%')
		OR u.username ILIKE '%' || $3 || '%' ESCAPE '\'
		OR u.email ILIKE '%' || $3 || '%' ESCAPE '\'
	)
`

func (s *AccessService) searchUsers(ctx context.Context, q rowsQuerier, in ListQuery) (UserDirectoryPage, error) {
	limit, offset := clampPage(in.Limit, in.Offset)
	query := strings.TrimSpace(in.Query)
	escaped := escapeLike(query)
	var exact uuid.UUID
	if parsed, err := uuid.Parse(query); err == nil {
		exact = parsed
	}
	prefix, _ := uuidPrefix(query)

	rows, err := q.Query(ctx, `
		SELECT u.id, u.username, u.email
		FROM users u
	`+userSearchPredicate+`
		ORDER BY (u.id = $1) DESC, u.username, u.id
		LIMIT $4 OFFSET $5
	`, exact, prefix, escaped, limit, offset)
	if err != nil {
		return UserDirectoryPage{}, fmt.Errorf("access: search users: %w", err)
	}
	defer rows.Close()

	page := UserDirectoryPage{Items: make([]UserDirectoryEntry, 0)}
	for rows.Next() {
		var entry UserDirectoryEntry
		if err := rows.Scan(&entry.UserID, &entry.Username, &entry.Email); err != nil {
			return UserDirectoryPage{}, fmt.Errorf("access: scan user search row: %w", err)
		}
		page.Items = append(page.Items, entry)
	}
	if err := rows.Err(); err != nil {
		return UserDirectoryPage{}, fmt.Errorf("access: iterate user search: %w", err)
	}
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM users u
	`+userSearchPredicate, exact, prefix, escaped).Scan(&page.Total); err != nil {
		return UserDirectoryPage{}, fmt.Errorf("access: count user search: %w", err)
	}
	return page, nil
}

// auditBoardSearch records a bounded board-scoped directory read. It never
// stores the search term, so an email probe is not re-readable from the audit.
func (s *AccessService) auditBoardSearch(ctx context.Context, actorID, boardID uuid.UUID, action string) error {
	if _, err := s.db.Pool.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, action, resource_type, resource_id, details)
		VALUES ($1, $2, $3, 'board', $2, $4)
	`, actorID, boardID, action, []byte(`{"scope":"board"}`)); err != nil {
		return fmt.Errorf("access: audit board search: %w", err)
	}
	return nil
}

var (
	ErrAccessGroupMemberExists   = errors.New("access: group member already exists")
	ErrAccessGroupMemberNotFound = errors.New("access: group member not found")
)

// GroupMember is one access-group membership row.
type GroupMember struct {
	UserID   uuid.UUID
	Username string
	Email    string
}

// GroupMemberPage is a bounded group membership page.
type GroupMemberPage struct {
	Items []GroupMember
	Total int
}

// ListAccessGroupMembers returns the membership of one group to a global admin.
func (s *AccessService) ListAccessGroupMembers(ctx context.Context, principal authorization.Principal, groupID uuid.UUID, q ListQuery) (GroupMemberPage, error) {
	if err := s.ready(); err != nil {
		return GroupMemberPage{}, err
	}
	if _, err := s.requireGlobalAdmin(ctx, s.db.Pool, principal, false); err != nil {
		return GroupMemberPage{}, err
	}
	if err := s.ensureGroupExists(ctx, s.db.Pool, groupID); err != nil {
		return GroupMemberPage{}, err
	}
	limit, offset := clampPage(q.Limit, q.Offset)
	escaped := escapeLike(strings.TrimSpace(q.Query))
	rows, err := s.db.Pool.Query(ctx, `
		SELECT u.id, u.username, u.email
		FROM access_group_members agm
		JOIN users u ON u.id = agm.user_id
		WHERE agm.group_id = $1
		  AND ($2 = '' OR u.username ILIKE '%' || $2 || '%' ESCAPE '\' OR u.email ILIKE '%' || $2 || '%' ESCAPE '\')
		ORDER BY u.username, u.id
		LIMIT $3 OFFSET $4
	`, groupID, escaped, limit, offset)
	if err != nil {
		return GroupMemberPage{}, fmt.Errorf("access: list group members: %w", err)
	}
	defer rows.Close()
	page := GroupMemberPage{Items: make([]GroupMember, 0)}
	for rows.Next() {
		var member GroupMember
		if err := rows.Scan(&member.UserID, &member.Username, &member.Email); err != nil {
			return GroupMemberPage{}, fmt.Errorf("access: scan group member: %w", err)
		}
		page.Items = append(page.Items, member)
	}
	if err := rows.Err(); err != nil {
		return GroupMemberPage{}, fmt.Errorf("access: iterate group members: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM access_group_members agm
		JOIN users u ON u.id = agm.user_id
		WHERE agm.group_id = $1
		  AND ($2 = '' OR u.username ILIKE '%' || $2 || '%' ESCAPE '\' OR u.email ILIKE '%' || $2 || '%' ESCAPE '\')
	`, groupID, escaped).Scan(&page.Total); err != nil {
		return GroupMemberPage{}, fmt.Errorf("access: count group members: %w", err)
	}
	return page, nil
}

// AddAccessGroupMember inserts one membership with a transactional audit row.
func (s *AccessService) AddAccessGroupMember(ctx context.Context, principal authorization.Principal, groupID, userID uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("access: begin group member add: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	actor, err := s.requireGlobalAdmin(ctx, tx, principal, true)
	if err != nil {
		return err
	}
	if err := s.ensureGroupExists(ctx, tx, groupID); err != nil {
		return err
	}
	var username string
	if err := tx.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, userID).Scan(&username); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("access: load group member user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1, $2)
	`, groupID, userID); err != nil {
		if isUniqueViolation(err) {
			return ErrAccessGroupMemberExists
		}
		return fmt.Errorf("access: add group member: %w", err)
	}
	if err := writeAccessAudit(ctx, tx, actor.ID, nil, nil, "access_group.member.add", "access_group", &groupID, map[string]any{
		"userUuid": userID.String(),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("access: commit group member add: %w", err)
	}
	return nil
}

// RemoveAccessGroupMember deletes one membership with a transactional audit row.
func (s *AccessService) RemoveAccessGroupMember(ctx context.Context, principal authorization.Principal, groupID, userID uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("access: begin group member remove: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	actor, err := s.requireGlobalAdmin(ctx, tx, principal, true)
	if err != nil {
		return err
	}
	if err := s.ensureGroupExists(ctx, tx, groupID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM access_group_members WHERE group_id = $1 AND user_id = $2
	`, groupID, userID)
	if err != nil {
		return fmt.Errorf("access: remove group member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAccessGroupMemberNotFound
	}
	if err := writeAccessAudit(ctx, tx, actor.ID, nil, nil, "access_group.member.remove", "access_group", &groupID, map[string]any{
		"userUuid": userID.String(),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("access: commit group member remove: %w", err)
	}
	return nil
}

func (s *AccessService) ensureGroupExists(ctx context.Context, q rowQuerier, groupID uuid.UUID) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM access_groups WHERE id = $1)`, groupID).Scan(&exists); err != nil {
		return fmt.Errorf("access: check access group: %w", err)
	}
	if !exists {
		return ErrAccessGroupNotFound
	}
	return nil
}
