package boards

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

type recordingBinder struct {
	calls int
	err   error
}

func (b *recordingBinder) BindTaskInstance(context.Context, authorization.Principal, uuid.UUID, uuid.UUID, uuid.UUID, InstanceBindingInput) (uuid.UUID, error) {
	b.calls++
	if b.err != nil {
		return uuid.Nil, b.err
	}
	return uuid.New(), nil
}

func TestInstanceTaskBindingAndCompensation(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the instance binding test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	defer cleanup()

	sealer, err := secrets.NewCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), 1)
	if err != nil {
		t.Fatal(err)
	}
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	binder := &recordingBinder{}
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(512),
		WithArgon2Config(config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}),
		WithIdempotencySecret("instance-bind-secret"), WithAccessPolicy(accessSvc),
		WithAccessService(accessSvc), WithSealer(sealer), WithInstanceBinder(binder))

	adminID := createBoardUser(t, ctx, db, "inst_admin", []string{"admin"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	now := time.Now().UTC()
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "inst-board", Name: "Instance Board", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate,
		RatingVisibility: RatingPublic, StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatal(err)
	}
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Instances", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	templateID := uuid.New()
	task, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "Bound instance", Type: TaskTypeInstance, Category: "web", Description: "bound",
		Points: 10, Visible: true,
		Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
		Instance: TaskInstanceInput{
			Enabled: true, HasDeploymentTemplate: true, HasTemplateVersion: true,
			DeploymentTemplateID: &templateID, TemplateVersion: intPtr(1),
		},
	})
	if err != nil {
		t.Fatalf("create instance task: %v", err)
	}
	if !task.InstanceEnabled || binder.calls != 1 {
		t.Fatalf("task instanceEnabled=%v binderCalls=%d", task.InstanceEnabled, binder.calls)
	}
	published, err := service.AdminBoardAction(ctx, admin, board.ID, BoardActionPublish)
	if err != nil {
		t.Fatalf("publish board with INSTANCE task: %v", err)
	}
	if published.Status != BoardStatusActive {
		t.Fatalf("published status = %s, want ACTIVE", published.Status)
	}

	binder.err = errors.New("binding rejected")
	before := taskCount(t, ctx, db, board.ID)
	if _, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "Failed instance", Type: TaskTypeInstance, Category: "web", Description: "fail",
		Points: 10, Visible: true,
		Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
		Instance: TaskInstanceInput{
			Enabled: true, HasDeploymentTemplate: true, HasTemplateVersion: true,
			DeploymentTemplateID: &templateID, TemplateVersion: intPtr(1),
		},
	}); err == nil {
		t.Fatal("expected binding failure")
	}
	if after := taskCount(t, ctx, db, board.ID); after != before {
		t.Fatalf("failed binding left a task: before=%d after=%d", before, after)
	}
}

func intPtr(value int) *int { return &value }

func taskCount(t *testing.T, ctx context.Context, db *postgres.DB, boardID uuid.UUID) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE board_id=$1`, boardID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
