package instances

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/runtime"
)

type store struct {
	db *postgres.DB
}

func newStore(db *postgres.DB) *store { return &store{db: db} }

// sourceContext is the live board/module/task/pentest configuration. Every
// field is reloaded at start and rechecked by the worker before apply.
type sourceContext struct {
	BoardID                 uuid.UUID
	BoardStatus             string
	BoardStartsAt           *time.Time
	BoardEndsAt             *time.Time
	ParticipationMode       string
	ModuleID                uuid.UUID
	ModuleType              string
	ModuleEnabled           bool
	ModuleStartsAt          *time.Time
	ModuleEndsAt            *time.Time
	TaskID                  *uuid.UUID
	TaskInstanceEnabled     bool
	TaskBindingID           *uuid.UUID
	PentestBindingID        *uuid.UUID
	PentestResetEnabled     bool
	PentestMaxResets        int
	PentestSubmissionPolicy json.RawMessage
}

func (s *store) loadSource(ctx context.Context, scope Scope, boardID, moduleID uuid.UUID, taskID *uuid.UUID) (*sourceContext, error) {
	source := &sourceContext{BoardID: boardID, ModuleID: moduleID, TaskID: taskID}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT b.status, b.starts_at, b.ends_at, b.participation_mode,
		       m.type, m.enabled, m.starts_at, m.ends_at
		FROM boards b JOIN modules m ON m.board_id = b.id AND m.id = $2
		WHERE b.id = $1
	`, boardID, moduleID).Scan(
		&source.BoardStatus, &source.BoardStartsAt, &source.BoardEndsAt, &source.ParticipationMode,
		&source.ModuleType, &source.ModuleEnabled, &source.ModuleStartsAt, &source.ModuleEndsAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound(CodeNotFound)
		}
		return nil, err
	}
	switch scope {
	case ScopeTask:
		if taskID == nil {
			return nil, validation("scopeUuid", "taskUuid is required")
		}
		if source.ModuleType != "TASKS" {
			return nil, conflict(CodeModuleTypeMismatch, "module is not a TASKS module")
		}
		if err := s.db.Pool.QueryRow(ctx, `
			SELECT instance_enabled, deployment_binding_id
			FROM tasks WHERE board_id = $1 AND module_id = $2 AND id = $3
		`, boardID, moduleID, *taskID).Scan(&source.TaskInstanceEnabled, &source.TaskBindingID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, notFound(CodeNotFound)
			}
			return nil, err
		}
	case ScopePentest:
		if source.ModuleType != "PENTEST" {
			return nil, conflict(CodeModuleTypeMismatch, "module is not a PENTEST module")
		}
		err := s.db.Pool.QueryRow(ctx, `
			SELECT deployment_binding_id, reset_enabled, max_resets, submission_policy
			FROM pentest_configs WHERE module_id = $1
		`, moduleID).Scan(&source.PentestBindingID, &source.PentestResetEnabled, &source.PentestMaxResets, &source.PentestSubmissionPolicy)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, notFound(CodeTemplateNotFound)
			}
			return nil, err
		}
	default:
		return nil, validation("scope", "unsupported scope")
	}
	return source, nil
}

// resolveSubject maps the caller to the board scoring subject.
func (s *store) resolveSubject(ctx context.Context, boardID uuid.UUID, participationMode string, userID uuid.UUID) (string, uuid.UUID, string, error) {
	if participationMode != "TEAM" {
		var name string
		if err := s.db.Pool.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(username,''), id::text) FROM users WHERE id = $1 AND disabled = FALSE
		`, userID).Scan(&name); err != nil {
			return "", uuid.Nil, "", forbidden()
		}
		return "USER", userID, name, nil
	}
	var teamID uuid.UUID
	var teamName string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT t.id, t.name FROM team_members tm JOIN teams t ON t.board_id = tm.board_id AND t.id = tm.team_id
		WHERE tm.board_id = $1 AND tm.user_id = $2
	`, boardID, userID).Scan(&teamID, &teamName)
	if err != nil {
		return "", uuid.Nil, "", instanceError(CodeActionNotAllowed, "a team is required on this board", 409)
	}
	return "TEAM", teamID, teamName, nil
}

type bindingData struct {
	ID               uuid.UUID
	BoardID          uuid.UUID
	ModuleID         uuid.UUID
	TaskID           *uuid.UUID
	VersionID        uuid.UUID
	Digest           string
	Manifest         deployment.Template
	Files            map[string][]byte
	Parameters       map[string]string
	SecretCiphertext []byte
	SecretKeyVersion int
	Targets          map[deployment.Kind]uuid.UUID
	Lifecycle        deployment.Lifecycle
	AllowlistID      uuid.UUID
	AllowlistTargets map[deployment.Kind]map[uuid.UUID]struct{}
}

type draftFileWire struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"contentBase64"`
}

