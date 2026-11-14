package attackdefense

import (
	"errors"
	"fmt"
)

// Sentinel errors are mapped to the documented Swagger error codes by the HTTP
// adapter. Internal details are never included in responses or logs.
var (
	ErrUnauthorized          = errors.New("attackdefense: unauthorized")
	ErrForbidden             = errors.New("attackdefense: forbidden")
	ErrNoDatabase            = errors.New("attackdefense: database is required")
	ErrNoAuthorizer          = errors.New("attackdefense: authorizer is required")
	ErrNoSealer              = errors.New("attackdefense: credential sealer is required")
	ErrNoChecker             = errors.New("attackdefense: checker is required")
	ErrNoGitProvider         = errors.New("attackdefense: git provider is not configured")
	ErrNoIntegrationResolver = errors.New("attackdefense: integration resolver is required")

	ErrBoardNotFound      = errors.New("attackdefense: board not found")
	ErrBoardAccessDenied  = errors.New("attackdefense: board access denied")
	ErrModuleNotFound     = errors.New("attackdefense: module not found")
	ErrModuleTypeMismatch = errors.New("attackdefense: module type mismatch")
	ErrModuleNotAvailable = errors.New("attackdefense: module not available")

	ErrRepositoryNotConfigured   = errors.New("attackdefense: repository not configured")
	ErrRepositoryBindingNotFound = errors.New("attackdefense: repository binding not found")
	ErrRepositoryBindingInUse    = errors.New("attackdefense: repository binding in use")
	ErrRepositoryAlreadyBound    = errors.New("attackdefense: repository already bound")
	ErrRepositoryNotFound        = errors.New("attackdefense: repository not found")
	ErrIntegrationNotFound       = errors.New("attackdefense: integration not found")
	ErrIntegrationNotGit         = errors.New("attackdefense: integration is not a Git integration")
	ErrIntegrationCredentials    = errors.New("attackdefense: integration credentials unavailable")

	ErrVulnerabilityNotFound  = errors.New("attackdefense: vulnerability not found")
	ErrVulnerabilityKeyExists = errors.New("attackdefense: vulnerability key already exists")
	ErrVulnerabilityInUse     = errors.New("attackdefense: vulnerability already has awards")

	ErrConfigNotFound          = errors.New("attackdefense: config not found")
	ErrBoardNotActive          = errors.New("attackdefense: board is not active")
	ErrRunKindNotAllowed       = errors.New("attackdefense: run kind is not enabled for this module")
	ErrRunNotFound             = errors.New("attackdefense: run not found")
	ErrRunAlreadyQueued        = errors.New("attackdefense: logical run already queued or running")
	ErrRunActionNotAllowed     = errors.New("attackdefense: run action not allowed")
	ErrRunRateLimited          = errors.New("attackdefense: run rate limited")
	ErrCommitNotFound          = errors.New("attackdefense: commit not found")
	ErrInvalidCommit           = errors.New("attackdefense: invalid commit")
	ErrIdempotencyKeyReused    = errors.New("attackdefense: idempotency key reused with a different request")
	ErrRunnerUnavailable       = errors.New("attackdefense: runner or runner image unavailable")
	ErrGitProvider             = errors.New("attackdefense: git provider error")
	ErrInvalidWebhook          = errors.New("attackdefense: invalid webhook")
	ErrInvalidWebhookSignature = errors.New("attackdefense: invalid webhook signature")
	ErrUnsupportedGitEvent     = errors.New("attackdefense: unsupported git event")
	ErrWebhookAlreadyProcessed = errors.New("attackdefense: webhook delivery already processed")
	ErrWebhookReplayed         = errors.New("attackdefense: webhook delivery replayed with a different payload")
	ErrPayloadTooLarge         = errors.New("attackdefense: payload too large")
	ErrUnsupportedMediaType    = errors.New("attackdefense: unsupported media type")
	ErrCheckerFailed           = errors.New("attackdefense: checker failed")
	ErrInvalidVerdict          = errors.New("attackdefense: invalid checker verdict")
	ErrCanceled                = errors.New("attackdefense: operation canceled")
)

// ValidationError is a bounded, field-addressed request problem. It never
// carries the rejected value.
type ValidationError struct {
	Field string
	Issue string
	Code  string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "attackdefense: validation failed"
	}
	return fmt.Sprintf("attackdefense: validation failed: %s: %s", e.Field, e.Issue)
}

// Invalid constructs a validation error.
func Invalid(field, issue, code string) error {
	return &ValidationError{Field: field, Issue: issue, Code: code}
}
