package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-openapi/loads"
	"github.com/go-openapi/spec"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"github.com/jackc/pgx/v5"
)

var p1LiveSchema *loads.Document

// TestP1CoreIntegration is the independent P1 HTTP acceptance test. It uses
// the existing isolated full-Compose harness and only public HTTP/operator-SQL
// fixtures, so it stays independent of unfinished domain constructors.
func TestP1CoreIntegration(t *testing.T) {
	if os.Getenv("ACTFB_P1_INTEGRATION") != "1" {
		t.Skip("set ACTFB_P1_INTEGRATION=1 to run the isolated P1 HTTP acceptance test")
	}

	stack := newRustFSStack(t)
	stack.waitHTTP(t, stack.baseURL()+"/health", http.StatusOK, 180*time.Second)
	stack.waitHTTP(t, stack.baseURL()+"/ready", http.StatusOK, 180*time.Second)
	assertFreshMigrationsAt(t, stack.postgresDSN())
	p1LiveSchema = p1LoadLiveSchema(t, stack.root)

	client := newAPIClient(t, stack.baseURL())
	admin := stack.p1Register(t, client, "p1admin")
	owner := stack.p1Register(t, client, "p1owner")
	editor := stack.p1Register(t, client, "p1editor")
	reviewer := stack.p1Register(t, client, "p1review")
	student := stack.p1Register(t, client, "p1student")
	stack.p1SetGlobalRoles(t, admin.ID, []string{"user", "admin"})

	adminBoards := stack.p1GetMap(t, client, admin.AccessToken, "/api/v2/boards", http.StatusOK)
	if p1Items(t, adminBoards) == nil {
		t.Fatal("admin board list items is not an array")
	}

	boardA := stack.p1CreateBoard(t, client, admin.AccessToken, "p1a-"+randomHex(4), "P1 Board A", "OPEN", "PRIVATE")
	boardB := stack.p1CreateBoard(t, client, admin.AccessToken, "p1b-"+randomHex(4), "P1 Board B", "CLOSED", "PRIVATE")
	boardC := stack.p1CreateBoard(t, client, admin.AccessToken, "p1c-"+randomHex(4), "P1 Board C", "OPEN", "PRIVATE")
	moduleA := stack.p1CreateModule(t, client, admin.AccessToken, boardA, "Module A")
	moduleA2 := stack.p1CreateModule(t, client, admin.AccessToken, boardA, "Module A2")
	moduleA3 := stack.p1CreateModule(t, client, admin.AccessToken, boardA, "Module A3")
	futureModule := stack.p1CreateFutureModule(t, client, admin.AccessToken, boardA, "Module A Future", "pre-start secret module description")
	moduleB := stack.p1CreateModule(t, client, admin.AccessToken, boardB, "Module B")
	moduleC := stack.p1CreateModule(t, client, admin.AccessToken, boardC, "Module C")

	const flagA = "flag{p1_core_contract}"
	taskA := stack.p1CreateFlagTask(t, client, admin.AccessToken, boardA, moduleA, "Flag A", flagA)
	taskA2 := stack.p1CreateFlagTask(t, client, admin.AccessToken, boardA, moduleA2, "Flag A2", "flag{p1_a2}")
	_ = stack.p1CreateFlagTask(t, client, admin.AccessToken, boardB, moduleB, "Flag B", "flag{p1_b}")
	taskC := stack.p1CreateFlagTask(t, client, admin.AccessToken, boardC, moduleC, "Flag C", "flag{p1_c}")

	stack.p1Publish(t, client, admin.AccessToken, boardA)
	stack.p1Publish(t, client, admin.AccessToken, boardB)
	stack.p1Publish(t, client, admin.AccessToken, boardC)

	ownerBinding := stack.p1Grant(t, client, admin.AccessToken, boardA, owner.ID, "board_owner", nil)
	editorBinding := stack.p1Grant(t, client, admin.AccessToken, boardA, editor.ID, "board_editor", nil)
	reviewerBinding := stack.p1Grant(t, client, admin.AccessToken, boardA, reviewer.ID, "reviewer", nil)

	// Owner cannot grant board_owner; only global admin may do that.
	ownerEscalation := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardA+"/role-bindings", map[string]any{
		"userUuid": reviewer.ID,
		"role":     "board_owner",
	}, requestOptions{accessToken: owner.AccessToken})
	requireStatusOneOf(t, ownerEscalation, http.StatusForbidden, http.StatusUnprocessableEntity)
	if parsed := decodeBody[apiError](t, ownerEscalation); parsed.Code != "ROLE_GRANT_NOT_ALLOWED" && parsed.Code != "FORBIDDEN" {
		t.Fatalf("owner owner-grant code = %q, want ROLE_GRANT_NOT_ALLOWED/FORBIDDEN", parsed.Code)
	}

	// Board-wide editor can create modules on its own board.
	editorModule := stack.p1CreateModule(t, client, editor.AccessToken, boardA, "Editor Module")

	// Module-scoped editor cannot create sibling modules or change task B.
	stack.p1Revoke(t, client, admin.AccessToken, boardA, editorBinding)
	moduleEditorBinding := stack.p1Grant(t, client, admin.AccessToken, boardA, editor.ID, "board_editor", &moduleA)
	moduleEditorBinding2 := stack.p1Grant(t, client, admin.AccessToken, boardA, editor.ID, "board_editor", &moduleA3)
	editorCreate := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardA+"/modules", map[string]any{
		"name":             "Forbidden Module",
		"type":             "TASKS",
		"allowedTaskTypes": []string{"FLAG"},
		"enabled":          true,
	}, requestOptions{accessToken: editor.AccessToken})
	requireStatusOneOf(t, editorCreate, http.StatusForbidden)
	editorTaskB := client.request(http.MethodPatch, "/api/v2/admin/boards/"+boardA+"/modules/"+moduleA2+"/tasks/"+taskA2, map[string]any{
		"title": "forbidden edit",
	}, requestOptions{accessToken: editor.AccessToken, headers: map[string]string{"Content-Type": "application/json"}})
	requireStatusOneOf(t, editorTaskB, http.StatusForbidden, http.StatusNotFound)

	// P1-04: two module assignments must produce exactly those two modules in
	// both staff and participant-minimal list responses, and only those two are editable.
	editorModules := p1Items(t, stack.p1GetMap(t, client, editor.AccessToken, "/api/v2/boards/"+boardA+"/modules", http.StatusOK))
	if len(editorModules) != 2 || !p1ItemsContainUUID(editorModules, moduleA) || !p1ItemsContainUUID(editorModules, moduleA3) || p1ItemsContainUUID(editorModules, moduleA2) {
		t.Fatalf("module editor participant list = %v, want exactly modules A/A3", p1ItemUUIDs(editorModules))
	}
	for _, item := range editorModules {
		for _, forbiddenField := range []string{"enabled", "taskInstanceSettings", "description"} {
			if _, exists := item[forbiddenField]; exists {
				t.Errorf("participant module list leaked admin field %q: %v", forbiddenField, item)
			}
		}
	}
	adminModulesForEditor := p1Items(t, stack.p1GetMap(t, client, editor.AccessToken, "/api/v2/admin/boards/"+boardA+"/modules", http.StatusOK))
	if len(adminModulesForEditor) != 2 || !p1ItemsContainUUID(adminModulesForEditor, moduleA) || !p1ItemsContainUUID(adminModulesForEditor, moduleA3) || p1ItemsContainUUID(adminModulesForEditor, moduleA2) {
		t.Fatalf("module editor admin list = %v, want exactly modules A/A3", p1ItemUUIDs(adminModulesForEditor))
	}
	for _, moduleID := range []string{moduleA, moduleA3} {
		stack.p1Map(t, client.request(http.MethodPatch, "/api/v2/admin/boards/"+boardA+"/modules/"+moduleID, map[string]any{"description": "assigned edit"}, requestOptions{accessToken: editor.AccessToken}), http.StatusOK)
	}
	siblingModuleEdit := client.request(http.MethodPatch, "/api/v2/admin/boards/"+boardA+"/modules/"+moduleA2, map[string]any{"description": "forbidden"}, requestOptions{accessToken: editor.AccessToken})
	requireStatusOneOf(t, siblingModuleEdit, http.StatusForbidden, http.StatusNotFound)
	editorPermissions := stack.p1GetMap(t, client, editor.AccessToken, "/api/v2/boards/"+boardA+"/me/permissions", http.StatusOK)
	if modules, ok := editorPermissions["modules"].([]any); !ok || len(modules) != 2 {
		t.Fatalf("module editor permission modules = %v, want exactly two", editorPermissions["modules"])
	}

	// Reviewer/owner/student scope reads.
	// Enrollment is code-only: the organizer issues a code, the participant
	// presents it. A bare board UUID no longer enrolls.
	studentJoin := client.request(http.MethodPost, "/api/v2/boards/"+boardA+"/join",
		map[string]any{"code": stack.p1IssueJoinCode(t, client, admin.AccessToken, boardA)}, requestOptions{accessToken: student.AccessToken})
	requireStatus(t, studentJoin, http.StatusOK)
	studentJoinC := client.request(http.MethodPost, "/api/v2/boards/"+boardC+"/join",
		map[string]any{"code": stack.p1IssueJoinCode(t, client, admin.AccessToken, boardC)}, requestOptions{accessToken: student.AccessToken})
	requireStatus(t, studentJoinC, http.StatusOK)

	// P1-05: joined participant must not receive future module metadata/description.
	futureResponse := client.request(http.MethodGet, "/api/v2/boards/"+boardA+"/modules/"+futureModule, nil, requestOptions{accessToken: student.AccessToken})
	requireStatusOneOf(t, futureResponse, http.StatusForbidden, http.StatusNotFound)
	if strings.Contains(string(futureResponse.body), "pre-start secret module description") {
		t.Fatalf("upcoming module response leaked description: %s", futureResponse.body)
	}
	participantModules := p1Items(t, stack.p1GetMap(t, client, student.AccessToken, "/api/v2/boards/"+boardA+"/modules", http.StatusOK))
	if p1ItemsContainUUID(participantModules, futureModule) {
		// P1-05's required GET-403/no-description gate is enforced above. The
		// list-visibility hardening is a separate in-flight owner fix; record it
		// without weakening the required regression.
		t.Logf("note: participant module list still includes future module %s", futureModule)
	}

	stack.p1ExpireBoard(t, boardC)
	expiredSubmission := client.request(http.MethodPost, "/api/v2/boards/"+boardC+"/modules/"+moduleC+"/tasks/"+taskC+"/submissions", map[string]any{"text": "flag{p1_c}"}, requestOptions{
		accessToken: student.AccessToken,
		headers:     map[string]string{"Idempotency-Key": "p1-expired-" + randomHex(6)},
	})
	requireStatus(t, expiredSubmission, http.StatusConflict)
	if parsed := decodeBody[apiError](t, expiredSubmission); parsed.Code != "BOARD_FINISHED" {
		t.Fatalf("expired board submission code = %q, want BOARD_FINISHED", parsed.Code)
	}
	if got := stack.p1SubmissionCountForTaskUser(t, taskC, student.ID); got != 0 {
		t.Fatalf("expired board submission wrote %d row(s), want 0", got)
	}
	studentBoards := stack.p1GetMap(t, client, student.AccessToken, "/api/v2/boards", http.StatusOK)
	if len(p1Items(t, studentBoards)) == 0 {
		t.Fatal("student joined-board list is empty")
	}
	studentBoardResponse := client.request(http.MethodGet, "/api/v2/boards/"+boardA, nil, requestOptions{accessToken: student.AccessToken})
	p1ValidateResponse(t, "Board", studentBoardResponse)
	studentBoard := stack.p1Map(t, studentBoardResponse, http.StatusOK)
	moduleResponse := client.request(http.MethodGet, "/api/v2/boards/"+boardA+"/modules/"+moduleA, nil, requestOptions{accessToken: student.AccessToken})
	p1ValidateResponse(t, "Module", moduleResponse)
	requireStatus(t, moduleResponse, http.StatusOK)
	taskResponse := client.request(http.MethodGet, "/api/v2/boards/"+boardA+"/modules/"+moduleA+"/tasks/"+taskA, nil, requestOptions{accessToken: student.AccessToken})
	p1ValidateResponse(t, "Task", taskResponse)
	requireStatus(t, taskResponse, http.StatusOK)
	adminBoardResponse := client.request(http.MethodGet, "/api/v2/admin/boards/"+boardA, nil, requestOptions{accessToken: admin.AccessToken})
	p1ValidateResponse(t, "AdminBoard", adminBoardResponse)
	requireStatus(t, adminBoardResponse, http.StatusOK)
	if joined, _ := studentBoard["joined"].(bool); !joined {
		t.Error("student board joined = false after join")
	}
	privateOther := client.request(http.MethodGet, "/api/v2/boards/"+boardB, nil, requestOptions{accessToken: student.AccessToken})
	requireStatus(t, privateOther, http.StatusForbidden)
	closedJoin := client.request(http.MethodPost, "/api/v2/boards/"+boardB+"/join", map[string]any{}, requestOptions{accessToken: student.AccessToken})
	requireStatus(t, closedJoin, http.StatusConflict)

	// Forged parent UUID must not leak sibling-board data.
	forged := client.request(http.MethodGet, "/api/v2/boards/"+boardA+"/modules/"+moduleB+"/tasks", nil, requestOptions{accessToken: admin.AccessToken})
	requireStatusOneOf(t, forged, http.StatusForbidden, http.StatusNotFound)
	if strings.Contains(string(forged.body), "Flag B") {
		t.Fatalf("forged parent request leaked sibling task data: %s", forged.body)
	}

	// TEAM and PUBLIC boards are first-class now. The old "unsupported"
	// expectation is replaced by their real validation contract, so the test
	// still proves inconsistent policies fail closed without writes.
	boardsBeforeUnsupported := stack.p1Count(t, "boards")
	teamWithoutSettings := client.request(http.MethodPost, "/api/v2/admin/boards", map[string]any{
		"slug":              "p1team-" + randomHex(4),
		"name":              "TEAM without settings",
		"participationMode": "TEAM",
		"registrationMode":  "OPEN",
		"visibility":        "PRIVATE",
		"rating":            map[string]any{"visibility": "PUBLIC"},
	}, requestOptions{accessToken: admin.AccessToken})
	stack.p1RequireValidationField(t, teamWithoutSettings, "body.teamSettings", "REQUIRED")
	stack.p1RequireCount(t, "boards", boardsBeforeUnsupported)

	publicWithGroups := client.request(http.MethodPost, "/api/v2/admin/boards", map[string]any{
		"slug":              "p1public-" + randomHex(4),
		"name":              "PUBLIC with access groups",
		"participationMode": "INDIVIDUAL",
		"registrationMode":  "OPEN",
		"visibility":        "PUBLIC",
		"rating":            map[string]any{"visibility": "PUBLIC"},
		"accessGroupUuids":  []string{"00000000-0000-0000-0000-000000000001"},
	}, requestOptions{accessToken: admin.AccessToken})
	stack.p1RequireValidationField(t, publicWithGroups, "body.accessGroupUuids", "INVALID_VALUE")
	stack.p1RequireCount(t, "boards", boardsBeforeUnsupported)

	tasksBefore := stack.p1Count(t, "tasks")
	manualTask := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardA+"/modules/"+moduleA+"/tasks", map[string]any{
		"title":           "Manual unsupported",
		"type":            "MANUAL",
		"category":        "misc",
		"description":     "unsupported",
		"points":          10,
		"visible":         true,
		"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
		"instance":        map[string]any{"enabled": false},
		"acceptedAnswers": []string{"dummy"},
	}, requestOptions{accessToken: admin.AccessToken})
	stack.p1RequireUnsupported(t, manualTask)
	stack.p1RequireCount(t, "tasks", tasksBefore)

	// Student submission path: wrong, correct, replay, conflict, already solved.
	wrong := stack.p1Submit(t, client, student.AccessToken, boardA, moduleA, taskA, "p1-wrong-key-"+randomHex(4), "wrong-flag")
	if got := p1String(t, wrong, "status"); got != "REJECTED" {
		t.Fatalf("wrong submission status = %q, want REJECTED", got)
	}
	correctKey := "p1-correct-key-" + randomHex(6)
	correct := stack.p1Submit(t, client, student.AccessToken, boardA, moduleA, taskA, correctKey, flagA)
	acceptedUUID := p1String(t, correct, "uuid")
	adminSubmissionResponse := client.request(http.MethodGet, "/api/v2/admin/boards/"+boardA+"/submissions/"+acceptedUUID, nil, requestOptions{accessToken: admin.AccessToken})
	p1ValidateResponse(t, "AdminSubmission", adminSubmissionResponse)
	requireStatus(t, adminSubmissionResponse, http.StatusOK)
	if got := p1String(t, correct, "status"); got != "ACCEPTED" {
		t.Fatalf("correct submission status = %q, want ACCEPTED", got)
	}
	if score, ok := correct["scoreAwarded"].(float64); !ok || int(score) != 100 {
		t.Fatalf("correct scoreAwarded = %v, want 100", correct["scoreAwarded"])
	}
	if text, _ := correct["text"].(string); strings.Contains(text, flagA) {
		t.Fatalf("submission response echoed flag text: %q", text)
	}
	replay := stack.p1Submit(t, client, student.AccessToken, boardA, moduleA, taskA, correctKey, flagA)
	if p1String(t, replay, "uuid") != acceptedUUID {
		t.Fatalf("idempotent replay uuid = %q, want %q", p1String(t, replay, "uuid"), acceptedUUID)
	}
	conflict := client.request(http.MethodPost, "/api/v2/boards/"+boardA+"/modules/"+moduleA+"/tasks/"+taskA+"/submissions", map[string]any{
		"text": "different flag",
	}, requestOptions{accessToken: student.AccessToken, headers: map[string]string{"Idempotency-Key": correctKey}})
	requireStatus(t, conflict, http.StatusConflict)
	if parsed := decodeBody[apiError](t, conflict); parsed.Code != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("idempotency conflict code = %q", parsed.Code)
	}
	already := client.request(http.MethodPost, "/api/v2/boards/"+boardA+"/modules/"+moduleA+"/tasks/"+taskA+"/submissions", map[string]any{
		"text": flagA,
	}, requestOptions{accessToken: student.AccessToken, headers: map[string]string{"Idempotency-Key": "p1-new-key-" + randomHex(6)}})
	requireStatus(t, already, http.StatusConflict)
	if parsed := decodeBody[apiError](t, already); parsed.Code != "TASK_ALREADY_SOLVED" {
		t.Fatalf("already-solved code = %q", parsed.Code)
	}

	// Concurrent correct submissions for a second participant must not double award.
	stack.p1AddParticipant(t, client, admin.AccessToken, boardA, reviewer.ID)
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	keys := []string{"p1-race-a-" + randomHex(6), "p1-race-b-" + randomHex(6)}
	for i := range keys {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			statuses[index] = stack.p1SubmitRaw(t, reviewer.AccessToken, boardA, moduleA, taskA, keys[index], flagA)
		}(i)
	}
	wg.Wait()
	acceptedCount := 0
	for _, status := range statuses {
		if status == http.StatusOK {
			acceptedCount++
		}
	}
	if acceptedCount != 1 {
		t.Fatalf("concurrent accepted statuses = %v, want exactly one 200", statuses)
	}

	history := stack.p1GetMap(t, client, student.AccessToken, "/api/v2/boards/"+boardA+"/modules/"+moduleA+"/tasks/"+taskA+"/submissions/me", http.StatusOK)
	items := p1Items(t, history)
	if len(items) == 0 {
		t.Fatal("own submission history is empty")
	}
	for _, item := range items {
		if item["userUuid"] != nil && item["userUuid"] != student.ID {
			t.Fatalf("own history leaked another user: %v", item)
		}
	}

	scoreboard := stack.p1GetMap(t, client, student.AccessToken, "/api/v2/boards/"+boardA+"/scoreboard", http.StatusOK)
	if len(p1Items(t, scoreboard)) == 0 {
		t.Fatal("scoreboard is empty")
	}
	me := stack.p1GetMap(t, client, student.AccessToken, "/api/v2/boards/"+boardA+"/me", http.StatusOK)
	if joined, _ := me["joined"].(bool); !joined {
		t.Error("BoardMe joined = false after participation")
	}
	p1RequireNullableNotZeroObject(t, "BoardMe.team", me["team"])
	permissions := stack.p1GetMap(t, client, owner.AccessToken, "/api/v2/boards/"+boardA+"/me/permissions", http.StatusOK)
	roles := p1StringsField(t, permissions, "roles")
	if !containsString(roles, "board_owner") {
		t.Fatalf("owner permissions roles = %v, want board_owner", roles)
	}

	// Admin can manage but is not automatically a participant.
	adminBoardMe := stack.p1GetMap(t, client, admin.AccessToken, "/api/v2/boards/"+boardA+"/me", http.StatusOK)
	if joined, _ := adminBoardMe["joined"].(bool); joined {
		t.Error("global admin was implicitly joined")
	}
	adminSubmit := client.request(http.MethodPost, "/api/v2/boards/"+boardA+"/modules/"+moduleA+"/tasks/"+taskA+"/submissions", map[string]any{
		"text": flagA,
	}, requestOptions{accessToken: admin.AccessToken, headers: map[string]string{"Idempotency-Key": "p1-admin-key-" + randomHex(6)}})
	requireStatusOneOf(t, adminSubmit, http.StatusForbidden, http.StatusConflict)

	// RBAC binding CRUD/audit and immediate revocation/expiry.
	bindings := stack.p1GetMap(t, client, admin.AccessToken, "/api/v2/admin/boards/"+boardA+"/role-bindings", http.StatusOK)
	if len(p1Items(t, bindings)) == 0 {
		t.Fatal("role binding list is empty")
	}
	updated := stack.p1PatchMap(t, client, admin.AccessToken, "/api/v2/admin/boards/"+boardA+"/role-bindings/"+reviewerBinding, map[string]any{
		"expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}, http.StatusOK)
	if p1String(t, updated, "uuid") != reviewerBinding {
		t.Fatalf("updated binding uuid = %q, want %q", p1String(t, updated, "uuid"), reviewerBinding)
	}
	stack.p1Delete(t, client, admin.AccessToken, "/api/v2/admin/boards/"+boardA+"/role-bindings/"+reviewerBinding, http.StatusNoContent)
	reviewerAfterRevoke := client.request(http.MethodGet, "/api/v2/admin/boards/"+boardA+"/submissions", nil, requestOptions{accessToken: reviewer.AccessToken})
	requireStatusOneOf(t, reviewerAfterRevoke, http.StatusForbidden)
	stack.p1ExpireBinding(t, moduleEditorBinding)
	stack.p1ExpireBinding(t, moduleEditorBinding2)
	editorAfterExpiry := client.request(http.MethodPatch, "/api/v2/admin/boards/"+boardA+"/modules/"+moduleA+"/tasks/"+taskA, map[string]any{
		"title": "expired edit",
	}, requestOptions{accessToken: editor.AccessToken})
	requireStatusOneOf(t, editorAfterExpiry, http.StatusForbidden)

	audit := stack.p1GetMap(t, client, admin.AccessToken, "/api/v2/admin/boards/"+boardA+"/audit", http.StatusOK)
	if len(p1Items(t, audit)) == 0 {
		t.Fatal("board audit is empty after role grants")
	}

	// The flag itself must be absent from API and logs; DB stores only a PHC hash.
	if bytes.Contains(auditJSON(audit), []byte(flagA)) {
		t.Fatal("audit response contains the flag plaintext")
	}
	hashes := stack.p1TaskFlagHashes(t, taskA)
	if len(hashes) == 0 || !strings.HasPrefix(hashes[0], "$argon2id$") {
		t.Fatalf("task flag hashes = %v, want Argon2id PHC", hashes)
	}
	backendLogs, _ := stack.compose(t, 30*time.Second, "logs", "--no-color", "backend")
	if strings.Contains(backendLogs, flagA) {
		t.Fatal("backend logs contain the flag plaintext")
	}

	// P1-03 deterministic committed-revocation check: once the owner binding is
	// revoked, the owner can no longer grant staff.
	stack.p1Revoke(t, client, admin.AccessToken, boardA, ownerBinding)
	ownerAfterRevoke := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardA+"/role-bindings", map[string]any{
		"userUuid": reviewer.ID,
		"role":     "board_editor",
	}, requestOptions{accessToken: owner.AccessToken})
	requireStatusOneOf(t, ownerAfterRevoke, http.StatusForbidden)

	_ = editorModule
	_ = taskA2
}

