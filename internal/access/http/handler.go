// Package http exposes only the global access-group and admin-user routes.
// Board audience/team routes remain in internal/boards/http and call AccessService.
package http

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

// Handler maps the global admin access-group and user contracts.
type Handler struct {
	service       *access.AccessService
	authenticator httpapi.AccessTokenAuthenticator
}

// NewHandler constructs the access HTTP adapter using the shared bearer
// authenticator.
func NewHandler(service *access.AccessService, authenticator httpapi.AccessTokenAuthenticator) *Handler {
	return &Handler{service: service, authenticator: authenticator}
}

// RegisterRoutes wires the global access-group and admin-user routes.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	auth := httpapi.RequireUser(h.authenticator)

	e.GET("/api/v2/admin/access-groups", h.ListAccessGroups, auth)
	e.POST("/api/v2/admin/access-groups", h.CreateAccessGroup, auth)
	e.GET("/api/v2/admin/access-groups/:groupUuid", h.GetAccessGroup, auth)
	e.PATCH("/api/v2/admin/access-groups/:groupUuid", h.UpdateAccessGroup, auth)
	e.DELETE("/api/v2/admin/access-groups/:groupUuid", h.DeleteAccessGroup, auth)

	e.GET("/api/v2/admin/access-groups/:groupUuid/members", h.ListGroupMembers, auth)
	e.POST("/api/v2/admin/access-groups/:groupUuid/members", h.AddGroupMember, auth)
	e.DELETE("/api/v2/admin/access-groups/:groupUuid/members/:userUuid", h.RemoveGroupMember, auth)

	e.GET("/api/v2/admin/users", h.ListUsers, auth)
	e.POST("/api/v2/admin/users", h.CreateUser, auth)
	e.GET("/api/v2/admin/users/:userUuid", h.GetUser, auth)
	e.PATCH("/api/v2/admin/users/:userUuid", h.UpdateUser, auth)

	e.GET("/api/v2/admin/user-search", h.SearchUsers, auth)
	e.GET("/api/v2/admin/boards/:boardUuid/user-search", h.SearchBoardUsers, auth)
}

func (h *Handler) ListAccessGroups(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	limit, offset, problem := pageParams(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := h.service.ListAccessGroups(c.Request().Context(), principal, access.ListQuery{
		Limit: limit, Offset: offset, Query: c.QueryParam("query"),
	})
	if err != nil {
		return h.writeServiceError(c, err)
	}
	response := accessGroupsResponse{
		Items:  make([]accessGroupResponse, 0, len(page.Items)),
		Limit:  int64(limit),
		Offset: int64(offset),
		Total:  int64(page.Total),
	}
	for _, group := range page.Items {
		response.Items = append(response.Items, accessGroupResponseFrom(group))
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) CreateAccessGroup(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	var req accessGroupCreateRequest
	if problem := bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if strings.TrimSpace(req.Name) == "" {
		return validationProblem("name", "name is required", "REQUIRED").write(c)
	}
	group, err := h.service.CreateAccessGroup(c.Request().Context(), principal, req.Name)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, accessGroupResponseFrom(*group))
}

func (h *Handler) GetAccessGroup(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	id, problem := pathUUID(c, "groupUuid")
	if problem != nil {
		return problem.write(c)
	}
	group, err := h.service.GetAccessGroup(c.Request().Context(), principal, id)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, accessGroupResponseFrom(*group))
}

func (h *Handler) UpdateAccessGroup(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	id, problem := pathUUID(c, "groupUuid")
	if problem != nil {
		return problem.write(c)
	}
	var req accessGroupPatchRequest
	if problem := bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if req.Name == nil {
		return validationProblem("body", "At least one field is required", "REQUIRED").write(c)
	}
	name := strings.TrimSpace(*req.Name)
	if name == "" {
		return validationProblem("name", "name is required", "REQUIRED").write(c)
	}
	group, err := h.service.UpdateAccessGroup(c.Request().Context(), principal, id, name)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, accessGroupResponseFrom(*group))
}

