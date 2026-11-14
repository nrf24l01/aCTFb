package attackdefense

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AssignmentRecord is the raw assignment row including legacy columns.
type AssignmentRecord struct {
	Assignment
	SourceIntegrationID  uuid.UUID
	ExploitIntegrationID uuid.UUID
}

func (s *Store) ListAssignments(ctx context.Context, moduleID uuid.UUID, limit, offset int) ([]Assignment, int, error) {
	var total int
	if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM ad_repository_assignments WHERE module_id=$1`, moduleID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("attackdefense: count assignments: %w", err)
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, module_id, subject_type, subject_id,
		       source_integration_id, source_repository, source_branch,
		       exploit_integration_id, exploit_repository, exploit_branch,
		       created_by, created_at,
		       COALESCE(source_binding_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(exploit_binding_id, '00000000-0000-0000-0000-000000000000'::uuid)
		FROM ad_repository_assignments
		WHERE module_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, moduleID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("attackdefense: list assignments: %w", err)
	}
	defer rows.Close()
	items := make([]Assignment, 0)
	for rows.Next() {
		item, err := scanAssignment(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	if rows.Err() != nil {
		return nil, 0, fmt.Errorf("attackdefense: list assignments: %w", rows.Err())
	}
	return items, total, nil
}

func scanAssignment(row interface{ Scan(...any) error }) (*Assignment, error) {
	var item Assignment
	err := row.Scan(&item.ID, &item.ModuleID, &item.SubjectType, &item.SubjectID,
		&item.Source.IntegrationID, &item.Source.Repository, &item.Source.Branch,
		&item.Exploit.IntegrationID, &item.Exploit.Repository, &item.Exploit.Branch,
		&item.CreatedBy, &item.CreatedAt, &item.SourceBindingID, &item.ExploitBindingID)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) assignmentByID(ctx context.Context, q querier, moduleID, assignmentID uuid.UUID, forUpdate bool) (*Assignment, error) {
	query := `
		SELECT id, module_id, subject_type, subject_id,
		       source_integration_id, source_repository, source_branch,
		       exploit_integration_id, exploit_repository, exploit_branch,
		       created_by, created_at,
		       COALESCE(source_binding_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(exploit_binding_id, '00000000-0000-0000-0000-000000000000'::uuid)
		FROM ad_repository_assignments
		WHERE module_id=$1 AND id=$2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	item, err := scanAssignment(q.QueryRow(ctx, query, moduleID, assignmentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRepositoryBindingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read assignment: %w", err)
	}
	return item, nil
}

// AssignmentByID is the admin read.
func (s *Store) AssignmentByID(ctx context.Context, moduleID, assignmentID uuid.UUID) (*Assignment, error) {
	return s.assignmentByID(ctx, s.db.Pool, moduleID, assignmentID, false)
}

// AssignmentForSubject returns the participant assignment or
// ErrRepositoryNotConfigured.
func (s *Store) AssignmentForSubject(ctx context.Context, moduleID uuid.UUID, subject Subject) (*Assignment, error) {
	item, err := scanAssignment(s.db.Pool.QueryRow(ctx, `
		SELECT id, module_id, subject_type, subject_id,
		       source_integration_id, source_repository, source_branch,
		       exploit_integration_id, exploit_repository, exploit_branch,
		       created_by, created_at,
		       COALESCE(source_binding_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(exploit_binding_id, '00000000-0000-0000-0000-000000000000'::uuid)
		FROM ad_repository_assignments
		WHERE module_id=$1 AND subject_type=$2 AND subject_id=$3
	`, moduleID, subject.Type, subject.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRepositoryNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read subject assignment: %w", err)
	}
	return item, nil
}

// BindingInsert is one pre-sealed repository binding prepared by the service.
type BindingInsert struct {
	Binding    RepositoryBinding
	Ciphertext []byte
	KeyVersion int
}

// CreateAssignment inserts bindings and the assignment in one transaction; the
// legacy columns and 012 binding FKs are written together so no row is left
// half-bound.
func (s *Store) CreateAssignment(ctx context.Context, actor uuid.UUID, item *Assignment, bindings []BindingInsert, audit AuditEvent) (*Assignment, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, binding := range bindings {
		id, _, err := s.UpsertBinding(ctx, tx, binding.Binding, binding.Ciphertext, binding.KeyVersion)
		if err != nil {
			return nil, err
		}
		switch binding.Binding.ID {
		case item.SourceBindingID:
			item.SourceBindingID = id
		case item.ExploitBindingID:
			item.ExploitBindingID = id
		}
	}
	var created Assignment
	err = tx.QueryRow(ctx, `
		INSERT INTO ad_repository_assignments
			(module_id, subject_type, subject_id,
			 source_integration_id, source_repository, source_branch,
			 exploit_integration_id, exploit_repository, exploit_branch,
			 source_binding_id, exploit_binding_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, module_id, subject_type, subject_id,
		          source_integration_id, source_repository, source_branch,
		          exploit_integration_id, exploit_repository, exploit_branch,
		          created_by, created_at,
		          COALESCE(source_binding_id, '00000000-0000-0000-0000-000000000000'::uuid),
		          COALESCE(exploit_binding_id, '00000000-0000-0000-0000-000000000000'::uuid)
	`, item.ModuleID, item.SubjectType, item.SubjectID,
		item.Source.IntegrationID, item.Source.Repository, item.Source.Branch,
		item.Exploit.IntegrationID, item.Exploit.Repository, item.Exploit.Branch,
		item.SourceBindingID, item.ExploitBindingID, actor).Scan(
		&created.ID, &created.ModuleID, &created.SubjectType, &created.SubjectID,
		&created.Source.IntegrationID, &created.Source.Repository, &created.Source.Branch,
		&created.Exploit.IntegrationID, &created.Exploit.Repository, &created.Exploit.Branch,
		&created.CreatedBy, &created.CreatedAt, &created.SourceBindingID, &created.ExploitBindingID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrRepositoryAlreadyBound
		}
		return nil, fmt.Errorf("attackdefense: insert assignment: %w", err)
	}
	audit.ResourceID = &created.ID
	if err := writeAudit(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("attackdefense: commit assignment: %w", err)
	}
	return &created, nil
}

// UpdateAssignment replaces both repository bindings; in-flight runs keep their
// own assignment/commit evidence.
func (s *Store) UpdateAssignment(ctx context.Context, actor uuid.UUID, item *Assignment, bindings []BindingInsert, audit AuditEvent) (*Assignment, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.assignmentByID(ctx, tx, item.ModuleID, item.ID, true); err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		id, _, err := s.UpsertBinding(ctx, tx, binding.Binding, binding.Ciphertext, binding.KeyVersion)
		if err != nil {
			return nil, err
		}
		switch binding.Binding.ID {
		case item.SourceBindingID:
			item.SourceBindingID = id
		case item.ExploitBindingID:
			item.ExploitBindingID = id
		}
	}
	var created Assignment
	err = tx.QueryRow(ctx, `
		UPDATE ad_repository_assignments SET
			subject_type=$3, subject_id=$4,
			source_integration_id=$5, source_repository=$6, source_branch=$7,
			exploit_integration_id=$8, exploit_repository=$9, exploit_branch=$10,
			source_binding_id=$11, exploit_binding_id=$12
		WHERE module_id=$1 AND id=$2
		RETURNING id, module_id, subject_type, subject_id,
		          source_integration_id, source_repository, source_branch,
		          exploit_integration_id, exploit_repository, exploit_branch,
		          created_by, created_at,
		          COALESCE(source_binding_id, '00000000-0000-0000-0000-000000000000'::uuid),
		          COALESCE(exploit_binding_id, '00000000-0000-0000-0000-000000000000'::uuid)
	`, item.ModuleID, item.ID, item.SubjectType, item.SubjectID,
		item.Source.IntegrationID, item.Source.Repository, item.Source.Branch,
		item.Exploit.IntegrationID, item.Exploit.Repository, item.Exploit.Branch,
		item.SourceBindingID, item.ExploitBindingID).Scan(
		&created.ID, &created.ModuleID, &created.SubjectType, &created.SubjectID,
		&created.Source.IntegrationID, &created.Source.Repository, &created.Source.Branch,
		&created.Exploit.IntegrationID, &created.Exploit.Repository, &created.Exploit.Branch,
		&created.CreatedBy, &created.CreatedAt, &created.SourceBindingID, &created.ExploitBindingID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrRepositoryAlreadyBound
		}
		return nil, fmt.Errorf("attackdefense: update assignment: %w", err)
	}
	audit.ResourceID = &created.ID
	if err := writeAudit(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("attackdefense: commit assignment update: %w", err)
	}
	return &created, nil
}

