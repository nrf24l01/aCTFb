package boards

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
)

// TestP1LateSubmissionAuthoritativeRecheck covers P1-02: eligibility is checked
// before Argon2 hashing, then the world changes. The locked store transaction
// must re-evaluate persisted state with one authoritative DB clock before
// writing any verdict/score.
func TestP1LateSubmissionAuthoritativeRecheck(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the late-submission regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	authz := authorization.NewAuthorizer(db)
	adminID := createBoardUser(t, ctx, db, "late_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "late_player", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	baseService := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(128),
		WithAccessPolicy(access.NewService(db, authz)),
		WithArgon2Config(argon), WithIdempotencySecret("late-submission-secret"))

	setup := func(t *testing.T, slug string) (*Board, *Module, *Task) {
		t.Helper()
		board, err := baseService.AdminCreateBoard(ctx, admin, CreateBoardInput{
			Slug: slug, Name: slug, ParticipationMode: ParticipationIndividual,
			RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
			StartsAt: &start, EndsAt: &end,
		})
		if err != nil {
			t.Fatalf("create board: %v", err)
		}
		module, err := baseService.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
			Name: "Tasks", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
		})
		if err != nil {
			t.Fatalf("create module: %v", err)
		}
		task, err := baseService.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
			Title: "Task", Type: TaskTypeFlag, Category: "web", Description: "description",
			Points: 100, Visible: true,
			Submission:      SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic},
			AcceptedAnswers: []string{"flag{late}"}, Instance: TaskInstanceInput{Enabled: false},
		})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		if _, err := baseService.AdminBoardAction(ctx, admin, board.ID, BoardActionPublish); err != nil {
			t.Fatalf("publish: %v", err)
		}
		if _, err := baseService.JoinBoard(ctx, player, board.ID, testJoinCode(t, db, board.ID, "P1-REGRESS-CODE-01")); err != nil {
			t.Fatalf("join: %v", err)
		}
		return board, module, task
	}

	run := func(t *testing.T, name, mutate string, want error) {
		t.Helper()
		board, module, task := setup(t, name)
		store := NewPostgresStore(db)
		store.beforeSubmissionWriteCheck = func() {
			if _, err := db.Pool.Exec(ctx, mutate, board.ID, module.ID); err != nil {
				t.Errorf("transition %s: %v", name, err)
			}
		}
		service := NewService(store, authz, SystemClock{}, ratelimit.NewMemoryLimiter(128),
			WithAccessPolicy(access.NewService(db, authz)),
			WithArgon2Config(argon), WithIdempotencySecret("late-submission-secret"))
		_, err := service.SubmitTask(ctx, player,
			TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID},
			"late-"+name+"-000001", SubmitInput{Text: "flag{late}"})
		if !errors.Is(err, want) {
			var ends *time.Time
			_ = db.Pool.QueryRow(ctx, `SELECT ends_at FROM boards WHERE id=$1`, board.ID).Scan(&ends)
			t.Fatalf("%s error = %v, want %v (board ends=%v)", name, err, want, ends)
		}
		var submissions, accepted int
		if err := db.Pool.QueryRow(ctx, `
			SELECT count(*), count(*) FILTER (WHERE status='ACCEPTED')
			FROM submissions WHERE user_id=$1 AND task_id=$2`, playerID, task.ID,
		).Scan(&submissions, &accepted); err != nil {
			t.Fatalf("count submissions: %v", err)
		}
		if submissions != 0 || accepted != 0 {
			t.Fatalf("%s wrote submissions=%d accepted=%d, want 0/0", name, submissions, accepted)
		}
	}

	t.Run("deadline passed before write", func(t *testing.T) {
		run(t, "late-deadline-1",
			`UPDATE boards SET ends_at=clock_timestamp()-interval '1 second' WHERE id=$1 AND $2::uuid IS NOT NULL`, ErrBoardFinished)
	})
	t.Run("module disabled before write", func(t *testing.T) {
		run(t, "late-disabled-1",
			`UPDATE modules SET enabled=FALSE WHERE board_id=$1 AND id=$2`, ErrModuleNotAvailable)
	})
	t.Run("module upcoming before write", func(t *testing.T) {
		run(t, "late-upcoming-1",
			`UPDATE modules SET starts_at=clock_timestamp()+interval '1 hour' WHERE board_id=$1 AND id=$2`, ErrModuleNotAvailable)
	})
	t.Run("module window closed before write", func(t *testing.T) {
		run(t, "late-module-end-1",
			`UPDATE modules SET ends_at=clock_timestamp()-interval '1 second' WHERE board_id=$1 AND id=$2`, ErrModuleNotAvailable)
	})
}

