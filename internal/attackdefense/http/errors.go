// Package http maps the 22 documented Attack+Defense operations onto the AD
// service. Boot owns route registration; this package never edits the router,
// generated models or another feature's HTTP package.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/attackdefense"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

type fieldError struct {
	Field string `json:"field"`
	Issue string `json:"issue"`
	Code  string `json:"code"`
}

type apiError struct {
	Code        string       `json:"code"`
	Message     string       `json:"message"`
	TraceID     string       `json:"traceId"`
	Timestamp   string       `json:"timestamp"`
	Path        string       `json:"path"`
	FieldErrors []fieldError `json:"fieldErrors,omitempty"`
}

func writeProblem(c *echo.Context, status int, code, message string, fields ...fieldError) error {
	trace := traceID(c)
	body := apiError{
		Code: code, Message: message, TraceID: trace,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Path:      c.Request().URL.Path, FieldErrors: fields,
	}
	return c.JSON(status, body)
}

func writeValidation(c *echo.Context, field, issue, code string) error {
	return writeProblem(c, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "Validation failed",
		fieldError{Field: field, Issue: issue, Code: code})
}

func writeServiceError(c *echo.Context, err error) error {
	if err == nil {
		return nil
	}
	var validation *attackdefense.ValidationError
	if errors.As(err, &validation) {
		return writeValidation(c, validation.Field, validation.Issue, validation.Code)
	}
	switch {
	case errors.Is(err, attackdefense.ErrUnauthorized):
		return writeProblem(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
	case errors.Is(err, attackdefense.ErrForbidden):
		return writeProblem(c, http.StatusForbidden, "FORBIDDEN", "Access denied")
	case errors.Is(err, attackdefense.ErrBoardAccessDenied):
		return writeProblem(c, http.StatusForbidden, "BOARD_ACCESS_DENIED", "Board access denied")
	case errors.Is(err, attackdefense.ErrModuleNotAvailable):
		return writeProblem(c, http.StatusForbidden, "MODULE_NOT_AVAILABLE", "Module not available")
	case errors.Is(err, attackdefense.ErrBoardNotFound):
		return writeProblem(c, http.StatusNotFound, "BOARD_NOT_FOUND", "Board not found")
	case errors.Is(err, attackdefense.ErrModuleNotFound):
		return writeProblem(c, http.StatusNotFound, "MODULE_NOT_FOUND", "Module not found")
	case errors.Is(err, attackdefense.ErrModuleTypeMismatch):
		return writeProblem(c, http.StatusConflict, "MODULE_TYPE_MISMATCH", "Module type mismatch")
	case errors.Is(err, attackdefense.ErrBoardNotActive):
		return writeProblem(c, http.StatusConflict, "BOARD_NOT_ACTIVE", "Board not active")
	case errors.Is(err, attackdefense.ErrRepositoryNotConfigured), errors.Is(err, attackdefense.ErrConfigNotFound):
		return writeProblem(c, http.StatusNotFound, "REPOSITORY_NOT_CONFIGURED", "Repository not configured")
	case errors.Is(err, attackdefense.ErrRepositoryBindingNotFound):
		return writeProblem(c, http.StatusNotFound, "REPOSITORY_BINDING_NOT_FOUND", "Repository binding not found")
	case errors.Is(err, attackdefense.ErrRepositoryBindingInUse):
		return writeProblem(c, http.StatusConflict, "REPOSITORY_BINDING_IN_USE", "Repository binding is in use")
	case errors.Is(err, attackdefense.ErrRepositoryAlreadyBound):
		return writeProblem(c, http.StatusConflict, "REPOSITORY_ALREADY_BOUND", "Repository already bound")
	case errors.Is(err, attackdefense.ErrIntegrationNotFound):
		return writeProblem(c, http.StatusNotFound, "INTEGRATION_NOT_FOUND", "Integration not found")
	case errors.Is(err, attackdefense.ErrVulnerabilityNotFound):
		return writeProblem(c, http.StatusNotFound, "VULNERABILITY_NOT_FOUND", "Vulnerability not found")
	case errors.Is(err, attackdefense.ErrVulnerabilityKeyExists):
		return writeProblem(c, http.StatusConflict, "VULNERABILITY_KEY_ALREADY_EXISTS", "Vulnerability key already exists")
	case errors.Is(err, attackdefense.ErrVulnerabilityInUse):
		return writeProblem(c, http.StatusConflict, "VULNERABILITY_IN_USE", "Vulnerability is in use")
	case errors.Is(err, attackdefense.ErrRunNotFound):
		return writeProblem(c, http.StatusNotFound, "RUN_NOT_FOUND", "Run not found")
	case errors.Is(err, attackdefense.ErrRunAlreadyQueued):
		return writeProblem(c, http.StatusConflict, "RUN_ALREADY_QUEUED", "Run already queued")
	case errors.Is(err, attackdefense.ErrRunActionNotAllowed):
		return writeProblem(c, http.StatusConflict, "RUN_ACTION_NOT_ALLOWED", "Run action not allowed")
	case errors.Is(err, attackdefense.ErrRunRateLimited):
		c.Response().Header().Set("Retry-After", "1")
		return writeProblem(c, http.StatusTooManyRequests, "RUN_RATE_LIMITED", "Run rate limit exceeded")
	case errors.Is(err, attackdefense.ErrCommitNotFound):
		return writeProblem(c, http.StatusUnprocessableEntity, "COMMIT_NOT_FOUND", "Commit not found")
	case errors.Is(err, attackdefense.ErrInvalidCommit):
		return writeProblem(c, http.StatusUnprocessableEntity, "INVALID_COMMIT", "Invalid commit")
	case errors.Is(err, attackdefense.ErrIdempotencyKeyReused):
		return writeProblem(c, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "Idempotency key was already used with a different request")
	case errors.Is(err, attackdefense.ErrRunnerUnavailable), errors.Is(err, attackdefense.ErrNoChecker):
		return writeProblem(c, http.StatusServiceUnavailable, "RUNNER_UNAVAILABLE", "Runner unavailable")
	case errors.Is(err, attackdefense.ErrGitProvider), errors.Is(err, attackdefense.ErrIntegrationCredentials),
		errors.Is(err, attackdefense.ErrRepositoryNotFound):
		return writeProblem(c, http.StatusBadGateway, "GIT_PROVIDER_ERROR", "Git provider error")
	case errors.Is(err, attackdefense.ErrNoIntegrationResolver), errors.Is(err, attackdefense.ErrNoGitProvider):
		return writeProblem(c, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Required service unavailable")
	case errors.Is(err, attackdefense.ErrInvalidWebhookSignature):
		return writeProblem(c, http.StatusUnauthorized, "INVALID_WEBHOOK_SIGNATURE", "Invalid webhook signature")
	case errors.Is(err, attackdefense.ErrPayloadTooLarge):
		return writeProblem(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "Payload exceeds the allowed size")
	case errors.Is(err, attackdefense.ErrUnsupportedMediaType):
		return writeProblem(c, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Unsupported media type; expected application/json")
	case errors.Is(err, attackdefense.ErrInvalidWebhook):
		return writeProblem(c, http.StatusUnprocessableEntity, "INVALID_WEBHOOK_PAYLOAD", "Invalid webhook payload")
	case errors.Is(err, attackdefense.ErrUnsupportedGitEvent):
		return writeProblem(c, http.StatusUnprocessableEntity, "UNSUPPORTED_GIT_EVENT", "Unsupported git event")
	case errors.Is(err, attackdefense.ErrWebhookAlreadyProcessed):
		return writeProblem(c, http.StatusConflict, "WEBHOOK_ALREADY_PROCESSED", "Webhook already processed")
	case errors.Is(err, attackdefense.ErrWebhookReplayed):
		return writeProblem(c, http.StatusConflict, "WEBHOOK_REPLAYED", "Webhook replayed")
	case errors.Is(err, attackdefense.ErrRunKindNotAllowed):
		return writeValidation(c, "kind", "task type is not enabled for this module", "TASK_TYPE_NOT_ALLOWED")
	default:
		return writeProblem(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}

func traceID(c *echo.Context) string {
	if c != nil {
		if value, ok := c.Get(httpapi.TraceIDKey).(string); ok && value != "" {
			return value
		}
	}
	return uuid.NewString()
}

// decodeStrict decodes a bounded JSON object, rejects unknown fields and
// trailing data, and validates generated models.
func decodeStrict(body []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errTrailingJSON
	}
	return nil
}

var errTrailingJSON = errors.New("attackdefense: trailing JSON")