// DeleteAssignment removes the binding link rows but never the shared
// repository binding (other subjects may reference it).
func (s *Store) DeleteAssignment(ctx context.Context, moduleID, assignmentID, actor uuid.UUID, audit AuditEvent) error {
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.assignmentByID(ctx, tx, moduleID, assignmentID, true); err != nil {
		return err
	}
	var runs int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ad_runs WHERE repository_assignment_id=$1`, assignmentID).Scan(&runs); err != nil {
		return fmt.Errorf("attackdefense: count assignment runs: %w", err)
	}
	if runs > 0 {
		return ErrRepositoryBindingInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ad_repository_assignments WHERE module_id=$1 AND id=$2`, moduleID, assignmentID); err != nil {
		return fmt.Errorf("attackdefense: delete assignment: %w", err)
	}
	if err := writeAudit(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("attackdefense: commit assignment delete: %w", err)
	}
	return nil
}

// BindingCiphertext is the raw encrypted secret row for the trusted webhook.
type BindingCiphertext struct {
	RepositoryBinding
	Ciphertext []byte
	KeyVersion int
}

// WebhookBinding loads the binding plus provider metadata and encrypted secret.
func (s *Store) WebhookBinding(ctx context.Context, bindingID uuid.UUID) (*BindingCiphertext, error) {
	var record BindingCiphertext
	err := s.db.Pool.QueryRow(ctx, `
		SELECT rb.id, rb.integration_id, rb.repository_ref, rb.branch,
		       rb.webhook_secret_ciphertext, rb.webhook_secret_key_version,
		       i.type, COALESCE(i.base_url,'')
		FROM repository_bindings rb
		JOIN integrations i ON i.id = rb.integration_id
		WHERE rb.id=$1
	`, bindingID).Scan(&record.ID, &record.IntegrationID, &record.Repository, &record.Branch,
		&record.Ciphertext, &record.KeyVersion, &record.Provider, &record.BaseURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRepositoryBindingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read webhook binding: %w", err)
	}
	return &record, nil
}

