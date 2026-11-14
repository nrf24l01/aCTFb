package http

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

func (h *Handler) AdminListTasks(c *echo.Context) error {
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
	limit, offset, problem := queryLimitOffset(c)
	if problem != nil {
		return problem.write(c)
	}
	search, problem := querySearch(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := h.service.AdminListTasks(c.Request().Context(), principal, boardID, moduleID,
		boards.ListQuery{Limit: limit, Offset: offset, Query: search})
	if err != nil {
		return writeAdminError(c, err)
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.AdminTasksResponse{
		Items: adminTasksModel(page.Items), Limit: l, Offset: o, Total: t,
	})
}

func (h *Handler) AdminCreateTask(c *echo.Context) error {
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
	var request oapimodels.AdminTaskCreateRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	policy := submissionPolicyInput(request.Submission, nestedRaw(raw, "submission"))
	instance := taskInstanceInput(request.Instance)
	extensionKey := ""
	if request.ExtensionKey != nil {
		extensionKey = strings.TrimSpace(*request.ExtensionKey)
	}
	task, err := h.service.AdminCreateTask(c.Request().Context(), principal, boardID, moduleID, boards.CreateTaskInput{
		Title: *request.Title, Type: string(*request.Type), Category: *request.Category,
		Description: *request.Description, Points: int(*request.Points), Visible: *request.Visible,
		ExtensionKey: extensionKey, Submission: policy, AcceptedAnswers: request.AcceptedAnswers, Instance: instance,
	})
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusCreated, adminTaskModel(*task))
}

func (h *Handler) AdminUpdateTask(c *echo.Context) error {
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
	var request oapimodels.AdminTaskPatchRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if len(raw) == 0 {
		return validationProblem(boards.Invalid("body", "at least one property is required", "REQUIRED")).write(c)
	}
	if problem := rejectNullFields(raw, "title", "category", "description", "points", "visible", "submission", "instance"); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	input := boards.UpdateTaskInput{
		Title:       optionalString(request.Title, isPresent(raw, "title")),
		Category:    optionalString(request.Category, isPresent(raw, "category")),
		Description: optionalString(request.Description, isPresent(raw, "description")),
		Visible:     optionalBool(request.Visible, isPresent(raw, "visible")),
	}
	if isPresent(raw, "extensionKey") {
		if isNull(raw, "extensionKey") {
			input.ExtensionKey = boards.OptionalString{Set: true}
		} else if request.ExtensionKey != nil {
			input.ExtensionKey = boards.OptionalString{Set: true, Value: stringValue(strings.TrimSpace(*request.ExtensionKey))}
		}
	}
	if isPresent(raw, "points") {
		if request.Points == nil || isNull(raw, "points") {
			return validationProblem(boards.Invalid("body.points", "must not be null", "INVALID_VALUE")).write(c)
		}
		input.Points = optionalInt(int(*request.Points), true)
	}
	if isPresent(raw, "submission") {
		if request.Submission == nil || isNull(raw, "submission") {
			return validationProblem(boards.Invalid("body.submission", "must not be null", "INVALID_VALUE")).write(c)
		}
		policy := submissionPolicyInput(request.Submission, nestedRaw(raw, "submission"))
		input.Submission = &policy
	}
	if isPresent(raw, "instance") {
		if request.Instance == nil || isNull(raw, "instance") {
			return validationProblem(boards.Invalid("body.instance", "must not be null", "INVALID_VALUE")).write(c)
		}
		instance := taskInstanceInput(request.Instance)
		input.Instance = &instance
	}
	if isPresent(raw, "acceptedAnswers") {
		if isNull(raw, "acceptedAnswers") {
			input.AcceptedAnswers = boards.OptionalStrings{Set: true}
		} else {
			input.AcceptedAnswers = boards.OptionalStrings{Set: true, Value: request.AcceptedAnswers}
		}
	}
	task, err := h.service.AdminUpdateTask(c.Request().Context(), principal, boardID, moduleID, taskID, input)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, adminTaskModel(*task))
}

func (h *Handler) AdminDeleteTask(c *echo.Context) error {
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
	if err := h.service.AdminDeleteTask(c.Request().Context(), principal, boardID, moduleID, taskID); err != nil {
		return writeAdminError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) AdminGetTask(c *echo.Context) error {
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
	task, err := h.service.AdminGetTask(c.Request().Context(), principal, boardID, moduleID, taskID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, adminTaskModel(*task))
}

func (h *Handler) AdminBatchTaskVisibility(c *echo.Context) error {
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
	var request oapimodels.BatchVisibilityRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	taskIDs := make([]uuid.UUID, 0, len(request.TaskUuids))
	for _, raw := range request.TaskUuids {
		parsed, err := uuid.Parse(string(raw))
		if err != nil {
			return validationProblem(boards.Invalid("body.taskUuids", "invalid UUID", "INVALID_FORMAT")).write(c)
		}
		taskIDs = append(taskIDs, parsed)
	}
	updated, err := h.service.AdminBatchTaskVisibility(c.Request().Context(), principal, boardID, moduleID, taskIDs, *request.Visible)
	if err != nil {
		return writeAdminError(c, err)
	}
	value := int64(updated)
	return c.JSON(http.StatusOK, &oapimodels.BatchUpdatedResponse{Updated: &value})
}
