package boards

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// newProgressTestContext builds an isolated real-PG schema plus the live access
// policy the participant read gates require. Every progress test is opt-in via
// ACTFB_TEST_POSTGRES_DSN and skips (never silently passes) without a database.
func newProgressTestContext(t *testing.T) (context.Context, *postgres.DB, *Service, *access.AccessService, func()) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the student task progress tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	service := newAccessModeService(t, ctx, db)
	accessSvc := access.NewService(db, authorization.NewAuthorizer(db))
	return ctx, db, service, accessSvc, func() {
		cleanup()
		cancel()
	}
}

func newProgressAdmin(t *testing.T, ctx context.Context, db *postgres.DB) (authorization.Principal, uuid.UUID) {
	t.Helper()
	id := createBoardUser(t, ctx, db, "progress_admin", []string{"admin"})
	return authorization.Principal{UserID: id, GlobalRoles: []string{"admin"}}, id
}

func newProgressUser(t *testing.T, ctx context.Context, db *postgres.DB, username string) (authorization.Principal, uuid.UUID) {
	t.Helper()
	id := createBoardUser(t, ctx, db, username, []string{"user"})
	return authorization.Principal{UserID: id, GlobalRoles: []string{"user"}}, id
}

func progressBoard(t *testing.T, ctx context.Context, service *Service, admin authorization.Principal, input CreateBoardInput) *Board {
	t.Helper()
	board, err := service.AdminCreateBoard(ctx, admin, input)
	if err != nil {
		t.Fatalf("create board %s: %v", input.Slug, err)
	}
	return board
}

func progressModule(t *testing.T, ctx context.Context, service *Service, admin authorization.Principal, boardID uuid.UUID, input CreateModuleInput) *Module {
	t.Helper()
	module, err := service.AdminCreateModule(ctx, admin, boardID, input)
	if err != nil {
		t.Fatalf("create module %s: %v", input.Name, err)
	}
	return module
}

func progressTask(t *testing.T, ctx context.Context, service *Service, admin authorization.Principal, boardID, moduleID uuid.UUID, input CreateTaskInput) *Task {
	t.Helper()
	task, err := service.AdminCreateTask(ctx, admin, boardID, moduleID, input)
	if err != nil {
		t.Fatalf("create task %s: %v", input.Title, err)
	}
	return task
}

func flagTaskInput(title string, points int, visible bool) CreateTaskInput {
	return CreateTaskInput{
		Title: title, Type: TaskTypeFlag, Category: "web", Points: points, Visible: visible,
		Submission:      SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic},
		AcceptedAnswers: []string{"flag{" + title + "}"}, Instance: TaskInstanceInput{Enabled: false},
	}
}

func publishProgressBoard(t *testing.T, ctx context.Context, service *Service, admin authorization.Principal, boardID uuid.UUID) {
	t.Helper()
	if _, err := service.AdminBoardAction(ctx, admin, boardID, BoardActionPublish); err != nil {
		t.Fatalf("publish board: %v", err)
	}
}

func findModuleProgress(t *testing.T, page Page[Module], moduleID uuid.UUID) *TaskProgress {
	t.Helper()
	for _, module := range page.Items {
		if module.ID == moduleID {
			return module.TaskProgress
		}
	}
	t.Fatalf("module %s missing from list page", moduleID)
	return nil
}

func wantProgress(t *testing.T, got *TaskProgress, solved, total int) {
	t.Helper()
	if got == nil {
		t.Fatalf("task progress is nil, want %d/%d", solved, total)
	}
	if got.SolvedTasks != solved || got.TotalTasks != total {
		t.Fatalf("task progress = %d/%d, want %d/%d", got.SolvedTasks, got.TotalTasks, solved, total)
	}
}