// BindingsByIDs loads the two bindings of an assignment.
func (s *Store) BindingsByIDs(ctx context.Context, sourceID, exploitID uuid.UUID) (map[string]*BindingCiphertext, error) {
	result := make(map[string]*BindingCiphertext, 2)
	for key, id := range map[string]uuid.UUID{"source": sourceID, "exploit": exploitID} {
		if id == uuid.Nil {
			continue
		}
		record, err := s.WebhookBinding(ctx, id)
		if err != nil {
			return nil, err
		}
		result[key] = record
	}
	return result, nil
}

// BindingBySpec finds an existing binding row. It is used for idempotent
// assignment updates.
func (s *Store) BindingBySpec(ctx context.Context, q querier, integrationID uuid.UUID, repository, branch string) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT id FROM repository_bindings
		WHERE integration_id=$1 AND repository_ref=$2 AND branch=$3
	`, integrationID, repository, branch).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrRepositoryBindingNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("attackdefense: read repository binding: %w", err)
	}
	return id, nil
}

// UpsertBinding inserts a pre-generated binding id with its encrypted secret.
// On conflict the existing row wins and `created` is false; the caller keeps
// the existing binding id and does not overwrite its secret.
func (s *Store) UpsertBinding(ctx context.Context, q querier, binding RepositoryBinding, ciphertext []byte, keyVersion int) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO repository_bindings
			(id, integration_id, repository_ref, branch, webhook_secret_ciphertext,
			 webhook_secret_key_version, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (integration_id, repository_ref, branch) DO NOTHING
		RETURNING id
	`, binding.ID, binding.IntegrationID, binding.Repository, binding.Branch, ciphertext, keyVersion, binding.CreatedBy).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, findErr := s.BindingBySpec(ctx, q, binding.IntegrationID, binding.Repository, binding.Branch)
		if findErr != nil {
			return uuid.Nil, false, findErr
		}
		return existing, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("attackdefense: insert repository binding: %w", err)
	}
	return id, true, nil
}

// BindingInUse reports whether any assignment still references the binding.
func (s *Store) BindingInUse(ctx context.Context, bindingID uuid.UUID) (bool, error) {
	var used bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM ad_repository_assignments
			WHERE source_binding_id=$1 OR exploit_binding_id=$1
		)
	`, bindingID).Scan(&used)
	if err != nil {
		return false, fmt.Errorf("attackdefense: check binding usage: %w", err)
	}
	return used, nil
}
