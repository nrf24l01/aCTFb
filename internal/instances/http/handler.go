// Package http maps the documented task/admin instance and Pentest routes onto
// the instances service. The backend never provisions provider resources and
// never returns credentials or secret parameter values.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/instances"
)

const maxBodyBytes = 1 << 20

// Handler is the instance/pentest HTTP adapter.
type Handler struct {
	service *instances.Service
	auth    httpapi.AccessTokenAuthenticator
}

// NewHandler constructs the adapter.
func NewHandler(service *instances.Service, auth httpapi.AccessTokenAuthenticator) *Handler {
	return &Handler{service: service, auth: auth}
}

// RegisterRoutes installs the documented instance and Pentest operations.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	if e == nil || h.service == nil {
		return
	}
	requireUser := httpapi.RequireUser(h.auth)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/instance", h.StartTask, requireUser)
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/instance", h.GetTask, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/instance/actions", h.TaskAction, requireUser)
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/instance-templates", h.ListApprovedInstanceTemplates, requireUser)

	e.GET("/api/v2/admin/instances", h.AdminListInstances, requireUser)
	e.GET("/api/v2/admin/instances/:instanceUuid", h.AdminGetInstance, requireUser)
	e.POST("/api/v2/admin/instances/:instanceUuid/actions", h.AdminInstanceAction, requireUser)

	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest", h.PentestInfo, requireUser)
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/environment", h.PentestEnvironment, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/environment", h.StartPentestEnvironment, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/environment/actions", h.PentestEnvironmentAction, requireUser)
	e.POST("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submissions", h.SubmitPentest, requireUser)
	e.GET("/api/v2/boards/:boardUuid/modules/:moduleUuid/pentest/submissions", h.ListMyPentestSubmissions, requireUser)

	e.GET("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/config", h.AdminGetPentestConfig, requireUser)
	e.PUT("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/config", h.AdminPutPentestConfig, requireUser)
	e.GET("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/environments", h.AdminListPentestEnvironments, requireUser)
	e.GET("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/environments/:environmentUuid", h.AdminGetPentestEnvironment, requireUser)
	e.POST("/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/pentest/environments/:environmentUuid/actions", h.AdminPentestAction, requireUser)

	h.RegisterAllowlistRoutes(e)
}

func (h *Handler) StartTask(c *echo.Context) error {
	actor, boardID, moduleID, taskID, err := h.taskParams(c)
	if err != nil {
		return err
	}
	idempotency, ok := idempotencyKey(c)
	if !ok {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Idempotency-Key is required", "Idempotency-Key")
	}
	instance, err := h.service.StartTask(c.Request().Context(), actor, boardID, moduleID, taskID, idempotency)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusAccepted, toInstanceModel(instance))
}

func (h *Handler) GetTask(c *echo.Context) error {
	actor, boardID, moduleID, taskID, err := h.taskParams(c)
	if err != nil {
		return err
	}
	instance, err := h.service.GetTask(c.Request().Context(), actor, boardID, moduleID, taskID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toInstanceModel(instance))
}

func (h *Handler) TaskAction(c *echo.Context) error {
	actor, boardID, moduleID, taskID, err := h.taskParams(c)
	if err != nil {
		return err
	}
	action, err := bindAction(c)
	if err != nil {
		return writeBindError(c, err)
	}
	idempotency, ok := idempotencyKey(c)
	if !ok {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Idempotency-Key is required", "Idempotency-Key")
	}
	instance, err := h.service.ActionTask(c.Request().Context(), actor, boardID, moduleID, taskID, instances.Action(action), idempotency)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusAccepted, toInstanceModel(instance))
}

func (h *Handler) AdminListInstances(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	limit, offset, err := pagination(c)
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "limit")
	}
	boardID, moduleID, err := optionalFilters(c)
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "boardUuid")
	}
	items, total, err := h.service.AdminList(c.Request().Context(), actor, boardID, moduleID, limit, offset)
	if err != nil {
		return writeServiceError(c, err)
	}
	modelsItems := make([]*models.Instance, 0, len(items))
	for index := range items {
		modelsItems = append(modelsItems, toInstanceModel(&items[index]))
	}
	limit64, offset64, total64 := int64(limit), int64(offset), total
	return c.JSON(http.StatusOK, &models.AdminInstancesResponse{Items: modelsItems, Limit: &limit64, Offset: &offset64, Total: &total64})
}

func (h *Handler) AdminGetInstance(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	instanceID, err := pathUUID(c, "instanceUuid")
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "instanceUuid")
	}
	instance, err := h.service.Get(c.Request().Context(), actor, instanceID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toInstanceModel(instance))
}

