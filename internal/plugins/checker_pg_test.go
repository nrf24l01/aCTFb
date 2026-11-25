package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
)

type boardsPluginCheckerAdapter struct {
	manager *Manager
}

var _ boards.PluginChecker = boardsPluginCheckerAdapter{}
var _ boards.PluginCheckerConfigValidator = boardsPluginCheckerAdapter{}

func (a boardsPluginCheckerAdapter) Check(ctx context.Context, request boards.PluginCheckRequest) (boards.PluginCheckResult, error) {
	result, err := a.manager.CheckChecker(ctx, CheckerRequest{
		CheckerKey: request.CheckerKey, ActorUserID: request.ActorUserID,
		BoardID: request.BoardID, ModuleID: request.ModuleID, TaskID: request.TaskID,
		SubmissionID: request.SubmissionID, SubjectType: request.SubjectType,
		SubjectID: request.SubjectID, Text: request.Text, MaxPoints: request.MaxPoints,
		Config: request.Config,
	})
	if err != nil {
		return boards.PluginCheckResult{}, mapCheckerTestError(err)
	}
	return boards.PluginCheckResult{Verdict: result.Verdict, Score: result.Score, Message: result.Message}, nil
}

func (a boardsPluginCheckerAdapter) ValidatePluginChecker(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
	extensionKey, checkerKey string,
) error {
	if err := a.manager.ValidateChecker(ctx, principal, boardID, moduleID, extensionKey, checkerKey); err != nil {
		return mapCheckerTestError(err)
	}
	return nil
}

func mapCheckerTestError(err error) error {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		return boards.ErrForbidden
	case errors.Is(err, ErrCheckerNotFound), errors.Is(err, ErrCheckerUnavailable), errors.Is(err, ErrIncompatible):
		return boards.ErrDependencyUnavailable
	case errors.Is(err, ErrCheckerInvalid):
		return boards.ErrCheckerError
	default:
		return err
	}
}

func testBoardsService(env *testEnv) *boards.Service {
	// Participant-own submission requires the live access policy, exactly as the
	// production backend wires it.
	accessSvc := access.NewService(env.db, env.manager.authorizer)
	return boards.NewService(
		boards.NewPostgresStore(env.db),
		env.manager.authorizer,
		boards.SystemClock{},
		ratelimit.NewMemoryLimiter(256),
		boards.WithPluginChecker(boardsPluginCheckerAdapter{manager: env.manager}),
		boards.WithOutbox(events.NewOutbox(env.db)),
		boards.WithIdempotencySecret("plugin-checker-test-secret"),
		boards.WithAccessPolicy(accessSvc),
	)
}

