package boards

import "testing"

func TestValidatePluginModuleAndTask(t *testing.T) {
	board := Board{Status: BoardStatusActive}
	module := Module{
		Name: "Plugin", Type: ModuleTypeExtension, ExtensionKey: "plugin.example.custom_checker.extension",
		AllowedTaskTypes: []string{TaskTypeExtension},
	}
	if err := validateModuleInput(module, board); err != nil {
		t.Fatalf("valid extension module: %v", err)
	}
	withoutKey := module
	withoutKey.ExtensionKey = ""
	if err := validateModuleInput(withoutKey, board); err == nil {
		t.Fatal("EXTENSION module without extensionKey must be rejected")
	}
	nativeWithKey := module
	nativeWithKey.Type = ModuleTypeTasks
	nativeWithKey.AllowedTaskTypes = []string{TaskTypeFlag}
	if err := validateModuleInput(nativeWithKey, board); err == nil {
		t.Fatal("native module with extensionKey must be rejected")
	}

	task := Task{
		Title: "Check", Category: "plugin", Type: TaskTypeExtension, Points: 10, Visible: true,
		SubmissionMode: SubmissionModeText, SubmissionChecker: SubmissionCheckerPlugin,
		ExtensionKey: module.ExtensionKey, CheckerKey: "plugin.example.custom_checker.text",
	}
	if err := validateTaskInput(task, module); err != nil {
		t.Fatalf("valid checker task: %v", err)
	}
	missingChecker := task
	missingChecker.CheckerKey = ""
	if err := validateTaskInput(missingChecker, module); err == nil {
		t.Fatal("PLUGIN task without checkerKey must be rejected")
	}
	wrongMode := task
	wrongMode.SubmissionMode = SubmissionModeFiles
	if err := validateTaskInput(wrongMode, module); err == nil {
		t.Fatal("plugin checker must be text only")
	}
	officialChecker := task
	officialChecker.SubmissionChecker = SubmissionCheckerAutomatic
	if err := validateTaskInput(officialChecker, module); err == nil {
		t.Fatal("EXTENSION task must use PLUGIN checker")
	}
}