func TestP1LateIdempotentReplayAfterDeadline(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the late replay regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	authz := authorization.NewAuthorizer(db)
	adminID := createBoardUser(t, ctx, db, "replay_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "replay_player", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(128),
		WithAccessPolicy(access.NewService(db, authz)),
		WithArgon2Config(argon), WithIdempotencySecret("late-replay-secret"))

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "late-replay", Name: "Late Replay", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
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
		AcceptedAnswers: []string{"flag{replay}"}, Instance: TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := service.AdminBoardAction(ctx, admin, board.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := service.JoinBoard(ctx, player, board.ID, testJoinCode(t, db, board.ID, "P1-REGRESS-CODE-01")); err != nil {
		t.Fatalf("join: %v", err)
	}
	path := TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID}
	first, err := service.SubmitTask(ctx, player, path, "replay-key-000001", SubmitInput{Text: "flag{replay}"})
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE boards SET ends_at=clock_timestamp()-interval '1 second' WHERE id=$1`, board.ID); err != nil {
		t.Fatalf("close board: %v", err)
	}
	replay, err := service.SubmitTask(ctx, player, path, "replay-key-000001", SubmitInput{Text: "flag{replay}"})
	if err != nil {
		t.Fatalf("late idempotent replay: %v", err)
	}
	if replay.ID != first.ID || replay.Status != first.Status {
		t.Fatalf("replay changed result: %+v vs %+v", replay, first)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM submissions WHERE user_id=$1 AND task_id=$2`, playerID, task.ID).Scan(&count); err != nil {
		t.Fatalf("count replay rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("submission rows after replay = %d, want 1", count)
	}
}

