package boards

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const moduleColumns = `m.id, m.board_id, m.name, m.description, m.type, COALESCE(m.extension_key, ''),
	m.allowed_task_types, COALESCE((SELECT string_agg(ma.audience_id::text, ',' ORDER BY ma.audience_id::text) FROM module_audiences ma WHERE ma.board_id=m.board_id AND ma.module_id=m.id), ''),
	m.enabled, m.sort_order, m.starts_at, m.ends_at, m.created_by, m.created_at, m.updated_at,
	m.task_instance_settings`

func scanModuleRow(row boardScanner) (*Module, error) {
	var module Module
	var audienceCSV string
	var instanceSettingsRaw []byte
	if err := row.Scan(
		&module.ID,
		&module.BoardID,
		&module.Name,
		&module.Description,
		&module.Type,
		&module.ExtensionKey,
		&module.AllowedTaskTypes,
		&audienceCSV,
		&module.Enabled,
		&module.SortOrder,
		&module.StartsAt,
		&module.EndsAt,
		&module.CreatedBy,
		&module.CreatedAt,
		&module.UpdatedAt,
		&instanceSettingsRaw,
	); err != nil {
		return nil, err
	}
	settings, err := decodeTaskInstanceSettings(instanceSettingsRaw)
	if err != nil {
		return nil, fmt.Errorf("decode module instance settings: %w", err)
	}
	module.TaskInstanceSettings = settings
	for _, raw := range strings.Split(audienceCSV, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("parse module audience uuid: %w", err)
		}
		module.AudienceUUIDs = append(module.AudienceUUIDs, id)
	}
	return &module, nil
}

func (s *PostgresStore) ListModules(ctx context.Context, boardID uuid.UUID, filter ModuleFilter) ([]Module, int, error) {
	p, err := s.pool()
	if err != nil {
		return nil, 0, err
	}

	where := []string{"m.board_id = $1"}
	args := []any{boardID}
	if filter.Type != "" {
		args = append(args, filter.Type)
		where = append(where, fmt.Sprintf("m.type = $%d", len(args)))
	}
	if filter.Query != "" {
		args = append(args, filter.Query)
		where = append(where, fmt.Sprintf("m.name ILIKE '%%' || $%d || '%%'", len(args)))
	}
	if !filter.Admin {
		where = append(where, "m.enabled = TRUE")
	}
	if filter.Subject != nil {
		args = append(args, filter.Subject.Type, filter.Subject.ID)
		where = append(where, fmt.Sprintf(`(NOT EXISTS (SELECT 1 FROM module_audiences ma WHERE ma.board_id=m.board_id AND ma.module_id=m.id)
			OR EXISTS (SELECT 1 FROM module_audiences ma JOIN audience_members am ON am.audience_id=ma.audience_id
				WHERE ma.board_id=m.board_id AND ma.module_id=m.id AND am.subject_type=$%d AND am.subject_id=$%d))`,
			len(args)-1, len(args)))
	}
	if len(filter.ModuleIDs) > 0 {
		// Multiple assigned modules are an OR set, not an impossible AND of all
		// module IDs. Keep sibling modules out without producing an empty list.
		parts := make([]string, 0, len(filter.ModuleIDs))
		for _, moduleID := range filter.ModuleIDs {
			args = append(args, moduleID)
			parts = append(parts, fmt.Sprintf("m.id = $%d", len(args)))
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	baseWhere := strings.Join(where, " AND ")

	var total int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM modules m WHERE `+baseWhere, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list modules count: %w", err)
	}
	if total == 0 {
		return []Module{}, 0, nil
	}

	listArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)
	rows, err := p.pool.Query(ctx, `SELECT `+moduleColumns+` FROM modules m WHERE `+baseWhere+
		` ORDER BY m.sort_order ASC, m.id ASC LIMIT $`+fmt.Sprint(len(args)+1)+` OFFSET $`+fmt.Sprint(len(args)+2),
		listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list modules: %w", err)
	}
	defer rows.Close()
	items := make([]Module, 0, filter.Limit)
	for rows.Next() {
		module, err := scanModuleRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan module: %w", err)
		}
		items = append(items, *module)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate modules: %w", err)
	}
	return items, total, nil
}

func (s *PostgresStore) GetModule(ctx context.Context, boardID, moduleID uuid.UUID) (*Module, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	module, err := scanModuleRow(p.pool.QueryRow(ctx,
		`SELECT `+moduleColumns+` FROM modules m WHERE m.board_id=$1 AND m.id=$2`, boardID, moduleID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrModuleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get module: %w", err)
	}
	return module, nil
}

func (s *PostgresStore) CreateModule(ctx context.Context, in CreateModuleParams, audit AuditEventParams) (*Module, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	audiences := in.AudienceUUIDs
	if audiences == nil {
		audiences = []uuid.UUID{}
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create module: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	module, err := scanModuleRow(tx.QueryRow(ctx, `
		INSERT INTO modules AS m (board_id, name, description, type, extension_key, allowed_task_types,
			audience_uuids, enabled, sort_order, starts_at, ends_at, created_by, task_instance_settings)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING `+moduleColumns,
		in.BoardID, in.Name, in.Description, in.Type, emptyToNil(in.ExtensionKey), in.AllowedTaskTypes,
		audiences, in.Enabled, in.SortOrder, in.StartsAt, in.EndsAt, in.CreatedBy,
		instanceSettingsJSON(in.InstanceSettings),
	))
	if isUniqueViolation(err) {
		return nil, ErrModuleNameAlreadyExists
	}
	if err != nil {
		return nil, fmt.Errorf("create module: %w", err)
	}
	for _, audienceID := range audiences {
		if _, err := tx.Exec(ctx, `INSERT INTO module_audiences (board_id, module_id, audience_id) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, module.BoardID, module.ID, audienceID); err != nil {
			return nil, fmt.Errorf("insert module audience: %w", err)
		}
	}
	audit.BoardID = &module.BoardID
	audit.ModuleID = &module.ID
	audit.ResourceID = &module.ID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create module: %w", err)
	}
	return module, nil
}

