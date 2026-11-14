package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/attackdefense"
	"github.com/nrf24l01/actfb/internal/authorization"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

// Handler is the AD HTTP adapter.
type Handler struct {
	service *attackdefense.Service
	auth    httpapi.AccessTokenAuthenticator
}

// NewHandler constructs the adapter with the shared access-token authenticator.
func NewHandler(service *attackdefense.Service, auth httpapi.AccessTokenAuthenticator) *Handler {
	return &Handler{service: service, auth: auth}
}

// RegisterRoutes installs exactly the documented 22 operations. Boot owns the
// call site; this package adds no router or feature flag edit.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	if e == nil || h.service == nil {
		return
	}
	requireUser := httpapi.RequireUser(h.auth)

	base := "/api/v2/boards/:boardUuid/modules/:moduleUuid/attack-defense"
	e.GET(base+"/repositories", h.ParticipantRepositories, requireUser)
	e.GET(base+"/progress", h.ParticipantProgress, requireUser)
	e.GET(base+"/runs", h.ParticipantListRuns, requireUser)
	e.POST(base+"/runs", h.ParticipantCreateRun, requireUser)
	e.GET(base+"/runs/:runUuid", h.ParticipantGetRun, requireUser)
	e.GET(base+"/runs/:runUuid/logs", h.ParticipantRunLogs, requireUser)

	admin := "/api/v2/admin/boards/:boardUuid/modules/:moduleUuid/attack-defense"
	e.GET(admin+"/config", h.AdminGetConfig, requireUser)
	e.PUT(admin+"/config", h.AdminPutConfig, requireUser)
	e.GET(admin+"/vulnerabilities", h.AdminListVulnerabilities, requireUser)
	e.POST(admin+"/vulnerabilities", h.AdminCreateVulnerability, requireUser)
	e.GET(admin+"/vulnerabilities/:vulnerabilityUuid", h.AdminGetVulnerability, requireUser)
	e.PATCH(admin+"/vulnerabilities/:vulnerabilityUuid", h.AdminUpdateVulnerability, requireUser)
	e.DELETE(admin+"/vulnerabilities/:vulnerabilityUuid", h.AdminDeleteVulnerability, requireUser)
	e.GET(admin+"/repositories", h.AdminListRepositories, requireUser)
	e.POST(admin+"/repositories", h.AdminCreateRepository, requireUser)
	e.GET(admin+"/repositories/:repositoryBindingUuid", h.AdminGetRepository, requireUser)
	e.PATCH(admin+"/repositories/:repositoryBindingUuid", h.AdminUpdateRepository, requireUser)
	e.DELETE(admin+"/repositories/:repositoryBindingUuid", h.AdminDeleteRepository, requireUser)
	e.GET(admin+"/runs", h.AdminListRuns, requireUser)
	e.GET(admin+"/runs/:runUuid", h.AdminGetRun, requireUser)
	e.POST(admin+"/runs/:runUuid/actions", h.AdminRunAction, requireUser)

	e.POST("/api/v2/webhooks/git/:repositoryBindingUuid", h.GitWebhook)
}

// ParticipantRepositories returns token-free source/exploit repository info.
func (h *Handler) ParticipantRepositories(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	repositories, err := h.service.GetRepositories(c.Request().Context(), actor, boardID, moduleID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toRepositoriesModel(repositories))
}

// ParticipantProgress returns only discovered vulnerabilities.
func (h *Handler) ParticipantProgress(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	progress, err := h.service.GetProgress(c.Request().Context(), actor, boardID, moduleID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toProgressModel(progress))
}

