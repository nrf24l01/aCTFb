package instances

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/deployment"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/runtime"
)

type fakeDriver struct {
	applied   int
	destroyed int
	failApply bool
}

func (f *fakeDriver) Name() string          { return deployment.DriverDocker }
func (f *fakeDriver) Kind() deployment.Kind { return deployment.KindContainer }
func (f *fakeDriver) Capabilities(context.Context, runtime.Target) (deployment.Capabilities, error) {
	return deployment.Capabilities{Kind: deployment.KindContainer}, nil
}
func (f *fakeDriver) Validate(context.Context, runtime.Target, deployment.Node) []deployment.Finding {
	return nil
}
func (f *fakeDriver) Plan(context.Context, runtime.Target, deployment.Node, *runtime.ProviderResource) (runtime.Plan, error) {
	return runtime.Plan{Action: "CREATE"}, nil
}
func (f *fakeDriver) Apply(_ context.Context, target runtime.Target, _ deployment.Node, op runtime.Operation) (runtime.ProviderResource, error) {
	if f.failApply {
		return runtime.ProviderResource{}, &runtime.CapabilityError{Driver: f.Name(), Capability: "test"}
	}
	f.applied++
	settings, err := runtime.DecodeTargetSettings(target.Settings)
	if err != nil || settings.Allocation == nil {
		return runtime.ProviderResource{}, err
	}
	binding := settings.Allocation.Ports["web"]
	return runtime.ProviderResource{
		ProviderID: "fake-" + op.InstanceID.String()[:8],
		State:      runtime.NodeStateRunning,
		Endpoints: []runtime.ProviderEndpoint{{
			Key: "web", Type: "http", Host: binding.Host, Port: binding.Port, Exposure: deployment.ExposurePublic,
		}},
	}, nil
}
func (f *fakeDriver) Inspect(context.Context, runtime.Target, string) (runtime.ProviderResource, error) {
	return runtime.ProviderResource{State: runtime.NodeStateRunning}, nil
}
func (f *fakeDriver) Destroy(context.Context, runtime.Target, string, runtime.Operation) error {
	f.destroyed++
	return nil
}

type fakePublisher struct{ jobs []Job }

func (p *fakePublisher) PublishJob(_ context.Context, job Job) error {
	p.jobs = append(p.jobs, job)
	return nil
}

func (p *fakePublisher) last(t *testing.T) Job {
	t.Helper()
	if len(p.jobs) == 0 {
		t.Fatal("no job published")
	}
	return p.jobs[len(p.jobs)-1]
}

