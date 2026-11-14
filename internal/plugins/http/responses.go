package http

import (
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/plugins"
)

type healthResponse struct {
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

type pluginPermissionResponse struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
}

type pluginRouteResponse struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Permission string `json:"permission"`
	Scope      string `json:"scope"`
	UI         bool   `json:"ui,omitempty"`
}

type dependencyResponse struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type pluginResponse struct {
	ID                string                     `json:"id"`
	Name              string                     `json:"name"`
	Version           string                     `json:"version"`
	APIVersion        string                     `json:"apiVersion"`
	Status            string                     `json:"status"`
	Health            healthResponse             `json:"health"`
	Author            string                     `json:"author"`
	Capabilities      []string                   `json:"capabilities"`
	Permissions       []pluginPermissionResponse `json:"permissions"`
	Routes            []pluginRouteResponse      `json:"routes"`
	FrontendMode      string                     `json:"frontendMode,omitempty"`
	Dependencies      []dependencyResponse       `json:"dependencies,omitempty"`
	TrustedJSApproved bool                       `json:"trustedJSApproved"`
	InstalledAt       *time.Time                 `json:"installedAt,omitempty"`
	UpdatedAt         *time.Time                 `json:"updatedAt,omitempty"`
}

type pluginListResponse struct {
	Items  []pluginResponse `json:"items"`
	Limit  int64            `json:"limit"`
	Offset int64            `json:"offset"`
	Total  int64            `json:"total"`
}

type pluginValidateRequest struct {
	PackagePath string `json:"packagePath"`
}

type pluginInstallRequest struct {
	PackagePath         string   `json:"packagePath"`
	ApproveCapabilities []string `json:"approveCapabilities"`
	ApprovePermissions  []string `json:"approvePermissions"`
	ApproveTrustedJS    bool     `json:"approveTrustedJS"`
	Enable              bool     `json:"enable"`
}

type pluginUpgradeRequest struct {
	PackagePath         string   `json:"packagePath"`
	ApproveCapabilities []string `json:"approveCapabilities"`
	ApprovePermissions  []string `json:"approvePermissions"`
	ApproveTrustedJS    bool     `json:"approveTrustedJS"`
}

type validationFinding struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

type pluginValidation struct {
	Valid        bool                       `json:"valid"`
	Findings     []validationFinding        `json:"findings"`
	ID           string                     `json:"id,omitempty"`
	Name         string                     `json:"name,omitempty"`
	Version      string                     `json:"version,omitempty"`
	APIVersion   string                     `json:"apiVersion,omitempty"`
	BackendMode  string                     `json:"backendMode,omitempty"`
	FrontendMode string                     `json:"frontendMode,omitempty"`
	Dependencies []dependencyResponse       `json:"dependencies,omitempty"`
	Capabilities []string                   `json:"capabilities,omitempty"`
	Permissions  []pluginPermissionResponse `json:"permissions,omitempty"`
}

type pluginLogsResponse struct {
	Lines []string `json:"lines"`
}

type eventDeliveryResponse struct {
	EventID     string     `json:"eventId"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	LastError   *string    `json:"lastError"`
	NextAttempt *time.Time `json:"nextAttemptAt"`
	DeliveredAt *time.Time `json:"deliveredAt"`
}

type eventDeliveryListResponse struct {
	Items  []eventDeliveryResponse `json:"items"`
	Limit  int64                   `json:"limit"`
	Offset int64                   `json:"offset"`
	Total  int64                   `json:"total"`
}

type grantCreateRequest struct {
	UserUUID   string     `json:"userUuid"`
	Permission string     `json:"permission"`
	BoardUUID  *string    `json:"boardUuid"`
	ModuleUUID *string    `json:"moduleUuid"`
	ExpiresAt  *time.Time `json:"expiresAt"`
}

type grantResponse struct {
	UUID          string     `json:"uuid"`
	UserUUID      string     `json:"userUuid"`
	Permission    string     `json:"permission"`
	BoardUUID     *string    `json:"boardUuid,omitempty"`
	ModuleUUID    *string    `json:"moduleUuid,omitempty"`
	GrantedByUUID string     `json:"grantedByUuid"`
	CreatedAt     time.Time  `json:"createdAt"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
}