func decodeVersionFiles(raw []byte) (map[string][]byte, error) {
	files := map[string][]byte{}
	if len(raw) == 0 {
		return files, nil
	}
	var wire []draftFileWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	for _, item := range wire {
		decoded, err := base64.StdEncoding.DecodeString(item.ContentBase64)
		if err != nil {
			return nil, err
		}
		files[item.Path] = decoded
	}
	return files, nil
}

// loadBinding resolves the immutable version and enforces the active 012
// allowlist for the exact version and the exact integration targets.
func (s *store) loadBinding(ctx context.Context, bindingID, boardID, moduleID uuid.UUID) (*bindingData, error) {
	binding := &bindingData{Targets: map[deployment.Kind]uuid.UUID{}, AllowlistTargets: map[deployment.Kind]map[uuid.UUID]struct{}{}}
	var manifestRaw, filesRaw, parametersRaw []byte
	var secretCiphertext []byte
	var secretVersion *int
	var lifecycleRaw []byte
	err := s.db.Pool.QueryRow(ctx, `
		SELECT b.id, b.board_id, b.module_id, b.task_id, b.template_version_id,
		       v.digest, v.manifest, v.files, b.parameters, b.secret_parameters_ciphertext,
		       b.secret_key_version, b.lifecycle
		FROM deployment_bindings b
		JOIN deployment_template_versions v ON v.id = b.template_version_id
		WHERE b.id = $1 AND b.board_id = $2 AND b.module_id = $3
	`, bindingID, boardID, moduleID).Scan(
		&binding.ID, &binding.BoardID, &binding.ModuleID, &binding.TaskID, &binding.VersionID,
		&binding.Digest, &manifestRaw, &filesRaw, &parametersRaw, &secretCiphertext,
		&secretVersion, &lifecycleRaw,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound(CodeTemplateNotFound)
		}
		return nil, err
	}
	if err := json.Unmarshal(manifestRaw, &binding.Manifest); err != nil {
		return nil, instanceError(CodeTemplateInvalid, "stored template manifest is invalid", 500)
	}
	binding.Files, err = decodeVersionFiles(filesRaw)
	if err != nil {
		return nil, instanceError(CodeTemplateInvalid, "stored template files are invalid", 500)
	}
	binding.Parameters = map[string]string{}
	if len(parametersRaw) > 0 {
		if err := json.Unmarshal(parametersRaw, &binding.Parameters); err != nil {
			return nil, instanceError(CodeTemplateInvalid, "stored parameters are invalid", 500)
		}
	}
	binding.SecretCiphertext = secretCiphertext
	if secretVersion != nil {
		binding.SecretKeyVersion = *secretVersion
	}
	if len(lifecycleRaw) > 0 {
		_ = json.Unmarshal(lifecycleRaw, &binding.Lifecycle)
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT kind, integration_id FROM deployment_binding_targets WHERE binding_id = $1
	`, bindingID)
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
		binding.Targets[deployment.Kind(kind)] = integrationID
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Active 012 allowlist for the exact immutable version, in this board/module.
	var allowlistID uuid.UUID
	err = s.db.Pool.QueryRow(ctx, `
		SELECT id FROM deployment_template_allowlist
		WHERE template_version_id = $1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		  AND board_id = $2 AND (module_id IS NULL OR module_id = $3)
		ORDER BY module_id NULLS LAST, created_at DESC
		LIMIT 1
	`, binding.VersionID, boardID, moduleID).Scan(&allowlistID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, instanceError(CodeTemplateInvalid, "the exact template version is not approved for this module", 422)
		}
		return nil, err
	}
	binding.AllowlistID = allowlistID
	targetRows, err := s.db.Pool.Query(ctx, `
		SELECT kind, integration_id FROM deployment_template_allowlist_targets WHERE allowlist_id = $1
	`, allowlistID)
	if err != nil {
		return nil, err
	}
	for targetRows.Next() {
		var kind string
		var integrationID uuid.UUID
		if err := targetRows.Scan(&kind, &integrationID); err != nil {
			targetRows.Close()
			return nil, err
		}
		if binding.AllowlistTargets[deployment.Kind(kind)] == nil {
			binding.AllowlistTargets[deployment.Kind(kind)] = map[uuid.UUID]struct{}{}
		}
		binding.AllowlistTargets[deployment.Kind(kind)][integrationID] = struct{}{}
	}
	targetRows.Close()
	if err := targetRows.Err(); err != nil {
		return nil, err
	}
	for kind, integrationID := range binding.Targets {
		if _, ok := binding.AllowlistTargets[kind][integrationID]; !ok {
			return nil, instanceError(CodeTemplateInvalid, "binding target is not approved by the allowlist", 422)
		}
	}
	if len(binding.Targets) == 0 {
		return nil, instanceError(CodeTemplateInvalid, "binding has no provider targets", 422)
	}
	return binding, nil
}

// integrationTarget is the trusted provider configuration required by the
// orchestrator. Credentials are never read here.
type integrationTarget struct {
	ID          uuid.UUID
	Driver      string
	Kind        deployment.Kind
	BaseURL     string
	Settings    json.RawMessage
	Publication *deployment.InstancePublicationPolicy
}

func (s *store) loadIntegrationTargets(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]integrationTarget, error) {
	result := make(map[uuid.UUID]integrationTarget, len(ids))
	for _, id := range ids {
		var target integrationTarget
		var kind, driver string
		var settings []byte
		var publication []byte
		err := s.db.Pool.QueryRow(ctx, `
			SELECT id, COALESCE(kind,''), COALESCE(driver,''), COALESCE(base_url,''), settings, publication_policy
			FROM integrations WHERE id = $1
		`, id).Scan(&target.ID, &kind, &driver, &target.BaseURL, &settings, &publication)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, instanceError(CodeTemplateInvalid, "provider integration is missing", 422)
			}
			return nil, err
		}
		target.Kind = deployment.Kind(kind)
		target.Driver = driver
		target.Settings = append(json.RawMessage(nil), settings...)
		if len(publication) > 0 && string(publication) != "null" {
			var policy deployment.InstancePublicationPolicy
			if err := json.Unmarshal(publication, &policy); err == nil {
				target.Publication = &policy
			}
		}
		result[id] = target
	}
	return result, nil
}

type liveInstance struct {
	ID             uuid.UUID
	Status         Status
	Generation     int64
	ExpiresAt      *time.Time
	ExtensionCount int64
	RemovedAt      *time.Time
	Snapshot       map[string]any
}

func (s *store) liveInstance(ctx context.Context, tx pgx.Tx, boardID, moduleID uuid.UUID, scope Scope, scopeUUID uuid.UUID, subjectType string, subjectID uuid.UUID, forUpdate bool) (*liveInstance, error) {
	query := `
		SELECT id, status, generation, expires_at, extension_count, removed_at, desired_snapshot
		FROM instances
		WHERE board_id=$1 AND module_id=$2 AND scope=$3 AND scope_uuid=$4
		  AND subject_type=$5 AND subject_id=$6 AND removed_at IS NULL
	`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	row := tx.QueryRow(ctx, query, boardID, moduleID, scope, scopeUUID, subjectType, subjectID)
	live := &liveInstance{}
	var raw []byte
	if err := row.Scan(&live.ID, &live.Status, &live.Generation, &live.ExpiresAt, &live.ExtensionCount, &live.RemovedAt, &raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	live.Snapshot = map[string]any{}
	_ = json.Unmarshal(raw, &live.Snapshot)
	return live, nil
}

func (s *store) findByIdempotencyKey(ctx context.Context, key string) (*liveInstance, uuid.UUID, error) {
	live := &liveInstance{}
	var raw []byte
	var id uuid.UUID
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, status, generation, expires_at, extension_count, removed_at, desired_snapshot
		FROM instances WHERE idempotency_key = $1
	`, key).Scan(&id, &live.Status, &live.Generation, &live.ExpiresAt, &live.ExtensionCount, &live.RemovedAt, &raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, uuid.Nil, nil
		}
		return nil, uuid.Nil, err
	}
	live.ID = id
	live.Snapshot = map[string]any{}
	_ = json.Unmarshal(raw, &live.Snapshot)
	return live, id, nil
}

