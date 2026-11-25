package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func TestBoardMeIndividualOmitsTeamZeroObject(t *testing.T) {
	model := boardMeModel(&boards.BoardMe{
		Joined: true, ParticipationMode: boards.ParticipationIndividual,
		Audiences: []boards.AudienceSummary{},
	})
	encoded, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal BoardMe: %v", err)
	}
	if strings.Contains(string(encoded), `"team"`) {
		t.Fatalf("INDIVIDUAL BoardMe emitted team: %s", encoded)
	}
}

func TestTaskInstanceSummaryReflectsEnabledTask(t *testing.T) {
	task := boards.Task{
		ID:       uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		ModuleID: uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		Title:    "Instance", Category: "web", Description: "description", Type: boards.TaskTypeInstance,
		Points: 10, Visible: true, SubmissionMode: boards.SubmissionModeText,
		SubmissionChecker: boards.SubmissionCheckerManual, InstanceEnabled: true,
		Status: boards.TaskStatusAvailable, CanParticipate: true,
	}
	model := taskModel(task)
	if model.Instance == nil || model.Instance.Required == nil || !*model.Instance.Required {
		t.Fatalf("instance.required not mapped: %#v", model.Instance)
	}
	if model.Instance.CanStart == nil || !*model.Instance.CanStart {
		t.Fatalf("instance.canStart not mapped: %#v", model.Instance)
	}
	task.Status = boards.TaskStatusSolved
	if solved := taskModel(task); solved.Instance == nil || solved.Instance.CanStart == nil || *solved.Instance.CanStart {
		t.Fatalf("solved task must not advertise canStart: %#v", solved.Instance)
	}
	// A staff preview (or an ineligible reader) must never advertise a startable
	// instance, even when the task itself is instance-enabled and available.
	previewTask := task
	previewTask.Preview = true
	previewTask.CanParticipate = false
	if preview := taskModel(previewTask); preview.Instance == nil || preview.Instance.CanStart == nil ||
		*preview.Instance.CanStart {
		t.Fatalf("preview task advertised canStart: %#v", preview.Instance)
	}

	flag := taskModel(boards.Task{
		ID:       uuid.MustParse("33333333-3333-4333-8333-333333333333"),
		ModuleID: uuid.MustParse("44444444-4444-4444-8444-444444444444"),
		Title:    "Flag", Category: "web", Description: "description", Type: boards.TaskTypeFlag,
		Points: 10, Visible: true, SubmissionMode: boards.SubmissionModeText,
		SubmissionChecker: boards.SubmissionCheckerAutomatic, Status: boards.TaskStatusAvailable,
	})
	if flag.Instance == nil || flag.Instance.Required == nil || *flag.Instance.Required ||
		flag.Instance.CanStart == nil || *flag.Instance.CanStart {
		t.Fatalf("FLAG task advertised an instance: %#v", flag.Instance)
	}
}

func TestTaskTextPolicyOmitsFilesZeroObject(t *testing.T) {
	model := taskModel(boards.Task{
		ID:       uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		ModuleID: uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		Title:    "Task", Category: "web", Description: "description", Type: boards.TaskTypeFlag,
		Points: 10, Visible: true, SubmissionMode: boards.SubmissionModeText,
		SubmissionChecker: boards.SubmissionCheckerAutomatic, Status: boards.TaskStatusAvailable,
	})
	encoded, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal Task: %v", err)
	}
	payload := string(encoded)
	if strings.Contains(payload, `"files"`) {
		t.Fatalf("TEXT Task emitted files zero object: %s", payload)
	}
	if !strings.Contains(payload, `"text":{"maxLength":1000000,"minLength":1,"required":true}`) {
		t.Fatalf("TEXT Task missing default text policy: %s", payload)
	}
}

func TestAdminModuleOmitsNilInstanceSettings(t *testing.T) {
	model := adminModuleModel(boards.Module{
		ID:      uuid.MustParse("33333333-3333-4333-8333-333333333333"),
		BoardID: uuid.MustParse("44444444-4444-4444-8444-444444444444"),
		Name:    "Tasks", Description: "description", Type: boards.ModuleTypeTasks,
		AllowedTaskTypes: []string{boards.TaskTypeFlag}, Enabled: true,
		Status: boards.ModuleStatusActive,
	})
	encoded, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal AdminModule: %v", err)
	}
	if strings.Contains(string(encoded), `"taskInstanceSettings"`) {
		t.Fatalf("nil taskInstanceSettings emitted: %s", encoded)
	}
}

func TestCreateFlagSubmissionPolicyNilTextIsSafeAndValid(t *testing.T) {
	body := []byte(`{
		"title":"Task","type":"FLAG","category":"web","description":"description",
		"points":10,"visible":true,
		"submission":{"mode":"TEXT","checker":"AUTOMATIC"},
		"instance":{"enabled":false}
	}`)
	var request oapimodels.AdminTaskCreateRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if problem := validateGenerated(&request); problem != nil {
		t.Fatalf("generated validation rejected omitted text policy: %+v", problem)
	}
	policy := submissionPolicyInput(request.Submission, nil)
	if policy.HasTextPolicy {
		t.Fatal("omitted text policy marked present")
	}
}

func TestCreateFlagSubmissionPolicyWithDefaultTextBoundsValidates(t *testing.T) {
	body := []byte(`{
		"title":"Task","type":"FLAG","category":"web","description":"description",
		"points":10,"visible":true,
		"submission":{"mode":"TEXT","checker":"AUTOMATIC","text":{"required":true,"minLength":1,"maxLength":1000000}},
		"instance":{"enabled":false}
	}`)
	var request oapimodels.AdminTaskCreateRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if problem := validateGenerated(&request); problem != nil {
		t.Fatalf("generated validation rejected default text bounds: %+v", problem)
	}
	policy := submissionPolicyInput(request.Submission, nil)
	if !policy.HasTextPolicy || !policy.TextRequired || policy.TextMinLength != 1 || policy.TextMaxLength != 1_000_000 {
		t.Fatalf("text policy = %+v", policy)
	}
}