func TestPluginCheckerEndToEndPG(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	principal := authorization.Principal{UserID: env.admin}
	participant := authorization.Principal{UserID: env.user}
	id := ID("example.custom_checker")

	buildExample(t, "example.custom_checker")
	source, _ := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", "example.custom_checker"))
	copyTree(t, source, filepath.Join(env.root, "incoming", "custom-checker"))
	if _, err := env.manager.Install(ctx, principal, InstallRequest{
		PackagePath:         "custom-checker",
		ApproveCapabilities: []Capability{CapabilityBoardsRead},
		ApprovePermissions: []authorization.Permission{
			"plugin.example.custom_checker.check",
		},
	}); err != nil {
		t.Fatalf("install checker: %v", err)
	}

	// Minimal core SDK endpoint for the checker process. It proves the manager
	// sends a real delegation and that the actual end user, not a body field,
	// is used.
	var sdkCalls atomic.Int32
	sdk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/board-context" {
			http.NotFound(w, r)
			return
		}
		sdkCalls.Add(1)
		runtime := env.manager.runtimeFor(id)
		if runtime == nil || r.Header.Get("Authorization") != "Plugin "+runtime.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		delegation, err := env.manager.VerifyDelegation(r.Context(), r.Header.Get("X-Actfb-Delegation"))
		if err != nil {
			http.Error(w, "delegation", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"boardUuid": delegation.Scope.BoardID.String(), "subjectType": "USER", "subjectUuid": delegation.UserID.String(),
		})
	}))
	defer sdk.Close()
	env.manager.coreSDKURL = sdk.URL

	if _, err := env.manager.Enable(ctx, principal, id); err != nil {
		t.Fatalf("enable checker: %v", err)
	}

	boardService := testBoardsService(env)
	checkerKey := "plugin.example.custom_checker.text"
	extensionKey := "plugin.example.custom_checker.extension"

	// No automatic grant: even the global admin cannot configure the module
	// until an explicit board grant exists.
	if _, err := boardService.AdminCreateModule(ctx, principal, env.board, boards.CreateModuleInput{
		Name: "Plugin Checker", Type: boards.ModuleTypeExtension,
		ExtensionKey: extensionKey, AllowedTaskTypes: []string{boards.TaskTypeExtension}, Enabled: true,
	}); !errors.Is(err, boards.ErrForbidden) {
		t.Fatalf("module without explicit grant = %v", err)
	}
	grant, err := env.manager.CreatePermissionGrant(ctx, principal, id, GrantInput{
		UserID: env.admin, Permission: "plugin.example.custom_checker.check", BoardID: &env.board,
	})
	if err != nil {
		t.Fatalf("grant checker permission: %v", err)
	}
	module, err := boardService.AdminCreateModule(ctx, principal, env.board, boards.CreateModuleInput{
		Name: "Plugin Checker", Type: boards.ModuleTypeExtension,
		ExtensionKey: extensionKey, AllowedTaskTypes: []string{boards.TaskTypeExtension}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create extension module: %v", err)
	}
	if module.ExtensionKey != extensionKey {
		t.Fatalf("module extension key = %q", module.ExtensionKey)
	}

	if _, err := boardService.AdminCreateTask(ctx, principal, env.board, module.ID, boards.CreateTaskInput{
		Title: "Unknown checker", Type: boards.TaskTypeExtension, Category: "plugin", Description: "unknown",
		Points: 10, Visible: true, ExtensionKey: extensionKey,
		Submission: boards.SubmissionPolicyInput{Mode: boards.SubmissionModeText, Checker: boards.SubmissionCheckerPlugin, CheckerKey: "plugin.example.custom_checker.missing"},
	}); !errors.Is(err, boards.ErrDependencyUnavailable) {
		t.Fatalf("unknown checker = %v", err)
	}

	task, err := boardService.AdminCreateTask(ctx, principal, env.board, module.ID, boards.CreateTaskInput{
		Title: "Benign checker", Type: boards.TaskTypeExtension, Category: "plugin", Description: "benign",
		Points: 50, Visible: true, ExtensionKey: extensionKey,
		Submission: boards.SubmissionPolicyInput{Mode: boards.SubmissionModeText, Checker: boards.SubmissionCheckerPlugin, CheckerKey: checkerKey},
	})
	if err != nil {
		t.Fatalf("create checker task: %v", err)
	}
	if task.CheckerKey != checkerKey || task.ExtensionKey != extensionKey {
		t.Fatalf("task keys = %q/%q", task.ExtensionKey, task.CheckerKey)
	}

	path := boards.TaskPath{BoardID: env.board, ModuleID: module.ID, TaskID: task.ID}
	idempotencyKey := "checker-idem-0001"
	submission, err := boardService.SubmitTask(ctx, participant, path, idempotencyKey, boards.SubmitInput{Text: "BENIGN_OK"})
	if err != nil {
		lines, _ := env.manager.Logs(ctx, principal, id, 50)
		t.Fatalf("submit benign checker text: %v logs=%v", err, lines)
	}
	if submission.Status != boards.SubmissionStatusAccepted || submission.ScoreAwarded == nil || *submission.ScoreAwarded != 50 {
		t.Fatalf("submission = %#v", submission)
	}
	if sdkCalls.Load() == 0 {
		t.Fatal("checker did not use the core delegation/SDK path")
	}

	// Core owns the canonical submission, audit and outbox exactly once.
	var acceptedCount int
	if err := env.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM submissions WHERE task_id=$1 AND status='ACCEPTED'
	`, task.ID).Scan(&acceptedCount); err != nil {
		t.Fatal(err)
	}
	if acceptedCount != 1 {
		t.Fatalf("accepted count = %d", acceptedCount)
	}
	var eventScore int
	if err := env.db.Pool.QueryRow(ctx, `
		SELECT (payload->>'scoreAwarded')::int FROM events_outbox
		WHERE type='submission.judged' AND payload->>'submissionUuid'=$1
	`, submission.ID.String()).Scan(&eventScore); err != nil {
		t.Fatalf("judged event: %v", err)
	}
	if eventScore != 50 {
		t.Fatalf("event score = %d", eventScore)
	}
	var auditCount int
	if err := env.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events WHERE action='submission.accept' AND resource_id=$1
	`, submission.ID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("accept audit = %d err=%v", auditCount, err)
	}

	replayed, err := boardService.SubmitTask(ctx, participant, path, idempotencyKey, boards.SubmitInput{Text: "BENIGN_OK"})
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if replayed.ID != submission.ID {
		t.Fatalf("replay id = %s want %s", replayed.ID, submission.ID)
	}
	if _, err := boardService.SubmitTask(ctx, participant, path, "checker-idem-0002", boards.SubmitInput{Text: "BENIGN_OK"}); !errors.Is(err, boards.ErrTaskAlreadySolved) {
		t.Fatalf("second accepted submission = %v", err)
	}

	// A malicious score is rejected and never becomes points.
	maliciousTask, err := boardService.AdminCreateTask(ctx, principal, env.board, module.ID, boards.CreateTaskInput{
		Title: "Malicious score", Type: boards.TaskTypeExtension, Category: "plugin", Description: "malicious",
		Points: 20, Visible: true, ExtensionKey: extensionKey,
		Submission: boards.SubmissionPolicyInput{Mode: boards.SubmissionModeText, Checker: boards.SubmissionCheckerPlugin, CheckerKey: checkerKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	maliciousPath := boards.TaskPath{BoardID: env.board, ModuleID: module.ID, TaskID: maliciousTask.ID}
	if _, err := boardService.SubmitTask(ctx, participant, maliciousPath, "checker-idem-0003", boards.SubmitInput{Text: "MALICIOUS_SCORE"}); !errors.Is(err, boards.ErrCheckerError) {
		t.Fatalf("malicious score error = %v", err)
	}
	var maliciousRows int
	if err := env.db.Pool.QueryRow(ctx, `SELECT count(*) FROM submissions WHERE task_id=$1`, maliciousTask.ID).Scan(&maliciousRows); err != nil || maliciousRows != 0 {
		t.Fatalf("malicious rows = %d err=%v", maliciousRows, err)
	}

	// Honest timeout fails closed with no submission.
	timeoutTask, err := boardService.AdminCreateTask(ctx, principal, env.board, module.ID, boards.CreateTaskInput{
		Title: "Timeout", Type: boards.TaskTypeExtension, Category: "plugin", Description: "timeout",
		Points: 5, Visible: true, ExtensionKey: extensionKey,
		Submission: boards.SubmissionPolicyInput{Mode: boards.SubmissionModeText, Checker: boards.SubmissionCheckerPlugin, CheckerKey: checkerKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	timeoutPath := boards.TaskPath{BoardID: env.board, ModuleID: module.ID, TaskID: timeoutTask.ID}
	started := time.Now()
	if _, err := boardService.SubmitTask(ctx, participant, timeoutPath, "checker-idem-0004", boards.SubmitInput{Text: "TIMEOUT"}); !errors.Is(err, boards.ErrDependencyUnavailable) {
		t.Fatalf("timeout error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}

	// Revoking the explicit grant fails closed for existing tasks and blocks new
	// configuration even for the global admin.
	liveTask, err := boardService.AdminCreateTask(ctx, principal, env.board, module.ID, boards.CreateTaskInput{
		Title: "Grant revoke", Type: boards.TaskTypeExtension, Category: "plugin", Description: "grant",
		Points: 30, Visible: true, ExtensionKey: extensionKey,
		Submission: boards.SubmissionPolicyInput{Mode: boards.SubmissionModeText, Checker: boards.SubmissionCheckerPlugin, CheckerKey: checkerKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.manager.RevokePermissionGrant(ctx, principal, id, grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := boardService.AdminCreateTask(ctx, principal, env.board, module.ID, boards.CreateTaskInput{
		Title: "No grant", Type: boards.TaskTypeExtension, Category: "plugin", Description: "no grant",
		Points: 1, Visible: true, ExtensionKey: extensionKey,
		Submission: boards.SubmissionPolicyInput{Mode: boards.SubmissionModeText, Checker: boards.SubmissionCheckerPlugin, CheckerKey: checkerKey},
	}); !errors.Is(err, boards.ErrForbidden) {
		t.Fatalf("configuration after revoke = %v", err)
	}
	if _, err := boardService.SubmitTask(ctx, participant,
		boards.TaskPath{BoardID: env.board, ModuleID: module.ID, TaskID: liveTask.ID},
		"checker-idem-0005", boards.SubmitInput{Text: "BENIGN_OK"}); !errors.Is(err, boards.ErrDependencyUnavailable) {
		t.Fatalf("execution after revoke = %v", err)
	}
	if _, err := env.manager.CreatePermissionGrant(ctx, principal, id, GrantInput{
		UserID: env.admin, Permission: "plugin.example.custom_checker.check", BoardID: &env.board,
	}); err != nil {
		t.Fatal(err)
	}

	// Disabling the plugin also closes checker execution. Re-enable is needed.
	waitTask, err := boardService.AdminCreateTask(ctx, principal, env.board, module.ID, boards.CreateTaskInput{
		Title: "Disable", Type: boards.TaskTypeExtension, Category: "plugin", Description: "disable",
		Points: 30, Visible: true, ExtensionKey: extensionKey,
		Submission: boards.SubmissionPolicyInput{Mode: boards.SubmissionModeText, Checker: boards.SubmissionCheckerPlugin, CheckerKey: checkerKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Disable(ctx, principal, id); err != nil {
		t.Fatal(err)
	}
	if _, err := boardService.SubmitTask(ctx, participant,
		boards.TaskPath{BoardID: env.board, ModuleID: module.ID, TaskID: waitTask.ID},
		"checker-idem-0006", boards.SubmitInput{Text: "BENIGN_OK"}); !errors.Is(err, boards.ErrDependencyUnavailable) {
		t.Fatalf("submission while disabled = %v", err)
	}
	if _, err := env.manager.Enable(ctx, principal, id); err != nil {
		t.Fatal(err)
	}
	if _, err := boardService.SubmitTask(ctx, participant,
		boards.TaskPath{BoardID: env.board, ModuleID: module.ID, TaskID: waitTask.ID},
		"checker-idem-0007", boards.SubmitInput{Text: "BENIGN_OK"}); err != nil {
		t.Fatalf("submission after re-enable: %v", err)
	}

	// A crashed backend also fails closed with no submission or score.
	crashTask, err := boardService.AdminCreateTask(ctx, principal, env.board, module.ID, boards.CreateTaskInput{
		Title: "Crash", Type: boards.TaskTypeExtension, Category: "plugin", Description: "crash",
		Points: 30, Visible: true, ExtensionKey: extensionKey,
		Submission: boards.SubmissionPolicyInput{Mode: boards.SubmissionModeText, Checker: boards.SubmissionCheckerPlugin, CheckerKey: checkerKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := env.manager.runtimeFor(id)
	if runtime == nil || runtime.handle.ProcessID == 0 {
		t.Fatal("missing checker process")
	}
	if err := syscall.Kill(-runtime.handle.ProcessID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := boardService.SubmitTask(ctx, participant,
		boards.TaskPath{BoardID: env.board, ModuleID: module.ID, TaskID: crashTask.ID},
		"checker-idem-0008", boards.SubmitInput{Text: "BENIGN_OK"}); !errors.Is(err, boards.ErrDependencyUnavailable) {
		t.Fatalf("submission after crash = %v", err)
	}
	if _, err := env.manager.Disable(ctx, principal, id); err != nil {
		t.Fatal(err)
	}

	// Only one accepted verdict exists per task and the canonical score is the
	// frozen task points (50 + 30).
	scoreboard, err := boardService.AdminGetScoreboard(ctx, principal, env.board)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range scoreboard.Items {
		if item.Subject.Type == boards.ScoreSubjectUser && item.Subject.ID == env.user {
			if item.Score != 80 {
				t.Fatalf("scoreboard score = %d want 80", item.Score)
			}
		}
	}
}

func TestManifestCheckerDeclarationValidation(t *testing.T) {
	manifest := validTestManifest()
	manifest.Routes = append(manifest.Routes, Route{
		Method: "POST", Path: "/boards/:boardUuid/check", Permission: "plugin.example.hello.view", Scope: "board",
	})
	manifest.Checkers = []CheckerDecl{{
		Key: "plugin.example.hello.text", ExtensionKey: "plugin.example.hello.extension",
		Route: "/boards/:boardUuid/check", Permission: "plugin.example.hello.view", Scope: "board",
		TimeoutSeconds: 1, MaxTextBytes: 1000,
	}}
	findings := ValidateManifest(NormalizeManifest(manifest), true)
	if hasErrorFinding(findings) {
		t.Fatalf("valid checker declaration rejected: %#v", findings)
	}
	manifest.Checkers[0].Permission = "plugin.example.hello.manage"
	findings = ValidateManifest(NormalizeManifest(manifest), true)
	if !hasErrorFinding(findings) {
		t.Fatal("checker permission mismatch must be rejected")
	}
}
