package attackdefense

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestWebhookDedupeAndSubjectDerivationPostgres(t *testing.T) {
	f := newADFixture(t)
	ctx := context.Background()
	body := f.pushBody("team/source", "main", strings.ToUpper(shaA))
	headers := http.Header(giteaHeaders(f.webhookSecret, "delivery-1", string(body)))

	result, err := f.service.HandleWebhook(ctx, WebhookRequest{
		BindingID: f.sourceBinding, Body: body, Headers: headers,
		RemoteAddr: "127.0.0.1:1234", TLS: false, ContentType: "application/json",
	})
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if len(result.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(result.Runs))
	}
	var (
		count       int
		kind        string
		sha         string
		subjectType string
		subjectID   uuid.UUID
		requestedBy uuid.UUID
		delivery    string
	)
	if err := f.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM ad_runs
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(ctx, `
		SELECT kind, commit_sha, subject_type, subject_id, requested_by, provider_delivery_id
		FROM ad_runs ORDER BY created_at DESC LIMIT 1
	`).Scan(&kind, &sha, &subjectType, &subjectID, &requestedBy, &delivery); err != nil {
		t.Fatal(err)
	}
	if count != 1 || kind != RunKindPatch || sha != shaA || subjectType != SubjectTypeUser ||
		subjectID != f.player.UserID || requestedBy != f.player.UserID || delivery != "delivery-1" {
		t.Fatalf("run row = count=%d kind=%s sha=%s subject=%s/%s requested=%s delivery=%s",
			count, kind, sha, subjectType, subjectID, requestedBy, delivery)
	}

	// Exact duplicate delivery id: already processed, no new run.
	if _, err := f.service.HandleWebhook(ctx, WebhookRequest{
		BindingID: f.sourceBinding, Body: body, Headers: headers,
		RemoteAddr: "127.0.0.1:1234", ContentType: "application/json",
	}); !errors.Is(err, ErrWebhookAlreadyProcessed) {
		t.Fatalf("duplicate delivery error = %v", err)
	}
	// Same commit through a different delivery id: accepted, one logical run.
	secondHeaders := http.Header(giteaHeaders(f.webhookSecret, "delivery-2", string(body)))
	second, err := f.service.HandleWebhook(ctx, WebhookRequest{
		BindingID: f.sourceBinding, Body: body, Headers: secondHeaders,
		RemoteAddr: "127.0.0.1:1234", ContentType: "application/json",
	})
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if len(second.Runs) != 0 {
		t.Fatalf("redelivery created %d runs", len(second.Runs))
	}
	// Same delivery id with a different payload is a replay, not a new run.
	otherBody := f.pushBody("team/source", "main", shaB)
	replayHeaders := http.Header(giteaHeaders(f.webhookSecret, "delivery-1", string(otherBody)))
	if _, err := f.service.HandleWebhook(ctx, WebhookRequest{
		BindingID: f.sourceBinding, Body: otherBody, Headers: replayHeaders,
		RemoteAddr: "127.0.0.1:1234", ContentType: "application/json",
	}); !errors.Is(err, ErrWebhookReplayed) {
		t.Fatalf("replay error = %v", err)
	}
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM ad_runs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("runs after replay = %d", count)
	}
	var deliveryCount int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM git_webhook_deliveries`).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 2 {
		t.Fatalf("deliveries = %d, want 2 (duplicate insert did not create a row)", deliveryCount)
	}

	// Negative transport/payload cases.
	if _, err := f.service.HandleWebhook(ctx, WebhookRequest{
		BindingID: f.sourceBinding, Body: body,
		Headers:    http.Header(giteaHeaders("wrong-secret", "delivery-3", string(body))),
		RemoteAddr: "127.0.0.1:1234", ContentType: "application/json",
	}); !errors.Is(err, ErrInvalidWebhookSignature) {
		t.Fatalf("bad signature error = %v", err)
	}
	branchBody := f.pushBody("team/source", "other", shaB)
	if _, err := f.service.HandleWebhook(ctx, WebhookRequest{
		BindingID: f.sourceBinding, Body: branchBody,
		Headers:    http.Header(giteaHeaders(f.webhookSecret, "delivery-4", string(branchBody))),
		RemoteAddr: "127.0.0.1:1234", ContentType: "application/json",
	}); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("branch mismatch error = %v", err)
	}
	repositoryBody := f.pushBody("team/other", "main", shaB)
	if _, err := f.service.HandleWebhook(ctx, WebhookRequest{
		BindingID: f.sourceBinding, Body: repositoryBody,
		Headers:    http.Header(giteaHeaders(f.webhookSecret, "delivery-5", string(repositoryBody))),
		RemoteAddr: "127.0.0.1:1234", ContentType: "application/json",
	}); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("repository mismatch error = %v", err)
	}
	oversized := make([]byte, MaxWebhookBodyBytes+1)
	if _, err := f.service.HandleWebhook(ctx, WebhookRequest{
		BindingID: f.sourceBinding, Body: oversized,
		Headers: http.Header{}, RemoteAddr: "127.0.0.1:1234", ContentType: "application/json",
	}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestAwardsIdempotentAndPatchRequiresBothPostgres(t *testing.T) {
	f := newADFixture(t)
	ctx := context.Background()
	worker := &Worker{service: f.service}

	makeRun := func(kind, sha string) *Run {
		run, created, err := f.service.store.InsertRun(ctx, f.db.Pool, InsertRunParams{
			ModuleID: f.moduleID, RepositoryAssignmentID: f.assignmentID,
			SubjectType: SubjectTypeUser, SubjectID: f.player.UserID,
			Kind: kind, CommitSHA: sha, RequestedBy: f.player.UserID,
		})
		if err != nil || !created {
			t.Fatalf("insert run %s/%s: created=%v err=%v", kind, sha, created, err)
		}
		if claimed, err := f.service.store.ClaimRun(ctx, run.ID); err != nil || !claimed {
			t.Fatalf("claim run: %v %v", claimed, err)
		}
		return run
	}
	execute := func(run *Run, kind string, verdict Verdict) {
		execution := &Execution{
			Run: *run,
			Vulnerabilities: []Vulnerability{{
				ID: f.vulnerabilityID, Key: "sqli", ExploitPoints: 100, PatchPoints: 150,
			}},
		}
		execution.Run.Kind = kind
		status, results := worker.buildVerdict(execution, verdict)
		if _, _, err := f.service.store.ApplyVerdict(ctx, VerdictInput{
			RunID: run.ID, Status: status, Results: results,
			Audit: AuditEvent{ActorUserID: f.player.UserID},
		}); err != nil {
			t.Fatal(err)
		}
	}
	boolean := func(value bool) *bool { return &value }

	exploitRun := makeRun(RunKindExploit, shaA)
	execute(exploitRun, RunKindExploit, Verdict{Vulnerabilities: []VerdictVulnerability{{
		Key: "sqli", Status: ResultStatusPassed, SecurityFixed: boolean(true), RegressionPassed: boolean(true),
	}}})
	// A second commit with the same exploit must not award again.
	secondExploit := makeRun(RunKindExploit, shaB)
	execute(secondExploit, RunKindExploit, Verdict{Vulnerabilities: []VerdictVulnerability{{
		Key: "sqli", Status: ResultStatusPassed,
	}}})
	var awards int
	var points int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*), COALESCE(sum(points),0) FROM ad_awards`).Scan(&awards, &points); err != nil {
		t.Fatal(err)
	}
	if awards != 1 || points != 100 {
		t.Fatalf("exploit awards = %d points=%d", awards, points)
	}

	// PATCH with only one of the two required signals never awards.
	patchFailed := makeRun(RunKindPatch, shaA)
	execute(patchFailed, RunKindPatch, Verdict{Vulnerabilities: []VerdictVulnerability{{
		Key: "sqli", Status: ResultStatusPassed, SecurityFixed: boolean(true), RegressionPassed: boolean(false),
	}}})
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM ad_awards WHERE kind='PATCH'`).Scan(&awards); err != nil {
		t.Fatal(err)
	}
	if awards != 0 {
		t.Fatalf("patch awarded without regression: %d", awards)
	}
	patchPassed := makeRun(RunKindPatch, shaB)
	execute(patchPassed, RunKindPatch, Verdict{Vulnerabilities: []VerdictVulnerability{{
		Key: "sqli", Status: ResultStatusPassed, SecurityFixed: boolean(true), RegressionPassed: boolean(true),
	}}})
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*), COALESCE(sum(points),0) FROM ad_awards`).Scan(&awards, &points); err != nil {
		t.Fatal(err)
	}
	if awards != 2 || points != 250 {
		t.Fatalf("total awards = %d points=%d", awards, points)
	}
	progress, err := f.service.GetProgress(ctx, f.player, f.boardID, f.moduleID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.TotalVulnerabilities != 1 || progress.FoundCount != 1 || progress.PatchedCount != 1 || progress.Score != 250 {
		t.Fatalf("progress = %+v", progress)
	}
	if progress.Vulnerabilities[0].CurrentPatchValid == nil || !*progress.Vulnerabilities[0].CurrentPatchValid {
		t.Fatalf("current patch validity = %v", progress.Vulnerabilities[0].CurrentPatchValid)
	}
	// A later failed patch makes the current patch invalid while the award stays.
	latestFailed := makeRun(RunKindPatch, "cccccccccccccccccccccccccccccccccccccccc")
	execute(latestFailed, RunKindPatch, Verdict{Vulnerabilities: []VerdictVulnerability{{
		Key: "sqli", Status: ResultStatusFailed, SecurityFixed: boolean(false), RegressionPassed: boolean(true),
	}}})
	progress, err = f.service.GetProgress(ctx, f.player, f.boardID, f.moduleID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Vulnerabilities[0].CurrentPatchValid == nil || *progress.Vulnerabilities[0].CurrentPatchValid {
		t.Fatalf("current patch validity after failure = %v", progress.Vulnerabilities[0].CurrentPatchValid)
	}
	if progress.Score != 250 {
		t.Fatalf("score changed on failed patch = %d", progress.Score)
	}
	var submissions int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM submissions`).Scan(&submissions); err != nil {
		t.Fatal(err)
	}
	if submissions != 0 {
		t.Fatalf("AD wrote %d core submissions", submissions)
	}
	var events int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE type='ad.run.completed'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 5 {
		t.Fatalf("ad.run.completed outbox rows = %d", events)
	}
	adapter, err := NewScoreSourceAdapter(f.service.store)
	if err != nil {
		t.Fatal(err)
	}
	scores, err := adapter.ListBoardScores(ctx, f.boardID)
	if err != nil {
		t.Fatal(err)
	}
	var playerScore int
	for _, score := range scores {
		if score.SubjectType == SubjectTypeUser && score.SubjectID == f.player.UserID {
			playerScore = score.Points
		}
	}
	if playerScore != 250 {
		t.Fatalf("AD score source player points = %d, want 250", playerScore)
	}
	subjectTotal, err := adapter.SubjectScore(ctx, f.boardID, SubjectTypeUser, f.player.UserID)
	if err != nil || subjectTotal != 250 {
		t.Fatalf("AD subject score = %d, err=%v, want 250", subjectTotal, err)
	}
}

func TestManualRunsIdempotencyCancelAndRetryPostgres(t *testing.T) {
	f := newADFixture(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/git/commits/"):
			_, _ = w.Write([]byte(`{"sha":"` + strings.TrimPrefix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "") + `"}`))
		case strings.Contains(r.URL.Path, "/branches/"):
			_, _ = w.Write([]byte(`{"commit":{"sha":"` + shaA + `"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	if _, err := f.db.Pool.Exec(ctx, `UPDATE integrations SET base_url=$1`, server.URL); err != nil {
		t.Fatal(err)
	}

	disabled, err := f.service.AdminGetConfig(ctx, f.admin, f.boardID, f.moduleID)
	if err != nil {
		t.Fatal(err)
	}
	disabled.ManualRerunEnabled = false
	if _, err := f.service.AdminPutConfig(ctx, f.admin, f.boardID, f.moduleID, *disabled); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateMyRun(ctx, f.player, f.boardID, f.moduleID, RunKindExploit, shaA, "manual-key-000000000000"); !errors.Is(err, ErrRunActionNotAllowed) {
		t.Fatalf("manual rerun disabled error = %v", err)
	}
	disabled.ManualRerunEnabled = true
	if _, err := f.service.AdminPutConfig(ctx, f.admin, f.boardID, f.moduleID, *disabled); err != nil {
		t.Fatal(err)
	}

	key := "manual-key-000000000001"
	run, err := f.service.CreateMyRun(ctx, f.player, f.boardID, f.moduleID, RunKindExploit, shaA, key)
	if err != nil {
		t.Fatalf("manual run: %v", err)
	}
	again, err := f.service.CreateMyRun(ctx, f.player, f.boardID, f.moduleID, RunKindExploit, shaA, key)
	if err != nil || again.ID != run.ID {
		t.Fatalf("idempotent manual run = %v %v", again, err)
	}
	if _, err := f.service.CreateMyRun(ctx, f.player, f.boardID, f.moduleID, RunKindPatch, shaA, key); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("idempotency reuse error = %v", err)
	}
	if _, err := f.service.CreateMyRun(ctx, f.player, f.boardID, f.moduleID, RunKindExploit, shaB, "manual-key-000000000002"); !errors.Is(err, ErrRunRateLimited) && !errors.Is(err, ErrRunAlreadyQueued) {
		// shaB is a different logical run, so the active-run limit applies.
		t.Fatalf("rate limit error = %v", err)
	}

	cancelRun, err := f.service.AdminRunAction(ctx, f.admin, f.boardID, f.moduleID, run.ID, "CANCEL", "action-key-000000000001")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelRun.Status != RunStatusCanceled {
		t.Fatalf("cancel status = %s", cancelRun.Status)
	}
	retryRun, err := f.service.AdminRunAction(ctx, f.admin, f.boardID, f.moduleID, run.ID, "RETRY", "action-key-000000000002")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retryRun.Status != RunStatusQueued {
		t.Fatalf("retry status = %s", retryRun.Status)
	}
	// Replaying the same key+action is a stable no-op.
	replayed, err := f.service.AdminRunAction(ctx, f.admin, f.boardID, f.moduleID, run.ID, "RETRY", "action-key-000000000002")
	if err != nil || replayed.ID != retryRun.ID {
		t.Fatalf("retry replay = %v %v", replayed, err)
	}
	// Same key with a different action is rejected.
	if _, err := f.service.AdminRunAction(ctx, f.admin, f.boardID, f.moduleID, run.ID, "CANCEL", "action-key-000000000002"); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("action key reuse error = %v", err)
	}
	if _, err := f.service.CreateMyRun(ctx, f.player, f.boardID, f.moduleID, RunKindExploit, shaB, "manual-key-000000000003"); !errors.Is(err, ErrRunRateLimited) {
		t.Fatalf("expected rate limit, got %v", err)
	}

	// Parent/child IDOR: the same run is not visible through another module.
	otherModule := uuid.New()
	if _, err := f.db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, enabled, created_by)
		VALUES ($1,$2,'Other','ATTACK_DEFENSE',ARRAY['EXPLOIT','PATCH'],TRUE,$3)
	`, otherModule, f.boardID, f.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AdminGetRun(ctx, f.admin, f.boardID, otherModule, run.ID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("cross-module run access error = %v", err)
	}
	if _, err := f.service.AdminUpdateVulnerability(ctx, f.admin, f.boardID, otherModule, f.vulnerabilityID, Vulnerability{
		Key: "sqli", Title: "x", ExploitPoints: 1, PatchPoints: 1,
		ExploitTestSelector: "e", PatchTestSelector: "p",
	}); !errors.Is(err, ErrVulnerabilityNotFound) {
		t.Fatalf("cross-module vulnerability access error = %v", err)
	}
}

func TestParticipantAndAdminAuthorizationBoundariesPostgres(t *testing.T) {
	f := newADFixture(t)
	ctx := context.Background()
	if _, err := f.service.AdminGetConfig(ctx, f.player, f.boardID, f.moduleID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("participant admin read error = %v", err)
	}
	if _, err := f.service.GetProgress(ctx, f.admin, f.boardID, f.moduleID); !errors.Is(err, ErrBoardAccessDenied) {
		t.Fatalf("non-participant progress error = %v", err)
	}
	if _, err := f.service.CreateMyRun(ctx, f.player, f.boardID, f.moduleID, "NOPE", shaA, "manual-key-000000000009"); err == nil {
		t.Fatal("invalid kind accepted")
	}
	config, err := f.service.AdminGetConfig(ctx, f.admin, f.boardID, f.moduleID)
	if err != nil {
		t.Fatal(err)
	}
	if config.RunnerImage != testRunnerImage {
		t.Fatalf("runner image = %s", config.RunnerImage)
	}
	if _, err := f.service.AdminPutConfig(ctx, f.admin, f.boardID, f.moduleID, Config{
		DefaultSourceBranch: "main", DefaultExploitBranch: "exploit",
		RunnerImage: "registry.example/ad-checker:latest", RunTimeoutSeconds: 60,
		MaxConcurrentRunsPerSubject: 1,
	}); err == nil {
		t.Fatal("mutable runner image accepted")
	}
	var auditCount int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='ad.config.update'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount == 0 {
		t.Fatal("config update was not audited")
	}
}

func TestOwnRunAndCrossBoardIsolationPostgres(t *testing.T) {
	f := newADFixture(t)
	ctx := context.Background()
	run, created, err := f.service.store.InsertRun(ctx, f.db.Pool, InsertRunParams{
		ModuleID: f.moduleID, RepositoryAssignmentID: f.assignmentID,
		SubjectType: SubjectTypeUser, SubjectID: f.player.UserID,
		Kind: RunKindExploit, CommitSHA: shaA, RequestedBy: f.player.UserID,
	})
	if err != nil || !created {
		t.Fatalf("insert run: %v %v", created, err)
	}
	other := createADUser(t, ctx, f.db, []string{"user"})
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id,user_id) VALUES ($1,$2)`, f.boardID, other); err != nil {
		t.Fatal(err)
	}
	otherPrincipal := authorization.Principal{UserID: other}
	if _, err := f.service.GetMyRun(ctx, otherPrincipal, f.boardID, f.moduleID, run.ID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("other participant read own run: %v", err)
	}
	if _, _, err := f.service.GetMyRunLogs(ctx, otherPrincipal, f.boardID, f.moduleID, run.ID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("other participant read own logs: %v", err)
	}
	// A second board/module cannot expose the first module's run or vulnerability.
	secondBoard := uuid.New()
	secondModule := uuid.New()
	if _, err := f.db.Pool.Exec(ctx, `
		INSERT INTO boards (id, slug, name, participation_mode, registration_mode, visibility, status, created_by)
		VALUES ($1,$2,'Second','INDIVIDUAL','OPEN','PUBLIC','ACTIVE',$3)
	`, secondBoard, "second-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:10], f.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, enabled, created_by)
		VALUES ($1,$2,'Other AD','ATTACK_DEFENSE',ARRAY['EXPLOIT','PATCH'],TRUE,$3)
	`, secondModule, secondBoard, f.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.GetProgress(ctx, otherPrincipal, secondBoard, secondModule); !errors.Is(err, ErrBoardAccessDenied) {
		t.Fatalf("unjoined second-board progress error = %v", err)
	}
	if _, err := f.service.AdminGetRun(ctx, f.admin, secondBoard, secondModule, run.ID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("cross-board run access error = %v", err)
	}
	if _, err := f.service.AdminGetVulnerability(ctx, f.admin, secondBoard, secondModule, f.vulnerabilityID); !errors.Is(err, ErrVulnerabilityNotFound) {
		t.Fatalf("cross-board vulnerability access error = %v", err)
	}
}

func TestTeamSubjectWebhookPostgres(t *testing.T) {
	f := newADFixture(t)
	ctx := context.Background()
	captain := createADUser(t, ctx, f.db, []string{"user"})
	member := createADUser(t, ctx, f.db, []string{"user"})
	boardID := uuid.New()
	moduleID := uuid.New()
	teamID := uuid.New()
	now := time.Now().UTC()
	if _, err := f.db.Pool.Exec(ctx, `
		INSERT INTO boards (id, slug, name, participation_mode, registration_mode, visibility, status,
			starts_at, ends_at, team_min_members, team_max_members, created_by)
		VALUES ($1,$2,'Team AD','TEAM','OPEN','PUBLIC','ACTIVE',$3,$4,1,10,$5)
	`, boardID, "team-ad-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:10], now.Add(-time.Hour), now.Add(time.Hour), f.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, enabled, created_by)
		VALUES ($1,$2,'AD','ATTACK_DEFENSE',ARRAY['EXPLOIT','PATCH'],TRUE,$3)
	`, moduleID, boardID, f.admin.UserID); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []uuid.UUID{captain, member} {
		if _, err := f.db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id,user_id) VALUES ($1,$2)`, boardID, userID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Pool.Exec(ctx, `
		INSERT INTO teams (id, board_id, name, captain_user_id) VALUES ($1,$2,'Team One',$3)
	`, teamID, boardID, captain); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []uuid.UUID{captain, member} {
		if _, err := f.db.Pool.Exec(ctx, `INSERT INTO team_members (board_id,team_id,user_id) VALUES ($1,$2,$3)`, boardID, teamID, userID); err != nil {
			t.Fatal(err)
		}
	}
	secret := "team-webhook-" + uuid.NewString()
	sourceBinding := insertBinding(t, ctx, f.db, f.sealer, f.integrationID, "team/team-source", "main", secret, f.admin.UserID)
	exploitBinding := insertBinding(t, ctx, f.db, f.sealer, f.integrationID, "team/team-exploit", "main", secret, f.admin.UserID)
	assignmentID := uuid.New()
	if _, err := f.db.Pool.Exec(ctx, `
		INSERT INTO ad_repository_assignments
			(id, module_id, subject_type, subject_id, source_integration_id, source_repository, source_branch,
			 exploit_integration_id, exploit_repository, exploit_branch, source_binding_id, exploit_binding_id, created_by)
		VALUES ($1,$2,'TEAM',$3,$4,'team/team-source','main',$4,'team/team-exploit','main',$5,$6,$7)
	`, assignmentID, moduleID, teamID, f.integrationID, sourceBinding, exploitBinding, f.admin.UserID); err != nil {
		t.Fatal(err)
	}
	body := f.pushBody("team/team-source", "main", shaB)
	headers := http.Header(giteaHeaders(secret, "team-delivery-1", string(body)))
	result, err := f.service.HandleWebhook(ctx, WebhookRequest{
		BindingID: sourceBinding, Body: body, Headers: headers,
		RemoteAddr: "127.0.0.1:1234", ContentType: "application/json",
	})
	if err != nil {
		t.Fatalf("team webhook: %v", err)
	}
	if len(result.Runs) != 1 {
		t.Fatalf("team runs = %d", len(result.Runs))
	}
	var subjectID, requestedBy uuid.UUID
	if err := f.db.Pool.QueryRow(ctx, `SELECT subject_id, requested_by FROM ad_runs WHERE id=$1`, result.Runs[0]).Scan(&subjectID, &requestedBy); err != nil {
		t.Fatal(err)
	}
	if subjectID != teamID || requestedBy != captain {
		t.Fatalf("team run subject=%s requested=%s", subjectID, requestedBy)
	}
}

func TestFailRunEmitsErrorEventPostgres(t *testing.T) {
	f := newADFixture(t)
	ctx := context.Background()
	run, created, err := f.service.store.InsertRun(ctx, f.db.Pool, InsertRunParams{
		ModuleID: f.moduleID, RepositoryAssignmentID: f.assignmentID,
		SubjectType: SubjectTypeUser, SubjectID: f.player.UserID,
		Kind: RunKindExploit, CommitSHA: shaA, RequestedBy: f.player.UserID,
	})
	if err != nil || !created {
		t.Fatalf("insert run: %v %v", created, err)
	}
	if claimed, err := f.service.store.ClaimRun(ctx, run.ID); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	failed, changed, err := f.service.store.FailRun(ctx, run.ID, "CHECKER_ERROR", []string{"checker unavailable"}, AuditEvent{
		ActorUserID: f.player.UserID,
	})
	if err != nil || !changed {
		t.Fatalf("fail run: %v %v", changed, err)
	}
	if failed.Status != RunStatusError || failed.Error == nil {
		t.Fatalf("failed run = %+v", failed)
	}
	var awards, eventRows int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM ad_awards`).Scan(&awards); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE type='ad.run.completed'`).Scan(&eventRows); err != nil {
		t.Fatal(err)
	}
	if awards != 0 || eventRows != 1 {
		t.Fatalf("failure awards=%d events=%d", awards, eventRows)
	}
}