func (h *Handler) AdminInstanceAction(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	instanceID, err := pathUUID(c, "instanceUuid")
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "instanceUuid")
	}
	action, err := bindAction(c)
	if err != nil {
		return writeBindError(c, err)
	}
	idempotency, ok := idempotencyKey(c)
	if !ok {
		idempotency = uuid.NewString()
	}
	instance, err := h.service.AdminAction(c.Request().Context(), actor, instanceID, instances.Action(action), idempotency)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusAccepted, toInstanceModel(instance))
}

func (h *Handler) PentestInfo(c *echo.Context) error {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return err
	}
	info, err := h.service.PentestInfo(c.Request().Context(), actor, boardID, moduleID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, &models.PentestModuleInfo{Attachments: []*models.FileRef{}, Submission: submissionPolicyModel(info.SubmissionPolicy)})
}

func (h *Handler) PentestEnvironment(c *echo.Context) error {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return err
	}
	instance, err := h.service.PentestEnvironment(c.Request().Context(), actor, boardID, moduleID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toInstanceModel(instance))
}

// StartPentestEnvironment provisions the environment. Nothing is started on a
// GET; this is the only participant creation route.
func (h *Handler) StartPentestEnvironment(c *echo.Context) error {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return err
	}
	idempotency, ok := idempotencyKey(c)
	if !ok {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Idempotency-Key is required", "Idempotency-Key")
	}
	instance, err := h.service.StartPentestEnvironment(c.Request().Context(), actor, boardID, moduleID, idempotency)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusAccepted, toInstanceModel(instance))
}

// ListMyPentestSubmissions returns only the caller subject's reports.
func (h *Handler) ListMyPentestSubmissions(c *echo.Context) error {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return err
	}
	limit, offset, err := pagination(c)
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "limit")
	}
	items, total, err := h.service.ListMyPentestSubmissions(c.Request().Context(), actor, boardID, moduleID, limit, offset)
	if err != nil {
		return writeServiceError(c, err)
	}
	modelsItems := make([]*models.Submission, 0, len(items))
	for index := range items {
		modelsItems = append(modelsItems, toSubmissionModel(&items[index]))
	}
	limit64, offset64 := int64(limit), int64(offset)
	return c.JSON(http.StatusOK, &models.SubmissionListResponse{
		Items: modelsItems, Limit: &limit64, Offset: &offset64, Total: &total,
	})
}

func (h *Handler) PentestEnvironmentAction(c *echo.Context) error {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return err
	}
	action, err := bindAction(c)
	if err != nil {
		return writeBindError(c, err)
	}
	idempotency, ok := idempotencyKey(c)
	if !ok {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Idempotency-Key is required", "Idempotency-Key")
	}
	instance, err := h.service.PentestAction(c.Request().Context(), actor, boardID, moduleID, instances.Action(action), idempotency)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusAccepted, toInstanceModel(instance))
}

func (h *Handler) SubmitPentest(c *echo.Context) error {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return err
	}
	var request struct {
		Text      string     `json:"text,omitempty"`
		DraftUUID *uuid.UUID `json:"draftUuid,omitempty"`
	}
	if err := decodeStrict(c, &request); err != nil {
		return writeBindError(c, err)
	}
	idempotency, ok := idempotencyKey(c)
	if !ok {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Idempotency-Key is required", "Idempotency-Key")
	}
	submission, err := h.service.SubmitPentest(c.Request().Context(), actor, boardID, moduleID, request.Text, request.DraftUUID, idempotency)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusAccepted, toSubmissionModel(submission))
}

func (h *Handler) AdminGetPentestConfig(c *echo.Context) error {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return err
	}
	config, err := h.service.AdminPentestConfig(c.Request().Context(), actor, boardID, moduleID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toPentestConfigModel(config))
}

