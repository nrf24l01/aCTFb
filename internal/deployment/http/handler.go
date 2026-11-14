// Package http maps the 11 documented /api/v2/admin/deployment-templates
// operations onto the deployment template service. It never defines a parallel
// envelope, never accepts provider credentials and never returns package file
// contents from published-version routes.
package http

import (
	"context"
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

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

const maxRequestBodyBytes = 24 << 20

// TemplateService is the deployment service surface used by the HTTP adapter.
// *deployment.Service implements it.
type TemplateService interface {
	CreateTemplate(ctx context.Context, actor authorization.Principal, input deployment.CreateInput) (*deployment.TemplateRecord, error)
	ListTemplates(ctx context.Context, actor authorization.Principal, limit, offset int) ([]deployment.TemplateRecord, int64, error)
	GetTemplate(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*deployment.TemplateRecord, error)
	GetDraft(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*deployment.Draft, error)
	SaveDraft(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, manifest []byte, files []deployment.FileInput) (*deployment.Draft, error)
	ValidateDraft(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*deployment.ValidationResult, error)
	PreviewDraft(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*deployment.Preview, *deployment.ValidationResult, error)
	Publish(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*deployment.TemplateVersion, error)
	ListVersions(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, limit, offset int) ([]deployment.TemplateVersion, int64, error)
	GetVersion(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, version int) (*deployment.TemplateVersion, error)
	DiffVersions(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, fromVersion, toVersion int) (*deployment.Diff, error)
}

// Handler is the deployment template HTTP adapter.
type Handler struct {
	service TemplateService
	auth    httpapi.AccessTokenAuthenticator
}

// NewHandler creates the adapter with the shared access-token authenticator.
func NewHandler(service TemplateService, auth httpapi.AccessTokenAuthenticator) *Handler {
	return &Handler{service: service, auth: auth}
}

// RegisterRoutes installs exactly the 11 documented operations. Boot wiring is
// owned by the main agent.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	if e == nil || h.service == nil {
		return
	}
	requireUser := httpapi.RequireUser(h.auth)
	e.GET("/api/v2/admin/deployment-templates", h.ListTemplates, requireUser)
	e.POST("/api/v2/admin/deployment-templates", h.CreateTemplate, requireUser)
	e.GET("/api/v2/admin/deployment-templates/:templateUuid", h.GetTemplate, requireUser)
	e.GET("/api/v2/admin/deployment-templates/:templateUuid/draft", h.GetDraft, requireUser)
	e.PUT("/api/v2/admin/deployment-templates/:templateUuid/draft", h.PutDraft, requireUser)
	e.POST("/api/v2/admin/deployment-templates/:templateUuid/draft/validate", h.ValidateDraft, requireUser)
	e.POST("/api/v2/admin/deployment-templates/:templateUuid/draft/preview", h.PreviewDraft, requireUser)
	e.POST("/api/v2/admin/deployment-templates/:templateUuid/publish", h.Publish, requireUser)
	e.GET("/api/v2/admin/deployment-templates/:templateUuid/versions", h.ListVersions, requireUser)
	e.GET("/api/v2/admin/deployment-templates/:templateUuid/versions/:version", h.GetVersion, requireUser)
	e.GET("/api/v2/admin/deployment-templates/:templateUuid/versions/:version/diff", h.DiffVersion, requireUser)
}

func (h *Handler) ListTemplates(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	limit, offset, err := pagination(c)
	if err != nil {
		return writeValidation(c, "limit", err.Error(), "INVALID_VALUE")
	}
	items, total, err := h.service.ListTemplates(c.Request().Context(), actor, limit, offset)
	if err != nil {
		return writeServiceError(c, err)
	}
	models := make([]*oapimodels.DeploymentTemplate, 0, len(items))
	for _, item := range items {
		models = append(models, toTemplateModel(item))
	}
	limit64, offset64, total64 := int64(limit), int64(offset), total
	return c.JSON(http.StatusOK, &oapimodels.DeploymentTemplateListResponse{
		Items: models, Limit: &limit64, Offset: &offset64, Total: &total64,
	})
}

func (h *Handler) CreateTemplate(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	input := deployment.CreateInput{}
	request, present, err := bindOptionalDraftRequest(c)
	if err != nil {
		return writeBindError(c, err)
	}
	if present {
		input.ManifestYAML = []byte(*request.ManifestYaml)
		input.Files = fileInputs(request.Files)
	}
	record, err := h.service.CreateTemplate(c.Request().Context(), actor, input)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, toTemplateModel(*record))
}

