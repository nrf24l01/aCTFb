package users

import (
	"context"
	"fmt"

	"github.com/nrf24l01/actfb/internal/platform/crypto"

	"github.com/google/uuid"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/email"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

type UserService struct {
	db     *postgres.DB
	config *config.Config
	email  *email.Client
}

type User struct {
	ID uuid.UUID `json:"id" db:"id"`

	Username string `json:"username" db:"username"`
	Email    string `json:"email" db:"email"`
	Password string `json:"-" db:"password"`

	EmailVerified  bool `json:"-" db:"email_verified"`
	Email2FASecret byte `json:"-" db:"email_2fa_secret"`
}

func NewUserService(db *postgres.DB, cfg *config.Config, emailClient *email.Client) *UserService {
	return &UserService{
		db:     db,
		config: cfg,
		email:  emailClient,
	}
}

func (us *UserService) CreateUser(username, password, email string, ctx context.Context) error {
	hash, err := crypto.Hash(password, us.config.Argon2id)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	email_checked := true

	if us.config.Email.Enabled {
		email_checked = false
	}

	_, err = us.db.Pool.Exec(
		ctx,
		"INSERT INTO users (username, email, password, email_verified) VALUES ($1, $2, $3, $4)",
		username,
		email,
		hash,
		email_checked,
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}

	return nil
}
