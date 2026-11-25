package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

func (h *Handler) ListBoards(c *echo.Context) error {
	principal, authenticated := httpapi.CurrentPrincipal(c)
	limit, offset, problem := queryLimitOffset(c)
	if problem != nil {
		return problem.write(c)
	}
	search, problem := querySearch(c)
	if problem != nil {
		return problem.write(c)
	}
	moduleType := strings.TrimSpace(c.QueryParam("moduleType"))
	status := strings.TrimSpace(c.QueryParam("status"))
	participation := strings.TrimSpace(c.QueryParam("participationMode"))
	if moduleType != "" && !validModuleType(moduleType) {
		return validationProblem(boards.Invalid("query.moduleType", "unknown module type", "INVALID_VALUE")).write(c)
	}
	if status != "" && !validBoardStatus(status) {
		return validationProblem(boards.Invalid("query.status", "unknown board status", "INVALID_VALUE")).write(c)
	}
	if participation != "" && participation != boards.ParticipationIndividual && participation != boards.ParticipationTeam {
		return validationProblem(boards.Invalid("query.participationMode", "unknown participation mode", "INVALID_VALUE")).write(c)
	}
	query := boards.ListBoardsQuery{
		ListQuery:  boards.ListQuery{Limit: limit, Offset: offset, Query: search},
		ModuleType: moduleType, Status: status, ParticipationMode: participation,
	}
	var page boards.Page[boards.Board]
	var err error
	if authenticated {
		page, err = h.service.ListBoards(c.Request().Context(), principal, query)
	} else {
		page, err = h.service.ListPublicBoards(c.Request().Context(), query)
	}
	if err != nil {
		return writeError(c, err)
	}
	items := make([]*oapimodels.BoardListItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, boardListModel(item))
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.BoardListResponse{Items: items, Limit: l, Offset: o, Total: t})
}

func (h *Handler) GetBoard(c *echo.Context) error {
	principal, authenticated := httpapi.CurrentPrincipal(c)
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var board *boards.Board
	var err error
	if authenticated {
		board, err = h.service.GetBoard(c.Request().Context(), principal, boardID)
	} else {
		board, err = h.service.GetPublicBoard(c.Request().Context(), boardID)
	}
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, boardModel(*board))
}

func (h *Handler) JoinBoard(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.BoardJoinRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	// Trim before the generated min/max check; normalization to uppercase
	// happens in the service for canonical codes, and legacy codes are compared
	// exactly as entered.
	request.Code = strings.TrimSpace(request.Code)
	request.InviteCode = strings.TrimSpace(request.InviteCode)
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	// Enrollment is code-only. The canonical field is `code`; the deprecated
	// `inviteCode` alias is still read for boards that keep a legacy sealed
	// invite code, so previously issued codes keep working.
	code := ""
	if isPresent(raw, "code") && !isNull(raw, "code") {
		code = request.Code
	} else if isPresent(raw, "inviteCode") && !isNull(raw, "inviteCode") {
		code = request.InviteCode
	}
	inviteCode := strings.TrimSpace(code)
	me, err := h.service.JoinBoard(c.Request().Context(), principal, boardID, inviteCode)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, boardMeModel(me))
}

func (h *Handler) LeaveBoard(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.service.LeaveBoard(c.Request().Context(), principal, boardID); err != nil {
		return writeError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) GetBoardMe(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	me, err := h.service.GetBoardMe(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, boardMeModel(me))
}

func (h *Handler) GetScoreboard(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	scoreboard, err := h.service.GetScoreboard(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, scoreboardModel(scoreboard))
}

func (h *Handler) GetMyScoreboardEntry(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	item, err := h.service.GetMyScoreboardEntry(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, scoreboardItemModel(*item))
}

func (h *Handler) ListModules(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	limit, offset, problem := queryLimitOffset(c)
	if problem != nil {
		return problem.write(c)
	}
	moduleType := strings.TrimSpace(c.QueryParam("type"))
	if moduleType != "" && !validModuleType(moduleType) {
		return validationProblem(boards.Invalid("query.type", "unknown module type", "INVALID_VALUE")).write(c)
	}
	page, err := h.service.ListModules(c.Request().Context(), principal, boardID,
		boards.ListQuery{Limit: limit, Offset: offset, Type: moduleType})
	if err != nil {
		return writeError(c, err)
	}
	items := make([]*oapimodels.ModuleListItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, moduleListModel(item))
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.ModuleListResponse{Items: items, Limit: l, Offset: o, Total: t})
}

func (h *Handler) GetModule(c *echo.Context) error {
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
	module, err := h.service.GetModule(c.Request().Context(), principal, boardID, moduleID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, moduleModel(*module))
}

func (h *Handler) ListTasks(c *echo.Context) error {
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
	limit, offset, problem := queryLimitOffset(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := h.service.ListTasks(c.Request().Context(), principal, boardID, moduleID, boards.ListQuery{Limit: limit, Offset: offset})
	if err != nil {
		return writeError(c, err)
	}
	items := make([]*oapimodels.TaskListItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, taskListModel(item))
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.TaskListResponse{Items: items, Limit: l, Offset: o, Total: t})
}

func (h *Handler) GetTask(c *echo.Context) error {
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
	task, err := h.service.GetTask(c.Request().Context(), principal, boardID, moduleID, taskID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, taskModel(*task))
}

func (h *Handler) SubmitTask(c *echo.Context) error {
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
	key, problem := idempotencyKey(c)
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.FinalizeSubmissionRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	var draftID *uuid.UUID
	if isPresent(raw, "draftUuid") && !isNull(raw, "draftUuid") {
		parsed, parseErr := uuid.Parse(string(request.DraftUUID))
		if parseErr != nil {
			return validationProblem(boards.Invalid("body.draftUuid", "invalid UUID", "INVALID_FORMAT")).write(c)
		}
		draftID = &parsed
	}
	submission, err := h.service.SubmitTask(c.Request().Context(), principal,
		boards.TaskPath{BoardID: boardID, ModuleID: moduleID, TaskID: taskID}, key,
		boards.SubmitInput{Text: request.Text, DraftID: draftID})
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, submissionModel(*submission))
}

func (h *Handler) ListMyTaskSubmissions(c *echo.Context) error {
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
	limit, offset, problem := queryLimitOffset(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := h.service.ListMyTaskSubmissions(c.Request().Context(), principal,
		boards.TaskPath{BoardID: boardID, ModuleID: moduleID, TaskID: taskID},
		boards.ListQuery{Limit: limit, Offset: offset})
	if err != nil {
		return writeError(c, err)
	}
	items := make([]*oapimodels.Submission, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, submissionModel(item))
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.SubmissionListResponse{Items: items, Limit: l, Offset: o, Total: t})
}

func validModuleType(value string) bool {
	switch value {
	case boards.ModuleTypeTasks, boards.ModuleTypeAttackDefense, boards.ModuleTypePentest, boards.ModuleTypeExtension:
		return true
	}
	return false
}

func validBoardStatus(value string) bool {
	switch value {
	case string(boards.BoardStatusDraft), string(boards.BoardStatusScheduled), string(boards.BoardStatusActive),
		string(boards.BoardStatusFinished), string(boards.BoardStatusArchived):
		return true
	}
	return false
}

func nestedRaw(raw rawBody, key string) rawBody {
	body, present := raw[key]
	if !present || string(body) == "null" {
		return nil
	}
	var nested rawBody
	if err := json.Unmarshal(body, &nested); err != nil {
		return nil
	}
	return nested
}