func (h *Handler) GetTemplate(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeValidation(c, "templateUuid", err.Error(), "INVALID_FORMAT")
	}
	record, err := h.service.GetTemplate(c.Request().Context(), actor, templateID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toTemplateModel(*record))
}

func (h *Handler) GetDraft(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeValidation(c, "templateUuid", err.Error(), "INVALID_FORMAT")
	}
	draft, err := h.service.GetDraft(c.Request().Context(), actor, templateID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toDraftModel(*draft))
}

func (h *Handler) PutDraft(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeValidation(c, "templateUuid", err.Error(), "INVALID_FORMAT")
	}
	request, present, err := bindOptionalDraftRequest(c)
	if err != nil {
		return writeBindError(c, err)
	}
	if !present || request.ManifestYaml == nil {
		return writeProblem(c, http.StatusBadRequest, oapimodels.ErrorCodeINVALIDJSON, "Malformed JSON")
	}
	draft, err := h.service.SaveDraft(c.Request().Context(), actor, templateID, []byte(*request.ManifestYaml), fileInputs(request.Files))
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toDraftModel(*draft))
}

func (h *Handler) ValidateDraft(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeValidation(c, "templateUuid", err.Error(), "INVALID_FORMAT")
	}
	result, err := h.service.ValidateDraft(c.Request().Context(), actor, templateID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toValidationModel(*result))
}

func (h *Handler) PreviewDraft(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeValidation(c, "templateUuid", err.Error(), "INVALID_FORMAT")
	}
	preview, result, err := h.service.PreviewDraft(c.Request().Context(), actor, templateID)
	if err != nil {
		return writeServiceError(c, err)
	}
	if result != nil && result.HasErrors() {
		return writeValidationFindings(c, result.Findings)
	}
	if preview == nil {
		return writeProblem(c, http.StatusUnprocessableEntity, oapimodels.ErrorCodeVALIDATIONFAILED, "Validation failed")
	}
	return c.JSON(http.StatusOK, toPreviewModel(*preview))
}

func (h *Handler) Publish(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeValidation(c, "templateUuid", err.Error(), "INVALID_FORMAT")
	}
	var request oapimodels.DeploymentTemplatePublishRequest
	if err := bindRequired(c, &request); err != nil {
		return writeBindError(c, err)
	}
	version, err := h.service.Publish(c.Request().Context(), actor, templateID)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusCreated, toVersionModel(*version))
}

func (h *Handler) ListVersions(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeValidation(c, "templateUuid", err.Error(), "INVALID_FORMAT")
	}
	limit, offset, err := pagination(c)
	if err != nil {
		return writeValidation(c, "limit", err.Error(), "INVALID_VALUE")
	}
	items, total, err := h.service.ListVersions(c.Request().Context(), actor, templateID, limit, offset)
	if err != nil {
		return writeServiceError(c, err)
	}
	models := make([]*oapimodels.DeploymentTemplateVersion, 0, len(items))
	for _, item := range items {
		models = append(models, toVersionModel(item))
	}
	limit64, offset64, total64 := int64(limit), int64(offset), total
	return c.JSON(http.StatusOK, &oapimodels.DeploymentTemplateVersionListResponse{
		Items: models, Limit: &limit64, Offset: &offset64, Total: &total64,
	})
}

func (h *Handler) GetVersion(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeValidation(c, "templateUuid", err.Error(), "INVALID_FORMAT")
	}
	version, err := pathVersion(c)
	if err != nil {
		return writeValidation(c, "version", err.Error(), "INVALID_VALUE")
	}
	record, err := h.service.GetVersion(c.Request().Context(), actor, templateID, version)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toVersionModel(*record))
}

func (h *Handler) DiffVersion(c *echo.Context) error {
	actor, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	}
	templateID, err := pathUUID(c, "templateUuid")
	if err != nil {
		return writeValidation(c, "templateUuid", err.Error(), "INVALID_FORMAT")
	}
	toVersion, err := pathVersion(c)
	if err != nil {
		return writeValidation(c, "version", err.Error(), "INVALID_VALUE")
	}
	fromVersion := 0
	if raw := strings.TrimSpace(c.QueryParam("from")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return writeValidation(c, "from", "must be a positive version", "INVALID_VALUE")
		}
		fromVersion = parsed
	}
	diff, err := h.service.DiffVersions(c.Request().Context(), actor, templateID, fromVersion, toVersion)
	if err != nil {
		return writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toDiffModel(*diff))
}

func pathUUID(c *echo.Context, name string) (uuid.UUID, error) {
	value, err := uuid.Parse(c.Param(name))
	if err != nil || value == uuid.Nil {
		return uuid.Nil, errors.New("must be a UUID")
	}
	return value, nil
}

