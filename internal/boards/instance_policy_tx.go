package boards

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// moduleInstancePolicyLockKey is the advisory-lock key that serializes every
// mutation of a module's instance policy and of any task lifetime override in
// that module. A module update and a concurrent task edit therefore cannot
// produce a combination that neither request validated on its own.
func moduleInstancePolicyLockKey(moduleID uuid.UUID) string {
	return "actfb:module-instance-policy:v1:" + moduleID.String()
}

// lockModuleInstancePolicy takes the shared lock inside the caller's transaction.
func lockModuleInstancePolicy(ctx context.Context, tx pgx.Tx, moduleID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, moduleInstancePolicyLockKey(moduleID))
	return err
}

// moduleInstanceSettingsTx reads the current module policy inside a transaction.
func moduleInstanceSettingsTx(ctx context.Context, tx pgx.Tx, boardID, moduleID uuid.UUID) (*TaskInstanceSettings, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `
		SELECT task_instance_settings FROM modules WHERE board_id = $1 AND id = $2
	`, boardID, moduleID).Scan(&raw); err != nil {
		return nil, err
	}
	return decodeTaskInstanceSettings(raw)
}

// validateTaskLifetimeTx is the authoritative per-task check. It runs inside the
// task mutation transaction after the module lock, so it sees the committed
// module policy and cannot be fooled by a concurrent module update.
func validateTaskLifetimeTx(ctx context.Context, tx pgx.Tx, boardID, moduleID uuid.UUID, overrides TaskLifetimeOverrides, enabled bool) error {
	settings, err := moduleInstanceSettingsTx(ctx, tx, boardID, moduleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrModuleNotFound
		}
		return err
	}
	return validateTaskLifetimeOverrides(overrides, settings, enabled)
}

// taskLifetimeRow is one instance-enabled task whose overrides must remain valid
// after a module policy change.
type taskLifetimeRow struct {
	ID        uuid.UUID
	Title     string
	Enabled   bool
	Overrides TaskLifetimeOverrides
}

// validateModuleLifetimeTx re-checks every instance-enabled task in the module
// against the new policy inside the module update transaction. A reduction that
// would strand a task is rejected with the offending task named, instead of
// leaving a task that fails at start.
func validateModuleLifetimeTx(ctx context.Context, tx pgx.Tx, boardID, moduleID uuid.UUID, settings *TaskInstanceSettings) error {
	rows, err := tx.Query(ctx, `
		SELECT id, title, instance_enabled,
		       instance_ttl_seconds, instance_max_ttl_seconds,
		       instance_extension_enabled, instance_extension_seconds, instance_extension_max_count
		FROM tasks
		WHERE board_id = $1 AND module_id = $2 AND instance_enabled
		ORDER BY created_at, id
	`, boardID, moduleID)
	if err != nil {
		return err
	}
	defer rows.Close()
	tasks := make([]taskLifetimeRow, 0)
	for rows.Next() {
		row := taskLifetimeRow{}
		var initial, maxTTL, extensionSeconds, extensionMaxCount *int
		var extensionEnabled *bool
		if err := rows.Scan(&row.ID, &row.Title, &row.Enabled,
			&initial, &maxTTL, &extensionEnabled, &extensionSeconds, &extensionMaxCount); err != nil {
			return err
		}
		row.Overrides = TaskLifetimeOverrides{
			InitialTTLSeconds: initial, MaxTTLSeconds: maxTTL,
			ExtensionEnabled: extensionEnabled, ExtensionSeconds: extensionSeconds,
			ExtensionMaxCount: extensionMaxCount,
		}
		tasks = append(tasks, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, task := range tasks {
		if err := validateTaskLifetimeOverrides(task.Overrides, settings, task.Enabled); err != nil {
			return annotateLifetimeConflict(err, task)
		}
	}
	return nil
}

// annotateLifetimeConflict keeps the field-specific reason and names the task
// that would break, so an operator can act on the 422 directly.
func annotateLifetimeConflict(err error, task taskLifetimeRow) error {
	invalid, ok := err.(*ValidationError)
	if !ok || len(invalid.Fields) == 0 {
		return err
	}
	field := invalid.Fields[0]
	return Invalid(field.Field,
		fmt.Sprintf("%s (task %q)", field.Issue, task.Title), field.Code)
}
