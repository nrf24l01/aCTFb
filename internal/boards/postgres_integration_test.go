package boards

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
)

// TestP1CorePostgresIntegration is opt-in and uses an isolated schema. It
// applies migrations 001-006, then exercises board/module/task creation,
// participation, flag verdicts, idempotency, concurrency, score/history,
// module-scoped reviewer isolation, expiry/revocation and IDOR.
func TestP1CorePostgresIntegration(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("ACTFB_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the P1 real PostgreSQL test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	authz := authorization.NewAuthorizer(db)
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(1024),
		WithArgon2Config(argon), WithIdempotencySecret("integration-idempotency-secret"))

	adminID := createBoardUser(t, ctx, db, "p1_admin", []string{"admin"})
	playerID := createBoardUser(t, ctx, db, "p1_player", []string{"user"})
	editorID := createBoardUser(t, ctx, db, "p1_editor", []string{"user"})
	reviewerID := createBoardUser(t, ctx, db, "p1_reviewer", []string{"user"})
	expiredID := createBoardUser(t, ctx, db, "p1_expired", []string{"user"})
	revokedID := createBoardUser(t, ctx, db, "p1_revoked", []string{"user"})
	concurrentID := createBoardUser(t, ctx, db, "p1_concurrent", []string{"user"})
	moduleEditorID := createBoardUser(t, ctx, db, "p1_module_editor", []string{"user"})
	privateID := createBoardUser(t, ctx, db, "p1_private", []string{"user"})

	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	player := authorization.Principal{UserID: playerID, GlobalRoles: []string{"user"}}
	editor := authorization.Principal{UserID: editorID, GlobalRoles: []string{"user"}}
	reviewer := authorization.Principal{UserID: reviewerID, GlobalRoles: []string{"user"}}
	expired := authorization.Principal{UserID: expiredID, GlobalRoles: []string{"user"}}
	revoked := authorization.Principal{UserID: revokedID, GlobalRoles: []string{"user"}}
	concurrent := authorization.Principal{UserID: concurrentID, GlobalRoles: []string{"user"}}
	moduleEditor := authorization.Principal{UserID: moduleEditorID, GlobalRoles: []string{"user"}}
	privateUser := authorization.Principal{UserID: privateID, GlobalRoles: []string{"user"}}

	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC().Add(24 * time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "p1-board", Name: "P1 Board", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	if board.Status != BoardStatusDraft {
		t.Fatalf("created status = %s, want DRAFT", board.Status)
	}

	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Tasks", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create module: %v", err)
	}
	secondModule, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Sibling", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create sibling module: %v", err)
	}

	task, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "Find the flag", Type: TaskTypeFlag, Category: "web", Description: "hidden description",
		Points: 100, Visible: true,
		Submission:      SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic},
		AcceptedAnswers: []string{"flag{correct}", "flag{alternate}"},
		Instance:        TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	secondTask, err := service.AdminCreateTask(ctx, admin, board.ID, secondModule.ID, CreateTaskInput{
		Title: "Sibling task", Type: TaskTypeFlag, Category: "web", Description: "sibling",
		Points: 50, Visible: true,
		Submission:      SubmissionModeInput(),
		AcceptedAnswers: []string{"flag{sibling}"},
		Instance:        TaskInstanceInput{Enabled: false},
	})
	if err != nil {
		t.Fatalf("create sibling task: %v", err)
	}

	if _, err := service.AdminBoardAction(ctx, admin, board.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish board: %v", err)
	}
	if _, err := service.JoinBoard(ctx, player, board.ID, testJoinCode(t, db, board.ID, "P1-JOIN-CODE-0001")); err != nil {
		t.Fatalf("player join: %v", err)
	}
	if _, err := service.JoinBoard(ctx, concurrent, board.ID, testJoinCode(t, db, board.ID, "P1-JOIN-CODE-0001")); err != nil {
		t.Fatalf("concurrent join: %v", err)
	}

	wrongKey := "wrong-key-0000001"
	wrong, err := service.SubmitTask(ctx, player, TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID}, wrongKey, SubmitInput{Text: "flag{nope}"})
	if err != nil {
		t.Fatalf("wrong flag: %v", err)
	}
	if wrong.Status != SubmissionStatusRejected || wrong.ScoreAwarded != nil {
		t.Fatalf("wrong verdict = %s score=%v, want REJECTED/nil", wrong.Status, wrong.ScoreAwarded)
	}

	rightKey := "right-key-0000001"
	right, err := service.SubmitTask(ctx, player, TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID}, rightKey, SubmitInput{Text: "flag{correct}"})
	if err != nil {
		t.Fatalf("right flag: %v", err)
	}
	if right.Status != SubmissionStatusAccepted || right.ScoreAwarded == nil || *right.ScoreAwarded != 100 {
		t.Fatalf("right verdict = %s score=%v, want ACCEPTED/100", right.Status, right.ScoreAwarded)
	}

	replay, err := service.SubmitTask(ctx, player, TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID}, rightKey, SubmitInput{Text: "flag{correct}"})
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if replay.ID != right.ID || replay.Status != right.Status {
		t.Fatalf("idempotent replay changed result: %+v vs %+v", replay, right)
	}

	if _, err := service.SubmitTask(ctx, player, TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID}, rightKey, SubmitInput{Text: "flag{alternate}"}); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("same key different body error = %v, want ErrIdempotencyKeyReused", err)
	}
	if _, err := service.SubmitTask(ctx, player, TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID}, "right-key-0000002", SubmitInput{Text: "flag{alternate}"}); !errors.Is(err, ErrTaskAlreadySolved) {
		t.Fatalf("new key after solve error = %v, want ErrTaskAlreadySolved", err)
	}

	history, err := service.ListMyTaskSubmissions(ctx, player, TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID}, ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if history.Total != 2 {
		t.Fatalf("history total = %d, want 2 (wrong + accepted; replay not duplicated)", history.Total)
	}
	for _, submission := range history.Items {
		if submission.Message != nil && strings.Contains(*submission.Message, "flag{") {
			t.Fatalf("history leaked submitted flag text: %q", *submission.Message)
		}
	}

	scoreboard, err := service.GetScoreboard(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("scoreboard: %v", err)
	}
	assertScore(t, scoreboard, playerID, 100)
	entry, err := service.GetMyScoreboardEntry(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("my scoreboard entry: %v", err)
	}
	if entry.Score != 100 || entry.Place != 1 {
		t.Fatalf("my entry = %+v, want score 100 place 1", entry)
	}

	if err := service.LeaveBoard(ctx, player, board.ID); !errors.Is(err, ErrBoardLeaveNotAllowed) {
		t.Fatalf("leave with history error = %v, want ErrBoardLeaveNotAllowed", err)
	}
	if err := service.AdminDeleteBoard(ctx, admin, board.ID); !errors.Is(err, ErrBoardDeleteNotAllowed) {
		t.Fatalf("delete with history error = %v, want ErrBoardDeleteNotAllowed", err)
	}

	// Concurrent different keys: exactly one accepted, no double score.
	const attempts = 10
	var wg sync.WaitGroup
	results := make(chan error, attempts)
	for index := 0; index < attempts; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, subErr := service.SubmitTask(ctx, concurrent,
				TaskPath{BoardID: board.ID, ModuleID: module.ID, TaskID: task.ID},
				fmt.Sprintf("concurrent-key-%03d", index), SubmitInput{Text: "flag{alternate}"})
			results <- subErr
		}(index)
	}
	wg.Wait()
	close(results)
	accepted, solved := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrTaskAlreadySolved):
			solved++
		default:
			t.Fatalf("concurrent submission error = %v", err)
		}
	}
	if accepted != 1 || solved != attempts-1 {
		t.Fatalf("concurrent accepted=%d solved=%d, want 1/%d", accepted, solved, attempts-1)
	}
	scoreboard, err = service.GetScoreboard(ctx, player, board.ID)
	if err != nil {
		t.Fatalf("scoreboard after concurrency: %v", err)
	}
	assertScore(t, scoreboard, concurrentID, 100)
	for _, item := range scoreboard.Items {
		if (item.UserID == playerID || item.UserID == concurrentID) && item.Place != 1 {
			t.Fatalf("tied score place for %s = %d, want 1", item.UserID, item.Place)
		}
	}

	// Board B module/task for IDOR.
	boardB, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "p1-board-b", Name: "P1 Board B", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
	})
	if err != nil {
		t.Fatalf("create board B: %v", err)
	}
	moduleB, err := service.AdminCreateModule(ctx, admin, boardB.ID, CreateModuleInput{
		Name: "Tasks B", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create module B: %v", err)
	}
	if _, err := service.GetModule(ctx, admin, boardB.ID, moduleB.ID); err != nil {
		t.Fatalf("get module B: %v", err)
	}
	if _, err := service.GetModule(ctx, player, boardB.ID, module.ID); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("cross-board parent substitution error = %v, want ErrModuleNotFound", err)
	}

	// Board-wide editor may create modules in board A but not board B.
	grantBinding(t, ctx, db, board.ID, nil, editorID, adminID, "board_editor", nil)
	if _, err := service.AdminCreateModule(ctx, editor, board.ID, CreateModuleInput{
		Name: "Editor module", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	}); err != nil {
		t.Fatalf("board-wide editor create own board: %v", err)
	}
	if _, err := service.AdminCreateModule(ctx, editor, boardB.ID, CreateModuleInput{
		Name: "Forbidden module", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor board B create error = %v, want ErrForbidden", err)
	}

	// Module-scoped editor updates only its assigned module and cannot create a
	// sibling module.
	grantBinding(t, ctx, db, board.ID, &module.ID, moduleEditorID, adminID, "board_editor", nil)
	if _, err := service.AdminUpdateModule(ctx, moduleEditor, board.ID, module.ID, UpdateModuleInput{
		Description: optionalStringForTest("module editor change"),
	}); err != nil {
		t.Fatalf("module-scoped editor update assigned module: %v", err)
	}
	if _, err := service.AdminCreateModule(ctx, moduleEditor, board.ID, CreateModuleInput{
		Name: "Sibling create", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("module-scoped editor create sibling error = %v, want ErrForbidden", err)
	}
	if _, err := service.AdminUpdateModule(ctx, moduleEditor, board.ID, secondModule.ID, UpdateModuleInput{
		Description: optionalStringForTest("sibling change"),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("module-scoped editor update sibling error = %v, want ErrForbidden", err)
	}

	// Module-scoped reviewer reads module A only, not sibling module.
	if _, err := service.SubmitTask(ctx, player, TaskPath{BoardID: board.ID, ModuleID: secondModule.ID, TaskID: secondTask.ID}, "sibling-key-00001", SubmitInput{Text: "flag{sibling}"}); err != nil {
		t.Fatalf("sibling submit: %v", err)
	}
	submissionsA, err := service.AdminListSubmissions(ctx, admin, board.ID, SubmissionFilter{
		ListQuery: ListQuery{Limit: 100}, ModuleID: &module.ID,
	})
	if err != nil || len(submissionsA.Items) == 0 {
		t.Fatalf("admin submissions module A: %v items=%d", err, len(submissionsA.Items))
	}
	submissionsSibling, err := service.AdminListSubmissions(ctx, admin, board.ID, SubmissionFilter{
		ListQuery: ListQuery{Limit: 100}, ModuleID: &secondModule.ID,
	})
	if err != nil || len(submissionsSibling.Items) == 0 {
		t.Fatalf("admin submissions sibling: %v items=%d", err, len(submissionsSibling.Items))
	}
	grantBinding(t, ctx, db, board.ID, &module.ID, reviewerID, adminID, "reviewer", nil)
	if _, err := service.AdminGetSubmission(ctx, reviewer, board.ID, submissionsA.Items[0].ID); err != nil {
		t.Fatalf("reviewer assigned module read: %v", err)
	}
	if _, err := service.AdminGetSubmission(ctx, reviewer, board.ID, submissionsSibling.Items[0].ID); !errors.Is(err, ErrSubmissionNotFound) {
		t.Fatalf("reviewer sibling read error = %v, want ErrSubmissionNotFound", err)
	}
	if _, err := service.AdminCreateTask(ctx, reviewer, board.ID, module.ID, CreateTaskInput{
		Title: "Reviewer task", Type: TaskTypeFlag, Category: "web", Points: 1, Visible: true,
		Submission: SubmissionModeInput(), AcceptedAnswers: []string{"x"},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer create task error = %v, want ErrForbidden", err)
	}

	// Expired and revoked bindings deny immediately on the next service call.
	createdPast := time.Now().UTC().Add(-2 * time.Hour)
	expiredAt := time.Now().UTC().Add(-time.Hour)
	grantBindingAt(t, ctx, db, board.ID, nil, expiredID, adminID, "board_editor", &expiredAt, createdPast)
	if _, err := service.AdminUpdateModule(ctx, expired, board.ID, module.ID, UpdateModuleInput{Description: optionalStringForTest("nope")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired binding error = %v, want ErrForbidden", err)
	}
	grantBinding(t, ctx, db, board.ID, nil, revokedID, adminID, "board_editor", nil)
	if _, err := db.Pool.Exec(ctx, `UPDATE board_role_bindings SET revoked_at=NOW() WHERE board_id=$1 AND user_id=$2`, board.ID, revokedID); err != nil {
		t.Fatalf("revoke binding: %v", err)
	}
	if _, err := service.AdminUpdateModule(ctx, revoked, board.ID, module.ID, UpdateModuleInput{Description: optionalStringForTest("nope")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked binding error = %v, want ErrForbidden", err)
	}

	// Private unjoined board returns BOARD_ACCESS_DENIED; unknown ID is not found.
	if _, err := service.GetBoard(ctx, privateUser, board.ID); !errors.Is(err, ErrBoardAccessDenied) {
		t.Fatalf("private unjoined board error = %v, want ErrBoardAccessDenied", err)
	}
	if _, err := service.GetBoard(ctx, privateUser, uuid.New()); !errors.Is(err, ErrBoardNotFound) {
		t.Fatalf("unknown board error = %v, want ErrBoardNotFound", err)
	}

	// Hidden rating still computes/stores but denies scoreboard and hides place.
	hiddenBoard, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "p1-hidden", Name: "Hidden", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingHidden,
		StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatalf("create hidden board: %v", err)
	}
	hiddenModule, err := service.AdminCreateModule(ctx, admin, hiddenBoard.ID, CreateModuleInput{
		Name: "Tasks", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create hidden module: %v", err)
	}
	hiddenTask, err := service.AdminCreateTask(ctx, admin, hiddenBoard.ID, hiddenModule.ID, CreateTaskInput{
		Title: "Hidden task", Type: TaskTypeFlag, Category: "web", Points: 10, Visible: true,
		Submission: SubmissionModeInput(), AcceptedAnswers: []string{"flag{hidden}"},
	})
	if err != nil {
		t.Fatalf("create hidden task: %v", err)
	}
	if _, err := service.AdminBoardAction(ctx, admin, hiddenBoard.ID, BoardActionPublish); err != nil {
		t.Fatalf("publish hidden board: %v", err)
	}
	if _, err := service.JoinBoard(ctx, player, hiddenBoard.ID, testJoinCode(t, db, hiddenBoard.ID, "P1-HIDDEN-CODE-01")); err != nil {
		t.Fatalf("join hidden board: %v", err)
	}
	if _, err := service.SubmitTask(ctx, player, TaskPath{BoardID: hiddenBoard.ID, ModuleID: hiddenModule.ID, TaskID: hiddenTask.ID}, "hidden-key-000001", SubmitInput{Text: "flag{hidden}"}); err != nil {
		t.Fatalf("hidden submit: %v", err)
	}
	if _, err := service.GetScoreboard(ctx, player, hiddenBoard.ID); !errors.Is(err, ErrRatingHidden) {
		t.Fatalf("hidden scoreboard error = %v, want ErrRatingHidden", err)
	}
	me, err := service.GetBoardMe(ctx, player, hiddenBoard.ID)
	if err != nil {
		t.Fatalf("hidden board me: %v", err)
	}
	if me.Place != nil {
		t.Fatalf("hidden board me place = %v, want nil", me.Place)
	}
}

func SubmissionModeInput() SubmissionPolicyInput {
	return SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic}
}

// TestIndividualScoreboardIsBoardScoped is a regression test for a scoreboard
// query that joined board_participants without a board filter. A participant
// with memberships in several INDIVIDUAL boards must appear at most once on
// each board, and participants of another board must never leak into it.
func TestIndividualScoreboardIsBoardScoped(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("ACTFB_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the scoreboard isolation test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	authz := authorization.NewAuthorizer(db)
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(64),
		WithIdempotencySecret("scoreboard-isolation-secret"))
	adminID := createBoardUser(t, ctx, db, "iso_admin", []string{"admin"})
	aliceID := createBoardUser(t, ctx, db, "iso_alice", []string{"user"})
	bobID := createBoardUser(t, ctx, db, "iso_bob", []string{"user"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	alice := authorization.Principal{UserID: aliceID, GlobalRoles: []string{"user"}}

	create := func(slug string) *Board {
		board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
			Slug: slug, Name: slug, ParticipationMode: ParticipationIndividual,
			RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
		})
		if err != nil {
			t.Fatalf("create board %s: %v", slug, err)
		}
		return board
	}
	boardA := create("iso-board-a")
	boardB := create("iso-board-b")
	for _, membership := range []struct {
		board *Board
		user  uuid.UUID
	}{{boardA, aliceID}, {boardB, aliceID}, {boardB, bobID}} {
		if err := service.AdminAddParticipant(ctx, admin, membership.board.ID, membership.user); err != nil {
			t.Fatalf("add participant: %v", err)
		}
	}

	scoreboard, err := service.GetScoreboard(ctx, alice, boardA.ID)
	if err != nil {
		t.Fatalf("scoreboard A: %v", err)
	}
	if len(scoreboard.Items) != 1 {
		t.Fatalf("board A scoreboard items = %+v, want exactly one scoped participant", scoreboard.Items)
	}
	if scoreboard.Items[0].UserID != aliceID {
		t.Fatalf("board A scoreboard subject = %s, want %s", scoreboard.Items[0].UserID, aliceID)
	}
	for _, item := range scoreboard.Items {
		if item.UserID == bobID {
			t.Fatal("board B participant leaked into board A scoreboard")
		}
	}
}

func optionalStringForTest(value string) OptionalString {
	return OptionalString{Set: true, Value: &value}
}

func assertScore(t *testing.T, scoreboard *Scoreboard, userID uuid.UUID, want int) {
	t.Helper()
	for _, item := range scoreboard.Items {
		if item.UserID == userID {
			if item.Score != want {
				t.Fatalf("score for %s = %d, want %d", userID, item.Score, want)
			}
			return
		}
	}
	t.Fatalf("scoreboard has no row for %s", userID)
}

func grantBinding(t *testing.T, ctx context.Context, db *postgres.DB, boardID uuid.UUID, moduleID *uuid.UUID, userID, adminID uuid.UUID, role string, expiresAt *time.Time) {
	t.Helper()
	grantBindingAt(t, ctx, db, boardID, moduleID, userID, adminID, role, expiresAt, time.Now().UTC())
}

func grantBindingAt(t *testing.T, ctx context.Context, db *postgres.DB, boardID uuid.UUID, moduleID *uuid.UUID, userID, adminID uuid.UUID, role string, expiresAt *time.Time, createdAt time.Time) {
	t.Helper()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO board_role_bindings (board_id, module_id, user_id, role, granted_by, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, boardID, moduleID, userID, role, adminID, expiresAt, createdAt)
	if err != nil {
		t.Fatalf("insert binding: %v", err)
	}
}

func createBoardUser(t *testing.T, ctx context.Context, db *postgres.DB, username string, roles []string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := db.Pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password, roles)
		VALUES ($1,$2,'argon2id-placeholder',$3) RETURNING id`,
		username, username+"@example.test", roles).Scan(&id)
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return id
}

func openIsolatedBoardDB(t *testing.T, ctx context.Context, dsn string) (*postgres.DB, func()) {
	t.Helper()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	schema := "actfb_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(schema, "actfb_test_") || len(schema) > 63 {
		admin.Close()
		t.Fatalf("unsafe schema name %q", schema)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	}

	scopedDSN, err := boardDSNWithSearchPath(dsn, schema)
	if err != nil {
		cleanup()
		t.Fatalf("scoped DSN: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(scopedDSN)
	if err != nil {
		cleanup()
		t.Fatalf("parse pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		cleanup()
		t.Fatalf("create pool: %v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	db := &postgres.DB{Pool: pool, SQL: sqlDB}
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		cleanup()
		t.Fatalf("resolve migrations: %v", err)
	}
	if err := postgres.RunMigrations(ctx, sqlDB, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		cleanup()
		pool.Close()
		_ = sqlDB.Close()
		t.Fatalf("run migrations: %v", err)
	}
	return db, func() {
		cleanup()
		_ = sqlDB.Close()
		pool.Close()
	}
}

func boardDSNWithSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", errors.New("DSN must use postgres:// or postgresql:// URL form")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	return u.String(), nil
}
