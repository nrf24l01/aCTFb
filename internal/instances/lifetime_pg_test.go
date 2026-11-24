package instances

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/runtime"
)

// lifetimeHarness wires a real PostgreSQL schema with the fake driver so the
// service, the orchestrator and the SQL policies are exercised together.
type lifetimeHarness struct {
	db         *postgres.DB
	ids        seedIDs
	service    *Service
	orchestrator *Orchestrator
	publisher  *fakePublisher
	driver     *fakeDriver
	actor      authorization.Principal
	ctx        context.Context
}

func newLifetimeHarness(t *testing.T) *lifetimeHarness {
	t.Helper()
	db, cleanup := testDB(t)
	t.Cleanup(cleanup)
	ids := seedInstanceFixture(t, db)
	driver := &fakeDriver{}
	registry, err := runtime.NewRegistry(driver)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &fakePublisher{}
	auth := authorization.NewAuthorizer(db)
	service, err := NewService(db, auth, registry, testSealer(t), 1, WithPublisher(publisher))
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := NewOrchestrator(db, registry, auth, testSealer(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	return &lifetimeHarness{
		db: db, ids: ids, service: service, orchestrator: orchestrator,
		publisher: publisher, driver: driver,
		actor: authorization.Principal{UserID: ids.UserID}, ctx: context.Background(),
	}
}

// setModuleSettings writes the persisted module policy directly, mirroring the
// document the boards lane stores.
func (h *lifetimeHarness) setModuleSettings(t *testing.T, document string) {
	t.Helper()
	if _, err := h.db.Pool.Exec(h.ctx, `UPDATE modules SET task_instance_settings = $2 WHERE id = $1`,
		h.ids.ModuleID, []byte(document)); err != nil {
		t.Fatalf("set module settings: %v", err)
	}
}

// setTaskOverrides writes the nullable task override columns directly.
func (h *lifetimeHarness) setTaskOverrides(t *testing.T, initial, maxTotal, extensionSeconds, extensionMaxCount *int, extensionEnabled *bool) {
	t.Helper()
	if _, err := h.db.Pool.Exec(h.ctx, `
		UPDATE tasks SET instance_ttl_seconds=$2, instance_max_ttl_seconds=$3,
		       instance_extension_enabled=$4, instance_extension_seconds=$5, instance_extension_max_count=$6
		WHERE id = $1
	`, h.ids.TaskID, initial, maxTotal, extensionEnabled, extensionSeconds, extensionMaxCount); err != nil {
		t.Fatalf("set task overrides: %v", err)
	}
}

func (h *lifetimeHarness) start(t *testing.T, key string) *Instance {
	t.Helper()
	instance, err := h.service.StartTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, key)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return instance
}

// runJob applies the last published job so the instance reaches RUNNING.
func (h *lifetimeHarness) runJob(t *testing.T) {
	t.Helper()
	if err := h.orchestrator.HandleJob(h.ctx, h.publisher.last(t)); err != nil {
		t.Fatalf("handle job: %v", err)
	}
}

func (h *lifetimeHarness) get(t *testing.T, id uuid.UUID) *Instance {
	t.Helper()
	instance, err := h.service.Get(h.ctx, h.actor, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return instance
}

func (h *lifetimeHarness) snapshot(t *testing.T, id uuid.UUID) DesiredSnapshot {
	t.Helper()
	var raw []byte
	if err := h.db.Pool.QueryRow(h.ctx, `SELECT desired_snapshot FROM instances WHERE id=$1`, id).Scan(&raw); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snapshot DesiredSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return snapshot
}

func intTestPtr(value int) *int { return &value }
func boolTestPtr(value bool) *bool { return &value }

func TestTaskLifetimeOverrideAndSnapshotFreezeRealPostgres(t *testing.T) {
	h := newLifetimeHarness(t)
	h.setModuleSettings(t, `{"defaultTtlSeconds":3600,"maxTtlSeconds":14400,"maxConcurrentInstancesPerSubject":3,"resetEnabled":true,"extension":{"enabled":true,"seconds":1800,"maxCount":3}}`)
	h.setTaskOverrides(t, intTestPtr(1800), intTestPtr(7200), intTestPtr(900), intTestPtr(6), boolTestPtr(true))

	before := time.Now()
	instance := h.start(t, "lifetime-snapshot-0001")
	if instance.ExpiresAt == nil {
		t.Fatal("expiry not set")
	}
	granted := instance.ExpiresAt.Sub(before)
	if granted < 29*time.Minute || granted > 31*time.Minute {
		t.Fatalf("initial lifetime = %s, want about 30 min from the request", granted)
	}

	snapshot := h.snapshot(t, instance.UUID)
	if snapshot.Lifecycle == nil {
		t.Fatal("lifecycle policy was not frozen into the snapshot")
	}
	if snapshot.Lifecycle.InitialTTLSeconds != 1800 || snapshot.Lifecycle.MaxTotalTTLSeconds != 7200 {
		t.Fatalf("frozen TTLs wrong: %+v", snapshot.Lifecycle)
	}
	if snapshot.Lifecycle.ExtendSeconds != 900 || snapshot.Lifecycle.ExtendMaxCount != 6 || !snapshot.Lifecycle.ExtendEnabled {
		t.Fatalf("frozen extension policy wrong: %+v", snapshot.Lifecycle)
	}

	// A later module edit must not rewrite the existing instance policy.
	h.setModuleSettings(t, `{"defaultTtlSeconds":60,"maxTtlSeconds":120,"maxConcurrentInstancesPerSubject":1,"resetEnabled":false,"extension":{"enabled":false,"seconds":60,"maxCount":0}}`)
	after := h.snapshot(t, instance.UUID)
	if after.Lifecycle == nil || after.Lifecycle.MaxTotalTTLSeconds != 7200 {
		t.Fatalf("module edit changed the frozen snapshot: %+v", after.Lifecycle)
	}
}

func TestExtendConsumesOneCreditAndReplaysIdempotentlyRealPostgres(t *testing.T) {
	h := newLifetimeHarness(t)
	h.setModuleSettings(t, `{"defaultTtlSeconds":3600,"maxTtlSeconds":7200,"maxConcurrentInstancesPerSubject":3,"resetEnabled":true,"extension":{"enabled":true,"seconds":900,"maxCount":3}}`)
	instance := h.start(t, "lifetime-extend-0001")
	h.runJob(t)
	running := h.get(t, instance.UUID)
	if running.Status != StatusRunning || !running.CanExtend {
		t.Fatalf("instance should be extendable: %+v", running)
	}
	if running.ExtensionsRemaining == nil || *running.ExtensionsRemaining != 3 {
		t.Fatalf("remaining extensions = %v, want 3", running.ExtensionsRemaining)
	}
	firstExpiry := *running.ExpiresAt

	extended, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionExtend, "extend-request-key-0001")
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if extended.ExtensionCount != 1 {
		t.Fatalf("extension count = %d, want 1", extended.ExtensionCount)
	}
	if got := extended.ExpiresAt.Sub(firstExpiry); got < 14*time.Minute || got > 16*time.Minute {
		t.Fatalf("granted %s, want about 15 min", got)
	}
	if extended.ExtensionsRemaining == nil || *extended.ExtensionsRemaining != 2 {
		t.Fatalf("remaining extensions = %v, want 2", extended.ExtensionsRemaining)
	}
	secondExpiry := *extended.ExpiresAt

	// The same Idempotency-Key must replay the original outcome.
	replay, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionExtend, "extend-request-key-0001")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.ExtensionCount != 1 || !replay.ExpiresAt.Equal(secondExpiry) {
		t.Fatalf("replay consumed another credit: count=%d expiry=%s", replay.ExtensionCount, replay.ExpiresAt)
	}

	// A different key is a new intent and does consume a credit.
	third, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionExtend, "extend-request-key-0002")
	if err != nil {
		t.Fatalf("second extend: %v", err)
	}
	if third.ExtensionCount != 2 {
		t.Fatalf("extension count = %d, want 2", third.ExtensionCount)
	}
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_extension_requests WHERE instance_id=$1`, 2, instance.UUID)
}

func TestExtendRejectsExpiredAndSpendsNoCreditRealPostgres(t *testing.T) {
	h := newLifetimeHarness(t)
	h.setModuleSettings(t, `{"defaultTtlSeconds":3600,"maxTtlSeconds":7200,"maxConcurrentInstancesPerSubject":3,"resetEnabled":true,"extension":{"enabled":true,"seconds":900,"maxCount":3}}`)
	instance := h.start(t, "lifetime-expired-0001")
	h.runJob(t)

	// Move the deadline into the past with the database clock.
	if _, err := h.db.Pool.Exec(h.ctx, `UPDATE instances SET expires_at = NOW() - interval '1 minute' WHERE id=$1`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionExtend, "extend-request-key-expired"); err == nil {
		t.Fatal("an expired instance must not be extendable")
	}
	assertCount(t, h.db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND extension_count=0`, 1, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_extension_requests WHERE instance_id=$1`, 0, instance.UUID)

	// A removed instance must never be revived either.
	if _, err := h.db.Pool.Exec(h.ctx, `UPDATE instances SET status='EXPIRED', removed_at=NOW(), expires_at=NOW() + interval '1 hour' WHERE id=$1`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionExtend, "extend-request-key-removed"); err == nil {
		t.Fatal("a removed instance must not be extendable")
	}
	assertCount(t, h.db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND extension_count=0`, 1, instance.UUID)
}