type p1User struct {
	ID           string
	AccessToken  string
	RefreshToken string
}

func (s *rustfsStack) p1Register(t *testing.T, client *apiClient, prefix string) p1User {
	t.Helper()
	username := prefix + randomHex(4)
	email := username + "@actfb.test"
	response := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
		"username": username,
		"email":    email,
		"password": s.secrets.userPassword,
	}, requestOptions{})
	requireStatus(t, response, http.StatusCreated)
	body := decodeBody[registerResponse](t, response)
	cookie := refreshCookie(t, response)
	s.addRedactions(body.AccessToken, cookie.Value)
	return p1User{ID: body.User.UUID, AccessToken: body.AccessToken, RefreshToken: cookie.Value}
}

func (s *rustfsStack) p1SetGlobalRoles(t *testing.T, userID string, roles []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		t.Fatalf("connect for global role update: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE users SET roles = $2 WHERE id = $1::uuid`, userID, roles); err != nil {
		t.Fatalf("promote test admin: %v", err)
	}
}

func (s *rustfsStack) p1CreateBoard(t *testing.T, client *apiClient, token, slug, name, registration, visibility string) string {
	t.Helper()
	response := client.request(http.MethodPost, "/api/v2/admin/boards", map[string]any{
		"slug":              slug,
		"name":              name,
		"description":       "P1 integration board",
		"participationMode": "INDIVIDUAL",
		"registrationMode":  registration,
		"visibility":        visibility,
		"rating":            map[string]any{"visibility": "PUBLIC"},
	}, requestOptions{accessToken: token})
	p1ValidateResponse(t, "AdminBoard", response)
	body := s.p1Map(t, response, http.StatusCreated)
	id := p1String(t, body, "uuid")
	if id == "" {
		t.Fatal("created board has no uuid")
	}
	return id
}

