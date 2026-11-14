package instances

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
)

// AllowlistTarget is one exact approved provider target. No provider URL,
// credential or native identifier is exposed here.
type AllowlistTarget struct {
	IntegrationUUID uuid.UUID
	Kind            deployment.Kind
}

// Allowlist is the admin read model for one immutable-version allowlist.
type Allowlist struct {
	UUID                uuid.UUID
	TemplateVersionUUID uuid.UUID
	BoardID             *uuid.UUID
	ModuleID            *uuid.UUID
	ParametersPolicy    json.RawMessage
	ApprovedBy          uuid.UUID
	CreatedAt           time.Time
	ExpiresAt           *time.Time
	RevokedAt           *time.Time
	Targets             []AllowlistTarget
}

// AllowlistCreateInput is the validated admin mutation input.
type AllowlistCreateInput struct {
	TemplateVersionUUID uuid.UUID
	BoardID             *uuid.UUID
	ModuleID            *uuid.UUID
	ParametersPolicy    json.RawMessage
	ExpiresAt           *time.Time
	Targets             []AllowlistTarget
}

// AllowlistListQuery optionally narrows enumeration to an exact board/module
// scope. Mutations are global-admin only; enumeration never returns provider
// credentials, file content or native provider identifiers.
type AllowlistListQuery struct {
	BoardID  *uuid.UUID
	ModuleID *uuid.UUID
}

// ListAllowlists returns allowlists for one template. Only a global admin with
// platform.deployment_templates.publish may enumerate them.
func (s *Service) ListAllowlists(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, query AllowlistListQuery) ([]Allowlist, error) {
	if err := s.authorizeAllowlist(ctx, actor); err != nil {
		return nil, err
	}
	if exists, err := s.store.templateExists(ctx, templateID); err != nil {
		return nil, err
	} else if !exists {
		return nil, notFound(CodeTemplateNotFound)
	}
	return s.store.listAllowlists(ctx, templateID, query)
}

// CreateAllowlist stores an exact immutable version + exact target set. A
// duplicate active row for the same scope is a conflict, not a silent merge.
func (s *Service) CreateAllowlist(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, input AllowlistCreateInput) (*Allowlist, error) {
	if err := s.authorizeAllowlist(ctx, actor); err != nil {
		return nil, err
	}
	if input.TemplateVersionUUID == uuid.Nil {
		return nil, validation("templateVersionUuid", "templateVersionUuid is required")
	}
	if (input.ModuleID != nil) && input.BoardID == nil {
		return nil, validation("boardUuid", "boardUuid is required when moduleUuid is set")
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		return nil, validation("expiresAt", "expiresAt must be in the future")
	}
	if len(input.ParametersPolicy) > 64<<10 {
		return nil, validation("parametersPolicy", "parametersPolicy is too large")
	}
	if len(input.ParametersPolicy) > 0 {
		if !json.Valid(input.ParametersPolicy) {
			return nil, validation("parametersPolicy", "parametersPolicy must be valid JSON")
		}
		if containsSensitiveKey(input.ParametersPolicy) {
			return nil, validation("parametersPolicy", "parametersPolicy must not contain secret or credential values")
		}
	}
	if len(input.Targets) == 0 || len(input.Targets) > 32 {
		return nil, validation("targets", "between 1 and 32 exact targets are required")
	}
	seen := map[string]struct{}{}
	for _, target := range input.Targets {
		if target.IntegrationUUID == uuid.Nil {
			return nil, validation("targets", "integrationUuid is required")
		}
		if target.Kind != deployment.KindVM && target.Kind != deployment.KindContainer {
			return nil, validation("targets", "kind must be VM or CONTAINER")
		}
		key := string(target.Kind) + ":" + target.IntegrationUUID.String()
		if _, exists := seen[key]; exists {
			return nil, validation("targets", "duplicate target")
		}
		seen[key] = struct{}{}
	}
	if exists, err := s.store.templateExists(ctx, templateID); err != nil {
		return nil, err
	} else if !exists {
		return nil, notFound(CodeTemplateNotFound)
	}
	allowlist, err := s.store.createAllowlist(ctx, actor.UserID, templateID, input)
	if err != nil {
		return nil, err
	}
	s.store.audit(ctx, actor.UserID, derefUUID(input.BoardID), derefUUID(input.ModuleID), "deployment_allowlist.create", allowlist.UUID)
	return allowlist, nil
}