func (s *store) getInstance(ctx context.Context, id uuid.UUID) (*Instance, error) {
	instance := &Instance{}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT i.id, i.board_id, i.module_id, i.scope, i.scope_uuid, i.subject_type, i.subject_id,
		       u.username, i.status, i.created_at, i.started_at, i.expires_at, i.extension_count,
		       i.generation, i.binding_id, i.template_version_id, i.removed_at
		FROM instances i LEFT JOIN users u ON u.id = i.subject_id
		WHERE i.id = $1
	`, id).Scan(
		&instance.UUID, &instance.BoardID, &instance.ModuleID, &instance.Scope, &instance.ScopeUUID,
		&instance.SubjectType, &instance.SubjectID, &instance.SubjectName, &instance.Status,
		&instance.CreatedAt, &instance.StartedAt, &instance.ExpiresAt, &instance.ExtensionCount,
		&instance.Generation, &instance.BindingID, &instance.VersionID, &instance.RemovedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound(CodeNotFound)
		}
		return nil, err
	}
	nodes, err := s.loadNodes(ctx, id)
	if err != nil {
		return nil, err
	}
	instance.Nodes = nodes
	return instance, nil
}

func (s *store) loadNodes(ctx context.Context, instanceID uuid.UUID) ([]Node, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, key, name, kind, status, COALESCE(provider_resource_id,''), generation
		FROM instance_nodes WHERE instance_id = $1 ORDER BY key
	`, instanceID)
	if err != nil {
		return nil, err
	}
	nodes := []Node{}
	for rows.Next() {
		var node Node
		var kind string
		if err := rows.Scan(&node.ID, &node.Key, &node.Name, &kind, &node.Status, &node.ProviderID, &node.Generation); err != nil {
			rows.Close()
			return nil, err
		}
		node.Kind = deployment.Kind(kind)
		nodes = append(nodes, node)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range nodes {
		endpoints, err := s.loadEndpoints(ctx, instanceID, nodes[index].Key)
		if err != nil {
			return nil, err
		}
		nodes[index].Endpoints = endpoints
	}
	return nodes, nil
}