func TestP1MultiModuleStaffListAndQuery(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the multi-module list regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	authz := authorization.NewAuthorizer(db)
	adminID := createBoardUser(t, ctx, db, "multi_admin", []string{"admin"})
	staffID := createBoardUser(t, ctx, db, "multi_staff", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(128),
		WithAccessPolicy(access.NewService(db, authz)),
		WithArgon2Config(argon), WithIdempotencySecret("multi-module-secret"))
	store := NewPostgresStore(db)
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "multi-module", Name: "Multi Module", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	var modules []*Module
	for index, name := range []string{"Alpha", "Beta", "Sibling"} {
		module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
			Name: name, Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag},
			Enabled: true, SortOrder: index,
		})
		if err != nil {
			t.Fatalf("create module %s: %v", name, err)
		}
		modules = append(modules, module)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by)
		VALUES ($1,$2,$3,'board_editor',$4), ($1,$5,$3,'board_editor',$4)`,
		board.ID, modules[0].ID, staffID, adminID, modules[1].ID); err != nil {
		t.Fatalf("grant two module bindings: %v", err)
	}
	staff := authorization.Principal{UserID: staffID, GlobalRoles: []string{"user"}}
	staffPage, err := service.AdminListModules(ctx, staff, board.ID, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("service AdminListModules two assignments: %v", err)
	}
	if staffPage.Total != 2 || len(staffPage.Items) != 2 {
		t.Fatalf("service AdminListModules total=%d len=%d, want 2/2", staffPage.Total, len(staffPage.Items))
	}
	staffQuery, err := service.AdminListModules(ctx, staff, board.ID, ListQuery{Limit: 50, Query: "Bet"})
	if err != nil {
		t.Fatalf("service AdminListModules query: %v", err)
	}
	if staffQuery.Total != 1 || len(staffQuery.Items) != 1 || staffQuery.Items[0].ID != modules[1].ID {
		t.Fatalf("service AdminListModules query total=%d len=%d, want Beta only", staffQuery.Total, len(staffQuery.Items))
	}
	listed, total, err := store.ListModules(ctx, board.ID, ModuleFilter{
		Limit: 50, Admin: true, ModuleIDs: []uuid.UUID{modules[0].ID, modules[1].ID},
	})
	if err != nil {
		t.Fatalf("multi-module list: %v", err)
	}
	if total != 2 || len(listed) != 2 {
		t.Fatalf("multi-module total=%d len=%d, want 2/2", total, len(listed))
	}
	for _, module := range listed {
		if module.ID == modules[2].ID {
			t.Fatal("sibling module leaked")
		}
	}
	queried, queryTotal, err := store.ListModules(ctx, board.ID, ModuleFilter{
		Limit: 50, Admin: true, Query: "Bet", ModuleIDs: []uuid.UUID{modules[0].ID, modules[1].ID},
	})
	if err != nil {
		t.Fatalf("multi-module query: %v", err)
	}
	if queryTotal != 1 || len(queried) != 1 || queried[0].ID != modules[1].ID {
		t.Fatalf("multi-module query total=%d len=%d, want Beta only", queryTotal, len(queried))
	}
	page, pageTotal, err := store.ListModules(ctx, board.ID, ModuleFilter{
		Limit: 1, Offset: 1, Admin: true, ModuleIDs: []uuid.UUID{modules[0].ID, modules[1].ID},
	})
	if err != nil || pageTotal != 2 || len(page) != 1 {
		t.Fatalf("multi-module paging total=%d len=%d err=%v, want 2/1", pageTotal, len(page), err)
	}
}

func TestP1UpcomingModuleParticipantGetDenied(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the upcoming-module regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	authz := authorization.NewAuthorizer(db)
	adminID := createBoardUser(t, ctx, db, "upcoming_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "upcoming_player", []string{"user"})
	staffID := createBoardUser(t, ctx, db, "upcoming_staff", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}
	staff := authorization.Principal{UserID: staffID, GlobalRoles: []string{"user"}}
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(128),
		WithAccessPolicy(access.NewService(db, authz)),
		WithArgon2Config(argon), WithIdempotencySecret("upcoming-secret"))

	start := time.Now().UTC().Add(2 * time.Hour)
	end := time.Now().UTC().Add(4 * time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "upcoming-board", Name: "Upcoming", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Upcoming Module", Description: "pre-start secret module description",
		Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create module: %v", err)
	}
	if _, err := service.AdminBoardAction(ctx, admin, board.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish scheduled board: %v", err)
	}
	if _, err := service.JoinBoard(ctx, player, board.ID, testJoinCode(t, db, board.ID, "P1-REGRESS-CODE-01")); err != nil {
		t.Fatalf("join scheduled board: %v", err)
	}
	if _, err := service.GetModule(ctx, player, board.ID, module.ID); !errors.Is(err, ErrModuleNotAvailable) {
		t.Fatalf("participant upcoming GetModule error = %v, want ErrModuleNotAvailable", err)
	}
	page, err := service.ListModules(ctx, player, board.ID, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("participant upcoming ListModules: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Status != ModuleStatusUpcoming {
		t.Fatalf("upcoming list = %+v total=%d, want minimal UPCOMING metadata", page, page.Total)
	}
	grantBinding(t, ctx, db, board.ID, &module.ID, staffID, adminID, "board_editor", nil)
	staffModule, err := service.GetModule(ctx, staff, board.ID, module.ID)
	if err != nil {
		t.Fatalf("assigned staff upcoming GetModule: %v", err)
	}
	if staffModule.Description != "pre-start secret module description" {
		t.Fatalf("staff module description = %q", staffModule.Description)
	}
}
