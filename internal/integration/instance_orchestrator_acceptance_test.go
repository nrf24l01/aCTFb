package integration

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/pressly/goose/v3"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/instances"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/runtime/containerapi"
	runtimedocker "github.com/nrf24l01/actfb/internal/runtime/docker"
)

// TestInstanceOrchestratorNATSDockerIntegration is one end-to-end acceptance
// path: real PostgreSQL -> instances service -> durable NATS JetStream ->
// orchestrator -> real Docker Engine -> HTTP endpoint -> reset -> snapshot
// allowlist -> TTL expiry -> idempotent destroy retry. No fake driver or fake
// publisher is involved.
func TestInstanceOrchestratorNATSDockerIntegration(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN for the real instance orchestrator test")
	}
	natsHost := os.Getenv("ACTFB_TEST_NATS_HOST")
	natsPortText := os.Getenv("ACTFB_TEST_NATS_PORT")
	if natsHost == "" || natsPortText == "" {
		t.Skip("set ACTFB_TEST_NATS_HOST/ACTFB_TEST_NATS_PORT for the real instance orchestrator test")
	}
	natsPort, err := strconv.Atoi(natsPortText)
	if err != nil {
		t.Fatalf("ACTFB_TEST_NATS_PORT: %v", err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not installed")
	}
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Skip("docker daemon is not available")
	}
	imageOutput, err := exec.Command("docker", "image", "inspect", "nginx:alpine", "--format", "{{index .RepoDigests 0}}").Output()
	if err != nil {
		t.Skip("nginx:alpine fixture image is not present locally")
	}
	image := strings.TrimSpace(string(imageOutput))
	if image == "" {
		t.Skip("nginx:alpine fixture image has no repository digest")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, cleanupDB := openInstanceAcceptanceDB(t, ctx, dsn)
	defer cleanupDB()
	ids := seedInstanceAcceptanceFixture(t, ctx, db, image)

	natsClient, err := platformnats.New(config.NatsConfig{Host: natsHost, Port: natsPort})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = natsClient.Close(closeCtx)
	}()
	if _, err := natsClient.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: instances.JobStream, Subjects: []string{instances.JobSubject}, Storage: jetstream.FileStorage,
	}); err != nil {
		t.Fatal(err)
	}
	jobs := make(chan instances.Job, 8)
	consumer, err := natsClient.SubscribeJetStream(ctx, instances.JobStream, jetstream.ConsumerConfig{
		Durable:       "inst-orch-accept-" + uuid.NewString()[:8],
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		FilterSubject: instances.JobSubject,
	}, func(message jetstream.Msg) {
		var job instances.Job
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

	forwarders := containerapi.NewForwarderRegistry()
	defer forwarders.StopAll()
	dockerDriver, err := runtimedocker.New(runtimedocker.WithForwarders(forwarders))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := runtime.NewRegistry(dockerDriver)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := secrets.NewCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), 1)
	if err != nil {
		t.Fatal(err)
	}
	authz := authorization.NewAuthorizer(db)
	service, err := instances.NewService(db, authz, registry, sealer, 1,
		instances.WithPublisher(instances.NewNATSJobPublisher(natsClient)),
		instances.WithOptions(instances.Options{DefaultTTL: 6 * time.Second, MaxTTL: time.Minute}),
	)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := instances.NewOrchestrator(db, registry, authz, sealer, 1)
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{UserID: ids.userID}
	created, err := service.StartTask(ctx, actor, ids.boardID, ids.moduleID, ids.taskID, "orch-nats-docker-0001")
	if err != nil {
		t.Fatalf("start instance: %v", err)
	}
	defer cleanupDockerInstance(created.UUID)

	firstJob := nextInstanceJob(t, jobs)
	if firstJob.InstanceID != created.UUID {
		t.Fatalf("first job instance = %s, want %s", firstJob.InstanceID, created.UUID)
	}
	if err := orchestrator.HandleJob(ctx, firstJob); err != nil {
		t.Fatalf("apply create job: %v", err)
	}
	running := waitInstanceStatus(t, ctx, service, actor, created.UUID, instances.StatusRunning)
	markerURL := publicMarkerURL(t, running)
	waitHTTPBody(t, markerURL, "gen-1")

	// Snapshot allowlist: revoking the allowlist after start must not break the
	// queued reset for the already-frozen snapshot, but must block a new start.
	if _, err := db.Pool.Exec(ctx, `UPDATE deployment_template_allowlist SET revoked_at=NOW() WHERE id=$1`, ids.allowlistID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, actor, ids.boardID, ids.moduleID, ids.taskID, "orch-nats-docker-0002"); err == nil {
		t.Fatal("new start succeeded after allowlist revocation")
	}

	reset, err := service.ActionTask(ctx, actor, ids.boardID, ids.moduleID, ids.taskID, instances.ActionReset, "orch-nats-docker-reset")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	resetJob := nextInstanceJob(t, jobs)
	if resetJob.Generation != reset.Generation {
		t.Fatalf("reset job generation = %d, want %d", resetJob.Generation, reset.Generation)
	}
	if err := orchestrator.HandleJob(ctx, resetJob); err != nil {
		t.Fatalf("apply reset job: %v", err)
	}
	resetRunning := waitInstanceStatus(t, ctx, service, actor, created.UUID, instances.StatusRunning)
	if resetRunning.Nodes[0].ProviderID == running.Nodes[0].ProviderID {
		t.Fatal("reset did not replace the provider resource")
	}
	waitHTTPBody(t, publicMarkerURL(t, resetRunning), "gen-1")

	// TTL expiry produces a durable destroy job; apply it twice to prove the
	// orchestrator/driver destroy retry is idempotent.
	time.Sleep(7 * time.Second)
	expiredJobs, err := orchestrator.ExpiredJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(expiredJobs) == 0 {
		t.Fatal("TTL expiry produced no destroy job")
	}
	for _, job := range expiredJobs {
		if err := orchestrator.HandleJob(ctx, job); err != nil {
			t.Fatalf("apply expiry destroy: %v", err)
		}
		if err := orchestrator.HandleJob(ctx, job); err != nil {
			t.Fatalf("destroy retry was not idempotent: %v", err)
		}
	}
	expired := waitInstanceStatus(t, ctx, service, actor, created.UUID, instances.StatusExpired)
	if expired.RemovedAt == nil {
		t.Fatal("expired instance has no removed_at")
	}
	if out, err := exec.Command("docker", "ps", "-aq", "--filter", "name=actfb-"+created.UUID.String()).Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		t.Fatalf("destroy left containers: %s", strings.TrimSpace(string(out)))
	}
}

