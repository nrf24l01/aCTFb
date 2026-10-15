package users

import (
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