func pathVersion(c *echo.Context) (int, error) {
	value, err := strconv.Atoi(c.Param("version"))
	if err != nil || value < 1 {
		return 0, errors.New("must be a positive integer")
	}
	return value, nil
}

func pagination(c *echo.Context) (int, int, error) {
	limit := 50
	offset := 0
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, errors.New("must be between 1 and 100")
		}
		limit = parsed
	}
	if raw := strings.TrimSpace(c.QueryParam("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("must be zero or greater")
		}
		offset = parsed
	}
	return limit, offset, nil
}

func fileInputs(files []*oapimodels.DeploymentTemplateFile) []deployment.FileInput {
	result := make([]deployment.FileInput, 0, len(files))
	for _, file := range files {
		if file == nil || file.Path == nil || file.ContentBase64 == nil {
			continue
		}
		result = append(result, deployment.FileInput{Path: *file.Path, ContentBase64: *file.ContentBase64})
	}
	return result
}

type bindError struct {
	field   string
	message string
	code    string
}

func (e *bindError) Error() string { return e.message }

func bindOptionalDraftRequest(c *echo.Context) (*oapimodels.DeploymentTemplateDraftRequest, bool, error) {
	body, err := readBody(c)
	if err != nil {
		return nil, false, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, false, nil
	}
	var request oapimodels.DeploymentTemplateDraftRequest
	if err := decodeStrict(body, &request); err != nil {
		return nil, false, err
	}
	if err := request.Validate(strfmt.Default); err != nil {
		return nil, false, &bindError{field: "body", message: err.Error(), code: "INVALID_VALUE"}
	}
	return &request, true, nil
}

func bindRequired(c *echo.Context, target any) error {
	body, err := readBody(c)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return &bindError{field: "body", message: "request body is required", code: "INVALID_JSON"}
	}
	if err := decodeStrict(body, target); err != nil {
		return err
	}
	if validatable, ok := target.(interface{ Validate(strfmt.Registry) error }); ok {
		if err := validatable.Validate(strfmt.Default); err != nil {
			return &bindError{field: "body", message: err.Error(), code: "INVALID_VALUE"}
		}
	}
	return nil
}

func readBody(c *echo.Context) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, maxRequestBodyBytes+1))
	if err != nil {
		return nil, &bindError{field: "body", message: "malformed JSON body", code: "INVALID_JSON"}
	}
	if len(body) > maxRequestBodyBytes {
		return nil, &bindError{field: "body", message: "request body is too large", code: "PAYLOAD_TOO_LARGE"}
	}
	return body, nil
}

func decodeStrict(body []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		const unknownPrefix = `json: unknown field "`
		if strings.HasPrefix(err.Error(), unknownPrefix) {
			field := strings.TrimSuffix(strings.TrimPrefix(err.Error(), unknownPrefix), `"`)
			return &bindError{field: "body." + field, message: "unknown field", code: "UNKNOWN_FIELD"}
		}
		return &bindError{field: "body", message: "malformed JSON body", code: "INVALID_JSON"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return &bindError{field: "body", message: "malformed JSON body", code: "INVALID_JSON"}
	}
	return nil
}

type problem struct {
	status  int
	code    oapimodels.ErrorCode
	message string
	field   string
	issue   string
}

func (p problem) write(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	trace := traceID(c)
	path := c.Request().URL.Path
	timestamp := strfmt.DateTime(time.Now().UTC())
	message := p.message
	if message == "" {
		message = http.StatusText(p.status)
	}
	code := p.code
	if p.field != "" {
		field := p.field
		issue := p.issue
		if issue == "" {
			issue = "Invalid value"
		}
		return c.JSON(p.status, &oapimodels.ValidationError{
			Code: &code,
			FieldErrors: []*oapimodels.FieldError{{
				Field: &field, Issue: &issue, Code: p.issue,
			}},
			Message: &message, Path: &path, Timestamp: &timestamp, TraceID: &trace,
		})
	}
	return c.JSON(p.status, &oapimodels.APIError{
		Code: &code, Message: &message, Path: &path, Timestamp: &timestamp, TraceID: &trace,
	})
}

func writeProblem(c *echo.Context, status int, code oapimodels.ErrorCode, message string) error {
	return problem{status: status, code: code, message: message}.write(c)
}

func writeValidation(c *echo.Context, field, issue, code string) error {
	return problem{
		status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
		message: "Validation failed", field: field, issue: issue + " (" + code + ")",
	}.write(c)
}