func (s *store) loadEndpoints(ctx context.Context, instanceID uuid.UUID, nodeKey string) ([]Endpoint, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT key, type, host, port, url, exposure, allocated_port, released_at
		FROM instance_endpoints
		WHERE instance_id = $1 AND node_key = $2 AND released_at IS NULL
		ORDER BY key
	`, instanceID, nodeKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	endpoints := []Endpoint{}
	for rows.Next() {
		var endpoint Endpoint
		var exposure string
		if err := rows.Scan(&endpoint.Key, &endpoint.Type, &endpoint.Host, &endpoint.Port, &endpoint.URL, &exposure, &endpoint.AllocatedPort, &endpoint.ReleasedAt); err != nil {
			return nil, err
		}
		endpoint.Exposure = deployment.Exposure(exposure)
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, rows.Err()
}

func (s *store) operations(ctx context.Context, instanceID uuid.UUID, ids []uuid.UUID) ([]Operation, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, instance_id, node_key, operation, generation, status, idempotency_key
		FROM instance_operations
		WHERE instance_id = $1 AND id = ANY($2)
		ORDER BY node_key, operation
	`, instanceID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	operations := []Operation{}
	for rows.Next() {
		var operation Operation
		var kind string
		if err := rows.Scan(&operation.ID, &operation.InstanceID, &operation.NodeKey, &kind, &operation.Generation, &operation.Status, &operation.IdempotencyKey); err != nil {
			return nil, err
		}
		operation.Kind = runtime.OperationKind(kind)
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
