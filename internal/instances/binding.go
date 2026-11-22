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

// BindInput selects an exact approved immutable version and non-secret
// parameters. SecretParameters are write-only and are sealed immediately with
// BindingSecretAAD; no raw provider target/image is accepted.
type BindInput struct {
	BoardID          uuid.UUID
	ModuleID         uuid.UUID
	TaskID           *uuid.UUID
	Scope            Scope
	TemplateID       uuid.UUID
	TemplateVersion  int
	Parameters       map[string]string
	SecretParameters map[string]string
}

// BindingResult is the persisted binding summary. It contains no secret value.
type BindingResult struct {
	BindingID           uuid.UUID
	TemplateVersionID   uuid.UUID
	Targets             map[deployment.Kind]uuid.UUID
	SecretParameterKeys []string
}

// BindingSummary is the read model for an existing binding. It never contains
// secret parameter values or provider credentials.
type BindingSummary struct {
	BindingID           uuid.UUID
	BoardID             uuid.UUID
	ModuleID            uuid.UUID
	TaskID              *uuid.UUID
	TemplateVersionID   uuid.UUID
	Parameters          map[string]string
	SecretParameterKeys []string
	Targets             map[deployment.Kind]uuid.UUID
	Lifecycle           deployment.Lifecycle
}

// BindInstance persists an allowlisted exact version + exact targets for a task
// or pentest scope. It is the exported binding API for the board/backend owner.
func (s *Service) BindInstance(ctx context.Context, actor authorization.Principal, input BindInput) (*BindingResult, error) {
	if input.BoardID == uuid.Nil || input.ModuleID == uuid.Nil {
		return nil, validation("boardUuid", "boardUuid and moduleUuid are required")
	}
	if input.Scope != ScopeTask && input.Scope != ScopePentest {
		return nil, validation("scope", "scope must be TASK or PENTEST")
	}
	if input.Scope == ScopeTask && input.TaskID == nil {
		return nil, validation("taskUuid", "taskUuid is required for a task binding")
	}
	if input.Scope == ScopePentest && input.TaskID != nil {
		return nil, validation("taskUuid", "a pentest binding must not target a task")
	}
	if input.TemplateID == uuid.Nil || input.TemplateVersion < 1 {
		return nil, validation("templateVersion", "deploymentTemplateUuid and templateVersion are required")
	}
	if s.sealer == nil {
		return nil, instanceError(CodeDependencyUnavailable, "secret encryption is unavailable", 503)
	}
	if err := s.authorizeBinding(ctx, actor, input.BoardID, input.ModuleID); err != nil {
		return nil, err
	}
	return s.store.bindInstance(ctx, actor.UserID, input, s.sealer, s.keyVersion)
}

// GetBinding returns the safe binding read model for an existing scope.
func (s *Service) GetBinding(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, taskID *uuid.UUID) (*BindingSummary, error) {
	if err := s.authorizeBinding(ctx, actor, boardID, moduleID); err != nil {
		return nil, err
	}
	return s.store.bindingSummary(ctx, boardID, moduleID, taskID)
}

// ApprovedTemplateVersion is one scoped, currently active allowlisted
// immutable version that an owner/editor may bind. It contains no provider
// credentials, targets or secret values.
type ApprovedTemplateVersion struct {
	TemplateUUID     uuid.UUID
	Slug             string
	Name             string
	Version          int
	ParametersPolicy json.RawMessage
	// Parameters is the safe projection of the immutable published manifest's
	// parameter *definitions*. It carries no files, env values, provider
	// credentials or native IDs, and a secret parameter's default is always
	// blank so a default secret is never disclosed.
	Parameters []TemplateParameter
}

// TemplateParameter is the picker/form projection of one declared template
// parameter. It mirrors deployment.Parameter without any provider detail.
type TemplateParameter struct {
	Key      string
	Label    string
	Type     string
	Options  []string
	Default  string
	Required bool
	Secret   bool
}

// projectTemplateParameters masks secret defaults and drops everything that is
// not a parameter definition.
func projectTemplateParameters(manifestRaw []byte) []TemplateParameter {
	var manifest deployment.Template
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil
	}
	projected := make([]TemplateParameter, 0, len(manifest.Parameters))
	for _, parameter := range manifest.Parameters {
		item := TemplateParameter{
			Key:      parameter.Key,
			Label:    parameter.Label,
			Type:     parameter.Type,
			Options:  append([]string(nil), parameter.Options...),
			Required: parameter.Required,
			Secret:   parameter.Secret,
		}
		if !parameter.Secret {
			item.Default = parameter.Default
		}
		projected = append(projected, item)
	}
	return projected
}

