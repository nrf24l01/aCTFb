package http

import (
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

const (
	errorCodeUnauthorized         = oapimodels.ErrorCodeUNAUTHORIZED
	errorCodeForbidden            = oapimodels.ErrorCodeFORBIDDEN
	errorCodeValidationFailed     = oapimodels.ErrorCodeVALIDATIONFAILED
	errorCodeInvalidJSON          = oapimodels.ErrorCodeINVALIDJSON
	errorCodeBoardNotFound        = oapimodels.ErrorCodeBOARDNOTFOUND
	errorCodeBoardAccessDenied    = oapimodels.ErrorCodeBOARDACCESSDENIED
	errorCodeModuleNotFound       = oapimodels.ErrorCodeMODULENOTFOUND
	errorCodeUserNotFound         = oapimodels.ErrorCodeUSERNOTFOUND
	errorCodeInternalError        = oapimodels.ErrorCodeINTERNALERROR
	errorCodeUnsupportedMediaType = oapimodels.ErrorCodeUNSUPPORTEDMEDIATYPE

	errorCodeRoleBindingNotFound = oapimodels.ErrorCodeROLEBINDINGNOTFOUND
	errorCodeRoleGrantNotAllowed = oapimodels.ErrorCodeROLEGRANTNOTALLOWED

	// ROLE_BINDING_ALREADY_EXISTS is documented in the approved Swagger
	// response description but is not a generated ErrorCode enum value.
	errorCodeRoleBindingExists = oapimodels.ErrorCode("ROLE_BINDING_ALREADY_EXISTS")

	// Permission-grant codes are additive; the approved Swagger uses generic
	// 403/404/422 groups for these operations.
	errorCodePermissionGrantNotFound = oapimodels.ErrorCode("PERMISSION_GRANT_NOT_FOUND")
)

type apiProblem struct {
	status      int
	code        oapimodels.ErrorCode
	message     string
	fieldErrors []*oapimodels.FieldError
}

func (p apiProblem) write(c *echo.Context) error {
	noStore(c)
	trace := traceID(c)
	path := requestPath(c)
	timestamp := strfmt.DateTime(time.Now().UTC())
	message := p.message
	if message == "" {
		message = http.StatusText(p.status)
	}
	code := p.code
	if len(p.fieldErrors) > 0 || p.code == errorCodeValidationFailed {
		fields := p.fieldErrors
		if len(fields) == 0 {
			fields = []*oapimodels.FieldError{fieldError("body", "Invalid value", "INVALID_VALUE")}
		}
		return c.JSON(p.status, &oapimodels.ValidationError{
			Code:        &code,
			FieldErrors: fields,
			Message:     &message,
			Path:        &path,
			Timestamp:   &timestamp,
			TraceID:     &trace,
		})
	}
	return c.JSON(p.status, &oapimodels.APIError{
		Code:      &code,
		Message:   &message,
		Path:      &path,
		Timestamp: &timestamp,
		TraceID:   &trace,
	})
}

func writeProblem(c *echo.Context, status int, code oapimodels.ErrorCode, message string) error {
	return apiProblem{status: status, code: code, message: message}.write(c)
}

func validationProblem(field, issue, code string) *apiProblem {
	return &apiProblem{
		status:      http.StatusUnprocessableEntity,
		code:        errorCodeValidationFailed,
		message:     "Validation failed",
		fieldErrors: []*oapimodels.FieldError{fieldError(field, issue, code)},
	}
}

func malformedJSONProblem(err error) *apiProblem {
	if err != nil {
		const unknownFieldPrefix = `json: unknown field "`
		if strings.HasPrefix(err.Error(), unknownFieldPrefix) {
			field := strings.TrimSuffix(strings.TrimPrefix(err.Error(), unknownFieldPrefix), `"`)
			field = strings.TrimSpace(field)
			if field == "" {
				field = "body"
			} else {
				field = "body." + field
			}
			return &apiProblem{
				status:      http.StatusUnprocessableEntity,
				code:        errorCodeValidationFailed,
				message:     "Validation failed",
				fieldErrors: []*oapimodels.FieldError{fieldError(field, "Unknown field", "UNKNOWN_FIELD")},
			}
		}
	}
	if err == nil || stderrors.Is(err, io.EOF) {
		return &apiProblem{status: http.StatusBadRequest, code: errorCodeInvalidJSON, message: "Malformed JSON"}
	}
	var maxBytesError *http.MaxBytesError
	if stderrors.As(err, &maxBytesError) {
		return &apiProblem{status: http.StatusBadRequest, code: errorCodeInvalidJSON, message: "Malformed JSON"}
	}
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if stderrors.As(err, &syntaxError) || stderrors.As(err, &typeError) {
		return &apiProblem{status: http.StatusBadRequest, code: errorCodeInvalidJSON, message: "Malformed JSON"}
	}
	return &apiProblem{status: http.StatusBadRequest, code: errorCodeInvalidJSON, message: "Malformed JSON"}
}

func fieldError(field, issue, code string) *oapimodels.FieldError {
	return &oapimodels.FieldError{
		Field: &field,
		Issue: &issue,
		Code:  code,
	}
}

func traceID(c *echo.Context) oapimodels.UUID {
	if value, ok := c.Get("trace_id").(string); ok {
		if _, err := uuid.Parse(value); err == nil {
			return oapimodels.UUID(value)
		}
	}
	generated := uuid.NewString()
	c.Set("trace_id", generated)
	return oapimodels.UUID(generated)
}

func requestPath(c *echo.Context) string {
	path := c.Path()
	if path == "" && c.Request() != nil && c.Request().URL != nil {
		path = c.Request().URL.Path
	}
	if path == "" {
		path = "/"
	}
	if len(path) > 2048 {
		return path[:2045] + "..."
	}
	return path
}

func parseNullableTime(raw json.RawMessage, field string) (*time.Time, *apiProblem) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var value time.Time
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, validationProblem(field, "Invalid date-time", "INVALID_FORMAT")
	}
	return &value, nil
}
