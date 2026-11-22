package boards

import (
	"testing"

	"github.com/google/uuid"
)

func TestBoardAccessModeProjection(t *testing.T) {
	cases := []struct {
		name  string
		board Board
		want  string
	}{
		{"public open", Board{Visibility: VisibilityPublic, RegistrationMode: RegistrationOpen}, AccessModePublic},
		{"private groups", Board{Visibility: VisibilityPrivate, RegistrationMode: RegistrationOpen, AccessGroupUUIDs: []uuid.UUID{uuid.New()}}, AccessModeAudience},
		{"invite", Board{Visibility: VisibilityPrivate, RegistrationMode: RegistrationInviteOnly}, AccessModeInvite},
		{"private open no groups", Board{Visibility: VisibilityPrivate, RegistrationMode: RegistrationOpen}, AccessModeLegacy},
		{"public invite", Board{Visibility: VisibilityPublic, RegistrationMode: RegistrationInviteOnly}, AccessModeLegacy},
		{"closed", Board{Visibility: VisibilityPrivate, RegistrationMode: RegistrationClosed}, AccessModeLegacy},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.board.AccessMode(); got != testCase.want {
				t.Fatalf("AccessMode() = %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestNewModuleFormatCardinality(t *testing.T) {
	cases := []struct {
		name    string
		module  Module
		wantErr bool
	}{
		{"single flag", Module{Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag}}, false},
		{"mixed tasks", Module{Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeFlag, TaskTypeManual}}, true},
		{"ad both", Module{Type: ModuleTypeAttackDefense, AllowedTaskTypes: []string{TaskTypeExploit, TaskTypePatch}}, false},
		{"ad one", Module{Type: ModuleTypeAttackDefense, AllowedTaskTypes: []string{TaskTypeExploit}}, true},
		{"pentest", Module{Type: ModuleTypePentest, AllowedTaskTypes: []string{TaskTypePentestReport}}, false},
		{"pentest wrong", Module{Type: ModuleTypePentest, AllowedTaskTypes: []string{TaskTypeFlag}}, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateNewModuleFormat(testCase.module)
			if testCase.wantErr && err == nil {
				t.Fatal("expected rejection")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
		})
	}
}

func TestApplyAccessModeNormalizesLegacyFields(t *testing.T) {
	board := Board{Visibility: VisibilityPrivate, RegistrationMode: RegistrationClosed}
	if err := applyAccessMode(&board, AccessModePublic); err != nil {
		t.Fatalf("public: %v", err)
	}
	if board.Visibility != VisibilityPublic || board.RegistrationMode != RegistrationOpen || len(board.AccessGroupUUIDs) != 0 {
		t.Fatalf("public normalization = %#v", board)
	}

	board = Board{}
	if err := applyAccessMode(&board, AccessModeAudience); err == nil {
		t.Fatal("audience without groups must be rejected")
	}
	board.AccessGroupUUIDs = []uuid.UUID{uuid.New()}
	if err := applyAccessMode(&board, AccessModeAudience); err != nil {
		t.Fatalf("audience with groups: %v", err)
	}
	if board.Visibility != VisibilityPrivate || board.RegistrationMode != RegistrationOpen {
		t.Fatalf("audience normalization = %#v", board)
	}

	board = Board{AccessGroupUUIDs: []uuid.UUID{uuid.New()}}
	if err := applyAccessMode(&board, AccessModeInvite); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if board.Visibility != VisibilityPrivate || board.RegistrationMode != RegistrationInviteOnly || len(board.AccessGroupUUIDs) != 0 {
		t.Fatalf("invite normalization = %#v", board)
	}
}

func TestValidateAccessModeConflict(t *testing.T) {
	public := VisibilityPublic
	private := VisibilityPrivate
	open := RegistrationOpen
	invite := RegistrationInviteOnly
	if err := validateAccessModeConflict(AccessModePublic, &private, &open, false, true); err == nil {
		t.Fatal("public mode with private visibility must conflict")
	}
	if err := validateAccessModeConflict(AccessModePublic, &public, &open, false, true); err != nil {
		t.Fatalf("consistent public: %v", err)
	}
	if err := validateAccessModeConflict(AccessModeInvite, &private, &invite, false, true); err != nil {
		t.Fatalf("consistent invite: %v", err)
	}
	if err := validateAccessModeConflict(AccessModeAudience, &private, &open, true, false); err != nil {
		t.Fatalf("consistent audience: %v", err)
	}
	if err := validateAccessModeConflict(AccessModeAudience, &private, &open, true, true); err == nil {
		t.Fatal("audience without groups must conflict")
	}
}

func TestManualOnlyModuleIsPublishable(t *testing.T) {
	// A single-format MANUAL module must count as a publishable task module.
	module := Module{Type: ModuleTypeTasks, AllowedTaskTypes: []string{TaskTypeManual}, Enabled: true}
	if err := validateNewModuleFormat(module); err != nil {
		t.Fatalf("manual-only module rejected: %v", err)
	}
	if !containsString(module.AllowedTaskTypes, TaskTypeManual) {
		t.Fatal("manual task type missing")
	}
}

func TestBoardAccessModeLegacyCombinationsStayLegacy(t *testing.T) {
	group := uuid.New()
	cases := []struct {
		name  string
		board Board
	}{
		{"private invite with groups keeps the group gate", Board{Visibility: VisibilityPrivate, RegistrationMode: RegistrationInviteOnly, AccessGroupUUIDs: []uuid.UUID{group}}},
		{"private invite with projected group count", Board{Visibility: VisibilityPrivate, RegistrationMode: RegistrationInviteOnly, AccessGroupCount: 1}},
		{"private closed", Board{Visibility: VisibilityPrivate, RegistrationMode: RegistrationClosed}},
		{"private open without groups", Board{Visibility: VisibilityPrivate, RegistrationMode: RegistrationOpen}},
		{"public invite", Board{Visibility: VisibilityPublic, RegistrationMode: RegistrationInviteOnly}},
		{"public with groups", Board{Visibility: VisibilityPublic, RegistrationMode: RegistrationOpen, AccessGroupUUIDs: []uuid.UUID{group}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.board.AccessMode(); got != AccessModeLegacy {
				t.Fatalf("AccessMode() = %s, want LEGACY", got)
			}
		})
	}
}

func TestSameUUIDSetIgnoresOrderAndDuplicates(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if !sameUUIDSet([]uuid.UUID{a, b}, []uuid.UUID{b, a}) {
		t.Fatal("same set in a different order must compare equal")
	}
	if !sameUUIDSet([]uuid.UUID{a, a, b}, []uuid.UUID{a, b}) {
		t.Fatal("duplicates must be collapsed")
	}
	if sameUUIDSet([]uuid.UUID{a}, []uuid.UUID{a, b}) {
		t.Fatal("a larger set must differ")
	}
	if !sameUUIDSet(nil, []uuid.UUID{}) {
		t.Fatal("nil and empty must compare equal")
	}
	if sameUUIDSet(nil, []uuid.UUID{a}) {
		t.Fatal("empty must differ from a populated set")
	}
}