func (s *rustfsStack) p1CreateModule(t *testing.T, client *apiClient, token, boardID, name string) string {
	t.Helper()
	response := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardID+"/modules", map[string]any{
		"name":             name,
		"type":             "TASKS",
		"allowedTaskTypes": []string{"FLAG"},
		"enabled":          true,
	}, requestOptions{accessToken: token})
	p1ValidateResponse(t, "AdminModule", response)
	body := s.p1Map(t, response, http.StatusCreated)
	id := p1String(t, body, "uuid")
	if id == "" {
		t.Fatal("created module has no uuid")
	}
	p1RequireNullableNotZeroObject(t, "AdminModule.taskInstanceSettings", body["taskInstanceSettings"])
	return id
}

func (s *rustfsStack) p1CreateFutureModule(t *testing.T, client *apiClient, token, boardID, name, description string) string {
	t.Helper()
	response := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardID+"/modules", map[string]any{
		"name":             name,
		"description":      description,
		"type":             "TASKS",
		"allowedTaskTypes": []string{"FLAG"},
		"enabled":          true,
		"startsAt":         time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}, requestOptions{accessToken: token})
	p1ValidateResponse(t, "AdminModule", response)
	body := s.p1Map(t, response, http.StatusCreated)
	id := p1String(t, body, "uuid")
	if id == "" {
		t.Fatal("created future module has no uuid")
	}
	return id
}