type grantListResponse struct {
	Items  []grantResponse `json:"items"`
	Limit  int64           `json:"limit"`
	Offset int64           `json:"offset"`
	Total  int64           `json:"total"`
}

type visibleItemResponse struct {
	Path      string `json:"path"`
	Label     string `json:"label"`
	IframeURL string `json:"iframeUrl"`
}

type visibleResponse struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Menu      []visibleItemResponse `json:"menu"`
	Pages     []visibleItemResponse `json:"pages"`
	TrustedJS *trustedJSResponse    `json:"trustedJS,omitempty"`
}

type trustedJSResponse struct {
	Entry     string `json:"entry"`
	URL       string `json:"url"`
	Integrity string `json:"integrity"`
}

type visibleListResponse struct {
	Items []visibleResponse `json:"items"`
}

func pluginResponseFrom(installed plugins.Installed) pluginResponse {
	health := installed.Health
	if health.Status == "" {
		health.Status = "unknown"
	}
	if health.CheckedAt.IsZero() {
		health.CheckedAt = installed.UpdatedAt
	}
	response := pluginResponse{
		ID:                string(installed.ID),
		Name:              installed.Manifest.Name,
		Version:           installed.Version,
		APIVersion:        installed.Manifest.APIVersion,
		Status:            string(installed.Status),
		Health:            healthResponse{Status: health.Status, Message: health.Message, CheckedAt: health.CheckedAt},
		Author:            installed.Manifest.Author,
		Capabilities:      make([]string, 0, len(installed.Manifest.Capabilities)),
		Permissions:       make([]pluginPermissionResponse, 0, len(installed.Manifest.Permissions)),
		Routes:            make([]pluginRouteResponse, 0, len(installed.Manifest.Routes)),
		Dependencies:      make([]dependencyResponse, 0, len(installed.Manifest.Dependencies)),
		FrontendMode:      string(installed.Manifest.Frontend.Mode),
		TrustedJSApproved: installed.TrustedJSApproved,
	}
	for _, dependency := range installed.Manifest.Dependencies {
		response.Dependencies = append(response.Dependencies, dependencyResponse{ID: string(dependency.ID), Version: dependency.Version})
	}
	for _, capability := range installed.Manifest.Capabilities {
		response.Capabilities = append(response.Capabilities, string(capability))
	}
	for _, declaration := range installed.Manifest.Permissions {
		response.Permissions = append(response.Permissions, pluginPermissionResponse{
			ID: string(declaration.ID), Scope: declaration.Scope,
		})
	}
	for _, route := range installed.Manifest.Routes {
		response.Routes = append(response.Routes, pluginRouteResponse{
			Method: route.Method, Path: route.Path, Permission: string(route.Permission), Scope: route.Scope, UI: route.UI,
		})
	}
	installedAt := installed.InstalledAt
	updatedAt := installed.UpdatedAt
	response.InstalledAt = &installedAt
	response.UpdatedAt = &updatedAt
	return response
}

func eventDeliveryResponseFrom(delivery plugins.EventDelivery) eventDeliveryResponse {
	return eventDeliveryResponse{
		EventID:     delivery.EventID.String(),
		Status:      delivery.Status,
		Attempts:    delivery.Attempts,
		LastError:   delivery.LastError,
		NextAttempt: delivery.NextAttempt,
		DeliveredAt: delivery.DeliveredAt,
	}
}

