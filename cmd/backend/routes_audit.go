package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/users"
)

// adminAuditHandler exposes the global, read-only audit log. It authorizes
// against live database state via the central authorizer, never trusts JWT
// roles, and never returns raw SQL errors.
type adminAuditHandler struct {
	db    *postgres.DB
	authz authorization.Authorizer
	auth  httpapi.AccessTokenAuthenticator
}

func registerAdminAuditRoute(e *echo.Echo, db *postgres.DB, authz authorization.Authorizer, userService *users.UserService) {
	if e == nil || db == nil || authz == nil || userService == nil {
		return
	}
	handler := &adminAuditHandler{db: db, authz: authz, auth: userService}
	e.GET("/api/v2/admin/audit-log", handler.List, httpapi.RequireUser(userService))
}

func (h *adminAuditHandler) List(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}
	decision, err := h.authz.Authorize(c.Request().Context(), principal,
		authorization.PermissionPlatformAuditRead, authorization.Scope{})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Authorization check failed")
	}
	if !decision.Allowed {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	limit, offset, filter, problem := parseAuditQuery(c)
	if problem != "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, problem)
	}

	var total int64
	countQuery := `SELECT count(*) FROM audit_events e WHERE ` + auditFilterSQL
	if err := h.db.Pool.QueryRow(c.Request().Context(), countQuery, filter...).Scan(&total); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Audit log unavailable")
	}

	rows, err := h.db.Pool.Query(c.Request().Context(), `
		SELECT e.id, e.actor_user_id, u.username, e.action, e.resource_type,
		       e.resource_id, e.details, e.created_at
		FROM audit_events e
		JOIN users u ON u.id = e.actor_user_id
		WHERE `+auditFilterSQL+`
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $7 OFFSET $8
	`, append(filter, limit, offset)...)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Audit log unavailable")
	}
	defer rows.Close()

	items := make([]*models.AuditEvent, 0, limit)
	for rows.Next() {
		var (
			eventID    uuid.UUID
			actorID    uuid.UUID
			actorName  string
			action     string
			resource   string
			resourceID pgtype.UUID
			detailsRaw []byte
			createdAt  time.Time
		)
		if err := rows.Scan(&eventID, &actorID, &actorName, &action, &resource,
			&resourceID, &detailsRaw, &createdAt); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Audit log unavailable")
		}
		items = append(items, auditEventModel(eventID, actorID, actorName, action, resource,
			resourceID, detailsRaw, createdAt))
	}
	if err := rows.Err(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Audit log unavailable")
	}

	limitValue := int64(limit)
	offsetValue := int64(offset)
	return c.JSON(http.StatusOK, &models.AuditEventsResponse{
		Items: items, Limit: &limitValue, Offset: &offsetValue, Total: &total,
	})
}

const auditFilterSQL = `($1::uuid IS NULL OR e.actor_user_id = $1)
  AND ($2::text IS NULL OR e.action = $2)
  AND ($3::text IS NULL OR e.resource_type = $3)
  AND ($4::uuid IS NULL OR e.resource_id = $4)
  AND ($5::timestamptz IS NULL OR e.created_at >= $5)
  AND ($6::timestamptz IS NULL OR e.created_at <= $6)`

func parseAuditQuery(c *echo.Context) (int, int, []any, string) {
	limit := 20
	offset := 0
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, nil, "limit must be between 1 and 100"
		}
		limit = parsed
	}
	if raw := strings.TrimSpace(c.QueryParam("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, nil, "offset must be zero or greater"
		}
		offset = parsed
	}

	var actorID any
	if raw := strings.TrimSpace(c.QueryParam("actorUuid")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return 0, 0, nil, "actorUuid must be a UUID"
		}
		actorID = parsed
	}
	var action any
	if raw := strings.TrimSpace(c.QueryParam("action")); raw != "" {
		if len(raw) > 128 {
			return 0, 0, nil, "action is too long"
		}
		action = raw
	}
	var resourceType any
	if raw := strings.TrimSpace(c.QueryParam("resourceType")); raw != "" {
		if len(raw) > 128 {
			return 0, 0, nil, "resourceType is too long"
		}
		resourceType = raw
	}
	var resourceID any
	if raw := strings.TrimSpace(c.QueryParam("resourceUuid")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return 0, 0, nil, "resourceUuid must be a UUID"
		}
		resourceID = parsed
	}
	var from, to any
	if raw := strings.TrimSpace(c.QueryParam("from")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return 0, 0, nil, "from must be an RFC3339 timestamp"
		}
		from = parsed
	}
	if raw := strings.TrimSpace(c.QueryParam("to")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return 0, 0, nil, "to must be an RFC3339 timestamp"
		}
		to = parsed
	}
	if from != nil && to != nil && from.(time.Time).After(to.(time.Time)) {
		return 0, 0, nil, "from must be before or equal to to"
	}
	return limit, offset, []any{actorID, action, resourceType, resourceID, from, to}, ""
}

func auditEventModel(eventID, actorID uuid.UUID, actorName, action, resource string,
	resourceID pgtype.UUID, detailsRaw []byte, createdAt time.Time,
) *models.AuditEvent {
	eventUUID := models.UUID(eventID.String())
	actorUUID := models.UUID(actorID.String())
	username := models.Username(actorName)
	created := strfmt.DateTime(createdAt.UTC())
	actionValue := action
	resourceValue := resource
	details := auditDetails(detailsRaw)
	event := &models.AuditEvent{
		UUID:         &eventUUID,
		Actor:        &models.UserSummary{UUID: &actorUUID, Username: &username},
		Action:       &actionValue,
		ResourceType: &resourceValue,
		CreatedAt:    &created,
		Details:      details,
	}
	if resourceID.Valid {
		value := strfmt.UUID(uuid.UUID(resourceID.Bytes).String())
		event.ResourceUUID = &value
	}
	return event
}

func auditDetails(raw []byte) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}
	}
	return redactAuditValue(value)
}

func redactAuditValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveAuditKey(key) {
				clean[key] = "[REDACTED]"
				continue
			}
			clean[key] = redactAuditValue(item)
		}
		return clean
	case []any:
		clean := make([]any, 0, len(typed))
		for _, item := range typed {
			clean = append(clean, redactAuditValue(item))
		}
		return clean
	default:
		return value
	}
}

func sensitiveAuditKey(key string) bool {
	lower := strings.ToLower(key)
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "api_key", "apikey",
		"access_key", "secret_key", "authorization", "credential",
		"ciphertext", "private_key", "requesthash", "hash", "dsn",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
