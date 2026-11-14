package http

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func TestSixActualMappersProduceValidCanonicalJSON(t *testing.T) {
	boardID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	moduleID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	taskID := uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	userID := uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	now := time.Now().UTC()

	board := boards.Board{
		ID: boardID, Slug: "valid-slug", Name: "Board", Description: "description",
		ParticipationMode: boards.ParticipationIndividual, RegistrationMode: boards.RegistrationOpen,
		Visibility: boards.VisibilityPrivate, Status: boards.BoardStatusActive,
		RatingVisibility: boards.RatingPublic, ModulesCount: 1,
		ModuleTypes: []string{boards.ModuleTypeTasks}, Joined: true,
	}
	module := boards.Module{
		ID: moduleID, BoardID: boardID, Name: "Tasks", Description: "description",
		Type: boards.ModuleTypeTasks, AllowedTaskTypes: []string{boards.TaskTypeFlag},
		Status: boards.ModuleStatusActive, Enabled: true, SortOrder: 0,
	}
	task := boards.Task{
		ID: taskID, BoardID: boardID, ModuleID: moduleID, Title: "Task", Category: "web",
		Description: "description", Type: boards.TaskTypeFlag, Points: 100, Visible: true,
		SubmissionMode: boards.SubmissionModeText, SubmissionChecker: boards.SubmissionCheckerAutomatic,
		Status: boards.TaskStatusAvailable, AcceptedAnswersCount: 2,
	}
	submission := boards.Submission{
		ID: uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"), BoardID: boardID,
		ModuleID: moduleID, TaskID: taskID, UserID: userID, Username: "player",
		Status: boards.SubmissionStatusAccepted, CreatedAt: now,
	}

	boardJSON := assertValidModel(t, boardModel(board))
	requireJSONKeys(t, boardJSON, `"visibility":"PRIVATE"`, `"description":"description"`, `"joined":true`)

	moduleJSON := assertValidModel(t, moduleModel(module))
	requireJSONKeys(t, moduleJSON, `"description":"description"`, `"boardUuid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"`)
	if strings.Contains(moduleJSON, `"taskInstanceSettings"`) {
		t.Fatalf("Module emitted admin-only instance settings: %s", moduleJSON)
	}

	taskJSON := assertValidModel(t, taskModel(task))
	requireJSONKeys(t, taskJSON, `"description":"description"`, `"submission":{"checker":"AUTOMATIC","mode":"TEXT","text":{"maxLength":1000000,"minLength":1,"required":true}}`)
	if strings.Contains(taskJSON, `"files"`) {
		t.Fatalf("TEXT Task emitted files zero object: %s", taskJSON)
	}

	adminModuleJSON := assertValidModel(t, adminModuleModel(module))
	requireJSONKeys(t, adminModuleJSON, `"enabled":true`)
	if strings.Contains(adminModuleJSON, `"taskInstanceSettings"`) {
		t.Fatalf("nil taskInstanceSettings emitted: %s", adminModuleJSON)
	}

	adminTaskJSON := assertValidModel(t, adminTaskModel(task))
	requireJSONKeys(t, adminTaskJSON, `"answerConfiguration":{"acceptedAnswersCount":2,"configured":true}`, `"instanceConfig":{"enabled":false}`)
	if strings.Contains(adminTaskJSON, `{"UUID"`) || strings.Contains(adminTaskJSON, `"nodes":null`) || strings.Contains(adminTaskJSON, `"providerIntegrationUuid"`) {
		t.Fatalf("AdminTask instanceConfig leaked zero/invalid fields: %s", adminTaskJSON)
	}

	adminSubmissionJSON := assertValidModel(t, adminSubmissionModel(submission))
	requireJSONKeys(t, adminSubmissionJSON, `"resourceType":"TASK"`, `"taskUuid":"cccccccc-cccc-4ccc-8ccc-cccccccccccc"`)
	if strings.Contains(adminSubmissionJSON, `"text":`) && !strings.Contains(adminSubmissionJSON, `"text":null`) {
		t.Fatalf("AdminSubmission leaked text: %s", adminSubmissionJSON)
	}
}

func assertValidModel(t *testing.T, model interface {
	Validate(strfmt.Registry) error
}) string {
	t.Helper()
	if err := model.Validate(strfmt.Default); err != nil {
		t.Fatalf("generated validation failed: %v", err)
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal generated model: %v", err)
	}
	return string(encoded)
}

func requireJSONKeys(t *testing.T, payload string, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if !strings.Contains(payload, key) {
			t.Fatalf("response %s is missing %s", payload, key)
		}
	}
}

var _ = oapimodels.Board{}
var _ = boards.ParticipationIndividual
var _ = strfmt.Default