func grantResponseFrom(grant authorization.PermissionGrant) grantResponse {
	response := grantResponse{
		UUID:          grant.ID.String(),
		UserUUID:      grant.UserID.String(),
		Permission:    string(grant.Permission),
		GrantedByUUID: grant.GrantedBy.String(),
		CreatedAt:     grant.CreatedAt,
		ExpiresAt:     grant.ExpiresAt,
		RevokedAt:     grant.RevokedAt,
	}
	if grant.BoardID != nil {
		value := grant.BoardID.String()
		response.BoardUUID = &value
	}
	if grant.ModuleID != nil {
		value := grant.ModuleID.String()
		response.ModuleUUID = &value
	}
	return response
}

func visibleResponseFrom(value plugins.Visible) visibleResponse {
	response := visibleResponse{
		ID:    string(value.ID),
		Name:  value.Name,
		Menu:  make([]visibleItemResponse, 0, len(value.Menu)),
		Pages: make([]visibleItemResponse, 0, len(value.Pages)),
	}
	for _, item := range value.Menu {
		response.Menu = append(response.Menu, visibleItemResponse{Path: item.Path, Label: item.Label, IframeURL: item.IframeURL})
	}
	for _, item := range value.Pages {
		response.Pages = append(response.Pages, visibleItemResponse{Path: item.Path, Label: item.Label, IframeURL: item.IframeURL})
	}
	if value.TrustedJS != nil {
		response.TrustedJS = &trustedJSResponse{
			Entry: value.TrustedJS.Entry, URL: value.TrustedJS.URL, Integrity: value.TrustedJS.Integrity,
		}
	}
	return response
}

const (
	codeUnauthorized          = oapimodels.ErrorCodeUNAUTHORIZED
	codeForbidden             = oapimodels.ErrorCodeFORBIDDEN
	codeNotFound              = oapimodels.ErrorCode("NOT_FOUND")
	codeValidationFailed      = oapimodels.ErrorCodeVALIDATIONFAILED
	codeInvalidJSON           = oapimodels.ErrorCodeINVALIDJSON
	codeInternalError         = oapimodels.ErrorCodeINTERNALERROR
	codeDependencyUnavailable = oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE
)

type apiProblem struct {
	status  int
	code    oapimodels.ErrorCode
	message string
	fields  []*oapimodels.FieldError
}

func (p apiProblem) write(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	trace := oapimodels.UUID(strfmt.UUID(traceID(c)))
	path := ""
	if c.Request() != nil && c.Request().URL != nil {
		path = c.Request().URL.Path
	}
	timestamp := strfmt.DateTime(time.Now().UTC())
	message := p.message
	if message == "" {
		message = stdhttp.StatusText(p.status)
	}
	if len(p.fields) > 0 {
		return c.JSON(p.status, &oapimodels.ValidationError{
			Code:        &p.code,
			FieldErrors: p.fields,
			Message:     &message,
			Path:        &path,
			Timestamp:   &timestamp,
			TraceID:     &trace,
		})
	}
	return c.JSON(p.status, &oapimodels.APIError{
		Code:      &p.code,
		Message:   &message,
		Path:      &path,
		Timestamp: &timestamp,
		TraceID:   &trace,
	})
}

func writeProblem(c *echo.Context, status int, code oapimodels.ErrorCode, message string) error {
	return apiProblem{status: status, code: code, message: message}.write(c)
}

func validationProblem(field, message string) *apiProblem {
	return &apiProblem{
		status:  stdhttp.StatusUnprocessableEntity,
		code:    codeValidationFailed,
		message: "Validation failed",
		fields: []*oapimodels.FieldError{{
			Field: &field, Issue: &message, Code: "INVALID_VALUE",
		}},
	}
}

