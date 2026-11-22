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

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

const (
	errorCodeUnauthorized         = oapimodels.ErrorCodeUNAUTHORIZED
	errorCodeForbidden            = oapimodels.ErrorCodeFORBIDDEN
	errorCodeValidationFailed     = oapimodels.ErrorCodeVALIDATIONFAILED
	errorCodeInvalidJSON          = oapimodels.ErrorCodeINVALIDJSON
	errorCodeUnsupportedMediaType = oapimodels.ErrorCodeUNSUPPORTEDMEDIATYPE
	errorCodeInternalError        = oapimodels.ErrorCodeINTERNALERROR
	errorCodeBoardNotFound        = oapimodels.ErrorCodeBOARDNOTFOUND
	errorCodeModuleNotFound       = oapimodels.ErrorCodeMODULENOTFOUND
	errorCodeUserNotFound         = oapimodels.ErrorCodeUSERNOTFOUND
	errorCodeUsernameExists       = oapimodels.ErrorCodeUSERNAMEALREADYEXISTS
	errorCodeEmailExists          = oapimodels.ErrorCodeEMAILALREADYEXISTS
	errorCodeAccessGroupNotFound  = oapimodels.ErrorCodeACCESSGROUPNOTFOUND
	errorCodeAccessGroupExists    = oapimodels.ErrorCodeACCESSGROUPALREADYEXISTS
	errorCodeAccessGroupInUse     = oapimodels.ErrorCodeACCESSGROUPINUSE
	errorCodeAudienceNotFound     = oapimodels.ErrorCodeAUDIENCENOTFOUND
	errorCodeAudienceExists       = oapimodels.ErrorCodeAUDIENCENAMEALREADYEXISTS
	errorCodeAudienceInUse        = oapimodels.ErrorCodeAUDIENCEINUSE
	errorCodeAudienceMemberExists = oapimodels.ErrorCodeAUDIENCEMEMBERALREADYEXISTS
	errorCodeAudienceMemberNo     = oapimodels.ErrorCodeAUDIENCEMEMBERNOTFOUND
	errorCodeLastAdmin            = oapimodels.ErrorCodeACTIONNOTSUPPORTED
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
	return &oapimodels.FieldError{Field: &field, Issue: &issue, Code: code}
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

func (h *Handler) writeServiceError(c *echo.Context, err error) error {
	switch {
	case stderrors.Is(err, access.ErrUnauthorized), stderrors.Is(err, access.ErrUserDisabled),
		stderrors.Is(err, authorization.ErrUnauthorized), stderrors.Is(err, authorization.ErrUserDisabled):
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required or credentials invalid")
	case stderrors.Is(err, access.ErrForbidden), stderrors.Is(err, authorization.ErrForbidden):
		return writeProblem(c, http.StatusForbidden, errorCodeForbidden, "Forbidden")
	case stderrors.Is(err, access.ErrBoardNotFound), stderrors.Is(err, authorization.ErrBoardNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeBoardNotFound, "Board not found")
	case stderrors.Is(err, access.ErrModuleNotFound), stderrors.Is(err, authorization.ErrModuleNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeModuleNotFound, "Module not found")
	case stderrors.Is(err, access.ErrUserNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeUserNotFound, "User not found")
	case stderrors.Is(err, access.ErrUsernameExists):
		return writeProblem(c, http.StatusConflict, errorCodeUsernameExists, "Username already exists")
	case stderrors.Is(err, access.ErrEmailExists):
		return writeProblem(c, http.StatusConflict, errorCodeEmailExists, "Email already exists")
	case stderrors.Is(err, access.ErrUserExists):
		return writeProblem(c, http.StatusConflict, errorCodeUsernameExists, "Username or email already exists")
	case stderrors.Is(err, access.ErrAccessGroupNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeAccessGroupNotFound, "Access group not found")
	case stderrors.Is(err, access.ErrAccessGroupExists):
		return writeProblem(c, http.StatusConflict, errorCodeAccessGroupExists, "Access group already exists")
	case stderrors.Is(err, access.ErrAccessGroupInUse):
		return writeProblem(c, http.StatusConflict, errorCodeAccessGroupInUse, "Access group is assigned to a board")
	case stderrors.Is(err, access.ErrAccessGroupMemberExists):
		return writeProblem(c, http.StatusConflict, oapimodels.ErrorCodeACCESSGROUPMEMBEREXISTS, "User is already a member of this group")
	case stderrors.Is(err, access.ErrAccessGroupMemberNotFound):
		return writeProblem(c, http.StatusNotFound, oapimodels.ErrorCodeACCESSGROUPMEMBERNOTFOUND, "User is not a member of this group")
	case stderrors.Is(err, access.ErrAudienceNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeAudienceNotFound, "Audience not found")
	case stderrors.Is(err, access.ErrAudienceExists):
		return writeProblem(c, http.StatusConflict, errorCodeAudienceExists, "Audience name already exists")
	case stderrors.Is(err, access.ErrAudienceInUse):
		return writeProblem(c, http.StatusConflict, errorCodeAudienceInUse, "Audience is in use")
	case stderrors.Is(err, access.ErrAudienceMemberExists):
		return writeProblem(c, http.StatusConflict, errorCodeAudienceMemberExists, "Audience member already exists")
	case stderrors.Is(err, access.ErrAudienceMemberNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeAudienceMemberNo, "Audience member not found")
	case stderrors.Is(err, access.ErrLastAdmin):
		return writeProblem(c, http.StatusConflict, errorCodeLastAdmin, "Cannot remove or disable the last enabled admin")
	case stderrors.Is(err, access.ErrInvalidRole):
		return validationProblem("roles", "Only user and admin global roles are allowed", "INVALID_VALUE").write(c)
	case stderrors.Is(err, access.ErrPublicBoardGroups):
		return validationProblem("accessGroupUuids", "Public boards cannot have access groups", "UNSUPPORTED_POLICY").write(c)
	case stderrors.Is(err, access.ErrInvalidSubject):
		return validationProblem("subject", "Invalid audience subject for this board", "INVALID_VALUE").write(c)
	case stderrors.Is(err, access.ErrInvalidInput):
		return validationProblem("body", "Invalid value", "INVALID_VALUE").write(c)
	default:
		return writeProblem(c, http.StatusInternalServerError, errorCodeInternalError, "Internal server error")
	}
}
