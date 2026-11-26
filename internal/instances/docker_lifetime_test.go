package instances

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/runtime/containerapi"
	"github.com/nrf24l01/actfb/internal/runtime/docker"
)

// dockerFixtureImage is the pinned workload image. It must already be present
// locally; the test skips otherwise so it never pulls over the network.
const dockerFixtureImage = "alpine:3.23"

// seedDockerFixture creates the minimum rows needed for a real Docker run with
// an administrator-verified dedicated worker node.
func seedDockerFixture(t *testing.T, db *postgres.DB, image string) seedIDs {
	t.Helper()
	ctx := context.Background()
	ids := seedIDs{
		UserID: uuid.New(), BoardID: uuid.New(), ModuleID: uuid.New(), TaskID: uuid.New(),
		IntegrationID: uuid.New(), TemplateID: uuid.New(), VersionID: uuid.New(), BindingID: uuid.New(),
	}
	now := time.Now().UTC()
	exec := func(query string, args ...any) {
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	exec(`INSERT INTO users (id, username, email, password, roles) VALUES ($1,$2,$3,'x',ARRAY['admin']::text[])`,
		ids.UserID, "admin-"+ids.UserID.String()[:8], ids.UserID.String()[:8]+"@example.test")
	exec(`INSERT INTO boards (id, slug, name, participation_mode, registration_mode, visibility, status, starts_at, ends_at, created_by)
	      VALUES ($1,$2,'Board','INDIVIDUAL','OPEN','PUBLIC','ACTIVE',$3,$4,$5)`,
		ids.BoardID, "b-"+ids.BoardID.String()[:8], now.Add(-time.Hour), now.Add(24*time.Hour), ids.UserID)
	// StartTask is a participant-own action, so the acting user must really be a
	// board participant: the live ABAC policy checks board_participants and the
	// core matrix grants instance.start_own only from that membership. Seeding
	// the membership is the fixture condition; production authorization is not
	// relaxed anywhere.
	exec(`INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2)`, ids.BoardID, ids.UserID)
	exec(`INSERT INTO modules (id, board_id, name, type, allowed_task_types, created_by)
	      VALUES ($1,$2,'Tasks','TASKS',ARRAY['INSTANCE']::text[],$3)`,
		ids.ModuleID, ids.BoardID, ids.UserID)
	exec(`INSERT INTO tasks (id, board_id, module_id, title, category, type, points, submission_mode, submission_checker, instance_enabled, created_by)
	      VALUES ($1,$2,$3,'Task','cat','INSTANCE',100,'TEXT','MANUAL',TRUE,$4)`,
		ids.TaskID, ids.BoardID, ids.ModuleID, ids.UserID)
	settings := `{"socket":"/var/run/docker.sock","registryAllowlist":["alpine"],"dedicatedNode":"docker-worker-lifetime","dedicatedNodeVerified":true,"pidsLimit":64,"network":{"kind":"network"}}`
	exec(`INSERT INTO integrations (id, category, type, kind, driver, name, base_url, credentials_ciphertext, credentials_key_version, settings, publication_policy, status, created_by)
	      VALUES ($1,'COMPUTE','DOCKER','CONTAINER','docker',$2,'',NULL,NULL,$3,$4,'UNKNOWN',$5)`,
		ids.IntegrationID, "docker-"+ids.IntegrationID.String()[:8], []byte(settings),
		[]byte(`{"publicTcpHost":"127.0.0.1","publicPortFrom":43100,"publicPortTo":43120}`), ids.UserID)
	exec(`INSERT INTO deployment_templates (id, slug, name, created_by) VALUES ($1,$2,'tmpl',$3)`,
		ids.TemplateID, "tmpl-"+ids.TemplateID.String()[:8], ids.UserID)
	manifest := fmt.Sprintf(`{"apiVersion":"actfb.dev/v1alpha1","kind":"DeploymentTemplate","metadata":{"name":"t","slug":"t","description":""},"nodes":[{"key":"web","name":"web","kind":"CONTAINER","source":{"type":"OCI_IMAGE","ref":%q},"resources":{"cpuMillicores":100,"memoryMiB":64,"diskMiB":32},"endpoints":[{"key":"web","protocol":"http","targetPort":8080,"exposure":"PUBLIC"}],"driverOptions":{"docker":{"cmd":["sleep","600"],"tmpfs":{"/tmp":8}}}}]}`, image)
	exec(`INSERT INTO deployment_template_versions (id, template_id, version, digest, manifest, files, published_by)
	      VALUES ($1,$2,1,'digest-docker',$3,'[]',$4)`,
		ids.VersionID, ids.TemplateID, []byte(manifest), ids.UserID)
	exec(`INSERT INTO deployment_bindings (id, board_id, module_id, task_id, template_version_id, parameters, lifecycle, created_by)
	      VALUES ($1,$2,$3,$4,$5,'{}','{}',$6)`,
		ids.BindingID, ids.BoardID, ids.ModuleID, ids.TaskID, ids.VersionID, ids.UserID)
	exec(`UPDATE tasks SET deployment_binding_id = $2 WHERE id = $1`, ids.TaskID, ids.BindingID)
	exec(`INSERT INTO deployment_binding_targets (binding_id, kind, integration_id) VALUES ($1,'CONTAINER',$2)`,
		ids.BindingID, ids.IntegrationID)
	var allowlistID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `INSERT INTO deployment_template_allowlist (template_version_id, board_id, module_id, approved_by)
	      VALUES ($1,$2,$3,$4) RETURNING id`, ids.VersionID, ids.BoardID, ids.ModuleID, ids.UserID).Scan(&allowlistID); err != nil {
		t.Fatalf("seed allowlist: %v", err)
	}
	exec(`INSERT INTO deployment_template_allowlist_targets (allowlist_id, kind, integration_id) VALUES ($1,'CONTAINER',$2)`,
		allowlistID, ids.IntegrationID)
	exec(`UPDATE modules SET task_instance_settings = $2 WHERE id = $1`, ids.ModuleID,
		[]byte(`{"defaultTtlSeconds":3600,"maxTtlSeconds":7200,"maxConcurrentInstancesPerSubject":2,"resetEnabled":true,"extension":{"enabled":true,"seconds":900,"maxCount":3}}`))
	return ids
}

