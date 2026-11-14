package instances

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

func (s *store) findLive(ctx context.Context, boardID, moduleID uuid.UUID, scope Scope, scopeUUID uuid.UUID, subjectType string, subjectID uuid.UUID) (*Instance, error) {
	var id uuid.UUID
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id FROM instances
		WHERE board_id=$1 AND module_id=$2 AND scope=$3 AND scope_uuid=$4
		  AND subject_type=$5 AND subject_id=$6 AND removed_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, boardID, moduleID, scope, scopeUUID, subjectType, subjectID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound(CodeNotFound)
		}
		return nil, err
	}
	return s.getInstance(ctx, id)
}

func (s *store) listInstances(ctx context.Context, boardID, moduleID *uuid.UUID, scope *Scope, limit, offset int) ([]Instance, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id FROM instances
		WHERE ($1::uuid IS NULL OR board_id = $1)
		  AND ($2::uuid IS NULL OR module_id = $2)
		  AND ($3::text IS NULL OR scope = $3)
		ORDER BY created_at DESC LIMIT $4 OFFSET $5
	`, boardID, moduleID, nullableScope(scope), limit, offset)
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]Instance, 0, len(ids))
	for _, id := range ids {
		instance, err := s.getInstance(ctx, id)
		if err != nil {
			return nil, err
		}
		items = append(items, *instance)
	}
	return items, nil
}

func nullableScope(scope *Scope) any {
	if scope == nil {
		return nil
	}
	return string(*scope)
}

func (s *store) countInstances(ctx context.Context, boardID, moduleID *uuid.UUID) (int64, error) {
	var total int64
	err := s.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM instances
		WHERE ($1::uuid IS NULL OR board_id = $1) AND ($2::uuid IS NULL OR module_id = $2)
	`, boardID, moduleID).Scan(&total)
	return total, err
}

func (s *store) isTeamMember(ctx context.Context, boardID, teamID, userID uuid.UUID) (bool, error) {
	var member bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM team_members WHERE board_id=$1 AND team_id=$2 AND user_id=$3)
	`, boardID, teamID, userID).Scan(&member)
	return member, err
}

// subjectInModuleAudience enforces the module audience constraint. No audience
// rows means the module is available to every joined subject.
func (s *store) subjectInModuleAudience(ctx context.Context, boardID, moduleID uuid.UUID, subjectType string, subjectID uuid.UUID) (bool, error) {
	var gated bool
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM module_audiences WHERE module_id = $1)
	`, moduleID).Scan(&gated); err != nil {
		return false, err
	}
	if !gated {
		return true, nil
	}
	var allowed bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM module_audiences ma
			JOIN audience_members am ON am.audience_id = ma.audience_id
			WHERE ma.module_id = $1 AND am.subject_type = $2 AND am.subject_id = $3
		)
	`, moduleID, subjectType, subjectID).Scan(&allowed)
	return allowed, err
}

func (s *store) usedAllocationKeys(ctx context.Context, integrationIDs []uuid.UUID) (map[uuid.UUID]map[string]struct{}, error) {
	result := make(map[uuid.UUID]map[string]struct{}, len(integrationIDs))
	for _, id := range integrationIDs {
		result[id] = map[string]struct{}{}
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT integration_id, allocation_key FROM instance_network_leases
		WHERE integration_id = ANY($1) AND released_at IS NULL
	`, integrationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var integrationID uuid.UUID
		var key string
		if err := rows.Scan(&integrationID, &key); err != nil {
			return nil, err
		}
		result[integrationID][key] = struct{}{}
	}
	return result, rows.Err()
}

func (s *store) upsertPentestEnvironment(ctx context.Context, moduleID uuid.UUID, subjectType string, subjectID, instanceID uuid.UUID, expiresAt *time.Time) error {
	expiry := time.Now()
	if expiresAt != nil {
		expiry = *expiresAt
	}
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO pentest_environments (module_id, subject_type, subject_id, instance_id, status, expires_at)
		VALUES ($1,$2,$3,$4,'PROVISIONING',$5)
		ON CONFLICT (module_id, subject_type, subject_id) DO UPDATE
		SET instance_id = EXCLUDED.instance_id, status = 'PROVISIONING', expires_at = EXCLUDED.expires_at
	`, moduleID, subjectType, subjectID, instanceID, expiry)
	return err
}

func (s *store) pentestResetsUsed(ctx context.Context, moduleID uuid.UUID, subjectType string, subjectID uuid.UUID) (int, error) {
	var used int
	err := s.db.Pool.QueryRow(ctx, `
		SELECT resets_used FROM pentest_environments WHERE module_id=$1 AND subject_type=$2 AND subject_id=$3
	`, moduleID, subjectType, subjectID).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return used, err
}

func (s *store) incrementPentestResets(ctx context.Context, moduleID uuid.UUID, subjectType string, subjectID uuid.UUID) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE pentest_environments SET resets_used = resets_used + 1
		WHERE module_id=$1 AND subject_type=$2 AND subject_id=$3
	`, moduleID, subjectType, subjectID)
	return err
}

func (s *store) syncPentestEnvironmentStatus(ctx context.Context, instanceID uuid.UUID, status Status) {
	_, _ = s.db.Pool.Exec(ctx, `
		UPDATE pentest_environments SET status = $2 WHERE instance_id = $1
	`, instanceID, string(status))
}

// leaseClaim returns the trusted network claim for one integration.
func (s *store) leaseClaim(ctx context.Context, instanceID, integrationID uuid.UUID) (runtime.NetworkClaim, error) {
	var raw []byte
	err := s.db.Pool.QueryRow(ctx, `
		SELECT claim FROM instance_network_leases
		WHERE instance_id=$1 AND integration_id=$2 AND released_at IS NULL
		ORDER BY leased_at LIMIT 1
	`, instanceID, integrationID).Scan(&raw)
	if err != nil {
		return runtime.NetworkClaim{}, err
	}
	var claim runtime.NetworkClaim
	if err := json.Unmarshal(raw, &claim); err != nil {
		return runtime.NetworkClaim{}, err
	}
	return claim, nil
}

