package http

import (
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func TestPluginCheckerMappings(t *testing.T) {
	module := boards.Module{
		ID: uuid.New(), BoardID: uuid.New(), Name: "Plugin", Type: boards.ModuleTypeExtension,
		ExtensionKey: "plugin.example.custom_checker.extension", AllowedTaskTypes: []string{boards.TaskTypeExtension},
		Enabled: true, Status: boards.ModuleStatusActive,
	}
	moduleModelValue := moduleModel(module)
	if moduleModelValue.ExtensionKey == nil || *moduleModelValue.ExtensionKey != module.ExtensionKey {
		t.Fatalf("module extensionKey not mapped: %#v", moduleModelValue.ExtensionKey)
	}

	task := boards.Task{
		ID: uuid.New(), ModuleID: module.ID, Title: "Check", Category: "plugin", Description: "desc",
		Type: boards.TaskTypeExtension, Points: 50, Visible: true,
		SubmissionMode: boards.SubmissionModeText, SubmissionChecker: boards.SubmissionCheckerPlugin,
		ExtensionKey: module.ExtensionKey, CheckerKey: "plugin.example.custom_checker.text",
		Status: boards.TaskStatusAvailable,
	}
	taskModelValue := taskModel(task)
	if taskModelValue.ExtensionKey == nil || *taskModelValue.ExtensionKey != module.ExtensionKey {
		t.Fatalf("task extensionKey not mapped: %#v", taskModelValue.ExtensionKey)
	}
	if taskModelValue.Submission == nil || taskModelValue.Submission.CheckerKey == nil ||
		*taskModelValue.Submission.CheckerKey != task.CheckerKey {
		t.Fatalf("task checkerKey not mapped: %#v", taskModelValue.Submission)
	}

	mode := oapimodels.SubmissionModeTEXT
	checker := oapimodels.SubmissionCheckerPLUGIN
	checkerKey := task.CheckerKey
	input := submissionPolicyInput(&oapimodels.SubmissionPolicy{
		Mode: &mode, Checker: &checker, CheckerKey: &checkerKey,
	}, nil)
	if input.CheckerKey != task.CheckerKey || input.Checker != boards.SubmissionCheckerPlugin {
		t.Fatalf("submission policy mapping = %#v", input)
	}
}
