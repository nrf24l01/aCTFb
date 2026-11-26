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

func (h *Handler) AdminListBoards(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
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
	if moduleType != "" && !validModuleType(moduleType) {
		return validationProblem(boards.Invalid("query.moduleType", "unknown module type", "INVALID_VALUE")).write(c)
	}
	if status != "" && !validBoardStatus(status) {
		return validationProblem(boards.Invalid("query.status", "unknown board status", "INVALID_VALUE")).write(c)
	}
	page, err := h.service.AdminListBoards(c.Request().Context(), principal, boards.ListBoardsQuery{
		ListQuery:  boards.ListQuery{Limit: limit, Offset: offset, Query: search},
		ModuleType: moduleType, Status: status,
	})
	if err != nil {
		return writeAdminError(c, err)
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.AdminBoardsResponse{
		Items: boardsModel(page.Items), Limit: l, Offset: o, Total: t,
	})
}

func (h *Handler) AdminCreateBoard(c *echo.Context) error {
	httpapi.SetOperation(c, "board.create")
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	var request oapimodels.AdminBoardCreateRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	_ = raw
	rating := oapimodels.RatingVisibilityPUBLIC
	if request.Rating != nil && request.Rating.Visibility != nil {
		rating = *request.Rating.Visibility
	}
	input := boards.CreateBoardInput{
		Slug: string(*request.Slug), Name: *request.Name, Description: request.Description,
		ParticipationMode: string(*request.ParticipationMode),
		RegistrationMode:  string(*request.RegistrationMode),
		Visibility:        string(*request.Visibility),
		AccessMode:        string(request.AccessMode),
		RatingVisibility:  string(rating),
		StartsAt:          dateTimePointer(request.StartsAt),
		EndsAt:            dateTimePointer(request.EndsAt),
		TeamSettings:      teamSettingsInput(request.TeamSettings),
		AccessGroupUUIDs:  uuidList(request.AccessGroupUuids),
	}
	board, err := h.service.AdminCreateBoard(c.Request().Context(), principal, input)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusCreated, adminBoardModel(*board))
}

func (h *Handler) AdminGetBoard(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	board, err := h.service.AdminGetBoard(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, adminBoardModel(*board))
}

func (h *Handler) AdminUpdateBoard(c *echo.Context) error {
	httpapi.SetOperation(c, "board.update")
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.AdminBoardPatchRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if len(raw) == 0 {
		return validationProblem(boards.Invalid("body", "at least one property is required", "REQUIRED")).write(c)
	}
	if problem := rejectNullFields(raw, "slug", "name", "description", "participationMode", "registrationMode", "visibility", "rating"); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	input := boards.UpdateBoardInput{
		Slug:              optionalString(string(request.Slug), isPresent(raw, "slug")),
		Name:              optionalString(request.Name, isPresent(raw, "name")),
		Description:       optionalString(request.Description, isPresent(raw, "description")),
		ParticipationMode: optionalString(string(request.ParticipationMode), isPresent(raw, "participationMode")),
		RegistrationMode:  optionalString(string(request.RegistrationMode), isPresent(raw, "registrationMode")),
		Visibility:        optionalString(string(request.Visibility), isPresent(raw, "visibility")),
		AccessMode:        optionalString(string(request.AccessMode), isPresent(raw, "accessMode")),
	}
	if isPresent(raw, "rating") {
		if request.Rating == nil || request.Rating.Visibility == nil {
			return validationProblem(boards.Invalid("body.rating", "must not be null", "INVALID_VALUE")).write(c)
		}
		input.RatingVisibility = optionalString(string(*request.Rating.Visibility), true)
	}
	input.StartsAt, problem = parsePresenceTime(raw, "startsAt", request.StartsAt)
	if problem != nil {
		return problem.write(c)
	}
	input.EndsAt, problem = parsePresenceTime(raw, "endsAt", request.EndsAt)
	if problem != nil {
		return problem.write(c)
	}
	if isPresent(raw, "teamSettings") {
		if isNull(raw, "teamSettings") {
			input.ClearTeamSettings = true
		} else {
			input.TeamSettings = teamSettingsInput(request.TeamSettings)
		}
	}
	if isPresent(raw, "accessGroupUuids") {
		if isNull(raw, "accessGroupUuids") {
			input.AccessGroupUUIDs = boards.OptionalUUIDs{Set: true}
		} else {
			values := make([]string, 0, len(request.AccessGroupUuids))
			for _, group := range request.AccessGroupUuids {
				values = append(values, string(group))
			}
			input.AccessGroupUUIDs = boards.OptionalUUIDs{Set: true, Value: values}
		}
	}
	board, err := h.service.AdminUpdateBoard(c.Request().Context(), principal, boardID, input)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, adminBoardModel(*board))
}

func (h *Handler) AdminDeleteBoard(c *echo.Context) error {
	httpapi.SetOperation(c, "board.delete")
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.service.AdminDeleteBoard(c.Request().Context(), principal, boardID); err != nil {
		return writeAdminError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) AdminBoardAction(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.BoardActionRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	if request.Action == nil {
		return validationProblem(boards.Invalid("body.action", "is required", "REQUIRED")).write(c)
	}
	board, err := h.service.AdminBoardAction(c.Request().Context(), principal, boardID, *request.Action)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, boardModel(*board))
}

func teamSettingsInput(value *oapimodels.TeamSettings) *boards.TeamSettings {
	if value == nil || value.MinMembers == nil || value.MaxMembers == nil || value.AllowSelfCreate == nil || value.AllowLeaveAfterStart == nil {
		return nil
	}
	return &boards.TeamSettings{
		MinMembers: int(*value.MinMembers), MaxMembers: int(*value.MaxMembers),
		AllowSelfCreate: *value.AllowSelfCreate, AllowLeaveAfterStart: *value.AllowLeaveAfterStart,
	}
}

func uuidList(values []oapimodels.UUID) []uuid.UUID {
	if values == nil {
		return nil
	}
	out := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		parsed, err := uuid.Parse(string(value))
		if err == nil {
			out = append(out, parsed)
		}
	}
	return out
}
