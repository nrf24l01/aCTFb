package integrations

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const integrationColumns = `
	id, category, type, COALESCE(kind, ''), COALESCE(driver, ''), name,
	COALESCE(base_url, ''), credentials_ciphertext IS NOT NULL, settings,
	publication_policy, status, created_by, created_at, updated_at
`

type store struct {
	db *postgres.DB
}

func newStore(db *postgres.DB) *store { return &store{db: db} }

type credentialRecord struct {
	Ciphertext []byte
	KeyVersion uint32
}

func scanIntegration(row interface {
	Scan(dest ...any) error
}) (Integration, error) {
	var item Integration
	var category, typ, kind, driver, status string
	var settings []byte
	var publication []byte
	if err := row.Scan(
		&item.UUID, &category, &typ, &kind, &driver, &item.Name,
		&item.BaseURL, &item.CredentialsConfigured, &settings,
		&publication, &status, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return Integration{}, err
	}
	item.Category = Category(category)
	item.Type = Type(typ)
	item.Kind = deployment.Kind(kind)
	item.Driver = driver
	item.Status = Status(status)
	item.Settings = append(json.RawMessage(nil), settings...)
	if len(publication) > 0 && string(publication) != "null" {
		var policy deployment.InstancePublicationPolicy
		if err := json.Unmarshal(publication, &policy); err == nil {
			item.Publication = &policy
		}
	}
	return item, nil
}

