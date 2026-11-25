package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

// TestDraftVisibilityRequiresTasksManage proves the draft gate in the preview:
// a reviewer (no module.tasks.manage) must not see an unpublished task, while a
// module editor must.
func TestDraftVisibilityRequiresTasksManage(t *testing.T) {
	f := newStaffReadHTTPFixture(t)
	b := seedStaffReadBoard(t, f, "staff-draft-scope")

	reviewerUser, reviewerToken := registerHTTPUser(t, f.ctx, f.users, f.db, "draft_reviewer", false)
	reviewer := reviewerUser.ID
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1, $2, 'reviewer', (SELECT id FROM users WHERE username='admin_staff-draft-scope'))`,
		b.boardID, reviewer); err != nil {
		t.Fatalf("grant reviewer: %v", err)
	}
	// Reviewer: visible task only, no draft.
	reviewerPage := decode[oapimodels.TaskListResponse](t, doJSON(t, f.router, http.MethodGet,
		b.modulePath(b.moduleID)+"/tasks?limit=50&offset=0", reviewerToken, nil, nil, http.StatusOK), http.StatusOK)
	for _, item := range reviewerPage.Items {
		if item.UUID != nil && *item.UUID == oapimodels.UUID(b.draftID) {
			t.Fatalf("reviewer preview exposed the draft task: %+v", item)
		}
	}
	// The draft detail is not available either.
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks/"+b.draftID,
		reviewerToken, nil, nil, http.StatusForbidden)

	// A reviewer also reads the board shell and module detail as a preview.
	doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+b.boardID, reviewerToken, nil, nil, http.StatusOK)
	reviewerModule := decode[oapimodels.Module](t, doJSON(t, f.router, http.MethodGet,
		b.modulePath(b.moduleID), reviewerToken, nil, nil, http.StatusOK), http.StatusOK)
	if reviewerModule.Preview == nil || !*reviewerModule.Preview {
		t.Fatalf("reviewer module read is not a preview: %+v", reviewerModule)
	}

	// A module editor holds module.tasks.manage and sees the draft.
	editorUser, editorToken := registerHTTPUser(t, f.ctx, f.users, f.db, "draft_editor", false)
	editor := editorUser.ID
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
		VALUES ($1, $2, $3, 'board_editor', (SELECT id FROM users WHERE username='admin_staff-draft-scope'))`,
		b.boardID, b.moduleID, editor); err != nil {
		t.Fatalf("grant module editor: %v", err)
	}
	editorPage := decode[oapimodels.TaskListResponse](t, doJSON(t, f.router, http.MethodGet,
		b.modulePath(b.moduleID)+"/tasks?limit=50&offset=0", editorToken, nil, nil, http.StatusOK), http.StatusOK)
	foundDraft := false
	for _, item := range editorPage.Items {
		if item.UUID != nil && *item.UUID == oapimodels.UUID(b.draftID) {
			foundDraft = true
		}
	}
	if !foundDraft {
		t.Fatalf("module editor did not see the draft task: %+v", editorPage.Items)
	}
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks/"+b.draftID,
		editorToken, nil, nil, http.StatusOK)
	// The module editor reads the board shell and module detail as a preview too.
	doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+b.boardID, editorToken, nil, nil, http.StatusOK)
	editorModule := decode[oapimodels.Module](t, doJSON(t, f.router, http.MethodGet,
		b.modulePath(b.moduleID), editorToken, nil, nil, http.StatusOK), http.StatusOK)
	if editorModule.Preview == nil || !*editorModule.Preview {
		t.Fatalf("module editor read is not a preview: %+v", editorModule)
	}
}

