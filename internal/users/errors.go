package users

import "errors"

var (
	ErrUsernameTaken = errors.New("username already taken")
	ErrEmailTaken    = errors.New("email already taken")

	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserDisabled       = errors.New("user account is disabled")
	ErrUserNotFound       = errors.New("user not found")
	ErrUnauthorized       = errors.New("authentication required")

	ErrRefreshTokenMissing = errors.New("refresh token is missing")
	ErrRefreshTokenInvalid = errors.New("refresh token is invalid")
	ErrRefreshTokenExpired = errors.New("refresh token expired")
	ErrRefreshTokenReused  = errors.New("refresh token reuse detected")
	ErrOriginNotAllowed    = errors.New("request origin is not allowed")

	ErrEmailAlreadyVerified     = errors.New("email already verified")
	ErrEmailUnavailable         = errors.New("email delivery is not configured")
	ErrVerificationTokenInvalid = errors.New("invalid email verification token")
	ErrVerificationTokenExpired = errors.New("email verification token expired")
	ErrVerificationTokenUsed    = errors.New("email verification token already used")

	ErrResetTokenInvalid = errors.New("invalid password reset token")
	ErrResetTokenExpired = errors.New("password reset token expired")
	ErrResetTokenUsed    = errors.New("password reset token already used")

	ErrCurrentPasswordIncorrect = errors.New("current password is incorrect")
	ErrAuthConfiguration        = errors.New("auth configuration is invalid")
	ErrRateLimited              = errors.New("rate limited")
)