func (s *store) list(ctx context.Context, limit, offset int) (Page, error) {
	page := Page{Items: make([]Integration, 0, limit)}
	if err := s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM integrations`).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := s.db.Pool.Query(ctx, `SELECT `+integrationColumns+` FROM integrations ORDER BY name LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanIntegration(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

func (s *store) get(ctx context.Context, id uuid.UUID) (*Integration, *credentialRecord, error) {
	row := s.db.Pool.QueryRow(ctx, `SELECT `+integrationColumns+`,
		credentials_ciphertext, COALESCE(credentials_key_version, 0)
		FROM integrations WHERE id = $1`, id)
	var item Integration
	var ciphertext []byte
	var keyVersion int
	var category, typ, kind, driver, status string
	var settings []byte
	var publication []byte
	if err := row.Scan(
		&item.UUID, &category, &typ, &kind, &driver, &item.Name,
		&item.BaseURL, &item.CredentialsConfigured, &settings,
		&publication, &status, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
		&ciphertext, &keyVersion,
	); err != nil {
		return nil, nil, err
	}
	item.Category = Category(category)
	item.Type = Type(typ)
	item.Kind = deployment.Kind(kind)
	item.Driver = driver
	item.Status = Status(status)
	item.Settings = append(json.RawMessage(nil), settings...)
	if len(publication) > 0 && string(publication) != "null" {
		var policy deployment.InstancePublicationPolicy
		if err := json.Unmarshal(publication, &policy); err == nil {
			item.Publication = &policy
		}
	}
	var record *credentialRecord
	if len(ciphertext) > 0 {
		record = &credentialRecord{Ciphertext: ciphertext, KeyVersion: uint32(keyVersion)}
	}
	return &item, record, nil
}

type writeParams struct {
	ID          uuid.UUID
	Category    Category
	Type        Type
	Kind        deployment.Kind
	Driver      string
	Name        string
	BaseURL     string
	Settings    json.RawMessage
	Publication *deployment.InstancePublicationPolicy
	Actor       uuid.UUID
	Ciphertext  []byte
	KeyVersion  uint32
}

func (s *store) create(ctx context.Context, p writeParams) (*Integration, error) {
	if len(p.Settings) == 0 {
		p.Settings = json.RawMessage(`{}`)
	}
	var publication any
	if p.Publication != nil {
		raw, err := json.Marshal(p.Publication)
		if err != nil {
			return nil, err
		}
		publication = raw
	}
	var ciphertext any
	var keyVersion any
	if len(p.Ciphertext) > 0 {
		ciphertext = p.Ciphertext
		keyVersion = int(p.KeyVersion)
	}
	var kind any
	if p.Kind != "" {
		kind = string(p.Kind)
	}
	var driver any
	if p.Driver != "" {
		driver = p.Driver
	}
	row := s.db.Pool.QueryRow(ctx, `
		INSERT INTO integrations
			(id, category, type, kind, driver, name, base_url, credentials_ciphertext, credentials_key_version,
			 settings, publication_policy, status, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'UNKNOWN',$12)
		RETURNING `+integrationColumns,
		p.ID, string(p.Category), string(p.Type), kind, driver, p.Name, nullString(p.BaseURL),
		ciphertext, keyVersion, p.Settings, publication, p.Actor)
	item, err := scanIntegration(row)
	if err != nil {
		if isUniqueName(err) {
			return nil, conflictError(CodeNameExists, "an integration with this name already exists")
		}
		return nil, err
	}
	return &item, nil
}

type updateParams struct {
	ID          uuid.UUID
	Name        *string
	BaseURL     *string
	Settings    *json.RawMessage
	Publication *deployment.InstancePublicationPolicy
	Ciphertext  []byte
	KeyVersion  uint32
}

func (s *store) update(ctx context.Context, p updateParams) (*Integration, error) {
	var publication any
	if p.Publication != nil {
		raw, err := json.Marshal(p.Publication)
		if err != nil {
			return nil, err
		}
		publication = raw
	}
	row := s.db.Pool.QueryRow(ctx, `
		UPDATE integrations SET
			name = COALESCE($2, name),
			base_url = COALESCE($3, base_url),
			settings = COALESCE($4, settings),
			publication_policy = COALESCE($5, publication_policy),
			credentials_ciphertext = COALESCE($6, credentials_ciphertext),
			credentials_key_version = COALESCE($7, credentials_key_version),
			updated_at = NOW()
		WHERE id = $1
		RETURNING `+integrationColumns,
		p.ID, p.Name, p.BaseURL, nullableSettings(p.Settings), publication,
		nullableBytes(p.Ciphertext), nullableInt(p),
	)
	item, err := scanIntegration(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFoundError()
		}
		if isUniqueName(err) {
			return nil, conflictError(CodeNameExists, "an integration with this name already exists")
		}
		return nil, err
	}
	return &item, nil
}

func nullableSettings(settings *json.RawMessage) any {
	if settings == nil {
		return nil
	}
	return []byte(*settings)
}

func nullableInt(p updateParams) any {
	if len(p.Ciphertext) == 0 {
		return nil
	}
	return int(p.KeyVersion)
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func (s *store) delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.db.Pool.Exec(ctx, `DELETE FROM integrations WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return notFoundError()
	}
	return nil
}

func (s *store) setStatus(ctx context.Context, id uuid.UUID, status Status) error {
	_, err := s.db.Pool.Exec(ctx, `UPDATE integrations SET status = $2, updated_at = NOW() WHERE id = $1`, id, string(status))
	return err
}

// inUse checks every foreign key that can pin a provider integration.
func (s *store) inUse(ctx context.Context, id uuid.UUID) (bool, error) {
	var used bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM deployment_binding_targets WHERE integration_id = $1)
			OR EXISTS (SELECT 1 FROM deployment_template_allowlist_targets WHERE integration_id = $1)
			OR EXISTS (SELECT 1 FROM repository_bindings WHERE integration_id = $1)
			OR EXISTS (SELECT 1 FROM instance_network_leases WHERE integration_id = $1 AND released_at IS NULL)
			OR EXISTS (SELECT 1 FROM ad_repository_assignments WHERE source_integration_id = $1 OR exploit_integration_id = $1)
	`, id).Scan(&used)
	return used, err
}

// audit writes one admin audit row inside the same pool. It records only
// metadata; credentials never appear in details.
func (s *store) audit(ctx context.Context, actor uuid.UUID, action string, resourceID uuid.UUID) {
	_, _ = s.db.Pool.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,'integration',$3,'{}'::jsonb)
	`, actor, action, resourceID)
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isUniqueName(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}
