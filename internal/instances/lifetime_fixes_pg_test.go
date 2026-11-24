package instances

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/runtime"
)

// legacyHarness wires a service with explicit operator Options so the tests can
// prove the read model uses the operator values, not hardcoded defaults.
func newLegacyHarness(t *testing.T, options Options) *lifetimeHarness {
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
	service, err := NewService(db, auth, registry, testSealer(t), 1, WithPublisher(publisher), WithOptions(options))
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

// stripLifecycle makes an instance look like one created before the policy block
// existed.
func (h *lifetimeHarness) stripLifecycle(t *testing.T, instanceID uuid.UUID) {
	t.Helper()
	if _, err := h.db.Pool.Exec(h.ctx, `
		UPDATE instances SET desired_snapshot = desired_snapshot - 'lifecycle' WHERE id=$1
	`, instanceID); err != nil {
		t.Fatalf("strip lifecycle: %v", err)
	}
}

// I1: the read model must report exactly what the service will do for a legacy
// instance, including a non-default operator extension policy.
func TestLegacyInstanceReadModelMatchesActionPolicyRealPostgres(t *testing.T) {
	h := newLegacyHarness(t, Options{
		DefaultTTL: time.Hour, MaxTTL: 7 * 24 * time.Hour,
		ExtensionTTL: 7 * time.Minute, MaxExtensions: 5,
		MaxInstancesPerSubject: 2,
	})
	instance := h.start(t, "legacy-match-0001")
	h.runJob(t)
	h.stripLifecycle(t, instance.UUID)

	legacy := h.get(t, instance.UUID)
	if legacy.Lifecycle != nil {
		t.Fatal("legacy snapshot must not carry a frozen policy")
	}
	if !legacy.CanExtend {
		t.Fatalf("legacy instance must advertise the extension the service grants: %+v", legacy)
	}
	if legacy.ExtensionSeconds == nil || *legacy.ExtensionSeconds != 420 {
		t.Fatalf("extension step = %v, want the operator option 420 s", legacy.ExtensionSeconds)
	}
	if legacy.ExtensionsRemaining == nil || *legacy.ExtensionsRemaining != 5 {
		t.Fatalf("remaining extensions = %v, want the operator option 5", legacy.ExtensionsRemaining)
	}

	// The action must agree with the button.
	extended, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionExtend, "legacy-match-extend-01")
	if err != nil {
		t.Fatalf("legacy extend must succeed when the button is enabled: %v", err)
	}
	if got := extended.ExpiresAt.Sub(*legacy.ExpiresAt); got != 7*time.Minute {
		t.Fatalf("granted %s, want the operator step 7m", got)
	}
	if extended.ExtensionsRemaining == nil || *extended.ExtensionsRemaining != 4 {
		t.Fatalf("remaining after extend = %v, want 4", extended.ExtensionsRemaining)
	}
}

// I2: a legacy instance follows the LIVE module reset switch, and never resets
// after its own deadline even while the board window is open.
func TestLegacyResetFollowsLiveModuleSettingRealPostgres(t *testing.T) {
	h := newLegacyHarness(t, Options{})
	// Live module policy disables reset.
	h.setModuleSettings(t, `{"defaultTtlSeconds":3600,"maxTtlSeconds":14400,"maxConcurrentInstancesPerSubject":3,"resetEnabled":false,"extension":{"enabled":true,"seconds":900,"maxCount":3}}`)
	instance := h.start(t, "legacy-reset-0001")
	h.runJob(t)
	h.stripLifecycle(t, instance.UUID)

	legacy := h.get(t, instance.UUID)
	if legacy.CanReset {
		t.Fatalf("legacy instance must report canReset=false when the module disables reset: %+v", legacy)
	}
	if _, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionReset, "legacy-reset-action-01"); err == nil {
		t.Fatal("legacy reset must be rejected when the live module disables it")
	}
	if current := h.get(t, instance.UUID); current.Generation != 1 {
		t.Fatalf("generation advanced despite a disabled reset: %d", current.Generation)
	}

	// Re-enable reset: the same legacy row must follow the live switch again.
	h.setModuleSettings(t, `{"defaultTtlSeconds":3600,"maxTtlSeconds":14400,"maxConcurrentInstancesPerSubject":3,"resetEnabled":true,"extension":{"enabled":true,"seconds":900,"maxCount":3}}`)
	if reopened := h.get(t, instance.UUID); !reopened.CanReset {
		t.Fatalf("legacy instance must report canReset=true after the module re-enables reset: %+v", reopened)
	}

	// Expired legacy instance: no reset, even though the module now allows it and
	// the board window is still open.
	if _, err := h.db.Pool.Exec(h.ctx, `UPDATE instances SET expires_at = NOW() - interval '1 minute' WHERE id=$1`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	expired := h.get(t, instance.UUID)
	if expired.CanReset {
		t.Fatalf("expired instance must not report canReset: %+v", expired)
	}
	if _, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionReset, "legacy-reset-expired-01"); err == nil {
		t.Fatal("an expired instance must not be reset while the board is still open")
	}
	if current := h.get(t, instance.UUID); current.Generation != 1 {
		t.Fatalf("expired reset advanced the generation: %d", current.Generation)
	}
}

