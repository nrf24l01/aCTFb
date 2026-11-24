package boards

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
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

func newLifetimeBoardService(t *testing.T) (*Service, *postgres.DB, authorization.Principal, *Board) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the instance lifetime test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	db, cleanup := openIsolatedBoardDB(t, ctx, dsn)
	t.Cleanup(cleanup)

	sealer, err := secrets.NewCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), 1)
	if err != nil {
		t.Fatal(err)
	}
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	service := NewService(NewPostgresStore(db), authz, SystemClock{}, ratelimit.NewMemoryLimiter(512),
		WithArgon2Config(config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}),
		WithIdempotencySecret("lifetime-secret"), WithAccessPolicy(accessSvc),
		WithAccessService(accessSvc), WithSealer(sealer), WithInstanceBinder(&recordingBinder{}))

	adminID := createBoardUser(t, ctx, db, "life_admin", []string{"admin"})
	admin := authorization.Principal{UserID: adminID, GlobalRoles: []string{"admin"}}
	now := time.Now().UTC()
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	board, err := service.AdminCreateBoard(ctx, admin, CreateBoardInput{
		Slug: "life-board", Name: "Lifetime Board", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate,
		RatingVisibility: RatingPublic, StartsAt: &start, EndsAt: &end,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, db, admin, board
}

func settingsFixture(defaultTTL, maxTTL, maxConcurrent int, resetEnabled, extensionEnabled bool, extensionSeconds, extensionCount int) *TaskInstanceSettings {
	return &TaskInstanceSettings{
		DefaultTTLSeconds: defaultTTL, MaxTTLSeconds: maxTTL,
		MaxConcurrentInstancesPerSubject: maxConcurrent, ResetEnabled: resetEnabled,
		Extension: &TaskInstanceExtensionSettings{Enabled: extensionEnabled, Seconds: extensionSeconds, MaxCount: extensionCount},
	}
}

func TestModuleInstanceSettingsPersistAndReload(t *testing.T) {
	service, _, admin, board := newLifetimeBoardService(t)
	ctx := context.Background()

	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Instances", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
		InstanceSettings: settingsFixture(1800, 7200, 3, false, true, 900, 6),
	})
	if err != nil {
		t.Fatalf("create module: %v", err)
	}
	if module.TaskInstanceSettings == nil {
		t.Fatal("settings were not returned on create")
	}
	if module.TaskInstanceSettings.DefaultTTLSeconds != 1800 || module.TaskInstanceSettings.MaxTTLSeconds != 7200 {
		t.Fatalf("settings wrong: %+v", module.TaskInstanceSettings)
	}
	if module.TaskInstanceSettings.Extension == nil || module.TaskInstanceSettings.Extension.MaxCount != 6 {
		t.Fatalf("extension settings wrong: %+v", module.TaskInstanceSettings.Extension)
	}
	if module.TaskInstanceSettings.ResetEnabled {
		t.Fatal("resetEnabled=false was not persisted")
	}

	reloaded, err := service.AdminGetModule(ctx, admin, board.ID, module.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TaskInstanceSettings == nil || reloaded.TaskInstanceSettings.MaxConcurrentInstancesPerSubject != 3 {
		t.Fatalf("settings did not reload: %+v", reloaded.TaskInstanceSettings)
	}

	// An omitted field preserves the stored policy.
	updated, err := service.AdminUpdateModule(ctx, admin, board.ID, module.ID, UpdateModuleInput{
		Name: OptionalString{Set: true, Value: stringPtr("Instances renamed")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.TaskInstanceSettings == nil || updated.TaskInstanceSettings.DefaultTTLSeconds != 1800 {
		t.Fatalf("omitted settings field was not preserved: %+v", updated.TaskInstanceSettings)
	}

	// An explicit null clears the policy back to the global defaults.
	cleared, err := service.AdminUpdateModule(ctx, admin, board.ID, module.ID, UpdateModuleInput{InstanceSettingsSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.TaskInstanceSettings != nil {
		t.Fatalf("explicit null did not clear the policy: %+v", cleared.TaskInstanceSettings)
	}
}

func TestModuleInstanceSettingsRejectInvalidBounds(t *testing.T) {
	service, _, admin, board := newLifetimeBoardService(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		settings *TaskInstanceSettings
		field    string
	}{
		{"default above max", settingsFixture(7200, 1800, 1, true, true, 900, 1), "maxTtlSeconds"},
		{"max above global cap", settingsFixture(60, 604801, 1, true, true, 900, 1), "maxTtlSeconds"},
		{"default below minute", settingsFixture(1, 1800, 1, true, true, 900, 1), "defaultTtlSeconds"},
		{"zero concurrency", settingsFixture(60, 1800, 0, true, true, 900, 1), "maxConcurrentInstancesPerSubject"},
		{"extension step above a day", settingsFixture(60, 1800, 1, true, true, 86401, 1), "extension.seconds"},
		{"extension count above cap", settingsFixture(60, 1800, 1, true, true, 900, 101), "extension.maxCount"},
	}
	for _, testCase := range cases {
		_, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
			Name: "M-" + testCase.name, Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeInstance},
			Enabled: true, InstanceSettings: testCase.settings,
		})
		if err == nil {
			t.Fatalf("%s: expected a validation error", testCase.name)
		}
		var invalid *ValidationError
		if !errors.As(err, &invalid) || len(invalid.Fields) == 0 {
			t.Fatalf("%s: unexpected error type %T: %v", testCase.name, err, err)
		}
		if !strings.Contains(invalid.Fields[0].Field, testCase.field) {
			t.Fatalf("%s: field = %q, want it to mention %q", testCase.name, invalid.Fields[0].Field, testCase.field)
		}
	}
}

func TestTaskLifetimeOverridesPersistAndValidateAgainstModule(t *testing.T) {
	service, _, admin, board := newLifetimeBoardService(t)
	ctx := context.Background()
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Instances", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
		InstanceSettings: settingsFixture(3600, 14400, 2, true, true, 1800, 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	templateID := uuid.New()
	createInstanceTask := func(title string, instance TaskInstanceInput) (*Task, error) {
		instance.Enabled = true
		instance.HasDeploymentTemplate = true
		instance.HasTemplateVersion = true
		instance.DeploymentTemplateID = &templateID
		instance.TemplateVersion = intPtr(1)
		return service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
			Title: title, Type: TaskTypeInstance, Category: "web", Description: "d",
			Points: 10, Visible: true,
			Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
			Instance:   instance,
		})
	}

	task, err := createInstanceTask("Tuned", TaskInstanceInput{
		TTLSeconds:        OptionalInt{Set: true, Value: intPtr(1800)},
		MaxTTLSeconds:     OptionalInt{Set: true, Value: intPtr(7200)},
		ExtensionEnabled:  OptionalBool{Set: true, Value: boolPtr(true)},
		ExtensionSeconds:  OptionalInt{Set: true, Value: intPtr(900)},
		ExtensionMaxCount: OptionalInt{Set: true, Value: intPtr(3)},
	})
	if err != nil {
		t.Fatalf("create tuned task: %v", err)
	}
	if task.InstanceTTLSeconds == nil || *task.InstanceTTLSeconds != 1800 {
		t.Fatalf("task ttl not persisted: %+v", task.InstanceTTLSeconds)
	}
	if task.InstanceExtensionMaxCount == nil || *task.InstanceExtensionMaxCount != 3 {
		t.Fatalf("task extension count not persisted: %+v", task.InstanceExtensionMaxCount)
	}

	// A PATCH that omits the lifetime fields preserves them.
	renamed, err := service.AdminUpdateTask(ctx, admin, board.ID, module.ID, task.ID, UpdateTaskInput{
		Title: OptionalString{Set: true, Value: stringPtr("Renamed")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.InstanceTTLSeconds == nil || *renamed.InstanceTTLSeconds != 1800 {
		t.Fatalf("omitted lifetime field was not preserved: %+v", renamed.InstanceTTLSeconds)
	}

	// An explicit null resets one override to inherit.
	inherited, err := service.AdminUpdateTask(ctx, admin, board.ID, module.ID, task.ID, UpdateTaskInput{
		Instance: &TaskInstanceInput{Enabled: true, TTLSeconds: OptionalInt{Set: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inherited.InstanceTTLSeconds != nil {
		t.Fatalf("explicit null did not clear the override: %+v", inherited.InstanceTTLSeconds)
	}
	if inherited.InstanceMaxTTLSeconds == nil || *inherited.InstanceMaxTTLSeconds != 7200 {
		t.Fatalf("unrelated override was cleared: %+v", inherited.InstanceMaxTTLSeconds)
	}

	// The read model resolves the effective initial lifetime from the module.
	summary := EffectiveInstanceInitialTTLSeconds(*inherited, DefaultInstanceInitialTTLSeconds)
	if summary == nil || *summary != 3600 {
		t.Fatalf("effective initial TTL = %v, want the module default 3600", summary)
	}

	// Overrides above the module ceiling are rejected with a field message.
	if _, err := createInstanceTask("TooLong", TaskInstanceInput{
		TTLSeconds: OptionalInt{Set: true, Value: intPtr(20000)},
	}); err == nil {
		t.Fatal("a task TTL above the module maximum must be rejected")
	}
	if _, err := createInstanceTask("TooManyExtensions", TaskInstanceInput{
		ExtensionSeconds:  OptionalInt{Set: true, Value: intPtr(900)},
		ExtensionMaxCount: OptionalInt{Set: true, Value: intPtr(6)},
	}); err == nil {
		t.Fatal("a task extension count above the module maximum must be rejected")
	}
	if _, err := createInstanceTask("TooLongStep", TaskInstanceInput{
		ExtensionSeconds:  OptionalInt{Set: true, Value: intPtr(2400)},
		ExtensionMaxCount: OptionalInt{Set: true, Value: intPtr(1)},
	}); err == nil {
		t.Fatal("a task extension step above the module step must be rejected")
	}
	if _, err := createInstanceTask("InitialAboveMax", TaskInstanceInput{
		TTLSeconds:    OptionalInt{Set: true, Value: intPtr(7200)},
		MaxTTLSeconds: OptionalInt{Set: true, Value: intPtr(1800)},
	}); err == nil {
		t.Fatal("initial > max must be rejected")
	}
}

func TestTaskLifetimeRequiresModuleExtensionWhenEnabling(t *testing.T) {
	service, _, admin, board := newLifetimeBoardService(t)
	ctx := context.Background()
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "NoExtend", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
		InstanceSettings: settingsFixture(3600, 14400, 2, true, false, 900, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	templateID := uuid.New()
	_, err = service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "WantsExtend", Type: TaskTypeInstance, Category: "web", Description: "d",
		Points: 10, Visible: true,
		Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
		Instance: TaskInstanceInput{
			Enabled: true, HasDeploymentTemplate: true, HasTemplateVersion: true,
			DeploymentTemplateID: &templateID, TemplateVersion: intPtr(1),
			ExtensionEnabled:  OptionalBool{Set: true, Value: boolPtr(true)},
			ExtensionSeconds:  OptionalInt{Set: true, Value: intPtr(900)},
			ExtensionMaxCount: OptionalInt{Set: true, Value: intPtr(1)},
		},
	})
	if err == nil {
		t.Fatal("enabling extensions over a module that disables them must be rejected")
	}
}

func TestDisablingInstanceClearsLifetimeOverrides(t *testing.T) {
	service, _, admin, board := newLifetimeBoardService(t)
	ctx := context.Background()
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Instances", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	templateID := uuid.New()
	task, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "Tuned", Type: TaskTypeInstance, Category: "web", Description: "d",
		Points: 10, Visible: true,
		Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
		Instance: TaskInstanceInput{
			Enabled: true, HasDeploymentTemplate: true, HasTemplateVersion: true,
			DeploymentTemplateID: &templateID, TemplateVersion: intPtr(1),
			TTLSeconds: OptionalInt{Set: true, Value: intPtr(1800)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// An INSTANCE task cannot drop its instance, so the overrides must stay
	// consistent with the enabled flag instead of being orphaned.
	if _, err := service.AdminUpdateTask(ctx, admin, board.ID, module.ID, task.ID, UpdateTaskInput{
		Instance: &TaskInstanceInput{Enabled: false},
	}); err == nil {
		t.Fatal("an INSTANCE task must not be able to disable its instance")
	}
	unchanged, err := service.AdminGetTask(ctx, admin, board.ID, module.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.InstanceEnabled {
		t.Fatal("instance flag changed despite the rejected update")
	}
	if unchanged.InstanceTTLSeconds == nil || *unchanged.InstanceTTLSeconds != 1800 {
		t.Fatalf("override was lost on a rejected update: %+v", unchanged.InstanceTTLSeconds)
	}

	// Overrides are rejected on a task that is not instance-enabled.
	if _, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "FlagWithTTL", Type: TaskTypeFlag, Category: "web", Description: "d",
		Points: 10, Visible: true,
		Submission:      SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic},
		AcceptedAnswers: []string{"flag{one}"},
		Instance:        TaskInstanceInput{Enabled: false, TTLSeconds: OptionalInt{Set: true, Value: intPtr(1800)}},
	}); err == nil {
		t.Fatal("a non-instance task must not accept lifetime overrides")
	}
}

func stringPtr(value string) *string { return &value }
func boolPtr(value bool) *bool       { return &value }

// I3: the effective pair, not just two overrides, must be valid at save time.
func TestEffectiveLifetimeValidatedAtTaskSave(t *testing.T) {
	service, _, admin, board := newLifetimeBoardService(t)
	ctx := context.Background()
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Instances", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
		InstanceSettings: settingsFixture(3600, 14400, 2, true, true, 900, 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	templateID := uuid.New()
	build := func(title string, instance TaskInstanceInput) (*Task, error) {
		instance.Enabled = true
		instance.HasDeploymentTemplate = true
		instance.HasTemplateVersion = true
		instance.DeploymentTemplateID = &templateID
		instance.TemplateVersion = intPtr(1)
		return service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
			Title: title, Type: TaskTypeInstance, Category: "web", Description: "d",
			Points: 10, Visible: true,
			Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
			Instance:   instance,
		})
	}

	// Only maxTtlSeconds set: the initial lifetime is inherited from the module
	// default (3600), which is larger. Saving this must fail now, not at start.
	_, err = build("InheritedInitialTooLong", TaskInstanceInput{
		MaxTTLSeconds: OptionalInt{Set: true, Value: intPtr(1800)},
	})
	if err == nil {
		t.Fatal("effective initial > effective max must be rejected at save")
	}
	var invalid *ValidationError
	if !errors.As(err, &invalid) || len(invalid.Fields) == 0 {
		t.Fatalf("expected a validation error, got %T: %v", err, err)
	}
	if !strings.Contains(invalid.Fields[0].Field, "maxTtlSeconds") {
		t.Fatalf("field = %q, want maxTtlSeconds", invalid.Fields[0].Field)
	}

	// Valid counterexample: an explicit shorter initial lifetime.
	if _, err := build("ExplicitShorter", TaskInstanceInput{
		TTLSeconds:    OptionalInt{Set: true, Value: intPtr(900)},
		MaxTTLSeconds: OptionalInt{Set: true, Value: intPtr(1800)},
	}); err != nil {
		t.Fatalf("a consistent explicit pair must be accepted: %v", err)
	}
}

// I3: lowering the module ceiling must not strand an existing task.
func TestModuleUpdateRejectsStrandingTask(t *testing.T) {
	service, _, admin, board := newLifetimeBoardService(t)
	ctx := context.Background()
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "Instances", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
		InstanceSettings: settingsFixture(3600, 14400, 2, true, true, 1800, 6),
	})
	if err != nil {
		t.Fatal(err)
	}
	templateID := uuid.New()
	task, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
		Title: "LongLab", Type: TaskTypeInstance, Category: "web", Description: "d",
		Points: 10, Visible: true,
		Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
		Instance: TaskInstanceInput{
			Enabled: true, HasDeploymentTemplate: true, HasTemplateVersion: true,
			DeploymentTemplateID: &templateID, TemplateVersion: intPtr(1),
			TTLSeconds: OptionalInt{Set: true, Value: intPtr(7200)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Lowering the module max below the task's override must fail and name the task.
	_, err = service.AdminUpdateModule(ctx, admin, board.ID, module.ID, UpdateModuleInput{
		InstanceSettingsSet: true,
		InstanceSettings:    settingsFixture(900, 1800, 2, true, true, 900, 3),
	})
	if err == nil {
		t.Fatal("a module update that strands an instance task must be rejected")
	}
	var invalid *ValidationError
	if !errors.As(err, &invalid) || len(invalid.Fields) == 0 {
		t.Fatalf("expected a validation error, got %T: %v", err, err)
	}
	if !strings.Contains(invalid.Fields[0].Issue, task.Title) {
		t.Fatalf("message %q must name the offending task %q", invalid.Fields[0].Issue, task.Title)
	}

	// The module was not changed.
	current, err := service.AdminGetModule(ctx, admin, board.ID, module.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.TaskInstanceSettings == nil || current.TaskInstanceSettings.MaxTTLSeconds != 14400 {
		t.Fatalf("rejected update changed the module: %+v", current.TaskInstanceSettings)
	}

	// The same guard covers extension limits: a task that asked for a larger step
	// must block a module update that lowers the step below it.
	if _, err := service.AdminUpdateTask(ctx, admin, board.ID, module.ID, task.ID, UpdateTaskInput{
		Instance: &TaskInstanceInput{
			Enabled:           true,
			ExtensionEnabled:  OptionalBool{Set: true, Value: boolPtr(true)},
			ExtensionSeconds:  OptionalInt{Set: true, Value: intPtr(1800)},
			ExtensionMaxCount: OptionalInt{Set: true, Value: intPtr(3)},
		},
	}); err != nil {
		t.Fatalf("task extension override inside the module limits must be accepted: %v", err)
	}
	_, err = service.AdminUpdateModule(ctx, admin, board.ID, module.ID, UpdateModuleInput{
		InstanceSettingsSet: true,
		InstanceSettings:    settingsFixture(3600, 14400, 2, true, true, 900, 3),
	})
	if err == nil {
		t.Fatal("a module update that lowers the extension step below a task override must be rejected")
	}
	if !strings.Contains(err.Error(), task.Title) {
		t.Fatalf("message %q must name the offending task %q", err.Error(), task.Title)
	}
}

// I3 race: a task lifetime edit and a module policy update must serialize on the
// same module lock, so the committed state is never an invalid combination that
// the service would reject at start.
func TestTaskEditAndModuleUpdateSerializeRealPostgres(t *testing.T) {
	service, db, admin, board := newLifetimeBoardService(t)
	ctx := context.Background()
	templateID := uuid.New()

	for iteration := 0; iteration < 6; iteration++ {
		module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
			Name: "Race" + string(rune('A'+iteration)), Type: ModuleTypeTasks,
			AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
			InstanceSettings: settingsFixture(3600, 14400, 2, true, true, 1800, 6),
		})
		if err != nil {
			t.Fatalf("iteration %d: create module: %v", iteration, err)
		}
		task, err := service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
			Title: "RaceLab", Type: TaskTypeInstance, Category: "web", Description: "d",
			Points: 10, Visible: true,
			Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
			Instance: TaskInstanceInput{
				Enabled: true, HasDeploymentTemplate: true, HasTemplateVersion: true,
				DeploymentTemplateID: &templateID, TemplateVersion: intPtr(1),
				TTLSeconds: OptionalInt{Set: true, Value: intPtr(1800)},
			},
		})
		if err != nil {
			t.Fatalf("iteration %d: create task: %v", iteration, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Lower the module ceiling.
			_, _ = service.AdminUpdateModule(ctx, admin, board.ID, module.ID, UpdateModuleInput{
				InstanceSettingsSet: true,
				InstanceSettings:    settingsFixture(900, 1200, 2, true, true, 900, 3),
			})
		}()
		go func() {
			defer wg.Done()
			// Raise the task initial lifetime.
			_, _ = service.AdminUpdateTask(ctx, admin, board.ID, module.ID, task.ID, UpdateTaskInput{
				Instance: &TaskInstanceInput{
					Enabled:    true,
					TTLSeconds: OptionalInt{Set: true, Value: intPtr(7200)},
				},
			})
		}()
		wg.Wait()

		// Whatever committed, the pair must be usable: the service must accept it.
		var settingsRaw []byte
		if err := db.Pool.QueryRow(ctx, `SELECT task_instance_settings FROM modules WHERE id=$1`, module.ID).Scan(&settingsRaw); err != nil {
			t.Fatal(err)
		}
		settings, err := decodeTaskInstanceSettings(settingsRaw)
		if err != nil {
			t.Fatal(err)
		}
		current, err := service.AdminGetTask(ctx, admin, board.ID, module.ID, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		initial, max := EffectiveLifetimeSeconds(taskLifetimeOverrides(*current), settings)
		if initial > max {
			t.Fatalf("iteration %d committed an invalid pair: initial=%d max=%d", iteration, initial, max)
		}
	}
}

// N3: a module that grants zero extensions must not be bypassed by a task
// override through the API, even when the module still reports enabled=true.
func TestTaskCannotRaiseExtensionsOverZeroModuleBudget(t *testing.T) {
	service, db, admin, board := newLifetimeBoardService(t)
	ctx := context.Background()
	module, err := service.AdminCreateModule(ctx, admin, board.ID, CreateModuleInput{
		Name: "ZeroBudget", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeInstance}, Enabled: true,
		InstanceSettings: settingsFixture(3600, 14400, 2, true, true, 900, 0),
	})
	if err != nil {
		t.Fatalf("a module with maxCount 0 is a valid configuration: %v", err)
	}
	if module.TaskInstanceSettings.Extension.MaxCount != 0 {
		t.Fatalf("maxCount 0 was not persisted: %+v", module.TaskInstanceSettings.Extension)
	}
	templateID := uuid.New()
	build := func(title string, instance TaskInstanceInput) (*Task, error) {
		instance.Enabled = true
		instance.HasDeploymentTemplate = true
		instance.HasTemplateVersion = true
		instance.DeploymentTemplateID = &templateID
		instance.TemplateVersion = intPtr(1)
		return service.AdminCreateTask(ctx, admin, board.ID, module.ID, CreateTaskInput{
			Title: title, Type: TaskTypeInstance, Category: "web", Description: "d",
			Points: 10, Visible: true,
			Submission: SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerManual},
			Instance:   instance,
		})
	}

	// A task asking for one extension over a zero budget is rejected, and no task
	// row is created.
	before := taskCount(t, ctx, db, board.ID)
	_, err = build("WantsOne", TaskInstanceInput{
		ExtensionEnabled:  OptionalBool{Set: true, Value: boolPtr(true)},
		ExtensionSeconds:  OptionalInt{Set: true, Value: intPtr(900)},
		ExtensionMaxCount: OptionalInt{Set: true, Value: intPtr(1)},
	})
	if err == nil {
		t.Fatal("a task must not raise extensions over a zero module budget")
	}
	var invalid *ValidationError
	if !errors.As(err, &invalid) || len(invalid.Fields) == 0 {
		t.Fatalf("expected a validation error, got %T: %v", err, err)
	}
	if !strings.Contains(invalid.Fields[0].Field, "extension.maxCount") {
		t.Fatalf("field = %q, want extension.maxCount", invalid.Fields[0].Field)
	}
	if after := taskCount(t, ctx, db, board.ID); after != before {
		t.Fatalf("rejected task was persisted: before=%d after=%d", before, after)
	}

	// A task with no override inherits the zero budget and is accepted.
	inheriting, err := build("InheritsZero", TaskInstanceInput{})
	if err != nil {
		t.Fatalf("a task inheriting a zero budget must be accepted: %v", err)
	}
	if inheriting.InstanceExtensionMaxCount != nil {
		t.Fatalf("unexpected override persisted: %+v", inheriting.InstanceExtensionMaxCount)
	}
}