// TestStudentTaskProgressIndividualAvailabilityAndFilters covers the approved
// per-subject aggregate and the server-side completion filter: zero-point
// acceptance counts, hidden/disabled/upcoming/native modules never contribute,
// filtered totals apply before LIMIT/OFFSET, and the module fraction stays
// unfiltered.
func TestStudentTaskProgressIndividualAvailabilityAndFilters(t *testing.T) {
	ctx, db, service, _, cleanup := newProgressTestContext(t)
	defer cleanup()

	admin, adminID := newProgressAdmin(t, ctx, db)
	player, playerID := newProgressUser(t, ctx, db, "progress_player")
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)

	board := progressBoard(t, ctx, service, admin, CreateBoardInput{
		Slug: "progress-individual", Name: "Progress Individual", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	// Persisted legacy mixed-format module: existing data may carry FLAG and
	// MANUAL tasks in one TASKS module even though new modules are single-format.
	module, err := NewPostgresStore(db).CreateModule(ctx, CreateModuleParams{
		BoardID: board.ID, Name: "Main", Type: ModuleTypeTasks,
		AllowedTaskTypes: []string{TaskTypeFlag, TaskTypeManual}, Enabled: true, CreatedBy: adminID,
	}, AuditEventParams{ActorUserID: adminID, Action: "test.module.seed", ResourceType: "module"})
	if err != nil {
		t.Fatalf("create mixed module: %v", err)
	}
	emptyModule := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Empty", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	disabledModule := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Disabled", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: false,
	})
	upcomingStart := time.Now().UTC().Add(2 * time.Hour)
	upcomingEnd := time.Now().UTC().Add(3 * time.Hour)
	upcomingModule := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Upcoming", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
		StartsAt: &upcomingStart, EndsAt: &upcomingEnd,
	})
	pentestModule := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Pentest", Type: ModuleTypePentest, AllowedTaskTypes: []string{TaskTypePentestReport}, Enabled: true,
	})

	zeroTask := progressTask(t, ctx, service, admin, board.ID, module.ID, flagTaskInput("zero", 0, true))
	normalTask := progressTask(t, ctx, service, admin, board.ID, module.ID, flagTaskInput("normal", 50, true))
	progressTask(t, ctx, service, admin, board.ID, module.ID, flagTaskInput("hidden", 10, false))
	manualTask := progressTask(t, ctx, service, admin, board.ID, module.ID, CreateTaskInput{
		Title: "manual", Type: TaskTypeManual, Category: "web", Points: 20, Visible: true,
		Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
		Instance:   TaskInstanceInput{Enabled: false},
	})
	progressTask(t, ctx, service, admin, board.ID, disabledModule.ID, flagTaskInput("disabled", 10, true))
	progressTask(t, ctx, service, admin, board.ID, upcomingModule.ID, flagTaskInput("upcoming", 10, true))

	publishProgressBoard(t, ctx, service, admin, board.ID)
	mustJoinBoard(t, db, service, ctx, board.ID, playerID)

	// Wrong attempt first, then the accepted answer: multiple attempts must not
	// inflate the solved count.
	if _, err := service.SubmitTask(ctx, player,
		TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: normalTask.ID},
		"progress-wrong-key-01", SubmitInput{Text: "flag{nope}"}); err != nil {
		t.Fatalf("wrong flag: %v", err)
	}
	if _, err := service.SubmitTask(ctx, player,
		TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: normalTask.ID},
		"progress-right-key-01", SubmitInput{Text: "flag{normal}"}); err != nil {
		t.Fatalf("normal flag: %v", err)
	}
	acceptedZero, err := service.SubmitTask(ctx, player,
		TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: zeroTask.ID},
		"progress-zero-key-01", SubmitInput{Text: "flag{zero}"})
	if err != nil {
		t.Fatalf("zero-point flag: %v", err)
	}
	if acceptedZero.Status != SubmissionStatusAccepted || acceptedZero.ScoreAwarded == nil || *acceptedZero.ScoreAwarded != 0 {
		t.Fatalf("zero-point acceptance = %+v, want ACCEPTED/0", acceptedZero)
	}
	pending, err := service.SubmitTask(ctx, player,
		TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: manualTask.ID},
		"progress-manual-key-1", SubmitInput{Text: "manual answer"})
	if err != nil {
		t.Fatalf("manual submission: %v", err)
	}
	if pending.Status != SubmissionStatusPendingRev {
		t.Fatalf("manual status = %s, want PENDING_REVIEW", pending.Status)
	}

	// Board summary: 2 solved / 3 visible tasks. Hidden, disabled, upcoming and
	// PENTEST content never reaches the denominator; the empty module is 0/0.
	me, err := service.GetBoardMe(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("board me: %v", err)
	}
	wantProgress(t, me.TaskProgress, 2, 3)

	modules, err := service.ListModules(ctx, player, board.ID, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("list modules: %v", err)
	}
	wantProgress(t, findModuleProgress(t, modules, module.ID), 2, 3)
	wantProgress(t, findModuleProgress(t, modules, emptyModule.ID), 0, 0)
	for _, omitted := range []uuid.UUID{disabledModule.ID, upcomingModule.ID, pentestModule.ID} {
		for _, item := range modules.Items {
			if item.ID == omitted && item.TaskProgress != nil {
				t.Fatalf("unavailable module %s exposed progress %d/%d", omitted, item.TaskProgress.SolvedTasks, item.TaskProgress.TotalTasks)
			}
		}
	}

	detail, err := service.GetModule(ctx, player, board.ID, module.ID)
	if err != nil {
		t.Fatalf("get module: %v", err)
	}
	wantProgress(t, detail.TaskProgress, 2, 3)
	emptyDetail, err := service.GetModule(ctx, player, board.ID, emptyModule.ID)
	if err != nil {
		t.Fatalf("get empty module: %v", err)
	}
	wantProgress(t, emptyDetail.TaskProgress, 0, 0)

	// Filtering happens before counting and pagination; the module fraction is
	// always unfiltered.
	all, err := service.ListTasks(ctx, player, board.ID, module.ID, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("list all tasks: %v", err)
	}
	if all.Total != 3 {
		t.Fatalf("unfiltered total = %d, want 3", all.Total)
	}
	solved, err := service.ListTasks(ctx, player, board.ID, module.ID, ListQuery{Limit: 50, Completion: TaskCompletionSolved})
	if err != nil {
		t.Fatalf("list solved tasks: %v", err)
	}
	if solved.Total != 2 || len(solved.Items) != 2 {
		t.Fatalf("solved total=%d len=%d, want 2/2", solved.Total, len(solved.Items))
	}
	for _, task := range solved.Items {
		if !task.Solved {
			t.Fatalf("solved filter returned unsolved task %s", task.ID)
		}
	}
	unsolved, err := service.ListTasks(ctx, player, board.ID, module.ID, ListQuery{Limit: 50, Completion: TaskCompletionUnsolved})
	if err != nil {
		t.Fatalf("list unsolved tasks: %v", err)
	}
	if unsolved.Total != 1 || len(unsolved.Items) != 1 || unsolved.Items[0].ID != manualTask.ID {
		t.Fatalf("unsolved total=%d items=%+v, want only the pending manual task", unsolved.Total, unsolved.Items)
	}
	paged, err := service.ListTasks(ctx, player, board.ID, module.ID, ListQuery{Limit: 1, Offset: 0, Completion: TaskCompletionSolved})
	if err != nil {
		t.Fatalf("paged solved tasks: %v", err)
	}
	if paged.Total != 2 || len(paged.Items) != 1 {
		t.Fatalf("paged solved total=%d len=%d, want 2/1", paged.Total, len(paged.Items))
	}
	if _, err := service.ListTasks(ctx, player, board.ID, module.ID, ListQuery{Limit: 50, Completion: "BOGUS"}); !IsValidation(err) {
		t.Fatalf("invalid completion error = %v, want validation", err)
	}

	me, err = service.GetBoardMe(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("board me after filter: %v", err)
	}
	wantProgress(t, me.TaskProgress, 2, 3)

	// Manual pending -> accepted is a real state transition: the accepted review
	// adds exactly one solved task, and re-submitting an accepted task is still
	// rejected without changing the count.
	reviewer, reviewerID := newProgressUser(t, ctx, db, "progress_reviewer")
	grantBinding(t, ctx, db, board.ID, &module.ID, reviewerID, adminID, "reviewer", nil)
	if _, err := service.AdminReviewSubmission(ctx, reviewer, board.ID, pending.ID,
		ReviewInput{Decision: ReviewDecisionAccept}, "progress-review-key-1"); err != nil {
		t.Fatalf("accept manual submission: %v", err)
	}
	me, err = service.GetBoardMe(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("board me after review: %v", err)
	}
	wantProgress(t, me.TaskProgress, 3, 3)
	detail, err = service.GetModule(ctx, player, board.ID, module.ID)
	if err != nil {
		t.Fatalf("get module after review: %v", err)
	}
	wantProgress(t, detail.TaskProgress, 3, 3)
	if _, err := service.SubmitTask(ctx, player,
		TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: normalTask.ID},
		"progress-repeat-key-1", SubmitInput{Text: "flag{normal}"}); !errors.Is(err, ErrTaskAlreadySolved) {
		t.Fatalf("repeat accepted submit error = %v, want ErrTaskAlreadySolved", err)
	}
	me, err = service.GetBoardMe(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("board me after duplicate attempt: %v", err)
	}
	wantProgress(t, me.TaskProgress, 3, 3)
}