// M3: EXTEND re-checks the live board/module window under the transaction lock.
func TestExtendRejectsClosedWindowWithoutSpendingCreditRealPostgres(t *testing.T) {
	cases := []struct {
		name  string
		apply func(t *testing.T, h *lifetimeHarness)
	}{
		{"board paused", func(t *testing.T, h *lifetimeHarness) {
			if _, err := h.db.Pool.Exec(h.ctx, `UPDATE boards SET status='DRAFT' WHERE id=$1`, h.ids.BoardID); err != nil {
				t.Fatal(err)
			}
		}},
		{"module disabled", func(t *testing.T, h *lifetimeHarness) {
			if _, err := h.db.Pool.Exec(h.ctx, `UPDATE modules SET enabled=FALSE WHERE id=$1`, h.ids.ModuleID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newLegacyHarness(t, Options{})
			h.setModuleSettings(t, `{"defaultTtlSeconds":3600,"maxTtlSeconds":14400,"maxConcurrentInstancesPerSubject":3,"resetEnabled":true,"extension":{"enabled":true,"seconds":900,"maxCount":3}}`)
			instance := h.start(t, "extend-window-0001")
			h.runJob(t)
			testCase.apply(t, h)
			if _, err := h.service.ActionTask(h.ctx, h.actor, h.ids.BoardID, h.ids.ModuleID, h.ids.TaskID, ActionExtend, "extend-window-key-0001"); err == nil {
				t.Fatal("extend must be rejected when the window is not usable")
			}
			assertCount(t, h.db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND extension_count=0`, 1, instance.UUID)
			assertCount(t, h.db, `SELECT COUNT(*) FROM instance_extension_requests WHERE instance_id=$1`, 0, instance.UUID)
		})
	}
}

// M2: a pentest environment never advertises an extension and reflects the real
// reset configuration and budget.
func TestPentestCapabilitiesReflectConfigRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	pentest := seedPentestModule(t, db, ids, true, 2)
	ctx := context.Background()
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
	actor := authorization.Principal{UserID: ids.UserID}
	instance, err := service.StartPentestEnvironment(ctx, actor, ids.BoardID, pentest.ModuleID, "pentest-cap-key-0001")
	if err != nil {
		t.Fatalf("start pentest: %v", err)
	}
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err != nil {
		t.Fatalf("apply pentest job: %v", err)
	}
	running, err := service.Get(ctx, actor, instance.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if running.CanExtend {
		t.Fatal("a pentest environment must never advertise an extension")
	}
	if running.ExtensionSeconds != nil || running.ExtensionsRemaining != nil {
		t.Fatalf("pentest must not expose extension fields: %+v", running)
	}
	if !running.CanReset {
		t.Fatalf("pentest with reset enabled and budget left must report canReset: %+v", running)
	}

	// Disable reset in the live config: the capability must follow.
	if _, err := db.Pool.Exec(ctx, `UPDATE pentest_configs SET reset_enabled=FALSE WHERE module_id=$1`, pentest.ModuleID); err != nil {
		t.Fatal(err)
	}
	if disabled, err := service.Get(ctx, actor, instance.UUID); err != nil {
		t.Fatal(err)
	} else if disabled.CanReset {
		t.Fatalf("pentest must report canReset=false after the config disables it: %+v", disabled)
	}
}

// I5: a failed destroy after a late expiry must keep the provider id and the
// ports, then succeed on the next reconcile pass with the same id.
type lateExpiryFailingDestroyDriver struct {
	fakeDriver
	db            *postgres.DB
	destroyCalls  int
	failFirstTime bool
	seenIDs       []string
}

func (d *lateExpiryFailingDestroyDriver) Apply(ctx context.Context, target runtime.Target, node deployment.Node, op runtime.Operation) (runtime.ProviderResource, error) {
	if _, err := d.db.Pool.Exec(ctx, `UPDATE instances SET expires_at = NOW() - interval '1 second' WHERE id=$1`, op.InstanceID); err != nil {
		return runtime.ProviderResource{}, err
	}
	return d.fakeDriver.Apply(ctx, target, node, op)
}

func (d *lateExpiryFailingDestroyDriver) Destroy(ctx context.Context, target runtime.Target, providerID string, op runtime.Operation) error {
	d.destroyCalls++
	d.seenIDs = append(d.seenIDs, providerID)
	if d.failFirstTime && d.destroyCalls == 1 {
		return &runtime.CapabilityError{Driver: d.Name(), Capability: "destroy", Detail: "transient"}
	}
	d.destroyed++
	return nil
}

func TestLateExpiryFailedDestroyPersistsProviderIDRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()
	driver := &lateExpiryFailingDestroyDriver{db: db, failFirstTime: true}
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
	instance, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "late-destroy-0001")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	job := publisher.last(t)
	if err := orchestrator.HandleJob(ctx, job); err == nil {
		t.Fatal("a failed destroy after a late expiry must surface an error")
	}

	// The provider id must be durable and the instance must not be released.
	var providerID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT COALESCE(provider_resource_id,'') FROM instance_nodes WHERE instance_id=$1
	`, instance.UUID).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if providerID == "" {
		t.Fatal("provider id was not persisted before the destroy attempt")
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND removed_at IS NULL`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_nodes WHERE instance_id=$1 AND status='RUNNING'`, 0, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM events_outbox WHERE type='instance.started'`, 0)

	// The next reconcile pass must retry with the SAME id and release once.
	jobs, err := orchestrator.PendingJobs(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	retried := false
	for _, pending := range jobs {
		if pending.InstanceID != instance.UUID {
			continue
		}
		retried = true
		if err := orchestrator.HandleJob(ctx, pending); err != nil {
			t.Fatalf("retry destroy: %v", err)
		}
	}
	if !retried {
		t.Fatal("no cleanup job was queued for the failed destroy")
	}
	if len(driver.seenIDs) < 2 || driver.seenIDs[len(driver.seenIDs)-1] != providerID {
		t.Fatalf("retry did not reuse the persisted provider id: %v", driver.seenIDs)
	}
	final, err := service.Get(ctx, actor, instance.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusExpired || final.RemovedAt == nil {
		t.Fatalf("instance was not released after the retry: %+v", final)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM events_outbox WHERE type='instance.expired'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM events_outbox WHERE type='instance.started'`, 0)
}

// A multi-node instance can pass the deadline between nodes. The first node
// already owns a persisted provider object when the second node's apply notices
// the deadline, so cleanup must queue that node's destroy instead of releasing
// the ports under a live workload.
type secondApplyExpiringDriver struct {
	fakeDriver
	db      *postgres.DB
	applies int
}

func (d *secondApplyExpiringDriver) Apply(ctx context.Context, target runtime.Target, node deployment.Node, op runtime.Operation) (runtime.ProviderResource, error) {
	d.applies++
	if d.applies == 2 {
		if _, err := d.db.Pool.Exec(ctx, `UPDATE instances SET expires_at = NOW() - interval '1 second' WHERE id=$1`, op.InstanceID); err != nil {
			return runtime.ProviderResource{}, err
		}
	}
	resource, err := d.fakeDriver.Apply(ctx, target, node, op)
	if err != nil {
		return resource, err
	}
	// The shared fake always reports a "web" endpoint; the second node has none,
	// so drop it to keep the fixture honest about its own manifest.
	if node.Key != "web" {
		resource.Endpoints = nil
	}
	return resource, nil
}

func TestLateExpiryWithAnotherLiveNodeQueuesDestroyRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()

	// Add a second node to the immutable version so the snapshot contains both.
	var manifestRaw []byte
	if err := db.Pool.QueryRow(ctx, `SELECT manifest FROM deployment_template_versions WHERE id=$1`, ids.VersionID).Scan(&manifestRaw); err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	nodes, _ := manifest["nodes"].([]any)
	manifest["nodes"] = append(nodes, map[string]any{
		"key": "db", "name": "db", "kind": "CONTAINER",
		"source":    map[string]any{"type": "OCI_IMAGE", "ref": "nginx@sha256:713fc2ebfd92e8217db451477182ee91944b123d2b1ee9fd5a8c6e91ca6c3a11"},
		"resources": map[string]any{"cpuMillicores": 100, "memoryMiB": 64, "diskMiB": 32},
	})
	updated, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	// The immutable-version trigger blocks UPDATE, so replace the row.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE deployment_template_versions DISABLE TRIGGER deployment_template_versions_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE deployment_template_versions SET manifest=$2 WHERE id=$1`, ids.VersionID, updated); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE deployment_template_versions ENABLE TRIGGER deployment_template_versions_immutable`); err != nil {
		t.Fatal(err)
	}

	driver := &secondApplyExpiringDriver{db: db}
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
	instance, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "late-multinode-01")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instance_nodes WHERE instance_id=$1`, 2, instance.UUID)

	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err == nil {
		t.Fatal("cleanup with another live node must not report a released instance")
	}
	// One node was cleaned up inline; the other keeps its provider object and a
	// queued destroy, and nothing was released under it.
	assertCount(t, db, `SELECT COUNT(*) FROM instance_nodes WHERE instance_id=$1 AND COALESCE(provider_resource_id,'')<>''`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_operations WHERE instance_id=$1 AND operation='DESTROY' AND status='QUEUED'`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND removed_at IS NULL`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 1, instance.UUID)

	// The reconcile pass destroys the surviving node and releases exactly once.
	jobs, err := orchestrator.PendingJobs(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	handled := false
	for _, pending := range jobs {
		if pending.InstanceID != instance.UUID {
			continue
		}
		handled = true
		if err := orchestrator.HandleJob(ctx, pending); err != nil {
			t.Fatalf("cleanup retry: %v", err)
		}
	}
	if !handled {
		t.Fatal("no cleanup job was queued")
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	final, err := service.Get(ctx, actor, instance.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusExpired || final.RemovedAt == nil {
		t.Fatalf("instance was not released after the retry: %+v", final)
	}
}

// N1: a crash between "provider destroy confirmed" and "bookkeeping committed"
// leaves a DESTROY op SUCCEEDED while the node still holds its provider id. The
// scheduler must heal that exact state, not require manual SQL.
func TestStuckStagedDestroySelfHealsRealPostgres(t *testing.T) {
	h := newLegacyHarness(t, Options{})
	instance := h.start(t, "stuck-staged-0001")
	h.runJob(t)

	// Seed the post-crash state: provider already destroyed, bookkeeping lost.
	if _, err := h.db.Pool.Exec(h.ctx, `
		UPDATE instances SET status='DESTROYING' WHERE id=$1
	`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Pool.Exec(h.ctx, `
		UPDATE instance_nodes SET status='DESTROYING', provider_resource_id='fake-stuck' WHERE instance_id=$1
	`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Pool.Exec(h.ctx, `
		INSERT INTO instance_operations
			(id, instance_id, node_key, operation, generation, idempotency_key, status, authorized_context, finished_at)
		VALUES ($1,$2,'web','DESTROY',1,$3,'SUCCEEDED',$4,NOW())
	`, uuid.New(), instance.UUID, fmt.Sprintf("%s:web:1:DESTROY", instance.UUID),
		[]byte(`{"system":true,"permission":"system.expire"}`)); err != nil {
		t.Fatal(err)
	}
	assertCount(t, h.db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND removed_at IS NULL`, 1, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 1, instance.UUID)

	// The scheduler alone must recover it: expire pass, pending pass, then job.
	handled := false
	for pass := 0; pass < 3 && !handled; pass++ {
		expired, err := h.orchestrator.ExpiredJobs(h.ctx, 50)
		if err != nil {
			t.Fatalf("expired jobs: %v", err)
		}
		pending, err := h.orchestrator.PendingJobs(h.ctx, 50)
		if err != nil {
			t.Fatalf("pending jobs: %v", err)
		}
		for _, job := range append(expired, pending...) {
			if job.InstanceID != instance.UUID {
				continue
			}
			if err := h.orchestrator.HandleJob(h.ctx, job); err != nil {
				continue
			}
			handled = true
		}
	}
	if !handled {
		t.Fatal("the scheduler never produced a healable job for the stuck destroy")
	}
	final := h.get(t, instance.UUID)
	if final.Status != StatusExpired || final.RemovedAt == nil {
		t.Fatalf("stuck destroy did not self-heal: %+v", final)
	}
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_nodes WHERE instance_id=$1 AND COALESCE(provider_resource_id,'')<>''`, 0, instance.UUID)
}

// N1: a DB error at any statement inside the completion transaction must roll the
// whole thing back, keeping the op queued and the lease held, and a later retry
// must finish the job. A trigger provides the fault deterministically.
func TestStagedDestroyCompletionRollsBackOnDBErrorRealPostgres(t *testing.T) {
	h := newLegacyHarness(t, Options{})
	instance := h.start(t, "staged-dbfail-0001")
	h.runJob(t)

	if _, err := h.db.Pool.Exec(h.ctx, `UPDATE instances SET status='DESTROYING' WHERE id=$1`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Pool.Exec(h.ctx, `
		UPDATE instance_nodes SET status='DESTROYING', provider_resource_id='fake-dbfail' WHERE instance_id=$1
	`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Pool.Exec(h.ctx, `
		INSERT INTO instance_operations
			(id, instance_id, node_key, operation, generation, idempotency_key, status, authorized_context, finished_at)
		VALUES ($1,$2,'web','DESTROY',1,$3,'SUCCEEDED',$4,NOW())
	`, uuid.New(), instance.UUID, fmt.Sprintf("%s:web:1:DESTROY", instance.UUID),
		[]byte(`{"system":true,"permission":"system.expire"}`)); err != nil {
		t.Fatal(err)
	}

	// Fail the LAST statement of the completion transaction (the lease release),
	// after the node and operation writes already happened.
	if _, err := h.db.Pool.Exec(h.ctx, `
		CREATE OR REPLACE FUNCTION test_fail_lease_release() RETURNS trigger AS $$
		BEGIN
		    IF NEW.released_at IS NOT NULL AND OLD.released_at IS NULL THEN
		        RAISE EXCEPTION 'injected lease release failure';
		    END IF;
		    RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER test_fail_lease_release BEFORE UPDATE ON instance_network_leases
		    FOR EACH ROW EXECUTE FUNCTION test_fail_lease_release();
	`); err != nil {
		t.Fatal(err)
	}

	// The first attempt must fail and roll back completely.
	_, _ = h.orchestrator.ExpiredJobs(h.ctx, 50)
	pending, err := h.orchestrator.PendingJobs(h.ctx, 50)
	if err != nil {
		t.Fatalf("pending jobs: %v", err)
	}
	attempted := false
	for _, job := range pending {
		if job.InstanceID == instance.UUID {
			attempted = true
			if err := h.orchestrator.HandleJob(h.ctx, job); err == nil {
				t.Fatal("the injected DB error must surface as a failure")
			}
		}
	}
	if !attempted {
		t.Fatal("the stuck destroy was not scheduled")
	}
	// Everything rolled back: still unreleased, id still present, op not SUCCEEDED.
	assertCount(t, h.db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND removed_at IS NULL`, 1, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 1, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 1, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_nodes WHERE instance_id=$1 AND COALESCE(provider_resource_id,'')<>''`, 1, instance.UUID)

	// Remove the fault: the retry must complete.
	if _, err := h.db.Pool.Exec(h.ctx, `DROP TRIGGER test_fail_lease_release ON instance_network_leases`); err != nil {
		t.Fatal(err)
	}
	handled := false
	for pass := 0; pass < 3 && !handled; pass++ {
		expired, _ := h.orchestrator.ExpiredJobs(h.ctx, 50)
		pending, _ := h.orchestrator.PendingJobs(h.ctx, 50)
		for _, job := range append(expired, pending...) {
			if job.InstanceID != instance.UUID {
				continue
			}
			if err := h.orchestrator.HandleJob(h.ctx, job); err == nil {
				handled = true
			}
		}
	}
	if !handled {
		t.Fatal("retry after the DB fault did not complete")
	}
	final := h.get(t, instance.UUID)
	if final.Status != StatusExpired || final.RemovedAt == nil {
		t.Fatalf("instance not released after the retry: %+v", final)
	}
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
}

// N1: the recovery path must call Destroy with the SAME persisted id, and a
// provider that reports the object already gone (404 -> nil) must still settle.
type countingDestroyDriver struct {
	fakeDriver
	ids    []string
	failOn int
	calls  int
}

func (d *countingDestroyDriver) Destroy(_ context.Context, _ runtime.Target, providerID string, _ runtime.Operation) error {
	d.calls++
	d.ids = append(d.ids, providerID)
	if d.failOn > 0 && d.calls == d.failOn {
		return &runtime.CapabilityError{Driver: d.Name(), Capability: "destroy", Detail: "injected"}
	}
	// A missing resource is success for this contract.
	return nil
}

func TestStuckDestroyRecoveryUsesSameProviderIDRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()
	driver := &countingDestroyDriver{}
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
	instance, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "same-id-recovery-01")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	const persistedID = "provider-abc-123"
	if _, err := db.Pool.Exec(ctx, `UPDATE instances SET status='DESTROYING' WHERE id=$1`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE instance_nodes SET status='DESTROYING', provider_resource_id=$2 WHERE instance_id=$1`,
		instance.UUID, persistedID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO instance_operations
			(id, instance_id, node_key, operation, generation, idempotency_key, status, authorized_context, finished_at)
		VALUES ($1,$2,'web','DESTROY',1,$3,'SUCCEEDED',$4,NOW())
	`, uuid.New(), instance.UUID, fmt.Sprintf("%s:web:1:DESTROY", instance.UUID),
		[]byte(`{"system":true,"permission":"system.expire"}`)); err != nil {
		t.Fatal(err)
	}

	// First attempt: the provider already removed it, but the DB fails afterwards.
	driver.failOn = 0
	handled := false
	for pass := 0; pass < 3 && !handled; pass++ {
		expired, _ := orchestrator.ExpiredJobs(ctx, 50)
		pending, _ := orchestrator.PendingJobs(ctx, 50)
		for _, job := range append(expired, pending...) {
			if job.InstanceID != instance.UUID {
				continue
			}
			if err := orchestrator.HandleJob(ctx, job); err == nil {
				handled = true
			}
		}
	}
	if !handled {
		t.Fatal("recovery did not settle")
	}
	if len(driver.ids) == 0 {
		t.Fatal("the recovery path never called Destroy")
	}
	for _, id := range driver.ids {
		if id != persistedID {
			t.Fatalf("recovery used id %q, want the persisted %q", id, persistedID)
		}
	}
	final, err := service.Get(ctx, actor, instance.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusExpired || final.RemovedAt == nil {
		t.Fatalf("instance not released: %+v", final)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
}

// N1: a partial job failure must never overwrite a queued cleanup operation.
func TestPartialFailureDoesNotClobberCleanupQueueRealPostgres(t *testing.T) {
	h := newLegacyHarness(t, Options{})
	instance := h.start(t, "partial-fail-0001")
	h.runJob(t)

	// A queued system DESTROY op exists (staged cleanup) for this node.
	if _, err := h.db.Pool.Exec(h.ctx, `
		UPDATE instance_nodes SET status='DESTROYING', provider_resource_id='fake-partial' WHERE instance_id=$1
	`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Pool.Exec(h.ctx, `
		INSERT INTO instance_operations
			(id, instance_id, node_key, operation, generation, idempotency_key, status, authorized_context)
		VALUES ($1,$2,'web','DESTROY',1,$3,'QUEUED',$4)
	`, uuid.New(), instance.UUID, fmt.Sprintf("%s:web:1:DESTROY", instance.UUID),
		[]byte(`{"system":true,"permission":"system.expire"}`)); err != nil {
		t.Fatal(err)
	}
	// A RESET job that fails must not mark that cleanup op FAILED.
	if _, err := h.db.Pool.Exec(h.ctx, `UPDATE instances SET status='RESETTING', generation=2 WHERE id=$1`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	resetOpID := uuid.New()
	if _, err := h.db.Pool.Exec(h.ctx, `
		INSERT INTO instance_operations
			(id, instance_id, node_key, operation, generation, idempotency_key, status, authorized_context)
		VALUES ($1,$2,'web','RESET',2,$3,'QUEUED',$4)
	`, resetOpID, instance.UUID, fmt.Sprintf("%s:web:2:RESET", instance.UUID),
		[]byte(`{"system":true,"permission":"system.expire"}`)); err != nil {
		t.Fatal(err)
	}
	// Fail the reset apply so the job takes the failure path.
	h.driver.failApply = true
	_ = h.orchestrator.HandleJob(h.ctx, Job{InstanceID: instance.UUID, Generation: 2, OperationIDs: []uuid.UUID{resetOpID}})

	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_operations WHERE instance_id=$1 AND operation='DESTROY' AND status='QUEUED'`, 1, instance.UUID)
	assertCount(t, h.db, `SELECT COUNT(*) FROM instance_operations WHERE id=$1 AND status='FAILED'`, 1, resetOpID)
}

