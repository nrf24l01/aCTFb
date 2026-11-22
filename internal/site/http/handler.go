// Package http exposes the public landing projection and the global-admin
// landing editor. It uses the canonical generated models from docs/swagger.yaml.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/site"
)

const maxPatchBody = 192 * 1024

// Handler maps the site service to /api/v2.
type Handler struct {
	service       *site.Service
	authenticator httpapi.AccessTokenAuthenticator
}

// NewHandler constructs the site HTTP adapter. authenticator is only needed for
// the admin routes; public GET /site is intentionally unauthenticated.
func NewHandler(service *site.Service, authenticator httpapi.AccessTokenAuthenticator) *Handler {
	return &Handler{service: service, authenticator: authenticator}
}

// RegisterRoutes installs the public and admin site routes.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	if e == nil {
		return
	}
	e.GET("/api/v2/site", h.GetPublic)
	auth := httpapi.RequireUser(h.authenticator)
	e.GET("/api/v2/admin/site", h.AdminGet, auth)
	e.PATCH("/api/v2/admin/site", h.AdminUpdate, auth)
}

// GetPublic returns only the whitelisted public projection.
func (h *Handler) GetPublic(c *echo.Context) error {
	noStore(c)
	settings, err := h.service.Public(c.Request().Context())
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(http.StatusOK, publicModel(settings))
}

// AdminGet returns the full document to a live global admin.
func (h *Handler) AdminGet(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return h.problem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required or credentials invalid")
	}
	settings, err := h.service.Admin(c.Request().Context(), principal)
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(http.StatusOK, adminModel(settings))
}

// AdminUpdate validates presence, then updates and audits transactionally.
func (h *Handler) AdminUpdate(c *echo.Context) error {
	noStore(c)
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return h.problem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required or credentials invalid")
	}
	in, problem := bindUpdate(c)
	if problem != nil {
		return problem.write(c)
	}
	settings, err := h.service.Update(c.Request().Context(), principal, in)
	if err != nil {
		return h.writeError(c, err)
	}
	return c.JSON(http.StatusOK, adminModel(settings))
}

func bindUpdate(c *echo.Context) (site.UpdateInput, *apiProblem) {
	var input site.UpdateInput
	contentType := c.Request().Header.Get(echo.HeaderContentType)
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), echo.MIMEApplicationJSON) {
		return input, problemPtr(http.StatusUnsupportedMediaType, oapimodels.ErrorCodeUNSUPPORTEDMEDIATYPE, "Content-Type must be application/json")
	}
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, maxPatchBody+1))
	if err != nil {
		return input, problemPtr(http.StatusBadRequest, oapimodels.ErrorCodeINVALIDJSON, "Could not read request body")
	}
	if len(body) > maxPatchBody {
		return input, problemPtr(http.StatusRequestEntityTooLarge, oapimodels.ErrorCodePAYLOADTOOLARGE, "Request body too large")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return input, problemPtr(http.StatusBadRequest, oapimodels.ErrorCodeINVALIDJSON, "Request body must be a JSON object")
	}
	if len(raw) == 0 {
		return input, validationProblem("body", "At least one field is required", "REQUIRED")
	}
	for key, value := range raw {
		switch key {
		case "title":
			var title string
			if err := json.Unmarshal(value, &title); err != nil {
				return input, validationProblem("title", "title must be a string", "INVALID_TYPE")
			}
			if strings.TrimSpace(title) == "" {
				return input, validationProblem("title", "title is required", "REQUIRED")
			}
			input.Title = &title
		case "introMarkdown":
			var markdown string
			if err := json.Unmarshal(value, &markdown); err != nil {
				return input, validationProblem("introMarkdown", "introMarkdown must be a string", "INVALID_TYPE")
			}
			input.IntroMarkdown = &markdown
		case "published":
			var published bool
			if err := json.Unmarshal(value, &published); err != nil {
				return input, validationProblem("published", "published must be a boolean", "INVALID_TYPE")
			}
			input.Published = &published
		default:
			return input, validationProblem(key, "unknown field", "UNKNOWN_FIELD")
		}
	}
	return input, nil
}

