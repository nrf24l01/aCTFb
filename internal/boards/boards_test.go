package boards

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
)

func TestBoardEffectiveStatusHalfOpen(t *testing.T) {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	board := Board{Status: BoardStatusScheduled, StartsAt: &start, EndsAt: &end}

	if got := board.EffectiveStatus(start.Add(-time.Nanosecond)); got != BoardStatusScheduled {
		t.Fatalf("before start = %s, want SCHEDULED", got)
	}
	if got := board.EffectiveStatus(start); got != BoardStatusActive {
		t.Fatalf("at start = %s, want ACTIVE", got)
	}
	if got := board.EffectiveStatus(end.Add(-time.Nanosecond)); got != BoardStatusActive {
		t.Fatalf("before end = %s, want ACTIVE", got)
	}
	if got := board.EffectiveStatus(end); got != BoardStatusFinished {
		t.Fatalf("at end = %s, want FINISHED", got)
	}
	archived := Board{Status: BoardStatusArchived, StartsAt: &start, EndsAt: &end}
	if got := archived.EffectiveStatus(start); got != BoardStatusArchived {
		t.Fatalf("archived = %s, want ARCHIVED", got)
	}
}

func TestValidateBoardUnsupportedPolicy(t *testing.T) {
	base := Board{
		Slug: "valid-slug", Name: "Board", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
	}
	if err := validateBoardInput(base, time.Now()); err != nil {
		t.Fatalf("valid board rejected: %v", err)
	}
	cases := []struct {
		name  string
		board Board
		code  string
	}{
		{"team-without-settings", func() Board { b := base; b.ParticipationMode = ParticipationTeam; return b }(), "REQUIRED"},
		{"slug", func() Board { b := base; b.Slug = "Bad Slug"; return b }(), "INVALID_FORMAT"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := validateBoardInput(test.board, time.Now())
			validation, ok := err.(*ValidationError)
			if !ok || len(validation.Fields) == 0 {
				t.Fatalf("error = %v, want ValidationError", err)
			}
			if validation.Fields[0].Code != test.code {
				t.Fatalf("code = %s, want %s", validation.Fields[0].Code, test.code)
			}
		})
	}
}

func TestValidateBoardWindow(t *testing.T) {
	start := time.Now().UTC()
	end := start.Add(time.Hour)
	board := Board{
		Slug: "valid-slug", Name: "Board", ParticipationMode: ParticipationIndividual,
		RegistrationMode: RegistrationOpen, Visibility: VisibilityPrivate, RatingVisibility: RatingPublic,
	}
	if err := validateBoardInput(board, time.Now()); err != nil {
		t.Fatalf("open window rejected: %v", err)
	}
	board.StartsAt = &start
	if err := validateBoardInput(board, time.Now()); err == nil {
		t.Fatal("one-sided board window accepted")
	}
	board.EndsAt = &end
	if err := validateBoardInput(board, time.Now()); err != nil {
		t.Fatalf("valid window rejected: %v", err)
	}
	board.EndsAt = &start
	if err := validateBoardInput(board, time.Now()); err == nil {
		t.Fatal("non-increasing window accepted")
	}
}

func TestValidateModulePolicyAndWindow(t *testing.T) {
	boardStart := time.Now().UTC()
	boardEnd := boardStart.Add(24 * time.Hour)
	board := &Board{StartsAt: &boardStart, EndsAt: &boardEnd}
	module := Module{Name: "Tasks", Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}, Enabled: true}
	if err := validateModuleInput(module, *board); err != nil {
		t.Fatalf("valid module rejected: %v", err)
	}
	module.AllowedTaskTypes = []string{TaskTypeInstance}
	if err := validateModuleInput(module, *board); err != nil {
		t.Fatalf("INSTANCE module task type rejected: %v", err)
	}
	module.Type = ModuleTypeAttackDefense
	module.AllowedTaskTypes = []string{TaskTypeExploit, TaskTypePatch}
	if err := validateModuleInput(module, *board); err != nil {
		t.Fatalf("ATTACK_DEFENSE module rejected: %v", err)
	}
	module.Type = ModuleTypePentest
	module.AllowedTaskTypes = []string{TaskTypePentestReport}
	if err := validateModuleInput(module, *board); err != nil {
		t.Fatalf("PENTEST module rejected: %v", err)
	}
	module.Type = ModuleTypeTasks
	module.AllowedTaskTypes = []string{TaskTypeFlag}
	outside := boardStart.Add(-time.Hour)
	module.StartsAt = &outside
	if err := validateModuleInput(module, *board); err == nil {
		t.Fatal("module window outside board accepted")
	}
}

func TestValidateTaskPolicy(t *testing.T) {
	module := Module{Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}}
	task := Task{
		Title: "Task", Category: "web", Type: TaskTypeFlag, Points: 10, Visible: true,
		SubmissionMode: SubmissionModeText, SubmissionChecker: SubmissionCheckerAutomatic,
	}
	if err := validateTaskInput(task, module); err != nil {
		t.Fatalf("valid task rejected: %v", err)
	}
	if err := validateSubmissionPolicy(SubmissionPolicyInput{
		Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic, HasFilesPolicy: true,
	}, TaskTypeFlag); err == nil {
		t.Fatal("file policy accepted")
	}
	task.InstanceEnabled = true
	if err := validateTaskInput(task, module); err == nil {
		t.Fatal("instance task accepted")
	}
	if err := validateAcceptedAnswers([]string{" ", "x"}); err == nil {
		t.Fatal("empty accepted answer accepted")
	}
	tooMany := make([]string, 17)
	for index := range tooMany {
		tooMany[index] = uuid.NewString()
	}
	if err := validateAcceptedAnswers(tooMany); err == nil {
		t.Fatal("17 accepted answers accepted")
	}
}

