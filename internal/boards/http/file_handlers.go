package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/files"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

func (h *Handler) filesUnavailable() bool { return h.service.FileService() == nil }

func fileDraftModel(draft *files.Draft) *oapimodels.SubmissionDraft {
	id := oapimodels.UUID(draft.UUID.String())
	boardID := oapimodels.UUID(draft.BoardID.String())
	moduleID := oapimodels.UUID(draft.ModuleID.String())
	resourceType := oapimodels.SubmissionResourceType(draft.ResourceType)
	status := oapimodels.SubmissionDraftStatus(draft.Status)
	createdAt := strfmt.DateTime(draft.CreatedAt.UTC())
	expiresAt := strfmt.DateTime(draft.ExpiresAt.UTC())
	files := make([]*oapimodels.PendingFile, 0, len(draft.Files))
	for _, file := range draft.Files {
		files = append(files, pendingFileModel(file))
	}
	result := &oapimodels.SubmissionDraft{
		UUID: &id, BoardUUID: &boardID, ModuleUUID: &moduleID, ResourceType: &resourceType,
		Status: &status, CreatedAt: &createdAt, ExpiresAt: &expiresAt, Files: files,
	}
	if draft.TaskID != nil {
		result.TaskUUID.UUID = oapimodels.UUID(draft.TaskID.String())
	}
	return result
}

func pendingFileModel(file files.PendingFile) *oapimodels.PendingFile {
	id := oapimodels.UUID(file.Ref.UUID.String())
	filename := file.Ref.Filename
	contentType := file.Ref.ContentType
	size := file.Ref.Size
	sha := file.Ref.SHA256
	status := oapimodels.FileValidationStatus(file.Status)
	return &oapimodels.PendingFile{
		UUID: &id, Filename: &filename, ContentType: &contentType, Size: &size, Sha256: &sha,
		Status: &status, RejectionCode: file.RejectionCode,
	}
}