func (h *Handler) AdminPutPentestConfig(c *echo.Context) error {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return err
	}
	var request struct {
		DeploymentTemplateUUID *uuid.UUID        `json:"deploymentTemplateUuid,omitempty"`
		TemplateVersion        *int              `json:"templateVersion,omitempty"`
		ResetEnabled           *bool             `json:"resetEnabled"`
		MaxResets              *int              `json:"maxResets"`
		Submission             json.RawMessage   `json:"submission,omitempty"`
		Parameters             map[string]string `json:"parameters,omitempty"`
		SecretParameters       map[string]string `json:"secretParameters,omitempty"`
		Nodes                  json.RawMessage   `json:"nodes,omitempty"`
		ProviderIntegration    *uuid.UUID        `json:"providerIntegrationUuid,omitempty"`
		ProxmoxIntegration     *uuid.UUID        `json:"proxmoxIntegrationUuid,omitempty"`
	}
	if err := decodeStrict(c, &request); err != nil {
		return writeBindError(c, err)
	}
	if request.ResetEnabled == nil || request.MaxResets == nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "resetEnabled and maxResets are required", "body")
	}
	if len(strings.TrimSpace(string(request.Nodes))) > 0 && string(request.Nodes) != "[]" && string(request.Nodes) != "null" {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "inline nodes are not supported; bind an approved immutable template version", "nodes")
	}
	if request.ProviderIntegration != nil || request.ProxmoxIntegration != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "provider integrations are selected by the approved binding, not the client", "providerIntegrationUuid")
	}
	config, err := h.service.UpdatePentestConfig(c.Request().Context(), actor, boardID, moduleID, instances.PentestConfigInput{
		DeploymentTemplateID: request.DeploymentTemplateUUID,
		TemplateVersion:      request.TemplateVersion,
		ResetEnabled:         *request.ResetEnabled,
		MaxResets:            *request.MaxResets,
		SubmissionPolicy:     request.Submission,
		Parameters:           request.Parameters,
		SecretParameters:     request.SecretParameters,
	})
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toPentestConfigModel(config))
}

func (h *Handler) AdminListPentestEnvironments(c *echo.Context) error {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return err
	}
	limit, offset, err := pagination(c)
	if err != nil {
		return writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "limit")
	}
	items, total, err := h.service.AdminPentestEnvironments(c.Request().Context(), actor, boardID, moduleID, limit, offset)
	if err != nil {
		return writeServiceError(c, err)
	}
	modelsItems := make([]*models.Instance, 0, len(items))
	for index := range items {
		modelsItems = append(modelsItems, toInstanceModel(&items[index]))
	}
	limit64, offset64, total64 := int64(limit), int64(offset), total
	return c.JSON(http.StatusOK, &models.AdminInstancesResponse{Items: modelsItems, Limit: &limit64, Offset: &offset64, Total: &total64})
}

func (h *Handler) AdminGetPentestEnvironment(c *echo.Context) error {
	actor, boardID, moduleID, environmentID, err := h.boardModuleParamsWithEnvironment(c)
	if err != nil {
		return err
	}
	instance, err := h.service.Get(c.Request().Context(), actor, environmentID)
	if err != nil {
		return writeServiceError(c, err)
	}
	if instance.Scope != instances.ScopePentest || instance.BoardID != boardID || instance.ModuleID != moduleID {
		return writeError(c, http.StatusNotFound, models.ErrorCodeENVIRONMENTNOTFOUND, "Environment not found", "")
	}
	return c.JSON(http.StatusOK, toInstanceModel(instance))
}

func (h *Handler) AdminPentestAction(c *echo.Context) error {
	actor, boardID, moduleID, environmentID, err := h.boardModuleParamsWithEnvironment(c)
	if err != nil {
		return err
	}
	action, err := bindAction(c)
	if err != nil {
		return writeBindError(c, err)
	}
	_ = boardID
	_ = moduleID
	idempotency, ok := idempotencyKey(c)
	if !ok {
		idempotency = uuid.NewString()
	}
	instance, err := h.service.AdminAction(c.Request().Context(), actor, environmentID, instances.Action(action), idempotency)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusAccepted, toInstanceModel(instance))
}

// --- parameter helpers ------------------------------------------------------

func (h *Handler) taskParams(c *echo.Context) (actor struct {
	UserID      uuid.UUID
	GlobalRoles []string
}, boardID, moduleID, taskID uuid.UUID, err error) {
	return firstPrincipalTask(h, c)
}

func firstPrincipalTask(h *Handler, c *echo.Context) (struct {
	UserID      uuid.UUID
	GlobalRoles []string
}, uuid.UUID, uuid.UUID, uuid.UUID, error) {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return struct {
			UserID      uuid.UUID
			GlobalRoles []string
		}{}, uuid.Nil, uuid.Nil, uuid.Nil, writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	actor := struct {
		UserID      uuid.UUID
		GlobalRoles []string
	}{UserID: principal.UserID, GlobalRoles: principal.GlobalRoles}
	boardID, err := pathUUID(c, "boardUuid")
	if err != nil {
		return actor, uuid.Nil, uuid.Nil, uuid.Nil, writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "boardUuid")
	}
	moduleID, err := pathUUID(c, "moduleUuid")
	if err != nil {
		return actor, uuid.Nil, uuid.Nil, uuid.Nil, writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "moduleUuid")
	}
	taskID, err := pathUUID(c, "taskUuid")
	if err != nil {
		return actor, uuid.Nil, uuid.Nil, uuid.Nil, writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "taskUuid")
	}
	return actor, boardID, moduleID, taskID, nil
}

