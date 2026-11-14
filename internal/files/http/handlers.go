package http

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/files"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func (h *Handler) CreateTaskDraft(c *echo.Context) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	boardID, err := pathUUID(c, "boardUuid")
	if err != nil {
		return writeError(c, err)
	}
	moduleID, err := pathUUID(c, "moduleUuid")
	if err != nil {
		return writeError(c, err)
	}
	taskID, err := pathUUID(c, "taskUuid")
	if err != nil {
		return writeError(c, err)
	}
	draft, err := h.service.CreateDraft(c.Request().Context(), files.CreateDraftInput{
		Actor: actor, BoardID: boardID, ModuleID: moduleID,
		ResourceType: files.ResourceTask, TaskID: &taskID,
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusCreated, draftModel(draft))
}

func (h *Handler) CreatePentestDraft(c *echo.Context) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	boardID, err := pathUUID(c, "boardUuid")
	if err != nil {
		return writeError(c, err)
	}
	moduleID, err := pathUUID(c, "moduleUuid")
	if err != nil {
		return writeError(c, err)
	}
	draft, err := h.service.CreateDraft(c.Request().Context(), files.CreateDraftInput{
		Actor: actor, BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourcePentest,
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusCreated, draftModel(draft))
}

func (h *Handler) getDraft(c *echo.Context, resourceType string, withTask bool) (*files.Draft, error) {
	actor, ok := principal(c)
	if !ok {
		return nil, files.ErrUnauthorized
	}
	boardID, err := pathUUID(c, "boardUuid")
	if err != nil {
		return nil, err
	}
	moduleID, err := pathUUID(c, "moduleUuid")
	if err != nil {
		return nil, err
	}
	draftID, err := pathUUID(c, "draftUuid")
	if err != nil {
		return nil, err
	}
	draft, err := h.service.GetDraft(c.Request().Context(), actor, draftID)
	if err != nil {
		return nil, err
	}
	if draft.BoardID != boardID || draft.ModuleID != moduleID || draft.ResourceType != resourceType {
		return nil, files.ErrDraftNotFound
	}
	if withTask {
		id, err := pathUUID(c, "taskUuid")
		if err != nil {
			return nil, err
		}
		if draft.TaskID == nil || *draft.TaskID != id {
			return nil, files.ErrDraftNotFound
		}
	} else if draft.TaskID != nil {
		return nil, files.ErrDraftNotFound
	}
	return draft, nil
}

func (h *Handler) GetTaskDraft(c *echo.Context) error {
	draft, err := h.getDraft(c, files.ResourceTask, true)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, draftModel(draft))
}

func (h *Handler) GetPentestDraft(c *echo.Context) error {
	draft, err := h.getDraft(c, files.ResourcePentest, false)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, draftModel(draft))
}

func (h *Handler) DeleteTaskDraft(c *echo.Context) error {
	return h.deleteDraft(c, files.ResourceTask, true)
}

func (h *Handler) DeletePentestDraft(c *echo.Context) error {
	return h.deleteDraft(c, files.ResourcePentest, false)
}

func (h *Handler) deleteDraft(c *echo.Context, resourceType string, withTask bool) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	draft, err := h.getDraft(c, resourceType, withTask)
	if err != nil {
		return writeError(c, err)
	}
	if err := h.service.DeleteDraft(c.Request().Context(), actor, draft.UUID); err != nil {
		return writeError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) presign(c *echo.Context, resourceType string) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	boardID, err := pathUUID(c, "boardUuid")
	if err != nil {
		return writeError(c, err)
	}
	moduleID, err := pathUUID(c, "moduleUuid")
	if err != nil {
		return writeError(c, err)
	}
	draftID, err := pathUUID(c, "draftUuid")
	if err != nil {
		return writeError(c, err)
	}
	var taskID *uuid.UUID
	if resourceType == files.ResourceTask {
		id, err := pathUUID(c, "taskUuid")
		if err != nil {
			return writeError(c, err)
		}
		taskID = &id
	}
	var request oapimodels.ScopedFilePresignRequest
	if err := bind(c, &request); err != nil {
		return writeError(c, err)
	}
	if err := validate(&request); err != nil {
		return writeError(c, err)
	}
	result, err := h.service.Presign(c.Request().Context(), files.PresignInput{
		Actor: actor, BoardID: boardID, ModuleID: moduleID, ResourceType: resourceType,
		TaskID: taskID, DraftID: draftID,
		Filename: derefString(request.Filename), ContentType: derefString(request.ContentType),
		Size: derefInt64(request.Size), SHA256: derefString(request.Sha256),
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusCreated, presignModel(result))
}

