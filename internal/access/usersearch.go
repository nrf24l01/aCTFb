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

// SearchBoardUsers is the board-scoped picker. It requires live
// board.participants.manage on the exact board and returns only the minimal
// identity fields. It is paginated and never an anonymous directory.
func (s *AccessService) SearchBoardUsers(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, q ListQuery) (UserDirectoryPage, error) {
	if err := s.ready(); err != nil {
		return UserDirectoryPage{}, err
	}
	if boardID == uuid.Nil {
		return UserDirectoryPage{}, ErrBoardNotFound
	}
	if s.auth == nil {
		return UserDirectoryPage{}, ErrNoAuthorizer
	}
	decision, err := s.auth.Authorize(ctx, principal, authorization.PermissionBoardParticipantsManage, authorization.Scope{BoardID: boardID})
	if err != nil {
		return UserDirectoryPage{}, err
	}
	allowed := decision.Allowed
	if !allowed {
		staffDecision, staffErr := s.auth.Authorize(ctx, principal, authorization.PermissionBoardStaffManage, authorization.Scope{BoardID: boardID})
		if staffErr != nil {
			return UserDirectoryPage{}, staffErr
		}
		allowed = staffDecision.Allowed
	}
	if !allowed {
		return UserDirectoryPage{}, ErrForbidden
	}
	if strings.TrimSpace(q.Query) == "" {
		// Never allow an empty board-scoped query to dump the global directory.
		return UserDirectoryPage{Items: []UserDirectoryEntry{}}, nil
	}
	page, err := s.searchUsers(ctx, s.db.Pool, q)
	if err != nil {
		return UserDirectoryPage{}, err
	}
	// Board-scoped directory reads are sensitive. Record a bounded audit row
	// that never stores the search term's raw email value.
	if strings.TrimSpace(q.Query) != "" {
		if _, auditErr := s.db.Pool.Exec(ctx, `
			INSERT INTO audit_events (actor_user_id, board_id, action, resource_type, resource_id, details)
			VALUES ($1, $2, 'user_search.board', 'board', $2, $3)
		`, principal.UserID, boardID, []byte(`{"scope":"board"}`)); auditErr != nil {
			return UserDirectoryPage{}, fmt.Errorf("access: audit board user search: %w", auditErr)
		}
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
