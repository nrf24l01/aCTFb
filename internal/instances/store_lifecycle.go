package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

type endpointPlan struct {
	NodeKey    string
	Key        string
	Type       string
	Exposure   deployment.Exposure
	TargetPort int
	PortFrom   int
	PortTo     int
	Host       string
}

type leasePlan struct {
	IntegrationID uuid.UUID
	Kind          deployment.Kind
	Claim         runtime.NetworkClaim
}

// ttlClamp carries the task-scope lifetime in seconds. The database computes the
// expiry from its own clock so created_at and the anchor are the same instant.
type ttlClamp struct {
	InitialSeconds  int64
	MaxTotalSeconds int64
}

type createParams struct {
	InstanceID     uuid.UUID
	BoardID        uuid.UUID
	ModuleID       uuid.UUID
	Scope          Scope
	ScopeUUID      uuid.UUID
	SubjectType    string
	SubjectID      uuid.UUID
	IdempotencyKey string
	BindingID      *uuid.UUID
	VersionID      *uuid.UUID
	Snapshot       []byte
	Authorized     AuthorizedContext
	Nodes          []SnapshotNode
	ExpiresAt      *time.Time
	EndpointPlans  []endpointPlan
	Leases         []leasePlan
	TTLClamp       *ttlClamp
	MaxConcurrent  int
	boardEndsAt    *time.Time
	moduleEndsAt   *time.Time
}

// createInstance writes the instance, nodes, operations, network leases and
// endpoint leases in one transaction. Provider calls happen only after this
// transaction commits.
func (s *store) createInstance(ctx context.Context, p createParams) (uuid.UUID, []Operation, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, nil, err
	}
	defer tx.Rollback(ctx)

	if p.MaxConcurrent > 0 {
		// Serialize per module+subject so two parallel task starts cannot both
		// pass the limit. Live rows count as allocated until cleanup is
		// confirmed, which is exactly removed_at IS NULL.
		lockKey := fmt.Sprintf("actfb:instance-concurrency:v1:%s:%s:%s", p.ModuleID, p.SubjectType, p.SubjectID)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
			return uuid.Nil, nil, err
		}
		var live int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM instances
			WHERE module_id = $1 AND subject_type = $2 AND subject_id = $3 AND removed_at IS NULL
		`, p.ModuleID, p.SubjectType, p.SubjectID).Scan(&live); err != nil {
			return uuid.Nil, nil, err
		}
		if live >= p.MaxConcurrent {
			return uuid.Nil, nil, instanceError(CodeLimitReached, "the concurrent instance limit for this subject is reached", 409)
		}
	}
	if p.TTLClamp != nil {
		// LEAST ignores NULLs, so a missing board/module end does not remove the
		// lifetime cap. created_at defaults to NOW(), the same statement clock.
		if _, err := tx.Exec(ctx, `
			INSERT INTO instances
				(id, board_id, module_id, scope, scope_uuid, subject_type, subject_id, status,
				 expires_at, generation, idempotency_key, binding_id, template_version_id,
				 desired_snapshot, request_actor_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'PROVISIONING',
			        LEAST(NOW() + make_interval(secs => $8),
			              NOW() + make_interval(secs => $9),
			              $10, $11),
			        1,$12,$13,$14,$15,$16)
		`, p.InstanceID, p.BoardID, p.ModuleID, string(p.Scope), p.ScopeUUID, p.SubjectType, p.SubjectID,
			float64(p.TTLClamp.InitialSeconds), float64(p.TTLClamp.MaxTotalSeconds),
			p.boardEndsAt, p.moduleEndsAt,
			p.IdempotencyKey, p.BindingID, p.VersionID, p.Snapshot, p.Authorized.ActorUserID,
		); err != nil {
			return uuid.Nil, nil, err
		}
	} else if _, err := tx.Exec(ctx, `
		INSERT INTO instances
			(id, board_id, module_id, scope, scope_uuid, subject_type, subject_id, status,
			 expires_at, generation, idempotency_key, binding_id, template_version_id,
			 desired_snapshot, request_actor_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'PROVISIONING',$8,1,$9,$10,$11,$12,$13)
	`, p.InstanceID, p.BoardID, p.ModuleID, string(p.Scope), p.ScopeUUID, p.SubjectType, p.SubjectID,
		p.ExpiresAt, p.IdempotencyKey, p.BindingID, p.VersionID, p.Snapshot, p.Authorized.ActorUserID,
	); err != nil {
		return uuid.Nil, nil, err
	}

	operations := make([]Operation, 0, len(p.Nodes))
	for _, node := range p.Nodes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO instance_nodes (instance_id, key, name, kind, status, generation)
			VALUES ($1,$2,$3,$4,'PROVISIONING',1)
		`, p.InstanceID, node.Key, node.Key, string(node.Kind)); err != nil {
			return uuid.Nil, nil, err
		}
		op := Operation{
			ID:             uuid.New(),
			InstanceID:     p.InstanceID,
			NodeKey:        node.Key,
			Kind:           runtime.OperationCreate,
			Generation:     1,
			Status:         "QUEUED",
			IdempotencyKey: fmt.Sprintf("%s:%s:1:CREATE", p.InstanceID, node.Key),
		}
		contextJSON, _ := json.Marshal(p.Authorized)
		if _, err := tx.Exec(ctx, `
			INSERT INTO instance_operations
				(id, instance_id, node_key, operation, generation, idempotency_key, status,
				 requested_by, authorized_context, desired_snapshot)
			VALUES ($1,$2,$3,'CREATE',1,$4,'QUEUED',$5,$6,$7)
		`, op.ID, p.InstanceID, node.Key, op.IdempotencyKey, p.Authorized.ActorUserID, contextJSON, p.Snapshot); err != nil {
			return uuid.Nil, nil, err
		}
		operations = append(operations, op)
	}

	for _, lease := range p.Leases {
		if err := allocateLease(ctx, tx, p.InstanceID, lease); err != nil {
			return uuid.Nil, nil, err
		}
	}
	for _, plan := range p.EndpointPlans {
		if err := allocateEndpoint(ctx, tx, p.InstanceID, plan); err != nil {
			return uuid.Nil, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, nil, err
	}
	return p.InstanceID, operations, nil
}

