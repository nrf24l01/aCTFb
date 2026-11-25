package instances

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

// recordingDriver is a trusted fake provider that records exactly which provider
// resource IDs were destroyed, so a test can prove the admin lane actually reached
// the provider instead of only returning 2xx.
type recordingDriver struct {
	fakeDriver
	mu          sync.Mutex
	destroyedID []string
	appliedIDs  []string
}

func (d *recordingDriver) Apply(ctx context.Context, target runtime.Target, node deployment.Node, op runtime.Operation) (runtime.ProviderResource, error) {
	resource, err := d.fakeDriver.Apply(ctx, target, node, op)
	if err == nil {
		d.mu.Lock()
		d.appliedIDs = append(d.appliedIDs, resource.ProviderID)
		d.mu.Unlock()
	}
	return resource, err
}

func (d *recordingDriver) Destroy(ctx context.Context, target runtime.Target, providerID string, op runtime.Operation) error {
	d.mu.Lock()
	d.destroyedID = append(d.destroyedID, providerID)
	d.mu.Unlock()
	return d.fakeDriver.Destroy(ctx, target, providerID, op)
}

func (d *recordingDriver) destroyedIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.destroyedID...)
}

func (d *recordingDriver) appliedProviderIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.appliedIDs...)
}

// provisionParticipantInstance starts and provisions one instance for the
// fixture participant, returning its id and the provider resource id.
func provisionParticipantInstance(t *testing.T, svc *Service, orchestrator *Orchestrator, publisher *fakePublisher, driver *recordingDriver, ids seedIDs, key string) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	created, err := svc.StartTask(ctx, authorization.Principal{UserID: ids.UserID}, ids.BoardID, ids.ModuleID, ids.TaskID, key)
	if err != nil {
		t.Fatalf("participant start: %v", err)
	}
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err != nil {
		t.Fatalf("provision job: %v", err)
	}
	applied := driver.appliedProviderIDs()
	if len(applied) == 0 {
		t.Fatal("provider was not applied")
	}
	provisioned, err := svc.store.getInstance(ctx, created.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if provisioned.Status != StatusRunning {
		t.Fatalf("instance status after provision = %s, want RUNNING", provisioned.Status)
	}
	return created.UUID, applied[len(applied)-1]
}

