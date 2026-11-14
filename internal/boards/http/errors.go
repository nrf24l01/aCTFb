package http

import (
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/files"
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
	c.Response().Header().Set("Cache-Control", "no-store")
	if p.status == http.StatusTooManyRequests && p.retryAfter > 0 {
		c.Response().Header().Set("Retry-After", itoa(p.retryAfter))
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
		path = path[:2048]
	}
	return path
}

func invalidJSONProblem(message string) *apiProblem {
	if message == "" {
		message = "Malformed JSON"
	}
	return &apiProblem{status: http.StatusBadRequest, code: oapimodels.ErrorCodeINVALIDJSON, message: message}
}

func validationProblem(err error) *apiProblem {
	if err == nil {
		return nil
	}
	var validation *boards.ValidationError
	if errors.As(err, &validation) {
		fields := make([]*oapimodels.FieldError, 0, len(validation.Fields))
		onlyTaskType := len(validation.Fields) > 0
		for _, item := range validation.Fields {
			fields = append(fields, fieldError(item.Field, item.Issue, item.Code))
			if item.Code != "TASK_TYPE_NOT_ALLOWED" {
				onlyTaskType = false
			}
		}
		code := oapimodels.ErrorCodeVALIDATIONFAILED
		if onlyTaskType {
			code = oapimodels.ErrorCodeTASKTYPENOTALLOWED
		}
		return &apiProblem{
			status: http.StatusUnprocessableEntity, code: code,
			message: "Validation failed", fieldErrors: fields,
		}
	}
	return &apiProblem{
		status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
		message: "Validation failed", fieldErrors: []*oapimodels.FieldError{fieldError("body", "Invalid value", "INVALID_VALUE")},
	}
}

// writeError maps domain sentinels onto the existing APIError codes.
func writeError(c *echo.Context, err error) error {
	return writeProblem(c, err, false)
}

// writeAdminError maps board-access denials on owner/admin routes to the
// documented generic FORBIDDEN rather than the participant BOARD_ACCESS_DENIED.
func writeAdminError(c *echo.Context, err error) error {
	return writeProblem(c, err, true)
}

