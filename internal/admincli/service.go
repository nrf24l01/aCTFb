package admincli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/crypto"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// Keep this exact key in sync with internal/access. The CLI and HTTP access
// service must serialize last-enabled-admin checks across both entrypoints.
const adminUserAdvisoryLockKey = "p1-admin-users"

const cliOperator = "admin-cli"

type Service struct {
	db     *postgres.DB
	argon2 config.Argon2idConfig
}

func NewService(db *postgres.DB, argon2 config.Argon2idConfig) *Service {
	return &Service{db: db, argon2: normalizeArgon2(argon2)}
}

func normalizeArgon2(cfg config.Argon2idConfig) config.Argon2idConfig {
	if cfg.Time == 0 {
		cfg.Time = config.DefaultArgon2Time
	}
	if cfg.Memory == 0 {
		cfg.Memory = config.DefaultArgon2Memory
	}
	if cfg.Threads == 0 {
		cfg.Threads = config.DefaultArgon2Threads
	}
	if cfg.KeyLen == 0 {
		cfg.KeyLen = config.DefaultArgon2KeyLen
	}
	return cfg
}

// CheckSchema verifies that the operator did not point the CLI at an
// uninitialized or pre-CLI database. It never applies migrations.
func (s *Service) CheckSchema(ctx context.Context) error {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return fmt.Errorf("%w: database is not configured", ErrSchemaMissing)
	}
	var users, audit pgtype.Text
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT to_regclass('users')::text, to_regclass('audit_events')::text
	`).Scan(&users, &audit); err != nil {
		return fmt.Errorf("check database schema: %w", err)
	}
	if !users.Valid || !audit.Valid {
		return ErrSchemaMissing
	}
	var nullable string
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'audit_events'
		  AND column_name = 'actor_user_id'
	`).Scan(&nullable); err != nil {
		return fmt.Errorf("%w: cannot verify audit_events.actor_user_id; run backend migrations", ErrSchemaMissing)
	}
	if nullable != "YES" {
		return fmt.Errorf("%w: audit_events.actor_user_id must allow NULL for operator CLI audit; run backend migrations", ErrSchemaMissing)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*AdminUser, error) {
	username := strings.TrimSpace(in.Username)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !access.ValidateAdminUsername(username) || !access.ValidateAdminEmail(email) {
		return nil, fmt.Errorf("%w: username or email does not satisfy the admin user policy", access.ErrInvalidInput)
	}
	if err := validateNewPassword(in.Password); err != nil {
		return nil, err
	}
	hash, err := crypto.Hash(in.Password, s.argon2)
	if err != nil {
		return nil, fmt.Errorf("hash admin password: %w", err)
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin admin create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAdminUsers(ctx, tx); err != nil {
		return nil, err
	}
	roles := []string{"user", "admin"}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO users (username, email, password, email_verified, disabled, roles)
		VALUES ($1, $2, $3, TRUE, FALSE, $4)
		RETURNING id
	`, username, email, hash, roles).Scan(&id)
	if err != nil {
		return nil, mapUserConflict(err)
	}
	if err := writeCLIAudit(ctx, tx, "admin.user.create", "user", &id, map[string]any{
		"username": username,
		"email":    email,
		"roles":    roles,
	}); err != nil {
		return nil, err
	}
	user, err := loadCLIUser(ctx, tx, id, false)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit admin create: %w", err)
	}
	return user, nil
}

func (s *Service) List(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, username, email, COALESCE(roles, ARRAY['user']::text[]), disabled
		FROM users
		ORDER BY username, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]AdminUser, 0)
	for rows.Next() {
		user, err := scanCLIUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (s *Service) Promote(ctx context.Context, target Target) (*AdminUser, bool, error) {
	return s.changeAdminRole(ctx, target, true)
}

func (s *Service) Demote(ctx context.Context, target Target) (*AdminUser, bool, error) {
	return s.changeAdminRole(ctx, target, false)
}

func (s *Service) changeAdminRole(ctx context.Context, target Target, promote bool) (*AdminUser, bool, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin admin role change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAdminUsers(ctx, tx); err != nil {
		return nil, false, err
	}
	user, err := loadTargetForUpdate(ctx, tx, target)
	if err != nil {
		return nil, false, err
	}
	roles, err := access.NormalizeAdminRoles(user.Roles)
	if err != nil {
		return nil, false, err
	}
	hasAdmin := containsExact(roles, "admin")

	if promote {
		if user.Disabled {
			return nil, false, ErrTargetDisabled
		}
		if hasAdmin {
			user.Roles = roles
			return user, false, nil
		}
		roles = append(roles, "admin")
		if roles, err = access.NormalizeAdminRoles(roles); err != nil {
			return nil, false, err
		}
	} else {
		if !hasAdmin {
			user.Roles = roles
			return user, false, nil
		}
		if !user.Disabled {
			var otherEnabledAdmins int
			if err := tx.QueryRow(ctx, `
				SELECT count(*)::int FROM users
				WHERE id <> $1 AND disabled = FALSE AND 'admin' = ANY(COALESCE(roles, ARRAY['user']::text[]))
			`, user.ID).Scan(&otherEnabledAdmins); err != nil {
				return nil, false, fmt.Errorf("count other enabled admins: %w", err)
			}
			if otherEnabledAdmins == 0 {
				return nil, false, access.ErrLastAdmin
			}
		}
		remaining := make([]string, 0, len(roles))
		for _, role := range roles {
			if role != "admin" {
				remaining = append(remaining, role)
			}
		}
		if len(remaining) == 0 {
			remaining = []string{"user"}
		}
		if roles, err = access.NormalizeAdminRoles(remaining); err != nil {
			return nil, false, err
		}
	}

	action := "admin.user.demote"
	if promote {
		action = "admin.user.promote"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET roles = $1, auth_version = auth_version + 1 WHERE id = $2
	`, roles, user.ID); err != nil {
		return nil, false, fmt.Errorf("update admin user roles: %w", err)
	}
	if err := revokeAdminUserSessions(ctx, tx, user.ID); err != nil {
		return nil, false, err
	}
	if err := writeCLIAudit(ctx, tx, action, "user", &user.ID, map[string]any{
		"username":      user.Username,
		"email":         user.Email,
		"previousRoles": user.Roles,
		"roles":         roles,
	}); err != nil {
		return nil, false, err
	}
	updated, err := loadCLIUser(ctx, tx, user.ID, false)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit admin role change: %w", err)
	}
	return updated, true, nil
}