func writeValidationFindings(c *echo.Context, findings []deployment.Finding) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	trace := traceID(c)
	path := c.Request().URL.Path
	timestamp := strfmt.DateTime(time.Now().UTC())
	code := oapimodels.ErrorCodeVALIDATIONFAILED
	message := "Validation failed"
	fields := make([]*oapimodels.FieldError, 0, len(findings))
	for index, finding := range findings {
		if index >= 100 {
			break
		}
		if finding.Severity != deployment.SeverityError {
			continue
		}
		field := finding.Path
		issue := deploymentFindingMessage(finding)
		findingCode := finding.Code
		fields = append(fields, &oapimodels.FieldError{Field: &field, Issue: &issue, Code: findingCode})
	}
	if len(fields) == 0 {
		field := "manifest"
		issue := "Validation failed"
		fields = append(fields, &oapimodels.FieldError{Field: &field, Issue: &issue, Code: "INVALID_VALUE"})
	}
	return c.JSON(http.StatusUnprocessableEntity, &oapimodels.ValidationError{
		Code: &code, FieldErrors: fields, Message: &message, Path: &path, Timestamp: &timestamp, TraceID: &trace,
	})
}

func deploymentFindingMessage(finding deployment.Finding) string {
	const max = 500
	if len(finding.Message) <= max {
		return finding.Message
	}
	return finding.Message[:max-3] + "..."
}

func writeBindError(c *echo.Context, err error) error {
	var binding *bindError
	if errors.As(err, &binding) {
		status := http.StatusBadRequest
		code := oapimodels.ErrorCodeINVALIDJSON
		if binding.code == "PAYLOAD_TOO_LARGE" {
			status = http.StatusRequestEntityTooLarge
			code = oapimodels.ErrorCodePAYLOADTOOLARGE
		}
		if binding.field != "body" {
			return problem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
				message: "Validation failed", field: binding.field, issue: binding.message}.write(c)
		}
		if binding.code == "INVALID_VALUE" {
			return problem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVALIDATIONFAILED,
				message: "Validation failed", field: "body", issue: binding.message}.write(c)
		}
		return writeProblem(c, status, code, "Malformed JSON")
	}
	return writeProblem(c, http.StatusBadRequest, oapimodels.ErrorCodeINVALIDJSON, "Malformed JSON")
}

func writeServiceError(c *echo.Context, err error) error {
	if validation, ok := deployment.AsValidationError(err); ok {
		return writeValidationFindings(c, validation.Findings)
	}
	switch {
	case errors.Is(err, deployment.ErrUnauthorized):
		return writeProblem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required")
	case errors.Is(err, deployment.ErrForbidden), errors.Is(err, deployment.ErrNoAuthorizer):
		return writeProblem(c, http.StatusForbidden, oapimodels.ErrorCodeFORBIDDEN, "Access denied")
	case errors.Is(err, deployment.ErrNotFound):
		return writeProblem(c, http.StatusNotFound, oapimodels.ErrorCodeINSTANCETEMPLATENOTFOUND, "Deployment template not found")
	case errors.Is(err, deployment.ErrTemplateExists):
		return writeProblem(c, http.StatusConflict, oapimodels.ErrorCodeINSTANCETEMPLATEINVALID, "Deployment template already exists")
	case errors.Is(err, deployment.ErrVersionExists):
		return writeProblem(c, http.StatusConflict, oapimodels.ErrorCodeINSTANCETEMPLATEINVALID, "Deployment template version already exists")
	case errors.Is(err, deployment.ErrNoDatabase):
		return writeProblem(c, http.StatusInternalServerError, oapimodels.ErrorCodeINTERNALERROR, "Internal error")
	default:
		return writeProblem(c, http.StatusInternalServerError, oapimodels.ErrorCodeINTERNALERROR, "Internal error")
	}
}

func traceID(c *echo.Context) oapimodels.UUID {
	if value, ok := c.Get("trace_id").(string); ok {
		if _, err := uuid.Parse(value); err == nil {
			return oapimodels.UUID(value)
		}
	}
	generated := uuid.NewString()
	c.Set("trace_id", generated)
	return oapimodels.UUID(generated)
}

func toTemplateModel(record deployment.TemplateRecord) *oapimodels.DeploymentTemplate {
	uuidValue := oapimodels.UUID(record.UUID.String())
	slug := oapimodels.Slug(record.Slug)
	created := strfmt.DateTime(record.CreatedAt.UTC())
	updated := strfmt.DateTime(record.UpdatedAt.UTC())
	return &oapimodels.DeploymentTemplate{
		UUID: &uuidValue, Slug: &slug, Name: &record.Name, Description: &record.Description,
		CreatedAt: &created, UpdatedAt: &updated,
	}
}

