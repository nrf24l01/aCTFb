package boards

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestPostgresTaskProgressByModule proves the single batched aggregate is
// visible-only and subject-scoped: hidden tasks and other subjects never enter
// the denominator or numerator, rejected/pending attempts are not solved, and
// modules without visible tasks are absent from the map.
func TestPostgresTaskProgressByModule(t *testing.T) {
	ctx, db, service, _, cleanup := newProgressTestContext(t)
	defer cleanup()

	admin, adminID := newProgressAdmin(t, ctx, db)
	_, playerID := newProgressUser(t, ctx, db, "progress_store_player")
	_, otherID := newProgressUser(t, ctx, db, "progress_store_other")
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)

	board := progressBoard(t, ctx, service, admin, CreateBoardInput{
		Slug: "progress-store", Name: "Progress Store", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err := service.AdminAddParticipant(ctx, admin, board.ID, playerID); err != nil {
		t.Fatalf("add player participant: %v", err)
	}
	moduleA := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "A", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	emptyModule := progressModule(t, ctx, service, admin, board.ID, CreateModuleInput{
		Name: "Empty", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	otherBoard := progressBoard(t, ctx, service, admin, CreateBoardInput{
		Slug: "progress-store-other", Name: "Progress Store Other", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	otherModule := progressModule(t, ctx, service, admin, otherBoard.ID, CreateModuleInput{
		Name: "Other", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})

	visibleSolved := progressTask(t, ctx, service, admin, board.ID, moduleA.ID, flagTaskInput("visible-solved", 10, true))
	hiddenSolved := progressTask(t, ctx, service, admin, board.ID, moduleA.ID, flagTaskInput("hidden-solved", 10, false))
	visibleOther := progressTask(t, ctx, service, admin, board.ID, moduleA.ID, flagTaskInput("visible-other", 10, true))
	teamOnly := progressTask(t, ctx, service, admin, board.ID, moduleA.ID, flagTaskInput("team-only", 10, true))

	// EXTENSION is task-bearing but requires an installed provider, so the
	// fixture is seeded as the persisted domain shape (no fake provider call).
	extensionModuleID := uuid.New()
	extensionTaskID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, extension_key, allowed_task_types,
			audience_uuids, enabled, sort_order, created_by)
		VALUES ($1,$2,'Ext','EXTENSION','test.ext',ARRAY['EXTENSION'],'{}',TRUE,1,$3)`,
		extensionModuleID, board.ID, adminID); err != nil {
		t.Fatalf("seed extension module: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO tasks (id, board_id, module_id, title, category, description, type, points,
			visible, submission_mode, submission_checker, extension_key, checker_key,
			instance_enabled, created_by)
		VALUES ($1,$2,$3,'Ext task','web','','EXTENSION',5,TRUE,'TEXT','PLUGIN','test.ext',
			'test.checker',FALSE,$4)`,
		extensionTaskID, board.ID, extensionModuleID, adminID); err != nil {
		t.Fatalf("seed extension task: %v", err)
	}

	insertSubmission := func(moduleID uuid.UUID, subjectType string, subjectID, taskID uuid.UUID, status, key string) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO submissions (board_id, module_id, task_id, user_id, status, score_awarded,
				idempotency_key, request_hash, subject_type, subject_id, submitted_by_user_id)
			VALUES ($1,$2,$3,$4,$5,NULL,$6,decode(repeat('cd',32),'hex'),$7,$8,$4)`,
			board.ID, moduleID, taskID, playerID, status, key, subjectType, subjectID); err != nil {
			t.Fatalf("insert %s submission: %v", status, err)
		}
	}
	insertSubmission(moduleA.ID, SubjectTypeUser, playerID, visibleSolved.ID, SubmissionStatusRejected, "progress-store-rejected-1")
	insertSubmission(moduleA.ID, SubjectTypeUser, playerID, visibleSolved.ID, SubmissionStatusPendingRev, "progress-store-pending-01")
	insertSubmission(moduleA.ID, SubjectTypeUser, playerID, visibleSolved.ID, SubmissionStatusAccepted, "progress-store-accepted-1")
	insertSubmission(moduleA.ID, SubjectTypeUser, playerID, hiddenSolved.ID, SubmissionStatusAccepted, "progress-store-accepted-2")
	insertSubmission(moduleA.ID, SubjectTypeUser, otherID, visibleOther.ID, SubmissionStatusAccepted, "progress-store-accepted-3")
	insertSubmission(extensionModuleID, SubjectTypeUser, playerID, extensionTaskID, SubmissionStatusAccepted, "progress-store-accepted-4")
	teamID := uuid.New()
	insertSubmission(moduleA.ID, SubjectTypeTeam, teamID, teamOnly.ID, SubmissionStatusAccepted, "progress-store-accepted-5")

	store := NewPostgresStore(db)
	progress, err := store.TaskProgressByModule(ctx, board.ID,
		[]uuid.UUID{moduleA.ID, emptyModule.ID, extensionModuleID, otherModule.ID},
		ScoreSubject{Type: SubjectTypeUser, ID: playerID})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if item := progress[moduleA.ID]; item.SolvedTasks != 1 || item.TotalTasks != 3 {
		t.Fatalf("module A progress = %+v, want 1/3 (hidden and other-subject excluded)", item)
	}
	if item := progress[extensionModuleID]; item.SolvedTasks != 1 || item.TotalTasks != 1 {
		t.Fatalf("extension module progress = %+v, want 1/1", item)
	}
	for _, absent := range []uuid.UUID{emptyModule.ID, otherModule.ID} {
		if item, ok := progress[absent]; ok {
			t.Fatalf("module %s unexpectedly present with %+v", absent, item)
		}
	}
	if _, err := store.TaskProgressByModule(ctx, board.ID, []uuid.UUID{moduleA.ID},
		ScoreSubject{Type: "SERVICE", ID: playerID}); err == nil {
		t.Fatal("aggregate accepted an unknown subject type")
	}

	// TEAM rows are isolated from USER rows: the same human's USER acceptance
	// does not solve the task for the team subject that accepted teamOnly.
	teamProgress, err := store.TaskProgressByModule(ctx, board.ID, []uuid.UUID{moduleA.ID},
		ScoreSubject{Type: SubjectTypeTeam, ID: teamID})
	if err != nil {
		t.Fatalf("team aggregate: %v", err)
	}
	if item := teamProgress[moduleA.ID]; item.SolvedTasks != 1 || item.TotalTasks != 3 {
		t.Fatalf("team module progress = %+v, want 1/3", item)
	}
}