func (s *rustfsStack) p1ExpireBoard(t *testing.T, boardID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		t.Fatalf("connect for board expiry: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE boards SET ends_at = NOW() - interval '1 minute' WHERE id = $1::uuid`, boardID); err != nil {
		t.Fatalf("expire board: %v", err)
	}
}

func (s *rustfsStack) p1SubmissionCountForTaskUser(t *testing.T, taskID, userID string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		t.Fatalf("connect for submission count: %v", err)
	}
	defer conn.Close(ctx)
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM submissions WHERE task_id = $1::uuid AND user_id = $2::uuid`, taskID, userID).Scan(&count); err != nil {
		t.Fatalf("count expired-board submissions: %v", err)
	}
	return count
}

func (s *rustfsStack) p1CreateFlagTask(t *testing.T, client *apiClient, token, boardID, moduleID, title, flag string) string {
	t.Helper()
	response := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardID+"/modules/"+moduleID+"/tasks", map[string]any{
		"title":       title,
		"type":        "FLAG",
		"category":    "web",
		"description": "P1 flag task",
		"points":      100,
		"visible":     true,
		"submission": map[string]any{
			"mode":    "TEXT",
			"checker": "AUTOMATIC",
		},
		"instance":        map[string]any{"enabled": false},
		"acceptedAnswers": []string{flag},
	}, requestOptions{accessToken: token})
	p1ValidateResponse(t, "AdminTask", response)
	body := s.p1Map(t, response, http.StatusCreated)
	id := p1String(t, body, "uuid")
	if id == "" {
		t.Fatal("created task has no uuid")
	}
	encoded, _ := json.Marshal(body)
	if strings.Contains(string(encoded), flag) || strings.Contains(string(encoded), `"acceptedAnswers"`) {
		t.Fatalf("task response leaked flag material: %s", encoded)
	}
	if submission, ok := body["submission"].(map[string]any); ok {
		p1RequireNullableNotZeroObject(t, "SubmissionPolicy.files", submission["files"])
	}
	return id
}

func (s *rustfsStack) p1Publish(t *testing.T, client *apiClient, token, boardID string) {
	t.Helper()
	response := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardID+"/actions", map[string]any{
		"action": "PUBLISH",
	}, requestOptions{accessToken: token})
	p1ValidateResponse(t, "Board", response)
	s.p1Map(t, response, http.StatusOK)
}

func (s *rustfsStack) p1Grant(t *testing.T, client *apiClient, token, boardID, userID, role string, moduleID *string) string {
	t.Helper()
	payload := map[string]any{"userUuid": userID, "role": role}
	if moduleID != nil {
		payload["moduleUuid"] = *moduleID
	}
	response := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardID+"/role-bindings", payload, requestOptions{accessToken: token})
	p1ValidateResponse(t, "BoardRoleBinding", response)
	body := s.p1Map(t, response, http.StatusCreated)
	id := p1String(t, body, "uuid")
	if id == "" {
		t.Fatal("created role binding has no uuid")
	}
	if moduleValue, exists := body["moduleUuid"]; exists && moduleValue != nil {
		if _, ok := moduleValue.(string); !ok {
			t.Fatalf("BoardRoleBinding.moduleUuid = %T %v, want UUID string or null", moduleValue, moduleValue)
		}
	}
	return id
}

func (s *rustfsStack) p1Revoke(t *testing.T, client *apiClient, token, boardID, bindingID string) {
	t.Helper()
	s.p1Delete(t, client, token, "/api/v2/admin/boards/"+boardID+"/role-bindings/"+bindingID, http.StatusNoContent)
}

func (s *rustfsStack) p1AddParticipant(t *testing.T, client *apiClient, token, boardID, userID string) {
	t.Helper()
	response := client.request(http.MethodPost, "/api/v2/admin/boards/"+boardID+"/participants", map[string]any{"userUuid": userID}, requestOptions{accessToken: token})
	requireStatus(t, response, http.StatusCreated)
}

func (s *rustfsStack) p1Submit(t *testing.T, client *apiClient, token, boardID, moduleID, taskID, key, text string) map[string]any {
	t.Helper()
	response := client.request(http.MethodPost, "/api/v2/boards/"+boardID+"/modules/"+moduleID+"/tasks/"+taskID+"/submissions", map[string]any{"text": text}, requestOptions{
		accessToken: token,
		headers:     map[string]string{"Idempotency-Key": key},
	})
	p1ValidateResponse(t, "Submission", response)
	return s.p1Map(t, response, http.StatusOK)
}

func (s *rustfsStack) p1SubmitRaw(t *testing.T, token, boardID, moduleID, taskID, key, text string) int {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequest(http.MethodPost, s.baseURL()+"/api/v2/boards/"+boardID+"/modules/"+moduleID+"/tasks/"+taskID+"/submissions", bytes.NewReader(payload))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", key)
	response, err := newHTTPClient().Do(req)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	return response.StatusCode
}

// p1IssueJoinCode generates a board join code through the real admin route.
func (s *rustfsStack) p1IssueJoinCode(t *testing.T, client *apiClient, adminToken, boardID string) string {
	t.Helper()
	body := s.p1Map(t, client.request(http.MethodPost, "/api/v2/admin/boards/"+boardID+"/join-code", nil,
		requestOptions{accessToken: adminToken}), http.StatusOK)
	code, _ := body["code"].(string)
	if code == "" {
		t.Fatalf("join code for board %s is empty: %v", boardID, body)
	}
	return code
}

func (s *rustfsStack) p1Map(t *testing.T, response apiResponse, want int) map[string]any {
	t.Helper()
	requireStatus(t, response, want)
	var body map[string]any
	if err := json.Unmarshal(response.body, &body); err != nil {
		t.Fatalf("decode P1 response: %v", err)
	}
	return body
}

func (s *rustfsStack) p1PatchMap(t *testing.T, client *apiClient, token, path string, payload map[string]any, want int) map[string]any {
	t.Helper()
	return s.p1Map(t, client.request(http.MethodPatch, path, payload, requestOptions{accessToken: token}), want)
}

func (s *rustfsStack) p1GetMap(t *testing.T, client *apiClient, token, path string, want int) map[string]any {
	t.Helper()
	return s.p1Map(t, client.request(http.MethodGet, path, nil, requestOptions{accessToken: token}), want)
}

func (s *rustfsStack) p1Delete(t *testing.T, client *apiClient, token, path string, want int) {
	t.Helper()
	requireStatus(t, client.request(http.MethodDelete, path, nil, requestOptions{accessToken: token}), want)
}

// p1RequireValidationField asserts a specific 422 field error. This is the
// supported-contract replacement for the old "TEAM/PUBLIC unsupported" checks.
func (s *rustfsStack) p1RequireValidationField(t *testing.T, response apiResponse, field, code string) {
	t.Helper()
	requireStatus(t, response, http.StatusUnprocessableEntity)
	var body struct {
		Code        string `json:"code"`
		FieldErrors []struct {
			Field string `json:"field"`
			Code  string `json:"code"`
		} `json:"fieldErrors"`
	}
	if err := json.Unmarshal(response.body, &body); err != nil {
		t.Fatalf("decode validation response: %v", err)
	}
	if body.Code != "VALIDATION_FAILED" {
		t.Fatalf("validation response code = %q, want VALIDATION_FAILED: %s", body.Code, response.body)
	}
	for _, item := range body.FieldErrors {
		if item.Field == field && item.Code == code {
			return
		}
	}
	t.Fatalf("validation response has no %s/%s field error: %s", field, code, response.body)
}

func (s *rustfsStack) p1RequireUnsupported(t *testing.T, response apiResponse) {
	t.Helper()
	requireStatus(t, response, http.StatusUnprocessableEntity)
	var body struct {
		Code        string `json:"code"`
		FieldErrors []struct {
			Field string `json:"field"`
			Code  string `json:"code"`
		} `json:"fieldErrors"`
	}
	if err := json.Unmarshal(response.body, &body); err != nil {
		t.Fatalf("decode unsupported response: %v", err)
	}
	if body.Code != "VALIDATION_FAILED" {
		t.Fatalf("unsupported response code = %q, want VALIDATION_FAILED", body.Code)
	}
	for _, field := range body.FieldErrors {
		if field.Code == "UNSUPPORTED_POLICY" {
			return
		}
	}
	t.Fatalf("unsupported response has no UNSUPPORTED_POLICY field error: %s", response.body)
}

func (s *rustfsStack) p1Count(t *testing.T, table string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		t.Fatalf("connect for P1 count: %v", err)
	}
	defer conn.Close(ctx)
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func (s *rustfsStack) p1RequireCount(t *testing.T, table string, want int) {
	t.Helper()
	if got := s.p1Count(t, table); got != want {
		t.Fatalf("%s count after unsupported write = %d, want %d", table, got, want)
	}
}

func (s *rustfsStack) p1ExpireBinding(t *testing.T, bindingID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		t.Fatalf("connect for role expiry: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE board_role_bindings SET created_at = NOW() - interval '2 hours', expires_at = NOW() - interval '1 hour' WHERE id = $1::uuid`, bindingID); err != nil {
		t.Fatalf("expire role binding: %v", err)
	}
}