func (h *Handler) writeError(c *echo.Context, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, plugins.ErrPermissionDenied), errors.Is(err, authorization.ErrForbidden):
		return writeProblem(c, stdhttp.StatusForbidden, codeForbidden, "Forbidden")
	case errors.Is(err, plugins.ErrNotFound), errors.Is(err, plugins.ErrRouteNotFound), errors.Is(err, authorization.ErrGrantNotFound):
		return writeProblem(c, stdhttp.StatusNotFound, codeNotFound, "Not found")
	case errors.Is(err, plugins.ErrBackendUnavailable):
		return writeProblem(c, stdhttp.StatusServiceUnavailable, codeDependencyUnavailable, "Plugin backend unavailable")
	case errors.Is(err, plugins.ErrDependencyMissing):
		return writeProblem(c, stdhttp.StatusConflict, oapimodels.ErrorCode("PLUGIN_DEPENDENCY_MISSING"), boundedMessage(err.Error()))
	case errors.Is(err, plugins.ErrDependencyCycle):
		return writeProblem(c, stdhttp.StatusConflict, oapimodels.ErrorCode("PLUGIN_DEPENDENCY_CYCLE"), boundedMessage(err.Error()))
	case errors.Is(err, plugins.ErrDependencyInUse):
		return writeProblem(c, stdhttp.StatusConflict, oapimodels.ErrorCode("PLUGIN_DEPENDENCY_IN_USE"), boundedMessage(err.Error()))
	case errors.Is(err, plugins.ErrDependencyIncompatible), errors.Is(err, plugins.ErrTrustedJSDisabled), errors.Is(err, plugins.ErrTrustedJSNotApproved):
		return writeProblem(c, stdhttp.StatusUnprocessableEntity, codeValidationFailed, boundedMessage(err.Error()))
	case errors.Is(err, plugins.ErrInvalidPackage), errors.Is(err, plugins.ErrManifestInvalid),
		errors.Is(err, plugins.ErrValidation), errors.Is(err, plugins.ErrIncompatible), errors.Is(err, plugins.ErrExecDisabled):
		return writeProblem(c, stdhttp.StatusUnprocessableEntity, codeValidationFailed, boundedMessage(err.Error()))
	case errors.Is(err, plugins.ErrAlreadyInstalled):
		return writeProblem(c, stdhttp.StatusUnprocessableEntity, oapimodels.ErrorCode("PLUGIN_ALREADY_INSTALLED"), "Plugin is already installed; use upgrade")
	case errors.Is(err, plugins.ErrRequestRejected):
		return writeProblem(c, stdhttp.StatusBadRequest, codeValidationFailed, boundedMessage(err.Error()))
	default:
		return writeProblem(c, stdhttp.StatusInternalServerError, codeInternalError, "Internal error")
	}
}

func bindJSON(c *echo.Context, target any) *apiProblem {
	if c.Request().Body == nil {
		return &apiProblem{status: stdhttp.StatusUnprocessableEntity, code: codeInvalidJSON, message: "Request body is required"}
	}
	decoder := json.NewDecoder(io.LimitReader(c.Request().Body, maxProxyBodyBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if strings.HasPrefix(err.Error(), `json: unknown field "`) {
			field := strings.TrimSuffix(strings.TrimPrefix(err.Error(), `json: unknown field "`), `"`)
			return validationProblem(field, "Unknown field")
		}
		return &apiProblem{status: stdhttp.StatusUnprocessableEntity, code: codeInvalidJSON, message: "Invalid JSON body"}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &apiProblem{status: stdhttp.StatusUnprocessableEntity, code: codeInvalidJSON, message: "Trailing JSON data"}
	}
	return nil
}

func pageParams(c *echo.Context) (int, int) {
	limit := 50
	offset := 0
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 1 && parsed <= 100 {
			limit = parsed
		}
	}
	if raw := strings.TrimSpace(c.QueryParam("offset")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	return limit, offset
}

func traceID(c *echo.Context) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Get(httpapi.TraceIDKey).(string); ok {
		return value
	}
	return ""
}

func truncateField(value string, size int) string {
	if len(value) <= size {
		return value
	}
	return value[:size]
}

func boundedMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1024 {
		value = value[:1024]
	}
	if value == "" {
		value = "Validation failed"
	}
	return value
}