func allocateLease(ctx context.Context, tx pgx.Tx, instanceID uuid.UUID, lease leasePlan) error {
	claim, _ := json.Marshal(lease.Claim)
	baseKey := lease.Claim.Key
	for attempt := 0; attempt < 20; attempt++ {
		key := baseKey
		if attempt > 0 {
			key = fmt.Sprintf("%s-%d", baseKey, attempt)
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO instance_network_leases (instance_id, integration_id, kind, allocation_key, claim)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (integration_id, allocation_key) WHERE released_at IS NULL DO NOTHING
		`, instanceID, lease.IntegrationID, string(lease.Kind), key, claim)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
	}
	return instanceError(CodeLimitReached, "no free network claim in the provider pool", 409)
}

func allocateEndpoint(ctx context.Context, tx pgx.Tx, instanceID uuid.UUID, plan endpointPlan) error {
	if plan.Exposure != deployment.ExposurePublic {
		_, err := tx.Exec(ctx, `
			INSERT INTO instance_endpoints
				(instance_id, node_key, key, type, host, port, exposure)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (instance_id, node_key, key) DO UPDATE
			SET type = EXCLUDED.type, host = EXCLUDED.host, port = EXCLUDED.port,
			    exposure = EXCLUDED.exposure, released_at = NULL
		`, instanceID, plan.NodeKey, plan.Key, plan.Type, "pending", plan.TargetPort, string(plan.Exposure))
		return err
	}
	if plan.PortFrom <= 0 || plan.PortTo < plan.PortFrom || plan.Host == "" {
		return instanceError(CodeTemplateInvalid, "public endpoint allocation is not configured", 422)
	}
	for port := plan.PortFrom; port <= plan.PortTo; port++ {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM instance_endpoints WHERE host = $1 AND port = $2 AND released_at IS NULL)
		`, plan.Host, port).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO instance_endpoints
				(instance_id, node_key, key, type, host, port, exposure, allocated_port)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$6)
			ON CONFLICT (host, port) WHERE released_at IS NULL DO NOTHING
		`, instanceID, plan.NodeKey, plan.Key, plan.Type, plan.Host, port, string(plan.Exposure))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
	}
	return instanceError(CodeLimitReached, "no free public port in the integration policy", 409)
}

// resetInstance advances the generation and queues RESET for every node. Old
// provider objects stay owned by the previous generation; the driver removes
// them before creating the new one.
// resetPentestInstanceAtomic serializes resets for one pentest subject and
// consumes the limit only after a successful reset. Concurrent resets therefore
// cannot exceed maxResets or both win the same generation.
func (s *store) resetPentestInstanceAtomic(ctx context.Context, moduleID uuid.UUID, subjectType string, subjectID, instanceID uuid.UUID, maxResets int, authorized AuthorizedContext) (int64, []Operation, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	lockKey := moduleID.String() + ":" + subjectType + ":" + subjectID.String()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return 0, nil, err
	}
	var used int
	err = tx.QueryRow(ctx, `
		SELECT resets_used FROM pentest_environments
		WHERE module_id=$1 AND subject_type=$2 AND subject_id=$3
		FOR UPDATE
	`, moduleID, subjectType, subjectID).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		used = 0
	} else if err != nil {
		return 0, nil, err
	}
	if used >= maxResets {
		return 0, nil, conflict(CodeResetLimitReached, "reset limit reached")
	}
	generation, operations, err := resetInstanceInTx(ctx, tx, instanceID, authorized)
	if err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pentest_environments SET resets_used = resets_used + 1
		WHERE module_id=$1 AND subject_type=$2 AND subject_id=$3
	`, moduleID, subjectType, subjectID); err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return generation, operations, nil
}