// TestStudentTaskProgressTeamSubjectAndTeamChange proves the aggregate follows
// the current TEAM subject, never the submitting user, and that a team change
// starts from the new team's own accepted tasks.
func TestStudentTaskProgressTeamSubjectAndTeamChange(t *testing.T) {
	ctx, db, service, _, cleanup := newProgressTestContext(t)
	defer cleanup()

	admin, _ := newProgressAdmin(t, ctx, db)
	first, firstID := newProgressUser(t, ctx, db, "progress_team_first")
	second, secondID := newProgressUser(t, ctx, db, "progress_team_second")
	third, thirdID := newProgressUser(t, ctx, db, "progress_team_third")
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)

	board := progressBoard(t, ctx, service, admin, CreateBoardInput{
		Slug: "progress-team", Name: "Progress Team", ParticipationMode: ParticipationTeam,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPublic, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
		TeamSettings: &TeamSettings{MinMembers: 1, MaxMembers: 2, AllowSelfCreate: true, AllowLeaveAfterStart: true},
	})
	module := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Team tasks", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	firstTask := progressTask(t, ctx, service, admin, board.ID, module.ID, flagTaskInput("team-one", 100, true))
	secondTask := progressTask(t, ctx, service, admin, board.ID, module.ID, flagTaskInput("team-two", 100, true))

	publishProgressBoard(t, ctx, service, admin, board.ID)
	for _, userID := range []uuid.UUID{firstID, secondID, thirdID} {
		mustJoinBoard(t, db, service, ctx, board.ID, userID)
	}

	team, err := service.CreateTeam(ctx, first, board.ID, "Red Team")
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	invite, err := service.RotateTeamInviteKey(ctx, first, board.ID)
	if err != nil {
		t.Fatalf("rotate team invite: %v", err)
	}
	if _, err := service.JoinTeam(ctx, second, board.ID, invite.InviteKey); err != nil {
		t.Fatalf("second member join: %v", err)
	}

	// One member's wrong attempt and the other member's accepted answer are the
	// same team subject; two distinct tasks are solved by two different users.
	if _, err := service.SubmitTask(ctx, first,
		TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: firstTask.ID},
		"progress-team-wrong-1", SubmitInput{Text: "flag{nope}"}); err != nil {
		t.Fatalf("team wrong flag: %v", err)
	}
	if _, err := service.SubmitTask(ctx, first,
		TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: firstTask.ID},
		"progress-team-right-1", SubmitInput{Text: "flag{team-one}"}); err != nil {
		t.Fatalf("team first accepted: %v", err)
	}
	if _, err := service.SubmitTask(ctx, second,
		TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: secondTask.ID},
		"progress-team-right-2", SubmitInput{Text: "flag{team-two}"}); err != nil {
		t.Fatalf("team second accepted: %v", err)
	}

	for _, member := range []authorization.Principal{first, second} {
		me, err := service.GetBoardMe(ctx, member, board.ID)
		if err != nil {
			t.Fatalf("team board me: %v", err)
		}
		if me.Team == nil || me.Team.ID != team.ID {
			t.Fatalf("team board me team = %+v, want %s", me.Team, team.ID)
		}
		wantProgress(t, me.TaskProgress, 2, 2)
	}
	modules, err := service.ListModules(ctx, second, board.ID, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("team list modules: %v", err)
	}
	wantProgress(t, findModuleProgress(t, modules, module.ID), 2, 2)

	// A participant on the same board without a team has no own subject.
	noTeam, err := service.GetBoardMe(ctx, third, board.ID)
	if err != nil {
		t.Fatalf("no-team board me: %v", err)
	}
	if noTeam.TaskProgress != nil {
		t.Fatalf("no-team board me exposed progress %+v", noTeam.TaskProgress)
	}
	if _, err := service.ListModules(ctx, third, board.ID, ListQuery{Limit: 50}); !errors.Is(err, ErrTeamRequired) {
		t.Fatalf("no-team list modules error = %v, want ErrTeamRequired", err)
	}

	// Team change: the second member leaves and creates a fresh team. The old
	// team keeps its accepted tasks; the new team starts at zero.
	if err := service.LeaveTeam(ctx, second, board.ID); err != nil {
		t.Fatalf("leave team: %v", err)
	}
	newTeam, err := service.CreateTeam(ctx, second, board.ID, "Blue Team")
	if err != nil {
		t.Fatalf("create new team: %v", err)
	}
	me, err := service.GetBoardMe(ctx, second, board.ID)
	if err != nil {
		t.Fatalf("new team board me: %v", err)
	}
	if me.Team == nil || me.Team.ID != newTeam.ID {
		t.Fatalf("new team board me team = %+v, want %s", me.Team, newTeam.ID)
	}
	wantProgress(t, me.TaskProgress, 0, 2)
	me, err = service.GetBoardMe(ctx, first, board.ID)
	if err != nil {
		t.Fatalf("old team board me: %v", err)
	}
	wantProgress(t, me.TaskProgress, 2, 2)

	// Staff management must not be able to grow a team's own progress: the
	// admin is not a participant and has no own projection.
	adminMe, err := service.GetBoardMe(ctx, admin, board.ID)
	if err != nil {
		t.Fatalf("admin board me: %v", err)
	}
	if adminMe.TaskProgress != nil || adminMe.Joined {
		t.Fatalf("admin board me joined=%v progress=%+v, want unjoined without progress", adminMe.Joined, adminMe.TaskProgress)
	}
}