// TestAdminDestroyLaneReachesProviderRealPostgres is the C1 regression: the admin
// instance lane must persist module.instances.admin in the queued
// AuthorizedContext so the worker revalidates the admin permission and actually
// calls the provider destroy for a non-participant admin / board owner.
func TestAdminDestroyLaneReachesProviderRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()

	sealer := testSealer(t)
	driver := &recordingDriver{}
	registry, err := runtime.NewRegistry(driver)
	if err != nil {
		t.Fatal(err)
	}
	auth := authorization.NewAuthorizer(db)
	publisher := &fakePublisher{}
	service, err := NewService(db, auth, registry, sealer, 1, WithPublisher(publisher))
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := NewOrchestrator(db, registry, auth, sealer, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Two staff actors, neither of them a participant on this board:
	//   * a global admin;
	//   * a board owner with an exact board binding.
	globalAdmin := seedUser(t, db, []string{"admin"})
	boardOwner := seedUser(t, db, []string{"user"})
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1,$2,'board_owner',$3)`, ids.BoardID, boardOwner, globalAdmin); err != nil {
		t.Fatalf("grant board owner: %v", err)
	}

	for name, actorID := range map[string]uuid.UUID{"global admin": globalAdmin, "board owner": boardOwner} {
		t.Run(name, func(t *testing.T) {
			var participant bool
			if err := db.Pool.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM board_participants WHERE board_id=$1 AND user_id=$2)
			`, ids.BoardID, actorID).Scan(&participant); err != nil {
				t.Fatal(err)
			}
			if participant {
				t.Fatal("fixture actor must not be a participant")
			}

			// Each actor destroys its own freshly provisioned instance, so no
			// state leaks between the two lanes. The previous actor's removed
			// instance is dropped first (one live instance per subject is a real
			// product rule).
			if _, err := db.Pool.Exec(ctx, `DELETE FROM instances WHERE board_id=$1`, ids.BoardID); err != nil {
				t.Fatalf("reset instances: %v", err)
			}
			instanceID, providerID := provisionParticipantInstance(t, service, orchestrator, publisher, driver, ids,
				"c1-admin-lane-"+actorID.String()[:8])

			action, err := service.AdminAction(ctx, authorization.Principal{UserID: actorID}, instanceID,
				ActionDestroy, "c1-admin-destroy-"+actorID.String()[:8])
			if err != nil {
				t.Fatalf("admin destroy: %v", err)
			}
			if action.Status == StatusRunning {
				t.Fatalf("destroy did not change status: %+v", action.Status)
			}

			// The queued operation must carry the admin lane, not a participant
			// permission.
			job := publisher.last(t)
			storedContext, err := orchestrator.store.operationContext(ctx, job.OperationIDs[0])
			if err != nil {
				t.Fatalf("load operation context: %v", err)
			}
			if storedContext.Permission != string(authorization.PermissionModuleInstancesAdmin) {
				t.Fatalf("stored permission = %q, want module.instances.admin", storedContext.Permission)
			}

			before := len(driver.destroyedIDs())
			if err := orchestrator.HandleJob(ctx, job); err != nil {
				t.Fatalf("admin destroy job: %v", err)
			}
			destroyed := driver.destroyedIDs()
			if len(destroyed) != before+1 {
				t.Fatalf("provider Destroy calls = %d, want %d (one per admin destroy)", len(destroyed), before+1)
			}
			if got := destroyed[len(destroyed)-1]; got != providerID {
				t.Fatalf("provider Destroy id = %q, want %q", got, providerID)
			}

			final, err := service.store.getInstance(ctx, instanceID)
			if err != nil {
				t.Fatal(err)
			}
			if final.Status != StatusExpired || final.RemovedAt == nil {
				t.Fatalf("instance after admin destroy = %s removed=%v, want EXPIRED", final.Status, final.RemovedAt)
			}
			var livePorts, liveLeases int
			if err := db.Pool.QueryRow(ctx, `
				SELECT count(*)::int FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`,
				instanceID).Scan(&livePorts); err != nil {
				t.Fatal(err)
			}
			if err := db.Pool.QueryRow(ctx, `
				SELECT count(*)::int FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`,
				instanceID).Scan(&liveLeases); err != nil {
				t.Fatal(err)
			}
			if livePorts != 0 || liveLeases != 0 {
				t.Fatalf("resources not released: ports=%d leases=%d", livePorts, liveLeases)
			}
			var nonTerminal int
			if err := db.Pool.QueryRow(ctx, `
				SELECT count(*)::int FROM instance_operations
				WHERE instance_id=$1 AND status NOT IN ('SUCCEEDED','FAILED','CANCELED')`,
				instanceID).Scan(&nonTerminal); err != nil {
				t.Fatal(err)
			}
			if nonTerminal != 0 {
				t.Fatalf("%d operation(s) not terminal after admin destroy", nonTerminal)
			}
		})
	}
}