func (h *Handler) DeleteAccessGroup(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	id, problem := pathUUID(c, "groupUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.service.DeleteAccessGroup(c.Request().Context(), principal, id); err != nil {
		return h.writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) ListUsers(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	limit, offset, problem := pageParams(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := h.service.ListUsers(c.Request().Context(), principal, access.ListQuery{
		Limit: limit, Offset: offset, Query: c.QueryParam("query"),
	})
	if err != nil {
		return h.writeServiceError(c, err)
	}
	response := adminUsersResponse{
		Items:  make([]adminUserResponse, 0, len(page.Items)),
		Limit:  int64(limit),
		Offset: int64(offset),
		Total:  int64(page.Total),
	}
	for _, user := range page.Items {
		response.Items = append(response.Items, adminUserResponseFrom(user))
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) CreateUser(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	var req adminUserCreateRequest
	if problem := bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validateAdminUserCreate(req); problem != nil {
		return problem.write(c)
	}
	groupIDs, problem := parseUUIDList(req.AccessGroupUUIDs, "accessGroupUuids")
	if problem != nil {
		return problem.write(c)
	}
	user, err := h.service.CreateUser(c.Request().Context(), principal, access.AdminUserInput{
		Email:            req.Email,
		Username:         req.Username,
		Password:         req.Password,
		EmailVerified:    req.EmailVerified,
		Disabled:         req.Disabled,
		Roles:            req.Roles,
		AccessGroupUUIDs: groupIDs,
	})
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, adminUserResponseFrom(*user))
}

func (h *Handler) GetUser(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	id, problem := pathUUID(c, "userUuid")
	if problem != nil {
		return problem.write(c)
	}
	user, err := h.service.GetUser(c.Request().Context(), principal, id)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, adminUserResponseFrom(*user))
}

func (h *Handler) UpdateUser(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	id, problem := pathUUID(c, "userUuid")
	if problem != nil {
		return problem.write(c)
	}
	var req adminUserPatchRequest
	if problem := bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	patch, problem := req.toPatch()
	if problem != nil {
		return problem.write(c)
	}
	user, err := h.service.UpdateUser(c.Request().Context(), principal, id, patch)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, adminUserResponseFrom(*user))
}

// SearchUsers is the global-admin picker search. It returns the same bounded
// page shape as the group member list.
func (h *Handler) SearchUsers(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	limit, offset, problem := pageParams(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := h.service.SearchUsers(c.Request().Context(), principal, access.ListQuery{
		Limit: limit, Offset: offset, Query: c.QueryParam("query"),
	})
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, userDirectoryResponseFrom(page, limit, offset))
}

// SearchBoardUsers is the board-scoped picker. The service enforces live
// board.participants.manage and audits the read; no participant may call it.
func (h *Handler) SearchBoardUsers(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	limit, offset, problem := pageParams(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := h.service.SearchBoardUsers(c.Request().Context(), principal, boardID, access.ListQuery{
		Limit: limit, Offset: offset, Query: c.QueryParam("query"),
	})
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, userDirectoryResponseFrom(page, limit, offset))
}

// ListGroupMembers returns one access group's membership to a global admin.
func (h *Handler) ListGroupMembers(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	groupID, problem := pathUUID(c, "groupUuid")
	if problem != nil {
		return problem.write(c)
	}
	limit, offset, problem := pageParams(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := h.service.ListAccessGroupMembers(c.Request().Context(), principal, groupID, access.ListQuery{
		Limit: limit, Offset: offset, Query: c.QueryParam("query"),
	})
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, groupMemberResponseFrom(page, limit, offset))
}

// AddGroupMember adds one user to a group with a transactional audit event.
func (h *Handler) AddGroupMember(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	groupID, problem := pathUUID(c, "groupUuid")
	if problem != nil {
		return problem.write(c)
	}
	var req groupMemberAddRequest
	if problem := bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	userID, err := uuid.Parse(strings.TrimSpace(req.UserUUID))
	if err != nil {
		return validationProblem("userUuid", "userUuid is required", "REQUIRED").write(c)
	}
	if err := h.service.AddAccessGroupMember(c.Request().Context(), principal, groupID, userID); err != nil {
		return h.writeServiceError(c, err)
	}
	return c.NoContent(http.StatusCreated)
}

// RemoveGroupMember removes one user from a group with a transactional audit.
func (h *Handler) RemoveGroupMember(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	groupID, problem := pathUUID(c, "groupUuid")
	if problem != nil {
		return problem.write(c)
	}
	userID, problem := pathUUID(c, "userUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.service.RemoveAccessGroupMember(c.Request().Context(), principal, groupID, userID); err != nil {
		return h.writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