// TestStudentTaskProgressRevocationAndStaffScope proves the aggregate reuses the
// live participant/audience/access-group policy: revocation is immediate, a
// joined staff participant only sees their own audiences, and preview reads
// never publish own progress.
func TestStudentTaskProgressRevocationAndStaffScope(t *testing.T) {
	ctx, db, service, accessSvc, cleanup := newProgressTestContext(t)
	defer cleanup()

	admin, adminID := newProgressAdmin(t, ctx, db)
	player, playerID := newProgressUser(t, ctx, db, "progress_access_player")
	staff, staffID := newProgressUser(t, ctx, db, "progress_access_staff")
	editor, editorID := newProgressUser(t, ctx, db, "progress_access_editor")
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)

	group, err := accessSvc.CreateAccessGroup(ctx, admin, "progress-access-group")
	if err != nil {
		t.Fatalf("create access group: %v", err)
	}
	board := progressBoard(t, ctx, service, admin, CreateBoardInput{
		Slug: "progress-access", Name: "Progress Access", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, AccessMode: AccessModeAudience,
		AccessGroupUUIDs: []uuid.UUID{group.ID}, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if _, err := db.Pool.Exec(ctx, `INSERT INTO access_group_members (group_id, user_id) VALUES ($1,$2),($1,$3)`,
		group.ID, playerID, staffID); err != nil {
		t.Fatalf("add group members: %v", err)
	}
	audience, err := accessSvc.CreateAudience(ctx, admin, board.ID, access.AudienceInput{Name: "Finalists"})
	if err != nil {
		t.Fatalf("create audience: %v", err)
	}
	restrictedAudience, err := accessSvc.CreateAudience(ctx, admin, board.ID, access.AudienceInput{Name: "Restricted"})
	if err != nil {
		t.Fatalf("create restricted audience: %v", err)
	}

	openModule := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Open", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	gatedModule := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Gated", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
		AudienceUUIDs: []uuid.UUID{audience.ID},
	})
	managementModule := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Management", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
		AudienceUUIDs: []uuid.UUID{restrictedAudience.ID},
	})
	openTask := progressTask(t, ctx, service, admin, board.ID, openModule.ID, flagTaskInput("open", 10, true))
	progressTask(t, ctx, service, admin, board.ID, openModule.ID, flagTaskInput("open-hidden", 10, false))
	gatedTask := progressTask(t, ctx, service, admin, board.ID, gatedModule.ID, flagTaskInput("gated", 10, true))
	progressTask(t, ctx, service, admin, board.ID, managementModule.ID, flagTaskInput("management", 10, true))

	publishProgressBoard(t, ctx, service, admin, board.ID)
	mustJoinBoard(t, db, service, ctx, board.ID, playerID)
	mustJoinBoard(t, db, service, ctx, board.ID, staffID)
	if _, err := accessSvc.AddAudienceMember(ctx, admin, board.ID, audience.ID,
		access.MemberInput{SubjectType: SubjectTypeUser, SubjectID: playerID}); err != nil {
		t.Fatalf("add audience member: %v", err)
	}

	for _, submission := range []struct {
		moduleID uuid.UUID
		taskID   uuid.UUID
		key      string
		text     string
	}{
		{openModule.ID, openTask.ID, "progress-access-open-1", "flag{open}"},
		{gatedModule.ID, gatedTask.ID, "progress-access-gated-1", "flag{gated}"},
	} {
		if _, err := service.SubmitTask(ctx, player,
			TaskPath{BoardID: board.ID, ModuleID: submission.moduleID, TaskID: submission.taskID},
			submission.key, SubmitInput{Text: submission.text}); err != nil {
			t.Fatalf("player submit %s: %v", submission.text, err)
		}
	}
	// The joined staff participant may only solve the audience-free module.
	if _, err := service.SubmitTask(ctx, staff,
		TaskPath{BoardID: board.ID, ModuleID: openModule.ID, TaskID: openTask.ID},
		"progress-access-staff-1", SubmitInput{Text: "flag{open}"}); err != nil {
		t.Fatalf("staff submit: %v", err)
	}

	me, err := service.GetBoardMe(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("player board me: %v", err)
	}
	wantProgress(t, me.TaskProgress, 2, 2)

	// Audience removal revokes the gated module immediately: the board total
	// drops to the open module and the gated module is no longer readable.
	if err := accessSvc.RemoveAudienceMember(ctx, admin, board.ID, audience.ID,
		access.Subject{Type: SubjectTypeUser, ID: playerID}); err != nil {
		t.Fatalf("remove audience member: %v", err)
	}
	me, err = service.GetBoardMe(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("player board me after audience revocation: %v", err)
	}
	wantProgress(t, me.TaskProgress, 1, 1)
	modules, err := service.ListModules(ctx, player, board.ID, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("player modules after audience revocation: %v", err)
	}
	wantProgress(t, findModuleProgress(t, modules, openModule.ID), 1, 1)
	for _, module := range modules.Items {
		if module.ID == gatedModule.ID {
			t.Fatalf("revoked audience module still listed: %+v", module)
		}
	}
	if _, err := service.GetModule(ctx, player, board.ID, gatedModule.ID); !errors.Is(err, ErrModuleNotAvailable) {
		t.Fatalf("revoked audience module read = %v, want ErrModuleNotAvailable", err)
	}

	// Joined staff with a management role: the board summary is built only from
	// their own participant audiences, so the restricted sibling module and its
	// hidden tasks never contribute. Per-module progress follows the same gate.
	grantBinding(t, ctx, db, board.ID, nil, staffID, adminID, "board_editor", nil)
	staffMe, err := service.GetBoardMe(ctx, staff, board.ID)
	if err != nil {
		t.Fatalf("staff board me: %v", err)
	}
	wantProgress(t, staffMe.TaskProgress, 1, 1)
	staffModules, err := service.ListModules(ctx, staff, board.ID, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("staff list modules: %v", err)
	}
	wantProgress(t, findModuleProgress(t, staffModules, openModule.ID), 1, 1)
	if got := findModuleProgress(t, staffModules, managementModule.ID); got != nil {
		t.Fatalf("management sibling exposed own progress %d/%d", got.SolvedTasks, got.TotalTasks)
	}
	management, err := service.GetModule(ctx, staff, board.ID, managementModule.ID)
	if err != nil {
		t.Fatalf("staff management module preview: %v", err)
	}
	if management.Preview != true || management.TaskProgress != nil {
		t.Fatalf("management preview = preview:%v progress:%+v, want preview without progress",
			management.Preview, management.TaskProgress)
	}

	// A non-participant editor keeps a staff preview with no own progress.
	grantBinding(t, ctx, db, board.ID, nil, editorID, adminID, "board_editor", nil)
	editorMe, err := service.GetBoardMe(ctx, editor, board.ID)
	if err != nil {
		t.Fatalf("editor board me: %v", err)
	}
	if editorMe.Joined || editorMe.TaskProgress != nil {
		t.Fatalf("editor board me joined=%v progress=%+v, want preview without progress", editorMe.Joined, editorMe.TaskProgress)
	}
	editorModules, err := service.ListModules(ctx, editor, board.ID, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("editor list modules: %v", err)
	}
	for _, module := range editorModules.Items {
		if module.TaskProgress != nil {
			t.Fatalf("editor preview module %s exposed progress %+v", module.ID, module.TaskProgress)
		}
	}

	// Access-group revocation keeps the participant row visible but removes all
	// own progress (existing preview semantics, never a fabricated zero).
	if _, err := db.Pool.Exec(ctx, `DELETE FROM access_group_members WHERE group_id=$1 AND user_id=$2`,
		group.ID, playerID); err != nil {
		t.Fatalf("revoke group membership: %v", err)
	}
	revoked, err := service.GetBoardMe(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("revoked board me: %v", err)
	}
	if !revoked.Joined || !revoked.Preview || revoked.TaskProgress != nil || revoked.Score != nil {
		t.Fatalf("revoked board me = joined:%v preview:%v progress:%+v score:%v",
			revoked.Joined, revoked.Preview, revoked.TaskProgress, revoked.Score)
	}
}

