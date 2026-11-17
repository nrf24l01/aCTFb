// Package admincli implements the operator-only admin commands built into the
// backend binary. It deliberately bypasses HTTP/JWT/SMTP/worker bootstrap; the
// trusted boundary is the operator's OS and PostgreSQL access.
package admincli

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

var (
	ErrPasswordEmpty         = errors.New("password is required")
	ErrPasswordTooShort      = errors.New("password must be at least 8 bytes")
	ErrPasswordTooLong       = errors.New("password must be at most 128 bytes")
	ErrPasswordInvalid       = errors.New("password contains an invalid NUL byte")
	ErrPasswordStdinFormat   = errors.New("password stdin must contain exactly one line")
	ErrPasswordMismatch      = errors.New("passwords do not match")
	ErrPasswordInputRequired = errors.New("password input requires a terminal; use --password-stdin")
	ErrTargetDisabled        = errors.New("target account is disabled")
	ErrNotAdmin              = errors.New("target account is not an admin")
	ErrSchemaMissing         = errors.New("database schema is missing: start backend or apply migrations first")
)

// Target is exactly one admin-account identifier.
type Target struct {
	ID       uuid.UUID
	Username string
}

type CreateInput struct {
	Username string
	Email    string
	Password string
}

// AdminUser is the non-secret CLI read model. Password hashes are never
// selected into this struct and tokens are never included.
type AdminUser struct {
	ID       uuid.UUID
	Username string
	Email    string
	Roles    []string
	Disabled bool
}

// Runner owns CLI streams and the few process dependencies that tests need to
// replace. Help and usage paths never call LoadConfig or OpenDB.
type Runner struct {
	In         io.Reader
	Out        io.Writer
	Err        io.Writer
	IsTerminal func() bool
	ReadSecret func(string) (string, error)
	LoadConfig func() (*config.Config, error)
	OpenDB     func(context.Context, *config.PostgresConfig) (*postgres.DB, error)
}
