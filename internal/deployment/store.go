package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type store struct {
	db *postgres.DB
}

func newStore(db *postgres.DB) *store { return &store{db: db} }

// TemplateRecord is one deployment_templates row.
type TemplateRecord struct {
	UUID        uuid.UUID
	Slug        string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DraftRecord is the mutable draft associated with a template.
type DraftRecord struct {
	TemplateUUID uuid.UUID
	ManifestYAML []byte
	Files        map[string][]byte
	UpdatedBy    uuid.UUID
	UpdatedAt    time.Time
}

type draftFileWire struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"contentBase64"`
}

func (s *store) createTemplate(ctx context.Context, actor uuid.UUID, slug, name, description string, manifest []byte, files map[string][]byte) (*TemplateRecord, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, ErrNoDatabase
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("deployment: begin create template: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	record := &TemplateRecord{}
	err = tx.QueryRow(ctx, `
		INSERT INTO deployment_templates (slug, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, slug, name, description, created_at, updated_at
	`, slug, name, description, actor).Scan(
		&record.UUID, &record.Slug, &record.Name, &record.Description, &record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTemplateExists
		}
		return nil, fmt.Errorf("deployment: insert template: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO deployment_template_drafts (template_id, manifest_yaml, files, updated_by)
		VALUES ($1, $2, $3, $4)
	`, record.UUID, string(manifest), encodeFilesJSON(files), actor); err != nil {
		return nil, fmt.Errorf("deployment: insert draft: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("deployment: commit create template: %w", err)
	}
	return record, nil
}

func (s *store) templateByID(ctx context.Context, q querier, id uuid.UUID, forUpdate bool) (*TemplateRecord, error) {
	query := `
		SELECT id, slug, name, description, created_at, updated_at
		FROM deployment_templates
		WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	record := &TemplateRecord{}
	err := q.QueryRow(ctx, query, id).Scan(
		&record.UUID, &record.Slug, &record.Name, &record.Description, &record.CreatedAt, &record.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("deployment: read template: %w", err)
	}
	return record, nil
}

func (s *store) listTemplates(ctx context.Context, limit, offset int) ([]TemplateRecord, int64, error) {
	var total int64
	if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM deployment_templates`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("deployment: count templates: %w", err)
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, slug, name, description, created_at, updated_at
		FROM deployment_templates
		ORDER BY created_at DESC, id DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("deployment: list templates: %w", err)
	}
	defer rows.Close()
	records := make([]TemplateRecord, 0)
	for rows.Next() {
		var record TemplateRecord
		if err := rows.Scan(&record.UUID, &record.Slug, &record.Name, &record.Description, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("deployment: scan template: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("deployment: iterate templates: %w", err)
	}
	return records, total, nil
}

func (s *store) draftByTemplate(ctx context.Context, q querier, templateID uuid.UUID) (*DraftRecord, error) {
	record := &DraftRecord{TemplateUUID: templateID}
	var filesJSON []byte
	var manifest string
	err := q.QueryRow(ctx, `
		SELECT manifest_yaml, files, updated_by, updated_at
		FROM deployment_template_drafts
		WHERE template_id = $1
	`, templateID).Scan(&manifest, &filesJSON, &record.UpdatedBy, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("deployment: read draft: %w", err)
	}
	record.ManifestYAML = []byte(manifest)
	files, err := decodeFilesJSON(filesJSON)
	if err != nil {
		return nil, err
	}
	record.Files = files
	return record, nil
}

func (s *store) upsertDraft(ctx context.Context, actor uuid.UUID, templateID uuid.UUID, manifest []byte, files map[string][]byte, slug, name, description *string) (*DraftRecord, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("deployment: begin save draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.templateByID(ctx, tx, templateID, true); err != nil {
		return nil, err
	}
	if name != nil && description != nil {
		query := `UPDATE deployment_templates SET name = $2, description = $3, updated_at = clock_timestamp() WHERE id = $1`
		args := []any{templateID, *name, *description}
		if slug != nil {
			query = `UPDATE deployment_templates SET slug = $2, name = $3, description = $4, updated_at = clock_timestamp() WHERE id = $1`
			args = []any{templateID, *slug, *name, *description}
		}
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			if isUniqueViolation(err) {
				return nil, ErrTemplateExists
			}
			return nil, fmt.Errorf("deployment: update template metadata: %w", err)
		}
	}
	record := &DraftRecord{TemplateUUID: templateID, ManifestYAML: manifest, Files: files, UpdatedBy: actor}
	err = tx.QueryRow(ctx, `
		INSERT INTO deployment_template_drafts (template_id, manifest_yaml, files, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, clock_timestamp())
		ON CONFLICT (template_id) DO UPDATE
		SET manifest_yaml = EXCLUDED.manifest_yaml,
		    files = EXCLUDED.files,
		    updated_by = EXCLUDED.updated_by,
		    updated_at = clock_timestamp()
		RETURNING updated_at
	`, templateID, string(manifest), encodeFilesJSON(files), actor).Scan(&record.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("deployment: save draft: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("deployment: commit save draft: %w", err)
	}
	return record, nil
}

func (s *store) nextVersion(ctx context.Context, tx pgx.Tx, templateID uuid.UUID) (int, error) {
	var next int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM deployment_template_versions
		WHERE template_id = $1
	`, templateID).Scan(&next); err != nil {
		return 0, fmt.Errorf("deployment: allocate version: %w", err)
	}
	return next, nil
}

func (s *store) insertVersion(ctx context.Context, tx pgx.Tx, templateID uuid.UUID, version int, digest string, manifest Template, files map[string][]byte, actor uuid.UUID) (*TemplateVersion, error) {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("deployment: encode template manifest: %w", err)
	}
	record := &TemplateVersion{
		TemplateID: templateID, Version: version, Digest: digest, Manifest: manifest,
		Files: files, PublishedBy: actor,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO deployment_template_versions
			(id, template_id, version, digest, manifest, files, published_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, published_at
	`, uuid.New(), templateID, version, digest, string(manifestJSON), encodeFilesJSON(files), actor).Scan(&record.UUID, &record.PublishedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrVersionExists
		}
		return nil, fmt.Errorf("deployment: insert template version: %w", err)
	}
	return record, nil
}