func toDraftModel(draft deployment.Draft) *oapimodels.DeploymentTemplateDraft {
	templateUUID := oapimodels.UUID(draft.TemplateUUID.String())
	manifest := string(draft.ManifestYAML)
	updated := strfmt.DateTime(draft.UpdatedAt.UTC())
	files := make([]*oapimodels.DeploymentTemplateFile, 0, len(draft.Files))
	for _, input := range draft.FilesInput() {
		path := input.Path
		content := input.ContentBase64
		files = append(files, &oapimodels.DeploymentTemplateFile{Path: &path, ContentBase64: &content})
	}
	return &oapimodels.DeploymentTemplateDraft{
		TemplateUUID: &templateUUID, ManifestYaml: &manifest, Files: files, UpdatedAt: &updated,
	}
}

func toValidationModel(result deployment.ValidationResult) *oapimodels.DeploymentTemplateValidation {
	valid := result.Valid
	findings := make([]*oapimodels.ValidationFinding, 0, len(result.Findings))
	for _, finding := range result.Findings {
		findings = append(findings, toFindingModel(finding))
	}
	return &oapimodels.DeploymentTemplateValidation{Valid: &valid, Findings: findings}
}

func toFindingModel(finding deployment.Finding) *oapimodels.ValidationFinding {
	path := finding.Path
	code := finding.Code
	message := deploymentFindingMessage(finding)
	severity := finding.Severity
	return &oapimodels.ValidationFinding{Path: &path, Code: &code, Message: &message, Severity: &severity}
}

func toPreviewModel(preview deployment.Preview) *oapimodels.DeploymentTemplatePreview {
	nodes := make([]*oapimodels.DeploymentTemplatePreviewNode, 0, len(preview.Nodes))
	for _, node := range preview.Nodes {
		key, name, kind, action := node.Key, node.Name, string(node.Kind), node.Action
		findings := make([]*oapimodels.ValidationFinding, 0, len(node.Findings))
		for _, finding := range node.Findings {
			findings = append(findings, toFindingModel(finding))
		}
		nodes = append(nodes, &oapimodels.DeploymentTemplatePreviewNode{
			Key: &key, Name: &name, Kind: &kind, Action: &action, Findings: findings,
		})
	}
	endpoints := make([]*oapimodels.DeploymentTemplatePreviewEndpoint, 0, len(preview.Endpoints))
	for _, endpoint := range preview.Endpoints {
		key, nodeKey, protocol := endpoint.Key, endpoint.NodeKey, endpoint.Protocol
		exposure := oapimodels.EndpointExposure(endpoint.Exposure)
		endpoints = append(endpoints, &oapimodels.DeploymentTemplatePreviewEndpoint{
			Key: &key, NodeKey: &nodeKey, Protocol: &protocol, Exposure: &exposure,
		})
	}
	warnings := append([]string(nil), preview.Warnings...)
	if warnings == nil {
		warnings = []string{}
	}
	return &oapimodels.DeploymentTemplatePreview{Nodes: nodes, Endpoints: endpoints, Warnings: warnings}
}

func toVersionModel(version deployment.TemplateVersion) *oapimodels.DeploymentTemplateVersion {
	uuidValue := oapimodels.UUID(version.UUID.String())
	templateUUID := oapimodels.UUID(version.TemplateID.String())
	number := int64(version.Version)
	digest := version.Digest
	publishedAt := strfmt.DateTime(version.PublishedAt.UTC())
	publishedBy := oapimodels.UUID(version.PublishedBy.String())
	return &oapimodels.DeploymentTemplateVersion{
		UUID: &uuidValue, TemplateUUID: &templateUUID, Version: &number, Digest: &digest,
		PublishedAt: &publishedAt, PublishedBy: &publishedBy,
	}
}

func toDiffModel(diff deployment.Diff) *oapimodels.DeploymentTemplateDiff {
	from := int64(diff.FromVersion)
	to := int64(diff.ToVersion)
	changes := make([]*oapimodels.DeploymentTemplateDiffEntry, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		kind, path, value := change.Kind, change.Path, change.Change
		entry := &oapimodels.DeploymentTemplateDiffEntry{Kind: &kind, Path: &path, Change: &value}
		if change.Before != nil {
			before := truncateDiffValue(*change.Before)
			entry.Before = &before
		}
		if change.After != nil {
			after := truncateDiffValue(*change.After)
			entry.After = &after
		}
		changes = append(changes, entry)
	}
	return &oapimodels.DeploymentTemplateDiff{FromVersion: &from, ToVersion: &to, Changes: changes}
}

func truncateDiffValue(value string) string {
	const max = 2000
	if len(value) <= max {
		return value
	}
	return value[:max-3] + "..."
}
