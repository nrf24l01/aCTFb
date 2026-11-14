package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
)

const accessGroupAdvisoryLockKey = "p1-access-groups"

func (s *AccessService) ListAccessGroups(ctx context.Context, principal authorization.Principal, q ListQuery) (GroupPage, error) {
	if err := s.ready(); err != nil {
		return GroupPage{}, err
	}
	if _, err := s.requireGlobalAdmin(ctx, s.db.Pool, principal, false); err != nil {
		return GroupPage{}, err
	}
	limit, offset := clampPage(q.Limit, q.Offset)
	query := strings.TrimSpace(q.Query)
	rows, err := s.db.Pool.Query(ctx, `
		SELECT g.id, g.name, count(agm.user_id)::int
		FROM access_groups g
		LEFT JOIN access_group_members agm ON agm.group_id = g.id
		WHERE ($1 = '' OR g.name ILIKE '%' || $1 || '%')
		GROUP BY g.id, g.name
		ORDER BY g.name, g.id
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return GroupPage{}, fmt.Errorf("access: list access groups: %w", err)
	}
	defer rows.Close()
	page := GroupPage{Items: make([]Group, 0)}
	for rows.Next() {
		var group Group
		if err := rows.Scan(&group.ID, &group.Name, &group.UsersCount); err != nil {
			return GroupPage{}, fmt.Errorf("access: scan access group: %w", err)
		}
		page.Items = append(page.Items, group)
	}
	if err := rows.Err(); err != nil {
		return GroupPage{}, fmt.Errorf("access: iterate access groups: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM access_groups
		WHERE ($1 = '' OR name ILIKE '%' || $1 || '%')
	`, query).Scan(&page.Total); err != nil {
		return GroupPage{}, fmt.Errorf("access: count access groups: %w", err)
	}
	return page, nil
}

func (s *AccessService) CreateAccessGroup(ctx context.Context, principal authorization.Principal, name string) (*Group, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return nil, ErrInvalidInput
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: begin access-group create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, accessGroupAdvisoryLockKey); err != nil {
		return nil, fmt.Errorf("access: lock access groups: %w", err)
	}
	actor, err := s.requireGlobalAdmin(ctx, tx, principal, true)
	if err != nil {
		return nil, err
	}
	var group Group
	err = tx.QueryRow(ctx, `
		INSERT INTO access_groups (name) VALUES ($1)
		RETURNING id, name
	`, name).Scan(&group.ID, &group.Name)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAccessGroupExists
		}
		return nil, fmt.Errorf("access: create access group: %w", err)
	}
	if err := writeAccessAudit(ctx, tx, actor.ID, nil, nil, "access_group.create", "access_group", &group.ID, map[string]any{"name": group.Name}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("access: commit access-group create: %w", err)
	}
	return &group, nil
}

func (s *AccessService) GetAccessGroup(ctx context.Context, principal authorization.Principal, id uuid.UUID) (*Group, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := s.requireGlobalAdmin(ctx, s.db.Pool, principal, false); err != nil {
		return nil, err
	}
	return s.getAccessGroup(ctx, s.db.Pool, id)
}

func (s *AccessService) UpdateAccessGroup(ctx context.Context, principal authorization.Principal, id uuid.UUID, name string) (*Group, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return nil, ErrInvalidInput
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("access: begin access-group update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, accessGroupAdvisoryLockKey); err != nil {
		return nil, fmt.Errorf("access: lock access groups: %w", err)
	}
	actor, err := s.requireGlobalAdmin(ctx, tx, principal, true)
	if err != nil {
		return nil, err
	}
	var group Group
	err = tx.QueryRow(ctx, `
		UPDATE access_groups SET name = $1 WHERE id = $2
		RETURNING id, name
	`, name, id).Scan(&group.ID, &group.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccessGroupNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAccessGroupExists
		}
		return nil, fmt.Errorf("access: update access group: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM access_group_members WHERE group_id = $1`, id).Scan(&group.UsersCount); err != nil {
		return nil, fmt.Errorf("access: count access-group members: %w", err)
	}
	if err := writeAccessAudit(ctx, tx, actor.ID, nil, nil, "access_group.update", "access_group", &group.ID, map[string]any{"name": group.Name}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("access: commit access-group update: %w", err)
	}
	return &group, nil
}

func (s *AccessService) DeleteAccessGroup(ctx context.Context, principal authorization.Principal, id uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("access: begin access-group delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, accessGroupAdvisoryLockKey); err != nil {
		return fmt.Errorf("access: lock access groups: %w", err)
	}
	actor, err := s.requireGlobalAdmin(ctx, tx, principal, true)
	if err != nil {
		return err
	}
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM access_groups WHERE id = $1 FOR UPDATE`, id).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAccessGroupNotFound
		}
		return fmt.Errorf("access: lock access group: %w", err)
	}
	var used int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM board_access_groups WHERE group_id = $1`, id).Scan(&used); err != nil {
		return fmt.Errorf("access: check access-group use: %w", err)
	}
	if used > 0 {
		return ErrAccessGroupInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM access_groups WHERE id = $1`, id); err != nil {
		return fmt.Errorf("access: delete access group: %w", err)
	}
	if err := writeAccessAudit(ctx, tx, actor.ID, nil, nil, "access_group.delete", "access_group", &id, map[string]any{"name": name}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("access: commit access-group delete: %w", err)
	}
	return nil
}

func (s *AccessService) getAccessGroup(ctx context.Context, q rowQuerier, id uuid.UUID) (*Group, error) {
	var group Group
	err := q.QueryRow(ctx, `
		SELECT g.id, g.name, count(agm.user_id)::int
		FROM access_groups g
		LEFT JOIN access_group_members agm ON agm.group_id = g.id
		WHERE g.id = $1
		GROUP BY g.id, g.name
	`, id).Scan(&group.ID, &group.Name, &group.UsersCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccessGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("access: load access group: %w", err)
	}
	return &group, nil
}

func writeAccessAudit(
	ctx context.Context,
	tx pgx.Tx,
	actorID uuid.UUID,
	boardID, moduleID *uuid.UUID,
	action, resourceType string,
	resourceID *uuid.UUID,
	details map[string]any,
) error {
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("access: encode audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, module_id, action, resource_type, resource_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, actorID, boardID, moduleID, action, resourceType, resourceID, payload); err != nil {
		return fmt.Errorf("access: write audit event: %w", err)
	}
	return nil
}

var _ = time.Time{}
