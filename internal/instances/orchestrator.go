package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/integrations"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/runtime"
)

// BindingSecretAAD is the frozen AAD for deployment_bindings secret parameters.
// The board/deployment worker must seal with this exact helper.
func BindingSecretAAD(bindingID uuid.UUID) []byte {
	return []byte("actfb:deployment-binding-secrets:v1:" + bindingID.String())
}

// GeneratedSecretAAD is the frozen AAD for one generated instance value.
func GeneratedSecretAAD(instanceID uuid.UUID, key string) []byte {
	return []byte("actfb:instance-generated:" + instanceID.String() + ":" + key)
}

// Orchestrator is the trusted worker logic. It decrypts provider credentials
// only inside this process, rechecks authorization from live PostgreSQL state
// and persists provider-confirmed inventory.
type Orchestrator struct {
	store      *store
	registry   *runtime.DriverRegistry
	auth       authorization.Authorizer
	sealer     secrets.Sealer
	keyVersion uint32
	outbox     events.Outbox
	logger     *slog.Logger
}

// OrchestratorOption customizes the worker.
type OrchestratorOption func(*Orchestrator)

// WithOrchestratorLogger installs the worker logger.
func WithOrchestratorLogger(logger *slog.Logger) OrchestratorOption {
	return func(orchestrator *Orchestrator) {
		if logger != nil {
			orchestrator.logger = logger
		}
	}
}

// NewOrchestrator constructs the trusted worker logic.
func NewOrchestrator(db *postgres.DB, registry *runtime.DriverRegistry, auth authorization.Authorizer, sealer secrets.Sealer, keyVersion uint32, options ...OrchestratorOption) (*Orchestrator, error) {
	if db == nil || registry == nil {
		return nil, errors.New("instances: database and driver registry are required")
	}
	orchestrator := &Orchestrator{
		store: newStore(db, Options{}), registry: registry, auth: auth, sealer: sealer, keyVersion: keyVersion,
		outbox: events.NewOutbox(db), logger: slog.Default(),
	}
	for _, option := range options {
		if option != nil {
			option(orchestrator)
		}
	}
	return orchestrator, nil
}

