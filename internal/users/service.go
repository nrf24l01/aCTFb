package users

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nrf24l01/actfb/internal/platform/crypto"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/users/nats"
)

type UserService struct {
	db       *postgres.DB
	config   *config.Config
	email    *nats.EmailTaskPublisher
	totp_gen *crypto.TotpGenerator
	logger   *slog.Logger
}

type User struct {
	ID uuid.UUID

	Username string
	Email    string
	Password string

	EmailVerified  bool
	Email2FASecret byte
}

func NewUserService(db *postgres.DB, cfg *config.Config, emailClient *nats.EmailTaskPublisher) *UserService {
	return &UserService{
		db:       db,
		config:   cfg,
		email:    emailClient,
		totp_gen: crypto.NewTotpGenerator(cfg.Security.EmailVerifyPeriod),
	}
}

func (us *UserService) CreateUser(ctx context.Context, username, password, email string) error {
	hash, err := crypto.Hash(password, us.config.Argon2id)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	email_checked := true
	email_secret := ""

	if us.config.Email.Enabled && us.config.Security.EmailVerifyEnabled {
		email_checked = false
		email_secret, err = crypto.GenerateTOTPSecret()
		if err != nil {
			return fmt.Errorf("generate email secret: %w", err)
		}
	}

	_, err = us.db.Pool.Exec(
		ctx,
		"INSERT INTO users (username, email, password, email_verified, email_secret) VALUES ($1, $2, $3, $4, $5)",
		username,
		email,
		hash,
		email_checked,
		email_secret,
	)
	if err != nil {
		var pgErr *pgconn.PgError

		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			switch pgErr.ConstraintName {
			case "users_username_key":
				return ErrUsernameTaken
			case "users_email_key":
				return ErrEmailTaken
			}
		}

		return err
	}

	return nil
}

func (us *UserService) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	var user User

	err := us.db.Pool.QueryRow(
		ctx,
		"SELECT id, username, email, password, email_verified, email_2fa_secret FROM users WHERE id = $1",
		id,
	).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Password,
		&user.EmailVerified,
		&user.Email2FASecret,
	)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	return &user, nil
}

func (us *UserService) CheckPassword(ctx context.Context, username, password string) (bool, error) {
	var hash string

	err := us.db.Pool.QueryRow(
		ctx,
		"SELECT password FROM users WHERE username = $1",
		username,
	).Scan(&hash)
	if err != nil {
		return false, fmt.Errorf("get user password: %w", err)
	}

	ok, err := crypto.VerifyArgon2id(password, hash)
	if err != nil {
		return false, fmt.Errorf("verify password: %w", err)
	}

	return ok, nil
}

func (us *UserService) UpdatePassword(ctx context.Context, username, newPassword string) error {
	hash, err := crypto.Hash(newPassword, us.config.Argon2id)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	_, err = us.db.Pool.Exec(
		ctx,
		"UPDATE users SET password = $1 WHERE username = $2",
		hash,
		username,
	)
	if err != nil {
		return fmt.Errorf("update user password: %w", err)
	}

	return nil
}

func (us *UserService) AskEmailVerification(ctx context.Context, id string) error {
	var email string
	var email_2fa_secret string
	var username string

	err := us.db.Pool.QueryRow(
		ctx,
		"SELECT email, username, email_2fa_secret FROM users WHERE id = $1",
		id,
	).Scan(&email, &username, &email_2fa_secret)
	if err != nil {
		return fmt.Errorf("get user email and 2fa secret: %w", err)
	}

	_, err = us.db.Pool.Exec(
		ctx,
		"UPDATE users SET email_send_at = NOW() WHERE id = $1",
		id,
	)
	if err != nil {
		return fmt.Errorf("update user email_send_at: %w", err)
	}

	totp_code, err := us.totp_gen.GenerateTOTPCode(email_2fa_secret)
	if err != nil {
		return fmt.Errorf("generate totp code: %w", err)
	}

	task_uuid := uuid.New()

	us.logger.Info("Email verification task published", "task_id", task_uuid)

	err = us.email.PublishEmailVerificationTask(ctx, email, username, totp_code, task_uuid)
	if err != nil {
		return fmt.Errorf("send verification email: %w", err)
	}

	return nil
}

func (us *UserService) VerifyEmail(ctx context.Context, id, code string) (bool, error) {
	var email_2fa_secret string

	err := us.db.Pool.QueryRow(
		ctx,
		"SELECT email_2fa_secret FROM users WHERE id = $1",
		id,
	).Scan(&email_2fa_secret)
	if err != nil {
		return false, fmt.Errorf("get user email_2fa_secret: %w", err)
	}

	ok, err := us.totp_gen.ValidateTOTPCode(email_2fa_secret, code)
	if err != nil {
		return false, fmt.Errorf("validate totp code: %w", err)
	}

	if !ok {
		return false, nil
	}

	_, err = us.db.Pool.Exec(
		ctx,
		"UPDATE users SET email_verified = TRUE WHERE id = $1",
		id,
	)
	if err != nil {
		return false, fmt.Errorf("update user email_verified: %w", err)
	}

	return true, nil
}
