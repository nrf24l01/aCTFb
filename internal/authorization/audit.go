package authorization

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// AuditEvent is the projected, secret-redacted audit row returned by the
// board audit endpoint.
type AuditEvent struct {
	ID            uuid.UUID
	ActorUserID   uuid.UUID
	ActorUsername string
	BoardID       *uuid.UUID
	ModuleID      *uuid.UUID
	Action        string
	ResourceType  string
	ResourceID    *uuid.UUID
	Details       json.RawMessage
	CreatedAt     time.Time
}

// RoleService owns scoped role binding CRUD and its audit writes. Every

func (s *RoleService) ListAudit(
	ctx context.Context,
	actor Principal,
	boardID uuid.UUID,
	limit, offset int,
) ([]AuditEvent, int, error) {
	if _, _, err := s.requirePermission(ctx, actor, PermissionBoardAuditRead, boardID); err != nil {
		return nil, 0, err
	}
	limit, offset = clampPage(limit, offset)

	rows, err := s.db.Pool.Query(ctx, `
		SELECT e.id, e.actor_user_id, u.username, e.board_id, e.module_id,
		       e.action, e.resource_type, e.resource_id, e.details, e.created_at
		FROM audit_events e
		JOIN users u ON u.id = e.actor_user_id
		WHERE e.board_id = $1
		ORDER BY e.created_at DESC, e.id
		LIMIT $2 OFFSET $3
	`, boardID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("authorization: list audit events: %w", err)
	}
	defer rows.Close()

	events := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		var boardUUID, moduleUUID, resourceUUID pgtype.UUID
		var actorID uuid.UUID
		var rawDetails []byte
		if err := rows.Scan(
			&event.ID,
			&actorID,
			&event.ActorUsername,
			&boardUUID,
			&moduleUUID,
			&event.Action,
			&event.ResourceType,
			&resourceUUID,
			&rawDetails,
			&event.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("authorization: scan audit event: %w", err)
		}
		event.ActorUserID = actorID
		if boardUUID.Valid {
			id := uuid.UUID(boardUUID.Bytes)
			event.BoardID = &id
		}
		if moduleUUID.Valid {
			id := uuid.UUID(moduleUUID.Bytes)
			event.ModuleID = &id
		}
		if resourceUUID.Valid {
			id := uuid.UUID(resourceUUID.Bytes)
			event.ResourceID = &id
		}
		event.Details = sanitizeDetails(rawDetails)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("authorization: iterate audit events: %w", err)
	}

	var total int
	if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE board_id = $1`, boardID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("authorization: count audit events: %w", err)
	}
	return events, total, nil
}

var sensitiveAuditKeyParts = []string{
	"password", "secret", "token", "answer", "flag", "hash",
	"credential", "apikey", "api_key", "private_key", "access_key",
}

func sanitizeDetails(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return json.RawMessage(`{}`)
	}
	redacted := redactValue(value)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			if isSensitiveAuditKey(key) {
				result[key] = "[redacted]"
				continue
			}
			result[key] = redactValue(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = redactValue(child)
		}
		return result
	default:
		return value
	}
}

func isSensitiveAuditKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	for _, part := range sensitiveAuditKeyParts {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}
