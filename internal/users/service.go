package users

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/crypto"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/users/nats"
)

const (
	EmailVerificationResendCooldown = 60 * time.Second
	PasswordResetTokenTTL           = 15 * time.Minute
)

type emailPublisher interface {
	PublishEmailVerificationTask(ctx context.Context, to, username, code string, taskID uuid.UUID) error
	PublishPasswordResetTask(ctx context.Context, to, username, token string, taskID uuid.UUID) error
}

type UserService struct {
	config    *config.Config
	store     authStore
	email     emailPublisher
	logger    *slog.Logger
	dummyHash string
}

type User struct {
	ID uuid.UUID

	Username string
	Email    string
	Password string

	EmailVerified  bool
	Email2FASecret string
	EmailSendAt    time.Time

	Disabled    bool
	Roles       []string
	CreatedAt   time.Time
	AuthVersion int64
}

type RegisterInput struct {
	Username string
	Email    string
	Password string
}

type AuthResult struct {
	User         *User
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
}

func NewUserService(db *postgres.DB, cfg *config.Config, emailClient *nats.EmailTaskPublisher) *UserService {
	var publisher emailPublisher
	if emailClient != nil {
		publisher = emailClient
	}
	return newUserService(newPostgresStore(db), cfg, publisher, slog.Default())
}

func newUserService(store authStore, cfg *config.Config, email emailPublisher, logger *slog.Logger) *UserService {
	if cfg == nil {
		cfg = &config.Config{}
	}
	if logger == nil {
		logger = slog.Default()
	}

	dummyHash := ""
	if cfg.Argon2id.Time > 0 && cfg.Argon2id.Memory > 0 && cfg.Argon2id.Threads > 0 && cfg.Argon2id.KeyLen > 0 {
		dummyHash, _ = crypto.Hash("dummy-password-not-a-real-user", cfg.Argon2id)
	}

	return &UserService{
		config:    cfg,
		store:     store,
		email:     email,
		logger:    logger,
		dummyHash: dummyHash,
	}
}

// SetLogger replaces the service logger. It exists mainly for bootstrap and
// tests; a nil logger falls back to slog.Default.
func (us *UserService) SetLogger(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	us.logger = logger
}

func (us *UserService) Register(ctx context.Context, input RegisterInput) (*AuthResult, error) {
	if err := us.validateTokenConfig(); err != nil {
		return nil, err
	}

	params, err := us.prepareUser(input.Username, input.Email, input.Password)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	refresh, refreshHash, refreshExpiresAt, expiresIn, err := us.newRefreshToken(now)
	if err != nil {
		return nil, err
	}

	// The token subject depends on the generated user id, so create the user
	// first inside the store transaction and sign the access token after it
	// returns. The refresh hash is independent of the user id.
	user, err := us.store.createUserWithSession(ctx, params, refreshHash, refreshExpiresAt)
	if err != nil {
		return nil, err
	}

	access, err := signAccessToken(user.ID, user.AuthVersion, us.config.JWT.Access.Secret, us.accessTTL(), now)
	if err != nil {
		// The account and session were created atomically; returning an error
		// here would strand a session. Revoke it best-effort.
		_ = us.store.revokeSession(ctx, refreshHash)
		return nil, err
	}

	return &AuthResult{
		User:         user,
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    expiresIn,
	}, nil
}

// CreateUser is kept for compatibility with the original service skeleton. It
// creates the account without issuing a session.
func (us *UserService) CreateUser(ctx context.Context, username, password, email string) (*User, error) {
	params, err := us.prepareUser(username, email, password)
	if err != nil {
		return nil, err
	}
	return us.store.createUser(ctx, params)
}