type instanceAcceptanceIDs struct {
	userID        uuid.UUID
	boardID       uuid.UUID
	moduleID      uuid.UUID
	taskID        uuid.UUID
	integrationID uuid.UUID
	templateID    uuid.UUID
	versionID     uuid.UUID
	bindingID     uuid.UUID
	allowlistID   uuid.UUID
}

func openInstanceAcceptanceDB(t *testing.T, ctx context.Context, dsn string) (*postgres.DB, func()) {
	t.Helper()
	schema := "ia_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	root := repositoryRoot(t)
	schemaDSN := dsn
	if strings.Contains(schemaDSN, "?") {
		schemaDSN += "&search_path=" + schema
	} else {
		schemaDSN += "?search_path=" + schema
	}
	sqlDB, err := sql.Open("pgx", schemaDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(sqlDB, filepath.Join(root, "migrations")); err != nil {
		t.Fatal(err)
	}
	poolConfig, err := pgxpool.ParseConfig(schemaDSN)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	db := &postgres.DB{Pool: pool, SQL: stdlib.OpenDBFromPool(pool)}
	return db, func() {
		pool.Close()
		_ = sqlDB.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		_ = admin.Close()
	}
}

func seedInstanceAcceptanceFixture(t *testing.T, ctx context.Context, db *postgres.DB, image string) instanceAcceptanceIDs {
	t.Helper()
	ids := instanceAcceptanceIDs{
		userID: uuid.New(), boardID: uuid.New(), moduleID: uuid.New(), taskID: uuid.New(),
		integrationID: uuid.New(), templateID: uuid.New(), versionID: uuid.New(),
		bindingID: uuid.New(), allowlistID: uuid.New(),
	}
	now := time.Now().UTC()
	exec := func(query string, args ...any) {
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO users (id, username, email, password, roles) VALUES ($1,$2,$3,'x',ARRAY['user','admin']::text[])`,
		ids.userID, "orch-"+ids.userID.String()[:8], ids.userID.String()[:8]+"@example.test")
	exec(`INSERT INTO boards (id, slug, name, participation_mode, registration_mode, visibility, status, starts_at, ends_at, created_by)
		VALUES ($1,$2,'Orch Board','INDIVIDUAL','OPEN','PRIVATE','ACTIVE',$3,$4,$5)`,
		ids.boardID, "orch-"+ids.boardID.String()[:8], now.Add(-time.Hour), now.Add(time.Hour), ids.userID)
	exec(`INSERT INTO modules (id, board_id, name, type, allowed_task_types, created_by)
		VALUES ($1,$2,'Instances','TASKS',ARRAY['INSTANCE']::text[],$3)`, ids.moduleID, ids.boardID, ids.userID)
	exec(`INSERT INTO tasks (id, board_id, module_id, title, category, type, points, submission_mode, submission_checker, instance_enabled, created_by)
		VALUES ($1,$2,$3,'Instance task','web','INSTANCE',10,'TEXT','MANUAL',TRUE,$4)`, ids.taskID, ids.boardID, ids.moduleID, ids.userID)
	dockerSocket := os.Getenv("ACTFB_TEST_DOCKER_SOCKET")
	if dockerSocket == "" {
		dockerSocket = "/var/run/docker.sock"
	}
	imageRepo := strings.SplitN(image, "@", 2)[0]
	registryHost := strings.SplitN(imageRepo, "/", 2)[0]
	if strings.Contains(imageRepo, "/") && !strings.ContainsAny(registryHost, ".:") {
		registryHost = "docker.io"
	}
	settings := []byte(fmt.Sprintf(`{"socket":%q,"registryAllowlist":[%q],"dedicatedNode":"orch-int","dedicatedNodeVerified":true,"pidsLimit":64,"network":{"kind":"network"}}`, dockerSocket, registryHost))
	publication := []byte(`{"publicTcpHost":"127.0.0.1","publicPortFrom":43000,"publicPortTo":43020}`)
	exec(`INSERT INTO integrations (id, category, type, kind, driver, name, base_url, credentials_ciphertext, credentials_key_version, settings, publication_policy, status, created_by)
		VALUES ($1,'COMPUTE','DOCKER','CONTAINER','docker',$2,'',NULL,NULL,$3,$4,'UNKNOWN',$5)`,
		ids.integrationID, "docker-"+ids.integrationID.String()[:8], settings, publication, ids.userID)
	manifest := fmt.Sprintf(`{"apiVersion":"actfb.dev/v1alpha1","kind":"DeploymentTemplate","metadata":{"name":"Orch","slug":"orch","description":""},"nodes":[{"key":"web","name":"web","kind":"CONTAINER","source":{"type":"OCI_IMAGE","ref":%q},"resources":{"cpuMillicores":200,"memoryMiB":64,"diskMiB":64},"files":[{"source":"files/nginx.conf","target":"/etc/nginx/conf.d/default.conf"},{"source":"files/marker","target":"/usr/share/nginx/html/marker"}],"endpoints":[{"key":"web","protocol":"http","targetPort":8080,"exposure":"PUBLIC"}],"driverOptions":{"docker":{"readOnlyRootFilesystem":true,"user":"101:101","userNumeric":true,"entrypoint":["nginx"],"cmd":["-g","daemon off;"],"tmpfs":{"/tmp":8,"/var/cache/nginx":16,"/var/run":8}}}}],"lifecycle":{"reset":"RECREATE"}}`, image)
	nginxConfig := "server {\n  listen 8080;\n  root /usr/share/nginx/html;\n  location / { try_files $uri =404; }\n}\n"
	files := []map[string]string{
		{"path": "files/nginx.conf", "contentBase64": base64.StdEncoding.EncodeToString([]byte(nginxConfig))},
		{"path": "files/marker", "contentBase64": base64.StdEncoding.EncodeToString([]byte("gen-1"))},
	}
	filesJSON, _ := json.Marshal(files)
	exec(`INSERT INTO deployment_templates (id, slug, name, created_by) VALUES ($1,$2,'Orch',$3)`,
		ids.templateID, "orch-"+ids.templateID.String()[:8], ids.userID)
	exec(`INSERT INTO deployment_template_versions (id, template_id, version, digest, manifest, files, published_by)
		VALUES ($1,$2,1,'digest-1',$3,$4,$5)`, ids.versionID, ids.templateID, []byte(manifest), filesJSON, ids.userID)
	exec(`INSERT INTO deployment_bindings (id, board_id, module_id, task_id, template_version_id, parameters, lifecycle, created_by)
		VALUES ($1,$2,$3,$4,$5,'{}','{}',$6)`, ids.bindingID, ids.boardID, ids.moduleID, ids.taskID, ids.versionID, ids.userID)
	exec(`UPDATE tasks SET deployment_binding_id=$2 WHERE id=$1`, ids.taskID, ids.bindingID)
	exec(`INSERT INTO deployment_binding_targets (binding_id, kind, integration_id) VALUES ($1,'CONTAINER',$2)`, ids.bindingID, ids.integrationID)
	exec(`INSERT INTO deployment_template_allowlist (id, template_version_id, board_id, module_id, approved_by)
		VALUES ($1,$2,$3,$4,$5)`, ids.allowlistID, ids.versionID, ids.boardID, ids.moduleID, ids.userID)
	exec(`INSERT INTO deployment_template_allowlist_targets (allowlist_id, kind, integration_id) VALUES ($1,'CONTAINER',$2)`, ids.allowlistID, ids.integrationID)
	return ids
}

func nextInstanceJob(t *testing.T, jobs <-chan instances.Job) instances.Job {
	t.Helper()
	select {
	case job := <-jobs:
		return job
	case <-time.After(15 * time.Second):
		t.Fatal("instance job was not delivered through JetStream")
		return instances.Job{}
	}
}

func waitInstanceStatus(t *testing.T, ctx context.Context, service *instances.Service, actor authorization.Principal, id uuid.UUID, want instances.Status) *instances.Instance {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last *instances.Instance
	for time.Now().Before(deadline) {
		instance, err := service.Get(ctx, actor, id)
		if err == nil {
			last = instance
			if instance.Status == want {
				return instance
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("instance %s did not reach %s; last=%+v", id, want, last)
	return nil
}

func publicMarkerURL(t *testing.T, instance *instances.Instance) string {
	t.Helper()
	for _, node := range instance.Nodes {
		for _, endpoint := range node.Endpoints {
			if endpoint.Exposure == deployment.ExposurePublic {
				host := endpoint.Host
				if host == "" || host == "0.0.0.0" {
					host = "127.0.0.1"
				}
				return "http://" + host + ":" + strconv.Itoa(endpoint.Port) + "/marker"
			}
		}
	}
	t.Fatalf("instance has no public endpoint: %+v", instance)
	return ""
}

func waitHTTPBody(t *testing.T, url, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			last = string(body)
			if response.StatusCode == http.StatusOK && strings.TrimSpace(last) == want {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("HTTP %s did not return %q; last=%q", url, want, last)
}

func cleanupDockerInstance(id uuid.UUID) {
	name := "actfb-" + id.String()
	out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name="+name).Output()
	for _, container := range strings.Fields(string(out)) {
		_ = exec.Command("docker", "rm", "-f", container).Run()
	}
	_ = exec.Command("docker", "network", "rm", name).Run()
}