// N1: a failed DESTROY must stay retryable, not become FAILED and strand the
// reservation. The provider object still exists, so the retry must use the same id.
func TestFailedDestroyStaysRetryableRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()
	driver := &countingDestroyDriver{failOn: 1}
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
	instance, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "failed-destroy-0001")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Ask for destroy; the first provider call fails.
	if _, err := service.ActionTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, ActionDestroy, "failed-destroy-key-01"); err != nil {
		t.Fatalf("destroy action: %v", err)
	}
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err == nil {
		t.Fatal("the injected provider failure must surface")
	}
	// The op must still be retryable and nothing released.
	assertCount(t, db, `SELECT COUNT(*) FROM instance_operations WHERE instance_id=$1 AND operation='DESTROY' AND status IN ('QUEUED','RUNNING')`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND removed_at IS NULL`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 1, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 1, instance.UUID)

	// The reconcile pass retries and releases exactly once.
	handled := false
	for pass := 0; pass < 3 && !handled; pass++ {
		expired, _ := orchestrator.ExpiredJobs(ctx, 50)
		pending, _ := orchestrator.PendingJobs(ctx, 50)
		for _, job := range append(expired, pending...) {
			if job.InstanceID != instance.UUID {
				continue
			}
			if err := orchestrator.HandleJob(ctx, job); err == nil {
				handled = true
			}
		}
	}
	if !handled {
		t.Fatal("the retry did not complete")
	}
	final, err := service.Get(ctx, actor, instance.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusExpired || final.RemovedAt == nil {
		t.Fatalf("instance not released after retry: %+v", final)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	if len(driver.ids) < 2 {
		t.Fatalf("expected two destroy attempts, got %v", driver.ids)
	}
}