// HandleJob applies one ids-only job. It is idempotent: a redelivered or
// post-crash job adopts the exact labelled provider resources, and a job whose
// generation is no longer current is canceled without creating new resources.
func (o *Orchestrator) HandleJob(ctx context.Context, job Job) error {
	if job.InstanceID == uuid.Nil || job.Generation < 1 || len(job.OperationIDs) == 0 || len(job.OperationIDs) > 64 {
		return validation("job", "invalid job")
	}
	instance, err := o.store.workerInstance(ctx, job.InstanceID)
	if err != nil {
		return err
	}
	operations, err := o.store.claimOperations(ctx, job)
	if err != nil {
		return err
	}
	if len(operations) == 0 {
		return nil
	}
	previousStatus := instance.Status

	// Re-read the authoritative deadline before any provider call. A job that is
	// already past its deadline must not provision anything.
	destroyOnly := true
	for _, operation := range operations {
		if operation.Kind != runtime.OperationDestroy {
			destroyOnly = false
			break
		}
	}
	if !destroyOnly {
		state, err := o.store.liveDeadline(ctx, instance.ID)
		if err != nil {
			return o.failAll(ctx, instance, operations, err)
		}
		if blocked := state.blocked(time.Now()); blocked != nil {
			return o.abortExpired(ctx, instance, operations, blocked)
		}
	}

	snapshot := instance.Snapshot
	binding, err := o.store.loadBindingForApply(ctx, snapshot.BindingID)
	if err != nil {
		return o.failAll(ctx, instance, operations, err)
	}
	if binding.VersionID != snapshot.TemplateVersionID || binding.Digest != snapshot.TemplateDigest {
		return o.failAll(ctx, instance, operations, instanceError(CodeTemplateInvalid, "queued snapshot does not match the immutable version", 500))
	}
	targets, err := o.store.loadIntegrationTargets(ctx, snapshotIntegrationIDs(snapshot))
	if err != nil {
		return o.failAll(ctx, instance, operations, err)
	}

	parameters := map[string]string{}
	for key, value := range binding.Parameters {
		parameters[key] = value
	}
	for key, value := range snapshot.Parameters {
		parameters[key] = value
	}
	secretParameters, err := o.openSecretParameters(binding)
	if err != nil {
		return o.failAll(ctx, instance, operations, err)
	}
	for key, value := range secretParameters {
		parameters[key] = value
	}
	generated, err := o.generatedValues(ctx, instance.ID, snapshot, operations)
	if err != nil {
		return o.failAll(ctx, instance, operations, err)
	}
	rendered, err := deployment.Render(ctx, deployment.RenderInput{
		Template: binding.Manifest, Files: binding.Files, Parameters: parameters, Generated: generated,
	})
	if err != nil {
		return o.failAll(ctx, instance, operations, instanceError(CodeTemplateInvalid, "template rendering failed", 500))
	}
	renderedByKey := map[string]deployment.RenderedNode{}
	for _, node := range rendered.Nodes {
		renderedByKey[node.Key] = node
	}
	providerIDs, err := o.store.nodeProviderIDs(ctx, instance.ID)
	if err != nil {
		return o.failAll(ctx, instance, operations, err)
	}

	destroyAll := true
	for _, operation := range operations {
		if operation.Kind != runtime.OperationDestroy {
			destroyAll = false
		}
		authorized, err := o.store.operationContext(ctx, operation.ID)
		if err != nil {
			return o.failAll(ctx, instance, operations, err)
		}
		if err := o.revalidateOperation(ctx, authorized, operation); err != nil {
			_ = o.store.markOperation(ctx, operation.ID, "FAILED", errorCode(err))
			return o.finishFailure(ctx, instance, operations, err)
		}
		snapshotNode, manifestNode, renderedNode, target, err := resolveNode(snapshot, binding, renderedByKey, targets, operation.NodeKey)
		if err != nil {
			_ = o.store.markOperation(ctx, operation.ID, "FAILED", errorCode(err))
			return o.finishFailure(ctx, instance, operations, err)
		}
		if err := o.applyOperation(ctx, instance, operation, snapshotNode, manifestNode, renderedNode, target, providerIDs[operation.NodeKey]); err != nil {
			var expired *expiredDuringApplyError
			if errors.As(err, &expired) {
				// The provider resource was created after the deadline and its
				// confirmed destroy already ran. Release resources and report the
				// environment as expired; never emit started/created events.
				_ = o.store.markOperation(ctx, operation.ID, "CANCELED", string(CodeExpired))
				return o.finishExpiredDuringApply(ctx, instance)
			}
			_ = o.store.markOperation(ctx, operation.ID, "FAILED", errorCode(err))
			return o.finishFailure(ctx, instance, operations, err)
		}
		if err := o.store.markOperation(ctx, operation.ID, "SUCCEEDED", ""); err != nil {
			return err
		}
	}

	if destroyAll {
		if err := o.store.releaseResources(ctx, instance.ID); err != nil {
			return err
		}
		o.store.syncPentestEnvironmentStatus(ctx, instance.ID, StatusExpired)
		o.enqueueEvent(ctx, instance, events.EventInstanceExpired, "EXPIRED")
		return nil
	}
	if err := o.store.finalizeInstance(ctx, instance.ID, instance.Generation, true); err != nil {
		return err
	}
	o.store.syncPentestEnvironmentStatus(ctx, instance.ID, StatusRunning)
	if previousStatus != StatusRunning {
		eventType := events.EventInstanceStarted
		if previousStatus == StatusProvisioning {
			eventType = events.EventInstanceCreated
		}
		o.enqueueEvent(ctx, instance, eventType, "RUNNING")
	}
	return nil
}