func writeProblem(c *echo.Context, err error, admin bool) error {
	if err == nil {
		return internalProblem().write(c)
	}
	if problem := validationProblemWhen(err); problem != nil {
		return problem.write(c)
	}

	status, code, message := http.StatusInternalServerError, oapimodels.ErrorCodeINTERNALERROR, "Internal server error"
	retry := 0
	switch {
	case errors.Is(err, boards.ErrUnauthorized):
		status, code, message = http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required or credentials invalid"
	case errors.Is(err, boards.ErrForbidden):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeFORBIDDEN, "Access denied"
	case errors.Is(err, boards.ErrBoardAccessDenied):
		status = http.StatusForbidden
		if admin {
			code, message = oapimodels.ErrorCodeFORBIDDEN, "Access denied"
		} else {
			code, message = oapimodels.ErrorCodeBOARDACCESSDENIED, "Board access denied"
		}
	case errors.Is(err, boards.ErrRatingHidden):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeRATINGHIDDEN, "Rating is hidden"
	case errors.Is(err, boards.ErrBoardNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeBOARDNOTFOUND, "Board not found"
	case errors.Is(err, boards.ErrModuleNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeMODULENOTFOUND, "Module not found"
	case errors.Is(err, boards.ErrTaskNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeTASKNOTFOUND, "Task not found"
	case errors.Is(err, boards.ErrSubmissionNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeSUBMISSIONNOTFOUND, "Submission not found"
	case errors.Is(err, boards.ErrUserNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeUSERNOTFOUND, "User not found"
	case errors.Is(err, boards.ErrModuleNotAvailable):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeMODULENOTAVAILABLE, "Module not available"
	case errors.Is(err, boards.ErrTaskNotAvailable):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeTASKNOTAVAILABLE, "Task not available"
	case errors.Is(err, boards.ErrBoardStateConflict):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDSTATECONFLICT, "Board state conflict"
	case errors.Is(err, boards.ErrBoardSlugAlreadyExists):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDSLUGALREADYEXISTS, "Board slug already exists"
	case errors.Is(err, boards.ErrBoardDeleteNotAllowed):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDDELETENOTALLOWED, "Board cannot be deleted while history exists"
	case errors.Is(err, boards.ErrBoardRegistrationClosed):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDREGISTRATIONCLOSED, "Board registration is closed"
	case errors.Is(err, boards.ErrAlreadyJoined):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeALREADYJOINED, "Already joined"
	case errors.Is(err, boards.ErrBoardFinished):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDFINISHED, "Board is finished"
	case errors.Is(err, boards.ErrBoardNotStarted):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDNOTSTARTED, "Board has not started"
	case errors.Is(err, boards.ErrBoardNotActive):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDNOTACTIVE, "Board is not active"
	case errors.Is(err, boards.ErrBoardNotJoined):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDNOTJOINED, "Board not joined"
	case errors.Is(err, boards.ErrBoardLeaveNotAllowed):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDLEAVENOTALLOWED, "Board leave is not allowed while submissions exist"
	case errors.Is(err, boards.ErrModuleTypeMismatch):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeMODULETYPEMISMATCH, "Module type mismatch"
	case errors.Is(err, boards.ErrModuleStateConflict):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeMODULESTATECONFLICT, "Module state conflict"
	case errors.Is(err, boards.ErrModuleNameAlreadyExists):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeMODULENAMEALREADYEXISTS, "Module name already exists"
	case errors.Is(err, boards.ErrModuleDeleteNotAllowed):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeMODULEDELETENOTALLOWED, "Module cannot be deleted while history exists"
	case errors.Is(err, boards.ErrTaskTypeNotAllowed):
		status, code, message = http.StatusUnprocessableEntity, oapimodels.ErrorCodeTASKTYPENOTALLOWED, "Task type is not allowed"
	case errors.Is(err, boards.ErrTaskAlreadySolved):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeTASKALREADYSOLVED, "Task is already solved"
	case errors.Is(err, boards.ErrTaskInUse):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeTASKINUSE, "Task has submission history"
	case errors.Is(err, boards.ErrIdempotencyKeyReused):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeIDEMPOTENCYKEYREUSED, "Idempotency key was already used with a different request"
	case errors.Is(err, boards.ErrSubmissionRateLimited):
		status, code, message = http.StatusTooManyRequests, oapimodels.ErrorCodeSUBMISSIONRATELIMITED, "Submission rate limited"
		retry = retryAfterSeconds(err)
	case errors.Is(err, boards.ErrCheckerError):
		status, code, message = http.StatusInternalServerError, oapimodels.ErrorCodeCHECKERERROR, "Flag checker error"
	case errors.Is(err, boards.ErrDependencyUnavailable):
		status, code, message = http.StatusServiceUnavailable, oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE, "Service unavailable"
	case errors.Is(err, boards.ErrSubmissionAlreadyReviewed):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeSUBMISSIONALREADYREVIEWED, "Submission already reviewed"
	case errors.Is(err, boards.ErrSubmissionReviewNotAllowed):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeSUBMISSIONREVIEWNOTALLOWED, "Submission review is not allowed"
	case errors.Is(err, boards.ErrInvalidInviteCode):
		status, code, message = http.StatusUnprocessableEntity, oapimodels.ErrorCodeINVALIDINVITECODE, "Invalid invite code"
	case errors.Is(err, boards.ErrWrongParticipationMode):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeWRONGPARTICIPATIONMODE, "Wrong participation mode"
	case errors.Is(err, boards.ErrTeamNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeTEAMNOTFOUND, "Team not found"
	case errors.Is(err, boards.ErrTeamNameExists):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeTEAMNAMEALREADYEXISTS, "Team name already exists"
	case errors.Is(err, boards.ErrTeamFull):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeTEAMFULL, "Team is full"
	case errors.Is(err, boards.ErrUserAlreadyInTeam):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeUSERALREADYINTEAM, "User already in a team"
	case errors.Is(err, boards.ErrTeamCaptainRequired):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeTEAMCAPTAINREQUIRED, "Team captain required"
	case errors.Is(err, boards.ErrTeamCaptainMustTransfer):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeTEAMCAPTAINMUSTTRANSFER, "Team captain must transfer"
	case errors.Is(err, boards.ErrTeamMemberNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeTEAMMEMBERNOTFOUND, "Team member not found"
	case errors.Is(err, boards.ErrTeamNotEmpty):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeTEAMNOTEMPTY, "Team is not empty"
	case errors.Is(err, boards.ErrInvalidTeamInviteKey):
		status, code, message = http.StatusUnprocessableEntity, oapimodels.ErrorCodeINVALIDTEAMINVITEKEY, "Invalid team invite key"
	case errors.Is(err, boards.ErrBoardTeamChangesLocked):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeBOARDTEAMCHANGESLOCKED, "Board team changes are locked"
	case errors.Is(err, boards.ErrTeamRequired):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeWRONGPARTICIPATIONMODE, "Team membership required"
	case errors.Is(err, boards.ErrAudienceNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeAUDIENCENOTFOUND, "Audience not found"
	case errors.Is(err, boards.ErrAccessGroupNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeACCESSGROUPNOTFOUND, "Access group not found"
	case errors.Is(err, boards.ErrDraftNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeSUBMISSIONDRAFTNOTFOUND, "Submission draft not found"
	case errors.Is(err, boards.ErrDraftForbidden):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeSUBMISSIONDRAFTACCESSDENIED, "Submission draft access denied"
	case errors.Is(err, boards.ErrDraftExpired):
		status, code, message = http.StatusGone, oapimodels.ErrorCodeSUBMISSIONDRAFTEXPIRED, "Submission draft expired"
	case errors.Is(err, boards.ErrDraftNotReady):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeSUBMISSIONDRAFTALREADYUSED, "Submission draft is not ready"
	case errors.Is(err, files.ErrDraftNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeSUBMISSIONDRAFTNOTFOUND, "Submission draft not found"
	case errors.Is(err, files.ErrDraftExpired):
		status, code, message = http.StatusGone, oapimodels.ErrorCodeSUBMISSIONDRAFTEXPIRED, "Submission draft expired"
	case errors.Is(err, files.ErrDraftAlreadyUsed), errors.Is(err, files.ErrFileNotReady), errors.Is(err, files.ErrFileValidationPending):
		status, code, message = http.StatusConflict, oapimodels.ErrorCodeSUBMISSIONDRAFTALREADYUSED, "Submission draft is not ready"
	case errors.Is(err, files.ErrForbidden), errors.Is(err, files.ErrFileContextMismatch):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeSUBMISSIONDRAFTACCESSDENIED, "Submission draft access denied"
	case errors.Is(err, files.ErrDependencyUnavailable), errors.Is(err, files.ErrScannerUnavailable), errors.Is(err, files.ErrS3PresignFailed):
		status, code, message = http.StatusServiceUnavailable, oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE, "Service unavailable"
	case errors.Is(err, boards.ErrFileNotFound):
		status, code, message = http.StatusNotFound, oapimodels.ErrorCodeFILENOTFOUND, "File not found"
	case errors.Is(err, boards.ErrRoleGrantNotAllowed):
		status, code, message = http.StatusForbidden, oapimodels.ErrorCodeROLEGRANTNOTALLOWED, "Role grant not allowed"
	}
	problem := &apiProblem{status: status, code: code, message: message, retryAfter: retry}
	return problem.write(c)
}

func validationProblemWhen(err error) *apiProblem {
	var validation *boards.ValidationError
	if errors.As(err, &validation) {
		return validationProblem(err)
	}
	return nil
}

func internalProblem() *apiProblem {
	return &apiProblem{status: http.StatusInternalServerError, code: oapimodels.ErrorCodeINTERNALERROR, message: "Internal server error"}
}

func retryAfterSeconds(err error) int {
	var limitErr *boards.RateLimitError
	if errors.As(err, &limitErr) && limitErr.RetryAfter > 0 {
		seconds := int(math.Ceil(limitErr.RetryAfter.Seconds()))
		if seconds < 1 {
			return 1
		}
		return seconds
	}
	// Older callers only carried the sentinel; keep a safe conservative value.
	return 60
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	index := len(buf)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		index--
		buf[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buf[index] = '-'
	}
	return string(buf[index:])
}

func (h *Handler) writeAccessError(c *echo.Context, err error, admin bool) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, access.ErrUnauthorized), errors.Is(err, access.ErrUserDisabled):
		err = boards.ErrUnauthorized
	case errors.Is(err, access.ErrForbidden):
		err = boards.ErrForbidden
	case errors.Is(err, access.ErrBoardNotFound):
		err = boards.ErrBoardNotFound
	case errors.Is(err, access.ErrModuleNotFound):
		err = boards.ErrModuleNotFound
	case errors.Is(err, access.ErrAudienceNotFound):
		err = boards.ErrAudienceNotFound
	case errors.Is(err, access.ErrAudienceExists):
		err = boards.Invalid("body.name", "audience name already exists", "ALREADY_EXISTS")
	case errors.Is(err, access.ErrAudienceInUse):
		err = boards.Invalid("path.audienceUuid", "audience is used by a module", "IN_USE")
	case errors.Is(err, access.ErrAudienceMemberExists):
		err = boards.Invalid("body.subjectUuid", "audience member already exists", "ALREADY_EXISTS")
	case errors.Is(err, access.ErrAudienceMemberNotFound):
		err = boards.ErrAudienceNotFound
	case errors.Is(err, access.ErrInvalidSubject):
		err = boards.Invalid("body.subjectUuid", "subject is invalid or not a board participant", "INVALID_VALUE")
	case errors.Is(err, access.ErrPublicBoardGroups):
		err = boards.Invalid("body.accessGroupUuids", "public boards cannot use access groups", "INVALID_VALUE")
	case errors.Is(err, access.ErrNoDatabase), errors.Is(err, access.ErrNoAuthorizer):
		err = boards.ErrDependencyUnavailable
	}
	if admin {
		return writeAdminError(c, err)
	}
	return writeError(c, err)
}