// ParticipantListRuns lists only the caller's subject runs.
func (h *Handler) ParticipantListRuns(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	filter, ok := runFilter(c)
	if !ok {
		return nil
	}
	runs, total, err := h.service.ListMyRuns(c.Request().Context(), actor, boardID, moduleID, filter)
	if err != nil {
		return writeServiceError(c, err)
	}
	items := make([]*oapimodels.ADRunSummary, 0, len(runs))
	for _, run := range runs {
		items = append(items, toRunSummaryModel(run))
	}
	limit, offset, totalValue := int64(filter.Limit), int64(filter.Offset), int64(total)
	return c.JSON(http.StatusOK, &oapimodels.ADRunListResponse{
		Items: items, Limit: &limit, Offset: &offset, Total: &totalValue,
	})
}

// ParticipantCreateRun enqueues a manual EXPLOIT/PATCH run.
func (h *Handler) ParticipantCreateRun(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	var request oapimodels.ADRunCreateRequest
	if !h.bindJSON(c, &request) {
		return nil
	}
	if request.Kind == nil {
		return writeValidation(c, "kind", "required", "INVALID_VALUE")
	}
	run, err := h.service.CreateMyRun(c.Request().Context(), actor, boardID, moduleID,
		string(*request.Kind), request.CommitSha, c.Request().Header.Get("Idempotency-Key"))
	if err != nil {
		return writeServiceError(c, err)
	}
	status := run.Status
	return c.JSON(http.StatusAccepted, &oapimodels.ADRunAccepted{RunUUID: modelUUID(run.ID), Status: &status})
}

// ParticipantGetRun returns one own run with hidden definitions filtered out.
func (h *Handler) ParticipantGetRun(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	runID, ok := uuidParam(c, "runUuid")
	if !ok {
		return nil
	}
	run, err := h.service.GetMyRun(c.Request().Context(), actor, boardID, moduleID, runID)
	if err != nil {
		return writeServiceError(c, err)
	}
	progress, progressErr := h.service.GetProgress(c.Request().Context(), actor, boardID, moduleID)
	visible := map[uuid.UUID]bool{}
	if progressErr == nil {
		for _, item := range progress.Vulnerabilities {
			visible[item.ID] = true
		}
	}
	return c.JSON(http.StatusOK, toRunModel(*run, false, visible))
}

// ParticipantRunLogs returns sanitized logs for an own run.
func (h *Handler) ParticipantRunLogs(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	runID, ok := uuidParam(c, "runUuid")
	if !ok {
		return nil
	}
	logs, truncated, err := h.service.GetMyRunLogs(c.Request().Context(), actor, boardID, moduleID, runID)
	if err != nil {
		return writeServiceError(c, err)
	}
	lines := make([]string, 0, len(logs))
	for _, entry := range logs {
		lines = append(lines, entry.Line)
	}
	return c.JSON(http.StatusOK, &oapimodels.RunLogsResponse{Lines: lines, Truncated: truncated})
}

// AdminGetConfig returns the stored config or documented defaults.
func (h *Handler) AdminGetConfig(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	config, err := h.service.AdminGetConfig(c.Request().Context(), actor, boardID, moduleID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toConfigModel(config))
}

// AdminPutConfig replaces the config.
func (h *Handler) AdminPutConfig(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	var request oapimodels.ADConfig
	if !h.bindJSON(c, &request) {
		return nil
	}
	config := attackdefense.Config{
		DefaultSourceBranch:         derefString(request.DefaultSourceBranch),
		DefaultExploitBranch:        derefString(request.DefaultExploitBranch),
		RunnerImage:                 derefString(request.RunnerImage),
		RunTimeoutSeconds:           int(derefInt64(request.RunTimeoutSeconds)),
		MaxConcurrentRunsPerSubject: int(derefInt64(request.MaxConcurrentRunsPerSubject)),
		ManualRerunEnabled:          request.ManualRerunEnabled != nil && *request.ManualRerunEnabled,
	}
	stored, err := h.service.AdminPutConfig(c.Request().Context(), actor, boardID, moduleID, config)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toConfigModel(stored))
}