func (h *Handler) CreateTaskSubmissionDraft(c *echo.Context) error {
	if h.filesUnavailable() {
		return writeError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	taskID, problem := pathUUID(c, "taskUuid")
	if problem != nil {
		return problem.write(c)
	}
	draft, err := h.service.FileService().CreateDraft(c.Request().Context(), files.CreateDraftInput{
		Actor: principal, BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourceTask,
		TaskID: &taskID, TTL: 15 * time.Minute,
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusCreated, fileDraftModel(draft))
}

func (h *Handler) GetTaskSubmissionDraft(c *echo.Context) error {
	if h.filesUnavailable() {
		return writeError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	draftID, problem := pathUUID(c, "draftUuid")
	if problem != nil {
		return problem.write(c)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	taskID, problem := pathUUID(c, "taskUuid")
	if problem != nil {
		return problem.write(c)
	}
	draft, err := h.service.FileService().GetDraft(c.Request().Context(), principal, draftID)
	if err != nil {
		return writeError(c, err)
	}
	if draft.BoardID != boardID || draft.ModuleID != moduleID || draft.TaskID == nil || *draft.TaskID != taskID {
		return writeError(c, boards.ErrDraftForbidden)
	}
	return c.JSON(http.StatusOK, fileDraftModel(draft))
}

func (h *Handler) DiscardTaskSubmissionDraft(c *echo.Context) error {
	if h.filesUnavailable() {
		return writeError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	draftID, problem := pathUUID(c, "draftUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.service.FileService().DeleteDraft(c.Request().Context(), principal, draftID); err != nil {
		return writeError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) PresignTaskSubmissionFile(c *echo.Context) error {
	if h.filesUnavailable() {
		return writeError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	taskID, problem := pathUUID(c, "taskUuid")
	if problem != nil {
		return problem.write(c)
	}
	draftID, problem := pathUUID(c, "draftUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.ScopedFilePresignRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	result, err := h.service.FileService().Presign(c.Request().Context(), files.PresignInput{
		Actor: principal, BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourceTask,
		TaskID: &taskID, DraftID: draftID, Filename: *request.Filename, ContentType: *request.ContentType,
		Size: *request.Size, SHA256: *request.Sha256,
	})
	if err != nil {
		return writeError(c, err)
	}
	fileID := oapimodels.UUID(result.FileUUID.String())
	method := result.Method
	url := strfmt.URI(result.UploadURL)
	expires := strfmt.DateTime(result.ExpiresAt.UTC())
	return c.JSON(http.StatusCreated, &oapimodels.ScopedFilePresignResponse{
		FileUUID: &fileID, Method: &method, UploadURL: &url, ExpiresAt: &expires, RequiredHeaders: result.RequiredHeaders,
	})
}

func (h *Handler) CompleteTaskSubmissionFile(c *echo.Context) error {
	if h.filesUnavailable() {
		return writeError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	draftID, problem := pathUUID(c, "draftUuid")
	if problem != nil {
		return problem.write(c)
	}
	fileID, problem := pathUUID(c, "fileUuid")
	if problem != nil {
		return problem.write(c)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	taskID, problem := pathUUID(c, "taskUuid")
	if problem != nil {
		return problem.write(c)
	}
	file, err := h.service.FileService().Complete(c.Request().Context(), files.CompleteInput{
		Actor: principal, DraftID: draftID, FileUUID: fileID,
		BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourceTask, TaskID: &taskID,
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusAccepted, pendingFileModel(*file))
}

func (h *Handler) DownloadTaskAttachment(c *echo.Context) error {
	return h.downloadFile(c, true)
}

func (h *Handler) DownloadOwnTaskSubmissionFile(c *echo.Context) error {
	return h.downloadFile(c, false)
}

func (h *Handler) AdminDownloadSubmissionFile(c *echo.Context) error {
	if h.filesUnavailable() {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	submissionID, problem := pathUUID(c, "submissionUuid")
	if problem != nil {
		return problem.write(c)
	}
	fileID, problem := pathUUID(c, "fileUuid")
	if problem != nil {
		return problem.write(c)
	}
	submission, err := h.service.AdminGetSubmission(c.Request().Context(), principal, boardID, submissionID)
	if err != nil {
		return writeAdminError(c, err)
	}
	result, err := h.service.FileService().Download(c.Request().Context(), files.DownloadInput{
		Actor: principal, BoardID: boardID, ModuleID: submission.ModuleID, ResourceType: files.ResourceTask,
		TaskID: &submission.TaskID, SubmissionID: &submissionID, FileUUID: fileID, Admin: true,
	})
	if err != nil {
		return writeAdminError(c, err)
	}
	url := strfmt.URI(result.URL)
	expires := strfmt.DateTime(result.ExpiresAt.UTC())
	return c.JSON(http.StatusOK, &oapimodels.DownloadURLResponse{URL: &url, ExpiresAt: &expires})
}

func (h *Handler) downloadFile(c *echo.Context, admin bool) error {
	if h.filesUnavailable() {
		return writeError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var moduleID *uuid.UUID
	if value := c.Param("moduleUuid"); value != "" {
		parsed, problem := pathUUID(c, "moduleUuid")
		if problem != nil {
			return problem.write(c)
		}
		moduleID = &parsed
	}
	if moduleID == nil {
		return writeError(c, boards.ErrModuleNotFound)
	}
	var taskID, submissionID *uuid.UUID
	if value := c.Param("taskUuid"); value != "" {
		parsed, problem := pathUUID(c, "taskUuid")
		if problem != nil {
			return problem.write(c)
		}
		taskID = &parsed
	}
	if value := c.Param("submissionUuid"); value != "" {
		parsed, problem := pathUUID(c, "submissionUuid")
		if problem != nil {
			return problem.write(c)
		}
		submissionID = &parsed
	}
	fileID, problem := pathUUID(c, "fileUuid")
	if problem != nil {
		return problem.write(c)
	}
	result, err := h.service.FileService().Download(c.Request().Context(), files.DownloadInput{
		Actor: principal, BoardID: boardID, ModuleID: *moduleID, ResourceType: files.ResourceTask,
		TaskID: taskID, SubmissionID: submissionID, FileUUID: fileID, Admin: admin,
	})
	if err != nil {
		if admin {
			return writeAdminError(c, err)
		}
		return writeError(c, err)
	}
	url := strfmt.URI(result.URL)
	expires := strfmt.DateTime(result.ExpiresAt.UTC())
	return c.JSON(http.StatusOK, &oapimodels.DownloadURLResponse{URL: &url, ExpiresAt: &expires})
}

type attachmentFileService interface {
	AttachmentPresign(ctx context.Context, in files.AttachmentPresignInput) (*files.PresignResult, error)
	AttachmentComplete(ctx context.Context, in files.AttachmentCompleteInput) (*files.PendingFile, error)
	DeleteAttachmentInScope(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, resourceType string, taskID *uuid.UUID, fileID uuid.UUID) error
}

func (h *Handler) AdminPresignTaskAttachment(c *echo.Context) error {
	attachments, ok := h.service.FileService().(attachmentFileService)
	if !ok {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	taskID, problem := pathUUID(c, "taskUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.ScopedFilePresignRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	result, err := attachments.AttachmentPresign(c.Request().Context(), files.AttachmentPresignInput{
		Actor: principal, BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourceTask,
		TaskID: &taskID, Filename: *request.Filename, ContentType: *request.ContentType,
		Size: *request.Size, SHA256: *request.Sha256,
	})
	if err != nil {
		return writeAdminError(c, err)
	}
	fileID := oapimodels.UUID(result.FileUUID.String())
	method := result.Method
	url := strfmt.URI(result.UploadURL)
	expires := strfmt.DateTime(result.ExpiresAt.UTC())
	return c.JSON(http.StatusCreated, &oapimodels.ScopedFilePresignResponse{FileUUID: &fileID, Method: &method, UploadURL: &url, ExpiresAt: &expires, RequiredHeaders: result.RequiredHeaders})
}

func (h *Handler) AdminCompleteTaskAttachment(c *echo.Context) error {
	attachments, ok := h.service.FileService().(attachmentFileService)
	if !ok {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	taskID, problem := pathUUID(c, "taskUuid")
	if problem != nil {
		return problem.write(c)
	}
	fileID, problem := pathUUID(c, "fileUuid")
	if problem != nil {
		return problem.write(c)
	}
	file, err := attachments.AttachmentComplete(c.Request().Context(), files.AttachmentCompleteInput{
		Actor: principal, FileUUID: fileID, BoardID: boardID, ModuleID: moduleID,
		ResourceType: files.ResourceTask, TaskID: &taskID,
	})
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusAccepted, pendingFileModel(*file))
}

func (h *Handler) AdminDownloadTaskAttachment(c *echo.Context) error {
	return h.downloadFile(c, true)
}

func (h *Handler) AdminDeleteTaskAttachment(c *echo.Context) error {
	attachments, ok := h.service.FileService().(attachmentFileService)
	if !ok {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	taskID, problem := pathUUID(c, "taskUuid")
	if problem != nil {
		return problem.write(c)
	}
	fileID, problem := pathUUID(c, "fileUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := attachments.DeleteAttachmentInScope(c.Request().Context(), principal, boardID, moduleID, files.ResourceTask, &taskID, fileID); err != nil {
		return writeAdminError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
