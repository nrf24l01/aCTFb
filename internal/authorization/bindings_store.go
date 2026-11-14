package authorization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *RoleService) queryBindings(
	ctx context.Context,
	boardID uuid.UUID,
	limit, offset int,
	paginate bool,
) ([]RoleBinding, int, error) {
	base := `
		SELECT b.id, b.board_id, b.module_id, b.user_id, b.role, b.granted_by,
		       b.expires_at, b.revoked_at, b.created_at,
		       u.username, g.username
		FROM board_role_bindings b
		JOIN users u ON u.id = b.user_id
		JOIN users g ON g.id = b.granted_by
		WHERE b.board_id = $1
		ORDER BY b.created_at DESC, b.id
	`
	args := []any{boardID}
	if paginate {
		base += ` LIMIT $2 OFFSET $3`
		args = append(args, limit, offset)
	}

	rows, err := s.db.Pool.Query(ctx, base, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("authorization: list role bindings: %w", err)
	}
	defer rows.Close()

	bindings := make([]RoleBinding, 0)
	for rows.Next() {
		binding, err := scanBinding(rows)
		if err != nil {
			return nil, 0, err
		}
		bindings = append(bindings, *binding)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("authorization: iterate role bindings: %w", err)
	}

	total := len(bindings)
	if paginate {
		if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM board_role_bindings WHERE board_id = $1`, boardID).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("authorization: count role bindings: %w", err)
		}
	}
	return bindings, total, nil
}

const bindingSelect = `
	SELECT b.id, b.board_id, b.module_id, b.user_id, b.role, b.granted_by,
	       b.expires_at, b.revoked_at, b.created_at,
	       u.username, g.username
	FROM board_role_bindings b
	JOIN users u ON u.id = b.user_id
	JOIN users g ON g.id = b.granted_by
	WHERE b.id = $1 AND b.board_id = $2
`

func loadBinding(ctx context.Context, q rowsQuerier, boardID, bindingID uuid.UUID) (*RoleBinding, error) {
	return loadBindingQuery(ctx, q, bindingSelect, boardID, bindingID)
}

// loadBindingForUpdate locks only the binding row (FOR UPDATE OF b), not the
// joined users rows. Role mutation callers already hold the actor/target user
// row locks in the fixed transaction order.
func loadBindingForUpdate(ctx context.Context, q rowsQuerier, boardID, bindingID uuid.UUID) (*RoleBinding, error) {
	return loadBindingQuery(ctx, q, bindingSelect+" FOR UPDATE OF b", boardID, bindingID)
}

func loadBindingQuery(
	ctx context.Context,
	q rowsQuerier,
	query string,
	boardID, bindingID uuid.UUID,
) (*RoleBinding, error) {
	row := q.QueryRow(ctx, query, bindingID, boardID)
	binding, err := scanBinding(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBindingNotFound
	}
	return binding, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanBinding(row scanner) (*RoleBinding, error) {
	var binding RoleBinding
	var rawRole string
	var moduleID pgtype.UUID
	var expires, revoked pgtype.Timestamptz
	if err := row.Scan(
		&binding.ID,
		&binding.BoardID,
		&moduleID,
		&binding.UserID,
		&rawRole,
		&binding.GrantedBy,
		&expires,
		&revoked,
		&binding.CreatedAt,
		&binding.UserUsername,
		&binding.GrantedByUsername,
	); err != nil {
		return nil, err
	}
	role, ok := ParseRole(rawRole)
	if !ok {
		return nil, ErrInvalidRole
	}
	binding.Role = role
	if moduleID.Valid {
		id := uuid.UUID(moduleID.Bytes)
		binding.ModuleID = &id
	}
	if expires.Valid {
		value := expires.Time
		binding.ExpiresAt = &value
	}
	if revoked.Valid {
		value := revoked.Time
		binding.RevokedAt = &value
	}
	return &binding, nil
}

func writeAudit(
	ctx context.Context,
	tx pgx.Tx,
	actorID, boardID uuid.UUID,
	moduleID *uuid.UUID,
	action, resourceType string,
	resourceID *uuid.UUID,
	details map[string]any,
) error {
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("authorization: encode audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, module_id, action, resource_type, resource_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, actorID, boardID, moduleID, action, resourceType, resourceID, payload); err != nil {
		return fmt.Errorf("authorization: write audit event: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func moduleIDString(moduleID *uuid.UUID) any {
	if moduleID == nil {
		return nil
	}
	return moduleID.String()
}

func clampPage(limit, offset int) (int, int) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
