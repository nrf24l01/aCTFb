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

type validatable interface {
	Validate(strfmt.Registry) error
}

func ptr[T any](v T) *T { return &v }

func TestP1CanonicalResponseJSON(t *testing.T) {
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

	now := strfmt.DateTime(time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC))
	boardID := models.UUID(uuid.NewString())
	moduleID := models.UUID(uuid.NewString())
	taskID := models.UUID(uuid.NewString())
	userID := models.UUID(uuid.NewString())

	validateCase := func(t *testing.T, name string, model validatable, value any) {
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
			t.Fatalf("%s decode JSON: %v", name, err)
		}
		if err := validate.AgainstSchema(&schema, decoded, strfmt.Default); err != nil {
			t.Fatalf("%s expanded schema validation: %v\nJSON: %s", name, err, raw)
		}
	}

	board := &models.Board{
		UUID:              &boardID,
		Slug:              ptr(models.Slug("p1-board")),
		Name:              ptr("P1 Board"),
		ModulesCount:      ptr(int64(1)),
		ModuleTypes:       []models.ModuleType{models.ModuleType("TASKS")},
		ParticipationMode: ptr(models.ParticipationMode("INDIVIDUAL")),
		RegistrationMode:  ptr(models.RegistrationMode("OPEN")),
		Status:            ptr(models.BoardStatus("ACTIVE")),
		Rating:            &models.RatingConfig{Visibility: ptr(models.RatingVisibility("PUBLIC"))},
		Joined:            ptr(true),
		Description:       ptr("description"),
		Visibility:        ptr(models.BoardVisibility("PRIVATE")),
	}
	t.Run("Board", func(t *testing.T) { validateCase(t, "Board", board, board) })

	module := &models.Module{
		UUID:             &moduleID,
		BoardUUID:        &boardID,
		Name:             ptr("TASKS"),
		Type:             ptr(models.ModuleType("TASKS")),
		AllowedTaskTypes: []models.TaskType{models.TaskType("FLAG")},
		Status:           ptr(models.ModuleStatus("ACTIVE")),
		AudienceUuids:    []models.UUID{},
		Order:            ptr(int64(0)),
		Description:      ptr("description"),
	}
	t.Run("Module", func(t *testing.T) { validateCase(t, "Module", module, module) })

	submissionPolicy := &models.SubmissionPolicy{
		Mode:    ptr(models.SubmissionMode("TEXT")),
		Checker: ptr(models.SubmissionChecker("AUTOMATIC")),
		Text: &models.SubmissionTextPolicy{
			Required:  ptr(true),
			MinLength: ptr(int64(1)),
			MaxLength: ptr(int64(1024)),
		},
	}
	instance := &models.TaskInstanceSummary{
		Required: ptr(false),
		Status:   ptr("NOT_CREATED"),
		CanStart: ptr(false),
	}
	task := &models.Task{
		UUID:             &taskID,
		ModuleUUID:       &moduleID,
		Type:             ptr(models.TaskType("FLAG")),
		Title:            ptr("Flag"),
		Category:         ptr("web"),
		Points:           ptr(int64(100)),
		Status:           ptr("AVAILABLE"),
		Solved:           ptr(false),
		InstanceRequired: ptr(false),
		Description:      ptr("description"),
		Attachments:      []*models.FileRef{},
		Submission:       submissionPolicy,
		Instance:         instance,
	}
	t.Run("Task", func(t *testing.T) { validateCase(t, "Task", task, task) })

	adminModule := &models.AdminModule{
		UUID:             &moduleID,
		BoardUUID:        &boardID,
		Name:             ptr("TASKS"),
		Type:             ptr(models.ModuleType("TASKS")),
		AllowedTaskTypes: []models.TaskType{models.TaskType("FLAG")},
		Status:           ptr(models.ModuleStatus("ACTIVE")),
		AudienceUuids:    []models.UUID{},
		Order:            ptr(int64(0)),
		Description:      ptr("description"),
		Enabled:          ptr(true),
	}
	t.Run("AdminModule", func(t *testing.T) { validateCase(t, "AdminModule", adminModule, adminModule) })

	adminTask := &models.AdminTask{
		UUID:             &taskID,
		ModuleUUID:       &moduleID,
		Type:             ptr(models.TaskType("FLAG")),
		Title:            ptr("Flag"),
		Category:         ptr("web"),
		Points:           ptr(int64(100)),
		Status:           ptr("AVAILABLE"),
		Solved:           ptr(false),
		InstanceRequired: ptr(false),
		Description:      ptr("description"),
		Attachments:      []*models.FileRef{},
		Submission:       submissionPolicy,
		Instance:         instance,
		Visible:          ptr(true),
		AnswerConfiguration: &models.AdminTaskAnswerConfiguration{
			Configured:           ptr(true),
			AcceptedAnswersCount: ptr(int64(1)),
		},
		InstanceConfig: &models.TaskInstanceConfig{Enabled: ptr(false)},
	}
	t.Run("AdminTask", func(t *testing.T) { validateCase(t, "AdminTask", adminTask, adminTask) })

	adminSubmission := &models.AdminSubmission{
		UUID:         ptr(models.UUID(uuid.NewString())),
		Status:       ptr(models.SubmissionStatus("ACCEPTED")),
		CreatedAt:    &now,
		Files:        []*models.FileRef{},
		ScoreAwarded: ptr(int64(100)),
		ResourceType: ptr(models.SubmissionResourceType("TASK")),
		BoardUUID:    &boardID,
		ModuleUUID:   &moduleID,
		TaskUUID:     &taskID,
		Subject: &models.ScoreSubject{
			Type: ptr(models.SubjectType("USER")),
			UUID: &userID,
			Name: ptr("alice"),
		},
	}
	t.Run("AdminSubmission", func(t *testing.T) { validateCase(t, "AdminSubmission", adminSubmission, adminSubmission) })
}