// allocation builds the core-issued allocation (network claim + public ports)
// for one node.
func (s *store) allocation(ctx context.Context, instanceID, integrationID uuid.UUID, nodeKey string, claim runtime.NetworkClaim) (*runtime.Allocation, error) {
	allocation := &runtime.Allocation{Network: claim, Ports: map[string]runtime.PortBinding{}}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT key, COALESCE(allocated_port, port), host
		FROM instance_endpoints
		WHERE instance_id=$1 AND node_key=$2 AND released_at IS NULL AND exposure='PUBLIC'
	`, instanceID, nodeKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, host string
		var port int
		if err := rows.Scan(&key, &port, &host); err != nil {
			return nil, err
		}
		allocation.Ports[key] = runtime.PortBinding{Host: host, Port: port}
	}
	return allocation, rows.Err()
}

// nodeProviderIDs returns the current provider ids per node for destroy.
func (s *store) nodeProviderIDs(ctx context.Context, instanceID uuid.UUID) (map[string]string, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT key, COALESCE(provider_resource_id,'') FROM instance_nodes WHERE instance_id=$1
	`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var key, providerID string
		if err := rows.Scan(&key, &providerID); err != nil {
			return nil, err
		}
		result[key] = providerID
	}
	return result, rows.Err()
}

// loadBindingForApply reads the immutable version and binding parameters
// without re-checking a possibly-revoked allowlist. The queued snapshot already
// froze the version; revocation stops new starts, not cleanup.
func (s *store) loadBindingForApply(ctx context.Context, bindingID uuid.UUID) (*bindingData, error) {
	binding := &bindingData{Targets: map[deployment.Kind]uuid.UUID{}}
	var manifestRaw, filesRaw, parametersRaw, lifecycleRaw []byte
	var secretVersion *int
	err := s.db.Pool.QueryRow(ctx, `
		SELECT b.id, b.board_id, b.module_id, b.task_id, b.template_version_id,
		       v.digest, v.manifest, v.files, b.parameters, b.secret_parameters_ciphertext,
		       b.secret_key_version, b.lifecycle
		FROM deployment_bindings b
		JOIN deployment_template_versions v ON v.id = b.template_version_id
		WHERE b.id = $1
	`, bindingID).Scan(
		&binding.ID, &binding.BoardID, &binding.ModuleID, &binding.TaskID, &binding.VersionID,
		&binding.Digest, &manifestRaw, &filesRaw, &parametersRaw, &binding.SecretCiphertext,
		&secretVersion, &lifecycleRaw,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(manifestRaw, &binding.Manifest); err != nil {
		return nil, err
	}
	binding.Files, err = decodeVersionFiles(filesRaw)
	if err != nil {
		return nil, err
	}
	binding.Parameters = map[string]string{}
	if len(parametersRaw) > 0 {
		_ = json.Unmarshal(parametersRaw, &binding.Parameters)
	}
	if secretVersion != nil {
		binding.SecretKeyVersion = *secretVersion
	}
	if len(lifecycleRaw) > 0 {
		_ = json.Unmarshal(lifecycleRaw, &binding.Lifecycle)
	}
	return binding, nil
}

// generatedSecrets returns the sealed generated values already stored.
func (s *store) generatedSecrets(ctx context.Context, instanceID uuid.UUID) (map[string]map[string]struct {
	Ciphertext []byte
	KeyVersion uint32
}, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT node_key, key, ciphertext, key_version FROM instance_secret_values WHERE instance_id = $1
	`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[string]struct {
		Ciphertext []byte
		KeyVersion uint32
	}{}
	for rows.Next() {
		var nodeKey, key string
		var ciphertext []byte
		var version int
		if err := rows.Scan(&nodeKey, &key, &ciphertext, &version); err != nil {
			return nil, err
		}
		if result[nodeKey] == nil {
			result[nodeKey] = map[string]struct {
				Ciphertext []byte
				KeyVersion uint32
			}{}
		}
		result[nodeKey][key] = struct {
			Ciphertext []byte
			KeyVersion uint32
		}{Ciphertext: ciphertext, KeyVersion: uint32(version)}
	}
	return result, rows.Err()
}

func (s *store) saveGenerated(ctx context.Context, instanceID uuid.UUID, nodeKey, key string, ciphertext []byte, keyVersion uint32) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO instance_secret_values (instance_id, node_key, key, ciphertext, key_version)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (instance_id, node_key, key) DO UPDATE
		SET ciphertext = EXCLUDED.ciphertext, key_version = EXCLUDED.key_version
	`, instanceID, nodeKey, key, ciphertext, int(keyVersion))
	return err
}

func (s *store) audit(ctx context.Context, actor uuid.UUID, boardID, moduleID uuid.UUID, action string, resourceID uuid.UUID) {
	var board any
	var module any
	var actorArg any
	if boardID != uuid.Nil {
		board = boardID
	}
	if moduleID != uuid.Nil {
		module = moduleID
	}
	if actor != uuid.Nil {
		actorArg = actor
	}
	resource := "instance"
	if strings.HasPrefix(action, "deployment_allowlist.") {
		resource = "deployment_template_allowlist"
	}
	_, _ = s.db.Pool.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, module_id, action, resource_type, resource_id, details)
		VALUES ($1,$2,$3,$4,$5,$6,'{}'::jsonb)
	`, actorArg, board, module, action, resource, resourceID)
}