func (o *Orchestrator) applyOperation(ctx context.Context, instance *workerInstance, operation Operation, snapshotNode SnapshotNode, node deployment.Node, rendered deployment.RenderedNode, target integrationTarget, providerID string) error {
	driver, ok := o.registry.Get(target.Driver)
	if !ok {
		return &runtime.CapabilityError{Driver: target.Driver, Capability: "driver"}
	}
	claim, err := o.store.leaseClaim(ctx, instance.ID, target.ID)
	if err != nil {
		return err
	}
	allocation, err := o.store.allocation(ctx, instance.ID, target.ID, operation.NodeKey, claim)
	if err != nil {
		return err
	}
	settings, err := runtime.EncodeTargetSettings(runtime.TargetSettings{
		Options: target.Settings, Allocation: allocation,
		Material: &runtime.Material{Env: rendered.Env, Files: materialFiles(rendered.Files), Bootstrap: rendered.Bootstrap},
	})
	if err != nil {
		return err
	}
	targetRuntime := runtime.Target{
		IntegrationUUID: target.ID, Driver: target.Driver, Kind: snapshotNode.Kind,
		BaseURL: target.BaseURL, Credentials: nil, Settings: settings,
	}
	if target.Publication != nil {
		targetRuntime.Publication = *target.Publication
	}
	// Provider credentials are opened only here, in the trusted worker.
	credentials, err := o.openCredentials(ctx, target.ID)
	if err != nil {
		return err
	}
	targetRuntime.Credentials = credentials
	node.Resources = rendered.Resources
	runtimeOp := runtime.Operation{
		ID: operation.ID, InstanceID: instance.ID, NodeKey: operation.NodeKey,
		Generation: operation.Generation, Kind: operation.Kind,
	}
	if operation.Kind == runtime.OperationDestroy {
		if strings.TrimSpace(providerID) == "" {
			return nil
		}
		return driver.Destroy(ctx, targetRuntime, providerID, runtimeOp)
	}
	resource, err := driver.Apply(ctx, targetRuntime, node, runtimeOp)
	if err != nil {
		return err
	}
	if resource.State != runtime.NodeStateRunning {
		return fmt.Errorf("%w: provider did not confirm RUNNING", runtime.ErrProviderUnavailable)
	}
	// A provider call can outlive the deadline. Re-check before advertising
	// RUNNING; if it passed, destroy what was just created instead of leaking an
	// unreachable instance and its published port.
	if state, err := o.store.liveDeadline(ctx, instance.ID); err != nil {
		return err
	} else if blocked := state.blocked(time.Now()); blocked != nil {
		if strings.TrimSpace(resource.ProviderID) == "" {
			// The driver reported RUNNING without an id. Nothing can be adopted
			// or destroyed, so never advertise success and never free ports as if
			// cleanup had been confirmed.
			return fmt.Errorf("%w: provider reported RUNNING without a resource id", runtime.ErrProviderUnavailable)
		}
		// Persist the id and queue the DESTROY before touching the provider, so a
		// failed or interrupted destroy is retried with the real id by the next
		// reconcile pass instead of orphaning a labelled object.
		cleanupOp, stageErr := o.store.stageExpiredResource(ctx, instance.ID, operation.NodeKey, operation.Generation, target.Driver, resource.ProviderID)
		if stageErr != nil {
			return stageErr
		}
		if destroyErr := driver.Destroy(ctx, targetRuntime, resource.ProviderID, runtimeOp); destroyErr != nil {
			// Leave the staged DESTROY op QUEUED (not FAILED) so the next
			// reconcile pass claims it and retries with the persisted id. The
			// instance is not removed and no lease is freed until a destroy is
			// confirmed.
			return destroyErr
		}
		_ = o.store.markOperation(ctx, cleanupOp, "SUCCEEDED", "")
		if err := o.store.confirmStagedDestroy(ctx, instance.ID, operation.NodeKey); err != nil {
			return err
		}
		return &expiredDuringApplyError{cause: blocked}
	}
	return o.store.nodeResult(ctx, instance.ID, operation.NodeKey, operation.Generation, target.Driver, resource)
}

