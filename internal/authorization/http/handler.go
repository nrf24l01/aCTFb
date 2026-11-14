// Package http maps the authorization service to the additive /api/v2 scoped
// RBAC endpoints. It uses the shared bearer-token adapter from internal/httpapi;
// every operation still re-authorizes in the business service.
package http

import (
	stderrors "errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

const maxBodyBytes = 1 << 20

// Handler exposes the six scoped RBAC/audit routes.
type Handler struct {
	authz         authorization.Authorizer
	roles         *authorization.RoleService
	grants        *authorization.GrantStore
	authenticator httpapi.AccessTokenAuthenticator
}

// HandlerOption configures additive authorization routes without breaking the
// existing NewHandler call sites.
type HandlerOption func(*Handler)

// WithGrantStore enables the global-admin explicit permission-grant routes.
func WithGrantStore(store *authorization.GrantStore) HandlerOption {
	return func(handler *Handler) {
		if store != nil {
			handler.grants = store
		}
	}
}

// NewHandler constructs the authorization HTTP adapter. authenticator must be
// the live users.UserService (or an equivalent adapter that enforces the
// access-token auth version and disabled state).
func NewHandler(
	authz authorization.Authorizer,
	roles *authorization.RoleService,
	authenticator httpapi.AccessTokenAuthenticator,
	opts ...HandlerOption,
) *Handler {
	handler := &Handler{authz: authz, roles: roles, authenticator: authenticator}
	for _, option := range opts {
		if option != nil {
			option(handler)
		}
	}
	return handler
}

// RegisterRoutes wires the additive /api/v2 RBAC routes.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	auth := httpapi.RequireUser(h.authenticator)

	e.GET("/api/v2/boards/:boardUuid/me/permissions", h.GetMyBoardPermissions, auth)

	e.GET("/api/v2/admin/boards/:boardUuid/role-bindings", h.ListRoleBindings, auth)
	e.POST("/api/v2/admin/boards/:boardUuid/role-bindings", h.CreateRoleBinding, auth)
	e.PATCH("/api/v2/admin/boards/:boardUuid/role-bindings/:bindingUuid", h.UpdateRoleBinding, auth)
	e.DELETE("/api/v2/admin/boards/:boardUuid/role-bindings/:bindingUuid", h.RevokeRoleBinding, auth)

	e.GET("/api/v2/admin/boards/:boardUuid/audit", h.ListBoardAudit, auth)

	// Global-admin explicit grants. Plugin-specific grant proxy routes stay in
	// the plugins worker and use the same GrantStore/registry.
	e.GET("/api/v2/admin/permission-grants", h.ListPermissionGrants, auth)
	e.POST("/api/v2/admin/permission-grants", h.CreatePermissionGrant, auth)
	e.DELETE("/api/v2/admin/permission-grants/:grantUuid", h.RevokePermissionGrant, auth)
}

// GetMyBoardPermissions returns the caller's effective board and module
// permissions. It intentionally has no pre-permission; module-scoped staff
// still receive their assigned module context.
func (h *Handler) GetMyBoardPermissions(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	effective, err := h.authz.Permissions(c.Request().Context(), principal, boardID)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	if len(effective.Permissions) == 0 && len(effective.Modules) == 0 {
		return writeProblem(c, http.StatusForbidden, errorCodeBoardAccessDenied, "Board access denied")
	}
	return c.JSON(http.StatusOK, boardPermissionsResponseFrom(effective))
}

// ListRoleBindings returns board bindings. The service filters by board_id and
// requires board.staff.manage.
func (h *Handler) ListRoleBindings(c *echo.Context) error {
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
	items, total, err := h.roles.ListPage(c.Request().Context(), principal, boardID, limit, offset)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	response := roleBindingListResponse{
		Items:  make([]boardRoleBindingResponse, 0, len(items)),
		Limit:  int64(limit),
		Offset: int64(offset),
		Total:  int64(total),
	}
	for _, item := range items {
		response.Items = append(response.Items, roleBindingResponseFrom(item))
	}
	return c.JSON(http.StatusOK, response)
}