// AdminListVulnerabilities lists hidden definitions.
func (h *Handler) AdminListVulnerabilities(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	limit, offset, ok := pagination(c)
	if !ok {
		return nil
	}
	items, total, err := h.service.AdminListVulnerabilities(c.Request().Context(), actor, boardID, moduleID, limit, offset)
	if err != nil {
		return writeServiceError(c, err)
	}
	models := make([]*oapimodels.ADVulnerabilityAdmin, 0, len(items))
	for index := range items {
		models = append(models, toVulnerabilityModel(&items[index]))
	}
	limit64, offset64, total64 := int64(limit), int64(offset), int64(total)
	return c.JSON(http.StatusOK, &oapimodels.ADVulnerabilitiesResponse{
		Items: models, Limit: &limit64, Offset: &offset64, Total: &total64,
	})
}

// AdminCreateVulnerability creates a hidden definition.
func (h *Handler) AdminCreateVulnerability(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	var request oapimodels.ADVulnerabilityCreateRequest
	if !h.bindJSON(c, &request) {
		return nil
	}
	item := attackdefense.Vulnerability{
		Key: derefString(request.Key), Title: derefString(request.Title),
		Description:         request.Description,
		ExploitPoints:       int(derefInt64(request.ExploitPoints)),
		PatchPoints:         int(derefInt64(request.PatchPoints)),
		ExploitTestSelector: derefString(request.ExploitTestSelector),
		PatchTestSelector:   derefString(request.PatchTestSelector),
	}
	created, err := h.service.AdminCreateVulnerability(c.Request().Context(), actor, boardID, moduleID, item)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, toVulnerabilityModel(created))
}

// AdminGetVulnerability returns one hidden definition.
func (h *Handler) AdminGetVulnerability(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	vulnerabilityID, ok := uuidParam(c, "vulnerabilityUuid")
	if !ok {
		return nil
	}
	item, err := h.service.AdminGetVulnerability(c.Request().Context(), actor, boardID, moduleID, vulnerabilityID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toVulnerabilityModel(item))
}

// AdminUpdateVulnerability applies a partial patch; omitted fields keep their
// current value.
func (h *Handler) AdminUpdateVulnerability(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	vulnerabilityID, ok := uuidParam(c, "vulnerabilityUuid")
	if !ok {
		return nil
	}
	body, err := readJSONBody(c, attackdefense.MaxRequestBodyBytes)
	if err != nil {
		return writeServiceError(c, err)
	}
	var presence map[string]json.RawMessage
	if err := json.Unmarshal(body, &presence); err != nil || len(presence) == 0 {
		return writeProblem(c, http.StatusBadRequest, "INVALID_JSON", "Malformed JSON")
	}
	var request oapimodels.ADVulnerabilityPatchRequest
	if err := decodeStrict(body, &request); err != nil {
		return writeProblem(c, http.StatusBadRequest, "INVALID_JSON", "Malformed JSON")
	}
	if err := request.Validate(strfmt.Default); err != nil {
		return writeValidation(c, "body", "invalid value", "INVALID_FORMAT")
	}
	current, err := h.service.AdminGetVulnerability(c.Request().Context(), actor, boardID, moduleID, vulnerabilityID)
	if err != nil {
		return writeServiceError(c, err)
	}
	merged := *current
	if hasKey(presence, "key") {
		merged.Key = request.Key
	}
	if hasKey(presence, "title") {
		merged.Title = request.Title
	}
	if hasKey(presence, "description") {
		merged.Description = request.Description
	}
	if hasKey(presence, "exploitPoints") {
		merged.ExploitPoints = int(derefInt64(request.ExploitPoints))
	}
	if hasKey(presence, "patchPoints") {
		merged.PatchPoints = int(derefInt64(request.PatchPoints))
	}
	if hasKey(presence, "exploitTestSelector") {
		merged.ExploitTestSelector = request.ExploitTestSelector
	}
	if hasKey(presence, "patchTestSelector") {
		merged.PatchTestSelector = request.PatchTestSelector
	}
	updated, err := h.service.AdminUpdateVulnerability(c.Request().Context(), actor, boardID, moduleID, vulnerabilityID, merged)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toVulnerabilityModel(updated))
}