// RevokeAllowlist revokes one exact row. Revocation is scoped to the template
// path and is idempotent for an already revoked row.
func (s *Service) RevokeAllowlist(ctx context.Context, actor authorization.Principal, templateID, allowlistID uuid.UUID) error {
	if err := s.authorizeAllowlist(ctx, actor); err != nil {
		return err
	}
	if err := s.store.revokeAllowlist(ctx, templateID, allowlistID); err != nil {
		return err
	}
	s.store.audit(ctx, actor.UserID, uuid.Nil, uuid.Nil, "deployment_allowlist.revoke", allowlistID)
	return nil
}

func (s *Service) authorizeAllowlist(ctx context.Context, actor authorization.Principal) error {
	if actor.UserID == uuid.Nil {
		return unauthorized()
	}
	if s.auth == nil {
		return instanceError(CodeInternal, "authorization is not configured", 500)
	}
	decision, err := s.auth.Authorize(ctx, actor, authorization.PermissionPlatformDeploymentTemplatesPublish, authorization.Scope{})
	if err != nil {
		return instanceError(CodeInternal, "authorization failed", 500)
	}
	if !decision.Allowed {
		return forbidden()
	}
	return nil
}

func derefUUID(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
}

func containsSensitiveKey(raw json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	var walk func(any) bool
	walk = func(node any) bool {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				lower := strings.ToLower(key)
				if lower == "secret" || lower == "password" || lower == "token" || lower == "credential" ||
					lower == "credentials" || strings.HasSuffix(lower, "secret") ||
					strings.HasSuffix(lower, "token") || strings.HasSuffix(lower, "password") {
					return true
				}
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(value)
}

// --- store ----------------------------------------------------------------

func (s *store) templateExists(ctx context.Context, templateID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM deployment_templates WHERE id = $1)`, templateID).Scan(&exists)
	return exists, err
}

func (s *store) listAllowlists(ctx context.Context, templateID uuid.UUID, query AllowlistListQuery) ([]Allowlist, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT a.id, a.template_version_id, a.board_id, a.module_id, a.parameters_policy,
		       a.approved_by, a.created_at, a.expires_at, a.revoked_at
		FROM deployment_template_allowlist a
		JOIN deployment_template_versions v ON v.id = a.template_version_id
		WHERE v.template_id = $1
		  AND ($2::uuid IS NULL OR a.board_id = $2)
		  AND ($3::uuid IS NULL OR a.module_id = $3)
		ORDER BY a.created_at DESC, a.id
	`, templateID, query.BoardID, query.ModuleID)
	if err != nil {
		return nil, err
	}
	items := []Allowlist{}
	ids := []uuid.UUID{}
	for rows.Next() {
		var item Allowlist
		var policy []byte
		if err := rows.Scan(&item.UUID, &item.TemplateVersionUUID, &item.BoardID, &item.ModuleID,
			&policy, &item.ApprovedBy, &item.CreatedAt, &item.ExpiresAt, &item.RevokedAt); err != nil {
			rows.Close()
			return nil, err
		}
		item.ParametersPolicy = append(json.RawMessage(nil), policy...)
		items = append(items, item)
		ids = append(ids, item.UUID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return items, nil
	}
	targetRows, err := s.db.Pool.Query(ctx, `
		SELECT allowlist_id, kind, integration_id
		FROM deployment_template_allowlist_targets
		WHERE allowlist_id = ANY($1)
		ORDER BY allowlist_id, kind, integration_id
	`, ids)
	if err != nil {
		return nil, err
	}
	defer targetRows.Close()
	targets := map[uuid.UUID][]AllowlistTarget{}
	for targetRows.Next() {
		var allowlistID uuid.UUID
		var kind string
		var integrationID uuid.UUID
		if err := targetRows.Scan(&allowlistID, &kind, &integrationID); err != nil {
			return nil, err
		}
		targets[allowlistID] = append(targets[allowlistID], AllowlistTarget{IntegrationUUID: integrationID, Kind: deployment.Kind(kind)})
	}
	if err := targetRows.Err(); err != nil {
		return nil, err
	}
	for index := range items {
		items[index].Targets = targets[items[index].UUID]
	}
	return items, nil
}