// CreateRoleBinding grants a scoped role. Role strength, owner escalation,
// module parent integrity, target user existence, and expiry are validated in
// RoleService.Grant.
func (h *Handler) CreateRoleBinding(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}

	var req roleBindingCreateRequest
	if problem := bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validateCreateRequest(req); problem != nil {
		return problem.write(c)
	}

	userID, err := uuid.Parse(strings.TrimSpace(req.UserUUID))
	if err != nil {
		return validationProblem("userUuid", "Invalid UUID", "INVALID_FORMAT").write(c)
	}
	var moduleID *uuid.UUID
	if req.ModuleUUID != nil {
		parsed, err := uuid.Parse(strings.TrimSpace(*req.ModuleUUID))
		if err != nil {
			return validationProblem("moduleUuid", "Invalid UUID", "INVALID_FORMAT").write(c)
		}
		moduleID = &parsed
	}

	binding, err := h.roles.Grant(c.Request().Context(), principal, authorization.GrantRoleRequest{
		BoardID:   boardID,
		ModuleID:  moduleID,
		UserID:    userID,
		Role:      authorization.Role(strings.TrimSpace(req.Role)),
		ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, roleBindingResponseFrom(*binding))
}

// UpdateRoleBinding updates only expiresAt. Role/module changes are rejected as
// unknown fields and require revoke + grant.
func (h *Handler) UpdateRoleBinding(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	bindingID, problem := pathUUID(c, "bindingUuid")
	if problem != nil {
		return problem.write(c)
	}

	var req roleBindingPatchRequest
	if problem := bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if len(req.ExpiresAt) == 0 {
		return validationProblem("expiresAt", "expiresAt is required (use null to clear)", "REQUIRED").write(c)
	}
	expiresAt, problem := parseNullableTime(req.ExpiresAt, "expiresAt")
	if problem != nil {
		return problem.write(c)
	}

	binding, err := h.roles.UpdateExpiry(c.Request().Context(), principal, boardID, bindingID, expiresAt)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, roleBindingResponseFrom(*binding))
}

// RevokeRoleBinding revokes a binding. Revocation is observed by the next
// Authorize call because the authorizer queries live state.
func (h *Handler) RevokeRoleBinding(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	bindingID, problem := pathUUID(c, "bindingUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.roles.Revoke(c.Request().Context(), principal, boardID, bindingID); err != nil {
		return h.writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ListBoardAudit returns board-scoped audit events. Details are projected and
// redacted by the service; raw JSONB is never returned.
func (h *Handler) ListBoardAudit(c *echo.Context) error {
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
	events, total, err := h.roles.ListAudit(c.Request().Context(), principal, boardID, limit, offset)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	response := boardAuditResponse{
		Items:  make([]boardAuditEventResponse, 0, len(events)),
		Limit:  int64(limit),
		Offset: int64(offset),
		Total:  int64(total),
	}
	for _, event := range events {
		response.Items = append(response.Items, auditEventResponseFrom(event))
	}
	return c.JSON(http.StatusOK, response)
}

// ListPermissionGrants returns explicit permission grants. The store re-reads
// the live global admin role; no Principal.GlobalRoles trust.
func (h *Handler) ListPermissionGrants(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	if h.grants == nil {
		return writeProblem(c, http.StatusInternalServerError, errorCodeInternalError, "Permission grant store is not configured")
	}
	limit, offset, problem := pageParams(c)
	if problem != nil {
		return problem.write(c)
	}
	grants, total, err := h.grants.List(c.Request().Context(), principal, limit, offset)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	response := permissionGrantListResponse{
		Items:  make([]permissionGrantResponse, 0, len(grants)),
		Limit:  int64(limit),
		Offset: int64(offset),
		Total:  int64(total),
	}
	for _, grant := range grants {
		response.Items = append(response.Items, permissionGrantResponseFrom(grant))
	}
	return c.JSON(http.StatusOK, response)
}

// CreatePermissionGrant grants one registered core or plugin permission to a
// user. Only a live global admin can pass GrantStore.Grant.
func (h *Handler) CreatePermissionGrant(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	if h.grants == nil {
		return writeProblem(c, http.StatusInternalServerError, errorCodeInternalError, "Permission grant store is not configured")
	}
	var req permissionGrantCreateRequest
	if problem := bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validatePermissionGrantRequest(req); problem != nil {
		return problem.write(c)
	}
	userID, err := uuid.Parse(strings.TrimSpace(req.UserUUID))
	if err != nil {
		return validationProblem("userUuid", "Invalid UUID", "INVALID_FORMAT").write(c)
	}
	var boardID *uuid.UUID
	if req.BoardUUID != nil && strings.TrimSpace(*req.BoardUUID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(*req.BoardUUID))
		if err != nil {
			return validationProblem("boardUuid", "Invalid UUID", "INVALID_FORMAT").write(c)
		}
		boardID = &parsed
	}
	var moduleID *uuid.UUID
	if req.ModuleUUID != nil && strings.TrimSpace(*req.ModuleUUID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(*req.ModuleUUID))
		if err != nil {
			return validationProblem("moduleUuid", "Invalid UUID", "INVALID_FORMAT").write(c)
		}
		moduleID = &parsed
	}
	scope := authorization.Scope{}
	if boardID != nil {
		scope.BoardID = *boardID
	}
	scope.ModuleID = moduleID
	grant, err := h.grants.Grant(c.Request().Context(), authorization.GrantParams{
		UserID:     userID,
		Permission: authorization.Permission(strings.TrimSpace(req.Permission)),
		Scope:      scope,
		GrantedBy:  principal.UserID,
		ExpiresAt:  req.ExpiresAt,
	})
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, permissionGrantResponseFrom(*grant))
}