func (s *store) listVersions(ctx context.Context, templateID uuid.UUID, limit, offset int) ([]TemplateVersion, int64, error) {
	var total int64
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM deployment_template_versions WHERE template_id = $1
	`, templateID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("deployment: count template versions: %w", err)
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, template_id, version, digest, manifest, files, published_at, published_by
		FROM deployment_template_versions
		WHERE template_id = $1
		ORDER BY version DESC
		LIMIT $2 OFFSET $3
	`, templateID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("deployment: list template versions: %w", err)
	}
	defer rows.Close()
	versions := make([]TemplateVersion, 0)
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, 0, err
		}
		versions = append(versions, *version)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("deployment: iterate template versions: %w", err)
	}
	return versions, total, nil
}

func (s *store) versionByNumber(ctx context.Context, templateID uuid.UUID, versionNumber int) (*TemplateVersion, error) {
	row := s.db.Pool.QueryRow(ctx, `
		SELECT id, template_id, version, digest, manifest, files, published_at, published_by
		FROM deployment_template_versions
		WHERE template_id = $1 AND version = $2
	`, templateID, versionNumber)
	version, err := scanVersion(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return version, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVersion(row rowScanner) (*TemplateVersion, error) {
	var (
		version    TemplateVersion
		manifestJS []byte
		filesJS    []byte
	)
	if err := row.Scan(
		&version.UUID, &version.TemplateID, &version.Version, &version.Digest,
		&manifestJS, &filesJS, &version.PublishedAt, &version.PublishedBy,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(manifestJS, &version.Manifest); err != nil {
		return nil, fmt.Errorf("deployment: decode version manifest: %w", err)
	}
	files, err := decodeFilesJSON(filesJS)
	if err != nil {
		return nil, err
	}
	version.Files = files
	return &version, nil
}

func encodeFilesJSON(files map[string][]byte) string {
	inputs := encodeFiles(files)
	wire := make([]draftFileWire, 0, len(inputs))
	for _, input := range inputs {
		wire = append(wire, draftFileWire{Path: input.Path, ContentBase64: input.ContentBase64})
	}
	encoded, _ := json.Marshal(wire)
	return string(encoded)
}

func decodeFilesJSON(raw []byte) (map[string][]byte, error) {
	if len(raw) == 0 {
		return map[string][]byte{}, nil
	}
	var wire []draftFileWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("deployment: decode package files: %w", err)
	}
	inputs := make([]FileInput, 0, len(wire))
	for _, item := range wire {
		inputs = append(inputs, FileInput{Path: item.Path, ContentBase64: item.ContentBase64})
	}
	files, findings := DecodeFiles(inputs)
	if len(findings) > 0 {
		return nil, fmt.Errorf("deployment: stored package files are invalid")
	}
	return fileMap(files), nil
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		return pgError.Code == "23505"
	}
	return false
}
