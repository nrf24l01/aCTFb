package http

import (
	"encoding/json"
	stderrors "errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	goerrors "github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

const maxBoardBodyBytes = 2 << 20

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

type rawBody map[string]json.RawMessage

// bindJSON enforces the existing JSON contract: application/json only,
// unknown top-level fields rejected, malformed JSON mapped to INVALID_JSON.
func bindJSON(c *echo.Context, dst any) (rawBody, *apiProblem) {
	contentType := c.Request().Header.Get("Content-Type")
	if contentType == "" {
		return nil, unsupportedMediaProblem()
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return nil, unsupportedMediaProblem()
	}

	body := http.MaxBytesReader(c.Response(), c.Request().Body, maxBoardBodyBytes)
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, invalidJSONProblem("Malformed JSON")
	}
	if len(raw) == 0 {
		return nil, invalidJSONProblem("Malformed JSON")
	}

	var presence rawBody
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return nil, malformedJSONProblem(err)
	}
	if err := decoder.Decode(&struct{}{}); !stderrors.Is(err, io.EOF) {
		return nil, malformedJSONProblem(err)
	}
	if err := json.Unmarshal(raw, &presence); err != nil {
		return nil, invalidJSONProblem("Malformed JSON")
	}
	return presence, nil
}

func unsupportedMediaProblem() *apiProblem {
	return &apiProblem{
		status: http.StatusUnsupportedMediaType, code: oapimodels.ErrorCodeUNSUPPORTEDMEDIATYPE,
		message: "Unsupported media type; expected application/json",
	}
}

func malformedJSONProblem(err error) *apiProblem {
	if err != nil {
		const unknownFieldPrefix = `json: unknown field "`
		if strings.HasPrefix(err.Error(), unknownFieldPrefix) {
			field := strings.TrimSuffix(strings.TrimPrefix(err.Error(), unknownFieldPrefix), `"`)
			if field == "" {
				field = "body"
			} else {
				field = "body." + field
			}
			return &apiProblem{
				status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
				message: "Validation failed", fieldErrors: []*oapimodels.FieldError{
					fieldError(field, "Unknown field", "UNKNOWN_FIELD"),
				},
			}
		}
		var maxBytesError *http.MaxBytesError
		if stderrors.As(err, &maxBytesError) {
			return &apiProblem{
				status: http.StatusRequestEntityTooLarge, code: oapimodels.ErrorCodePAYLOADTOOLARGE,
				message: "Payload too large",
			}
		}
	}
	return invalidJSONProblem("Malformed JSON")
}

func validateGenerated(model interface{ Validate(strfmt.Registry) error }) *apiProblem {
	if model == nil {
		return nil
	}
	if err := model.Validate(strfmt.Default); err != nil {
		return generatedValidationProblem(err)
	}
	return nil
}

func generatedValidationProblem(err error) *apiProblem {
	fields := collectValidationFields(err)
	if len(fields) == 0 {
		fields = []*oapimodels.FieldError{fieldError("body", "Invalid value", "INVALID_VALUE")}
	}
	return &apiProblem{
		status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
		message: "Validation failed", fieldErrors: fields,
	}
}

func collectValidationFields(err error) []*oapimodels.FieldError {
	if err == nil {
		return nil
	}
	var composite *goerrors.CompositeError
	if stderrors.As(err, &composite) {
		var fields []*oapimodels.FieldError
		for _, child := range composite.Errors {
			fields = append(fields, collectValidationFields(child)...)
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

func pathUUID(c *echo.Context, name string) (uuid.UUID, *apiProblem) {
	raw := c.Param(name)
	value, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, &apiProblem{
			status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
			message: "Validation failed", fieldErrors: []*oapimodels.FieldError{
				fieldError("path."+name, "Invalid UUID", "INVALID_FORMAT"),
			},
		}
	}
	return value, nil
}

func queryLimitOffset(c *echo.Context) (int, int, *apiProblem) {
	limit := 50
	offset := 0
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, &apiProblem{
				status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
				message: "Validation failed", fieldErrors: []*oapimodels.FieldError{
					fieldError("query.limit", "must be between 1 and 100", "OUT_OF_RANGE"),
				},
			}
		}
		limit = parsed
	}
	if raw := strings.TrimSpace(c.QueryParam("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, &apiProblem{
				status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
				message: "Validation failed", fieldErrors: []*oapimodels.FieldError{
					fieldError("query.offset", "must be at least 0", "OUT_OF_RANGE"),
				},
			}
		}
		offset = parsed
	}
	return limit, offset, nil
}