// ListApprovedTemplateVersions enumerates only versions explicitly allowlisted
// for this board/module (or board-wide). Authorization is the same scoped
// deployment.templates.use / module.instances.admin check used for binding.
func (s *Service) ListApprovedTemplateVersions(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID) ([]ApprovedTemplateVersion, error) {
	if boardID == uuid.Nil || moduleID == uuid.Nil {
		return nil, validation("boardUuid", "boardUuid and moduleUuid are required")
	}
	if err := s.authorizeBinding(ctx, actor, boardID, moduleID); err != nil {
		return nil, err
	}
	rows, err := s.store.db.Pool.Query(ctx, `
		SELECT DISTINCT t.id, t.slug, t.name, v.version, a.parameters_policy, v.manifest
		FROM deployment_template_allowlist a
		JOIN deployment_template_versions v ON v.id = a.template_version_id
		JOIN deployment_templates t ON t.id = v.template_id
		WHERE a.board_id = $1
		  AND (a.module_id IS NULL OR a.module_id = $2)
		  AND a.revoked_at IS NULL
		  AND (a.expires_at IS NULL OR a.expires_at > NOW())
		ORDER BY t.name, v.version DESC
	`, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ApprovedTemplateVersion{}
	for rows.Next() {
		var item ApprovedTemplateVersion
		var policy, manifestRaw []byte
		if err := rows.Scan(&item.TemplateUUID, &item.Slug, &item.Name, &item.Version, &policy, &manifestRaw); err != nil {
			return nil, err
		}
		item.ParametersPolicy = append(json.RawMessage(nil), policy...)
		item.Parameters = projectTemplateParameters(manifestRaw)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) authorizeBinding(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID) error {
	if actor.UserID == uuid.Nil {
		return unauthorized()
	}
	if s.auth == nil {
		return instanceError(CodeInternal, "authorization is not configured", 500)
	}
	scope := authorization.Scope{BoardID: boardID, ModuleID: &moduleID}
	decision, err := s.auth.Authorize(ctx, actor, authorization.PermissionDeploymentTemplatesUse, scope)
	if err == nil && decision.Allowed {
		return nil
	}
	// Staff who may administer module instances can also bind; this is the same
	// permission used by the instance admin routes.
	adminDecision, adminErr := s.auth.Authorize(ctx, actor, authorization.PermissionModuleInstancesAdmin, scope)
	if adminErr == nil && adminDecision.Allowed {
		return nil
	}
	return forbidden()
}

// --- store ----------------------------------------------------------------

func (s *store) bindInstance(ctx context.Context, actor uuid.UUID, input BindInput, sealer interface {
	Seal(plaintext, aad []byte) ([]byte, error)
}, keyVersion uint32) (*BindingResult, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 1. The exact immutable version must belong to the requested template.
	var versionID uuid.UUID
	var manifestRaw []byte
	err = tx.QueryRow(ctx, `
		SELECT id, manifest FROM deployment_template_versions
		WHERE template_id = $1 AND version = $2
	`, input.TemplateID, input.TemplateVersion).Scan(&versionID, &manifestRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound(CodeTemplateNotFound)
		}
		return nil, err
	}
	var manifest deployment.Template
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil, instanceError(CodeTemplateInvalid, "stored template manifest is invalid", 500)
	}

	// 2. An active 012 allowlist for this exact version and scope.
	var allowlistID uuid.UUID
	var parametersPolicy []byte
	err = tx.QueryRow(ctx, `
		SELECT id, parameters_policy FROM deployment_template_allowlist
		WHERE template_version_id = $1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		  AND board_id = $2 AND (module_id IS NULL OR module_id = $3)
		ORDER BY module_id NULLS LAST, created_at DESC
		LIMIT 1
	`, versionID, input.BoardID, input.ModuleID).Scan(&allowlistID, &parametersPolicy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, instanceError(CodeTemplateInvalid, "the exact template version is not approved for this module", 422)
		}
		return nil, err
	}
	if trimmed := strings.TrimSpace(string(parametersPolicy)); trimmed != "" && trimmed != "null" && trimmed != "{}" {
		// There is no persisted policy schema yet. Failing closed is safer than
		// silently ignoring an operator-declared restriction.
		return nil, instanceError(CodeTemplateInvalid, "this allowlist declares a parametersPolicy restriction, which is not supported yet; clear parametersPolicy on the allowlist (template parameters are validated from the published manifest) or use a template without one", 422)
	}
	rows, err := tx.Query(ctx, `
		SELECT kind, integration_id FROM deployment_template_allowlist_targets
		WHERE allowlist_id = $1
		ORDER BY kind, integration_id
	`, allowlistID)
	if err != nil {
		return nil, err
	}
	targets := map[deployment.Kind]uuid.UUID{}
	for rows.Next() {
		var kind string
		var integrationID uuid.UUID
		if err := rows.Scan(&kind, &integrationID); err != nil {
			rows.Close()
			return nil, err
		}
		if _, exists := targets[deployment.Kind(kind)]; exists {
			rows.Close()
			return nil, instanceError(CodeTemplateInvalid, "the allowlist has ambiguous targets for one kind", 422)
		}
		targets[deployment.Kind(kind)] = integrationID
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, instanceError(CodeTemplateInvalid, "the allowlist has no targets", 422)
	}

	// 3. Validate parameters against the immutable manifest declaration.
	parameters, secretKeys, err := validateBindingParameters(manifest, input.Parameters, input.SecretParameters)
	if err != nil {
		return nil, err
	}
	secretPlaintext, err := json.Marshal(input.SecretParameters)
	if err != nil {
		return nil, err
	}

	// 4. Reuse the existing scoped binding row (unique scope) so sealed AAD
	// always matches the persisted binding id.
	var bindingID uuid.UUID
	existingErr := tx.QueryRow(ctx, `
		SELECT id FROM deployment_bindings
		WHERE board_id = $1 AND module_id = $2 AND task_id IS NOT DISTINCT FROM $3
		FOR UPDATE
	`, input.BoardID, input.ModuleID, input.TaskID).Scan(&bindingID)
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		return nil, existingErr
	}
	if bindingID == uuid.Nil {
		bindingID = uuid.New()
	}
	var ciphertext []byte
	var keyVersionArg any
	if len(input.SecretParameters) > 0 {
		ciphertext, err = sealer.Seal(secretPlaintext, BindingSecretAAD(bindingID))
		if err != nil {
			return nil, instanceError(CodeInternal, "secret encryption failed", 500)
		}
		keyVersionArg = int(keyVersion)
	}
	lifecycleRaw, err := json.Marshal(manifest.Lifecycle)
	if err != nil {
		return nil, err
	}
	parametersRaw, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	if existingErr == nil {
		if _, err := tx.Exec(ctx, `
			UPDATE deployment_bindings SET
				template_version_id = $2, parameters = $3,
				secret_parameters_ciphertext = COALESCE($4, secret_parameters_ciphertext),
				secret_key_version = COALESCE($5, secret_key_version),
				lifecycle = $6, updated_at = NOW()
			WHERE id = $1
		`, bindingID, versionID, parametersRaw, ciphertext, keyVersionArg, lifecycleRaw); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			INSERT INTO deployment_bindings
				(id, board_id, module_id, task_id, template_version_id, parameters,
				 secret_parameters_ciphertext, secret_key_version, lifecycle, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		`, bindingID, input.BoardID, input.ModuleID, input.TaskID, versionID, parametersRaw,
			ciphertext, keyVersionArg, lifecycleRaw, actor); err != nil {
			if isUniqueViolation(err) {
				return nil, conflict(CodeAlreadyExists, "a binding already exists for this scope; retry")
			}
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM deployment_binding_targets WHERE binding_id = $1`, bindingID); err != nil {
		return nil, err
	}
	for kind, integrationID := range targets {
		if _, err := tx.Exec(ctx, `
			INSERT INTO deployment_binding_targets (binding_id, kind, integration_id) VALUES ($1,$2,$3)
		`, bindingID, string(kind), integrationID); err != nil {
			return nil, err
		}
	}
	if input.Scope == ScopeTask {
		tag, err := tx.Exec(ctx, `UPDATE tasks SET deployment_binding_id = $2 WHERE id = $1 AND board_id = $3 AND module_id = $4 `,
			*input.TaskID, bindingID, input.BoardID, input.ModuleID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, validation("taskUuid", "task does not belong to the requested board/module")
		}
	} else {
		if _, err := tx.Exec(ctx, `
			INSERT INTO pentest_configs (module_id, deployment_binding_id, reset_enabled, max_resets, submission_policy, created_by)
			VALUES ($1,$2,FALSE,0,'{}'::jsonb,$3)
			ON CONFLICT (module_id) DO UPDATE SET deployment_binding_id = EXCLUDED.deployment_binding_id
		`, input.ModuleID, bindingID, actor); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.audit(ctx, actor, input.BoardID, input.ModuleID, "instance.binding.upsert", bindingID)
	return &BindingResult{BindingID: bindingID, TemplateVersionID: versionID, Targets: targets, SecretParameterKeys: secretKeys}, nil
}

func validateBindingParameters(manifest deployment.Template, parameters, secretParameters map[string]string) (map[string]string, []string, error) {
	declared := map[string]deployment.Parameter{}
	for _, parameter := range manifest.Parameters {
		declared[parameter.Key] = parameter
	}
	clean := map[string]string{}
	for key, value := range parameters {
		parameter, ok := declared[key]
		if !ok {
			return nil, nil, validation("parameters", "parameter is not declared by the template: "+key)
		}
		if parameter.Secret {
			return nil, nil, validation("parameters", "secret parameters must use secretParameters: "+key)
		}
		if len(value) > deployment.MaxStringLength {
			return nil, nil, validation("parameters", "parameter value is too large")
		}
		if parameter.Type == "choice" && len(parameter.Options) > 0 && !containsOption(parameter.Options, value) {
			return nil, nil, validation("parameters", "value is not an allowed choice: "+key)
		}
		clean[key] = value
	}
	secretKeys := make([]string, 0, len(secretParameters))
	for key, value := range secretParameters {
		parameter, ok := declared[key]
		if !ok || !parameter.Secret {
			return nil, nil, validation("secretParameters", "secret parameter is not declared by the template: "+key)
		}
		if strings.TrimSpace(value) == "" || len(value) > deployment.MaxStringLength {
			return nil, nil, validation("secretParameters", "secret parameter value is invalid")
		}
		secretKeys = append(secretKeys, key)
	}
	for _, parameter := range manifest.Parameters {
		if !parameter.Required {
			continue
		}
		if parameter.Secret {
			if _, ok := secretParameters[parameter.Key]; !ok {
				return nil, nil, validation("secretParameters", "required secret parameter is missing: "+parameter.Key)
			}
			continue
		}
		if _, ok := clean[parameter.Key]; !ok && strings.TrimSpace(parameter.Default) == "" {
			return nil, nil, validation("parameters", "required parameter is missing: "+parameter.Key)
		}
	}
	return clean, secretKeys, nil
}

func containsOption(options []string, value string) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}
	return false
}