func resolveNode(snapshot DesiredSnapshot, binding *bindingData, rendered map[string]deployment.RenderedNode, targets map[uuid.UUID]integrationTarget, nodeKey string) (SnapshotNode, deployment.Node, deployment.RenderedNode, integrationTarget, error) {
	var snapshotNode SnapshotNode
	var manifestNode deployment.Node
	foundSnapshot := false
	for _, node := range snapshot.Nodes {
		if node.Key == nodeKey {
			snapshotNode = node
			foundSnapshot = true
			break
		}
	}
	if !foundSnapshot {
		return snapshotNode, manifestNode, deployment.RenderedNode{}, integrationTarget{}, instanceError(CodeTemplateInvalid, "queued node is missing", 500)
	}
	foundManifest := false
	for _, node := range binding.Manifest.Nodes {
		if node.Key == nodeKey {
			manifestNode = node
			foundManifest = true
			break
		}
	}
	if !foundManifest {
		return snapshotNode, manifestNode, deployment.RenderedNode{}, integrationTarget{}, instanceError(CodeTemplateInvalid, "manifest node is missing", 500)
	}
	renderedNode, ok := rendered[nodeKey]
	if !ok {
		return snapshotNode, manifestNode, renderedNode, integrationTarget{}, instanceError(CodeTemplateInvalid, "rendered node is missing", 500)
	}
	target, ok := targets[snapshotNode.IntegrationUUID]
	if !ok {
		return snapshotNode, manifestNode, renderedNode, target, instanceError(CodeTemplateInvalid, "integration target is missing", 500)
	}
	return snapshotNode, manifestNode, renderedNode, target, nil
}

func materialFiles(files []deployment.RenderedFile) []runtime.MaterialFile {
	result := make([]runtime.MaterialFile, 0, len(files))
	for _, file := range files {
		result = append(result, runtime.MaterialFile{Target: file.Target, Mode: file.Mode, Content: file.Content})
	}
	return result
}

func (o *Orchestrator) openCredentials(ctx context.Context, integrationID uuid.UUID) (map[string]string, error) {
	var ciphertext []byte
	var keyVersion *int
	err := o.store.db.Pool.QueryRow(ctx, `
		SELECT credentials_ciphertext, credentials_key_version FROM integrations WHERE id = $1
	`, integrationID).Scan(&ciphertext, &keyVersion)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 {
		return map[string]string{}, nil
	}
	if o.sealer == nil {
		return nil, instanceError(CodeProviderUnavailable, "provider credentials cannot be decrypted", 503)
	}
	// AAD is frozen by the integrations package and shared with the AD worker.
	plaintext, err := o.sealer.Open(ciphertext, integrations.CredentialAAD(integrationID))
	if err != nil {
		return nil, instanceError(CodeProviderUnavailable, "provider credentials cannot be decrypted", 503)
	}
	credentials := map[string]string{}
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return nil, instanceError(CodeProviderUnavailable, "provider credentials are invalid", 503)
	}
	return credentials, nil
}

func (o *Orchestrator) openSecretParameters(binding *bindingData) (map[string]string, error) {
	parameters := map[string]string{}
	if len(binding.SecretCiphertext) == 0 {
		return parameters, nil
	}
	if o.sealer == nil {
		return nil, instanceError(CodeProviderUnavailable, "binding secrets cannot be decrypted", 503)
	}
	plaintext, err := o.sealer.Open(binding.SecretCiphertext, BindingSecretAAD(binding.ID))
	if err != nil {
		return nil, instanceError(CodeProviderUnavailable, "binding secrets cannot be decrypted", 503)
	}
	if err := json.Unmarshal(plaintext, &parameters); err != nil {
		return nil, instanceError(CodeProviderUnavailable, "binding secrets are invalid", 503)
	}
	return parameters, nil
}