func TestSubmissionTextPolicyDefaultsAndExplicitBounds(t *testing.T) {
	omitted := SubmissionPolicyInput{Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic}
	if err := validateSubmissionPolicy(omitted, TaskTypeFlag); err != nil {
		t.Fatalf("omitted text policy should default: %v", err)
	}
	defaults := SubmissionPolicyInput{
		Mode: SubmissionModeText, Checker: SubmissionCheckerAutomatic,
		HasTextPolicy: true, TextRequired: true, TextMinLength: 1, TextMaxLength: maxTaskTextLength,
	}
	if err := validateSubmissionPolicy(defaults, TaskTypeFlag); err != nil {
		t.Fatalf("default text bounds should validate: %v", err)
	}
	custom := defaults
	custom.TextMinLength = 5
	if err := validateSubmissionPolicy(custom, TaskTypeFlag); err != nil {
		t.Fatalf("persisted custom text bounds should validate: %v", err)
	}
	optionalText := defaults
	optionalText.TextRequired = false
	if err := validateSubmissionPolicy(optionalText, TaskTypeFlag); err == nil {
		t.Fatal("optional FLAG text policy accepted")
	}
}

func TestMergeBoardPresenceClearsNullableDates(t *testing.T) {
	start := time.Now().UTC()
	end := start.Add(time.Hour)
	current := &Board{Slug: "slug", Name: "Name", StartsAt: &start, EndsAt: &end}
	cleared := OptionalTime{Set: true, Value: nil}
	merged, err := mergeBoard(current, UpdateBoardInput{StartsAt: cleared, EndsAt: cleared})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.StartsAt != nil || merged.EndsAt != nil {
		t.Fatalf("nullable dates not cleared: %+v %+v", merged.StartsAt, merged.EndsAt)
	}
	if merged.Name != current.Name {
		t.Fatal("absent name changed during PATCH")
	}
}

func TestMergeModuleRemovedType(t *testing.T) {
	current := &Module{Name: "M", AllowedTaskTypes: []string{TaskTypeFlag, TaskTypeManual}, Enabled: true}
	merged, removed, err := mergeModule(current, UpdateModuleInput{
		AllowedTaskTypes: OptionalStrings{Set: true, Value: []string{TaskTypeFlag}},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(removed) != 1 || removed[0] != TaskTypeManual {
		t.Fatalf("removed = %v, want [MANUAL]", removed)
	}
	if len(merged.AllowedTaskTypes) != 1 || merged.AllowedTaskTypes[0] != TaskTypeFlag {
		t.Fatalf("merged allowed types = %v", merged.AllowedTaskTypes)
	}
}

func TestIdempotencyRequestHashIsKeyedAndDomainSeparated(t *testing.T) {
	service := NewService(nil, nil, SystemClock{}, nil,
		WithArgon2Config(config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}),
		WithIdempotencySecret("test-secret"))
	principal := authorization.Principal{UserID: uuid.New()}
	path := TaskPath{BoardID: uuid.New(), ModuleID: uuid.New(), TaskID: uuid.New()}

	first, err := service.requestHash(principal, path, "key-000000000001", "flag{low-entropy}")
	if err != nil {
		t.Fatalf("request hash: %v", err)
	}
	second, err := service.requestHash(principal, path, "key-000000000001", "flag{low-entropy}")
	if err != nil {
		t.Fatalf("request hash repeat: %v", err)
	}
	if !constantTimeEqual(first, second) {
		t.Fatal("request hash is not deterministic")
	}
	unkeyed := sha256.Sum256([]byte("flag{low-entropy}"))
	if len(first) == len(unkeyed) && constantTimeEqual(first, unkeyed[:]) {
		t.Fatal("request hash equals an unkeyed SHA-256 of the flag")
	}
	different, err := service.requestHash(principal, path, "key-000000000002", "flag{low-entropy}")
	if err != nil {
		t.Fatalf("different hash: %v", err)
	}
	if constantTimeEqual(first, different) {
		t.Fatal("different key/body produced the same request hash")
	}
}

func TestVerifyFlagChecksEveryHash(t *testing.T) {
	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	service := NewService(nil, nil, SystemClock{}, nil, WithArgon2Config(argon))
	hashes, err := service.hashAnswers([]string{"flag{one}", "flag{two}", "flag{three}"})
	if err != nil {
		t.Fatalf("hash answers: %v", err)
	}
	for _, answer := range []string{"flag{one}", "flag{three}"} {
		matched, err := service.verifyFlag(answer, hashes)
		if err != nil || !matched {
			t.Fatalf("verify %q = %v, %v; want true", answer, matched, err)
		}
	}
	matched, err := service.verifyFlag("flag{nope}", hashes)
	if err != nil {
		t.Fatalf("verify wrong: %v", err)
	}
	if matched {
		t.Fatal("wrong flag matched")
	}
}
