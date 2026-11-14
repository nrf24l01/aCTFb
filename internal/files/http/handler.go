// Package http maps the 23 generated file/draft/attachment operations onto the
// files domain service. It never defines a parallel API envelope and never
// exposes bucket, key, version or raw file IDs in create/patch payloads.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/files"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/users"
)

// Handler is the files HTTP adapter.
type Handler struct {
	service *files.FileService
	auth    *users.UserService
}

func NewHandler(service *files.FileService, auth *users.UserService) *Handler {
	return &Handler{service: service, auth: auth}
}

// RegisterRoutes installs exactly the 23 documented operations. Boot wiring is
// owned by the main agent; this method is additive and does not edit the router.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	if e == nil {
		return
	}
	requireUser := httpapi.RequireUser(h.auth)

	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts", h.CreateTaskDraft, requireUser)
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid", h.GetTaskDraft, requireUser)
	e.DELETE("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid", h.DeleteTaskDraft, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid/files/presign", h.PresignTaskFile, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submission-drafts/:draftUuid/files/:fileUuid/complete", h.CompleteTaskFile, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid/download-url", h.DownloadTaskAttachment, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/submissions/:submissionUuid/files/:fileUuid/download-url", h.DownloadOwnTaskSubmissionFile, requireUser)

	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts", h.CreatePentestDraft, requireUser)
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts/:draftUuid", h.GetPentestDraft, requireUser)
	e.DELETE("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts/:draftUuid", h.DeletePentestDraft, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts/:draftUuid/files/presign", h.PresignPentestFile, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submission-drafts/:draftUuid/files/:fileUuid/complete", h.CompletePentestFile, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/:fileUuid/download-url", h.DownloadPentestAttachment, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submissions/:submissionUuid/files/:fileUuid/download-url", h.DownloadOwnPentestSubmissionFile, requireUser)

	e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/presign", h.AdminPresignTaskAttachment, requireUser)
	e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid/complete", h.AdminCompleteTaskAttachment, requireUser)
	e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid/download-url", h.AdminDownloadTaskAttachment, requireUser)
	e.DELETE("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/attachments/:fileUuid", h.AdminDeleteTaskAttachment, requireUser)
	e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/presign", h.AdminPresignPentestAttachment, requireUser)
	e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/:fileUuid/complete", h.AdminCompletePentestAttachment, requireUser)
	e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/:fileUuid/download-url", h.AdminDownloadPentestAttachment, requireUser)
	e.DELETE("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/attachments/:fileUuid", h.AdminDeletePentestAttachment, requireUser)
	e.POST("/api/v2/admin/boards/:boardUuid/submissions/:submissionUuid/files/:fileUuid/download-url", h.AdminDownloadSubmissionFile, requireUser)
}

func principal(c *echo.Context) (authorization.Principal, bool) {
	return httpapi.CurrentPrincipal(c)
}

func pathUUID(c *echo.Context, name string) (uuid.UUID, error) {
	value, err := uuid.Parse(c.Param(name))
	if err != nil || value == uuid.Nil {
		return uuid.Nil, files.Invalid(name, "must be a UUID", "INVALID_FORMAT")
	}
	return value, nil
}

func bind(c *echo.Context, target any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request().Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return files.Invalid("body", "malformed JSON body", "INVALID_JSON")
	}
	return nil
}

func validate(target interface{ Validate(strfmt.Registry) error }) error {
	if err := target.Validate(strfmt.Default); err != nil {
		return files.Invalid("body", err.Error(), "INVALID_VALUE")
	}
	return nil
}

type problem struct {
	status  int
	code    oapimodels.ErrorCode
	message string
	field   string
	issue   string
}