// TestJoinedNonAdminWithScopedRoleSeesPreviewOnly covers the matrix cell that the
// joined-admin test does not: a joined NON-admin participant whose access group
// was revoked, then granted a scoped role, reads a preview and gains no
// participant-own action.
func TestJoinedNonAdminWithScopedRoleSeesPreviewOnly(t *testing.T) {
	f := newStaffReadHTTPFixture(t)
	b := seedStaffReadBoard(t, f, "staff-nonadmin-mixed")

	player, playerToken := registerHTTPUser(t, f.ctx, f.users, f.db, "nonadmin_mixed", false)
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)`, b.groupID, player.ID); err != nil {
		t.Fatalf("join group: %v", err)
	}
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+b.boardID+"/join", playerToken,
		map[string]any{"code": issueJoinCode(t, f.router, b.boardID, b.adminToken)}, nil, http.StatusOK)

	// Revoke the group, then grant a board-wide editor role. Roles never make a
	// revoked participant eligible for own actions.
	if _, err := f.db.Pool.Exec(f.ctx, `
		DELETE FROM access_group_members WHERE group_id=$1 AND user_id=$2`, b.groupID, player.ID); err != nil {
		t.Fatalf("revoke group: %v", err)
	}
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1,$2,'board_editor',(SELECT id FROM users WHERE username='admin_staff-nonadmin-mixed'))`,
		b.boardID, player.ID); err != nil {
		t.Fatalf("grant editor: %v", err)
	}

	// Board shell, module and tasks: 200 preview.
	shell := decode[oapimodels.Board](t, doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+b.boardID,
		playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if shell.Preview == nil || !*shell.Preview || shell.CanParticipate == nil || *shell.CanParticipate {
		t.Fatalf("non-admin mixed board shell = preview:%v canParticipate:%v", shell.Preview, shell.CanParticipate)
	}
	module := decode[oapimodels.Module](t, doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID),
		playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if module.Preview == nil || !*module.Preview || module.CanParticipate == nil || *module.CanParticipate {
		t.Fatalf("non-admin mixed module = preview:%v canParticipate:%v", module.Preview, module.CanParticipate)
	}
	task := decode[oapimodels.Task](t, doJSON(t, f.router, http.MethodGet,
		b.modulePath(b.moduleID)+"/tasks/"+b.taskID, playerToken, nil, nil, http.StatusOK), http.StatusOK)
	if task.Preview == nil || !*task.Preview || task.Solved != nil && *task.Solved {
		t.Fatalf("non-admin mixed task = %+v", task)
	}
	// No participant-own action or own progress.
	submitPath := b.modulePath(b.moduleID) + "/tasks/" + b.taskID + "/submissions"
	doJSON(t, f.router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{staff-preview-secret}"},
		map[string]string{"Idempotency-Key": "nonadmin-mixed-submit-01"}, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet, submitPath+"/me", playerToken, nil, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet, "/api/v2/boards/"+b.boardID+"/scoreboard/me",
		playerToken, nil, nil, http.StatusForbidden)
}

