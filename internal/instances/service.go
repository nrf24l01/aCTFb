package instances

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/files"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/runtime"
)

// JobPublisher ships ids-only jobs to the trusted worker.
type JobPublisher interface {
	PublishJob(ctx context.Context, job Job) error
}

// DraftVerifier is the caller-scoped files service contract.
type DraftVerifier interface {
	VerifyDraft(ctx context.Context, in files.VerifyDraftInput) (*files.VerifiedDraft, error)
}

// Options are the frozen defaults for TTL and extension policy.
type Options struct {
	DefaultTTL             time.Duration
	MaxTTL                 time.Duration
	ExtensionTTL           time.Duration
	MaxExtensions          int
	MaxInstancesPerSubject int
}

// Option customizes the service.
type Option func(*Service)

// WithAccessPolicy installs the audience/board policy for start checks.
func WithAccessPolicy(policy access.Policy) Option {
	return func(service *Service) { service.access = policy }
}

// WithPublisher installs the worker job publisher.
func WithPublisher(publisher JobPublisher) Option {
	return func(service *Service) { service.publisher = publisher }
}

// WithOptions overrides the TTL/extension defaults.
func WithOptions(options Options) Option {
	return func(service *Service) {
		if options.DefaultTTL > 0 {
			service.options.DefaultTTL = options.DefaultTTL
		}
		if options.MaxTTL > 0 {
			service.options.MaxTTL = options.MaxTTL
		}
		if options.ExtensionTTL > 0 {
			service.options.ExtensionTTL = options.ExtensionTTL
		}
		if options.MaxExtensions > 0 {
			service.options.MaxExtensions = options.MaxExtensions
		}
		if options.MaxInstancesPerSubject > 0 {
			service.options.MaxInstancesPerSubject = options.MaxInstancesPerSubject
		}
	}
}

// WithDraftVerifier installs the caller-scoped files verifier.
func WithDraftVerifier(verifier DraftVerifier) Option {
	return func(service *Service) { service.verifier = verifier }
}

// WithLogger installs a structured logger.
func WithLogger(logger *slog.Logger) Option {
	return func(service *Service) {
		if logger != nil {
			service.logger = logger
		}
	}
}

// Service is the instance lifecycle business service.
type Service struct {
	store      *store
	auth       authorization.Authorizer
	access     access.Policy
	registry   *runtime.DriverRegistry
	sealer     secrets.Sealer
	keyVersion uint32
	publisher  JobPublisher
	verifier   DraftVerifier
	logger     *slog.Logger
	options    Options
}

