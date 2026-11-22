package boards

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
)

func TestTeamMyScoreboardMatchesCanonicalPlaceRealPostgres(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("ACTFB_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run team scoreboard test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()
	service := NewService(NewPostgresStore(db), authorization.NewAuthorizer(db), SystemClock{},
		ratelimit.NewMemoryLimiter(1024), WithIdempotencySecret("team-my-score-secret"))
	adminID := createBoardUser(t, ctx, db, "team_my_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "team_my_player", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "team-my-score", Name: "Team My Score", ParticipationMode: ParticipationTeam,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
		TeamSettings: &TeamSettings{MinMembers: 1, MaxMembers: 4, AllowSelfCreate: true, AllowLeaveAfterStart: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Tasks", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "Team flag", Type: TaskTypeFlag, Category: "web", Description: "team",
		Points: 100, Visible: true,
		Submission:      SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic},
		AcceptedAnswers: []string{"flag{team}"}, Instance: TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdminBoardAction(ctx, admin, board.ID, BoardActionPublish); err != nil {
		t.Fatal(err)
	}
	if _, err := service.JoinBoard(ctx, player, board.ID); err != nil {
		t.Fatal(err)
	}
	team, err := service.CreateTeam(ctx, player, board.ID, "Blue Team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO submissions
			(board_id, module_id, task_id, user_id, subject_type, subject_id, submitted_by_user_id,
			 status, score_awarded, message, idempotency_key, request_hash, created_at)
		VALUES ($1,$2,$3,$4,'TEAM',$5,$4,'ACCEPTED',100,'Accepted','team-my-score-key-1',decode('aa','hex'),NOW())
	`, board.ID, module.ID, task.ID, playerID, team.ID); err != nil {
		t.Fatal(err)
	}
	canonical, err := service.GetScoreboard(ctx, player, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical.Items) != 1 || canonical.Items[0].Subject.Type != SubjectTypeTeam || canonical.Items[0].Score != 100 {
		t.Fatalf("canonical team scoreboard = %+v", canonical.Items)
	}
	entry, err := service.GetMyScoreboardEntry(ctx, player, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Subject.Type != SubjectTypeTeam || entry.Subject.ID != team.ID || entry.Score != 100 {
		t.Fatalf("my team score = %+v, want team %s score 100", entry, team.ID)
	}
	if entry.Place != canonical.Items[0].Place {
		t.Fatalf("my place = %d, canonical place = %d", entry.Place, canonical.Items[0].Place)
	}
}

func TestSubmissionPolicyAndInstanceTaskRealPostgres(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("ACTFB_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run submission policy tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	service := NewService(NewPostgresStore(db), authorization.NewAuthorizer(db), SystemClock{},
		ratelimit.NewMemoryLimiter(1024), WithIdempotencySecret("submission-policy-secret"))
	adminID := createBoardUser(t, ctx, db, "policy_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "policy_player", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "submission-policy", Name: "Submission Policy", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatal(err)
	}
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Policy", Type: ModuleTypeTasks,
		AllowedTaskTypes: []string{TaskTypeManual}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	instanceModule, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Instance policy", Type: ModuleTypeTasks,
		AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	manualTask, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "Bounded text", Type: TaskTypeManual, Category: "web", Description: "bounded",
		Points: 10, Visible: true,
		Submission: SubmissionPolicyInput{
			Mode: SubmissionModeText, Checker: SubmissionCheckerManual,
			HasTextPolicy: true, TextRequired: true, TextMinLength: 5, TextMaxLength: 10,
		},
		Instance: TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	orTask, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "Text or files", Type: TaskTypeManual, Category: "web", Description: "or",
		Points: 10, Visible: true,
		Submission: SubmissionPolicyInput{
			Mode: SubmissionModeTextOrFiles, Checker: SubmissionCheckerManual,
			HasTextPolicy: true, TextRequired: false, TextMinLength: 1, TextMaxLength: 20,
		},
		Instance: TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	instanceTaskID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO tasks
			(id, board_id, module_id, title, category, description, type, points, visible,
			 submission_mode, submission_checker, instance_enabled, created_by, submission_text_policy)
		VALUES ($1,$2,$3,'Instance report','web','instance','INSTANCE',20,TRUE,
		        'TEXT','MANUAL',TRUE,$4,'{"required":true,"minLength":3,"maxLength":20}'::jsonb)
	`, instanceTaskID, board.ID, instanceModule.ID, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdminBoardAction(ctx, admin, board.ID, BoardActionPublish); err != nil {
		t.Fatal(err)
	}
	if _, err := service.JoinBoard(ctx, player, board.ID); err != nil {
		t.Fatal(err)
	}

	submit := func(moduleID, taskID uuid.UUID, text, key string) (*Submission, error) {
		t.Helper()
		return service.SubmitTask(ctx, player, TaskPath{BoardID: board.ID, ModuleID: moduleID, TaskID: taskID},
			key, SubmitInput{Text: text})
	}

	if _, err := submit(module.ID, manualTask.ID, "abc", "policy-short-00001"); err == nil {
		t.Fatal("short text must be rejected by the persisted minimum")
	}
	if _, err := submit(module.ID, manualTask.ID, "abcdefghijk", "policy-long-000001"); err == nil {
		t.Fatal("long text must be rejected by the persisted maximum")
	}
	accepted, err := submit(module.ID, manualTask.ID, "abcdef", "policy-valid-00001")
	if err != nil {
		t.Fatalf("valid bounded text: %v", err)
	}
	if accepted.Status != SubmissionStatusPendingRev {
		t.Fatalf("bounded text status = %s, want PENDING_REVIEW", accepted.Status)
	}
	if _, err := submit(module.ID, orTask.ID, "", "policy-empty-or-001"); err == nil {
		t.Fatal("TEXT_OR_FILES with neither text nor a draft must be rejected")
	}
	orSubmission, err := submit(module.ID, orTask.ID, "text only", "policy-text-or-001")
	if err != nil || orSubmission.Status != SubmissionStatusPendingRev {
		t.Fatalf("TEXT_OR_FILES with text: %v %+v", err, orSubmission)
	}

	instanceSubmission, err := submit(instanceModule.ID, instanceTaskID, "instance report", "policy-instance-01")
	if err != nil {
		t.Fatalf("INSTANCE submission must not be silently rejected: %v", err)
	}
	if instanceSubmission.Status != SubmissionStatusPendingRev || instanceSubmission.ScoreAwarded != nil {
		t.Fatalf("INSTANCE submission = %+v, want pending review with no score", instanceSubmission)
	}
	if instanceSubmission.SubmittedText == nil || *instanceSubmission.SubmittedText != "instance report" {
		t.Fatalf("INSTANCE durable text = %v", instanceSubmission.SubmittedText)
	}
	if _, err := submit(instanceModule.ID, instanceTaskID, "x", "policy-instance-02"); err == nil {
		t.Fatal("INSTANCE text below policy minimum must be rejected")
	}
	var rejectedRows int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM submissions WHERE status='REJECTED'`).Scan(&rejectedRows); err != nil {
		t.Fatal(err)
	}
	if rejectedRows != 0 {
		t.Fatalf("policy failures wrote %d silent REJECTED rows, want 0", rejectedRows)
	}
}