// TestSubmissionReplayHonoursLiveRevocation covers B10: an eligible replay after
// the deadline still returns the original verdict, while a revoked access group
// blocks the replay instead of returning a private verdict.
func TestSubmissionReplayHonoursLiveRevocation(t *testing.T) {
	f := newStaffReadHTTPFixture(t)
	b := seedStaffReadBoard(t, f, "staff-replay")

	playerUser, playerToken := registerHTTPUser(t, f.ctx, f.users, f.db, "replay_player", false)
	player := playerUser.ID
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2)`, b.groupID, player); err != nil {
		t.Fatalf("join group: %v", err)
	}
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+b.boardID+"/join", playerToken,
		map[string]any{"code": issueJoinCode(t, f.router, b.boardID, b.adminToken)}, nil, http.StatusOK)

	submitPath := b.modulePath(b.moduleID) + "/tasks/" + b.taskID + "/submissions"
	key := "replay-live-revocation-0001"
	first := decode[oapimodels.Submission](t, doJSON(t, f.router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{staff-preview-secret}"}, map[string]string{"Idempotency-Key": key}, http.StatusOK), http.StatusOK)
	if first.Status == nil || *first.Status != oapimodels.SubmissionStatusACCEPTED {
		t.Fatalf("first submission = %+v", first)
	}

	// Close the board window: an eligible replay must still return the original
	// verdict without a new write.
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE boards SET ends_at = now() - interval '1 minute' WHERE id=$1`, b.boardID); err != nil {
		t.Fatalf("close window: %v", err)
	}
	replay := decode[oapimodels.Submission](t, doJSON(t, f.router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{staff-preview-secret}"}, map[string]string{"Idempotency-Key": key}, http.StatusOK), http.StatusOK)
	if replay.UUID == nil || first.UUID == nil || *replay.UUID != *first.UUID {
		t.Fatalf("eligible replay changed the submission: %+v vs %+v", replay, first)
	}

	// Revoke the access group: the replay is denied, and own history is denied.
	if _, err := f.db.Pool.Exec(f.ctx, `
		DELETE FROM access_group_members WHERE group_id=$1 AND user_id=$2`, b.groupID, player); err != nil {
		t.Fatalf("revoke group: %v", err)
	}
	doJSON(t, f.router, http.MethodPost, submitPath, playerToken,
		map[string]any{"text": "flag{staff-preview-secret}"}, map[string]string{"Idempotency-Key": key}, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet, submitPath+"/me", playerToken, nil, nil, http.StatusForbidden)
}

// TestSiblingTaskDetailDoesNotLeakCrossModule covers the task-detail parent
// check: a task UUID from another module must not be readable through a
// permitted module parent.
func TestSiblingTaskDetailDoesNotLeakCrossModule(t *testing.T) {
	f := newStaffReadHTTPFixture(t)
	b := seedStaffReadBoard(t, f, "staff-cross-parent")

	crossUser, editorToken := registerHTTPUser(t, f.ctx, f.users, f.db, "cross_editor", false)
	editor := crossUser.ID
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
		VALUES ($1, $2, $3, 'board_editor', (SELECT id FROM users WHERE username='admin_staff-cross-parent'))`,
		b.boardID, b.moduleID, editor); err != nil {
		t.Fatalf("grant module editor: %v", err)
	}
	// A real task in the sibling module.
	siblingTask := decode[oapimodels.AdminTask](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+b.boardID+"/modules/"+b.siblingID+"/tasks", b.adminToken, map[string]any{
			"title": "Sibling flag", "type": "FLAG", "category": "web", "description": "sibling",
			"points": 10, "visible": true,
			"submission":      map[string]any{"mode": "TEXT", "checker": "AUTOMATIC"},
			"acceptedAnswers": []string{"flag{sibling}"},
			"instance":        map[string]any{"enabled": false},
		}, nil, http.StatusCreated), http.StatusCreated)

	// The sibling task through the permitted module parent must not resolve.
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.moduleID)+"/tasks/"+string(*siblingTask.UUID),
		editorToken, nil, nil, http.StatusNotFound)
	// And through its own (out-of-scope) parent it is denied.
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.siblingID)+"/tasks/"+string(*siblingTask.UUID),
		editorToken, nil, nil, http.StatusForbidden)
	// Sanity: the admin can read it through the right parent.
	doJSON(t, f.router, http.MethodGet, b.modulePath(b.siblingID)+"/tasks/"+string(*siblingTask.UUID),
		b.adminToken, nil, nil, http.StatusOK)
}

// TestJoinCodeStatusRevisionStartsAtZero documents the initial revision and the
// first-create precondition value.
func TestJoinCodeStatusRevisionStartsAtZero(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	base := "/api/v2/admin/boards/" + f.boardID + "/join-code"
	status := decode[oapimodels.BoardJoinCodeStatus](t, doJSON(t, f.router, http.MethodGet, base, f.admin,
		nil, nil, http.StatusOK), http.StatusOK)
	if status.Configured == nil || *status.Configured || status.Revision == nil || *status.Revision != 0 {
		t.Fatalf("initial status = %+v, want unconfigured revision 0", status)
	}
	// expectedRevision 0 is accepted for the first create.
	created := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{"expectedRevision": 0}, nil, http.StatusOK), http.StatusOK)
	if created.Revision == nil || *created.Revision != 1 {
		t.Fatalf("create with expectedRevision 0 = %+v", created)
	}
	// A non-zero expected revision on a board with no code is stale.
	other := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards", f.admin,
		map[string]any{
			"slug": "join-rev-" + uuid.NewString()[:8], "name": "Rev", "participationMode": "INDIVIDUAL",
			"registrationMode": "OPEN", "visibility": "PUBLIC",
			"rating": map[string]any{"visibility": "PUBLIC"},
		}, nil, http.StatusCreated), http.StatusCreated)
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+string(*other.UUID)+"/join-code", f.admin,
		map[string]any{"expectedRevision": 7}, nil, http.StatusConflict)
}