func (o *Orchestrator) generatedValues(ctx context.Context, instanceID uuid.UUID, snapshot DesiredSnapshot, operations []Operation) (map[string]string, error) {
	stored, err := o.store.generatedSecrets(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	rotating := false
	for _, operation := range operations {
		if operation.Kind == runtime.OperationReset {
			rotating = true
		}
	}
	for _, ref := range snapshot.Generated {
		existing := stored["_instance"][ref.Key]
		if len(existing.Ciphertext) > 0 && !(rotating && ref.RotateOnReset) {
			if o.sealer == nil {
				return nil, instanceError(CodeProviderUnavailable, "generated values cannot be decrypted", 503)
			}
			plaintext, err := o.sealer.Open(existing.Ciphertext, GeneratedSecretAAD(instanceID, ref.Key))
			if err != nil {
				return nil, instanceError(CodeProviderUnavailable, "generated value cannot be decrypted", 503)
			}
			values[ref.Key] = string(plaintext)
			continue
		}
		value, err := randomValue(ref.Kind, ref.Length)
		if err != nil {
			return nil, err
		}
		if o.sealer == nil {
			return nil, instanceError(CodeProviderUnavailable, "generated values cannot be sealed", 503)
		}
		ciphertext, err := o.sealer.Seal([]byte(value), GeneratedSecretAAD(instanceID, ref.Key))
		if err != nil {
			return nil, err
		}
		if err := o.store.saveGenerated(ctx, instanceID, "_instance", ref.Key, ciphertext, o.keyVersion); err != nil {
			return nil, err
		}
		values[ref.Key] = value
	}
	return values, nil
}

// revalidateOperation is the worker's authoritative check. NATS payloads are
// never trusted for identity, roles or scope.
func (o *Orchestrator) revalidateOperation(ctx context.Context, authorized AuthorizedContext, operation Operation) error {
	if authorized.System {
		return nil
	}
	if authorized.ActorUserID == uuid.Nil || o.auth == nil {
		return forbidden()
	}
	permission := authorization.Permission(authorized.Permission)
	switch operation.Kind {
	case runtime.OperationCreate:
		if permission != authorization.PermissionInstanceStartOwn && permission != authorization.PermissionModuleInstancesAdmin {
			permission = authorization.PermissionInstanceStartOwn
		}
	case runtime.OperationReset, runtime.OperationDestroy:
		if permission != authorization.PermissionInstanceManageOwn && permission != authorization.PermissionModuleInstancesAdmin {
			permission = authorization.PermissionInstanceManageOwn
		}
	}
	scope := authorization.Scope{BoardID: authorized.BoardID, ModuleID: &authorized.ModuleID}
	decision, err := o.auth.Authorize(ctx, authorization.Principal{UserID: authorized.ActorUserID}, permission, scope)
	if err != nil {
		return instanceError(CodeInternal, "authorization failed", 500)
	}
	if !decision.Allowed {
		// Staff with module.instances.admin may manage any instance; the
		// participation-only permission itself is never escalated.
		adminDecision, adminErr := o.auth.Authorize(ctx, authorization.Principal{UserID: authorized.ActorUserID}, authorization.PermissionModuleInstancesAdmin, scope)
		if adminErr != nil || !adminDecision.Allowed {
			return forbidden()
		}
	}
	if authorized.SubjectType == "TEAM" {
		member, err := o.store.isTeamMember(ctx, authorized.BoardID, authorized.SubjectID, authorized.ActorUserID)
		if err != nil {
			return err
		}
		if !member {
			return forbidden()
		}
	} else if authorized.SubjectID != authorized.ActorUserID && authorized.Permission == string(authorization.PermissionInstanceStartOwn) {
		return forbidden()
	}
	allowed, err := o.store.subjectInModuleAudience(ctx, authorized.BoardID, authorized.ModuleID, authorized.SubjectType, authorized.SubjectID)
	if err != nil {
		return err
	}
	if !allowed {
		return forbidden()
	}
	if operation.Kind != runtime.OperationDestroy && authorized.EffectiveUntil != nil && !time.Now().Before(*authorized.EffectiveUntil) {
		return conflict(CodeExpired, "effective window has ended")
	}
	return nil
}

func (o *Orchestrator) failAll(ctx context.Context, instance *workerInstance, operations []Operation, cause error) error {
	for _, operation := range operations {
		_ = o.store.markOperation(ctx, operation.ID, "FAILED", errorCode(cause))
	}
	return o.finishFailure(ctx, instance, operations, cause)
}

func (o *Orchestrator) finishFailure(ctx context.Context, instance *workerInstance, operations []Operation, cause error) error {
	_ = o.store.finalizeInstance(ctx, instance.ID, instance.Generation, false)
	o.store.syncPentestEnvironmentStatus(ctx, instance.ID, StatusError)
	return cause
}

func errorCode(err error) string {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return string(apiErr.Code)
	}
	if errors.Is(err, runtime.ErrUnsupported) {
		return string(CodeProviderUnavailable)
	}
	return string(CodeProvisionFailed)
}