func (h *Handler) boardModuleParams(c *echo.Context) (authorizationPrincipal, uuid.UUID, uuid.UUID, uuid.UUID, error) {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return authorizationPrincipal{}, uuid.Nil, uuid.Nil, uuid.Nil, writeError(c, http.StatusUnauthorized, models.ErrorCodeUNAUTHORIZED, "Authentication required", "")
	}
	boardID, err := pathUUID(c, "boardUuid")
	if err != nil {
		return authorizationPrincipal{}, uuid.Nil, uuid.Nil, uuid.Nil, writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "boardUuid")
	}
	moduleID, err := pathUUID(c, "moduleUuid")
	if err != nil {
		return authorizationPrincipal{}, uuid.Nil, uuid.Nil, uuid.Nil, writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "moduleUuid")
	}
	return authorizationPrincipal{UserID: principal.UserID, GlobalRoles: principal.GlobalRoles}, boardID, moduleID, uuid.Nil, nil
}

func (h *Handler) boardModuleParamsWithEnvironment(c *echo.Context) (authorizationPrincipal, uuid.UUID, uuid.UUID, uuid.UUID, error) {
	actor, boardID, moduleID, _, err := h.boardModuleParams(c)
	if err != nil {
		return actor, uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	environmentID, err := pathUUID(c, "environmentUuid")
	if err != nil {
		return actor, uuid.Nil, uuid.Nil, uuid.Nil, writeError(c, 422, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "environmentUuid")
	}
	return actor, boardID, moduleID, environmentID, nil
}

type authorizationPrincipal = struct {
	UserID      uuid.UUID
	GlobalRoles []string
}

func bindAction(c *echo.Context) (string, error) {
	var request struct {
		Action string `json:"action"`
	}
	if err := decodeStrict(c, &request); err != nil {
		return "", err
	}
	action := strings.ToUpper(strings.TrimSpace(request.Action))
	switch action {
	case "RESET", "DESTROY", "EXTEND":
		return action, nil
	default:
		return "", errors.New("action must be RESET, DESTROY or EXTEND")
	}
}

func idempotencyKey(c *echo.Context) (string, bool) {
	value := strings.TrimSpace(c.Request().Header.Get("Idempotency-Key"))
	if len(value) < 16 || len(value) > 128 {
		return "", false
	}
	return value, true
}

func pagination(c *echo.Context) (int, int, error) {
	limit, offset := 20, 0
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

func optionalFilters(c *echo.Context) (*uuid.UUID, *uuid.UUID, error) {
	var boardID *uuid.UUID
	var moduleID *uuid.UUID
	if raw := c.QueryParam("boardUuid"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return nil, nil, err
		}
		boardID = &parsed
	}
	if raw := c.QueryParam("moduleUuid"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return nil, nil, err
		}
		moduleID = &parsed
	}
	return boardID, moduleID, nil
}

func pathUUID(c *echo.Context, name string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(c.Param(name))
	if err != nil || parsed == uuid.Nil {
		return uuid.Nil, errors.New("invalid uuid")
	}
	return parsed, nil
}

func decodeStrict(c *echo.Context, dst any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request().Body, maxBodyBytes))
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