func (us *UserService) Login(ctx context.Context, username, password string) (*AuthResult, error) {
	user, err := us.store.getUserByUsername(ctx, username)
	if errors.Is(err, ErrUserNotFound) {
		us.verifyDummy(password)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	ok, err := crypto.VerifyArgon2id(password, user.Password)
	if err != nil || !ok {
		if err != nil {
			us.logger.Warn("password verification failed", "user_id", user.ID)
		}
		return nil, ErrInvalidCredentials
	}
	if user.Disabled {
		return nil, ErrUserDisabled
	}

	now := time.Now()
	refresh, refreshHash, refreshExpiresAt, expiresIn, err := us.newRefreshToken(now)
	if err != nil {
		return nil, err
	}

	// The password check above reads the hash/version before this transaction.
	// Re-check both under the same user lock used by password mutations before
	// creating a session, so an old credential cannot obtain a usable session
	// after a concurrent reset/change.
	created, err := us.store.createSessionIfAuthVersion(
		ctx,
		user.ID,
		user.AuthVersion,
		user.Password,
		refreshHash,
		refreshExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, ErrInvalidCredentials
	}

	access, err := signAccessToken(user.ID, user.AuthVersion, us.config.JWT.Access.Secret, us.accessTTL(), now)
	if err != nil {
		_ = us.store.revokeSession(ctx, refreshHash)
		return nil, err
	}

	return &AuthResult{
		User:         user,
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    expiresIn,
	}, nil
}

func (us *UserService) Refresh(ctx context.Context, rawRefreshToken string) (*AuthResult, error) {
	if strings.TrimSpace(rawRefreshToken) == "" {
		return nil, ErrRefreshTokenMissing
	}
	if err := us.validateTokenConfig(); err != nil {
		return nil, err
	}

	now := time.Now()
	refresh, refreshHash, refreshExpiresAt, expiresIn, err := us.newRefreshToken(now)
	if err != nil {
		return nil, err
	}
	oldHash, err := hashRefreshToken(rawRefreshToken, us.refreshSecret())
	if err != nil {
		return nil, err
	}

	user, status, err := us.store.rotateRefreshSession(ctx, oldHash, refreshHash, now, refreshExpiresAt)
	if err != nil {
		return nil, err
	}

	switch status {
	case rotationOK:
	case rotationInvalid:
		return nil, ErrRefreshTokenInvalid
	case rotationExpired:
		return nil, ErrRefreshTokenExpired
	case rotationReused:
		us.logger.Warn("refresh token reuse detected", "user_id", user.ID)
		return nil, ErrRefreshTokenReused
	case rotationDisabled:
		return nil, ErrUserDisabled
	default:
		return nil, ErrRefreshTokenInvalid
	}

	access, err := signAccessToken(user.ID, user.AuthVersion, us.config.JWT.Access.Secret, us.accessTTL(), now)
	if err != nil {
		return nil, err
	}

	return &AuthResult{
		User:         user,
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    expiresIn,
	}, nil
}

func (us *UserService) Logout(ctx context.Context, rawRefreshToken string) error {
	if strings.TrimSpace(rawRefreshToken) == "" {
		return nil
	}
	hash, err := hashRefreshToken(rawRefreshToken, us.refreshSecret())
	if err != nil {
		return err
	}
	return us.store.revokeSession(ctx, hash)
}

func (us *UserService) AuthenticateAccessToken(ctx context.Context, token string) (*User, error) {
	claims, err := parseAccessToken(token, us.config.JWT.Access.Secret, time.Now())
	if err != nil {
		if errors.Is(err, ErrAuthConfiguration) {
			return nil, err
		}
		return nil, ErrUnauthorized
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, ErrUnauthorized
	}

	user, err := us.store.getUserByID(ctx, userID)
	if errors.Is(err, ErrUserNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if user.Disabled {
		// Bearer authentication has no documented USER_DISABLED response on
		// /me; treat it as an invalid principal.
		return nil, ErrUnauthorized
	}
	if claims.AuthVersion != user.AuthVersion {
		return nil, ErrUnauthorized
	}
	return user, nil
}

func (us *UserService) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	return us.store.getUserByID(ctx, id)
}

func (us *UserService) UpdateUsername(ctx context.Context, id uuid.UUID, username string) (*User, error) {
	username = strings.TrimSpace(username)
	if err := us.store.updateUsername(ctx, id, username); err != nil {
		return nil, err
	}
	return us.store.getUserByID(ctx, id)
}

func (us *UserService) ChangePassword(ctx context.Context, id uuid.UUID, currentPassword, newPassword string) error {
	user, err := us.store.getUserByID(ctx, id)
	if errors.Is(err, ErrUserNotFound) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}

	ok, err := crypto.VerifyArgon2id(currentPassword, user.Password)
	if err != nil || !ok {
		return ErrCurrentPasswordIncorrect
	}

	hash, err := crypto.Hash(newPassword, us.config.Argon2id)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	changed, err := us.store.changePasswordIfCurrent(
		ctx,
		id,
		user.AuthVersion,
		user.Password,
		hash,
	)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	if !changed {
		return ErrCurrentPasswordIncorrect
	}
	return nil
}