func TestExtendAtHardDeadlineConsumesOneCreditForPartialGainRealPostgres(t *testing.T) {
	h := newLifetimeHarness(t)
	// 2 minutes of total lifetime with a 15 minute step: the second extension can
	// only grant a partial amount and must still spend exactly one credit.
	h.setModuleSettings(t, `{"defaultTtlSeconds":60,"maxTtlSeconds":120,"maxConcurrentInstancesPerSubject":3,"resetEnabled":true,"extension":{"enabled":true,"seconds":900,"maxCount":5}}`)
	instance := h.start(t, "lifetime-partial-0001")
	h.runJob(t)

	extended, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionExtend, "extend-request-key-partial")
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if extended.ExtensionCount != 1 {
		t.Fatalf("partial extension must consume one credit, count=%d", extended.ExtensionCount)
	}
	if extended.ExpiresAt == nil {
		t.Fatal("expiry missing")
	}
	var hardDeadline time.Time
	if err := h.db.Pool.QueryRow(h.ctx, `SELECT created_at + interval '120 seconds' FROM instances WHERE id=$1`, instance.UUID).Scan(&hardDeadline); err != nil {
		t.Fatal(err)
	}
	if extended.ExpiresAt.After(hardDeadline.Add(time.Second)) {
		t.Fatalf("expiry %s exceeded the hard deadline %s", extended.ExpiresAt, hardDeadline)
	}

	// Already at the hard deadline: no budget left, and no credit consumed.
	if _, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionExtend, "extend-request-key-nobudget"); err == nil {
		t.Fatal("an extension with no budget left must be rejected")
	}
	assertCount(t, h.db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND extension_count=1`, 1, instance.UUID)
}

func TestResetDoesNotExtendLifetimeRealPostgres(t *testing.T) {
	h := newLifetimeHarness(t)
	h.setModuleSettings(t, `{"defaultTtlSeconds":3600,"maxTtlSeconds":7200,"maxConcurrentInstancesPerSubject":3,"resetEnabled":true,"extension":{"enabled":true,"seconds":900,"maxCount":3}}`)
	instance := h.start(t, "lifetime-reset-0001")
	h.runJob(t)
	before := h.get(t, instance.UUID)

	if _, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionReset, "reset-request-key-0001"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	after := h.get(t, instance.UUID)
	if !after.ExpiresAt.Equal(*before.ExpiresAt) {
		t.Fatalf("reset changed the expiry: before=%s after=%s", before.ExpiresAt, after.ExpiresAt)
	}
	if after.ExtensionCount != before.ExtensionCount {
		t.Fatalf("reset changed the extension count: %d -> %d", before.ExtensionCount, after.ExtensionCount)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("reset changed the lifetime anchor")
	}
}

func TestResetDisabledByModulePolicyRealPostgres(t *testing.T) {
	h := newLifetimeHarness(t)
	h.setModuleSettings(t, `{"defaultTtlSeconds":3600,"maxTtlSeconds":7200,"maxConcurrentInstancesPerSubject":3,"resetEnabled":false,"extension":{"enabled":true,"seconds":900,"maxCount":3}}`)
	instance := h.start(t, "lifetime-resetoff-0001")
	h.runJob(t)
	if _, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionReset, "reset-request-key-0002"); err == nil {
		t.Fatal("reset must be rejected when the module disables it")
	}
	if current := h.get(t, instance.UUID); current.Generation != 1 {
		t.Fatalf("generation advanced despite a disabled reset: %d", current.Generation)
	}
}

func TestMaxConcurrentInstancesEnforcedRealPostgres(t *testing.T) {
	h := newLifetimeHarness(t)
	h.setModuleSettings(t, `{"defaultTtlSeconds":3600,"maxTtlSeconds":7200,"maxConcurrentInstancesPerSubject":1,"resetEnabled":true,"extension":{"enabled":true,"seconds":900,"maxCount":3}}`)
	// A second task in the same module so the limit is per subject, not per task.
	secondTask := uuid.New()
	if _, err := h.db.Pool.Exec(h.ctx, `
		INSERT INTO tasks (id, board_id, module_id, title, category, type, points, submission_mode, submission_checker, instance_enabled, created_by)
		VALUES ($1,$2,$3,'Second','cat','FLAG',100,'TEXT','AUTOMATIC',TRUE,$4)
	`, secondTask, h.ids.BoardID, h.ids.ModuleID, h.ids.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Pool.Exec(h.ctx, `UPDATE tasks SET deployment_binding_id=$2 WHERE id=$1`, secondTask, h.ids.BindingID); err != nil {
		t.Fatal(err)
	}

	first := h.start(t, "lifetime-concurrent-0001")
	if _, err := h.service.StartTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, secondTask, "lifetime-concurrent-0002"); err == nil {
		t.Fatal("the second parallel instance must be rejected")
	}

	// Capacity is released only after the destroy is confirmed.
	if _, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionDestroy, "destroy-request-key-0001"); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := h.service.StartTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, secondTask, "lifetime-concurrent-0003"); err == nil {
		t.Fatal("capacity must stay allocated until the provider confirms destroy")
	}
	h.runJob(t)
	if _, err := h.service.StartTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, secondTask, "lifetime-concurrent-0004"); err != nil {
		t.Fatalf("capacity was not released after confirmed destroy: %v", err)
	}
	_ = first
}

func TestLegacySnapshotFallsBackToGlobalPolicyRealPostgres(t *testing.T) {
	h := newLifetimeHarness(t)
	instance := h.start(t, "lifetime-legacy-0001")
	h.runJob(t)
	// Simulate an instance created before the lifecycle block existed.
	if _, err := h.db.Pool.Exec(h.ctx, `
		UPDATE instances SET desired_snapshot = desired_snapshot - 'lifecycle' WHERE id=$1
	`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	legacy := h.get(t, instance.UUID)
	if legacy.Lifecycle != nil {
		t.Fatal("legacy snapshot should have no frozen policy")
	}
	// The legacy path reports no extension capability rather than inventing one.
	if legacy.CanExtend {
		t.Fatalf("legacy instance must not advertise canExtend: %+v", legacy)
	}
}

func TestWorkerSkipsExpiredJobWithoutProviderCallRealPostgres(t *testing.T) {
	h := newLifetimeHarness(t)
	instance := h.start(t, "lifetime-expiredjob-0001")
	// The deadline passes while the CREATE job is still queued.
	if _, err := h.db.Pool.Exec(h.ctx, `UPDATE instances SET expires_at = NOW() - interval '1 minute' WHERE id=$1`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	h.runJob(t)
	if h.driver.applied != 0 {
		t.Fatalf("worker applied an expired job: %d provider calls", h.driver.applied)
	}
	current := h.get(t, instance.UUID)
	if current.Status == StatusRunning {
		t.Fatalf("expired job produced a RUNNING instance: %+v", current)
	}
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_operations WHERE instance_id=$1 AND status='CANCELED'`, 1, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
}

