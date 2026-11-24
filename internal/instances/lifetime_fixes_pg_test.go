package instances

import (
	"context"
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
