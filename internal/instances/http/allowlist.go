package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/instances"
)

// RegisterAllowlistRoutes installs the three documented exact-version
// allowlist operations. Mutations require global admin; enumeration returns
// only provider target ids and kind, never credentials or package files.
func (h *Handler) RegisterAllowlistRoutes(e *echo.Echo) {
	if e == nil || h.service == nil {
		return
	}
	requireUser := httpapi.RequireUser(h.auth)
	e.GET("/api/v2/admin/deployment-templates/:templateUuid/allowlist", h.AdminListAllowlists, requireUser)
	e.POST("/api/v2/admin/deployment-templates/:templateUuid/allowlist", h.AdminCreateAllowlist, requireUser)
	e.DELETE("/api/v2/admin/deployment-templates/:templateUuid/allowlist/:allowlistUuid", h.AdminRevokeAllowlist, requireUser)
}

// ListApprovedInstanceTemplates returns only active allowlisted immutable
// versions for the exact board/module scope. It is safe for a scoped owner or
// module instance admin; no provider target or secret is returned.
func (h *Handler) ListApprovedInstanceTemplates(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	boardID, err := pathUUID(c, "boardUuid")
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "boardUuid")
	}
	moduleID, err := pathUUID(c, "moduleUuid")
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "moduleUuid")
	}
	items, err := h.service.ListApprovedTemplateVersions(c.Request().Context(), actor, boardID, moduleID)
	if err != nil {
		return writeServiceError(c, err)
	}
	response := make([]*models.ApprovedInstanceTemplate, 0, len(items))
	for _, item := range items {
		templateUUID := models.UUID(item.TemplateUUID.String())
		slug := item.Slug
		name := item.Name
		version := int64(item.Version)
		var policy any
		if len(item.ParametersPolicy) > 0 {
			_ = json.Unmarshal(item.ParametersPolicy, &policy)
		}
		parameters := make([]*models.ApprovedTemplateParameter, 0, len(item.Parameters))
		for _, parameter := range item.Parameters {
			key := parameter.Key
			label := parameter.Label
			paramType := parameter.Type
			options := append([]string(nil), parameter.Options...)
			defaultValue := parameter.Default
			required := parameter.Required
			secret := parameter.Secret
			parameters = append(parameters, &models.ApprovedTemplateParameter{
				Key: &key, Label: &label, Type: &paramType, Options: options,
				Default: defaultValue, Required: &required, Secret: &secret,
			})
		}
		response = append(response, &models.ApprovedInstanceTemplate{
			TemplateUUID: &templateUUID, Slug: &slug, Name: &name, Version: &version,
			ParametersPolicy: policy, Parameters: parameters,
		})
	}
	return c.JSON(http.StatusOK, &models.ApprovedInstanceTemplateListResponse{Items: response})
}

func (h *Handler) AdminListAllowlists(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "templateUuid")
	}
	query := instances.AllowlistListQuery{}
	if raw := c.QueryParam("boardUuid"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "boardUuid")
		}
		query.BoardID = &parsed
	}
	if raw := c.QueryParam("moduleUuid"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "moduleUuid")
		}
		query.ModuleID = &parsed
	}
	items, err := h.service.ListAllowlists(c.Request().Context(), actor, templateID, query)
	if err != nil {
		return writeServiceError(c, err)
	}
	response := &models.DeploymentTemplateAllowlistListResponse{Items: make([]*models.DeploymentTemplateAllowlist, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, toAllowlistModel(item))
	}
	return c.JSON(http.StatusOK, response)
}

func (h *Handler) AdminCreateAllowlist(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "templateUuid")
	}
	var request struct {
		TemplateVersionUUID uuid.UUID        `json:"templateVersionUuid"`
		BoardUUID           *uuid.UUID       `json:"boardUuid,omitempty"`
		ModuleUUID          *uuid.UUID       `json:"moduleUuid,omitempty"`
		ParametersPolicy    json.RawMessage  `json:"parametersPolicy,omitempty"`
		ExpiresAt           *strfmt.DateTime `json:"expiresAt,omitempty"`
		Targets             []struct {
			IntegrationUUID uuid.UUID `json:"integrationUuid"`
			Kind            string    `json:"kind"`
		} `json:"targets"`
	}
	if err := decodeStrict(c, &request); err != nil {
		return writeBindError(c, err)
	}
	input := instances.AllowlistCreateInput{
		TemplateVersionUUID: request.TemplateVersionUUID,
		BoardID:             request.BoardUUID,
		ModuleID:            request.ModuleUUID,
		ParametersPolicy:    request.ParametersPolicy,
	}
	if request.ExpiresAt != nil {
		expiry := time.Time(*request.ExpiresAt)
		input.ExpiresAt = &expiry
	}
	for _, target := range request.Targets {
		input.Targets = append(input.Targets, instances.AllowlistTarget{
			IntegrationUUID: target.IntegrationUUID,
			Kind:            deployment.Kind(target.Kind),
		})
	}
	allowlist, err := h.service.CreateAllowlist(c.Request().Context(), actor, templateID, input)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, toAllowlistModel(*allowlist))
}

func (h *Handler) AdminRevokeAllowlist(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "templateUuid")
	}
	allowlistID, err := pathUUID(c, "allowlistUuid")
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "allowlistUuid")
	}
	if err := h.service.RevokeAllowlist(c.Request().Context(), actor, templateID, allowlistID); err != nil {
		return writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func toAllowlistModel(item instances.Allowlist) *models.DeploymentTemplateAllowlist {
	id := models.UUID(item.UUID.String())
	versionID := models.UUID(item.TemplateVersionUUID.String())
	approvedBy := models.UUID(item.ApprovedBy.String())
	createdAt := strfmt.DateTime(item.CreatedAt)
	model := &models.DeploymentTemplateAllowlist{
		UUID: &id, TemplateVersionUUID: &versionID, ApprovedByUUID: &approvedBy,
		CreatedAt: &createdAt, Targets: make([]*models.DeploymentTemplateAllowlistTarget, 0, len(item.Targets)),
	}
	if item.BoardID != nil {
		boardID := models.UUID(item.BoardID.String())
		model.BoardUUID = &boardID
	}
	if item.ModuleID != nil {
		moduleID := models.UUID(item.ModuleID.String())
		model.ModuleUUID = &moduleID
	}
	if len(item.ParametersPolicy) > 0 && string(item.ParametersPolicy) != "null" {
		var policy any
		if json.Unmarshal(item.ParametersPolicy, &policy) == nil {
			model.ParametersPolicy = policy
		}
	}
	if item.ExpiresAt != nil {
		expires := strfmt.DateTime(*item.ExpiresAt)
		model.ExpiresAt = &expires
	}
	if item.RevokedAt != nil {
		revoked := strfmt.DateTime(*item.RevokedAt)
		model.RevokedAt = &revoked
	}
	for _, target := range item.Targets {
		integrationID := models.UUID(target.IntegrationUUID.String())
		kind := string(target.Kind)
		model.Targets = append(model.Targets, &models.DeploymentTemplateAllowlistTarget{
			IntegrationUUID: &integrationID, Kind: &kind,
		})
	}
	return model
}

var _ = strconv.Itoa
