package boards

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
)

func TestP1AuditAtomicFaultInjection(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run audit atomicity tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	authz := authorization.NewAuthorizer(db)
	adminID := createBoardUser(t, ctx, db, "audit_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "audit_player", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(128),
		WithArgon2Config(argon), WithIdempotencySecret("audit-atomic-secret"))

	installAuditFailure := func() {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `
			CREATE OR REPLACE FUNCTION fail_audit_insert() RETURNS trigger
			LANGUAGE plpgsql AS $$
			BEGIN
				RAISE EXCEPTION 'injected audit failure';
			END $$;
			CREATE TRIGGER fail_audit_insert
			BEFORE INSERT ON audit_events
			FOR EACH ROW EXECUTE FUNCTION fail_audit_insert();`); err != nil {
			t.Fatalf("install audit trigger: %v", err)
		}
	}
	removeAuditFailure := func() {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS fail_audit_insert ON audit_events`); err != nil {
			t.Fatalf("drop audit trigger: %v", err)
		}
	}

	// Board create must roll back if its audit insert fails.
	installAuditFailure()
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	if _, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "audit-fail-board", Name: "Audit Fail", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	}); err == nil {
		t.Fatal("board create succeeded despite audit failure")
	}
	var boardCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM boards WHERE slug='audit-fail-board'`).Scan(&boardCount); err != nil {
		t.Fatalf("count board: %v", err)
	}
	if boardCount != 0 {
		t.Fatalf("board committed without audit: count=%d", boardCount)
	}
	removeAuditFailure()

	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "audit-ok-board", Name: "Audit OK", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatalf("create board after drop trigger: %v", err)
	}
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Tasks", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create module: %v", err)
	}
	task, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "Task", Type: TaskTypeFlag, Category: "web", Points: 100, Visible: true,
		Submission:      SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic},
		AcceptedAnswers: []string{"flag{audit}"}, Instance: TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := service.AdminBoardAction(ctx, admin, board.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := service.JoinBoard(ctx, player, board.ID); err != nil {
		t.Fatalf("join: %v", err)
	}

	path := TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID}
	installAuditFailure()
	if _, err := service.SubmitTask(ctx, player, path, "audit-fail-submit-1", SubmitInput{Text: "flag{audit}"}); err == nil {
		t.Fatal("accepted submission committed despite audit failure")
	}
	var submissions, accepted int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status='ACCEPTED')
		FROM submissions WHERE user_id=$1 AND task_id=$2`, playerID, task.ID,
	).Scan(&submissions, &accepted); err != nil {
		t.Fatalf("count submissions after audit failure: %v", err)
	}
	if submissions != 0 || accepted != 0 {
		t.Fatalf("partial score committed: submissions=%d accepted=%d", submissions, accepted)
	}
	removeAuditFailure()

	first, err := service.SubmitTask(ctx, player, path, "audit-ok-submit-1", SubmitInput{Text: "flag{audit}"})
	if err != nil {
		t.Fatalf("accepted submission after audit restore: %v", err)
	}
	replay, err := service.SubmitTask(ctx, player, path, "audit-ok-submit-1", SubmitInput{Text: "flag{audit}"})
	if err != nil || replay.ID != first.ID {
		t.Fatalf("idempotent replay: %v %+v vs %+v", err, replay, first)
	}
	var auditCount int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action='submission.accept' AND resource_id=$1`, first.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count submission audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("submission.accept audit rows = %d, want exactly 1 after replay", auditCount)
	}
	if err := service.AdminDeleteBoard(ctx, admin, board.ID); !errors.Is(err, ErrBoardDeleteNotAllowed) {
		t.Fatalf("delete history board error = %v, want ErrBoardDeleteNotAllowed", err)
	}
	var phantomDeleteAudits int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action='board.delete' AND resource_id=$1`, board.ID,
	).Scan(&phantomDeleteAudits); err != nil {
		t.Fatalf("count failed-delete audits: %v", err)
	}
	if phantomDeleteAudits != 0 {
		t.Fatalf("failed history delete left %d phantom audit rows", phantomDeleteAudits)
	}

	deleteBoard, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "audit-delete-board", Name: "Audit Delete", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatalf("create delete board: %v", err)
	}
	if err := service.AdminDeleteBoard(ctx, admin, deleteBoard.ID); err != nil {
		t.Fatalf("delete board with audit: %v", err)
	}
	var deleteAuditCount, deleteAuditBoardNull int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE board_id IS NULL)
		FROM audit_events WHERE action='board.delete' AND resource_id=$1`, deleteBoard.ID,
	).Scan(&deleteAuditCount, &deleteAuditBoardNull); err != nil {
		t.Fatalf("read board.delete audit: %v", err)
	}
	if deleteAuditCount != 1 || deleteAuditBoardNull != 1 {
		t.Fatalf("board.delete audit rows=%d nullBoard=%d, want 1/1", deleteAuditCount, deleteAuditBoardNull)
	}
}

func TestP1SiblingSubmissionReadUniformNotFound(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the L2 regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	authz := authorization.NewAuthorizer(db)
	adminID := createBoardUser(t, ctx, db, "l2_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "l2_player", []string{"user"})
	reviewerID := createBoardUser(t, ctx, db, "l2_reviewer", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}
	reviewer := authorization.Principal{UserID: reviewerID, GlobalRoles: []string{"user"}}
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(128),
		WithArgon2Config(argon), WithIdempotencySecret("l2-secret"))

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "l2-board", Name: "L2 Board", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	moduleX, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Module X", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create module X: %v", err)
	}
	moduleY, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Module Y", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create module Y: %v", err)
	}
	taskY, err := service.AdminCreateTask(ctx, admin, board.ID, moduleY.ID, CreateTaskInput{
		Title: "Task Y", Type: TaskTypeFlag, Category: "web", Points: 10, Visible: true,
		Submission:      SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic},
		AcceptedAnswers: []string{"flag{y}"}, Instance: TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatalf("create task Y: %v", err)
	}
	if _, err := service.AdminBoardAction(ctx, admin, board.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := service.JoinBoard(ctx, player, board.ID); err != nil {
		t.Fatalf("join: %v", err)
	}
	submission, err := service.SubmitTask(ctx, player,
		TaskPath{BoardID: board.ID, ModuleID: moduleY.ID, TaskID: taskY.ID},
		"l2-submit-key-0001", SubmitInput{Text: "flag{y}"})
	if err != nil {
		t.Fatalf("submit sibling: %v", err)
	}
	grantBinding(t, ctx, db, board.ID, &moduleX.ID, reviewerID, adminID, "reviewer", nil)

	if _, err := service.AdminGetSubmission(ctx, reviewer, board.ID, submission.ID); !errors.Is(err, ErrSubmissionNotFound) {
		t.Fatalf("known sibling submission error = %v, want ErrSubmissionNotFound", err)
	}
	if _, err := service.AdminGetSubmission(ctx, reviewer, board.ID, uuid.New()); !errors.Is(err, ErrSubmissionNotFound) {
		t.Fatalf("unknown submission error = %v, want ErrSubmissionNotFound", err)
	}
	var unauthorizedReadAudits int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE actor_user_id=$1 AND action='submission.read'`, reviewerID,
	).Scan(&unauthorizedReadAudits); err != nil {
		t.Fatalf("count unauthorized read audits: %v", err)
	}
	if unauthorizedReadAudits != 0 {
		t.Fatalf("unauthorized sibling read wrote %d audit rows", unauthorizedReadAudits)
	}
}