// dockerFixtureDigest returns the local repository digest for the fixture image.
func dockerFixtureDigest(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("docker", "image", "inspect", dockerFixtureImage, "--format", "{{index .RepoDigests 0}}").Output()
	if err != nil {
		t.Skipf("fixture image %s is not present locally", dockerFixtureImage)
	}
	digest := strings.TrimSpace(string(output))
	if digest == "" {
		t.Skipf("fixture image %s has no repository digest", dockerFixtureImage)
	}
	return digest
}

// dockerContainersFor returns the container ids carrying the exact instance
// label. Cleanup is always scoped to this instance, never a broad prune.
func dockerContainersFor(instanceID uuid.UUID) []string {
	output, err := exec.Command("docker", "ps", "-aq", "--filter",
		"label=actfb.instance="+instanceID.String()).Output()
	if err != nil {
		return nil
	}
	ids := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids
}

// TestRealDockerExpiryCleanupAndExtend drives the real Docker driver through the
// instance service: start, extend over HTTP-equivalent service calls, then expire
// and confirm the container, endpoint and lease are gone.
func TestRealDockerExpiryCleanupAndExtend(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not installed")
	}
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Skip("docker daemon is not available")
	}
	image := dockerFixtureDigest(t)

	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedDockerFixture(t, db, image)
	ctx := context.Background()

	forwarders := containerapi.NewForwarderRegistry()
	defer forwarders.StopAll()
	driver, err := docker.New(docker.WithForwarders(forwarders))
	if err != nil {
		t.Fatalf("docker driver: %v", err)
	}
	registry, err := runtime.NewRegistry(driver)
	if err != nil {
		t.Fatal(err)
	}
	auth := authorization.NewAuthorizer(db)
	sealer := testSealer(t)
	publisher := &fakePublisher{}
	service, err := NewService(db, auth, registry, sealer, 1, WithPublisher(publisher))
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := NewOrchestrator(db, registry, auth, sealer, 1)
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{UserID: ids.UserID}

	instance, err := service.StartTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, "docker-lifetime-0001")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		for _, containerID := range dockerContainersFor(instance.UUID) {
			_ = exec.Command("docker", "rm", "-f", containerID).Run()
		}
	}()
	if err := orchestrator.HandleJob(ctx, publisher.last(t)); err != nil {
		t.Fatalf("apply job: %v", err)
	}
	running, err := service.Get(ctx, actor, instance.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != StatusRunning {
		t.Fatalf("instance did not reach RUNNING: %+v", running)
	}
	if len(dockerContainersFor(instance.UUID)) != 1 {
		t.Fatalf("expected exactly one owned container, got %v", dockerContainersFor(instance.UUID))
	}

	// Extend over the service API: the expiry must move by the snapshotted step.
	before := *running.ExpiresAt
	extended, err := service.ActionTask(ctx, actor, ids.BoardID, ids.ModuleID, ids.TaskID, ActionExtend, "docker-extend-key-0001")
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if got := extended.ExpiresAt.Sub(before); got < 14*time.Minute || got > 16*time.Minute {
		t.Fatalf("extension granted %s, want about 15 min", got)
	}
	if extended.ExtensionCount != 1 {
		t.Fatalf("extension count = %d, want 1", extended.ExtensionCount)
	}

	// Expire the row with the database clock and let the real reconcile loop
	// destroy the provider resource.
	if _, err := db.Pool.Exec(ctx, `UPDATE instances SET expires_at = NOW() - interval '1 minute' WHERE id=$1`, instance.UUID); err != nil {
		t.Fatal(err)
	}
	jobs, err := orchestrator.ExpiredJobs(ctx, 50)
	if err != nil {
		t.Fatalf("expired jobs: %v", err)
	}
	handled := false
	for _, job := range jobs {
		if job.InstanceID != instance.UUID {
			continue
		}
		handled = true
		if err := orchestrator.HandleJob(ctx, job); err != nil {
			t.Fatalf("expiry cleanup: %v", err)
		}
	}
	if !handled {
		t.Fatal("expiry produced no cleanup job for the instance")
	}
	final, err := service.Get(ctx, actor, instance.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusExpired || final.RemovedAt == nil {
		t.Fatalf("instance was not released: %+v", final)
	}
	if remaining := dockerContainersFor(instance.UUID); len(remaining) != 0 {
		t.Fatalf("containers leaked after expiry: %v", remaining)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instance_endpoints WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)
	assertCount(t, db, `SELECT COUNT(*) FROM instance_network_leases WHERE instance_id=$1 AND released_at IS NULL`, 0, instance.UUID)

	// A repeated cleanup must be idempotent and must not free the lease twice.
	if _, err := orchestrator.ExpiredJobs(ctx, 50); err != nil {
		t.Fatalf("second expire pass: %v", err)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM instances WHERE id=$1 AND removed_at IS NOT NULL`, 1, instance.UUID)
}