func testSealer(t *testing.T) secrets.Sealer {
	t.Helper()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	sealer, err := secrets.NewCipher(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	return sealer
}

func TestInstanceLifecycleRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()
	sealer := testSealer(t)
	driver := &fakeDriver{}
	registry, err := runtime.NewRegistry(driver)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &fakePublisher{}
	auth := authorization.NewAuthorizer(db)
	service, err := NewService(db, auth, registry, sealer, 1, WithPublisher(publisher))
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{UserID: ids.UserID}
	created, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "idempotency-key-0001")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if created.Status != StatusProvisioning || created.Generation != 1 || len(created.Nodes) != 1 {
		t.Fatalf("unexpected instance: %+v", created)
	}
	if created.ExpiresAt == nil || !created.ExpiresAt.After(time.Now()) {
		t.Fatalf("expiry not set: %+v", created.ExpiresAt)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 1, created.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL AND allocated_port BETWEEN 41000 AND 41020`, 1, created.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_operations WHERE instance_id=$1 AND status='QUEUED' AND operation='CREATE'`, 1, created.UUID)

	// Idempotent replay returns the same instance and publishes no second job.
	again, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "idempotency-key-0001")
	if err != nil {
		t.Fatalf("idempotent start: %v", err)
	}
	if again.UUID != created.UUID {
		t.Fatalf("idempotent start created a second instance")
	}

	orchestrator, err := NewOrchestrator(db, registry, auth, sealer, 1)
	if err != nil {
		t.Fatal(err)
	}
	job := publisher.last(t)
	if err := orchestrator.HandleJob(ctx, job); err != nil {
		t.Fatalf("apply job: %v", err)
	}
	running, err := service.Get(ctx, actor, created.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != StatusRunning || running.Nodes[0].Status != "RUNNING" || running.Nodes[0].ProviderID == "" {
		t.Fatalf("instance did not reach RUNNING: %+v", running)
	}
	if len(running.Nodes[0].Endpoints) != 1 || running.Nodes[0].Endpoints[0].Host != "127.0.0.1" {
		t.Fatalf("endpoint not persisted: %+v", running.Nodes[0].Endpoints)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM events_outbox WHERE type='instance.created'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_secret_values WHERE instance_id=$1 AND key='password'`, 1, created.UUID)

	// A redelivered job is a no-op.
	if err := orchestrator.HandleJob(ctx, job); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if driver.applied != 1 {
		t.Fatalf("redelivery applied the driver twice: %d", driver.applied)
	}

	reset, err := service.ActionTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, ActionReset, "idempotency-key-reset-1")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if reset.Status != StatusResetting || reset.Generation != 2 {
		t.Fatalf("reset did not advance generation: %+v", reset)
	}
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err != nil {
		t.Fatalf("reset job: %v", err)
	}
	resetDone, err := service.Get(ctx, actor, created.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if resetDone.Status != StatusRunning || resetDone.Generation != 2 {
		t.Fatalf("reset did not finish: %+v", resetDone)
	}

	destroyed, err := service.ActionTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, ActionDestroy, "idempotency-key-destroy-1")
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if destroyed.Status != StatusDestroying {
		t.Fatalf("destroy status: %s", destroyed.Status)
	}
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err != nil {
		t.Fatalf("destroy job: %v", err)
	}
	final, err := service.Get(ctx, actor, created.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusExpired || final.RemovedAt == nil {
		t.Fatalf("destroy did not release the instance: %+v", final)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 0, created.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 0, created.UUID)
	if driver.destroyed == 0 {
		t.Fatal("driver destroy was not called")
	}
	if len(final.Nodes[0].Endpoints) != 0 {
		t.Fatalf("released endpoints must not be returned: %+v", final.Nodes[0].Endpoints)
	}
}

func TestRemovedParticipantLosesInstanceReadRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()
	playerID := seedUser(t, db, []string{"user"})
	if _, err := db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id,user_id) VALUES ($1,$2)`, ids.BoardID, playerID); err != nil {
		t.Fatal(err)
	}
	sealer := testSealer(t)
	driver := &fakeDriver{}
	registry, _ := runtime.NewRegistry(driver)
	service, err := NewService(db, authorization.NewAuthorizer(db), registry, sealer, 1, WithPublisher(&fakePublisher{}))
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{UserID: playerID}
	created, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "removed-read-key-01")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := service.Get(ctx, actor, created.UUID); err != nil {
		t.Fatalf("owner read before removal: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM board_participants WHERE board_id=$1 AND user_id=$2`, ids.BoardID, playerID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, actor, created.UUID); err == nil {
		t.Fatal("removed participant still read the instance and its endpoints")
	}
}