func (s *store) resetInstance(ctx context.Context, instanceID uuid.UUID, authorized AuthorizedContext) (int64, []Operation, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	generation, operations, err := resetInstanceInTx(ctx, tx, instanceID, authorized)
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return generation, operations, nil
}

// resetInstanceInTx performs the reset in the caller's transaction. Keeping it
// in the same transaction avoids acquiring a second pool connection while the
// caller already holds one (for example the serialized PENTEST reset path).
func resetInstanceInTx(ctx context.Context, tx pgx.Tx, instanceID uuid.UUID, authorized AuthorizedContext) (int64, []Operation, error) {
	var generation int64
	if err := tx.QueryRow(ctx, `
		UPDATE instances SET generation = generation + 1, status = 'RESETTING'
		WHERE id = $1 AND removed_at IS NULL
		RETURNING generation
	`, instanceID).Scan(&generation); err != nil {
		return 0, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT key FROM instance_nodes WHERE instance_id = $1 ORDER BY key`, instanceID)
	if err != nil {
		return 0, nil, err
	}
	nodeKeys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return 0, nil, err
		}
		nodeKeys = append(nodeKeys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	operations := make([]Operation, 0, len(nodeKeys))
	contextJSON, _ := json.Marshal(authorized)
	var snapshot []byte
	_ = tx.QueryRow(ctx, `SELECT desired_snapshot FROM instances WHERE id = $1`, instanceID).Scan(&snapshot)
	for _, key := range nodeKeys {
		op := Operation{
			ID: uuid.New(), InstanceID: instanceID, NodeKey: key,
			Kind: runtime.OperationReset, Generation: generation, Status: "QUEUED",
			IdempotencyKey: fmt.Sprintf("%s:%s:%d:RESET", instanceID, key, generation),
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO instance_operations
				(id, instance_id, node_key, operation, generation, idempotency_key, status,
				 requested_by, authorized_context, desired_snapshot)
			VALUES ($1,$2,$3,'RESET',$4,$5,'QUEUED',$6,$7,$8)
			ON CONFLICT (instance_id, node_key, generation, operation) DO NOTHING
		`, op.ID, instanceID, key, generation, op.IdempotencyKey, authorized.ActorUserID, contextJSON, snapshot); err != nil {
			return 0, nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE instance_nodes SET status = 'PROVISIONING', generation = $2 WHERE instance_id = $1 AND key = $3
		`, instanceID, generation, key); err != nil {
			return 0, nil, err
		}
		operations = append(operations, op)
	}
	// Rotate core-issued public ports only after the provider destroy/create
	// confirms; the current endpoints stay visible until then.
	return generation, operations, nil
}

// destroyInstance queues DESTROY for the current generation.
func (s *store) destroyInstance(ctx context.Context, instanceID uuid.UUID, authorized AuthorizedContext) (int64, []Operation, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	var generation int64
	if err := tx.QueryRow(ctx, `
		UPDATE instances SET status = 'DESTROYING' WHERE id = $1 AND removed_at IS NULL
		RETURNING generation
	`, instanceID).Scan(&generation); err != nil {
		return 0, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT key, COALESCE(provider_resource_id,'') FROM instance_nodes WHERE instance_id = $1`, instanceID)
	if err != nil {
		return 0, nil, err
	}
	type nodeRef struct{ key, providerID string }
	refs := []nodeRef{}
	for rows.Next() {
		var ref nodeRef
		if err := rows.Scan(&ref.key, &ref.providerID); err != nil {
			rows.Close()
			return 0, nil, err
		}
		refs = append(refs, ref)
	}
	rows.Close()
	contextJSON, _ := json.Marshal(authorized)
	operations := make([]Operation, 0, len(refs))
	for _, ref := range refs {
		op := Operation{
			ID: uuid.New(), InstanceID: instanceID, NodeKey: ref.key,
			Kind: runtime.OperationDestroy, Generation: generation, Status: "QUEUED",
			IdempotencyKey: fmt.Sprintf("%s:%s:%d:DESTROY", instanceID, ref.key, generation),
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO instance_operations
				(id, instance_id, node_key, operation, generation, idempotency_key, status,
				 requested_by, authorized_context)
			VALUES ($1,$2,$3,'DESTROY',$4,$5,'QUEUED',$6,$7)
			ON CONFLICT (instance_id, node_key, generation, operation) DO NOTHING
		`, op.ID, instanceID, ref.key, generation, op.IdempotencyKey, nullableActor(authorized.ActorUserID), contextJSON); err != nil {
			return 0, nil, err
		}
		operations = append(operations, op)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return generation, operations, nil
}

// workerInstance is the trusted worker view: snapshot plus live status.
type workerInstance struct {
	ID          uuid.UUID
	BoardID     uuid.UUID
	ModuleID    uuid.UUID
	Scope       Scope
	ScopeUUID   uuid.UUID
	SubjectType string
	SubjectID   uuid.UUID
	Status      Status
	Generation  int64
	ExpiresAt   *time.Time
	BindingID   *uuid.UUID
	VersionID   *uuid.UUID
	Snapshot    DesiredSnapshot
}

func (s *store) workerInstance(ctx context.Context, instanceID uuid.UUID) (*workerInstance, error) {
	instance := &workerInstance{}
	var raw []byte
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, board_id, module_id, scope, scope_uuid, subject_type, subject_id, status,
		       generation, expires_at, binding_id, template_version_id, desired_snapshot
		FROM instances WHERE id = $1
	`, instanceID).Scan(
		&instance.ID, &instance.BoardID, &instance.ModuleID, &instance.Scope, &instance.ScopeUUID,
		&instance.SubjectType, &instance.SubjectID, &instance.Status, &instance.Generation,
		&instance.ExpiresAt, &instance.BindingID, &instance.VersionID, &raw,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &instance.Snapshot); err != nil {
		return nil, err
	}
	return instance, nil
}

// claimOperations marks queued operations RUNNING and returns the authoritative
// authorized context for each. A job for an outdated generation is canceled and
// never creates future provider resources.
func (s *store) claimOperations(ctx context.Context, job Job) ([]Operation, error) {
	operations, err := s.operations(ctx, job.InstanceID, job.OperationIDs)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var instanceGeneration int64
	var removedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT generation, removed_at FROM instances WHERE id = $1`, job.InstanceID).Scan(&instanceGeneration, &removedAt); err != nil {
		return nil, err
	}
	claimed := make([]Operation, 0, len(operations))
	for _, operation := range operations {
		if operation.Generation != instanceGeneration || job.Generation != operation.Generation {
			if _, err := tx.Exec(ctx, `
				UPDATE instance_operations SET status = 'CANCELED', finished_at = NOW()
				WHERE id = $1 AND status IN ('QUEUED','RUNNING')
			`, operation.ID); err != nil {
				return nil, err
			}
			continue
		}
		tag, err := tx.Exec(ctx, `
			UPDATE instance_operations
			SET status = 'RUNNING', started_at = COALESCE(started_at, NOW())
			WHERE id = $1 AND status IN ('QUEUED','RUNNING')
		`, operation.ID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 1 {
			operation.Status = "RUNNING"
			claimed = append(claimed, operation)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *store) operationContext(ctx context.Context, operationID uuid.UUID) (AuthorizedContext, error) {
	var raw []byte
	if err := s.db.Pool.QueryRow(ctx, `SELECT authorized_context FROM instance_operations WHERE id = $1`, operationID).Scan(&raw); err != nil {
		return AuthorizedContext{}, err
	}
	authorized := AuthorizedContext{}
	if err := json.Unmarshal(raw, &authorized); err != nil {
		return AuthorizedContext{}, err
	}
	return authorized, nil
}

func (s *store) markOperation(ctx context.Context, operationID uuid.UUID, status, message string) error {
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE instance_operations SET status = $2, error = NULLIF($3,''), finished_at = NOW()
		WHERE id = $1
	`, operationID, status, message)
	return err
}