// Small aliases keep the handler free of pointer-type noise without changing
// the service contract.
func (h *Handler) PresignTaskFile(c *echo.Context) error {
	return h.presign(c, files.ResourceTask)
}

func (h *Handler) PresignPentestFile(c *echo.Context) error {
	return h.presign(c, files.ResourcePentest)
}

func (h *Handler) completeDraft(c *echo.Context, resourceType string, withTask bool) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	draftID, err := pathUUID(c, "draftUuid")
	if err != nil {
		return writeError(c, err)
	}
	fileID, err := pathUUID(c, "fileUuid")
	if err != nil {
		return writeError(c, err)
	}
	boardID, moduleID, err := boardModule(c)
	if err != nil {
		return writeError(c, err)
	}
	var taskID *uuid.UUID
	if withTask {
		id, err := pathUUID(c, "taskUuid")
		if err != nil {
			return writeError(c, err)
		}
		taskID = &id
	}
	pending, err := h.service.Complete(c.Request().Context(), files.CompleteInput{
		Actor: actor, DraftID: draftID, FileUUID: fileID,
		BoardID: boardID, ModuleID: moduleID, ResourceType: resourceType, TaskID: taskID,
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusAccepted, pendingFileModel(pending))
}

func (h *Handler) CompleteTaskFile(c *echo.Context) error {
	return h.completeDraft(c, files.ResourceTask, true)
}
func (h *Handler) CompletePentestFile(c *echo.Context) error {
	return h.completeDraft(c, files.ResourcePentest, false)
}

func (h *Handler) download(c *echo.Context, in files.DownloadInput) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	in.Actor = actor
	result, err := h.service.Download(c.Request().Context(), in)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, downloadModel(result))
}

func (h *Handler) DownloadTaskAttachment(c *echo.Context) error {
	boardID, moduleID, taskID, fileID, err := taskDownloadPath(c)
	if err != nil {
		return writeError(c, err)
	}
	return h.download(c, files.DownloadInput{
		BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourceTask,
		TaskID: &taskID, FileUUID: fileID,
	})
}

func (h *Handler) DownloadOwnTaskSubmissionFile(c *echo.Context) error {
	boardID, moduleID, taskID, err := boardModuleTask(c)
	if err != nil {
		return writeError(c, err)
	}
	submissionID, err := pathUUID(c, "submissionUuid")
	if err != nil {
		return writeError(c, err)
	}
	fileID, err := pathUUID(c, "fileUuid")
	if err != nil {
		return writeError(c, err)
	}
	return h.download(c, files.DownloadInput{
		BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourceTask,
		TaskID: &taskID, SubmissionID: &submissionID, FileUUID: fileID,
	})
}

func (h *Handler) DownloadPentestAttachment(c *echo.Context) error {
	boardID, moduleID, err := boardModule(c)
	if err != nil {
		return writeError(c, err)
	}
	fileID, err := pathUUID(c, "fileUuid")
	if err != nil {
		return writeError(c, err)
	}
	return h.download(c, files.DownloadInput{
		BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourcePentest, FileUUID: fileID,
	})
}

func (h *Handler) DownloadOwnPentestSubmissionFile(c *echo.Context) error {
	boardID, moduleID, err := boardModule(c)
	if err != nil {
		return writeError(c, err)
	}
	submissionID, err := pathUUID(c, "submissionUuid")
	if err != nil {
		return writeError(c, err)
	}
	fileID, err := pathUUID(c, "fileUuid")
	if err != nil {
		return writeError(c, err)
	}
	return h.download(c, files.DownloadInput{
		BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourcePentest,
		SubmissionID: &submissionID, FileUUID: fileID,
	})
}

func (h *Handler) adminPresign(c *echo.Context, resourceType string) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	boardID, moduleID, err := boardModule(c)
	if err != nil {
		return writeError(c, err)
	}
	var taskID *uuid.UUID
	if resourceType == files.ResourceTask {
		id, err := pathUUID(c, "taskUuid")
		if err != nil {
			return writeError(c, err)
		}
		taskID = &id
	}
	var request oapimodels.ScopedFilePresignRequest
	if err := bind(c, &request); err != nil {
		return writeError(c, err)
	}
	if err := validate(&request); err != nil {
		return writeError(c, err)
	}
	result, err := h.service.AttachmentPresign(c.Request().Context(), files.AttachmentPresignInput{
		Actor: actor, BoardID: boardID, ModuleID: moduleID, ResourceType: resourceType,
		TaskID: taskID, Filename: derefString(request.Filename),
		ContentType: derefString(request.ContentType), Size: derefInt64(request.Size),
		SHA256: derefString(request.Sha256),
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusCreated, presignModel(result))
}

