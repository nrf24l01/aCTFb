// Package http maps the plugin manager to the additive /api/v2 admin, visible
// metadata and namespaced proxy routes. Every operation re-authorizes in the
// manager; the browser never receives plugin service tokens or delegations.
package http

import (
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/plugins"
)

const maxProxyBodyBytes = 1 << 20

// Handler is the plugin HTTP adapter.
type Handler struct {
	manager       *plugins.Manager
	authenticator httpapi.AccessTokenAuthenticator
}

// NewHandler constructs the adapter. authenticator must enforce access-token
// auth version and disabled state (the live users.UserService).
func NewHandler(manager *plugins.Manager, authenticator httpapi.AccessTokenAuthenticator) *Handler {
	return &Handler{manager: manager, authenticator: authenticator}
}

// RegisterRoutes wires the frozen plugin routes. It intentionally does not
// serve plugin HTML/assets on the core origin.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	auth := httpapi.RequireUser(h.authenticator)
	e.GET("/api/v2/admin/plugins", h.List, auth)
	e.POST("/api/v2/admin/plugins/validate", h.Validate, auth)
	e.POST("/api/v2/admin/plugins/install", h.Install, auth)
	e.GET("/api/v2/admin/plugins/:pluginId", h.Get, auth)
	e.DELETE("/api/v2/admin/plugins/:pluginId", h.Remove, auth)
	e.POST("/api/v2/admin/plugins/:pluginId/enable", h.Enable, auth)
	e.POST("/api/v2/admin/plugins/:pluginId/disable", h.Disable, auth)
	e.POST("/api/v2/admin/plugins/:pluginId/upgrade", h.Upgrade, auth)
	e.GET("/api/v2/admin/plugins/:pluginId/logs", h.Logs, auth)
	e.GET("/api/v2/admin/plugins/:pluginId/event-deliveries", h.ListEventDeliveries, auth)
	e.POST("/api/v2/admin/plugins/:pluginId/event-deliveries/:eventId/replay", h.ReplayEvent, auth)
	e.GET("/api/v2/admin/plugins/:pluginId/permission-grants", h.ListGrants, auth)
	e.POST("/api/v2/admin/plugins/:pluginId/permission-grants", h.CreateGrant, auth)
	e.DELETE("/api/v2/admin/plugins/:pluginId/permission-grants/:grantUuid", h.RevokeGrant, auth)
	e.GET("/api/v2/plugins", h.Visible, auth)
	e.GET("/api/v2/trusted-plugins/:pluginId/bundle", h.TrustedBundle, auth)
	e.Any("/api/v2/plugins/:pluginId/*", h.Proxy, auth)
}

// List returns installed plugins.
func (h *Handler) List(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	limit, offset := pageParams(c)
	items, err := h.manager.List(c.Request().Context(), principal)
	if err != nil {
		return h.writeError(c, err)
	}
	total := len(items)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	response := pluginListResponse{
		Items:  make([]pluginResponse, 0, end-start),
		Limit:  int64(limit),
		Offset: int64(offset),
		Total:  int64(total),
	}
	for _, item := range items[start:end] {
		response.Items = append(response.Items, pluginResponseFrom(item))
	}
	return c.JSON(stdhttp.StatusOK, response)
}

// Validate validates a server-side package without executing it.
func (h *Handler) Validate(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	var request pluginValidateRequest
	if problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if strings.TrimSpace(request.PackagePath) == "" || len(request.PackagePath) > 512 {
		return validationProblem("packagePath", "packagePath is required and must be at most 512 characters").write(c)
	}
	validation, err := h.manager.Validate(c.Request().Context(), principal, request.PackagePath)
	if err != nil {
		return h.writeError(c, err)
	}
	findings := make([]validationFinding, 0, len(validation.Findings))
	for _, finding := range validation.Findings {
		findings = append(findings, validationFinding{
			Path: truncateField(finding.Path, 512), Code: truncateField(finding.Code, 128),
			Message: truncateField(finding.Message, 2000), Severity: finding.Severity,
		})
	}
	capabilities := make([]string, 0, len(validation.Capabilities))
	for _, capability := range validation.Capabilities {
		capabilities = append(capabilities, string(capability))
	}
	permissions := make([]pluginPermissionResponse, 0, len(validation.Permissions))
	for _, permission := range validation.Permissions {
		permissions = append(permissions, pluginPermissionResponse{
			ID: string(permission.ID), Scope: permission.Scope,
		})
	}
	dependencies := make([]dependencyResponse, 0, len(validation.Dependencies))
	for _, dependency := range validation.Dependencies {
		dependencies = append(dependencies, dependencyResponse{ID: string(dependency.ID), Version: dependency.Version})
	}
	return c.JSON(stdhttp.StatusOK, pluginValidation{
		Valid:        validation.Valid,
		Findings:     findings,
		ID:           string(validation.ID),
		Name:         truncateField(validation.Name, 128),
		Version:      truncateField(validation.Version, 64),
		APIVersion:   truncateField(validation.APIVersion, 32),
		BackendMode:  string(validation.BackendMode),
		FrontendMode: string(validation.FrontendMode),
		Dependencies: dependencies,
		Capabilities: capabilities,
		Permissions:  permissions,
	})
}

