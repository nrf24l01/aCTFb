// Package http maps the six documented /api/v2/admin/integrations operations
// onto the integration service. Credentials are write-only: no response,
// error or log line contains them.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/integrations"
)

const maxBodyBytes = 1 << 20

// Handler is the admin integrations HTTP adapter.
type Handler struct {
	service *integrations.Service
	auth    httpapi.AccessTokenAuthenticator
}

// NewHandler constructs the adapter.
func NewHandler(service *integrations.Service, auth httpapi.AccessTokenAuthenticator) *Handler {
	return &Handler{service: service, auth: auth}
}

// RegisterRoutes installs exactly the six documented operations.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	if e == nil || h.service == nil {
		return
	}
	requireUser := httpapi.RequireUser(h.auth)
	e.GET("/api/v2/admin/integrations", h.List, requireUser)
	e.POST("/api/v2/admin/integrations", h.Create, requireUser)
	e.GET("/api/v2/admin/integrations/:integrationUuid", h.Get, requireUser)
	e.PATCH("/api/v2/admin/integrations/:integrationUuid", h.Update, requireUser)
	e.DELETE("/api/v2/admin/integrations/:integrationUuid", h.Delete, requireUser)
	e.POST("/api/v2/admin/integrations/:integrationUuid/test", h.Test, requireUser)
}

func (h *Handler) List(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	limit, offset, err := pagination(c)
	if err != nil {
		return writeError(c, http.StatusUnprocessableEntity, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "limit")
	}
	page, err := h.service.List(c.Request().Context(), actor, integrations.ListQuery{Limit: limit, Offset: offset})
	if err != nil {
		return writeServiceError(c, err)
	}
	items := make([]*models.IntegrationSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, toSummary(item))
	}
	limit64, offset64 := int64(limit), int64(offset)
	total := page.Total
	return c.JSON(http.StatusOK, &models.IntegrationsResponse{Items: items, Limit: &limit64, Offset: &offset64, Total: &total})
}

func (h *Handler) Create(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	request, err := bindCreate(c)
	if err != nil {
		return writeBindError(c, err)
	}
	item, err := h.service.Create(c.Request().Context(), actor, request)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, toSummary(*item))
}

func (h *Handler) Get(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	id, err := pathUUID(c, "integrationUuid")
	if err != nil {
		return writeError(c, http.StatusUnprocessableEntity, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "integrationUuid")
	}
	item, err := h.service.Get(c.Request().Context(), actor, id)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toSummary(*item))
}

func (h *Handler) Update(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	id, err := pathUUID(c, "integrationUuid")
	if err != nil {
		return writeError(c, http.StatusUnprocessableEntity, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "integrationUuid")
	}
	input, err := bindPatch(c)
	if err != nil {
		return writeBindError(c, err)
	}
	item, err := h.service.Update(c.Request().Context(), actor, id, input)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toSummary(*item))
}

func (h *Handler) Delete(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	id, err := pathUUID(c, "integrationUuid")
	if err != nil {
		return writeError(c, http.StatusUnprocessableEntity, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "integrationUuid")
	}
	if err := h.service.Delete(c.Request().Context(), actor, id); err != nil {
		return writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) Test(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	id, err := pathUUID(c, "integrationUuid")
	if err != nil {
		return writeError(c, http.StatusUnprocessableEntity, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "integrationUuid")
	}
	result, err := h.service.TestConnection(c.Request().Context(), actor, id)
	if err != nil {
		return writeServiceError(c, err)
	}
	ok2 := result.OK
	message := result.Message
	return c.JSON(http.StatusOK, &models.IntegrationTestResponse{Ok: &ok2, Message: message})
}

func toSummary(item integrations.Integration) *models.IntegrationSummary {
	id := models.UUID(item.UUID.String())
	typ := models.IntegrationType(item.Type)
	status := item.Status
	name := item.Name
	configured := item.CredentialsConfigured
	statusValue := string(status)
	summary := &models.IntegrationSummary{
		UUID:                  &id,
		Type:                  &typ,
		Name:                  &name,
		Status:                &statusValue,
		CredentialsConfigured: &configured,
	}
	if item.Driver != "" {
		driver := item.Driver
		summary.Driver = &driver
	}
	if item.Kind != "" {
		kind := string(item.Kind)
		summary.Kind = &kind
	}
	if item.BaseURL != "" {
		uri := strfmt.URI(item.BaseURL)
		summary.BaseURL = &uri
	}
	summary.PublicationPolicyConfigured = item.Publication != nil
	return summary
}

func pagination(c *echo.Context) (int, int, error) {
	limit := 20
	offset := 0
	if raw := c.QueryParam("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, errors.New("invalid limit")
		}
		limit = parsed
	}
	if raw := c.QueryParam("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("invalid offset")
		}
		offset = parsed
	}
	return limit, offset, nil
}

func pathUUID(c *echo.Context, name string) (uuid.UUID, error) {
	raw := c.Param(name)
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed == uuid.Nil {
		return uuid.Nil, errors.New("invalid uuid")
	}
	return parsed, nil
}

func decodeStrict(c *echo.Context, dst any) error {
	body := c.Request().Body
	decoder := json.NewDecoder(io.LimitReader(body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