// AdminDeleteVulnerability deletes only unused definitions.
func (h *Handler) AdminDeleteVulnerability(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	vulnerabilityID, ok := uuidParam(c, "vulnerabilityUuid")
	if !ok {
		return nil
	}
	if err := h.service.AdminDeleteVulnerability(c.Request().Context(), actor, boardID, moduleID, vulnerabilityID); err != nil {
		return writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// AdminListRepositories lists assignments.
func (h *Handler) AdminListRepositories(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	limit, offset, ok := pagination(c)
	if !ok {
		return nil
	}
	items, total, err := h.service.AdminListAssignments(c.Request().Context(), actor, boardID, moduleID, limit, offset)
	if err != nil {
		return writeServiceError(c, err)
	}
	models := make([]*oapimodels.ADRepositoryAssignment, 0, len(items))
	for index := range items {
		models = append(models, toAssignmentModel(&items[index]))
	}
	limit64, offset64, total64 := int64(limit), int64(offset), int64(total)
	return c.JSON(http.StatusOK, &oapimodels.ADRepositoryAssignmentsResponse{
		Items: models, Limit: &limit64, Offset: &offset64, Total: &total64,
	})
}

// AdminCreateRepository creates an assignment.
func (h *Handler) AdminCreateRepository(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	var request oapimodels.ADRepositoryAssignmentRequest
	if !h.bindJSON(c, &request) {
		return nil
	}
	input, err := assignmentInput(&request)
	if err != nil {
		return writeValidation(c, "body", "invalid repository assignment", "INVALID_FORMAT")
	}
	created, err := h.service.AdminCreateAssignment(c.Request().Context(), actor, boardID, moduleID, input)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, toAssignmentModel(created))
}

// AdminGetRepository returns an assignment by assignment UUID.
func (h *Handler) AdminGetRepository(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	assignmentID, ok := uuidParam(c, "repositoryBindingUuid")
	if !ok {
		return nil
	}
	item, err := h.service.AdminGetAssignment(c.Request().Context(), actor, boardID, moduleID, assignmentID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toAssignmentModel(item))
}

// AdminUpdateRepository replaces an assignment.
func (h *Handler) AdminUpdateRepository(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	assignmentID, ok := uuidParam(c, "repositoryBindingUuid")
	if !ok {
		return nil
	}
	var request oapimodels.ADRepositoryAssignmentRequest
	if !h.bindJSON(c, &request) {
		return nil
	}
	input, err := assignmentInput(&request)
	if err != nil {
		return writeValidation(c, "body", "invalid repository assignment", "INVALID_FORMAT")
	}
	updated, err := h.service.AdminUpdateAssignment(c.Request().Context(), actor, boardID, moduleID, assignmentID, input)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toAssignmentModel(updated))
}