func (s *Service) ResetPassword(ctx context.Context, target Target, password string) (*AdminUser, error) {
	if err := validateNewPassword(password); err != nil {
		return nil, err
	}
	hash, err := crypto.Hash(password, s.argon2)
	if err != nil {
		return nil, fmt.Errorf("hash admin password: %w", err)
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin admin password reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAdminUsers(ctx, tx); err != nil {
		return nil, err
	}
	userID, err := resolveTargetID(ctx, tx, target)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "auth-user:"+userID.String()); err != nil {
		return nil, fmt.Errorf("lock auth user: %w", err)
	}
	user, err := loadCLIUser(ctx, tx, userID, true)
	if err != nil {
		return nil, err
	}
	if target.Username != "" && user.Username != strings.TrimSpace(target.Username) {
		return nil, access.ErrUserNotFound
	}
	roles, err := access.NormalizeAdminRoles(user.Roles)
	if err != nil {
		return nil, err
	}
	if !containsExact(roles, "admin") {
		return nil, fmt.Errorf("%w: %s (%s) is not an admin; reset-password only manages admin accounts", ErrNotAdmin, user.Username, user.ID)
	}
	user.Roles = roles
	if _, err := tx.Exec(ctx, `
		UPDATE users SET password = $1, auth_version = auth_version + 1 WHERE id = $2
	`, hash, user.ID); err != nil {
		return nil, fmt.Errorf("update admin password: %w", err)
	}
	if err := revokeAdminUserSessions(ctx, tx, user.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_password_resets SET used_at = NOW()
		WHERE user_id = $1 AND used_at IS NULL AND expires_at > NOW()
	`, user.ID); err != nil {
		return nil, fmt.Errorf("invalidate outstanding password resets: %w", err)
	}
	if err := writeCLIAudit(ctx, tx, "admin.user.password_reset", "user", &user.ID, map[string]any{
		"username": user.Username,
		"email":    user.Email,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit admin password reset: %w", err)
	}
	return user, nil
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type rowScanner interface {
	Scan(dest ...any) error
}

func lockAdminUsers(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, adminUserAdvisoryLockKey); err != nil {
		return fmt.Errorf("lock admin users: %w", err)
	}
	return nil
}

func resolveTargetID(ctx context.Context, q queryRower, target Target) (uuid.UUID, error) {
	if err := validateTarget(target); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	var err error
	if target.ID != uuid.Nil {
		err = q.QueryRow(ctx, `SELECT id FROM users WHERE id = $1`, target.ID).Scan(&id)
	} else {
		err = q.QueryRow(ctx, `SELECT id FROM users WHERE username = $1`, target.Username).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, access.ErrUserNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve admin user: %w", err)
	}
	return id, nil
}

func loadTargetForUpdate(ctx context.Context, tx pgx.Tx, target Target) (*AdminUser, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	var row pgx.Row
	if target.ID != uuid.Nil {
		row = tx.QueryRow(ctx, `
			SELECT id, username, email, COALESCE(roles, ARRAY['user']::text[]), disabled
			FROM users WHERE id = $1 FOR UPDATE
		`, target.ID)
	} else {
		row = tx.QueryRow(ctx, `
			SELECT id, username, email, COALESCE(roles, ARRAY['user']::text[]), disabled
			FROM users WHERE username = $1 FOR UPDATE
		`, target.Username)
	}
	user, err := scanCLIUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, access.ErrUserNotFound
	}
	return user, err
}

func loadCLIUser(ctx context.Context, q queryRower, id uuid.UUID, forUpdate bool) (*AdminUser, error) {
	query := `
		SELECT id, username, email, COALESCE(roles, ARRAY['user']::text[]), disabled
		FROM users WHERE id = $1`
	if forUpdate {
		query += " FOR UPDATE"
	}
	user, err := scanCLIUser(q.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, access.ErrUserNotFound
	}
	return user, err
}

func scanCLIUser(row rowScanner) (*AdminUser, error) {
	var user AdminUser
	if err := row.Scan(&user.ID, &user.Username, &user.Email, &user.Roles, &user.Disabled); err != nil {
		return nil, err
	}
	if len(user.Roles) == 0 {
		user.Roles = []string{"user"}
	}
	sort.Strings(user.Roles)
	return &user, nil
}

func validateTarget(target Target) error {
	if (target.ID == uuid.Nil) == (strings.TrimSpace(target.Username) == "") {
		return fmt.Errorf("%w: exactly one of --username or --id is required", access.ErrInvalidInput)
	}
	return nil
}

func revokeAdminUserSessions(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = NOW(), revoked_reason = 'admin_update'
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID); err != nil {
		return fmt.Errorf("revoke admin user sessions: %w", err)
	}
	return nil
}

func writeCLIAudit(
	ctx context.Context,
	tx pgx.Tx,
	action, resourceType string,
	resourceID *uuid.UUID,
	details map[string]any,
) error {
	payload := make(map[string]any, len(details)+2)
	for key, value := range details {
		payload[key] = value
	}
	payload["source"] = "cli"
	payload["operator"] = cliOperator
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode admin CLI audit: %w", err)
	}
	// No user row is fabricated for the external operator. Migration 017 makes
	// actor_user_id nullable; authenticated writes keep their existing actors.
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, action, resource_type, resource_id, details)
		VALUES (NULL, $1, $2, $3, $4)
	`, action, resourceType, resourceID, encoded); err != nil {
		return fmt.Errorf("write admin CLI audit: %w", err)
	}
	return nil
}

func mapUserConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
		switch pgErr.ConstraintName {
		case "users_username_key", "users_username_key1":
			return access.ErrUsernameExists
		case "users_email_key", "users_email_key1":
			return access.ErrEmailExists
		default:
			return access.ErrUserExists
		}
	}
	return fmt.Errorf("create admin user: %w", err)
}

func containsExact(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
