package http

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

func (h *Handler) ListMyAudiences(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	items, err := h.service.ListMyAudiences(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeError(c, err)
	}
	response := make([]*oapimodels.AudienceSummary, 0, len(items))
	for _, item := range items {
		id := oapimodels.UUID(item.UUID.String())
		name := item.Name
		response = append(response, &oapimodels.AudienceSummary{UUID: &id, Name: &name})
	}
	return c.JSON(http.StatusOK, &oapimodels.AudienceListResponse{Items: response})
}

func (h *Handler) AdminListAudiences(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	accessSvc := h.service.AccessService()
	if accessSvc == nil {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
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
	page, err := accessSvc.ListAudiences(c.Request().Context(), principal, boardID,
		access.ListQuery{Limit: limit, Offset: offset, Query: search})
	if err != nil {
		return h.writeAccessError(c, err, true)
	}
	items := make([]*oapimodels.Audience, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, audienceModel(item))
	}
	l, o, t := pageResponse(limit, offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.AdminAudiencesResponse{Items: items, Limit: l, Offset: o, Total: t})
}

func (h *Handler) AdminCreateAudience(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	accessSvc := h.service.AccessService()
	if accessSvc == nil {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.AudienceCreateRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	audience, err := accessSvc.CreateAudience(c.Request().Context(), principal, boardID,
		access.AudienceInput{Name: *request.Name, Description: request.Description})
	if err != nil {
		return h.writeAccessError(c, err, true)
	}
	return c.JSON(http.StatusCreated, audienceModel(*audience))
}

func (h *Handler) AdminGetAudience(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	accessSvc := h.service.AccessService()
	if accessSvc == nil {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	audienceID, problem := pathUUID(c, "audienceUuid")
	if problem != nil {
		return problem.write(c)
	}
	for offset := 0; ; offset += 100 {
		audiences, err := accessSvc.ListAudiences(c.Request().Context(), principal, boardID,
			access.ListQuery{Limit: 100, Offset: offset})
		if err != nil {
			return h.writeAccessError(c, err, true)
		}
		for _, audience := range audiences.Items {
			if audience.ID == audienceID {
				return c.JSON(http.StatusOK, audienceModel(audience))
			}
		}
		if offset+len(audiences.Items) >= audiences.Total || len(audiences.Items) == 0 {
			break
		}
	}
	return writeAdminError(c, boards.ErrAudienceNotFound)
}

func (h *Handler) AdminUpdateAudience(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	accessSvc := h.service.AccessService()
	if accessSvc == nil {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	audienceID, problem := pathUUID(c, "audienceUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.AudiencePatchRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if len(raw) == 0 {
		return validationProblem(boards.Invalid("body", "at least one property is required", "REQUIRED")).write(c)
	}
	patch := access.AudiencePatch{}
	if isPresent(raw, "name") {
		name := request.Name
		patch.Name = &name
	}
	if isPresent(raw, "description") {
		description := request.Description
		patch.Description = &description
	}
	audience, err := accessSvc.UpdateAudience(c.Request().Context(), principal, boardID, audienceID, patch)
	if err != nil {
		return h.writeAccessError(c, err, true)
	}
	return c.JSON(http.StatusOK, audienceModel(*audience))
}

func (h *Handler) AdminDeleteAudience(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	accessSvc := h.service.AccessService()
	if accessSvc == nil {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	audienceID, problem := pathUUID(c, "audienceUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := accessSvc.DeleteAudience(c.Request().Context(), principal, boardID, audienceID); err != nil {
		return h.writeAccessError(c, err, true)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) AdminListAudienceMembers(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	accessSvc := h.service.AccessService()
	if accessSvc == nil {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	audienceID, problem := pathUUID(c, "audienceUuid")
	if problem != nil {
		return problem.write(c)
	}
	limit, offset, problem := queryLimitOffset(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := accessSvc.ListAudienceMembers(c.Request().Context(), principal, boardID, audienceID,
		access.ListQuery{Limit: limit, Offset: offset})
	if err != nil {
		return h.writeAccessError(c, err, true)
	}
	items := make([]*oapimodels.AudienceMember, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, audienceMemberModel(item))
	}
	l, o, t := pageResponse(limit, offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.AudienceMembersResponse{Items: items, Limit: l, Offset: o, Total: t})
}

func (h *Handler) AdminAddAudienceMember(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	accessSvc := h.service.AccessService()
	if accessSvc == nil {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	audienceID, problem := pathUUID(c, "audienceUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.AudienceMemberCreateRequest
	if _, problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	subjectID, err := uuid.Parse(string(*request.SubjectUUID))
	if err != nil {
		return validationProblem(boards.Invalid("body.subjectUuid", "invalid UUID", "INVALID_FORMAT")).write(c)
	}
	member, err := accessSvc.AddAudienceMember(c.Request().Context(), principal, boardID, audienceID,
		access.MemberInput{SubjectType: string(*request.SubjectType), SubjectID: subjectID})
	if err != nil {
		return h.writeAccessError(c, err, true)
	}
	return c.JSON(http.StatusCreated, audienceMemberModel(*member))
}

func (h *Handler) AdminRemoveAudienceMember(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	accessSvc := h.service.AccessService()
	if accessSvc == nil {
		return writeAdminError(c, boards.ErrDependencyUnavailable)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	audienceID, problem := pathUUID(c, "audienceUuid")
	if problem != nil {
		return problem.write(c)
	}
	subjectID, problem := pathUUID(c, "subjectUuid")
	if problem != nil {
		return problem.write(c)
	}
	subjectType := strings.ToUpper(strings.TrimSpace(c.Param("subjectType")))
	if subjectType != boards.SubjectTypeUser && subjectType != boards.SubjectTypeTeam {
		return validationProblem(boards.Invalid("path.subjectType", "must be USER or TEAM", "INVALID_VALUE")).write(c)
	}
	if err := accessSvc.RemoveAudienceMember(c.Request().Context(), principal, boardID, audienceID,
		access.Subject{Type: subjectType, ID: subjectID}); err != nil {
		return h.writeAccessError(c, err, true)
	}
	return c.NoContent(http.StatusNoContent)
}