func (s *store) bindingSummary(ctx context.Context, boardID, moduleID uuid.UUID, taskID *uuid.UUID) (*BindingSummary, error) {
	summary := &BindingSummary{BoardID: boardID, ModuleID: moduleID, TaskID: taskID,
		Parameters: map[string]string{}, Targets: map[deployment.Kind]uuid.UUID{}}
	var parametersRaw, lifecycleRaw []byte
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, template_version_id, parameters, lifecycle
		FROM deployment_bindings
		WHERE board_id = $1 AND module_id = $2 AND task_id IS NOT DISTINCT FROM $3
	`, boardID, moduleID, taskID).Scan(&summary.BindingID, &summary.TemplateVersionID, &parametersRaw, &lifecycleRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound(CodeNotFound)
		}
		return nil, err
	}
	_ = json.Unmarshal(parametersRaw, &summary.Parameters)
	_ = json.Unmarshal(lifecycleRaw, &summary.Lifecycle)
	rows, err := s.db.Pool.Query(ctx, `
		SELECT kind, integration_id FROM deployment_binding_targets WHERE binding_id = $1 ORDER BY kind
	`, summary.BindingID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind string
		var integrationID uuid.UUID
		if err := rows.Scan(&kind, &integrationID); err != nil {
			rows.Close()
			return nil, err
		}
		summary.Targets[deployment.Kind(kind)] = integrationID
	}
	rows.Close()
	// Secret keys are derived from the immutable manifest; values are never read.
	var manifestRaw []byte
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT manifest FROM deployment_template_versions WHERE id = $1
	`, summary.TemplateVersionID).Scan(&manifestRaw); err == nil {
		var manifest deployment.Template
		if json.Unmarshal(manifestRaw, &manifest) == nil {
			for _, parameter := range manifest.Parameters {
				if parameter.Secret {
					summary.SecretParameterKeys = append(summary.SecretParameterKeys, parameter.Key)
				}
			}
		}
	}
	return summary, nil
}

var _ = time.Now