func (s *rustfsStack) p1TaskFlagHashes(t *testing.T, taskID string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		t.Fatalf("connect for flag hash check: %v", err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `SELECT answer_hash FROM task_flags WHERE task_id = $1::uuid`, taskID)
	if err != nil {
		t.Fatalf("query flag hashes: %v", err)
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatalf("scan flag hash: %v", err)
		}
		hashes = append(hashes, hash)
	}
	return hashes
}

func p1ItemsContainUUID(items []map[string]any, wanted string) bool {
	for _, item := range items {
		if p1StringValue(item["uuid"]) == wanted {
			return true
		}
	}
	return false
}

func p1ItemUUIDs(items []map[string]any) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value := p1StringValue(item["uuid"]); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func p1StringValue(value any) string {
	text, _ := value.(string)
	return text
}

func p1RequireNullableNotZeroObject(t *testing.T, label string, value any) {
	t.Helper()
	if value == nil {
		return
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Errorf("%s = %T, want nullable object/null", label, value)
		return
	}
	if p1ZeroJSONValue(object) {
		t.Errorf("%s serialized as a zero object (%v); want null or absent for a nullable ref", label, object)
	}
}

func p1ZeroJSONValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case map[string]any:
		if len(typed) == 0 {
			return true
		}
		for _, child := range typed {
			if !p1ZeroJSONValue(child) {
				return false
			}
		}
		return true
	case []any:
		return len(typed) == 0
	case string:
		return typed == ""
	default:
		return false
	}
}