// Install installs a package and never auto-enables it.
func (h *Handler) Install(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	var request pluginInstallRequest
	if problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if request.Enable {
		return validationProblem("enable", "enable=true is rejected; install leaves the plugin installed and a separate enable call is required").write(c)
	}
	if request.ApproveCapabilities == nil {
		return validationProblem("approveCapabilities", "approveCapabilities is required (send [] when none are requested)").write(c)
	}
	if request.ApprovePermissions == nil {
		return validationProblem("approvePermissions", "approvePermissions is required (send [] when none are requested)").write(c)
	}
	if strings.TrimSpace(request.PackagePath) == "" || len(request.PackagePath) > 512 {
		return validationProblem("packagePath", "packagePath is required and must be at most 512 characters").write(c)
	}
	capabilities := make([]plugins.Capability, 0, len(request.ApproveCapabilities))
	for _, value := range request.ApproveCapabilities {
		capabilities = append(capabilities, plugins.Capability(value))
	}
	permissions := make([]authorization.Permission, 0, len(request.ApprovePermissions))
	for _, value := range request.ApprovePermissions {
		permissions = append(permissions, authorization.Permission(value))
	}
	installed, err := h.manager.Install(c.Request().Context(), principal, plugins.InstallRequest{
		PackagePath:         request.PackagePath,
		ApproveCapabilities: capabilities,
		ApprovePermissions:  permissions,
		ApproveTrustedJS:    request.ApproveTrustedJS,
	})
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(stdhttp.StatusCreated, pluginResponseFrom(*installed))
}