func (s *store) createAllowlist(ctx context.Context, actor, templateID uuid.UUID, input AllowlistCreateInput) (*Allowlist, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var versionTemplate uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT template_id FROM deployment_template_versions WHERE id = $1
	`, input.TemplateVersionUUID).Scan(&versionTemplate); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, validation("templateVersionUuid", "the exact immutable version does not exist")
		}
		return nil, err
	}
	if versionTemplate != templateID {
		return nil, validation("templateVersionUuid", "the version does not belong to this template")
	}
	if input.BoardID != nil {
		var boardExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM boards WHERE id = $1)`, *input.BoardID).Scan(&boardExists); err != nil {
			return nil, err
		}
		if !boardExists {
			return nil, notFound(CodeNotFound)
		}
	}
	if input.ModuleID != nil {
		var moduleBoard uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT board_id FROM modules WHERE id = $1`, *input.ModuleID).Scan(&moduleBoard); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, notFound(CodeNotFound)
			}
			return nil, err
		}
		if moduleBoard != *input.BoardID {
			return nil, validation("moduleUuid", "module does not belong to the board")
		}
	}
	for _, target := range input.Targets {
		var kind string
		err := tx.QueryRow(ctx, `
			SELECT COALESCE(kind,'') FROM integrations WHERE id = $1 AND category = 'COMPUTE'
		`, target.IntegrationUUID).Scan(&kind)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, validation("targets", "the integration does not exist or is not a compute provider")
			}
			return nil, err
		}
		if kind != string(target.Kind) {
			return nil, validation("targets", "target kind does not match the integration kind")
		}
	}

	allowlist := &Allowlist{
		UUID: uuid.New(), TemplateVersionUUID: input.TemplateVersionUUID,
		BoardID: input.BoardID, ModuleID: input.ModuleID,
		ParametersPolicy: append(json.RawMessage(nil), input.ParametersPolicy...),
		ApprovedBy:       actor, CreatedAt: time.Now(), ExpiresAt: input.ExpiresAt,
		Targets: append([]AllowlistTarget(nil), input.Targets...),
	}
	var policy any
	if len(input.ParametersPolicy) > 0 {
		policy = []byte(input.ParametersPolicy)
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM deployment_template_allowlist
			WHERE template_version_id = $1
			  AND board_id IS NOT DISTINCT FROM $2
			  AND module_id IS NOT DISTINCT FROM $3
			  AND revoked_at IS NULL
		)
	`, input.TemplateVersionUUID, input.BoardID, input.ModuleID).Scan(&duplicate); err != nil {
		return nil, err
	}
	if duplicate {
		return nil, conflict(CodeAllowlistConflict, "an active allowlist already exists for this exact scope")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO deployment_template_allowlist
			(id, template_version_id, board_id, module_id, parameters_policy, approved_by, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, allowlist.UUID, input.TemplateVersionUUID, input.BoardID, input.ModuleID, policy, actor, input.ExpiresAt); err != nil {
		if isUniqueViolation(err) {
			return nil, conflict(CodeAllowlistConflict, "an active allowlist already exists for this exact scope")
		}
		return nil, err
	}
	for _, target := range input.Targets {
		if _, err := tx.Exec(ctx, `
			INSERT INTO deployment_template_allowlist_targets (allowlist_id, kind, integration_id)
			VALUES ($1,$2,$3)
		`, allowlist.UUID, string(target.Kind), target.IntegrationUUID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return allowlist, nil
}

func (s *store) revokeAllowlist(ctx context.Context, templateID, allowlistID uuid.UUID) error {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE deployment_template_allowlist a
		SET revoked_at = COALESCE(a.revoked_at, NOW())
		FROM deployment_template_versions v
		WHERE a.id = $2 AND a.template_version_id = v.id AND v.template_id = $1
	`, templateID, allowlistID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return notFound(CodeNotFound)
	}
	return nil
}

var _ = deployment.KindContainer
