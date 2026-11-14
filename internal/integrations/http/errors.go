package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/integrations"
)

func writeError(c *echo.Context, status int, code models.ErrorCode, message, field string) error {
	if message == "" {
		message = http.StatusText(status)
	}
	trace := traceID(c)
	path := c.Path()
	timestamp := strfmt.DateTime(time.Now().UTC())
	if status == http.StatusUnprocessableEntity {
		fieldName := field
		if fieldName == "" {
			fieldName = "body"
		}
		issue := message
		return c.JSON(status, &models.ValidationError{
			Code:        &code,
			Message:     &message,
			Path:        &path,
			Timestamp:   &timestamp,
			TraceID:     &trace,
			FieldErrors: []*models.FieldError{{Field: &fieldName, Issue: &issue, Code: "INVALID_VALUE"}},
		})
	}
	return c.JSON(status, &models.APIError{
		Code:      &code,
		Message:   &message,
		Path:      &path,
		Timestamp: &timestamp,
		TraceID:   &trace,
	})
}

func writeServiceError(c *echo.Context, err error) error {
	var apiErr *integrations.Error
	if errors.As(err, &apiErr) {
		return writeError(c, apiErr.Status, mapCode(apiErr.Code), apiErr.Message, apiErr.Field)
	}
	return writeError(c, http.StatusInternalServerError, models.ErrorCodeINTERNALERROR, "Internal server error", "")
}

func mapCode(code integrations.Code) models.ErrorCode {
	switch code {
	case integrations.CodeNotFound:
		return models.ErrorCodeINTEGRATIONNOTFOUND
	case integrations.CodeNameExists:
		return models.ErrorCodeINTEGRATIONNAMEALREADYEXISTS
	case integrations.CodeInUse:
		return models.ErrorCodeINTEGRATIONINUSE
	case integrations.CodeBaseURLNotAllowed:
		return models.ErrorCodeINTEGRATIONBASEURLNOTALLOWED
	case integrations.CodeConnectionFailed:
		return models.ErrorCodeINTEGRATIONCONNECTIONFAILED
	case integrations.CodeDependencyUnavail:
		return models.ErrorCodeDEPENDENCYUNAVAILABLE
	case integrations.CodeValidationFailed:
		return models.ErrorCodeVALIDATIONFAILED
	case integrations.CodeForbidden:
		return models.ErrorCodeFORBIDDEN
	case integrations.CodeUnauthorized:
		return models.ErrorCodeUNAUTHORIZED
	default:
		return models.ErrorCodeINTERNALERROR
	}
}

func traceID(c *echo.Context) models.UUID {
	if value, ok := c.Get("trace_id").(string); ok {
		if _, err := uuid.Parse(value); err == nil {
			return models.UUID(value)
		}
	}
	generated := uuid.NewString()
	c.Set("trace_id", generated)
	return models.UUID(generated)
}
