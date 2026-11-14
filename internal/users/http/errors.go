package http

import (
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"strings"
	"time"

	goerrors "github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

type apiProblem struct {
	status      int
	code        oapimodels.ErrorCode
	message     string
	fieldErrors []*oapimodels.FieldError
	retryAfter  int
}

func (p apiProblem) write(c *echo.Context) error {
	noStore(c)

	if p.status == http.StatusTooManyRequests && p.retryAfter > 0 {
		c.Response().Header().Set("Retry-After", strconvItoa(p.retryAfter))
	}

	trace := traceID(c)
	path := requestPath(c)
	timestamp := strfmt.DateTime(time.Now().UTC())
	message := p.message
	if message == "" {
		message = http.StatusText(p.status)
	}
	code := p.code

	if len(p.fieldErrors) > 0 || p.code == oapimodels.ErrorCodeVALIDATIONFAILED {
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
				code:        oapimodels.ErrorCodeVALIDATIONFAILED,
				message:     "Validation failed",
				fieldErrors: []*oapimodels.FieldError{fieldError(field, "Unknown field", "UNKNOWN_FIELD")},
			}
		}
	}

	if err == nil || stderrors.Is(err, io.EOF) {
		return &apiProblem{
			status:  http.StatusBadRequest,
			code:    oapimodels.ErrorCodeINVALIDJSON,
			message: "Malformed JSON",
		}
	}

	var maxBytesError *http.MaxBytesError
	if stderrors.As(err, &maxBytesError) {
		return &apiProblem{
			status:  http.StatusBadRequest,
			code:    oapimodels.ErrorCodeINVALIDJSON,
			message: "Malformed JSON",
		}
	}

	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if stderrors.As(err, &syntaxError) || stderrors.As(err, &typeError) {
		return &apiProblem{
			status:  http.StatusBadRequest,
			code:    oapimodels.ErrorCodeINVALIDJSON,
			message: "Malformed JSON",
		}
	}

	return &apiProblem{
		status:  http.StatusBadRequest,
		code:    oapimodels.ErrorCodeINVALIDJSON,
		message: "Malformed JSON",
	}
}

func validationProblem(err error) *apiProblem {
	if err == nil {
		return nil
	}

	fields := collectValidationErrors(err)
	if len(fields) == 0 {
		fields = []*oapimodels.FieldError{fieldError("body", "Invalid value", "INVALID_VALUE")}
	}

	return &apiProblem{
		status:      http.StatusUnprocessableEntity,
		code:        oapimodels.ErrorCodeVALIDATIONFAILED,
		message:     "Validation failed",
		fieldErrors: fields,
	}
}

func collectValidationErrors(err error) []*oapimodels.FieldError {
	if err == nil {
		return nil
	}

	var composite *goerrors.CompositeError
	if stderrors.As(err, &composite) {
		var fields []*oapimodels.FieldError
		for _, child := range composite.Errors {
			fields = append(fields, collectValidationErrors(child)...)
		}
		return fields
	}

	var validation *goerrors.Validation
	if stderrors.As(err, &validation) {
		field := strings.TrimSpace(validation.Name)
		switch {
		case field == "":
			field = "body"
		case field == "body" || strings.HasPrefix(field, "body."):
		case strings.HasPrefix(field, "body["):
		default:
			field = "body." + field
		}

		issue := strings.TrimSpace(validation.Error())
		if issue == "" {
			issue = "Invalid value"
		}
		issue = truncate(issue, 1024)

		return []*oapimodels.FieldError{fieldError(field, issue, validationIssueCode(issue))}
	}

	return nil
}

func validationIssueCode(issue string) string {
	lower := strings.ToLower(issue)
	switch {
	case strings.Contains(lower, "required"):
		return "REQUIRED"
	case strings.Contains(lower, "length"), strings.Contains(lower, "minimum"), strings.Contains(lower, "maximum"):
		return "OUT_OF_RANGE"
	case strings.Contains(lower, "format"), strings.Contains(lower, "should match"), strings.Contains(lower, "pattern"):
		return "INVALID_FORMAT"
	default:
		return "INVALID_VALUE"
	}
}

func fieldError(field, issue, code string) *oapimodels.FieldError {
	field = truncate(field, 512)
	issue = truncate(issue, 1024)
	return &oapimodels.FieldError{
		Field: &field,
		Issue: &issue,
		Code:  code,
	}
}

func fieldRequired(field string) error {
	name := strings.TrimPrefix(field, "body.")
	return goerrors.Required(name, "body", nil)
}

func traceID(c *echo.Context) oapimodels.UUID {
	if value, ok := c.Get("trace_id").(string); ok {
		if _, err := uuid.Parse(value); err == nil {
			return oapimodels.UUID(value)
		}
	}
	if value, ok := c.Get("trace_id").(uuid.UUID); ok {
		return oapimodels.UUID(value.String())
	}
	if value, ok := c.Get("trace_id").(oapimodels.UUID); ok {
		if _, err := uuid.Parse(string(value)); err == nil {
			return value
		}
	}

	for _, header := range []string{"X-Trace-Id", "X-Request-Id"} {
		if value := strings.TrimSpace(c.Request().Header.Get(header)); value != "" {
			if _, err := uuid.Parse(value); err == nil {
				c.Set("trace_id", value)
				return oapimodels.UUID(value)
			}
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
	return truncate(path, 2048)
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func strconvItoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
