package contracttests

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-openapi/loads"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

// TestTaskProgressContract pins the additive student progress contract: a
// required nonnegative solved/total pair, an optional taskProgress field on the
// three response models, and omission (not null) when no own progress exists.
func TestTaskProgressContract(t *testing.T) {
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
	progressSchema, ok := schemas["TaskProgress"]
	if !ok {
		t.Fatal("TaskProgress missing from Swagger definitions")
	}

	validateValue := func(t *testing.T, name string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s marshal: %v", name, err)
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s decode: %v", name, err)
		}
		if err := validate.AgainstSchema(&progressSchema, decoded, strfmt.Default); err != nil {
			t.Fatalf("%s schema validation: %v", name, err)
		}
	}

	zero := &models.TaskProgress{SolvedTasks: ptr(int64(0)), TotalTasks: ptr(int64(0))}
	if err := zero.Validate(strfmt.Default); err != nil {
		t.Fatalf("zero progress Validate: %v", err)
	}
	validateValue(t, "zero", zero)
	full := &models.TaskProgress{SolvedTasks: ptr(int64(3)), TotalTasks: ptr(int64(8))}
	if err := full.Validate(strfmt.Default); err != nil {
		t.Fatalf("full progress Validate: %v", err)
	}
	validateValue(t, "full", full)
	negative := &models.TaskProgress{SolvedTasks: ptr(int64(-1)), TotalTasks: ptr(int64(2))}
	if err := negative.Validate(strfmt.Default); err == nil {
		t.Fatal("negative solvedTasks passed generated validation")
	}
	if err := validate.AgainstSchema(&progressSchema, map[string]any{"solvedTasks": -1, "totalTasks": 2},
		strfmt.Default); err == nil {
		t.Fatal("negative solvedTasks passed expanded schema validation")
	}

	boardID := models.UUID(uuid.NewString())
	moduleID := models.UUID(uuid.NewString())
	module := &models.Module{
		UUID: &moduleID, BoardUUID: &boardID, Name: ptr("Tasks"),
		Type: ptr(models.ModuleType("TASKS")), AllowedTaskTypes: []models.TaskType{models.TaskType("FLAG")},
		Status: ptr(models.ModuleStatus("ACTIVE")), AudienceUuids: []models.UUID{}, Order: ptr(int64(0)),
		Description: ptr(""), Preview: ptr(false), CanParticipate: ptr(true), TaskProgress: full,
	}
	item := &models.ModuleListItem{
		UUID: &moduleID, BoardUUID: &boardID, Name: ptr("Tasks"),
		Type: ptr(models.ModuleType("TASKS")), AllowedTaskTypes: []models.TaskType{models.TaskType("FLAG")},
		Status: ptr(models.ModuleStatus("ACTIVE")), AudienceUuids: []models.UUID{}, Order: ptr(int64(0)),
		Preview: ptr(false), CanParticipate: ptr(true), TaskProgress: zero,
	}
	me := &models.BoardMe{
		Joined: ptr(true), ParticipationMode: ptr(models.ParticipationMode("INDIVIDUAL")),
		Audiences: []*models.AudienceSummary{}, Preview: ptr(false), CanParticipate: ptr(true),
		TaskProgress: full,
	}
	for name, value := range map[string]any{"Module": module, "ModuleListItem": item, "BoardMe": me} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s marshal: %v", name, err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s decode: %v", name, err)
		}
		if _, present := decoded["taskProgress"]; !present {
			t.Fatalf("%s did not serialize taskProgress", name)
		}
		schema, ok := schemas[name]
		if !ok {
			t.Fatalf("%s missing from Swagger definitions", name)
		}
		if err := validate.AgainstSchema(&schema, decoded, strfmt.Default); err != nil {
			t.Fatalf("%s expanded schema validation: %v", name, err)
		}
	}

	// Nil progress must serialize without the key (omitted, never null).
	omitted := &models.BoardMe{
		Joined: ptr(true), ParticipationMode: ptr(models.ParticipationMode("INDIVIDUAL")),
		Audiences: []*models.AudienceSummary{}, Preview: ptr(true), CanParticipate: ptr(false),
	}
	raw, err := json.Marshal(omitted)
	if err != nil {
		t.Fatalf("omitted marshal: %v", err)
	}
	if strings.Contains(string(raw), "taskProgress") {
		t.Fatalf("nil progress serialized as a field: %s", raw)
	}
}