// TestAdminRoleRevokedBeforeWorkerDeniesAndSystemCleans proves that a demoted
// admin cannot have a queued admin operation applied by the worker, and that the
// system expiry lane still reaps the resource afterwards.
func TestAdminRoleRevokedBeforeWorkerDeniesAndSystemCleans(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()

	sealer := testSealer(t)
	driver := &recordingDriver{}
	registry, err := runtime.NewRegistry(driver)
	if err != nil {
		t.Fatal(err)
	}
	auth := authorization.NewAuthorizer(db)
	publisher := &fakePublisher{}
	service, err := NewService(db, auth, registry, sealer, 1, WithPublisher(publisher))
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := NewOrchestrator(db, registry, auth, sealer, 1)
	if err != nil {
		t.Fatal(err)
	}

	participant := authorization.Principal{UserID: ids.UserID}
	created, err := service.StartTask(ctx, participant, ids.BoardID, ids.ModuleID, ids.TaskID, "c1-demote-start-0001")
	if err != nil {
		t.Fatalf("participant start: %v", err)
	}
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err != nil {
		t.Fatalf("provision job: %v", err)
	}
	applied := driver.appliedProviderIDs()
	if len(applied) != 1 {
		t.Fatalf("provider apply count = %d, want 1", len(applied))
	}
	providerID := applied[0]

	adminID := seedUser(t, db, []string{"admin"})
	if _, err := service.AdminAction(ctx, authorization.Principal{UserID: adminID}, created.UUID,
		ActionDestroy, "c1-demote-destroy-01"); err != nil {
		t.Fatalf("admin destroy: %v", err)
	}
	job := publisher.last(t)

	// Demote the admin before the worker runs.
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET roles=ARRAY['user']::text[] WHERE id=$1`, adminID); err != nil {
		t.Fatal(err)
	}
	before := len(driver.destroyedIDs())
	if err := orchestrator.HandleJob(ctx, job); err == nil {
		t.Fatal("worker applied an admin-queued destroy after the admin role was revoked")
	}
	if got := len(driver.destroyedIDs()); got != before {
		t.Fatalf("provider Destroy ran %d time(s) for a revoked admin", got-before)
	}

	// The system expiry lane is independent of actor authorization and still
	// cleans the resource up. The failed admin attempt left the row in ERROR with
	// terminal operations; simulate the normal overdue state the reconcile pass
	// selects (a live generation with no pending destroy).
	// Keep the generation: the instance node rows carry it, and a real overdue
	// instance is in the same generation as its nodes.
	if _, err := db.Pool.Exec(ctx, `
		UPDATE instances SET status='RUNNING', expires_at = NOW() - INTERVAL '1 minute', removed_at = NULL
		WHERE id=$1`, created.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		DELETE FROM instance_operations WHERE instance_id=$1`, created.UUID); err != nil {
		t.Fatal(err)
	}

	reaped := false
	for pass := 0; pass < 6 && !reaped; pass++ {
		// The worker reconciles two sources: newly due instances and operations
		// left queued/running by an interrupted pass.
		jobs, err := orchestrator.ExpiredJobs(ctx, 10)
		if err != nil {
			t.Fatalf("expired jobs: %v", err)
		}
		pending, err := orchestrator.PendingJobs(ctx, 10)
		if err != nil {
			t.Fatalf("pending jobs: %v", err)
		}
		jobs = append(jobs, pending...)
		for _, expiryJob := range jobs {
			if err := orchestrator.HandleJob(ctx, expiryJob); err != nil {
				// A multi-pass destroy legitimately reports "pending on another
				// node"; the next reconcile pass continues it.
				var apiErr *Error
				if errors.As(err, &apiErr) && apiErr.Code == CodeExpired {
					continue
				}
				t.Fatalf("system expiry job: %v", err)
			}
		}
		final, err := service.store.getInstance(ctx, created.UUID)
		if err != nil {
			t.Fatal(err)
		}
		reaped = final.Status == StatusExpired && final.RemovedAt != nil
	}
	if !reaped {
		final, _ := service.store.getInstance(ctx, created.UUID)
		t.Fatalf("system cleanup did not expire the instance: %s removed=%v", final.Status, final.RemovedAt)
	}
	destroyed := driver.destroyedIDs()
	if len(destroyed) == 0 || destroyed[len(destroyed)-1] != providerID {
		t.Fatalf("system cleanup did not destroy provider resource %q: %v", providerID, destroyed)
	}
}