func (s *PostgresStore) UpdateModule(ctx context.Context, boardID, moduleID uuid.UUID, in UpdateModuleParams, audit AuditEventParams) (*Module, error) {
	p, err := s.pool()
	if err != nil {
		return nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin update module: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	audiences := in.AudienceUUIDs
	if audiences == nil {
		audiences = []uuid.UUID{}
	}
	module, err := scanModuleRow(tx.QueryRow(ctx, `
		UPDATE modules AS m SET name=$3, description=$4, extension_key=$5, allowed_task_types=$6,
			audience_uuids=$7, enabled=$8, sort_order=$9, starts_at=$10, ends_at=$11,
			task_instance_settings=$12, updated_at=NOW()
		WHERE board_id=$1 AND id=$2
		RETURNING `+moduleColumns,
		boardID, moduleID, in.Name, in.Description, emptyToNil(in.ExtensionKey), in.AllowedTaskTypes, audiences,
		in.Enabled, in.SortOrder, in.StartsAt, in.EndsAt,
		instanceSettingsJSON(in.InstanceSettings),
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrModuleNotFound
	}
	if isUniqueViolation(err) {
		return nil, ErrModuleNameAlreadyExists
	}
	if err != nil {
		return nil, fmt.Errorf("update module: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM module_audiences WHERE board_id=$1 AND module_id=$2`, boardID, moduleID); err != nil {
		return nil, fmt.Errorf("clear module audiences: %w", err)
	}
	for _, audienceID := range in.AudienceUUIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO module_audiences (board_id, module_id, audience_id) VALUES ($1,$2,$3)`, boardID, moduleID, audienceID); err != nil {
			return nil, fmt.Errorf("insert module audience: %w", err)
		}
	}
	audit.BoardID = &boardID
	audit.ModuleID = &moduleID
	audit.ResourceID = &moduleID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update module: %w", err)
	}
	return module, nil
}

func (s *PostgresStore) DeleteModule(ctx context.Context, boardID, moduleID uuid.UUID, audit AuditEventParams) error {
	p, err := s.pool()
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete module: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	audit.BoardID = &boardID
	audit.ModuleID = &moduleID
	audit.ResourceID = &moduleID
	if err := writeAuditTx(ctx, tx, audit); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM modules WHERE board_id=$1 AND id=$2`, boardID, moduleID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrModuleDeleteNotAllowed
		}
		return fmt.Errorf("delete module: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrModuleNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete module: %w", err)
	}
	return nil
}

func (s *PostgresStore) ModuleHasTasksUsingType(ctx context.Context, moduleID uuid.UUID, taskType string) (bool, error) {
	p, err := s.pool()
	if err != nil {
		return false, err
	}
	var exists bool
	if err := p.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE module_id=$1 AND type=$2)`,
		moduleID, taskType).Scan(&exists); err != nil {
		return false, fmt.Errorf("check module task type usage: %w", err)
	}
	return exists, nil
}