// TestStudentTaskProgressLargeBoardNoPageCap seeds more than 100 modules and
// more than 100 tasks per module through SQL fixtures and proves the board
// summary covers every permitted module, while the module list still pages.
func TestStudentTaskProgressLargeBoardNoPageCap(t *testing.T) {
	ctx, db, service, _, cleanup := newProgressTestContext(t)
	defer cleanup()

	admin, adminID := newProgressAdmin(t, ctx, db)
	player, playerID := newProgressUser(t, ctx, db, "progress_large_player")
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)

	board := progressBoard(t, ctx, service, admin, CreateBoardInput{
		Slug: "progress-large", Name: "Progress Large", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	// Publishing needs one valid task module, so seed it through the service and
	// then bulk-insert the >100 fixtures through SQL (no 100 Argon2 hash cost).
	seedModule := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Seed", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	progressTask(t, ctx, service, admin, board.ID, seedModule.ID, flagTaskInput("seed", 10, true))
	publishProgressBoard(t, ctx, service, admin, board.ID)
	mustJoinBoard(t, db, service, ctx, board.ID, playerID)

	// 101 modules, two of which carry 101 visible tasks each.
	firstModule := uuid.New()
	secondModule := uuid.New()
	for index := 0; index < 101; index++ {
		moduleID := uuid.New()
		switch index {
		case 0:
			moduleID = firstModule
		case 1:
			moduleID = secondModule
		}
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO modules (id, board_id, name, description, type, allowed_task_types,
				audience_uuids, enabled, sort_order, created_by)
			VALUES ($1,$2,$3,'', 'TASKS', ARRAY['FLAG'], '{}', TRUE, $4, $5)`,
			moduleID, board.ID, "Bulk "+uuid.NewString()[:8], index, adminID); err != nil {
			t.Fatalf("seed module %d: %v", index, err)
		}
	}
	var firstTask uuid.UUID
	for index := 0; index < 202; index++ {
		taskID := uuid.New()
		moduleID := firstModule
		if index >= 101 {
			moduleID = secondModule
		}
		if index == 0 {
			firstTask = taskID
		}
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO tasks (id, board_id, module_id, title, category, description, type,
				points, visible, submission_mode, submission_checker, instance_enabled, created_by)
			VALUES ($1,$2,$3,$4,'web','','FLAG',10,TRUE,'TEXT','AUTOMATIC',FALSE,$5)`,
			taskID, board.ID, moduleID, "Bulk task "+uuid.NewString()[:8], adminID); err != nil {
			t.Fatalf("seed task %d: %v", index, err)
		}
	}
	// One real accepted row for the first task (fixture only; the verdict path
	// itself is covered by the individual/team tests above).
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO submissions (board_id, module_id, task_id, user_id, status, score_awarded,
			idempotency_key, request_hash, subject_type, subject_id, submitted_by_user_id)
		VALUES ($1,$2,$3,$4,'ACCEPTED',10,'progress-large-seed-1',decode(repeat('ab',32),'hex'),
			'USER',$4,$4)`,
		board.ID, firstModule, firstTask, playerID); err != nil {
		t.Fatalf("seed accepted submission: %v", err)
	}

	me, err := service.GetBoardMe(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("large board me: %v", err)
	}
	wantProgress(t, me.TaskProgress, 1, 203)

	firstPage, err := service.ListModules(ctx, player, board.ID, ListQuery{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("large module page 1: %v", err)
	}
	if firstPage.Total != 102 || len(firstPage.Items) != 100 {
		t.Fatalf("module page 1 total=%d len=%d, want 102/100", firstPage.Total, len(firstPage.Items))
	}
	wantProgress(t, findModuleProgress(t, firstPage, firstModule), 1, 101)
	wantProgress(t, findModuleProgress(t, firstPage, secondModule), 0, 101)
	secondPage, err := service.ListModules(ctx, player, board.ID, ListQuery{Limit: 100, Offset: 100})
	if err != nil {
		t.Fatalf("large module page 2: %v", err)
	}
	if secondPage.Total != 102 || len(secondPage.Items) != 2 {
		t.Fatalf("module page 2 total=%d len=%d, want 102/2", secondPage.Total, len(secondPage.Items))
	}
}

// TestStudentTaskProgressCrossBoardAndUnjoined covers parent-ID substitution and
// unjoined/absent-subject reads. Cross-board module IDs must never aggregate.
func TestStudentTaskProgressCrossBoardAndUnjoined(t *testing.T) {
	ctx, db, service, _, cleanup := newProgressTestContext(t)
	defer cleanup()

	admin, _ := newProgressAdmin(t, ctx, db)
	_, playerID := newProgressUser(t, ctx, db, "progress_cross_player")
	outsider, _ := newProgressUser(t, ctx, db, "progress_cross_outsider")
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)

	boardA := progressBoard(t, ctx, service, admin, CreateBoardInput{
		Slug: "progress-cross-a", Name: "Progress Cross A", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	boardB := progressBoard(t, ctx, service, admin, CreateBoardInput{
		Slug: "progress-cross-b", Name: "Progress Cross B", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	moduleA := progressModule(t, ctx, service, admin, boardA.ID, CreateModuleInput{
		Name: "A", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	moduleB := progressModule(t, ctx, service, admin, boardB.ID, CreateModuleInput{
		Name: "B", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	progressTask(t, ctx, service, admin, boardA.ID, moduleA.ID, flagTaskInput("cross-a", 10, true))
	progressTask(t, ctx, service, admin, boardB.ID, moduleB.ID, flagTaskInput("cross-b", 10, true))
	publishProgressBoard(t, ctx, service, admin, boardA.ID)
	publishProgressBoard(t, ctx, service, admin, boardB.ID)
	mustJoinBoard(t, db, service, ctx, boardA.ID, playerID)

	// Unjoined on a private board has no own projection at all.
	if _, err := service.GetBoardMe(ctx, outsider, boardA.ID); !errors.Is(err, ErrBoardAccessDenied) {
		t.Fatalf("unjoined board me error = %v, want ErrBoardAccessDenied", err)
	}
	if _, err := service.ListModules(ctx, outsider, boardA.ID, ListQuery{Limit: 50}); !errors.Is(err, ErrBoardAccessDenied) {
		t.Fatalf("unjoined list modules error = %v, want ErrBoardAccessDenied", err)
	}

	// The store aggregate is constrained to the requested board even when a
	// caller passes a valid module UUID from another board.
	store := NewPostgresStore(db)
	progress, err := store.TaskProgressByModule(ctx, boardA.ID, []uuid.UUID{moduleA.ID, moduleB.ID},
		ScoreSubject{Type: SubjectTypeUser, ID: playerID})
	if err != nil {
		t.Fatalf("cross-board aggregate: %v", err)
	}
	if item, ok := progress[moduleB.ID]; ok {
		t.Fatalf("cross-board module leaked progress %+v", item)
	}
	if item := progress[moduleA.ID]; item.TotalTasks != 1 || item.SolvedTasks != 0 {
		t.Fatalf("board A module progress = %+v, want 0/1", item)
	}
	if _, err := store.TaskProgressByModule(ctx, boardA.ID, []uuid.UUID{moduleA.ID},
		ScoreSubject{Type: SubjectTypeUser, ID: uuid.Nil}); err == nil {
		t.Fatal("aggregate accepted a nil subject ID")
	}
	empty, err := store.TaskProgressByModule(ctx, boardA.ID, nil,
		ScoreSubject{Type: SubjectTypeUser, ID: playerID})
	if err != nil {
		t.Fatalf("empty aggregate: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty module set returned %d entries, want none", len(empty))
	}
}