func (p problem) write(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	trace := traceID(c)
	path := c.Request().URL.Path
	ts := strfmt.DateTime(time.Now().UTC())
	message := p.message
	if message == "" {
		message = http.StatusText(p.status)
	}
	if p.field != "" || p.code == oapimodels.ErrorCodeVALIDATIONFAILED {
		field := p.field
		if field == "" {
			field = "body"
		}
		issue := p.issue
		if issue == "" {
			issue = "Invalid value"
		}
		code := p.code
		return c.JSON(p.status, &oapimodels.ValidationError{
			Code: &code, FieldErrors: []*oapimodels.FieldError{{
				Field: &field, Issue: &issue, Code: "INVALID_VALUE",
			}}, Message: &message, Path: &path, Timestamp: &ts, TraceID: &trace,
		})
	}
	code := p.code
	return c.JSON(p.status, &oapimodels.APIError{
		Code: &code, Message: &message, Path: &path, Timestamp: &ts, TraceID: &trace,
	})
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

func writeError(c *echo.Context, err error) error {
	var validation *files.ValidationError
	if errors.As(err, &validation) {
		return problem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
			message: "Validation failed", field: validation.Field, issue: validation.Issue}.write(c)
	}
	switch {
	case errors.Is(err, files.ErrUnauthorized):
		return problem{status: http.StatusUnauthorized, code: oapimodels.ErrorCodeUNAUTHORIZED, message: "Authentication required"}.write(c)
	case errors.Is(err, files.ErrForbidden):
		return problem{status: http.StatusForbidden, code: oapimodels.ErrorCodeFILEACCESSDENIED, message: "Access denied"}.write(c)
	case errors.Is(err, files.ErrBoardNotFound):
		return problem{status: http.StatusNotFound, code: oapimodels.ErrorCodeBOARDNOTFOUND, message: "Board not found"}.write(c)
	case errors.Is(err, files.ErrModuleNotFound):
		return problem{status: http.StatusNotFound, code: oapimodels.ErrorCodeMODULENOTFOUND, message: "Module not found"}.write(c)
	case errors.Is(err, files.ErrTaskNotFound):
		return problem{status: http.StatusNotFound, code: oapimodels.ErrorCodeTASKNOTFOUND, message: "Task not found"}.write(c)
	case errors.Is(err, files.ErrDraftNotFound):
		return problem{status: http.StatusNotFound, code: oapimodels.ErrorCodeSUBMISSIONDRAFTNOTFOUND, message: "Submission draft not found"}.write(c)
	case errors.Is(err, files.ErrFileNotFound):
		return problem{status: http.StatusNotFound, code: oapimodels.ErrorCodeFILENOTFOUND, message: "File not found"}.write(c)
	case errors.Is(err, files.ErrSubmissionNotFound):
		return problem{status: http.StatusNotFound, code: oapimodels.ErrorCodeSUBMISSIONNOTFOUND, message: "Submission not found"}.write(c)
	case errors.Is(err, files.ErrDraftExpired):
		return problem{status: http.StatusGone, code: oapimodels.ErrorCodeSUBMISSIONDRAFTEXPIRED, message: "Submission draft expired"}.write(c)
	case errors.Is(err, files.ErrDraftAlreadyUsed):
		return problem{status: http.StatusConflict, code: oapimodels.ErrorCodeSUBMISSIONDRAFTALREADYUSED, message: "Submission draft already used"}.write(c)
	case errors.Is(err, files.ErrDraftAlreadyOpen):
		return problem{status: http.StatusConflict, code: oapimodels.ErrorCodeSUBMISSIONDRAFTALREADYOPEN, message: "An open submission draft already exists"}.write(c)
	case errors.Is(err, files.ErrFileAlreadyAttached):
		return problem{status: http.StatusConflict, code: oapimodels.ErrorCodeFILEALREADYATTACHED, message: "File already attached"}.write(c)
	case errors.Is(err, files.ErrFileInUse):
		return problem{status: http.StatusConflict, code: oapimodels.ErrorCodeFILEINUSE, message: "File is in use"}.write(c)
	case errors.Is(err, files.ErrFileValidationPending):
		return problem{status: http.StatusConflict, code: oapimodels.ErrorCodeFILEVALIDATIONPENDING, message: "File validation pending"}.write(c)
	case errors.Is(err, files.ErrFileNotReady):
		return problem{status: http.StatusConflict, code: oapimodels.ErrorCodeFILENOTREADY, message: "File is not ready"}.write(c)
	case errors.Is(err, files.ErrFileContextMismatch):
		return problem{status: http.StatusConflict, code: oapimodels.ErrorCodeFILECONTEXTMISMATCH, message: "File context mismatch"}.write(c)
	case errors.Is(err, files.ErrUnsupportedFileType):
		return problem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeUNSUPPORTEDFILETYPE, message: "Unsupported file type"}.write(c)
	case errors.Is(err, files.ErrFileTooLarge):
		return problem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeFILETOOLARGE, message: "File too large"}.write(c)
	case errors.Is(err, files.ErrFileSizeMismatch):
		return problem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeFILESIZEMISMATCH, message: "File size mismatch"}.write(c)
	case errors.Is(err, files.ErrFileChecksumMismatch):
		return problem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeFILECHECKSUMMISMATCH, message: "File checksum mismatch"}.write(c)
	case errors.Is(err, files.ErrFilesNotAllowed):
		return problem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeSUBMISSIONFILESNOTALLOWED, message: "Submission files are not allowed"}.write(c)
	case errors.Is(err, files.ErrFileLimitReached):
		return problem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeSUBMISSIONFILELIMITREACHED, message: "Submission file limit reached"}.write(c)
	case errors.Is(err, files.ErrFileUploadExpired):
		return problem{status: http.StatusGone, code: oapimodels.ErrorCodeFILEUPLOADEXPIRED, message: "File upload expired"}.write(c)
	case errors.Is(err, files.ErrModuleNotAvailable):
		return problem{status: http.StatusForbidden, code: oapimodels.ErrorCodeMODULENOTAVAILABLE, message: "Module not available"}.write(c)
	case errors.Is(err, files.ErrTaskNotAvailable):
		return problem{status: http.StatusForbidden, code: oapimodels.ErrorCodeTASKNOTAVAILABLE, message: "Task not available"}.write(c)
	case errors.Is(err, files.ErrS3PresignFailed):
		return problem{status: http.StatusInternalServerError, code: oapimodels.ErrorCodeS3PRESIGNFAILED, message: "S3 presign failed"}.write(c)
	case errors.Is(err, files.ErrDependencyUnavailable), errors.Is(err, files.ErrScannerUnavailable):
		return problem{status: http.StatusServiceUnavailable, code: oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE, message: "Dependency unavailable"}.write(c)
	default:
		return problem{status: http.StatusInternalServerError, code: oapimodels.ErrorCodeINTERNALERROR, message: "Internal server error"}.write(c)
	}
}