func (s *store) nodeResult(ctx context.Context, instanceID uuid.UUID, nodeKey string, generation int64, driver string, resource runtime.ProviderResource) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	status := "PROVISIONING"
	switch resource.State {
	case runtime.NodeStateRunning:
		status = "RUNNING"
	case runtime.NodeStateError, runtime.NodeStateUnknown:
		status = "ERROR"
	case runtime.NodeStateDestroyed:
		status = "DESTROYED"
	}
	tag, err := tx.Exec(ctx, `
		UPDATE instance_nodes
		SET status = $4, provider_resource_id = NULLIF($5,''), generation = $6, provider_labels = $7
		WHERE instance_id = $1 AND key = $2 AND generation <= $3
	`, instanceID, nodeKey, generation, status, resource.ProviderID, generation, mustJSON(runtime.Labels(driver, runtime.Operation{InstanceID: instanceID, NodeKey: nodeKey, Generation: generation})))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// A newer generation superseded this operation; its result must not
		// overwrite the current provider inventory.
		_ = tx.Rollback(ctx)
		return nil
	}
	for _, endpoint := range resource.Endpoints {
		if _, err := tx.Exec(ctx, `
			INSERT INTO instance_endpoints
				(instance_id, node_key, key, type, host, port, url, exposure)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (instance_id, node_key, key) DO UPDATE
			SET type = EXCLUDED.type, host = EXCLUDED.host, port = EXCLUDED.port,
			    url = EXCLUDED.url, exposure = EXCLUDED.exposure,
			    allocated_port = COALESCE(instance_endpoints.allocated_port, EXCLUDED.port),
			    released_at = NULL
		`, instanceID, nodeKey, endpoint.Key, endpoint.Type, endpoint.Host, endpoint.Port, endpoint.URL, string(endpoint.Exposure)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func nullableActor(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

// finalizeInstance flips the aggregate status only after every node confirmed.
func (s *store) finalizeInstance(ctx context.Context, instanceID uuid.UUID, generation int64, success bool) error {
	var total, running, failed int
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'RUNNING'),
		       COUNT(*) FILTER (WHERE status = 'ERROR')
		FROM instance_nodes WHERE instance_id = $1
	`, instanceID).Scan(&total, &running, &failed); err != nil {
		return err
	}
	status := StatusProvisioning
	if success && total > 0 && running == total {
		status = StatusRunning
	} else if failed > 0 || !success {
		status = StatusError
	}
	query := `UPDATE instances SET status = $2 WHERE id = $1 AND generation = $3`
	args := []any{instanceID, string(status), generation}
	if status == StatusRunning {
		query = `UPDATE instances SET status = $2, started_at = COALESCE(started_at, NOW()) WHERE id = $1 AND generation = $3`
	}
	_, err := s.db.Pool.Exec(ctx, query, args...)
	return err
}

// releaseResources frees endpoint and network leases only after the worker
// confirmed provider destroy.
func (s *store) releaseResources(ctx context.Context, instanceID uuid.UUID) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := releaseResourcesTx(ctx, tx, instanceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// releaseResourcesTx frees endpoints/leases and expires the instance inside the
// caller's transaction. Callers must have confirmed that no node still owns a
// provider object.
func releaseResourcesTx(ctx context.Context, tx pgx.Tx, instanceID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE instance_endpoints SET released_at = NOW() WHERE instance_id = $1 AND released_at IS NULL
	`, instanceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE instance_network_leases SET released_at = NOW() WHERE instance_id = $1 AND released_at IS NULL
	`, instanceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE instance_nodes SET status = 'DESTROYED' WHERE instance_id = $1
	`, instanceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE instances SET status = 'EXPIRED', removed_at = NOW() WHERE id = $1 AND removed_at IS NULL
	`, instanceID); err != nil {
		return err
	}
	return nil
}

// expireDue marks overdue RUNNING instances as DESTROYING and queues cleanup.
func (s *store) expireDue(ctx context.Context, limit int) ([]Job, error) {
	// Heal a cleanup interrupted between the provider ACK and the bookkeeping
	// commit before selecting work, so a DESTROYING row cannot stay unreconciled.
	if _, err := s.repairStuckStagedDestroys(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, generation FROM instances
		WHERE removed_at IS NULL AND status IN ('RUNNING','ERROR','PROVISIONING','RESETTING')
		  AND expires_at IS NOT NULL AND expires_at <= NOW()
		ORDER BY expires_at LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	type due struct {
		id         uuid.UUID
		generation int64
	}
	items := []due{}
	for rows.Next() {
		var item due
		if err := rows.Scan(&item.id, &item.generation); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	rows.Close()
	jobs := make([]Job, 0, len(items))
	for _, item := range items {
		authorized := AuthorizedContext{System: true, Permission: "system.expire"}
		generation, operations, err := s.destroyInstance(ctx, item.id, authorized)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}
		ids := make([]uuid.UUID, 0, len(operations))
		for _, operation := range operations {
			ids = append(ids, operation.ID)
		}
		if len(ids) == 0 {
			if err := s.releaseResources(ctx, item.id); err != nil {
				return nil, err
			}
			continue
		}
		jobs = append(jobs, Job{InstanceID: item.id, Generation: generation, OperationIDs: ids, Reason: "expired"})
	}
	return jobs, nil
}

// pendingJobs reconstructs jobs for queued/running operations after a worker
// crash. Operations of an older generation are claimed and canceled by the
// worker, never executed.
func (s *store) pendingJobs(ctx context.Context, limit int) ([]Job, error) {
	if _, err := s.repairStuckStagedDestroys(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT instance_id, generation, id
		FROM instance_operations
		WHERE status IN ('QUEUED','RUNNING')
		ORDER BY started_at NULLS FIRST, id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		instanceID uuid.UUID
		generation int64
	}
	order := []key{}
	grouped := map[key][]uuid.UUID{}
	for rows.Next() {
		var instanceID uuid.UUID
		var generation int64
		var operationID uuid.UUID
		if err := rows.Scan(&instanceID, &generation, &operationID); err != nil {
			return nil, err
		}
		k := key{instanceID: instanceID, generation: generation}
		if _, exists := grouped[k]; !exists {
			order = append(order, k)
		}
		grouped[k] = append(grouped[k], operationID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(order))
	for _, k := range order {
		jobs = append(jobs, Job{InstanceID: k.instanceID, Generation: k.generation, OperationIDs: grouped[k], Reason: "reconcile"})
	}
	return jobs, nil
}

// completeStagedDestroy records one confirmed provider destroy in a single
// transaction: the node loses its provider id and becomes DESTROYED, the matching
// DESTROY operation becomes SUCCEEDED, and the instance is released only when
// every node has finished. A crash or a DB error at any statement rolls the whole
// thing back, so the operation stays claimable with its provider id intact and the
// next reconcile pass retries the idempotent destroy instead of leaking the
// reservation. It reports whether this completion released the instance.
func (s *store) completeStagedDestroy(ctx context.Context, instanceID uuid.UUID, nodeKey string, generation int64, operationID uuid.UUID) (bool, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var instanceGeneration int64
	var removedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT generation, removed_at FROM instances WHERE id = $1 FOR UPDATE
	`, instanceID).Scan(&instanceGeneration, &removedAt); err != nil {
		return false, err
	}
	if removedAt != nil {
		return false, tx.Commit(ctx)
	}
	if instanceGeneration != generation {
		// A newer generation superseded this cleanup; never touch it.
		return false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE instance_nodes
		SET status = 'DESTROYED', provider_resource_id = NULL
		WHERE instance_id = $1 AND key = $2 AND generation = $3
	`, instanceID, nodeKey, generation); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE instance_operations
		SET status = 'SUCCEEDED', error = NULL, finished_at = NOW()
		WHERE id = $1 AND operation = 'DESTROY' AND generation = $2 AND status IN ('QUEUED','RUNNING')
	`, operationID, generation); err != nil {
		return false, err
	}
	var remaining int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM instance_nodes
		WHERE instance_id = $1 AND COALESCE(provider_resource_id,'') <> ''
	`, instanceID).Scan(&remaining); err != nil {
		return false, err
	}
	released := remaining == 0
	if released {
		if err := releaseResourcesTx(ctx, tx, instanceID); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return released, nil
}

// repairStuckStagedDestroys re-queues the exact broken state left by a crash
// between "provider destroy confirmed" and "bookkeeping committed": a DESTROY
// operation marked SUCCEEDED while its own node is still DESTROYING with a
// provider id at the same generation.
//
// It is deliberately narrow: it never touches a CREATE/RESET operation, never
// touches an operation whose node is finished, and never touches another
// generation, so a successful apply or an unrelated provider id can never be
// replayed. Re-running it is a no-op.
func (s *store) repairStuckStagedDestroys(ctx context.Context) (int, error) {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE instance_operations o
		SET status = 'QUEUED', error = NULL, finished_at = NULL, started_at = NULL
		WHERE o.operation = 'DESTROY'
		  AND o.status = 'SUCCEEDED'
		  AND EXISTS (
		      SELECT 1 FROM instance_nodes n
		      JOIN instances i ON i.id = n.instance_id
		      WHERE n.instance_id = o.instance_id
		        AND n.key = o.node_key
		        AND n.generation = o.generation
		        AND i.generation = o.generation
		        AND i.removed_at IS NULL
		        AND n.status = 'DESTROYING'
		        AND COALESCE(n.provider_resource_id,'') <> ''
		  )
	`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// liveProviderNodeCount counts nodes that still own a provider object.
func (s *store) liveProviderNodeCount(ctx context.Context, instanceID uuid.UUID) (int, error) {
	var count int
	err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM instance_nodes
		WHERE instance_id = $1 AND COALESCE(provider_resource_id,'') <> ''
	`, instanceID).Scan(&count)
	return count, err
}

// stageExpiredResource persists a provider resource that was created after the
// instance deadline and queues its DESTROY, in one transaction, BEFORE any
// external destroy is attempted. If the worker crashes or the destroy call fails,
// the next reconcile pass finds the real provider id and a queued DESTROY op, so
// the object is never orphaned.
//
// It returns the queued operation id. The caller may attempt an inline destroy
// and mark that operation done, but the durable record exists either way.
func (s *store) stageExpiredResource(ctx context.Context, instanceID uuid.UUID, nodeKey string, generation int64, driver, providerID string) (uuid.UUID, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	labels := mustJSON(runtime.Labels(driver, runtime.Operation{InstanceID: instanceID, NodeKey: nodeKey, Generation: generation}))
	if _, err := tx.Exec(ctx, `
		UPDATE instance_nodes
		SET status = 'DESTROYING', provider_resource_id = NULLIF($4,''), generation = $3, provider_labels = $5
		WHERE instance_id = $1 AND key = $2
	`, instanceID, nodeKey, generation, providerID, labels); err != nil {
		return uuid.Nil, err
	}
	opID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO instance_operations
			(id, instance_id, node_key, operation, generation, idempotency_key, status,
			 requested_by, authorized_context)
		VALUES ($1,$2,$3,'DESTROY',$4,$5,'QUEUED',NULL,$6)
		ON CONFLICT (instance_id, node_key, generation, operation) DO NOTHING
	`, opID, instanceID, nodeKey, generation,
		fmt.Sprintf("%s:%s:%d:DESTROY", instanceID, nodeKey, generation),
		mustJSON(AuthorizedContext{System: true, Permission: "system.expire"}),
	); err != nil {
		return uuid.Nil, err
	}
	var queued uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM instance_operations
		WHERE instance_id=$1 AND node_key=$2 AND generation=$3 AND operation='DESTROY'
	`, instanceID, nodeKey, generation).Scan(&queued); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return queued, nil
}

// failOperation records a failed attempt. A failed CREATE/RESET becomes FAILED,
// which is the honest terminal state for that attempt. A failed DESTROY is put
// back to QUEUED instead: destroying is idempotent, the provider object still
// exists, and marking it FAILED would permanently strand the reservation (the
// reconcile pass only schedules QUEUED/RUNNING work).
func (s *store) failOperation(ctx context.Context, operation Operation, cause error) error {
	if operation.Kind == runtime.OperationDestroy {
		_, err := s.db.Pool.Exec(ctx, `
			UPDATE instance_operations
			SET status = 'QUEUED', error = NULLIF($2,''), finished_at = NULL, started_at = NULL
			WHERE id = $1 AND operation = 'DESTROY' AND status IN ('QUEUED','RUNNING')
		`, operation.ID, errorCode(cause))
		return err
	}
	return s.markOperation(ctx, operation.ID, "FAILED", errorCode(cause))
}