func (us *UserService) CheckPassword(ctx context.Context, username, password string) (bool, error) {
	user, err := us.store.getUserByUsername(ctx, username)
	if errors.Is(err, ErrUserNotFound) {
		us.verifyDummy(password)
		return false, nil
	}
	if err != nil {
		return false, err
	}

	ok, err := crypto.VerifyArgon2id(password, user.Password)
	if err != nil {
		return false, nil
	}
	return ok, nil
}

func (us *UserService) UpdatePassword(ctx context.Context, username, newPassword string) error {
	user, err := us.store.getUserByUsername(ctx, username)
	if errors.Is(err, ErrUserNotFound) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}

	hash, err := crypto.Hash(newPassword, us.config.Argon2id)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	changed, err := us.store.changePasswordIfCurrent(
		ctx,
		user.ID,
		user.AuthVersion,
		user.Password,
		hash,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if !changed {
		return ErrUserNotFound
	}
	return nil
}

func (us *UserService) RequestPasswordReset(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	if strings.TrimSpace(us.refreshSecret()) == "" {
		us.logger.Error("password reset is unavailable: token secret is not configured")
		return nil
	}

	user, err := us.store.getUserByEmail(ctx, email)
	if errors.Is(err, ErrUserNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup password reset account: %w", err)
	}
	if user.Disabled {
		return nil
	}
	if !us.config.Email.Enabled || us.email == nil {
		us.logger.Warn("password reset email is not configured; request accepted without delivery")
		return nil
	}

	token, err := generatePasswordResetToken()
	if err != nil {
		us.logger.Error("failed to generate password reset token")
		return nil
	}
	tokenHash, err := hashPasswordResetToken(token, us.refreshSecret())
	if err != nil {
		us.logger.Error("failed to hash password reset token")
		return nil
	}

	now := time.Now()
	if err := us.store.createPasswordReset(ctx, user.ID, tokenHash, now.Add(us.passwordResetTTL())); err != nil {
		// Do not let a known-account storage failure become an enumeration
		// oracle or claim a delivery that did not happen.
		us.logger.Error("failed to persist password reset token", "error", err)
		return nil
	}

	taskID := uuid.New()
	if err := us.email.PublishPasswordResetTask(
		ctx,
		user.Email,
		user.Username,
		token,
		taskID,
	); err != nil {
		if revokeErr := us.store.revokePasswordReset(ctx, tokenHash); revokeErr != nil {
			us.logger.Error("failed to revoke undelivered password reset token", "error", revokeErr)
		}
		us.logger.Warn("password reset email publish failed", "error", err)
		return nil
	}

	us.logger.Info("password reset email task published", "task_id", taskID)
	return nil
}

func (us *UserService) ConfirmPasswordReset(ctx context.Context, token, newPassword string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrResetTokenInvalid
	}

	tokenHash, err := hashPasswordResetToken(token, us.refreshSecret())
	if err != nil {
		return err
	}
	newPasswordHash, err := crypto.Hash(newPassword, us.config.Argon2id)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	_, status, err := us.store.consumePasswordReset(ctx, tokenHash, time.Now(), newPasswordHash)
	if err != nil {
		return err
	}

	switch status {
	case passwordResetOK:
		return nil
	case passwordResetExpired:
		return ErrResetTokenExpired
	case passwordResetUsed:
		return ErrResetTokenUsed
	case passwordResetInvalid:
		return ErrResetTokenInvalid
	default:
		return ErrResetTokenInvalid
	}
}

func (us *UserService) passwordResetTTL() time.Duration {
	if period := us.config.Security.PasswordResetPeriod; period > 0 {
		return period
	}
	return PasswordResetTokenTTL
}

func (us *UserService) AskEmailVerification(ctx context.Context, id uuid.UUID) error {
	state, err := us.store.getEmailState(ctx, id)
	if errors.Is(err, ErrUserNotFound) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if state.Verified {
		return ErrEmailAlreadyVerified
	}
	if us.email == nil || !us.config.Email.Enabled || !us.config.Security.EmailVerifyEnabled {
		return ErrEmailUnavailable
	}
	period := us.config.Security.EmailVerifyPeriod
	if period <= 0 {
		return fmt.Errorf("%w: email verification period must be positive", ErrAuthConfiguration)
	}

	now := time.Now()
	if !state.SentAt.IsZero() && now.Sub(state.SentAt) < EmailVerificationResendCooldown {
		return ErrRateLimited
	}

	token, err := generateEmailVerificationToken()
	if err != nil {
		return err
	}
	tokenHash, err := hashEmailVerificationToken(token, us.refreshSecret())
	if err != nil {
		return err
	}

	if err := us.store.createEmailVerification(ctx, id, tokenHash, now.Add(period)); err != nil {
		return fmt.Errorf("persist email verification token: %w", err)
	}

	taskID := uuid.New()
	if err := us.email.PublishEmailVerificationTask(ctx, state.Email, state.Username, token, taskID); err != nil {
		// The token was persisted before publish to guarantee the recipient
		// never receives a token that is unknown to the database. If publish
		// fails, consume it immediately so it cannot be used later.
		if revokeErr := us.store.revokeEmailVerification(ctx, tokenHash); revokeErr != nil {
			us.logger.Error("failed to revoke unsent email verification token", "user_id", id, "error", revokeErr)
		}
		return fmt.Errorf("publish verification email: %w", err)
	}
	if err := us.store.markEmailSent(ctx, id, now); err != nil {
		return fmt.Errorf("record email verification send time: %w", err)
	}

	us.logger.Info("email verification task published", "user_id", id, "task_id", taskID)
	return nil
}