func draftModel(draft *files.Draft) *oapimodels.SubmissionDraft {
	resourceType := oapimodels.SubmissionResourceType(draft.ResourceType)
	status := oapimodels.SubmissionDraftStatus(draft.Status)
	filesList := make([]*oapimodels.PendingFile, 0, len(draft.Files))
	for i := range draft.Files {
		filesList = append(filesList, pendingFileModel(&draft.Files[i]))
	}
	model := &oapimodels.SubmissionDraft{
		UUID: fileUUID(draft.UUID), BoardUUID: fileUUID(draft.BoardID),
		ModuleUUID: fileUUID(draft.ModuleID), ResourceType: &resourceType,
		Status: &status, CreatedAt: dateTime(draft.CreatedAt), ExpiresAt: dateTime(draft.ExpiresAt),
		Files: filesList,
	}
	if draft.TaskID != nil {
		model.TaskUUID.UUID = oapimodels.UUID(draft.TaskID.String())
	}
	return model
}

func pendingFileModel(file *files.PendingFile) *oapimodels.PendingFile {
	status := oapimodels.FileValidationStatus(file.Status)
	model := &oapimodels.PendingFile{
		UUID: fileUUID(file.UUID), Filename: &file.Filename,
		ContentType: &file.ContentType, Size: &file.Size, Sha256: &file.SHA256,
		Status: &status, RejectionCode: file.RejectionCode,
	}
	return model
}

func presignModel(result *files.PresignResult) *oapimodels.ScopedFilePresignResponse {
	method := result.Method
	uri := strfmt.URI(result.UploadURL)
	return &oapimodels.ScopedFilePresignResponse{
		FileUUID: fileUUID(result.FileUUID), Method: &method, UploadURL: &uri,
		RequiredHeaders: result.RequiredHeaders, ExpiresAt: dateTime(result.ExpiresAt),
	}
}

func downloadModel(result *files.DownloadResult) *oapimodels.DownloadURLResponse {
	uri := strfmt.URI(result.URL)
	return &oapimodels.DownloadURLResponse{URL: &uri, ExpiresAt: dateTime(result.ExpiresAt)}
}

func fileUUID(value uuid.UUID) *oapimodels.UUID {
	id := oapimodels.UUID(value.String())
	return &id
}

func dateTime(value time.Time) *strfmt.DateTime {
	result := strfmt.DateTime(value.UTC())
	return &result
}
