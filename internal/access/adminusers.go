package access

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/crypto"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

const adminUserAdvisoryLockKey = "p1-admin-users"

func (s *AccessService) ListUsers(ctx context.Context, principal authorization.Principal, q ListQuery) (AdminUserPage, error) {
	if err := s.ready(); err != nil {
		return AdminUserPage{}, err
	}
	if _, err := s.requireGlobalAdmin(ctx, s.db.Pool, principal, false); err != nil {
		return AdminUserPage{}, err
	}
	limit, offset := clampPage(q.Limit, q.Offset)
	query := strings.TrimSpace(q.Query)
	rows, err := s.db.Pool.Query(ctx, adminUserSelect+`
		WHERE ($1 = '' OR u.username ILIKE '%' || $1 || '%' OR u.email ILIKE '%' || $1 || '%')
		GROUP BY u.id
		ORDER BY u.username, u.id
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return AdminUserPage{}, fmt.Errorf("access: list admin users: %w", err)
	}
	defer rows.Close()
	page := AdminUserPage{Items: make([]AdminUser, 0)}
	for rows.Next() {
		user, err := scanAdminUser(rows)
		if err != nil {
			return AdminUserPage{}, err
		}
		page.Items = append(page.Items, user)
	}
	if err := rows.Err(); err != nil {
		return AdminUserPage{}, fmt.Errorf("access: iterate admin users: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM users u
		WHERE ($1 = '' OR u.username ILIKE '%' || $1 || '%' OR u.email ILIKE '%' || $1 || '%')
	`, query).Scan(&page.Total); err != nil {
		return AdminUserPage{}, fmt.Errorf("access: count admin users: %w", err)
	}
	return page, nil
}

func (s *AccessService) CreateUser(ctx context.Context, principal authorization.Principal, in AdminUserInput) (*AdminUser, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := s.requireGlobalAdmin(ctx, s.db.Pool, principal, false); err != nil {
		return nil, err
	}
	email := strings.TrimSpace(in.Email)
	username := strings.TrimSpace(in.Username)
	if !validEmail(email) || !validUsername(username) || !validPassword(in.Password) {
		return nil, ErrInvalidInput
	}
	roles, err := normalizeAdminRoles(in.Roles)
	if err != nil {
		return nil, err
	}
	groupIDs := dedupeUUIDs(in.AccessGroupUUIDs)

	// Password hashing is intentionally outside the SQL transaction; it is CPU
	// work and does not depend on database state.
	hash, err := crypto.Hash(in.Password, s.argon2)
	if err != nil {
		return nil, fmt.Errorf("access: hash admin user password: %w", err)
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: begin admin user create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, adminUserAdvisoryLockKey); err != nil {
		return nil, fmt.Errorf("access: lock admin users: %w", err)
	}
	actor, err := s.requireGlobalAdmin(ctx, tx, principal, true)
	if err != nil {
		return nil, err
	}
	if err := validateAccessGroups(ctx, tx, groupIDs); err != nil {
		return nil, err
	}

	var userID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO users (username, email, password, email_verified, disabled, roles)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, username, email, hash, in.EmailVerified, in.Disabled, roles).Scan(&userID)
	if err != nil {
		return nil, mapAdminUserConflict(err)
	}
	if err := replaceAccessGroups(ctx, tx, userID, groupIDs); err != nil {
		return nil, err
	}
	if err := writeAccessAudit(ctx, tx, actor.ID, nil, nil, "admin.user.create", "user", &userID, map[string]any{
		"username": username,
		"email":    email,
		"roles":    roles,
		"groups":   uuidStrings(groupIDs),
	}); err != nil {
		return nil, err
	}
	user, err := loadAdminUser(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("access: commit admin user create: %w", err)
	}
	return user, nil
}

func (s *AccessService) GetUser(ctx context.Context, principal authorization.Principal, id uuid.UUID) (*AdminUser, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := s.requireGlobalAdmin(ctx, s.db.Pool, principal, false); err != nil {
		return nil, err
	}
	return loadAdminUser(ctx, s.db.Pool, id)
}