func TestInstanceLifecycleNATSPublisherRealPostgres(t *testing.T) {
	host := os.Getenv("ACTFB_TEST_NATS_HOST")
	portText := os.Getenv("ACTFB_TEST_NATS_PORT")
	if host == "" || portText == "" {
		t.Skip("set ACTFB_TEST_NATS_HOST/ACTFB_TEST_NATS_PORT for the NATS-backed instance test")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("ACTFB_TEST_NATS_PORT: %v", err)
	}
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sealer := testSealer(t)
	registry, err := runtime.NewRegistry(&fakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	natsClient, err := platformnats.New(config.NatsConfig{Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = natsClient.Close(closeCtx)
	})
	if _, err := natsClient.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: JobStream, Subjects: []string{JobSubject}, Storage: jetstream.FileStorage,
	}); err != nil {
		t.Fatal(err)
	}
	jobs := make(chan Job, 1)
	consumer, err := natsClient.SubscribeJetStream(ctx, JobStream, jetstream.ConsumerConfig{
		Durable:       "instance-test-" + uuid.NewString()[:8],
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		FilterSubject: JobSubject,
	}, func(message jetstream.Msg) {
		var job Job
		if err := json.Unmarshal(message.Data(), &job); err == nil {
			select {
			case jobs <- job:
			default:
			}
		}
		_ = message.Ack()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Stop()

	auth := authorization.NewAuthorizer(db)
	service, err := NewService(db, auth, registry, sealer, 1, WithPublisher(NewNATSJobPublisher(natsClient)))
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{UserID: ids.UserID}
	created, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "nats-idempotency-0001")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var job Job
	select {
	case job = <-jobs:
	case <-time.After(10 * time.Second):
		t.Fatal("instance job was not delivered through JetStream")
	}
	if job.InstanceID != created.UUID {
		t.Fatalf("NATS job instance id = %s, want %s", job.InstanceID, created.UUID)
	}
	orchestrator, err := NewOrchestrator(db, registry, auth, sealer, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.HandleJob(ctx, job); err != nil {
		t.Fatalf("handle NATS job: %v", err)
	}
	running, err := service.Get(ctx, actor, created.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != StatusRunning {
		t.Fatalf("instance status after NATS job = %s, want RUNNING", running.Status)
	}
}

func TestInstanceJobRejectsForeignActor(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()
	sealer := testSealer(t)
	driver := &fakeDriver{}
	registry, _ := runtime.NewRegistry(driver)
	auth := authorization.NewAuthorizer(db)
	publisher := &fakePublisher{}
	service, err := NewService(db, auth, registry, sealer, 1, WithPublisher(publisher))
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{UserID: ids.UserID}
	created, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "idempotency-key-foreign")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a tampered NATS payload: the operation context is loaded from the
	// DB, so replacing the actor id in the job cannot escalate anything. The
	// worker must also revalidate live participant eligibility, so removing the
	// participant row after the job was queued has to block the provider call.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM board_participants WHERE board_id=$1 AND user_id=$2`, ids.BoardID, ids.UserID); err != nil {
		t.Fatal(err)
	}
	orchestrator, _ := NewOrchestrator(db, registry, auth, sealer, 1)
	err = orchestrator.HandleJob(ctx, publisher.last(t))
	if err == nil {
		t.Fatal("worker must recheck live eligibility and refuse")
	}
	if driver.applied != 0 {
		t.Fatal("provider must not be called when authorization fails")
	}
	status, loadErr := service.store.getInstance(ctx, created.UUID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if status.Status != StatusError {
		t.Fatalf("failed instance must be ERROR, got %s", status.Status)
	}
}

func TestExpiredInstanceCleanupRealPostgres(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	ctx := context.Background()
	sealer := testSealer(t)
	driver := &fakeDriver{}
	registry, _ := runtime.NewRegistry(driver)
	auth := authorization.NewAuthorizer(db)
	publisher := &fakePublisher{}
	service, _ := NewService(db, auth, registry, sealer, 1, WithPublisher(publisher))
	actor := authorization.Principal{UserID: ids.UserID}
	created, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "idempotency-key-expiry")
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, _ := NewOrchestrator(db, registry, auth, sealer, 1)
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE instances SET expires_at = NOW() - INTERVAL '1 minute' WHERE id=$1`, created.UUID); err != nil {
		t.Fatal(err)
	}
	jobs, err := orchestrator.ExpiredJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected one expiry job, got %d", len(jobs))
	}
	if err := orchestrator.HandleJob(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	final, err := service.Get(ctx, actor, created.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusExpired || final.RemovedAt == nil {
		t.Fatalf("expired instance not cleaned: %+v", final)
	}
}

func assertCount(t *testing.T, db *postgres.DB, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := db.Pool.QueryRow(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("count %q = %d, want %d", query, got, want)
	}
}

var _ = json.Marshal
var _ = uuid.Nil