func toInstanceModel(instance *instances.Instance) *models.Instance {
	boardUUID := models.UUID(instance.BoardID.String())
	moduleUUID := models.UUID(instance.ModuleID.String())
	id := models.UUID(instance.UUID.String())
	status := models.InstanceStatus(instance.Status)
	scope := string(instance.Scope)
	subjectType := models.SubjectType(instance.SubjectType)
	subjectUUID := models.UUID(instance.SubjectID.String())
	subjectName := instance.SubjectName
	if subjectName == "" {
		subjectName = instance.SubjectID.String()
	}
	created := strfmt.DateTime(instance.CreatedAt)
	model := &models.Instance{
		UUID: &id, BoardUUID: &boardUUID, ModuleUUID: &moduleUUID,
		Owner:  &models.ScoreSubject{Type: &subjectType, UUID: &subjectUUID, Name: &subjectName},
		Status: &status, Scope: &scope, CreatedAt: &created,
		Nodes: make([]*models.InstanceNode, 0, len(instance.Nodes)),
	}
	remaining := instance.RemainingSeconds(time.Now())
	model.RemainingSeconds = &remaining
	extensionCount := instance.ExtensionCount
	model.ExtensionCount = &extensionCount
	if instance.StartedAt != nil {
		started := strfmt.DateTime(*instance.StartedAt)
		model.StartedAt = &started
	}
	if instance.ExpiresAt != nil {
		expires := strfmt.DateTime(*instance.ExpiresAt)
		model.ExpiresAt = &expires
	}
	scopeUUID := models.UUID(instance.ScopeUUID.String())
	model.ScopeUUID.UUID = scopeUUID
	for index := range instance.Nodes {
		node := &instance.Nodes[index]
		nodeID := models.UUID(node.ID.String())
		nodeKey := node.Key
		nodeName := node.Name
		kind := models.InstanceNodeKind(node.Kind)
		nodeStatus := node.Status
		nodeModel := &models.InstanceNode{UUID: &nodeID, Key: &nodeKey, Name: &nodeName, Kind: &kind, Status: &nodeStatus, Endpoints: []*models.AccessEndpoint{}}
		for _, endpoint := range node.Endpoints {
			nodeModel.Endpoints = append(nodeModel.Endpoints, toEndpointModel(endpoint))
		}
		model.Nodes = append(model.Nodes, nodeModel)
	}
	return model
}

func toEndpointModel(endpoint instances.Endpoint) *models.AccessEndpoint {
	key := endpoint.Key
	host := endpoint.Host
	port := int64(endpoint.Port)
	exposure := models.EndpointExposure(endpoint.Exposure)
	typ := models.EndpointType(strings.ToUpper(endpoint.Type))
	switch typ {
	case models.EndpointTypeTCP, models.EndpointTypeUDP, models.EndpointTypeHTTP, models.EndpointTypeHTTPS, models.EndpointTypeSSH, models.EndpointTypeRDP, models.EndpointTypeVNC, models.EndpointTypeCUSTOM:
	default:
		typ = models.EndpointTypeCUSTOM
	}
	model := &models.AccessEndpoint{Key: &key, Host: &host, Port: &port, Type: &typ, Exposure: &exposure}
	if endpoint.URL != nil {
		uri := strfmt.URI(*endpoint.URL)
		model.URL = &uri
	}
	return model
}

func toSubmissionModel(submission *instances.PentestSubmission) *models.Submission {
	id := models.UUID(submission.UUID.String())
	status := models.SubmissionStatus(submission.Status)
	created := strfmt.DateTime(submission.CreatedAt)
	model := &models.Submission{UUID: &id, Status: &status, CreatedAt: &created, Files: []*models.FileRef{}}
	for _, file := range submission.Files {
		fileID := models.UUID(file.UUID.String())
		filename := file.Filename
		contentType := file.ContentType
		size := file.Size
		sha := file.SHA256
		model.Files = append(model.Files, &models.FileRef{
			UUID: &fileID, Filename: &filename, ContentType: &contentType, Size: &size, Sha256: &sha,
		})
	}
	model.Text = submission.Text
	model.Message = submission.Message
	if submission.ScoreAwarded != nil {
		score := int64(*submission.ScoreAwarded)
		model.ScoreAwarded = &score
	}
	if submission.ReviewedAt != nil {
		reviewed := strfmt.DateTime(*submission.ReviewedAt)
		model.ReviewedAt = &reviewed
	}
	return model
}

func toPentestConfigModel(config *instances.PentestConfig) *models.PentestConfig {
	maxResets := int64(config.MaxResets)
	resetEnabled := config.ResetEnabled
	model := &models.PentestConfig{
		MaxResets: &maxResets, ResetEnabled: &resetEnabled,
		Submission: submissionPolicyModel(config.SubmissionPolicy),
		Nodes:      []*models.InstanceNodeConfig{},
	}
	if config.DeploymentTemplateID != nil {
		value := models.UUID(config.DeploymentTemplateID.String())
		model.DeploymentTemplateUUID = &value
	}
	if config.TemplateVersion != nil {
		value := int64(*config.TemplateVersion)
		model.TemplateVersion = &value
	}
	if len(config.Parameters) > 0 {
		model.Parameters = config.Parameters
	}
	return model
}

func submissionPolicyModel(raw json.RawMessage) *models.SubmissionPolicy {
	policy := &models.SubmissionPolicy{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, policy)
	}
	mode := models.SubmissionModeNONE
	if policy.Mode != nil {
		mode = *policy.Mode
	}
	checker := models.SubmissionCheckerMANUAL
	if policy.Checker != nil {
		checker = *policy.Checker
	}
	policy.Mode = &mode
	policy.Checker = &checker
	return policy
}

var _ = deployment.KindVM