func (s *AccessService) UpdateUser(ctx context.Context, principal authorization.Principal, id uuid.UUID, in AdminUserPatch) (*AdminUser, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := s.requireGlobalAdmin(ctx, s.db.Pool, principal, false); err != nil {
		return nil, err
	}
	if in.Email != nil {
		email := strings.TrimSpace(*in.Email)
		if !validEmail(email) {
			return nil, ErrInvalidInput
		}
		in.Email = &email
	}
	if in.Username != nil {
		username := strings.TrimSpace(*in.Username)
		if !validUsername(username) {
			return nil, ErrInvalidInput
		}
		in.Username = &username
	}
	var normalizedRoles []string
	if in.Roles != nil {
		roles, err := normalizeAdminRoles(*in.Roles)
		if err != nil {
			return nil, err
		}
		normalizedRoles = roles
	}
	var groupIDs []uuid.UUID
	replaceGroups := in.AccessGroupUUIDs != nil
	if replaceGroups {
		groupIDs = dedupeUUIDs(*in.AccessGroupUUIDs)
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: begin admin user update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, adminUserAdvisoryLockKey); err != nil {
		return nil, fmt.Errorf("access: lock admin users: %w", err)
	}
	actor, err := s.requireGlobalAdmin(ctx, tx, principal, true)
	if err != nil {
		return nil, err
	}
	var currentRoles []string
	var currentDisabled bool
	var currentEmail, currentUsername string
	if err := tx.QueryRow(ctx, `
		SELECT username, email, COALESCE(roles, ARRAY['user']::text[]), disabled
		FROM users WHERE id = $1 FOR UPDATE
	`, id).Scan(&currentUsername, &currentEmail, &currentRoles, &currentDisabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("access: lock admin user: %w", err)
	}
	if replaceGroups {
		if err := validateAccessGroups(ctx, tx, groupIDs); err != nil {
			return nil, err
		}
	}

	newRoles := currentRoles
	if in.Roles != nil {
		newRoles = normalizedRoles
	}
	newDisabled := currentDisabled
	if in.Disabled != nil {
		newDisabled = *in.Disabled
	}
	wasEnabledAdmin := !currentDisabled && containsRole(currentRoles, "admin")
	willBeEnabledAdmin := newDisabled == false && containsRole(newRoles, "admin")
	if wasEnabledAdmin && !willBeEnabledAdmin {
		var others int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)::int FROM users
			WHERE id <> $1 AND disabled = FALSE AND 'admin' = ANY(COALESCE(roles, ARRAY['user']::text[]))
		`, id).Scan(&others); err != nil {
			return nil, fmt.Errorf("access: count other admins: %w", err)
		}
		if others == 0 {
			return nil, ErrLastAdmin
		}
	}

	normalizedCurrentRoles, _ := normalizeAdminRoles(currentRoles)
	authChanged := !equalStrings(normalizedCurrentRoles, newRoles) || currentDisabled != newDisabled
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET email = COALESCE($1, email),
		    username = COALESCE($2, username),
		    email_verified = COALESCE($3, email_verified),
		    disabled = COALESCE($4, disabled),
		    roles = $5,
		    auth_version = auth_version + CASE WHEN $6 THEN 1 ELSE 0 END
		WHERE id = $7
	`, in.Email, in.Username, in.EmailVerified, in.Disabled, newRoles, authChanged, id); err != nil {
		return nil, mapAdminUserConflict(err)
	}
	if authChanged {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = NOW(), revoked_reason = 'admin_update'
			WHERE user_id = $1 AND revoked_at IS NULL
		`, id); err != nil {
			return nil, fmt.Errorf("access: revoke admin user sessions: %w", err)
		}
	}
	if replaceGroups {
		if err := replaceAccessGroups(ctx, tx, id, groupIDs); err != nil {
			return nil, err
		}
	}
	changes := map[string]any{}
	if in.Email != nil {
		changes["email"] = *in.Email
	}
	if in.Username != nil {
		changes["username"] = *in.Username
	}
	if in.Disabled != nil {
		changes["disabled"] = *in.Disabled
	}
	if in.Roles != nil {
		changes["roles"] = newRoles
	}
	if replaceGroups {
		changes["groups"] = uuidStrings(groupIDs)
	}
	if err := writeAccessAudit(ctx, tx, actor.ID, nil, nil, "admin.user.update", "user", &id, changes); err != nil {
		return nil, err
	}
	user, err := loadAdminUser(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("access: commit admin user update: %w", err)
	}
	return user, nil
}

const adminUserSelect = `
	SELECT u.id, u.email, u.username, COALESCE(u.roles, ARRAY['user']::text[]),
	       u.email_verified, u.disabled, u.created_at,
	       COALESCE(array_agg(g.id::text ORDER BY g.name, g.id) FILTER (WHERE g.id IS NOT NULL), '{}'),
	       COALESCE(array_agg(g.name ORDER BY g.name, g.id) FILTER (WHERE g.id IS NOT NULL), '{}')
	FROM users u
	LEFT JOIN access_group_members agm ON agm.user_id = u.id
	LEFT JOIN access_groups g ON g.id = agm.group_id
`

func loadAdminUser(ctx context.Context, q rowsQuerier, id uuid.UUID) (*AdminUser, error) {
	row := q.QueryRow(ctx, adminUserSelect+`
		WHERE u.id = $1
		GROUP BY u.id
	`, id)
	user, err := scanAdminUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	return &user, err
}

func scanAdminUser(row pgx.Row) (AdminUser, error) {
	var user AdminUser
	var groupIDs, groupNames []string
	err := row.Scan(&user.ID, &user.Email, &user.Username, &user.Roles, &user.EmailVerified, &user.Disabled, &user.CreatedAt, &groupIDs, &groupNames)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUser{}, ErrUserNotFound
	}
	if err != nil {
		return AdminUser{}, fmt.Errorf("access: scan admin user: %w", err)
	}
	user.UpdatedAt = user.CreatedAt
	user.AccessGroups = make([]Group, 0, len(groupIDs))
	for index, rawID := range groupIDs {
		id, parseErr := uuid.Parse(rawID)
		if parseErr != nil {
			continue
		}
		name := ""
		if index < len(groupNames) {
			name = groupNames[index]
		}
		user.AccessGroups = append(user.AccessGroups, Group{ID: id, Name: name})
	}
	sort.Slice(user.AccessGroups, func(i, j int) bool { return user.AccessGroups[i].Name < user.AccessGroups[j].Name })
	return user, nil
}

func normalizeAdminRoles(roles []string) ([]string, error) {
	if len(roles) == 0 {
		roles = []string{"user"}
	}
	seen := make(map[string]struct{}, len(roles))
	result := make([]string, 0, len(roles))
	for _, raw := range roles {
		role := strings.ToLower(strings.TrimSpace(raw))
		if role != "user" && role != "admin" {
			return nil, ErrInvalidRole
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		result = append(result, role)
	}
	if len(result) == 0 {
		return nil, ErrInvalidRole
	}
	sort.Strings(result)
	return result, nil
}

func validUsername(username string) bool {
	return len(username) >= 3 && len(username) <= 32 && usernamePattern.MatchString(username)
}

func validPassword(password string) bool {
	return len(password) >= 8 && len(password) <= 128 && strings.IndexByte(password, 0) < 0
}

// ValidateAdminPassword exposes the shared admin password policy to the
// operator CLI. Spaces are intentional password material and are not trimmed.
func ValidateAdminPassword(password string) bool {
	return validPassword(password)
}

// ValidateAdminUsername exposes the shared admin username policy to the CLI.
func ValidateAdminUsername(username string) bool {
	return validUsername(username)
}

// ValidateAdminEmail exposes the shared admin email policy to the CLI.
func ValidateAdminEmail(email string) bool {
	return validEmail(email)
}

// NormalizeAdminRoles exposes the shared global-role policy to the CLI.
func NormalizeAdminRoles(roles []string) ([]string, error) {
	return normalizeAdminRoles(roles)
}

func validEmail(email string) bool {
	if email == "" || len(email) > 254 {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && strings.EqualFold(address.Address, email)
}

func mapAdminUserConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "users_username_key", "users_username_key1":
			return ErrUsernameExists
		case "users_email_key", "users_email_key1":
			return ErrEmailExists
		default:
			return ErrUserExists
		}
	}
	return fmt.Errorf("access: write admin user: %w", err)
}

func containsRole(roles []string, role string) bool {
	for _, candidate := range roles {
		if strings.EqualFold(candidate, role) {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateAccessGroups(ctx context.Context, q rowQuerier, groupIDs []uuid.UUID) error {
	if len(groupIDs) == 0 {
		return nil
	}
	var count int
	if err := q.QueryRow(ctx, `SELECT count(*)::int FROM access_groups WHERE id = ANY($1::uuid[])`, groupIDs).Scan(&count); err != nil {
		return fmt.Errorf("access: validate access groups: %w", err)
	}
	if count != len(groupIDs) {
		return ErrAccessGroupNotFound
	}
	return nil
}

func replaceAccessGroups(ctx context.Context, tx pgx.Tx, userID uuid.UUID, groupIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM access_group_members WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("access: clear user access groups: %w", err)
	}
	for _, groupID := range groupIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO access_group_members (group_id, user_id) VALUES ($1, $2)`, groupID, userID); err != nil {
			if isForeignKeyViolation(err) {
				return ErrAccessGroupNotFound
			}
			return fmt.Errorf("access: insert user access group: %w", err)
		}
	}
	return nil
}