func snapshotIntegrationIDs(snapshot DesiredSnapshot) []uuid.UUID {
	seen := map[uuid.UUID]struct{}{}
	ids := make([]uuid.UUID, 0)
	for _, node := range snapshot.Nodes {
		if _, ok := seen[node.IntegrationUUID]; ok {
			continue
		}
		seen[node.IntegrationUUID] = struct{}{}
		ids = append(ids, node.IntegrationUUID)
	}
	return ids
}

func (o *Orchestrator) enqueueEvent(ctx context.Context, instance *workerInstance, eventType, status string) {
	if o.outbox == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"instanceUuid": instance.ID,
		"boardUuid":    instance.BoardID,
		"moduleUuid":   instance.ModuleID,
		"scope":        instance.Scope,
		"subjectType":  instance.SubjectType,
		"subjectUuid":  instance.SubjectID,
		"status":       status,
	})
	subjectID := instance.SubjectID
	event := events.Event{
		ID: uuid.New(), Type: eventType, OccurredAt: time.Now().UTC(),
		BoardID: &instance.BoardID, ModuleID: &instance.ModuleID,
		SubjectType: instance.SubjectType, SubjectID: &subjectID,
		Payload: payload,
	}
	if err := o.outbox.Enqueue(ctx, o.store.db.Pool, event); err != nil && o.logger != nil {
		o.logger.Warn("instance event enqueue failed", "instanceId", instance.ID.String(), "error", err.Error())
	}
}

// ReapExpired queues cleanup for every overdue instance.
func (o *Orchestrator) ReapExpired(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	jobs, err := o.store.expireDue(ctx, limit)
	if err != nil {
		return 0, err
	}
	return len(jobs), nil
}

// ExpiredJobs returns the jobs produced by expiry so the worker can process
// them without an external publisher.
func (o *Orchestrator) ExpiredJobs(ctx context.Context, limit int) ([]Job, error) {
	return o.store.expireDue(ctx, limit)
}

// PendingJobs returns jobs reconstructed from queued/running operations after a
// previous worker was killed. It performs no provider calls.
func (o *Orchestrator) PendingJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return o.store.pendingJobs(ctx, limit)
}

// expiredDuringApplyError marks the narrow case where a provider resource was
// created after the instance deadline and has already been destroyed (or staged
// for destroy). It is not a provisioning failure: the environment is expired.
type expiredDuringApplyError struct{ cause error }

func (e *expiredDuringApplyError) Error() string {
	if e.cause == nil {
		return "instance expired during provider apply"
	}
	return e.cause.Error()
}

func (e *expiredDuringApplyError) Unwrap() error { return e.cause }

// finishExpiredDuringApply releases the instance after a confirmed cleanup and
// emits only the expired event. Any still-queued DESTROY means the inline
// destroy did not confirm, so resources stay held for the reconcile retry.
func (o *Orchestrator) finishExpiredDuringApply(ctx context.Context, instance *workerInstance) error {
	var pending int
	if err := o.store.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM instance_operations
		WHERE instance_id = $1 AND operation = 'DESTROY' AND status IN ('QUEUED','RUNNING')
	`, instance.ID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return conflict(CodeExpired, "instance expired during provider apply; cleanup is pending")
	}
	// Another node of the same instance may still own a live provider object
	// (a multi-node template where the deadline passed between nodes). Queue its
	// destroy instead of releasing ports under a running workload.
	liveNodes, err := o.store.liveProviderNodeCount(ctx, instance.ID)
	if err != nil {
		return err
	}
	if liveNodes > 0 {
		if _, _, err := o.store.destroyInstance(ctx, instance.ID, AuthorizedContext{System: true, Permission: "system.expire"}); err != nil {
			return err
		}
		return conflict(CodeExpired, "instance expired during provider apply; cleanup is pending")
	}
	if err := o.store.releaseResources(ctx, instance.ID); err != nil {
		return err
	}
	o.store.syncPentestEnvironmentStatus(ctx, instance.ID, StatusExpired)
	o.enqueueEvent(ctx, instance, events.EventInstanceExpired, "EXPIRED")
	return nil
}
