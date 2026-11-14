package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nrf24l01/actfb/internal/authorization"
)

const (
	auditActionPluginValidate = "plugin.validate"
	auditActionPluginInstall  = "plugin.install"
	auditActionPluginEnable   = "plugin.enable"
	auditActionPluginDisable  = "plugin.disable"
	auditActionPluginUpgrade  = "plugin.upgrade"
	auditActionPluginRemove   = "plugin.remove"
	auditActionGrantCreate    = "plugin.permission_grant.create"
	auditActionGrantRevoke    = "plugin.permission_grant.revoke"
	auditActionEventReplay    = "plugin.event.replay"
	auditResourcePlugin       = "plugin"
)

// writeAudit records a global plugin administration action. Details never
// contain tokens, DSNs, package contents or plugin credentials.
func writeAudit(
	ctx context.Context,
	querier interface {
		Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	},
	actor authorization.Principal,
	action, pluginID string,
	details map[string]any,
) error {
	if querier == nil {
		return errors.New("plugins: audit database is not configured")
	}
	if actor.UserID == uuid.Nil {
		return errors.New("plugins: audit actor is required")
	}
	if details == nil {
		details = map[string]any{}
	}
	details["pluginId"] = pluginID
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("plugins: encode audit details: %w", err)
	}
	if _, err := querier.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, action, resource_type, details)
		VALUES ($1, $2, $3, $4)
	`, actor.UserID, action, auditResourcePlugin, payload); err != nil {
		return fmt.Errorf("plugins: write audit event: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}