// expiringDriver expires the instance during Apply, modelling a provider call
// that outlives the deadline.
type expiringDriver struct {
	fakeDriver
	db *postgres.DB
}

func (d *expiringDriver) Apply(ctx context.Context, target runtime.Target, node deployment.Node, op runtime.Operation) (runtime.ProviderResource, error) {
	if _, err := d.db.Pool.Exec(ctx, `UPDATE instances SET expires_at = NOW() - interval '1 second' WHERE id=$1`, op.InstanceID); err != nil {
		return runtime.ProviderResource{}, err
	}
	return d.fakeDriver.Apply(ctx, target, node, op)
}

func TestWorkerLateExpiryDoesNotAdvertiseRunningRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()
	driver := &expiringDriver{db: db}
	registry, err := runtime.NewRegistry(driver)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &fakePublisher{}
	auth := authorization.NewAuthorizer(db)
	service, err := NewService(db, auth, registry, testSealer(t), 1, WithPublisher(publisher))
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := NewOrchestrator(db, registry, auth, testSealer(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{UserID: ids.UserID}
	instance, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "lifetime-late-0001")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// HandleJob surfaces the expiry as an error; the important part is the state.
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err == nil {
		t.Fatal("a job that expires during Apply must not report success")
	}
	current, err := service.Get(ctx, actor, instance.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status == StatusRunning {
		t.Fatalf("late expiry produced a RUNNING instance: %+v", current)
	}
	if driver.destroyed == 0 {
		t.Fatal("the resource created after the deadline was not destroyed")
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instance_nodes WHERE instance_id=$1 AND status='RUNNING'`, 0, instance.UUID)
}