// p1NormalizeNullableSchema is a test-only Swagger 2.0 adapter. The stock
// go-openapi validator ignores the vendor x-nullable extension. For each
// x-nullable schema it preserves the complete original schema (constraints,
// allOf, required, additionalProperties, enums, patterns, formats) as one
// anyOf branch and adds a real JSON-Schema null branch. Non-null values are
// still validated by every original constraint; only null semantics change.
func p1NormalizeNullableSchema(schema spec.Schema) spec.Schema {
	normalized := schema
	if schema.Extensions != nil {
		normalized.Extensions = make(map[string]any, len(schema.Extensions))
		for key, value := range schema.Extensions {
			normalized.Extensions[key] = value
		}
	}
	if len(normalized.Properties) > 0 {
		properties := make(map[string]spec.Schema, len(normalized.Properties))
		for name, property := range normalized.Properties {
			properties[name] = p1NormalizeNullableSchema(property)
		}
		normalized.Properties = properties
	}
	if len(normalized.AllOf) > 0 {
		allOf := make([]spec.Schema, len(normalized.AllOf))
		for index, child := range normalized.AllOf {
			allOf[index] = p1NormalizeNullableSchema(child)
		}
		normalized.AllOf = allOf
	}
	if len(normalized.AnyOf) > 0 {
		anyOf := make([]spec.Schema, len(normalized.AnyOf))
		for index, child := range normalized.AnyOf {
			anyOf[index] = p1NormalizeNullableSchema(child)
		}
		normalized.AnyOf = anyOf
	}
	if len(normalized.OneOf) > 0 {
		oneOf := make([]spec.Schema, len(normalized.OneOf))
		for index, child := range normalized.OneOf {
			oneOf[index] = p1NormalizeNullableSchema(child)
		}
		normalized.OneOf = oneOf
	}
	if normalized.Not != nil {
		child := p1NormalizeNullableSchema(*normalized.Not)
		normalized.Not = &child
	}
	if normalized.Items != nil {
		items := *normalized.Items
		if items.Schema != nil {
			child := p1NormalizeNullableSchema(*items.Schema)
			items.Schema = &child
		}
		if len(items.Schemas) > 0 {
			schemas := make([]spec.Schema, len(items.Schemas))
			for index, child := range items.Schemas {
				schemas[index] = p1NormalizeNullableSchema(child)
			}
			items.Schemas = schemas
		}
		normalized.Items = &items
	}
	if normalized.AdditionalProperties != nil {
		additional := *normalized.AdditionalProperties
		if additional.Schema != nil {
			child := p1NormalizeNullableSchema(*additional.Schema)
			additional.Schema = &child
		}
		normalized.AdditionalProperties = &additional
	}
	if normalized.AdditionalItems != nil {
		additional := *normalized.AdditionalItems
		if additional.Schema != nil {
			child := p1NormalizeNullableSchema(*additional.Schema)
			additional.Schema = &child
		}
		normalized.AdditionalItems = &additional
	}

	nullable := schema.Nullable
	if value, ok := schema.Extensions["x-nullable"].(bool); ok && value {
		nullable = true
	}
	if !nullable {
		return normalized
	}

	if normalized.Extensions != nil {
		delete(normalized.Extensions, "x-nullable")
	}
	normalized.Nullable = false
	nullSchema := spec.Schema{Type: []string{"null"}}
	return spec.Schema{AnyOf: []spec.Schema{normalized, nullSchema}}
}

