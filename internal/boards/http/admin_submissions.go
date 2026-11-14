package http

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

func (h *Handler) AdminListSubmissions(c *echo.Context) error {
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
	search, problem := querySearch(c)
	if problem != nil {
		return problem.write(c)
	}
	filter := boards.SubmissionFilter{ListQuery: boards.ListQuery{Limit: limit, Offset: offset, Query: search}}
	if raw := strings.TrimSpace(c.QueryParam("moduleUuid")); raw != "" {
		moduleID, err := parseUUID(raw)
		if err != nil {
			return validationProblem(boards.Invalid("query.moduleUuid", "Invalid UUID", "INVALID_FORMAT")).write(c)
		}
		filter.ModuleID = &moduleID
	}
	if resourceType := strings.TrimSpace(c.QueryParam("resourceType")); resourceType != "" {
		if resourceType != boards.SubmissionResourceTask && resourceType != boards.SubmissionResourcePentest {
			return validationProblem(boards.Invalid("query.resourceType", "unknown resource type", "INVALID_VALUE")).write(c)
		}
		filter.ResourceType = resourceType
	}
	if status := strings.TrimSpace(c.QueryParam("status")); status != "" {
		if !validSubmissionStatus(status) {
			return validationProblem(boards.Invalid("query.status", "unknown submission status", "INVALID_VALUE")).write(c)
		}
		filter.Status = status
	}
	page, err := h.service.AdminListSubmissions(c.Request().Context(), principal, boardID, filter)
	if err != nil {
		return writeAdminError(c, err)
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.AdminSubmissionListResponse{
		Items: adminSubmissionsModel(page.Items), Limit: l, Offset: o, Total: t,
	})
}

func (h *Handler) AdminGetSubmission(c *echo.Context) error {
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
	submission, err := h.service.AdminGetSubmission(c.Request().Context(), principal, boardID, submissionID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, adminSubmissionModel(*submission))
}

func (h *Handler) AdminReviewSubmission(c *echo.Context) error {
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
	key, problem := idempotencyKey(c)
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.AdminSubmissionReviewRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	if request.Decision == nil {
		return validationProblem(boards.Invalid("body.decision", "is required", "REQUIRED")).write(c)
	}
	submission, err := h.service.AdminReviewSubmission(c.Request().Context(), principal, boardID, submissionID,
		boards.ReviewInput{Decision: string(*request.Decision), Message: request.Message}, key)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, adminSubmissionModel(*submission))
}

type participantListResponse struct {
	Items  []*oapimodels.UserSummary `json:"items"`
	Limit  *int64                    `json:"limit"`
	Offset *int64                    `json:"offset"`
	Total  *int64                    `json:"total"`
}

func (h *Handler) AdminListParticipants(c *echo.Context) error {
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
	page, err := h.service.AdminListParticipants(c.Request().Context(), principal, boardID, boards.ListQuery{Limit: limit, Offset: offset})
	if err != nil {
		return writeAdminError(c, err)
	}
	items := make([]*oapimodels.UserSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, userSummaryModel(item))
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &participantListResponse{Items: items, Limit: l, Offset: o, Total: t})
}

func (h *Handler) AdminAddParticipant(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.AdminParticipantAddRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	userID, err := parseUUID(string(*request.UserUUID))
	if err != nil {
		return validationProblem(boards.Invalid("body.userUuid", "Invalid UUID", "INVALID_FORMAT")).write(c)
	}
	if err := h.service.AdminAddParticipant(c.Request().Context(), principal, boardID, userID); err != nil {
		return writeAdminError(c, err)
	}
	status := "participant_added"
	return c.JSON(http.StatusCreated, &oapimodels.StatusResponse{Status: &status})
}

func (h *Handler) AdminRemoveParticipant(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	userID, problem := pathUUID(c, "userUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.service.AdminRemoveParticipant(c.Request().Context(), principal, boardID, userID); err != nil {
		return writeAdminError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
