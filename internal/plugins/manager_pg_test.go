package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/plugins/protocol"
)

func buildExample(t *testing.T, name string) {
	t.Helper()
	moduleDir, err := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		t.Skipf("example module missing: %v", err)
	}
	command := exec.Command("go", "build", "-o", filepath.Join(moduleDir, "bin", "plugin"), ".")
	command.Dir = moduleDir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build example %s: %v\n%s", name, err, output)
	}
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in example tree")
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o640)
		if info.Mode()&0o111 != 0 {
			mode = 0o750
		}
		return os.WriteFile(target, data, mode)
	}); err != nil {
		t.Fatal(err)
	}
}

func installHello(t *testing.T, env *testEnv) {
	t.Helper()
	buildExample(t, "example.hello")
	source, _ := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", "example.hello"))
	copyTree(t, source, filepath.Join(env.root, "incoming", "hello"))
}

func helloApprovals() ([]Capability, []authorization.Permission) {
	return []Capability{CapabilityBoardsRead}, []authorization.Permission{
		"plugin.example.hello.view",
		"plugin.example.hello.manage",
	}
}

func TestPluginStageP5LifecyclePG(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	principal := authorization.Principal{UserID: env.admin}
	id := ID("example.hello")
	installHello(t, env)

	validation, err := env.manager.Validate(ctx, principal, "hello")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("validation findings: %#v", validation.Findings)
	}
	if validation.ID != id || validation.Name == "" || validation.Version == "" ||
		validation.APIVersion == "" || len(validation.Capabilities) == 0 || len(validation.Permissions) == 0 {
		t.Fatalf("validation approval metadata = %#v", validation)
	}
	if env.manager.runtimeFor(id) != nil {
		t.Fatal("validate must not start package code")
	}

	if _, err := env.manager.Install(ctx, principal, InstallRequest{
		PackagePath: "hello", ApproveCapabilities: []Capability{CapabilityBoardsRead},
	}); err == nil {
		t.Fatal("install must reject incomplete approvals")
	}
	capabilities, permissions := helloApprovals()
	installed, err := env.manager.Install(ctx, principal, InstallRequest{
		PackagePath: "hello", ApproveCapabilities: capabilities, ApprovePermissions: permissions,
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if installed.Status != StatusInstalled {
		t.Fatalf("install status = %s, must be installed (never auto-enabled)", installed.Status)
	}
	if env.manager.runtimeFor(id) != nil {
		t.Fatal("install must not execute the backend")
	}

	enabled, err := env.manager.Enable(ctx, principal, id)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if enabled.Status != StatusEnabled || enabled.Health.Status != "healthy" {
		t.Fatalf("enabled=%#v", enabled)
	}
	runtime := env.manager.runtimeFor(id)
	if runtime == nil {
		t.Fatal("missing runtime")
	}

	// A global admin does not receive plugin permission automatically.
	request := ProxyRequest{
		PluginID: id, Actor: principal, Method: "GET",
		Path:    "/api/v2/plugins/example.hello/boards/" + env.board.String() + "/hello",
		Headers: http.Header{}, Query: map[string][]string{},
	}
	if _, err := env.manager.Proxy(ctx, request); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("unexpected ungranted proxy result: %v", err)
	}
	if visible, err := env.manager.Visible(ctx, principal); err != nil || len(visible) != 0 {
		t.Fatalf("admin must not see ungranted plugin pages: %#v err=%v", visible, err)
	}

	viewGrant, err := env.manager.CreatePermissionGrant(ctx, principal, id, GrantInput{
		UserID: env.admin, Permission: "plugin.example.hello.view", BoardID: &env.board,
	})
	if err != nil {
		t.Fatalf("grant view: %v", err)
	}
	if _, err := env.manager.CreatePermissionGrant(ctx, principal, id, GrantInput{
		UserID: env.admin, Permission: "plugin.example.hello.manage", BoardID: &env.board,
	}); err != nil {
		t.Fatalf("grant manage: %v", err)
	}

	// Grants are delegated only to this plugin's namespace and declared scopes.
	if _, err := env.manager.CreatePermissionGrant(ctx, principal, id, GrantInput{
		UserID: env.admin, Permission: authorization.PermissionBoardRead, BoardID: &env.board,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("core permission grant must be rejected: %v", err)
	}
	if _, err := env.manager.CreatePermissionGrant(ctx, principal, id, GrantInput{
		UserID: env.admin, Permission: "plugin.other.plugin.view", BoardID: &env.board,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("foreign plugin grant must be rejected: %v", err)
	}
	if _, err := env.manager.CreatePermissionGrant(ctx, principal, id, GrantInput{
		UserID: env.admin, Permission: "plugin.example.hello.view",
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid global scope for board permission must be rejected: %v", err)
	}

	grants, grantTotal, err := env.manager.ListPermissionGrants(ctx, principal, id, 50, 0)
	if err != nil || grantTotal != 2 || len(grants) != 2 {
		t.Fatalf("list grants: total=%d len=%d err=%v", grantTotal, len(grants), err)
	}
	for _, grant := range grants {
		if !strings.HasPrefix(string(grant.Permission), "plugin.example.hello.") {
			t.Fatalf("foreign grant leaked: %s", grant.Permission)
		}
	}

	visible, err := env.manager.Visible(ctx, principal)
	if err != nil {
		t.Fatalf("visible: %v", err)
	}
	if len(visible) != 1 || len(visible[0].Pages) != 2 {
		t.Fatalf("visible = %#v", visible)
	}
	for _, item := range visible[0].Pages {
		if !strings.HasPrefix(item.IframeURL, "http://127.0.0.2:8091/plugins/example.hello/assets/") {
			t.Fatalf("unsafe iframe URL %q", item.IframeURL)
		}
		if strings.Contains(item.IframeURL, "token") {
			t.Fatalf("iframe URL leaks a token: %q", item.IframeURL)
		}
	}

	response, err := env.manager.Proxy(ctx, request)
	if err != nil {
		t.Fatalf("proxy GET: %v", err)
	}
	if response.Status != 200 || !strings.Contains(string(response.Body), "messages") {
		t.Fatalf("proxy response: %d %s", response.Status, response.Body)
	}

	post := request
	post.Method = "POST"
	post.Path = "/api/v2/plugins/example.hello/boards/" + env.board.String() + "/hello/messages"
	post.Body = []byte(`{"body":"hello from integration"}`)
	post.Headers = http.Header{"Content-Type": []string{"application/json"}}
	if response, err = env.manager.Proxy(ctx, post); err != nil || response.Status != 201 {
		t.Fatalf("proxy POST: %#v err=%v", response, err)
	}

	// Grant is scoped: another board is denied even though the actor is admin.
	otherBoard := request
	otherBoard.Path = "/api/v2/plugins/example.hello/boards/" + env.boardB.String() + "/hello"
	if _, err := env.manager.Proxy(ctx, otherBoard); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("board B must be denied: %v", err)
	}

	// Hint headers are consistency hints only; a forged board is rejected.
	hintForged := request
	hintForged.Headers = http.Header{"X-Actfb-BoardUuid": []string{env.boardB.String()}}
	if _, err := env.manager.Proxy(ctx, hintForged); !errors.Is(err, ErrRequestRejected) {
		t.Fatalf("forged hint must be rejected: %v", err)
	}

	// A delegation is mandatory and tampering fails. The body cannot inject a
	// user id; the SDK handler rejects it before CoreProxy is reached.
	coreProxy, err := env.manager.NewCoreProxy()
	if err != nil {
		t.Fatal(err)
	}
	delegation, err := env.manager.issuer.Issue(ctx, runtime.identity, env.admin, authorization.Scope{BoardID: env.board}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	call := CallContext{Identity: runtime.identity, Delegation: delegation, UserID: env.admin, Scope: delegation.Scope}
	decision, err := coreProxy.Authorize(ctx, call, "plugin.example.hello.view")
	if err != nil || !decision.Allowed {
		t.Fatalf("SDK authorize: %#v err=%v", decision, err)
	}
	call.UserID = uuid.New() // forged user is ignored/denied
	if _, err := coreProxy.Authorize(ctx, call, "plugin.example.hello.view"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("forged call user must be denied: %v", err)
	}
	if _, err := env.manager.VerifyDelegation(ctx, ""); !errors.Is(err, ErrDelegationInvalid) {
		t.Fatalf("missing delegation = %v", err)
	}
	tampered := delegation.Token[:len(delegation.Token)-2] + "zz"
	if _, err := env.manager.VerifyDelegation(ctx, tampered); err == nil {
		t.Fatal("tampered delegation must fail")
	}
	if _, err := env.manager.VerifyServiceToken(ctx, "wrong-token"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("wrong service token must fail")
	}
	replayToken, err := env.manager.issuer.Issue(ctx, runtime.identity, env.admin, authorization.Scope{BoardID: env.board}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var debugCount int
	_ = env.db.Pool.QueryRow(ctx, `SELECT count(*) FROM plugin_delegations WHERE token_hash=$1 AND revoked_at IS NULL`, delegationTokenHash(replayToken.Token)).Scan(&debugCount)
	if _, err := env.manager.VerifyDelegation(ctx, replayToken.Token); err != nil {
		t.Fatalf("first delegation use: %v", err)
	}
	if _, err := env.manager.VerifyDelegation(ctx, replayToken.Token); !errors.Is(err, ErrDelegationRevoked) {
		t.Fatalf("replayed nonce must fail: %v", err)
	}

	// Revocation is live and effective immediately, including for global admins.
	if err := env.manager.RevokePermissionGrant(ctx, principal, id, viewGrant.ID); err != nil {
		t.Fatal(err)
	}
	call.UserID = env.admin
	decision, err = coreProxy.Authorize(ctx, call, "plugin.example.hello.view")
	if err != nil || decision.Allowed {
		t.Fatalf("revoked grant must be denied: %#v err=%v", decision, err)
	}
	if _, err := env.manager.CreatePermissionGrant(ctx, principal, id, GrantInput{
		UserID: env.admin, Permission: "plugin.example.hello.view", BoardID: &env.board,
	}); err != nil {
		t.Fatal(err)
	}

	// Disable closes routes and preserves data; re-enable keeps the same schema.
	if _, err := env.manager.Disable(ctx, principal, id); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Proxy(ctx, request); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("disabled proxy = %v", err)
	}
	if visible, err := env.manager.Visible(ctx, principal); err != nil || len(visible) != 0 {
		t.Fatalf("disabled plugin must be invisible: %#v err=%v", visible, err)
	}
	if _, err := env.manager.Enable(ctx, principal, id); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	response, err = env.manager.Proxy(ctx, request)
	if err != nil || !strings.Contains(string(response.Body), "hello from integration") {
		t.Fatalf("data not preserved across disable/enable: %v %s", err, response.Body)
	}

	// Crash isolation: kill the separate process, core remains operational and
	// this plugin's route returns a controlled 503.
	runtime = env.manager.runtimeFor(id)
	if runtime == nil || runtime.handle.ProcessID == 0 {
		t.Fatal("expected exec process")
	}
	if err := syscall.Kill(-runtime.handle.ProcessID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill plugin: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := env.manager.Proxy(ctx, request); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("crashed backend must return 503, got %v", err)
	}
	if _, err := env.manager.Get(ctx, principal, id); err != nil {
		t.Fatalf("core get must still work after crash: %v", err)
	}
	if _, err := env.manager.Disable(ctx, principal, id); err != nil {
		t.Fatal(err)
	}

	// Version upgrade runs pending plugin migrations before serving traffic and
	// preserves rows.
	upgradeSource := filepath.Join(env.root, "incoming", "hello-upgrade")
	copyTree(t, filepath.Join(env.root, "incoming", "hello"), upgradeSource)
	manifestPath := filepath.Join(upgradeSource, "manifest.yaml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes = []byte(strings.Replace(string(manifestBytes), "version: 1.0.0", "version: 1.1.0", 1))
	if err := os.WriteFile(manifestPath, manifestBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upgradeSource, "migrations", "002_upgrade.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS hello_upgrade_marker (id int);"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Enable(ctx, principal, id); err != nil {
		t.Fatal(err)
	}
	upgraded, err := env.manager.Upgrade(ctx, principal, id, "hello-upgrade")
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if upgraded.Version != "1.1.0" || upgraded.Status != StatusEnabled {
		t.Fatalf("upgraded = %#v", upgraded)
	}
	runtime = env.manager.runtimeFor(id)
	if runtime == nil || runtime.dsn == "" {
		t.Fatal("missing upgraded runtime/private DSN")
	}
	connect, err := pgx.Connect(ctx, runtime.dsn)
	if err != nil {
		t.Fatalf("connect private schema: %v", err)
	}
	var marker string
	if err := connect.QueryRow(ctx, `SELECT to_regclass('hello_upgrade_marker')::text`).Scan(&marker); err != nil || marker == "" {
		t.Fatalf("upgrade migration not applied: %v %q", err, marker)
	}
	connect.Close(ctx)
	response, err = env.manager.Proxy(ctx, request)
	if err != nil || !strings.Contains(string(response.Body), "hello from integration") {
		t.Fatalf("data lost after upgrade: %v %s", err, response.Body)
	}

	// A failing new-version migration must not serve requests on a partially
	// applied schema: the previous version is restarted instead.
	badSource := filepath.Join(env.root, "incoming", "hello-bad")
	copyTree(t, filepath.Join(env.root, "incoming", "hello-upgrade"), badSource)
	badManifestPath := filepath.Join(badSource, "manifest.yaml")
	badManifest, err := os.ReadFile(badManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	badManifest = []byte(strings.Replace(string(badManifest), "version: 1.1.0", "version: 1.2.0", 1))
	if err := os.WriteFile(badManifestPath, badManifest, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badSource, "migrations", "003_bad.sql"),
		[]byte("CREATE TABLE broken ("), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Upgrade(ctx, principal, id, "hello-bad"); err == nil {
		t.Fatal("expected failing migration upgrade to be rejected")
	}
	afterFailure, err := env.manager.Get(ctx, principal, id)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.Version != "1.1.0" || afterFailure.Status != StatusEnabled {
		t.Fatalf("previous version not restored: %#v", afterFailure)
	}
	response, err = env.manager.Proxy(ctx, request)
	if err != nil || !strings.Contains(string(response.Body), "hello from integration") {
		t.Fatalf("previous version not serving after failed upgrade: %v %s", err, response.Body)
	}

	dsnBeforeRemove := runtime.dsn
	if err := env.manager.Remove(ctx, principal, id); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := env.manager.Get(ctx, principal, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed plugin get = %v", err)
	}
	// Private schema data is intentionally retained after remove.
	connect, err = pgx.Connect(ctx, dsnBeforeRemove)
	if err != nil {
		t.Fatalf("connect retained private schema: %v", err)
	}
	var count int
	if err := connect.QueryRow(ctx, `SELECT count(*) FROM hello_messages`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("private data not retained: count=%d err=%v", count, err)
	}
	connect.Close(ctx)
}

func TestPrivatePluginRoleIsRestrictedPG(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	installHello(t, env)
	capabilities, permissions := helloApprovals()
	if _, err := env.manager.Install(ctx, authorization.Principal{UserID: env.admin}, InstallRequest{
		PackagePath: "hello", ApproveCapabilities: capabilities, ApprovePermissions: permissions,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Enable(ctx, authorization.Principal{UserID: env.admin}, "example.hello"); err != nil {
		t.Fatal(err)
	}
	runtime := env.manager.runtimeFor("example.hello")
	if runtime == nil || runtime.dsn == "" {
		t.Fatal("private DSN missing")
	}
	conn, err := pgx.Connect(ctx, runtime.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var currentUser, currentSchema string
	if err := conn.QueryRow(ctx, `SELECT current_user, current_schema()`).Scan(&currentUser, &currentSchema); err != nil {
		t.Fatal(err)
	}
	expectedIdentity := pluginSchemaName("example.hello")
	if currentUser != expectedIdentity || currentSchema != expectedIdentity {
		t.Fatalf("current_user=%q schema=%q want=%q", currentUser, currentSchema, expectedIdentity)
	}
	if _, err := conn.Exec(ctx, `DROP TABLE public.plugins`); err == nil {
		t.Fatal("restricted plugin role must not DROP a core table")
	}
	if _, err := conn.Exec(ctx, `SET ROLE actfb`); err == nil {
		t.Fatal("restricted plugin role must not SET ROLE to the core role")
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE public.actfb_plugin_escape (id int)`); err == nil {
		t.Fatal("restricted plugin role must not create objects in public")
	}
}

func TestScoreEventReplayDedupesPG(t *testing.T) {
	env := newTestEnv(t)
	if env.nats == nil {
		t.Skip("set ACTFB_TEST_NATS_URL to run the JetStream event delivery test")
	}
	ctx := context.Background()
	principal := authorization.Principal{UserID: env.admin}
	buildExample(t, "example.score_events")
	source, _ := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", "example.score_events"))
	copyTree(t, source, filepath.Join(env.root, "incoming", "score-events"))
	id := ID("example.score_events")
	if _, err := env.manager.Install(ctx, principal, InstallRequest{
		PackagePath:         "score-events",
		ApproveCapabilities: []Capability{CapabilityBoardsRead, CapabilityEventsSubscribe},
		ApprovePermissions: []authorization.Permission{
			"plugin.example.score_events.view", "plugin.example.score_events.manage",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Enable(ctx, principal, id); err != nil {
		t.Fatalf("enable score events: %v", err)
	}
	runtime := env.manager.runtimeFor(id)
	if runtime == nil {
		t.Fatal("runtime missing")
	}

	subjectID := env.admin
	payload, _ := json.Marshal(events.SubmissionJudgedPayload{
		SubmissionUUID: uuid.New(), BoardUUID: env.board, ModuleUUID: env.module, TaskUUID: uuid.New(),
		SubjectType: events.SubjectTypeUser, SubjectUUID: subjectID, Status: "ACCEPTED",
		ScoreAwarded: intPointer(100), OccurredAt: time.Now().UTC(),
	})
	event := events.Event{
		ID: uuid.New(), Type: events.EventSubmissionJudged, OccurredAt: time.Now().UTC(),
		BoardID: &env.board, ModuleID: &env.module, SubjectType: events.SubjectTypeUser, SubjectID: &subjectID,
		Payload: payload,
	}
	tx, err := env.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.manager.outbox.Enqueue(ctx, tx, event); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := env.manager.publisher.Publish(ctx, event); err != nil {
		t.Fatalf("publish event: %v", err)
	}

	connect, err := pgx.Connect(ctx, runtime.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connect.Close(ctx)
	waitFor(t, 15*time.Second, func() bool {
		var count int
		if err := connect.QueryRow(ctx, `SELECT count(*) FROM score_event_seen WHERE event_id = $1`, event.ID).Scan(&count); err != nil {
			return false
		}
		return count == 1
	})
	var judged int
	if err := connect.QueryRow(ctx, `SELECT judged_count FROM score_stats WHERE board_id=$1 AND module_id=$2`, env.board, env.module).Scan(&judged); err != nil {
		t.Fatal(err)
	}
	if judged != 1 {
		t.Fatalf("judged_count=%d want 1", judged)
	}

	// Replay the same event: the plugin must dedupe by event UUID and cannot
	// double its own stats (or core score).
	deliveries, total, err := env.manager.ListEventDeliveries(ctx, principal, id, 10, 0)
	if err != nil || total != 1 || len(deliveries) != 1 || deliveries[0].Status != "delivered" {
		t.Fatalf("deliveries=%#v total=%d err=%v", deliveries, total, err)
	}
	if err := env.manager.ReplayEvent(ctx, principal, id, event.ID); err != nil {
		t.Fatalf("replay: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	var seen, afterJudged int
	if err := connect.QueryRow(ctx, `SELECT count(*) FROM score_event_seen WHERE event_id=$1`, event.ID).Scan(&seen); err != nil || seen != 1 {
		t.Fatalf("seen=%d err=%v", seen, err)
	}
	if err := connect.QueryRow(ctx, `SELECT judged_count FROM score_stats WHERE board_id=$1 AND module_id=$2`, env.board, env.module).Scan(&afterJudged); err != nil || afterJudged != 1 {
		t.Fatalf("judged after replay=%d err=%v", afterJudged, err)
	}
	var replayAudit int
	if err := env.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events WHERE action = 'plugin.event.replay' AND details->>'pluginId' = $1
	`, string(id)).Scan(&replayAudit); err != nil || replayAudit == 0 {
		t.Fatalf("replay audit missing: %d %v", replayAudit, err)
	}

	// Retry budget exhaustion moves the delivery to the dead/DLQ state rather
	// than losing it or blocking core.
	badClient, err := protocol.NewClient(protocol.ClientOptions{
		BaseURL: "http://127.0.0.1:1", ServiceToken: runtime.token, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	badRuntime := &runtimeBackend{
		installed: runtime.installed,
		identity:  runtime.identity,
		token:     runtime.token,
		client:    badClient,
	}
	deadEvent := event
	deadEvent.ID = uuid.New()
	for index := 0; index < 6; index++ {
		_, _, _ = env.manager.deliverEvent(ctx, badRuntime, deadEvent)
	}
	var status string
	var attempts int
	if err := env.db.Pool.QueryRow(ctx, `
		SELECT status, attempts FROM plugin_event_deliveries WHERE plugin_id = $1 AND event_id = $2
	`, string(id), deadEvent.ID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "dead" || attempts < maxEventAttempts {
		t.Fatalf("DLQ status=%s attempts=%d", status, attempts)
	}
}

func intPointer(value int) *int { return &value }

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestOCIRequiresTrustedRunnerPG(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	principal := authorization.Principal{UserID: env.admin}
	source, _ := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", "example.hello"))
	destination := filepath.Join(env.root, "incoming", "oci")
	copyTree(t, source, destination)
	manifestPath := filepath.Join(destination, "manifest.yaml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(manifestBytes)
	manifest = strings.ReplaceAll(manifest, "id: example.hello", "id: example.oci")
	manifest = strings.ReplaceAll(manifest, "name: Example Hello", "name: Example OCI")
	manifest = strings.ReplaceAll(manifest, "plugin.example.hello.", "plugin.example.oci.")
	manifest = strings.Replace(manifest,
		"  mode: exec\n  entrypoint: bin/plugin\n",
		"  mode: oci\n  image: registry.example/plugin@sha256:"+strings.Repeat("a", 64)+"\n",
		1)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Install(ctx, principal, InstallRequest{
		PackagePath:         "oci",
		ApproveCapabilities: []Capability{CapabilityBoardsRead},
		ApprovePermissions: []authorization.Permission{
			"plugin.example.oci.view", "plugin.example.oci.manage",
		},
	}); err != nil {
		t.Fatalf("install oci package: %v", err)
	}
	if _, err := env.manager.Enable(ctx, principal, "example.oci"); err == nil || !strings.Contains(err.Error(), "plugin-runner") {
		t.Fatalf("OCI enable without trusted runner must fail honestly, got %v", err)
	}
	installed, err := env.manager.Get(ctx, principal, "example.oci")
	if err != nil {
		t.Fatal(err)
	}
	if installed.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", installed.Status)
	}
	if env.manager.runtimeFor("example.oci") != nil {
		t.Fatal("no fake OCI runtime may be registered")
	}
}