// AdminDeleteRepository deletes an assignment without run history.
func (h *Handler) AdminDeleteRepository(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	assignmentID, ok := uuidParam(c, "repositoryBindingUuid")
	if !ok {
		return nil
	}
	if err := h.service.AdminDeleteAssignment(c.Request().Context(), actor, boardID, moduleID, assignmentID); err != nil {
		return writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// AdminListRuns lists all module runs.
func (h *Handler) AdminListRuns(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	filter, ok := runFilter(c)
	if !ok {
		return nil
	}
	runs, total, err := h.service.AdminListRuns(c.Request().Context(), actor, boardID, moduleID, filter)
	if err != nil {
		return writeServiceError(c, err)
	}
	items := make([]*oapimodels.ADRunSummary, 0, len(runs))
	for _, run := range runs {
		items = append(items, toRunSummaryModel(run))
	}
	limit, offset, totalValue := int64(filter.Limit), int64(filter.Offset), int64(total)
	return c.JSON(http.StatusOK, &oapimodels.ADRunListResponse{
		Items: items, Limit: &limit, Offset: &offset, Total: &totalValue,
	})
}

// AdminGetRun returns full sanitized test details.
func (h *Handler) AdminGetRun(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	runID, ok := uuidParam(c, "runUuid")
	if !ok {
		return nil
	}
	run, err := h.service.AdminGetRun(c.Request().Context(), actor, boardID, moduleID, runID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toRunModel(*run, true, nil))
}

// AdminRunAction performs CANCEL/RETRY.
func (h *Handler) AdminRunAction(c *echo.Context) error {
	actor, ok := h.actor(c)
	if !ok {
		return nil
	}
	boardID, moduleID, ok := boardAndModule(c)
	if !ok {
		return nil
	}
	runID, ok := uuidParam(c, "runUuid")
	if !ok {
		return nil
	}
	var request oapimodels.ADRunAdminActionRequest
	if !h.bindJSON(c, &request) {
		return nil
	}
	if request.Action == nil {
		return writeValidation(c, "action", "required", "INVALID_VALUE")
	}
	run, err := h.service.AdminRunAction(c.Request().Context(), actor, boardID, moduleID, runID,
		*request.Action, c.Request().Header.Get("Idempotency-Key"))
	if err != nil {
		return writeServiceError(c, err)
	}
	status := run.Status
	return c.JSON(http.StatusAccepted, &oapimodels.ADRunAccepted{RunUUID: modelUUID(run.ID), Status: &status})
}

// GitWebhook authenticates and enqueues a provider push. It never uses a user
// token; the repository binding is the trusted credential.
func (h *Handler) GitWebhook(c *echo.Context) error {
	bindingID, err := uuid.Parse(strings.TrimSpace(c.Param("repositoryBindingUuid")))
	if err != nil {
		return writeProblem(c, http.StatusNotFound, "REPOSITORY_BINDING_NOT_FOUND", "Repository binding not found")
	}
	body, err := readRawBody(c, attackdefense.MaxWebhookBodyBytes)
	if err != nil {
		return writeServiceError(c, err)
	}
	headers := c.Request().Header.Clone()
	_, err = h.service.HandleWebhook(c.Request().Context(), attackdefense.WebhookRequest{
		BindingID: bindingID, Body: body, Headers: headers,
		RemoteAddr: c.Request().RemoteAddr, TLS: c.Request().TLS != nil,
		ContentType: c.Request().Header.Get("Content-Type"),
	})
	if err != nil {
		return writeServiceError(c, err)
	}
	status := "accepted"
	return c.JSON(http.StatusAccepted, &oapimodels.StatusResponse{Status: &status})
}

func (h *Handler) actor(c *echo.Context) (authorization.Principal, bool) {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		_ = writeProblem(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return authorization.Principal{}, false
	}
	return actor, true
}

func (h *Handler) bindJSON(c *echo.Context, target interface{ Validate(strfmt.Registry) error }) bool {
	body, err := readJSONBody(c, attackdefense.MaxRequestBodyBytes)
	if err != nil {
		_ = writeServiceError(c, err)
		return false
	}
	if err := decodeStrict(body, target); err != nil {
		_ = writeProblem(c, http.StatusBadRequest, "INVALID_JSON", "Malformed JSON")
		return false
	}
	if err := target.Validate(strfmt.Default); err != nil {
		_ = writeValidation(c, "body", "invalid value", "INVALID_FORMAT")
		return false
	}
	return true
}

func readJSONBody(c *echo.Context, limit int64) ([]byte, error) {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(c.Request().Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		return nil, attackdefense.ErrUnsupportedMediaType
	}
	return readRawBody(c, limit)
}

func readRawBody(c *echo.Context, limit int64) ([]byte, error) {
	if c.Request().ContentLength > limit {
		return nil, attackdefense.ErrPayloadTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, limit+1))
	if err != nil {
		return nil, attackdefense.ErrInvalidWebhook
	}
	if int64(len(body)) > limit {
		return nil, attackdefense.ErrPayloadTooLarge
	}
	return body, nil
}

func uuidParam(c *echo.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(c.Param(name)))
	if err != nil {
		_ = writeValidation(c, "path."+name, "invalid UUID", "INVALID_FORMAT")
		return uuid.Nil, false
	}
	return id, true
}

