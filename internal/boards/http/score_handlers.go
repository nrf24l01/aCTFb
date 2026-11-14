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

func (h *Handler) GetBoardInviteCode(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	invite, err := h.service.GetBoardInviteCode(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, boardInviteModel(invite))
}

func (h *Handler) RotateBoardInviteCode(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	invite, err := h.service.RotateBoardInviteCode(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, boardInviteModel(invite))
}

func (h *Handler) AdminListScoreAdjustments(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	limit, offset, problem := queryLimitOffset(c)
	if problem != nil {
		return problem.write(c)
	}
	var moduleID *uuid.UUID
	if raw := strings.TrimSpace(c.QueryParam("moduleUuid")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return validationProblem(boards.Invalid("query.moduleUuid", "invalid UUID", "INVALID_FORMAT")).write(c)
		}
		moduleID = &parsed
	}
	page, err := h.service.AdminListScoreAdjustments(c.Request().Context(), principal, boardID, moduleID,
		boards.ListQuery{Limit: limit, Offset: offset})
	if err != nil {
		return writeAdminError(c, err)
	}
	items := make([]*oapimodels.ScoreAdjustment, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, scoreAdjustmentModel(item))
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.ScoreAdjustmentsResponse{Items: items, Limit: l, Offset: o, Total: t})
}

func (h *Handler) AdminCreateScoreAdjustment(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.ScoreAdjustmentRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	subjectID, err := uuid.Parse(string(*request.SubjectUUID))
	if err != nil {
		return validationProblem(boards.Invalid("body.subjectUuid", "invalid UUID", "INVALID_FORMAT")).write(c)
	}
	var moduleID *uuid.UUID
	if isPresent(raw, "moduleUuid") && !isNull(raw, "moduleUuid") {
		parsed, parseErr := uuid.Parse(string(request.ModuleUUID.UUID))
		if parseErr != nil {
			return validationProblem(boards.Invalid("body.moduleUuid", "invalid UUID", "INVALID_FORMAT")).write(c)
		}
		moduleID = &parsed
	}
	item, err := h.service.AdminCreateScoreAdjustment(c.Request().Context(), principal, boardID, boards.ScoreAdjustmentInput{
		ModuleID: moduleID,
		Subject:  boards.ScoreSubject{Type: string(*request.SubjectType), ID: subjectID},
		Delta:    int(*request.Delta),
		Reason:   *request.Reason,
	})
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusCreated, scoreAdjustmentModel(*item))
}

func (h *Handler) AdminGetScoreboard(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	scoreboard, err := h.service.AdminGetScoreboard(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, scoreboardModel(scoreboard))
}