func (h *Handler) RevokePermissionGrant(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required")
	}
	if h.grants == nil {
		return writeProblem(c, http.StatusInternalServerError, errorCodeInternalError, "Permission grant store is not configured")
	}
	grantID, problem := pathUUID(c, "grantUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.grants.Revoke(c.Request().Context(), principal, grantID); err != nil {
		return h.writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) writeServiceError(c *echo.Context, err error) error {
	switch {
	case stderrors.Is(err, authorization.ErrUnauthorized), stderrors.Is(err, authorization.ErrUserDisabled):
		return writeProblem(c, http.StatusUnauthorized, errorCodeUnauthorized, "Authentication required or credentials invalid")
	case stderrors.Is(err, authorization.ErrGrantNotAllowed):
		return writeProblem(c, http.StatusForbidden, errorCodeRoleGrantNotAllowed, "Role grant not allowed")
	case stderrors.Is(err, authorization.ErrForbidden):
		return writeProblem(c, http.StatusForbidden, errorCodeForbidden, "Forbidden")
	case stderrors.Is(err, authorization.ErrBoardNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeBoardNotFound, "Board not found")
	case stderrors.Is(err, authorization.ErrModuleBoardMismatch), stderrors.Is(err, authorization.ErrModuleNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeModuleNotFound, "Module not found")
	case stderrors.Is(err, authorization.ErrUserNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeUserNotFound, "User not found")
	case stderrors.Is(err, authorization.ErrTargetUserDisabled):
		return validationProblem("userUuid", "User account is disabled", "INVALID_VALUE").write(c)
	case stderrors.Is(err, authorization.ErrBindingNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodeRoleBindingNotFound, "Role binding not found")
	case stderrors.Is(err, authorization.ErrBindingExists):
		return writeProblem(c, http.StatusConflict, errorCodeRoleBindingExists, "An active role binding already exists")
	case stderrors.Is(err, authorization.ErrPermissionNotRegistered):
		return validationProblem("permission", "Permission is not registered", "INVALID_VALUE").write(c)
	case stderrors.Is(err, authorization.ErrGrantScopeNotAllowed):
		return validationProblem("boardUuid", "Permission grant scope is not allowed", "INVALID_VALUE").write(c)
	case stderrors.Is(err, authorization.ErrGrantNotFound):
		return writeProblem(c, http.StatusNotFound, errorCodePermissionGrantNotFound, "Permission grant not found")
	case stderrors.Is(err, authorization.ErrGrantExists):
		return validationProblem("permission", "An active permission grant already exists", "INVALID_VALUE").write(c)
	case stderrors.Is(err, authorization.ErrInvalidRole):
		return validationProblem("role", "Unknown role", "INVALID_VALUE").write(c)
	case stderrors.Is(err, authorization.ErrExpiryNotFuture):
		return validationProblem("expiresAt", "Expiry must be in the future", "INVALID_VALUE").write(c)
	case stderrors.Is(err, authorization.ErrOwnerModuleGrant):
		return validationProblem("moduleUuid", "board_owner cannot be module-scoped", "INVALID_VALUE").write(c)
	case stderrors.Is(err, authorization.ErrInvalidScope):
		return validationProblem("boardUuid", "Invalid board scope", "INVALID_VALUE").write(c)
	default:
		slog.Default().Error("authorization request failed", "error", err)
		return writeProblem(c, http.StatusInternalServerError, errorCodeInternalError, "Internal server error")
	}
}