// ConfirmEmailVerification handles the public Swagger endpoint whose request
// body contains only a token. The token row is evaluated before the user's
// email_verified flag so a consumed token reports USED (410), not 409.
func (us *UserService) ConfirmEmailVerification(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrVerificationTokenInvalid
	}

	tokenHash, err := hashEmailVerificationToken(token, us.refreshSecret())
	if err != nil {
		return err
	}

	_, status, err := us.store.consumeEmailVerification(ctx, tokenHash, time.Now())
	if err != nil {
		return err
	}

	switch status {
	case emailVerificationOK:
		return nil
	case emailVerificationInvalid:
		return ErrVerificationTokenInvalid
	case emailVerificationExpired:
		return ErrVerificationTokenExpired
	case emailVerificationUsed:
		return ErrVerificationTokenUsed
	case emailVerificationAlreadyVerified:
		return ErrEmailAlreadyVerified
	default:
		return ErrVerificationTokenInvalid
	}
}

func (us *UserService) prepareUser(username, email, password string) (createUserParams, error) {
	username = strings.TrimSpace(username)
	email = strings.ToLower(strings.TrimSpace(email))

	hash, err := crypto.Hash(password, us.config.Argon2id)
	if err != nil {
		return createUserParams{}, fmt.Errorf("hash password: %w", err)
	}

	verifyRequired := us.config.Email.Enabled && us.config.Security.EmailVerifyEnabled
	emailVerified := !verifyRequired

	return createUserParams{
		Username:      username,
		Email:         email,
		PasswordHash:  hash,
		EmailVerified: emailVerified,
	}, nil
}

func (us *UserService) newRefreshToken(now time.Time) (string, []byte, time.Time, int64, error) {
	refreshTTL := us.refreshTTL()
	if refreshTTL <= 0 {
		return "", nil, time.Time{}, 0, fmt.Errorf("%w: refresh token expiry must be positive", ErrAuthConfiguration)
	}
	refresh, err := generateRefreshToken()
	if err != nil {
		return "", nil, time.Time{}, 0, err
	}
	hash, err := hashRefreshToken(refresh, us.refreshSecret())
	if err != nil {
		return "", nil, time.Time{}, 0, err
	}
	return refresh, hash, now.Add(refreshTTL), int64(us.accessTTL().Seconds()), nil
}

func (us *UserService) accessTTL() time.Duration {
	return time.Duration(us.config.JWT.Access.Expiry) * time.Second
}

func (us *UserService) refreshTTL() time.Duration {
	return time.Duration(us.config.JWT.Refresh.Expiry) * time.Second
}

func (us *UserService) validateTokenConfig() error {
	if strings.TrimSpace(us.config.JWT.Access.Secret) == "" {
		return fmt.Errorf("%w: access token secret is empty", ErrAuthConfiguration)
	}
	if strings.TrimSpace(us.config.JWT.Refresh.Secret) == "" {
		return fmt.Errorf("%w: refresh token secret is empty", ErrAuthConfiguration)
	}
	if us.accessTTL() <= 0 {
		return fmt.Errorf("%w: access token expiry must be positive", ErrAuthConfiguration)
	}
	if us.refreshTTL() <= 0 {
		return fmt.Errorf("%w: refresh token expiry must be positive", ErrAuthConfiguration)
	}
	return nil
}

func (us *UserService) refreshSecret() string {
	return us.config.JWT.Refresh.Secret
}

func (us *UserService) verifyDummy(password string) {
	if us.dummyHash == "" {
		return
	}
	_, _ = crypto.VerifyArgon2id(password, us.dummyHash)
}