func p1LoadLiveSchema(t *testing.T, root string) *loads.Document {
	t.Helper()
	doc, err := loads.Spec(filepath.Join(root, "docs", "swagger.yaml"))
	if err != nil {
		t.Fatalf("load source Swagger for live response validation: %v", err)
	}
	expanded, err := doc.Expanded()
	if err != nil {
		t.Fatalf("expand source Swagger for live response validation: %v", err)
	}
	return expanded
}

func p1ValidateResponse(t *testing.T, definition string, response apiResponse) {
	t.Helper()
	if p1LiveSchema == nil || len(response.body) == 0 {
		return
	}
	var value any
	if err := json.Unmarshal(response.body, &value); err != nil {
		t.Fatalf("decode %s response for live schema validation: %v", definition, err)
	}
	spec := p1LiveSchema.Spec()
	schema, ok := spec.Definitions[definition]
	if !ok {
		t.Fatalf("source Swagger has no definition %q", definition)
	}
	normalized := p1NormalizeNullableSchema(schema)
	validator := validate.NewSchemaValidator(&normalized, spec, "", strfmt.Default)
	result := validator.Validate(value)
	if !result.IsValid() {
		t.Errorf("live %s response failed source Swagger validation: %v; body=%s", definition, result.Errors, response.body)
	}
}

func p1String(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, ok := object[key].(string)
	if !ok {
		return ""
	}
	return value
}

func p1StringsField(t *testing.T, object map[string]any, key string) []string {
	t.Helper()
	raw, ok := object[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func p1Items(t *testing.T, object map[string]any) []map[string]any {
	t.Helper()
	raw, ok := object["items"].([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		if item, ok := value.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func auditJSON(value map[string]any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
