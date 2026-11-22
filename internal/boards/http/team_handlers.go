package http

import (
	"net/http"
	"strings"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

func (h *Handler) GetMyTeam(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	team, err := h.service.GetMyTeam(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, teamModel(*team))
}

func (h *Handler) UpdateMyTeam(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.TeamPatchRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	name := strings.TrimSpace(string(request.Name))
	if name == "" {
		return validationProblem(boards.Invalid("body.name", "is required", "REQUIRED")).write(c)
	}
	team, err := h.service.UpdateMyTeam(c.Request().Context(), principal, boardID, name)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, teamModel(*team))
}

func (h *Handler) CreateTeam(c *echo.Context) error {
	return h.createTeam(c, false)
}

func (h *Handler) AdminCreateTeam(c *echo.Context) error {
	return h.createTeam(c, true)
}

func (h *Handler) createTeam(c *echo.Context, admin bool) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.TeamCreateRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	if request.Name == nil {
		return validationProblem(boards.Invalid("body.name", "is required", "REQUIRED")).write(c)
	}
	var team *boards.Team
	var err error
	if admin {
		team, err = h.service.AdminCreateTeam(c.Request().Context(), principal, boardID, string(*request.Name), principal.UserID)
	} else {
		team, err = h.service.CreateTeam(c.Request().Context(), principal, boardID, string(*request.Name))
	}
	if err != nil {
		if admin {
			return writeAdminError(c, err)
		}
		return writeError(c, err)
	}
	return c.JSON(http.StatusCreated, teamModel(*team))
}

func (h *Handler) JoinTeam(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.TeamJoinRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	if request.InviteKey == nil {
		return validationProblem(boards.Invalid("body.inviteKey", "is required", "REQUIRED")).write(c)
	}
	team, err := h.service.JoinTeam(c.Request().Context(), principal, boardID, *request.InviteKey)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, teamModel(*team))
}

func (h *Handler) GetTeamInviteKey(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	invite, err := h.service.TeamInviteKey(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, teamInviteModel(invite))
}

func (h *Handler) RotateTeamInviteKey(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	invite, err := h.service.RotateTeamInviteKey(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, teamInviteModel(invite))
}

func (h *Handler) LeaveTeam(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.service.LeaveTeam(c.Request().Context(), principal, boardID); err != nil {
		return writeError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) RemoveTeamMember(c *echo.Context) error {
	return h.removeTeamMember(c, false)
}

func (h *Handler) AdminRemoveTeamMember(c *echo.Context) error {
	return h.removeTeamMember(c, true)
}

func (h *Handler) removeTeamMember(c *echo.Context, admin bool) error {
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
	var err error
	if admin {
		var teamID uuid.UUID
		teamID, problem = pathUUID(c, "teamUuid")
		if problem != nil {
			return problem.write(c)
		}
		err = h.service.AdminRemoveTeamMember(c.Request().Context(), principal, boardID, teamID, userID)
	} else {
		err = h.service.RemoveTeamMember(c.Request().Context(), principal, boardID, userID)
	}
	if err != nil {
		if admin {
			return writeAdminError(c, err)
		}
		return writeError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) TransferTeamCaptain(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.TransferCaptainRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	if request.UserUUID == nil {
		return validationProblem(boards.Invalid("body.userUuid", "is required", "REQUIRED")).write(c)
	}
	userID, err := uuid.Parse(string(*request.UserUUID))
	if err != nil {
		return validationProblem(boards.Invalid("body.userUuid", "invalid UUID", "INVALID_FORMAT")).write(c)
	}
	team, err := h.service.TransferCaptain(c.Request().Context(), principal, boardID, userID)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, teamModel(*team))
}

func (h *Handler) AdminListTeams(c *echo.Context) error {
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
	page, err := h.service.AdminListTeams(c.Request().Context(), principal, boardID, limit, offset)
	if err != nil {
		return writeAdminError(c, err)
	}
	items := make([]*oapimodels.Team, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, teamModel(item))
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &teamListResponse{Items: items, Limit: l, Offset: o, Total: t})
}

func (h *Handler) AdminGetTeam(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	teamID, problem := pathUUID(c, "teamUuid")
	if problem != nil {
		return problem.write(c)
	}
	team, err := h.service.AdminGetTeam(c.Request().Context(), principal, boardID, teamID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, teamModel(*team))
}

func (h *Handler) AdminUpdateTeam(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	teamID, problem := pathUUID(c, "teamUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.TeamPatchRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if strings.TrimSpace(string(request.Name)) == "" {
		return validationProblem(boards.Invalid("body.name", "is required", "REQUIRED")).write(c)
	}
	team, err := h.service.AdminUpdateTeam(c.Request().Context(), principal, boardID, teamID, string(request.Name))
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, teamModel(*team))
}

func (h *Handler) AdminDeleteTeam(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	teamID, problem := pathUUID(c, "teamUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.service.AdminDeleteTeam(c.Request().Context(), principal, boardID, teamID); err != nil {
		return writeAdminError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) AdminAddTeamMember(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	teamID, problem := pathUUID(c, "teamUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.AdminTeamMemberAddRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	if request.UserUUID == nil {
		return validationProblem(boards.Invalid("body.userUuid", "is required", "REQUIRED")).write(c)
	}
	userID, err := uuid.Parse(string(*request.UserUUID))
	if err != nil {
		return validationProblem(boards.Invalid("body.userUuid", "invalid UUID", "INVALID_FORMAT")).write(c)
	}
	team, err := h.service.AdminAddTeamMember(c.Request().Context(), principal, boardID, teamID, userID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, teamModel(*team))
}

type teamListResponse struct {
	Items  []*oapimodels.Team `json:"items"`
	Limit  *int64             `json:"limit"`
	Offset *int64             `json:"offset"`
	Total  *int64             `json:"total"`
}

// MyTeamMembers lists the caller's own team roster with names only.
func (h *Handler) MyTeamMembers(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	members, err := h.service.MyTeamMembers(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeError(c, err)
	}
	items := make([]*oapimodels.TeamMember, 0, len(members))
	for _, member := range members {
		userUUID := oapimodels.UUID(member.UserID.String())
		username := member.Username
		joinedAt := strfmt.DateTime(member.JoinedAt.UTC())
		items = append(items, &oapimodels.TeamMember{
			UserUUID: &userUUID, Username: &username, JoinedAt: &joinedAt,
		})
	}
	return c.JSON(http.StatusOK, &oapimodels.TeamMemberListResponse{Items: items})
}