func (h *Handler) writeError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, site.ErrUnauthorized):
		return h.problem(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required or credentials invalid")
	case errors.Is(err, site.ErrForbidden):
		return h.problem(c, http.StatusForbidden, oapimodels.ErrorCodeFORBIDDEN, "Access denied")
	case errors.Is(err, site.ErrInvalidInput):
		return validationProblem("body", "Invalid landing content", "INVALID_VALUE").write(c)
	case errors.Is(err, site.ErrNoDatabase):
		return h.problem(c, http.StatusServiceUnavailable, oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE, "Service unavailable")
	default:
		return h.problem(c, http.StatusInternalServerError, oapimodels.ErrorCodeINTERNALERROR, "Internal server error")
	}
}

func (h *Handler) problem(c *echo.Context, status int, code oapimodels.ErrorCode, message string) error {
	return apiProblem{status: status, code: code, message: message}.write(c)
}

type apiProblem struct {
	status      int
	code        oapimodels.ErrorCode
	message     string
	fieldErrors []*oapimodels.FieldError
}

func problemPtr(status int, code oapimodels.ErrorCode, message string) *apiProblem {
	return &apiProblem{status: status, code: code, message: message}
}

func validationProblem(field, issue, code string) *apiProblem {
	return &apiProblem{
		status:      http.StatusUnprocessableEntity,
		code:        oapimodels.ErrorCodeVALIDATIONFAILED,
		message:     "Validation failed",
		fieldErrors: []*oapimodels.FieldError{{Field: &field, Issue: &issue, Code: code}},
	}
}

func (p apiProblem) write(c *echo.Context) error {
	noStore(c)
	path := c.Request().URL.Path
	if path == "" {
		path = "/"
	}
	trace := traceID(c)
	timestamp := strfmt.DateTime(time.Now().UTC())
	message := p.message
	if message == "" {
		message = http.StatusText(p.status)
	}
	code := p.code
	if len(p.fieldErrors) > 0 {
		return c.JSON(p.status, &oapimodels.ValidationError{
			Code:        &code,
			FieldErrors: p.fieldErrors,
			Message:     &message,
			Path:        &path,
			Timestamp:   &timestamp,
			TraceID:     &trace,
		})
	}
	return c.JSON(p.status, &oapimodels.APIError{
		Code:      &code,
		Message:   &message,
		Path:      &path,
		Timestamp: &timestamp,
		TraceID:   &trace,
	})
}

func publicModel(settings *site.PublicSettings) *oapimodels.SitePublic {
	title := settings.Title
	markdown := settings.IntroMarkdown
	published := settings.Published
	updated := strfmt.DateTime(settings.UpdatedAt.UTC())
	return &oapimodels.SitePublic{
		Title:         &title,
		IntroMarkdown: &markdown,
		Published:     &published,
		UpdatedAt:     &updated,
	}
}

func adminModel(settings *site.AdminSettings) *oapimodels.AdminSite {
	model := &oapimodels.AdminSite{
		Title:         ptr(settings.Title),
		IntroMarkdown: ptr(settings.IntroMarkdown),
		Published:     &settings.Published,
	}
	updated := strfmt.DateTime(settings.UpdatedAt.UTC())
	model.UpdatedAt = &updated
	if settings.UpdatedByUserID != nil && *settings.UpdatedByUserID != uuid.Nil {
		updatedBy := strfmt.UUID(settings.UpdatedByUserID.String())
		model.UpdatedByUserUUID = &updatedBy
	}
	return model
}

func ptr(value string) *string { return &value }

func noStore(c *echo.Context) {
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
}

func traceID(c *echo.Context) oapimodels.UUID {
	if value, ok := c.Get(httpapi.TraceIDKey).(string); ok && value != "" {
		return oapimodels.UUID(value)
	}
	return oapimodels.UUID(uuid.NewString())
}