func boardAndModule(c *echo.Context) (uuid.UUID, uuid.UUID, bool) {
	boardID, ok := uuidParam(c, "boardUuid")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	moduleID, ok := uuidParam(c, "moduleUuid")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return boardID, moduleID, true
}

func pagination(c *echo.Context) (int, int, bool) {
	limit := 50
	offset := 0
	if raw := c.QueryParam("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			_ = writeValidation(c, "query.limit", "must be 1..200", "INVALID_FORMAT")
			return 0, 0, false
		}
		limit = parsed
	}
	if raw := c.QueryParam("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			_ = writeValidation(c, "query.offset", "must be >= 0", "INVALID_FORMAT")
			return 0, 0, false
		}
		offset = parsed
	}
	return limit, offset, true
}

func runFilter(c *echo.Context) (attackdefense.RunFilter, bool) {
	limit, offset, ok := pagination(c)
	if !ok {
		return attackdefense.RunFilter{}, false
	}
	kind := strings.ToUpper(strings.TrimSpace(c.QueryParam("kind")))
	status := strings.ToUpper(strings.TrimSpace(c.QueryParam("status")))
	if kind != "" && kind != attackdefense.RunKindExploit && kind != attackdefense.RunKindPatch {
		_ = writeValidation(c, "query.kind", "must be EXPLOIT or PATCH", "INVALID_VALUE")
		return attackdefense.RunFilter{}, false
	}
	switch status {
	case "", attackdefense.RunStatusQueued, attackdefense.RunStatusRunning, attackdefense.RunStatusPassed,
		attackdefense.RunStatusFailed, attackdefense.RunStatusError, attackdefense.RunStatusCanceled:
	default:
		_ = writeValidation(c, "query.status", "invalid status", "INVALID_VALUE")
		return attackdefense.RunFilter{}, false
	}
	return attackdefense.RunFilter{Kind: kind, Status: status, Limit: limit, Offset: offset}, true
}

func assignmentInput(request *oapimodels.ADRepositoryAssignmentRequest) (attackdefense.AssignmentInput, error) {
	if request == nil || request.SubjectType == nil || request.SubjectUUID == nil ||
		request.SourceRepository == nil || request.ExploitRepository == nil {
		return attackdefense.AssignmentInput{}, errors.New("missing fields")
	}
	subjectID, err := uuid.Parse(string(*request.SubjectUUID))
	if err != nil {
		return attackdefense.AssignmentInput{}, err
	}
	source, err := repositorySpec(request.SourceRepository)
	if err != nil {
		return attackdefense.AssignmentInput{}, err
	}
	exploit, err := repositorySpec(request.ExploitRepository)
	if err != nil {
		return attackdefense.AssignmentInput{}, err
	}
	return attackdefense.AssignmentInput{
		SubjectType: string(*request.SubjectType), SubjectID: subjectID,
		Source: source, Exploit: exploit,
	}, nil
}

func repositorySpec(model *oapimodels.RepositorySpec) (attackdefense.RepositorySpec, error) {
	if model == nil || model.IntegrationUUID == nil || model.Repository == nil || model.Branch == nil {
		return attackdefense.RepositorySpec{}, errors.New("missing repository fields")
	}
	integrationID, err := uuid.Parse(string(*model.IntegrationUUID))
	if err != nil {
		return attackdefense.RepositorySpec{}, err
	}
	return attackdefense.RepositorySpec{
		IntegrationID: integrationID, Repository: *model.Repository, Branch: *model.Branch,
	}, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func hasKey(values map[string]json.RawMessage, key string) bool {
	_, ok := values[key]
	return ok
}