func querySearch(c *echo.Context) (string, *apiProblem) {
	value := strings.TrimSpace(c.QueryParam("query"))
	if len(value) > 128 {
		return "", &apiProblem{
			status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
			message: "Validation failed", fieldErrors: []*oapimodels.FieldError{
				fieldError("query.query", "must be at most 128 characters", "OUT_OF_RANGE"),
			},
		}
	}
	return value, nil
}

func idempotencyKey(c *echo.Context) (string, *apiProblem) {
	key := strings.TrimSpace(c.Request().Header.Get("Idempotency-Key"))
	if len(key) < 16 || len(key) > 128 || !idempotencyKeyPattern.MatchString(key) {
		return "", &apiProblem{
			status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
			message: "Validation failed", fieldErrors: []*oapimodels.FieldError{
				fieldError("header.Idempotency-Key", "required 16..128 characters matching ^[A-Za-z0-9._:-]+$", "INVALID_FORMAT"),
			},
		}
	}
	return key, nil
}

func dateTimePointer(value *strfmt.DateTime) *time.Time {
	if value == nil {
		return nil
	}
	parsed := time.Time(*value).UTC()
	return &parsed
}

func parsePresenceTime(raw rawBody, key string, value *strfmt.DateTime) (boards.OptionalTime, *apiProblem) {
	result := boards.OptionalTime{}
	body, present := raw[key]
	if !present {
		return result, nil
	}
	result.Set = true
	if string(body) == "null" {
		return result, nil
	}
	if value == nil {
		return result, &apiProblem{
			status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
			message: "Validation failed", fieldErrors: []*oapimodels.FieldError{
				fieldError("body."+key, "Invalid date-time", "INVALID_FORMAT"),
			},
		}
	}
	result.Value = dateTimePointer(value)
	return result, nil
}

func optionalString(value string, present bool) boards.OptionalString {
	if !present {
		return boards.OptionalString{}
	}
	return boards.OptionalString{Set: true, Value: stringValue(value)}
}

func stringValue(value string) *string { return &value }

func optionalInt(value int, present bool) boards.OptionalInt {
	if !present {
		return boards.OptionalInt{}
	}
	return boards.OptionalInt{Set: true, Value: intValue(value)}
}

func intValue(value int) *int { return &value }

func optionalBool(value bool, present bool) boards.OptionalBool {
	if !present {
		return boards.OptionalBool{}
	}
	return boards.OptionalBool{Set: true, Value: boolValue(value)}
}

func boolValue(value bool) *bool { return &value }

func isNull(raw rawBody, key string) bool {
	body, present := raw[key]
	return present && string(body) == "null"
}

func isPresent(raw rawBody, key string) bool {
	_, present := raw[key]
	return present
}

func nonEmptyObject(raw rawBody, key string) bool {
	body, present := raw[key]
	if !present || string(body) == "null" {
		return false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return true
	}
	for name, value := range object {
		if string(value) != "null" {
			_ = name
			return true
		}
	}
	return len(object) > 0
}

// rejectNullFields enforces non-nullable PATCH properties. Generated models
// collapse JSON null into zero values, so this check uses raw presence.
func rejectNullFields(raw rawBody, fields ...string) *apiProblem {
	for _, field := range fields {
		if isNull(raw, field) {
			return &apiProblem{
				status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
				message: "Validation failed", fieldErrors: []*oapimodels.FieldError{
					fieldError("body."+field, "must not be null", "INVALID_VALUE"),
				},
			}
		}
	}
	return nil
}
