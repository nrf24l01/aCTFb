package errorgen

import (
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/labstack/echo/v5"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func GenerateErrorResponse(c *echo.Context, error_code oapimodels.ErrorCode, error string) oapimodels.APIError {
	path := c.Path()
	timestamp := strfmt.DateTime(time.Now())
	traceIDHeader := c.Get("trace_id").(string)
	var traceID *oapimodels.UUID
	if traceIDHeader != "" {
		tid := oapimodels.UUID(traceIDHeader)
		traceID = &tid
	}

	return oapimodels.APIError{
		Code:      &error_code,
		Message:   &error,
		Path:      &path,
		Timestamp: &timestamp,
		TraceID:   traceID,
	}
}

func GenerateInternalServerErrorResponse(c *echo.Context) oapimodels.APIError {
	path := c.Path()
	timestamp := strfmt.DateTime(time.Now())
	traceIDHeader := c.Get("trace_id").(string)
	var traceID *oapimodels.UUID
	if traceIDHeader != "" {
		tid := oapimodels.UUID(traceIDHeader)
		traceID = &tid
	}
	error := "Internal server error"

	errorCode := oapimodels.ErrorCodeINTERNALERROR

	return oapimodels.APIError{
		Code:      &errorCode,
		Message:   &error,
		Path:      &path,
		Timestamp: &timestamp,
		TraceID:   traceID,
	}
}
