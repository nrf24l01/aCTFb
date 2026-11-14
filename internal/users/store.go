package users

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

type createUserParams struct {
	Username      string
	Email         string
	PasswordHash  string
	EmailVerified bool
	EmailSecret   string
}

type emailState struct {
	Email    string
	Username string
	Secret   string
	Verified bool
	SentAt   time.Time
}

type rotationStatus int

const (
	rotationOK rotationStatus = iota
	rotationInvalid
	rotationExpired
	rotationReused
	rotationDisabled
)

const (
	sessionRevokedReasonRotated        = "rotated"
	sessionRevokedReasonLogout         = "logout"
	sessionRevokedReasonPasswordChange = "password_change"
	sessionRevokedReasonReuse          = "reuse"
)

type emailVerificationStatus int

const (
	emailVerificationOK emailVerificationStatus = iota
	emailVerificationInvalid
	emailVerificationExpired
	emailVerificationUsed
	emailVerificationAlreadyVerified
)

type passwordResetStatus int

const (
	passwordResetInvalid passwordResetStatus = iota
	passwordResetOK
	passwordResetExpired
	passwordResetUsed
)

type authStore interface {
	createUser(ctx context.Context, params createUserParams) (*User, error)
	createUserWithSession(ctx context.Context, params createUserParams, tokenHash []byte, expiresAt time.Time) (*User, error)
	getUserByUsername(ctx context.Context, username string) (*User, error)
	getUserByEmail(ctx context.Context, email string) (*User, error)
	getUserByID(ctx context.Context, id uuid.UUID) (*User, error)

	createSession(ctx context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error
	createSessionIfAuthVersion(ctx context.Context, userID uuid.UUID, expectedVersion int64, expectedPasswordHash string, tokenHash []byte, expiresAt time.Time) (bool, error)
	rotateRefreshSession(ctx context.Context, oldHash, newHash []byte, now, newExpiresAt time.Time) (*User, rotationStatus, error)
	revokeSession(ctx context.Context, tokenHash []byte) error
	revokeAllSessions(ctx context.Context, userID uuid.UUID) error

	markEmailSent(ctx context.Context, userID uuid.UUID, at time.Time) error
	getEmailState(ctx context.Context, userID uuid.UUID) (*emailState, error)
	markEmailVerified(ctx context.Context, userID uuid.UUID) (bool, error)
	createEmailVerification(ctx context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error
	consumeEmailVerification(ctx context.Context, tokenHash []byte, now time.Time) (uuid.UUID, emailVerificationStatus, error)
	revokeEmailVerification(ctx context.Context, tokenHash []byte) error

	updatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error
	changePasswordIfCurrent(ctx context.Context, userID uuid.UUID, expectedVersion int64, expectedPasswordHash, newPasswordHash string) (bool, error)
	updateUsername(ctx context.Context, userID uuid.UUID, username string) error

	createPasswordReset(ctx context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error
	consumePasswordReset(ctx context.Context, tokenHash []byte, now time.Time, newPasswordHash string) (uuid.UUID, passwordResetStatus, error)
	revokePasswordReset(ctx context.Context, tokenHash []byte) error
}

type postgresStore struct {
	db *postgres.DB
}

func newPostgresStore(db *postgres.DB) *postgresStore {
	return &postgresStore{db: db}
}

const userColumns = `id, username, email, password, email_verified, email_2fa_secret,
	email_send_at, disabled, COALESCE(roles, ARRAY['user']), created_at, auth_version`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var user User
	if err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Password,
		&user.EmailVerified,
		&user.Email2FASecret,
		&user.EmailSendAt,
		&user.Disabled,
		&user.Roles,
		&user.CreatedAt,
		&user.AuthVersion,
	); err != nil {
		return nil, err
	}
	if len(user.Roles) == 0 {
		user.Roles = []string{"user"}
	}
	return &user, nil
}

func (s *postgresStore) createUser(ctx context.Context, params createUserParams) (*User, error) {
	row := s.db.Pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password, email_verified, email_2fa_secret, email_send_at)
		VALUES ($1, $2, $3, $4, $5, to_timestamp(0))
		RETURNING `+userColumns,
		params.Username,
		params.Email,
		params.PasswordHash,
		params.EmailVerified,
		params.EmailSecret,
	)

	user, err := scanUser(row)
	if err != nil {
		return nil, mapUserConflict(err)
	}
	return user, nil
}

func (s *postgresStore) createUserWithSession(
	ctx context.Context,
	params createUserParams,
	tokenHash []byte,
	expiresAt time.Time,
) (*User, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	row := tx.QueryRow(ctx, `
		INSERT INTO users (username, email, password, email_verified, email_2fa_secret, email_send_at)
		VALUES ($1, $2, $3, $4, $5, to_timestamp(0))
		RETURNING `+userColumns,
		params.Username,
		params.Email,
		params.PasswordHash,
		params.EmailVerified,
		params.EmailSecret,
	)

	user, err := scanUser(row)
	if err != nil {
		return nil, mapUserConflict(err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_sessions (id, user_id, family_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), user.ID, uuid.New(), tokenHash, expiresAt,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *postgresStore) getUserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE username = $1`, username)
	user, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *postgresStore) getUserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE LOWER(email) = LOWER($1)`, email)
	user, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *postgresStore) getUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	row := s.db.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	user, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *postgresStore) createSession(ctx context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO auth_sessions (id, user_id, family_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), userID, uuid.New(), tokenHash, expiresAt,
	)
	return err
}

// createSessionIfAuthVersion guards login against a password mutation between
// password verification and session creation. The user advisory lock is the
// same scope used by ChangePassword and password reset.
func (s *postgresStore) createSessionIfAuthVersion(
	ctx context.Context,
	userID uuid.UUID,
	expectedVersion int64,
	expectedPasswordHash string,
	tokenHash []byte,
	expiresAt time.Time,
) (bool, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := lockAuthScope(ctx, tx, "auth-user:"+userID.String()); err != nil {
		return false, err
	}

	var (
		version      int64
		passwordHash string
	)
	err = tx.QueryRow(ctx, `
		SELECT auth_version, password FROM users WHERE id = $1 FOR UPDATE`, userID,
	).Scan(&version, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUserNotFound
	}
	if err != nil {
		return false, err
	}
	if version != expectedVersion || passwordHash != expectedPasswordHash {
		return false, nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_sessions (id, user_id, family_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), userID, uuid.New(), tokenHash, expiresAt,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *postgresStore) rotateRefreshSession(
	ctx context.Context,
	oldHash, newHash []byte,
	now, newExpiresAt time.Time,
) (*User, rotationStatus, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, rotationInvalid, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Resolve the family before taking locks so all rotations in a family use
	// the same lock order: user scope, then family scope. This serializes
	// rotation with password revocation and with reuse escalation.
	var (
		lockedFamilyID uuid.UUID
		lockedUserID   uuid.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT family_id, user_id FROM auth_sessions WHERE token_hash = $1`, oldHash,
	).Scan(&lockedFamilyID, &lockedUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rotationInvalid, nil
	}
	if err != nil {
		return nil, rotationInvalid, err
	}

	if err := lockAuthScope(ctx, tx, "auth-user:"+lockedUserID.String()); err != nil {
		return nil, rotationInvalid, err
	}
	if err := lockAuthScope(ctx, tx, "auth-family:"+lockedFamilyID.String()); err != nil {
		return nil, rotationInvalid, err
	}

	var (
		sessionID     uuid.UUID
		familyID      uuid.UUID
		expiresAt     time.Time
		revokedAt     pgtype.Timestamptz
		revokedReason pgtype.Text
		user          User
	)

	row := tx.QueryRow(ctx, `
		SELECT s.id, s.family_id, s.expires_at, s.revoked_at, s.revoked_reason,
		       u.id, u.username, u.email, u.password, u.email_verified,
		       u.email_2fa_secret, u.email_send_at, u.disabled,
		       COALESCE(u.roles, ARRAY['user']), u.created_at, u.auth_version
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		FOR UPDATE OF s`, oldHash)

	err = row.Scan(
		&sessionID,
		&familyID,
		&expiresAt,
		&revokedAt,
		&revokedReason,
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Password,
		&user.EmailVerified,
		&user.Email2FASecret,
		&user.EmailSendAt,
		&user.Disabled,
		&user.Roles,
		&user.CreatedAt,
		&user.AuthVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rotationInvalid, nil
	}
	if err != nil {
		return nil, rotationInvalid, err
	}
	if familyID != lockedFamilyID || user.ID != lockedUserID {
		return nil, rotationInvalid, nil
	}
	if len(user.Roles) == 0 {
		user.Roles = []string{"user"}
	}

	if revokedAt.Valid {
		if !now.Before(expiresAt) {
			return &user, rotationExpired, nil
		}
		reason := ""
		if revokedReason.Valid {
			reason = revokedReason.String
		}
		switch reason {
		case sessionRevokedReasonRotated:
			// Only a rotated, unexpired token escalates. Revoke the whole
			// family, but never other independent login families.
			if _, err := tx.Exec(ctx, `
				UPDATE auth_sessions
				SET revoked_at = $1, revoked_reason = $2
				WHERE family_id = $3 AND revoked_at IS NULL`,
				now, sessionRevokedReasonReuse, familyID,
			); err != nil {
				return nil, rotationInvalid, err
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, rotationInvalid, err
			}
			return &user, rotationReused, nil
		case sessionRevokedReasonReuse:
			return &user, rotationReused, nil
		default:
			// logout, password_change, legacy and unknown revoked rows are
			// terminal for this token and must not escalate.
			return &user, rotationInvalid, nil
		}
	}

	if !now.Before(expiresAt) {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = $1, revoked_reason = NULL
			WHERE id = $2 AND revoked_at IS NULL`, now, sessionID); err != nil {
			return nil, rotationInvalid, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, rotationInvalid, err
		}
		return &user, rotationExpired, nil
	}

	if user.Disabled {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = $1, revoked_reason = NULL
			WHERE id = $2 AND revoked_at IS NULL`, now, sessionID); err != nil {
			return nil, rotationInvalid, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, rotationInvalid, err
		}
		return &user, rotationDisabled, nil
	}

	tag, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $1, revoked_reason = $2
		WHERE id = $3 AND revoked_at IS NULL`,
		now, sessionRevokedReasonRotated, sessionID)
	if err != nil {
		return nil, rotationInvalid, err
	}
	if tag.RowsAffected() != 1 {
		return nil, rotationInvalid, nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_sessions (id, user_id, family_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), user.ID, familyID, newHash, newExpiresAt,
	); err != nil {
		return nil, rotationInvalid, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, rotationInvalid, err
	}
	return &user, rotationOK, nil
}

func lockAuthScope(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	return err
}

func (s *postgresStore) revokeSession(ctx context.Context, tokenHash []byte) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var (
		familyID uuid.UUID
		userID   uuid.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT family_id, user_id FROM auth_sessions WHERE token_hash = $1`, tokenHash,
	).Scan(&familyID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	// Match the user -> family advisory-lock order used by rotation and
	// password mutations. Logout must not take a family lock before the user
	// scope while a user-wide revocation is running.
	if err := lockAuthScope(ctx, tx, "auth-user:"+userID.String()); err != nil {
		return err
	}
	if err := lockAuthScope(ctx, tx, "auth-family:"+familyID.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = NOW(), revoked_reason = $1
		WHERE family_id = $2 AND revoked_at IS NULL`,
		sessionRevokedReasonLogout, familyID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *postgresStore) revokeAllSessions(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := lockAuthScope(ctx, tx, "auth-user:"+userID.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = NOW(), revoked_reason = $1
		WHERE user_id = $2 AND revoked_at IS NULL`,
		sessionRevokedReasonPasswordChange, userID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *postgresStore) markEmailSent(ctx context.Context, userID uuid.UUID, at time.Time) error {
	tag, err := s.db.Pool.Exec(ctx, `UPDATE users SET email_send_at = $1 WHERE id = $2`, at, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *postgresStore) getEmailState(ctx context.Context, userID uuid.UUID) (*emailState, error) {
	var state emailState
	err := s.db.Pool.QueryRow(ctx, `
		SELECT email, username, email_2fa_secret, email_verified, email_send_at
		FROM users WHERE id = $1`, userID,
	).Scan(&state.Email, &state.Username, &state.Secret, &state.Verified, &state.SentAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *postgresStore) markEmailVerified(ctx context.Context, userID uuid.UUID) (bool, error) {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE users SET email_verified = TRUE
		WHERE id = $1 AND email_verified = FALSE`, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *postgresStore) createEmailVerification(
	ctx context.Context,
	userID uuid.UUID,
	tokenHash []byte,
	expiresAt time.Time,
) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// A resend invalidates every still-active token for this user. Marking
	// them used preserves an honest "used" history rather than deleting rows.
	if _, err := tx.Exec(ctx, `
		UPDATE auth_email_verifications SET used_at = NOW()
		WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`,
		uuid.New(), userID, tokenHash, expiresAt,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *postgresStore) consumeEmailVerification(
	ctx context.Context,
	tokenHash []byte,
	now time.Time,
) (uuid.UUID, emailVerificationStatus, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, emailVerificationInvalid, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var (
		verificationID uuid.UUID
		userID         uuid.UUID
		expiresAt      time.Time
		usedAt         pgtype.Timestamptz
	)

	err = tx.QueryRow(ctx, `
		SELECT id, user_id, expires_at, used_at
		FROM auth_email_verifications
		WHERE token_hash = $1
		FOR UPDATE`, tokenHash,
	).Scan(&verificationID, &userID, &expiresAt, &usedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, emailVerificationInvalid, nil
	}
	if err != nil {
		return uuid.Nil, emailVerificationInvalid, err
	}

	// Token-row status is intentionally evaluated before the user's current
	// email_verified flag so a consumed token reports USED (410), not
	// EMAIL_ALREADY_VERIFIED (409).
	if usedAt.Valid {
		return userID, emailVerificationUsed, nil
	}
	if !now.Before(expiresAt) {
		return userID, emailVerificationExpired, nil
	}

	var emailVerified bool
	if err := tx.QueryRow(ctx, `
		SELECT email_verified FROM users WHERE id = $1 FOR UPDATE`, userID,
	).Scan(&emailVerified); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, emailVerificationInvalid, nil
		}
		return uuid.Nil, emailVerificationInvalid, err
	}

	if emailVerified {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_email_verifications SET used_at = $1
			WHERE id = $2 AND used_at IS NULL`, now, verificationID); err != nil {
			return uuid.Nil, emailVerificationInvalid, err
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, emailVerificationInvalid, err
		}
		return userID, emailVerificationAlreadyVerified, nil
	}

	tag, err := tx.Exec(ctx, `
		UPDATE auth_email_verifications SET used_at = $1
		WHERE id = $2 AND used_at IS NULL`, now, verificationID)
	if err != nil {
		return uuid.Nil, emailVerificationInvalid, err
	}
	if tag.RowsAffected() != 1 {
		return uuid.Nil, emailVerificationUsed, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET email_verified = TRUE WHERE id = $1`, userID); err != nil {
		return uuid.Nil, emailVerificationInvalid, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, emailVerificationInvalid, err
	}
	return userID, emailVerificationOK, nil
}

func (s *postgresStore) revokeEmailVerification(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE auth_email_verifications SET used_at = NOW()
		WHERE token_hash = $1 AND used_at IS NULL`, tokenHash)
	return err
}

func (s *postgresStore) updatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	tag, err := s.db.Pool.Exec(ctx, `UPDATE users SET password = $1 WHERE id = $2`, passwordHash, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *postgresStore) updateUsername(ctx context.Context, userID uuid.UUID, username string) error {
	tag, err := s.db.Pool.Exec(ctx, `UPDATE users SET username = $1 WHERE id = $2`, username, userID)
	if err != nil {
		return mapUserConflict(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *postgresStore) changePasswordIfCurrent(
	ctx context.Context,
	userID uuid.UUID,
	expectedVersion int64,
	expectedPasswordHash, newPasswordHash string,
) (bool, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := lockAuthScope(ctx, tx, "auth-user:"+userID.String()); err != nil {
		return false, err
	}

	var (
		version      int64
		passwordHash string
	)
	err = tx.QueryRow(ctx, `
		SELECT auth_version, password FROM users WHERE id = $1 FOR UPDATE`, userID,
	).Scan(&version, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUserNotFound
	}
	if err != nil {
		return false, err
	}
	if version != expectedVersion || passwordHash != expectedPasswordHash {
		return false, nil
	}

	now := time.Now()
	if _, err := tx.Exec(ctx, `
		UPDATE users SET password = $1, auth_version = auth_version + 1
		WHERE id = $2`, newPasswordHash, userID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $1, revoked_reason = $2
		WHERE user_id = $3 AND revoked_at IS NULL`,
		now, sessionRevokedReasonPasswordChange, userID,
	); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_password_resets SET used_at = $1
		WHERE user_id = $2 AND used_at IS NULL AND expires_at > $1`,
		now, userID,
	); err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *postgresStore) createPasswordReset(
	ctx context.Context,
	userID uuid.UUID,
	tokenHash []byte,
	expiresAt time.Time,
) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := lockAuthScope(ctx, tx, "auth-user:"+userID.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_password_resets SET used_at = NOW()
		WHERE user_id = $1 AND used_at IS NULL AND expires_at > NOW()`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`,
		uuid.New(), userID, tokenHash, expiresAt,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *postgresStore) consumePasswordReset(
	ctx context.Context,
	tokenHash []byte,
	now time.Time,
	newPasswordHash string,
) (uuid.UUID, passwordResetStatus, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var userID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT user_id FROM auth_password_resets WHERE token_hash = $1`, tokenHash,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, passwordResetInvalid, nil
	}
	if err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}

	if err := lockAuthScope(ctx, tx, "auth-user:"+userID.String()); err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}

	var (
		resetID   uuid.UUID
		expiresAt time.Time
		usedAt    pgtype.Timestamptz
	)
	err = tx.QueryRow(ctx, `
		SELECT id, expires_at, used_at FROM auth_password_resets
		WHERE token_hash = $1
		FOR UPDATE`, tokenHash,
	).Scan(&resetID, &expiresAt, &usedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, passwordResetInvalid, nil
	}
	if err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}
	if usedAt.Valid {
		return userID, passwordResetUsed, nil
	}
	if !now.Before(expiresAt) {
		return userID, passwordResetExpired, nil
	}

	// Disabled accounts must not consume a reset token issued before they were
	// disabled. Lock the live user row after the reset row (same user advisory
	// scope) and burn the token without mutating password/version/sessions.
	var disabled bool
	err = tx.QueryRow(ctx, `
		SELECT disabled FROM users WHERE id = $1 FOR UPDATE`, userID,
	).Scan(&disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, passwordResetInvalid, nil
	}
	if err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}
	if disabled {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_password_resets SET used_at = $1
			WHERE id = $2`, now, resetID); err != nil {
			return uuid.Nil, passwordResetInvalid, err
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, passwordResetInvalid, err
		}
		return userID, passwordResetInvalid, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET password = $1, auth_version = auth_version + 1
		WHERE id = $2`, newPasswordHash, userID); err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $1, revoked_reason = $2
		WHERE user_id = $3 AND revoked_at IS NULL`,
		now, sessionRevokedReasonPasswordChange, userID,
	); err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_password_resets SET used_at = $1
		WHERE user_id = $2 AND used_at IS NULL AND expires_at > $1 AND id <> $3`,
		now, userID, resetID,
	); err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_password_resets SET used_at = $1
		WHERE id = $2`, now, resetID); err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, passwordResetInvalid, err
	}
	return userID, passwordResetOK, nil
}

func (s *postgresStore) revokePasswordReset(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE auth_password_resets SET used_at = NOW()
		WHERE token_hash = $1 AND used_at IS NULL`, tokenHash)
	return err
}

func mapUserConflict(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.UniqueViolation {
		return err
	}

	switch pgErr.ConstraintName {
	case "users_username_key":
		return ErrUsernameTaken
	case "users_email_key":
		return ErrEmailTaken
	}

	// Constraint names can differ when a migration used explicit indexes.
	// The detail text is stable enough for a defensive fallback.
	if strings.Contains(pgErr.Detail, "(username)") {
		return ErrUsernameTaken
	}
	if strings.Contains(pgErr.Detail, "(email)") {
		return ErrEmailTaken
	}
	return err
}