func (h *Handler) AdminPresignTaskAttachment(c *echo.Context) error {
	return h.adminPresign(c, files.ResourceTask)
}

func (h *Handler) AdminPresignPentestAttachment(c *echo.Context) error {
	return h.adminPresign(c, files.ResourcePentest)
}

func (h *Handler) adminComplete(c *echo.Context, resourceType string) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	fileID, err := pathUUID(c, "fileUuid")
	if err != nil {
		return writeError(c, err)
	}
	boardID, moduleID, err := boardModule(c)
	if err != nil {
		return writeError(c, err)
	}
	var taskID *uuid.UUID
	if resourceType == files.ResourceTask {
		id, err := pathUUID(c, "taskUuid")
		if err != nil {
			return writeError(c, err)
		}
		taskID = &id
	}
	pending, err := h.service.AttachmentComplete(c.Request().Context(), files.AttachmentCompleteInput{
		Actor: actor, FileUUID: fileID, BoardID: boardID, ModuleID: moduleID,
		ResourceType: resourceType, TaskID: taskID,
	})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusAccepted, pendingFileModel(pending))
}

func (h *Handler) AdminCompleteTaskAttachment(c *echo.Context) error {
	return h.adminComplete(c, files.ResourceTask)
}

func (h *Handler) AdminCompletePentestAttachment(c *echo.Context) error {
	return h.adminComplete(c, files.ResourcePentest)
}

func (h *Handler) AdminDownloadTaskAttachment(c *echo.Context) error {
	boardID, moduleID, taskID, fileID, err := taskDownloadPath(c)
	if err != nil {
		return writeError(c, err)
	}
	return h.download(c, files.DownloadInput{
		Admin: true, BoardID: boardID, ModuleID: moduleID, ResourceType: files.ResourceTask,
		TaskID: &taskID, FileUUID: fileID,
	})
}

func (h *Handler) AdminDownloadPentestAttachment(c *echo.Context) error {
	boardID, moduleID, err := boardModule(c)
	if err != nil {
		return writeError(c, err)
	}
	fileID, err := pathUUID(c, "fileUuid")
	if err != nil {
		return writeError(c, err)
	}
	return h.download(c, files.DownloadInput{
		Admin: true, BoardID: boardID, ModuleID: moduleID,
		ResourceType: files.ResourcePentest, FileUUID: fileID,
	})
}

func (h *Handler) AdminDownloadSubmissionFile(c *echo.Context) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	boardID, err := pathUUID(c, "boardUuid")
	if err != nil {
		return writeError(c, err)
	}
	submissionID, err := pathUUID(c, "submissionUuid")
	if err != nil {
		return writeError(c, err)
	}
	fileID, err := pathUUID(c, "fileUuid")
	if err != nil {
		return writeError(c, err)
	}
	result, err := h.service.AdminDownloadSubmissionFile(c.Request().Context(), actor, boardID, submissionID, fileID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, downloadModel(result))
}

func (h *Handler) deleteAttachment(c *echo.Context, resourceType string, withTask bool) error {
	actor, ok := principal(c)
	if !ok {
		return writeError(c, files.ErrUnauthorized)
	}
	fileID, err := pathUUID(c, "fileUuid")
	if err != nil {
		return writeError(c, err)
	}
	boardID, moduleID, err := boardModule(c)
	if err != nil {
		return writeError(c, err)
	}
	var taskID *uuid.UUID
	if withTask {
		id, err := pathUUID(c, "taskUuid")
		if err != nil {
			return writeError(c, err)
		}
		taskID = &id
	}
	if err := h.service.DeleteAttachmentInScope(c.Request().Context(), actor, boardID, moduleID, resourceType, taskID, fileID); err != nil {
		return writeError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) AdminDeleteTaskAttachment(c *echo.Context) error {
	return h.deleteAttachment(c, files.ResourceTask, true)
}

func (h *Handler) AdminDeletePentestAttachment(c *echo.Context) error {
	return h.deleteAttachment(c, files.ResourcePentest, false)
}

func boardModule(c *echo.Context) (uuid.UUID, uuid.UUID, error) {
	boardID, err := pathUUID(c, "boardUuid")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	moduleID, err := pathUUID(c, "moduleUuid")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return boardID, moduleID, nil
}

func boardModuleTask(c *echo.Context) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	boardID, moduleID, err := boardModule(c)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	taskID, err := pathUUID(c, "taskUuid")
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	return boardID, moduleID, taskID, nil
}

func taskDownloadPath(c *echo.Context) (uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, error) {
	boardID, moduleID, taskID, err := boardModuleTask(c)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	fileID, err := pathUUID(c, "fileUuid")
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	return boardID, moduleID, taskID, fileID, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
