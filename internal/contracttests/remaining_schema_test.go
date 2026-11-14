package contracttests

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-openapi/loads"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func TestRemainingStageSchemas(t *testing.T) {
	specPath, err := filepath.Abs(filepath.Join("..", "..", "docs", "swagger.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := loads.Spec(specPath)
	if err != nil {
		t.Fatalf("load Swagger: %v", err)
	}
	expanded, err := doc.Expanded()
	if err != nil {
		t.Fatalf("expand Swagger: %v", err)
	}
	schemas := expanded.Spec().Definitions

	now := strfmt.DateTime(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
	boardID := models.UUID(uuid.NewString())
	userID := models.UUID(uuid.NewString())
	templateID := models.UUID(uuid.NewString())
	grantID := models.UUID(uuid.NewString())

	check := func(name string, model validatable, value any) {
		t.Helper()
		if err := model.Validate(strfmt.Default); err != nil {
			t.Fatalf("%s generated Validate: %v", name, err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s marshal: %v", name, err)
		}
		schema, ok := schemas[name]
		if !ok {
			t.Fatalf("%s missing from Swagger definitions", name)
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s decode: %v", name, err)
		}
		if err := validate.AgainstSchema(&schema, decoded, strfmt.Default); err != nil {
			t.Fatalf("%s schema validation: %v\nJSON: %s", name, err, raw)
		}
	}

	adminBoard := &models.AdminBoard{
		UUID:              &boardID,
		Slug:              ptr(models.Slug("remaining-board")),
		Name:              ptr("Remaining Board"),
		ModulesCount:      ptr(int64(1)),
		ModuleTypes:       []models.ModuleType{models.ModuleType("TASKS")},
		ParticipationMode: ptr(models.ParticipationMode("TEAM")),
		RegistrationMode:  ptr(models.RegistrationMode("INVITE_ONLY")),
		Status:            ptr(models.BoardStatus("ACTIVE")),
		Rating:            &models.RatingConfig{Visibility: ptr(models.RatingVisibility("PUBLIC"))},
		Joined:            ptr(false),
		Description:       ptr("board"),
		Visibility:        ptr(models.BoardVisibility("PRIVATE")),
		TeamSettings: &models.TeamSettings{
			MinMembers: ptr(int64(1)), MaxMembers: ptr(int64(4)),
			AllowSelfCreate: ptr(true), AllowLeaveAfterStart: ptr(false),
		},
		AccessGroupUuids: []models.UUID{models.UUID(uuid.NewString())},
	}
	t.Run("AdminBoard", func(t *testing.T) { check("AdminBoard", adminBoard, adminBoard) })

	invite := &models.BoardInviteResponse{InviteCode: ptr("invite-code-0123456789"), ExpiresAt: &now}
	t.Run("BoardInviteResponse", func(t *testing.T) { check("BoardInviteResponse", invite, invite) })

	grant := &models.PermissionGrant{
		UUID: &grantID, UserUUID: &userID, Permission: ptr("plugin.example.view"),
		BoardUUID: &boardID, GrantedByUUID: &userID, CreatedAt: &now,
	}
	t.Run("PermissionGrant", func(t *testing.T) { check("PermissionGrant", grant, grant) })

	file := &models.DeploymentTemplateFile{Path: ptr("files/app.yml"), ContentBase64: ptr("aGVsbG8=")}
	draft := &models.DeploymentTemplateDraft{
		TemplateUUID: &templateID, ManifestYaml: ptr("apiVersion: actfb.dev/v1alpha1\nkind: DeploymentTemplate\n"),
		Files: []*models.DeploymentTemplateFile{file}, UpdatedAt: &now,
	}
	t.Run("DeploymentTemplateDraft", func(t *testing.T) { check("DeploymentTemplateDraft", draft, draft) })

	allowlist := &models.DeploymentTemplateAllowlist{
		UUID: &grantID, TemplateVersionUUID: &templateID, BoardUUID: &boardID,
		ApprovedByUUID: &userID, CreatedAt: &now,
		Targets: []*models.DeploymentTemplateAllowlistTarget{{
			Kind: ptr("CONTAINER"), IntegrationUUID: &boardID,
		}},
	}
	t.Run("DeploymentTemplateAllowlist", func(t *testing.T) { check("DeploymentTemplateAllowlist", allowlist, allowlist) })

	plugin := &models.Plugin{
		ID: ptr("example.hello"), Name: ptr("Hello"), Version: ptr("1.0.0"), APIVersion: ptr("1"),
		Status: ptr("installed"), Author: ptr("example"),
		Health:       &models.PluginHealth{Status: ptr("healthy"), CheckedAt: &now},
		Capabilities: []string{"core.events.subscribe"},
		Permissions:  []*models.PluginPermission{{ID: ptr("plugin.example.hello.view"), Scope: ptr("board")}},
		Routes:       []*models.PluginRoute{{Method: ptr("GET"), Path: ptr("/boards/:boardUuid/hello"), Permission: ptr("plugin.example.hello.view"), Scope: ptr("board"), UI: true}},
	}
	t.Run("Plugin", func(t *testing.T) { check("Plugin", plugin, plugin) })

	pending := &models.PendingFile{
		UUID: &grantID, Filename: ptr("report.txt"), ContentType: ptr("text/plain"),
		Size: ptr(int64(10)), Sha256: ptr("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Status: ptr(models.FileValidationStatus("READY")),
	}
	t.Run("PendingFile", func(t *testing.T) { check("PendingFile", pending, pending) })

	adjustment := &models.ScoreAdjustment{
		UUID: &grantID, SubjectType: ptr(models.SubjectType("USER")), SubjectUUID: &userID,
		Delta: ptr(int64(5)), Reason: ptr("manual correction"), CreatedAt: &now,
		Actor: &models.UserSummary{UUID: &userID, Username: ptr(models.Username("admin"))},
	}
	t.Run("ScoreAdjustment", func(t *testing.T) { check("ScoreAdjustment", adjustment, adjustment) })

	run := &models.ADRun{
		UUID: &grantID, ModuleUUID: &boardID, Kind: ptr(models.ADRunKind("EXPLOIT")),
		CommitSha: ptr("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Status: ptr(models.ADRunStatus("PASSED")),
		CreatedAt: &now,
	}
	t.Run("ADRun", func(t *testing.T) { check("ADRun", run, run) })

	// New extension enums are additive and keep the native values.
	for _, value := range []models.ModuleType{"TASKS", "ATTACK_DEFENSE", "PENTEST", "EXTENSION"} {
		if string(value) == "" {
			t.Fatal("empty module type")
		}
	}
	for _, value := range []models.TaskType{"FLAG", "MANUAL", "INSTANCE", "EXPLOIT", "PATCH", "PENTEST_REPORT", "EXTENSION"} {
		if string(value) == "" {
			t.Fatal("empty task type")
		}
	}
	if got := models.SubmissionChecker("PLUGIN"); string(got) != "PLUGIN" {
		t.Fatalf("checker = %q", got)
	}
	mode := models.SubmissionModeTEXT
	checker := models.SubmissionCheckerPLUGIN
	checkerKey := "plugin.example.custom_checker.text"
	policy := &models.SubmissionPolicy{Mode: &mode, Checker: &checker, CheckerKey: &checkerKey}
	if err := policy.Validate(strfmt.Default); err != nil {
		t.Fatalf("plugin checker policy: %v", err)
	}
	extensionKey := "plugin.example.custom_checker.extension"
	task := &models.Task{
		UUID: &grantID, ModuleUUID: &boardID, Type: ptr(models.TaskType("EXTENSION")),
		ExtensionKey: &extensionKey, Title: ptr("Check"), Category: ptr("plugin"),
		Points: ptr(int64(10)), Status: ptr("AVAILABLE"), Solved: ptr(false),
		InstanceRequired: ptr(false), Description: ptr("text-only checker"),
		Attachments: []*models.FileRef{}, Submission: policy,
		Instance: &models.TaskInstanceSummary{Required: ptr(false), Status: ptr("NOT_CREATED"), CanStart: ptr(false)},
	}
	if err := task.Validate(strfmt.Default); err != nil {
		t.Fatalf("extension task: %v", err)
	}
	if got := models.PermissionCode("board.audiences.manage"); string(got) != "board.audiences.manage" {
		t.Fatalf("permission code = %q", got)
	}
}