// Get returns one plugin.
func (h *Handler) Get(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	installed, err := h.manager.Get(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")))
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(stdhttp.StatusOK, pluginResponseFrom(*installed))
}

// Enable starts the isolated plugin backend.
func (h *Handler) Enable(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	installed, err := h.manager.Enable(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")))
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(stdhttp.StatusOK, pluginResponseFrom(*installed))
}

// Disable stops routes and preserves plugin data.
func (h *Handler) Disable(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	installed, err := h.manager.Disable(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")))
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(stdhttp.StatusOK, pluginResponseFrom(*installed))
}

// Upgrade applies a new package version without adding permissions.
func (h *Handler) Upgrade(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	var request pluginUpgradeRequest
	if problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	if strings.TrimSpace(request.PackagePath) == "" || len(request.PackagePath) > 512 {
		return validationProblem("packagePath", "packagePath is required and must be at most 512 characters").write(c)
	}
	installed, err := h.manager.UpgradeWithApproval(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")), request.PackagePath, request.ApproveTrustedJS)
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(stdhttp.StatusOK, pluginResponseFrom(*installed))
}

// Remove removes the registry row but not private plugin data.
func (h *Handler) Remove(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	if err := h.manager.Remove(c.Request().Context(), principal, plugins.ID(c.Param("pluginId"))); err != nil {
		return h.writeError(c, err)
	}
	return c.NoContent(stdhttp.StatusNoContent)
}

// Logs returns bounded logs.
func (h *Handler) Logs(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	lines, err := h.manager.Logs(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")), 1000)
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(stdhttp.StatusOK, pluginLogsResponse{Lines: lines})
}

// ListEventDeliveries returns delivery rows for one plugin.
func (h *Handler) ListEventDeliveries(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	limit, offset := pageParams(c)
	items, total, err := h.manager.ListEventDeliveries(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")), limit, offset)
	if err != nil {
		return h.writeError(c, err)
	}
	response := eventDeliveryListResponse{
		Items: make([]eventDeliveryResponse, 0, len(items)),
		Limit: int64(limit), Offset: int64(offset), Total: int64(total),
	}
	for _, item := range items {
		response.Items = append(response.Items, eventDeliveryResponseFrom(item))
	}
	return c.JSON(stdhttp.StatusOK, response)
}

// ReplayEvent re-delivers one event from the outbox.
func (h *Handler) ReplayEvent(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	eventID, err := uuid.Parse(c.Param("eventId"))
	if err != nil {
		return validationProblem("eventId", "eventId must be a UUID").write(c)
	}
	if err := h.manager.ReplayEvent(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")), eventID); err != nil {
		return h.writeError(c, err)
	}
	return c.NoContent(stdhttp.StatusAccepted)
}

// ListGrants returns only this plugin's explicit grants.
func (h *Handler) ListGrants(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	limit, offset := pageParams(c)
	grants, total, err := h.manager.ListPermissionGrants(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")), limit, offset)
	if err != nil {
		return h.writeError(c, err)
	}
	response := grantListResponse{
		Items: make([]grantResponse, 0, len(grants)),
		Limit: int64(limit), Offset: int64(offset), Total: int64(total),
	}
	for _, grant := range grants {
		response.Items = append(response.Items, grantResponseFrom(grant))
	}
	return c.JSON(stdhttp.StatusOK, response)
}

// CreateGrant grants one explicit plugin-scoped permission.
func (h *Handler) CreateGrant(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	var request grantCreateRequest
	if problem := bindJSON(c, &request); problem != nil {
		return problem.write(c)
	}
	userID, err := uuid.Parse(strings.TrimSpace(request.UserUUID))
	if err != nil || userID == uuid.Nil {
		return validationProblem("userUuid", "userUuid must be a UUID").write(c)
	}
	permission := authorization.Permission(strings.TrimSpace(request.Permission))
	if permission == "" || len(permission) > 128 {
		return validationProblem("permission", "permission is required and must be at most 128 characters").write(c)
	}
	input := plugins.GrantInput{UserID: userID, Permission: permission, ExpiresAt: request.ExpiresAt}
	if request.BoardUUID != nil {
		boardID, err := uuid.Parse(strings.TrimSpace(*request.BoardUUID))
		if err != nil || boardID == uuid.Nil {
			return validationProblem("boardUuid", "boardUuid must be a UUID").write(c)
		}
		input.BoardID = &boardID
	}
	if request.ModuleUUID != nil {
		moduleID, err := uuid.Parse(strings.TrimSpace(*request.ModuleUUID))
		if err != nil || moduleID == uuid.Nil {
			return validationProblem("moduleUuid", "moduleUuid must be a UUID").write(c)
		}
		input.ModuleID = &moduleID
	}
	grant, err := h.manager.CreatePermissionGrant(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")), input)
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(stdhttp.StatusCreated, grantResponseFrom(*grant))
}

// RevokeGrant revokes one grant in this plugin's namespace.
func (h *Handler) RevokeGrant(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	grantID, err := uuid.Parse(c.Param("grantUuid"))
	if err != nil {
		return validationProblem("grantUuid", "grantUuid must be a UUID").write(c)
	}
	if err := h.manager.RevokePermissionGrant(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")), grantID); err != nil {
		return h.writeError(c, err)
	}
	return c.NoContent(stdhttp.StatusNoContent)
}

// Visible returns plugin shell metadata permitted for the current user.
func (h *Handler) Visible(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	visible, err := h.manager.Visible(c.Request().Context(), principal)
	if err != nil {
		return h.writeError(c, err)
	}
	response := visibleListResponse{Items: make([]visibleResponse, 0, len(visible))}
	for _, item := range visible {
		response.Items = append(response.Items, visibleResponseFrom(item))
	}
	return c.JSON(stdhttp.StatusOK, response)
}

// TrustedBundle serves the explicitly approved full-trust ESM bundle from the
// platform origin. It is not an iframe asset and never carries JWT/service
// tokens; the browser verifies the advertised sha256 before import.
func (h *Handler) TrustedBundle(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	raw, integrity, err := h.manager.TrustedBundle(c.Request().Context(), principal, plugins.ID(c.Param("pluginId")))
	if err != nil {
		return h.writeError(c, err)
	}
	response := c.Response()
	response.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	response.Header().Set("Content-Security-Policy", "default-src 'none'")
	response.Header().Set("X-Plugin-Bundle-Integrity", integrity)
	response.WriteHeader(stdhttp.StatusOK)
	_, _ = response.Write(raw)
	return nil
}

// Proxy forwards only declared methods/paths under this plugin's namespace.
func (h *Handler) Proxy(c *echo.Context) error {
	principal, ok := currentPrincipal(c)
	if !ok {
		return writeProblem(c, stdhttp.StatusUnauthorized, codeUnauthorized, "Authentication required")
	}
	request := c.Request()
	if request.ContentLength > maxProxyBodyBytes {
		return writeProblem(c, stdhttp.StatusRequestEntityTooLarge, codeValidationFailed, "Request body exceeds the plugin proxy limit")
	}
	body := make([]byte, 0)
	if request.Body != nil {
		limited := stdhttp.MaxBytesReader(c.Response(), request.Body, maxProxyBodyBytes+1)
		read, err := io.ReadAll(limited)
		if err != nil || len(read) > maxProxyBodyBytes {
			return writeProblem(c, stdhttp.StatusBadRequest, codeValidationFailed, "Invalid request body")
		}
		body = read
	}
	query := make(url.Values, len(request.URL.Query()))
	for key, values := range request.URL.Query() {
		query[key] = append([]string(nil), values...)
	}
	pluginID := plugins.ID(c.Param("pluginId"))
	response, err := h.manager.Proxy(c.Request().Context(), plugins.ProxyRequest{
		PluginID: pluginID,
		Actor:    principal,
		Method:   request.Method,
		Path:     request.URL.Path,
		Query:    query,
		Headers:  request.Header,
		Body:     body,
	})
	if err != nil {
		return h.writeError(c, err)
	}
	for key, values := range response.Headers {
		for _, value := range values {
			c.Response().Header().Add(key, value)
		}
	}
	c.Response().WriteHeader(response.Status)
	if len(response.Body) > 0 {
		_, _ = c.Response().Write(response.Body)
	}
	return nil
}

func currentPrincipal(c *echo.Context) (authorization.Principal, bool) {
	return httpapi.CurrentPrincipal(c)
}