// NewService constructs the instance service.
func NewService(db *postgres.DB, auth authorization.Authorizer, registry *runtime.DriverRegistry, sealer secrets.Sealer, keyVersion uint32, options ...Option) (*Service, error) {
	if db == nil {
		return nil, errors.New("instances: database is required")
	}
	service := &Service{
		store: dbStore(db), auth: auth, registry: registry, sealer: sealer, keyVersion: keyVersion,
		logger: slog.Default(),
		options: Options{
			DefaultTTL:             time.Hour,
			MaxTTL:                 7 * 24 * time.Hour,
			ExtensionTTL:           30 * time.Minute,
			MaxExtensions:          3,
			MaxInstancesPerSubject: 2,
		},
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func dbStore(db *postgres.DB) *store { return &store{db: db} }

// StartTask creates the immutable desired snapshot and queues provisioning.
func (s *Service) StartTask(ctx context.Context, actor authorization.Principal, boardID, moduleID, taskID uuid.UUID, idempotencyKey string) (*Instance, error) {
	return s.start(ctx, actor, ScopeTask, boardID, moduleID, &taskID, idempotencyKey)
}

// GetTask returns the caller's current task instance.
func (s *Service) GetTask(ctx context.Context, actor authorization.Principal, boardID, moduleID, taskID uuid.UUID) (*Instance, error) {
	if err := s.authorizeParticipant(ctx, actor, authorization.PermissionInstanceStartOwn, boardID, moduleID); err != nil {
		return nil, err
	}
	source, err := s.store.loadSource(ctx, ScopeTask, boardID, moduleID, &taskID)
	if err != nil {
		return nil, err
	}
	subjectType, subjectID, _, err := s.store.resolveSubject(ctx, boardID, source.ParticipationMode, actor.UserID)
	if err != nil {
		return nil, err
	}
	return s.store.findLive(ctx, boardID, moduleID, ScopeTask, taskID, subjectType, subjectID)
}

// ActionTask applies RESET/DESTROY/EXTEND to the caller's task instance.
func (s *Service) ActionTask(ctx context.Context, actor authorization.Principal, boardID, moduleID, taskID uuid.UUID, action Action, idempotencyKey string) (*Instance, error) {
	if err := s.authorizeParticipant(ctx, actor, authorization.PermissionInstanceManageOwn, boardID, moduleID); err != nil {
		return nil, err
	}
	source, err := s.store.loadSource(ctx, ScopeTask, boardID, moduleID, &taskID)
	if err != nil {
		return nil, err
	}
	subjectType, subjectID, _, err := s.store.resolveSubject(ctx, boardID, source.ParticipationMode, actor.UserID)
	if err != nil {
		return nil, err
	}
	return s.applyAction(ctx, actor, ScopeTask, boardID, moduleID, taskID, subjectType, subjectID, action, idempotencyKey, source, nil)
}

// Get returns one instance visible to its owner, a team member, or staff.
func (s *Service) Get(ctx context.Context, actor authorization.Principal, instanceID uuid.UUID) (*Instance, error) {
	instance, err := s.store.getInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeRead(ctx, actor, instance); err != nil {
		return nil, err
	}
	return instance, nil
}

// AdminList lists every instance (admin lane).
func (s *Service) AdminList(ctx context.Context, actor authorization.Principal, boardID, moduleID *uuid.UUID, limit, offset int) ([]Instance, int64, error) {
	if err := s.authorizeGlobal(ctx, actor, authorization.PermissionModuleInstancesAdmin, boardID, moduleID); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	items, err := s.store.listInstances(ctx, boardID, moduleID, nil, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.store.countInstances(ctx, boardID, moduleID)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// AdminAction applies an action as staff.
func (s *Service) AdminAction(ctx context.Context, actor authorization.Principal, instanceID uuid.UUID, action Action, idempotencyKey string) (*Instance, error) {
	instance, err := s.store.getInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if instance.Scope == ScopePentest && action != ActionReset && action != ActionDestroy {
		return nil, validation("action", "only RESET and DESTROY are supported for a pentest environment")
	}
	if err := s.authorizeGlobal(ctx, actor, authorization.PermissionModuleInstancesAdmin, &instance.BoardID, &instance.ModuleID); err != nil {
		return nil, err
	}
	source, err := s.store.loadSource(ctx, instance.Scope, instance.BoardID, instance.ModuleID, scopeTaskID(instance))
	if err != nil {
		return nil, err
	}
	return s.applyAction(ctx, actor, instance.Scope, instance.BoardID, instance.ModuleID, instance.ScopeUUID,
		instance.SubjectType, instance.SubjectID, action, idempotencyKey, source, &instance.UUID)
}

func scopeTaskID(instance *Instance) *uuid.UUID {
	if instance.Scope == ScopeTask {
		id := instance.ScopeUUID
		return &id
	}
	return nil
}

// start is the shared immutable snapshot + queue path.
func (s *Service) start(ctx context.Context, actor authorization.Principal, scope Scope, boardID, moduleID uuid.UUID, taskID *uuid.UUID, idempotencyKey string) (*Instance, error) {
	if actor.UserID == uuid.Nil {
		return nil, unauthorized()
	}
	if err := s.authorizeParticipant(ctx, actor, authorization.PermissionInstanceStartOwn, boardID, moduleID); err != nil {
		return nil, err
	}
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 128 {
		return nil, validation("Idempotency-Key", "Idempotency-Key must be between 16 and 128 characters")
	}
	source, err := s.store.loadSource(ctx, scope, boardID, moduleID, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureActive(source); err != nil {
		return nil, err
	}
	if scope == ScopeTask && !source.TaskInstanceEnabled {
		return nil, instanceError(CodeTaskInstanceUnsupported, "task does not support instances", 403)
	}
	if scope == ScopeTask && source.TaskBindingID == nil {
		return nil, instanceError(CodeTemplateNotFound, "task has no deployment template binding", 404)
	}
	bindingID := source.TaskBindingID
	if scope == ScopePentest {
		bindingID = source.PentestBindingID
	}
	if bindingID == nil {
		return nil, instanceError(CodeTemplateNotFound, "module has no deployment binding", 404)
	}
	subjectType, subjectID, _, err := s.store.resolveSubject(ctx, boardID, source.ParticipationMode, actor.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureAudience(ctx, boardID, moduleID, subjectType, subjectID); err != nil {
		return nil, err
	}
	binding, err := s.store.loadBinding(ctx, *bindingID, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	expiresAt, ttl := s.expiryFor(source)
	if scope == ScopePentest {
		// Pentest has no independent TTL: the effective module/board end is
		// authoritative.
		expiresAt = earliest(source.BoardEndsAt, source.ModuleEndsAt)
		ttl = 0
	}
	snapshot := DesiredSnapshot{
		Version:           1,
		TemplateVersionID: binding.VersionID,
		TemplateDigest:    binding.Digest,
		BindingID:         binding.ID,
		Parameters:        binding.Parameters,
		Generated:         generatedRefs(binding.Manifest),
		TTLSeconds:        int64(ttl / time.Second),
	}
	for _, node := range binding.Manifest.Nodes {
		integrationID, ok := binding.Targets[node.Kind]
		if !ok {
			return nil, instanceError(CodeTemplateInvalid, "binding has no target for node kind", 422)
		}
		snapshot.Nodes = append(snapshot.Nodes, SnapshotNode{Key: node.Key, Kind: node.Kind, Source: node.Source, IntegrationUUID: integrationID})
	}
	for _, parameter := range binding.Manifest.Parameters {
		if parameter.Secret {
			snapshot.SecretParameterKeys = append(snapshot.SecretParameterKeys, parameter.Key)
		}
	}
	requestHash := hashRequest(scope, boardID, moduleID, taskID, binding.VersionID, binding.Parameters)
	snapshot.RequestHash = requestHash
	if existing, existingID, err := s.store.findByIdempotencyKey(ctx, idempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.Snapshot["requestHash"] != requestHash {
			return nil, conflict(CodeIdempotencyReused, "Idempotency-Key was reused with a different request")
		}
		return s.store.getInstance(ctx, existingID)
	}
	integrationIDs := make([]uuid.UUID, 0, len(binding.Targets))
	for _, id := range binding.Targets {
		integrationIDs = append(integrationIDs, id)
	}
	targets, err := s.store.loadIntegrationTargets(ctx, integrationIDs)
	if err != nil {
		return nil, err
	}
	usedVLANs, err := s.store.usedAllocationKeys(ctx, integrationIDs)
	if err != nil {
		return nil, err
	}
	instanceID := uuid.New()
	leases := make([]leasePlan, 0, len(targets))
	claims := map[uuid.UUID]runtime.NetworkClaim{}
	for _, integrationID := range integrationIDs {
		claim, err := buildClaim(targets[integrationID], instanceID, usedVLANs[integrationID])
		if err != nil {
			return nil, err
		}
		claims[integrationID] = claim
		leases = append(leases, leasePlan{IntegrationID: integrationID, Kind: targets[integrationID].Kind, Claim: claim})
	}
	endpointPlans := make([]endpointPlan, 0)
	for _, node := range binding.Manifest.Nodes {
		target := targets[binding.Targets[node.Kind]]
		for _, endpoint := range node.Endpoints {
			plan := endpointPlan{NodeKey: node.Key, Key: endpoint.Key, Type: endpoint.Protocol, Exposure: endpoint.Exposure, TargetPort: endpoint.TargetPort}
			if endpoint.Exposure == deployment.ExposurePublic {
				if target.Publication == nil {
					return nil, instanceError(CodeTemplateInvalid, "integration has no publication policy", 422)
				}
				plan.Host = target.Publication.PublicTCPHost
				plan.PortFrom = target.Publication.PublicPortFrom
				plan.PortTo = target.Publication.PublicPortTo
			}
			endpointPlans = append(endpointPlans, plan)
		}
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	authorized := AuthorizedContext{
		ActorUserID: actor.UserID, BoardID: boardID, ModuleID: moduleID, Scope: scope,
		SubjectType: subjectType, SubjectID: subjectID, Permission: string(authorization.PermissionInstanceStartOwn),
	}
	if scope == ScopeTask {
		authorized.ScopeUUID = *taskID
	} else {
		authorized.ScopeUUID = moduleID
	}
	authorized.EffectiveUntil = earliest(source.BoardEndsAt, source.ModuleEndsAt)
	instanceUUID, operations, err := s.store.createInstance(ctx, createParams{
		InstanceID: instanceID, BoardID: boardID, ModuleID: moduleID, Scope: scope, ScopeUUID: authorized.ScopeUUID,
		SubjectType: subjectType, SubjectID: subjectID, IdempotencyKey: idempotencyKey,
		BindingID: &binding.ID, VersionID: &binding.VersionID, Snapshot: snapshotJSON,
		Authorized: authorized, Nodes: snapshot.Nodes, ExpiresAt: expiresAt,
		EndpointPlans: endpointPlans, Leases: leases,
	})
	if err != nil {
		if isUniqueViolation(err) {
			if existing, _, findErr := s.store.findByIdempotencyKey(ctx, idempotencyKey); findErr == nil && existing != nil {
				return s.store.getInstance(ctx, existing.ID)
			}
			return nil, conflict(CodeAlreadyExists, "an instance already exists for this subject")
		}
		return nil, err
	}
	operationIDs := make([]uuid.UUID, 0, len(operations))
	for _, operation := range operations {
		operationIDs = append(operationIDs, operation.ID)
	}
	s.enqueue(ctx, Job{InstanceID: instanceUUID, Generation: 1, OperationIDs: operationIDs})
	if scope == ScopePentest {
		if err := s.store.upsertPentestEnvironment(ctx, moduleID, subjectType, subjectID, instanceUUID, expiresAt); err != nil {
			return nil, err
		}
	}
	return s.store.getInstance(ctx, instanceUUID)
}

func (s *Service) applyAction(ctx context.Context, actor authorization.Principal, scope Scope, boardID, moduleID, scopeUUID uuid.UUID, subjectType string, subjectID uuid.UUID, action Action, idempotencyKey string, source *sourceContext, instanceID *uuid.UUID) (*Instance, error) {
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 128 {
		return nil, validation("Idempotency-Key", "Idempotency-Key must be between 16 and 128 characters")
	}
	var target *Instance
	var err error
	if instanceID != nil {
		target, err = s.store.getInstance(ctx, *instanceID)
	} else {
		target, err = s.store.findLive(ctx, boardID, moduleID, scope, scopeUUID, subjectType, subjectID)
	}
	if err != nil {
		return nil, err
	}
	switch action {
	case ActionExtend:
		if target.Status != StatusRunning {
			return nil, conflict(CodeActionNotAllowed, "only a running instance can be extended")
		}
		if target.ExpiresAt != nil && !target.ExpiresAt.After(time.Now()) {
			return nil, conflict(CodeExpired, "instance has expired")
		}
		if err := s.store.extendInstance(ctx, target.UUID, s.options.ExtensionTTL, s.options.MaxExtensions, s.options.MaxTtl()); err != nil {
			return nil, err
		}
		return s.store.getInstance(ctx, target.UUID)
	case ActionReset:
		if target.Status == StatusDestroying || target.Status == StatusExpired || target.RemovedAt != nil {
			return nil, conflict(CodeActionNotAllowed, "instance cannot be reset")
		}
		if err := s.ensureActive(source); err != nil {
			return nil, conflict(CodeExpired, "instance has expired")
		}
		if scope == ScopePentest && !source.PentestResetEnabled {
			return nil, instanceError(CodeActionNotAllowed, "reset is disabled for this pentest", 422)
		}
		authorized := AuthorizedContext{ActorUserID: actor.UserID, BoardID: boardID, ModuleID: moduleID, Scope: scope,
			ScopeUUID: target.ScopeUUID, SubjectType: subjectType, SubjectID: subjectID,
			Permission: string(authorization.PermissionInstanceManageOwn)}
		var generation int64
		var operations []Operation
		if scope == ScopePentest {
			generation, operations, err = s.store.resetPentestInstanceAtomic(ctx, moduleID, subjectType, subjectID,
				target.UUID, source.PentestMaxResets, authorized)
		} else {
			generation, operations, err = s.store.resetInstance(ctx, target.UUID, authorized)
		}
		if err != nil {
			return nil, err
		}
		s.enqueue(ctx, Job{InstanceID: target.UUID, Generation: generation, OperationIDs: operationIDs(operations)})
		return s.store.getInstance(ctx, target.UUID)
	case ActionDestroy:
		if target.RemovedAt != nil {
			return s.store.getInstance(ctx, target.UUID)
		}
		authorized := AuthorizedContext{ActorUserID: actor.UserID, BoardID: boardID, ModuleID: moduleID, Scope: scope,
			ScopeUUID: target.ScopeUUID, SubjectType: subjectType, SubjectID: subjectID,
			Permission: string(authorization.PermissionInstanceManageOwn)}
		generation, operations, err := s.store.destroyInstance(ctx, target.UUID, authorized)
		if err != nil {
			return nil, err
		}
		s.enqueue(ctx, Job{InstanceID: target.UUID, Generation: generation, OperationIDs: operationIDs(operations)})
		return s.store.getInstance(ctx, target.UUID)
	default:
		return nil, validation("action", "action must be RESET, DESTROY or EXTEND")
	}
}

func (o Options) MaxTtl() time.Duration {
	if o.MaxTTL > 0 {
		return o.MaxTTL
	}
	return 7 * 24 * time.Hour
}

func operationIDs(operations []Operation) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(operations))
	for _, operation := range operations {
		ids = append(ids, operation.ID)
	}
	return ids
}

func (s *Service) enqueue(ctx context.Context, job Job) {
	if s.publisher == nil || len(job.OperationIDs) == 0 {
		return
	}
	if err := s.publisher.PublishJob(ctx, job); err != nil && s.logger != nil {
		s.logger.Warn("instance job publish failed", "instanceId", job.InstanceID.String(), "error", err.Error())
	}
}

func (s *Service) expiryFor(source *sourceContext) (*time.Time, time.Duration) {
	ttl := s.options.DefaultTTL
	expiresAt := time.Now().Add(ttl)
	if maximum := earliest(source.BoardEndsAt, source.ModuleEndsAt); maximum != nil && maximum.Before(expiresAt) {
		expiresAt = *maximum
		ttl = time.Until(expiresAt)
		if ttl < 0 {
			ttl = 0
		}
	}
	if s.options.MaxTTL > 0 && time.Until(expiresAt) > s.options.MaxTTL {
		expiresAt = time.Now().Add(s.options.MaxTTL)
		ttl = s.options.MaxTTL
	}
	return &expiresAt, ttl
}

func earliest(values ...*time.Time) *time.Time {
	var result *time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		if result == nil || value.Before(*result) {
			copy := *value
			result = &copy
		}
	}
	return result
}

func (s *Service) ensureActive(source *sourceContext) error {
	now := time.Now()
	if source.BoardStatus != "ACTIVE" && source.BoardStatus != "SCHEDULED" {
		return conflict(CodeBoardNotActive, "board is not active")
	}
	if source.BoardStartsAt != nil && now.Before(*source.BoardStartsAt) {
		return conflict(CodeBoardNotActive, "board has not started")
	}
	if source.BoardEndsAt != nil && !now.Before(*source.BoardEndsAt) {
		return conflict(CodeExpired, "board has ended")
	}
	if !source.ModuleEnabled {
		return instanceError(CodeForbidden, "module is not available", 403)
	}
	if source.ModuleStartsAt != nil && now.Before(*source.ModuleStartsAt) {
		return instanceError(CodeForbidden, "module is not available", 403)
	}
	if source.ModuleEndsAt != nil && !now.Before(*source.ModuleEndsAt) {
		return conflict(CodeExpired, "module has ended")
	}
	return nil
}

// authorizeParticipant allows the participation-only permission or the staff
// module.instances.admin permission. It never widens a participant grant.
func (s *Service) authorizeParticipant(ctx context.Context, actor authorization.Principal, permission authorization.Permission, boardID, moduleID uuid.UUID) error {
	if err := s.authorize(ctx, actor, permission, boardID, moduleID, true); err == nil {
		return nil
	}
	return s.authorize(ctx, actor, authorization.PermissionModuleInstancesAdmin, boardID, moduleID, false)
}

func (s *Service) authorize(ctx context.Context, actor authorization.Principal, permission authorization.Permission, boardID, moduleID uuid.UUID, requireParticipant bool) error {
	if actor.UserID == uuid.Nil {
		return unauthorized()
	}
	if s.auth == nil {
		return instanceError(CodeInternal, "authorization is not configured", 500)
	}
	scope := authorization.Scope{BoardID: boardID, ModuleID: &moduleID}
	decision, err := s.auth.Authorize(ctx, actor, permission, scope)
	if err != nil {
		return instanceError(CodeInternal, "authorization failed", 500)
	}
	if !decision.Allowed {
		return forbidden()
	}
	return nil
}

func (s *Service) authorizeGlobal(ctx context.Context, actor authorization.Principal, permission authorization.Permission, boardID, moduleID *uuid.UUID) error {
	if actor.UserID == uuid.Nil {
		return unauthorized()
	}
	scope := authorization.Scope{}
	if boardID != nil {
		scope.BoardID = *boardID
	}
	if moduleID != nil {
		scope.ModuleID = moduleID
	}
	decision, err := s.auth.Authorize(ctx, actor, permission, scope)
	if err != nil {
		return instanceError(CodeInternal, "authorization failed", 500)
	}
	if !decision.Allowed {
		return forbidden()
	}
	return nil
}

func (s *Service) authorizeRead(ctx context.Context, actor authorization.Principal, instance *Instance) error {
	subjectType, subjectID, allowed, err := s.store.isCurrentSubject(ctx, instance.BoardID, instance.ModuleID, actor.UserID)
	if err != nil {
		return instanceError(CodeInternal, "authorization failed", 500)
	}
	if allowed && subjectType == instance.SubjectType && subjectID == instance.SubjectID {
		if s.access == nil {
			return nil
		}
		moduleAllowed, accessErr := s.access.CanAccessModule(ctx, actor, instance.BoardID, instance.ModuleID)
		if accessErr != nil {
			return instanceError(CodeInternal, "authorization failed", 500)
		}
		if moduleAllowed {
			return nil
		}
	}
	return s.authorizeGlobal(ctx, actor, authorization.PermissionModuleInstancesAdmin, &instance.BoardID, &instance.ModuleID)
}

// isCurrentSubject re-checks the live participant row, current team subject and
// module audience. Revoking a participant or audience takes effect immediately
// for instance/endpoint reads.
func (s *store) isCurrentSubject(ctx context.Context, boardID, moduleID, userID uuid.UUID) (string, uuid.UUID, bool, error) {
	var participant bool
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM board_participants WHERE board_id=$1 AND user_id=$2)
	`, boardID, userID).Scan(&participant); err != nil || !participant {
		return "", uuid.Nil, false, err
	}
	var participationMode string
	if err := s.db.Pool.QueryRow(ctx, `SELECT participation_mode FROM boards WHERE id=$1`, boardID).Scan(&participationMode); err != nil {
		return "", uuid.Nil, false, err
	}
	subjectType, subjectID, _, err := s.resolveSubject(ctx, boardID, participationMode, userID)
	if err != nil {
		return "", uuid.Nil, false, nil
	}
	var allowed bool
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT NOT EXISTS (SELECT 1 FROM module_audiences WHERE module_id=$1)
		    OR EXISTS (
		        SELECT 1 FROM module_audiences ma
		        JOIN audience_members am ON am.audience_id=ma.audience_id
		        WHERE ma.module_id=$1 AND am.subject_type=$2 AND am.subject_id=$3
		    )
	`, moduleID, subjectType, subjectID).Scan(&allowed); err != nil {
		return "", uuid.Nil, false, err
	}
	return subjectType, subjectID, allowed, nil
}

func (s *Service) ensureAudience(ctx context.Context, boardID, moduleID uuid.UUID, subjectType string, subjectID uuid.UUID) error {
	allowed, err := s.store.subjectInModuleAudience(ctx, boardID, moduleID, subjectType, subjectID)
	if err != nil {
		return err
	}
	if !allowed {
		return instanceError(CodeForbidden, "module is not available", 403)
	}
	return nil
}

func generatedRefs(manifest deployment.Template) []GeneratedRef {
	refs := make([]GeneratedRef, 0, len(manifest.Generated))
	for _, generated := range manifest.Generated {
		refs = append(refs, GeneratedRef{Key: generated.Key, Kind: generated.Kind, Length: generated.Length, RotateOnReset: generated.RotateOnReset})
	}
	return refs
}

func hashRequest(scope Scope, boardID, moduleID uuid.UUID, taskID *uuid.UUID, versionID uuid.UUID, parameters map[string]string) string {
	hasher := sha256.New()
	fmt.Fprintf(hasher, "%s|%s|%s|%s|", scope, boardID, moduleID, versionID)
	if taskID != nil {
		hasher.Write([]byte(taskID.String()))
	}
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		hasher.Write([]byte(key + "=" + parameters[key] + ";"))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func randomValue(kind string, length int) (string, error) {
	if length < 8 {
		length = 8
	}
	if length > 128 {
		length = 128
	}
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	switch kind {
	case "uuid":
		return uuid.NewString(), nil
	case "base64":
		return base64.RawURLEncoding.EncodeToString(raw), nil
	case "hex":
		return hex.EncodeToString(raw), nil
	default:
		const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
		out := make([]byte, length)
		for index := range out {
			out[index] = alphabet[int(raw[index])%len(alphabet)]
		}
		return string(out), nil
	}
}

var _ = strings.TrimSpace
