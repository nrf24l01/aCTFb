package contracttests

import (
	"path/filepath"
	"testing"

	"github.com/go-openapi/loads"
	"os"

	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"gopkg.in/yaml.v3"
)

// TestOrganizerUXContract pins the contract decisions the organizer UX depends
// on: the public landing is documented as unauthenticated, board list/detail
// allow anonymous reads, the picker scope is an explicit enum, and the approved
// template parameter projection is a closed schema.
func TestOrganizerUXContract(t *testing.T) {
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
	spec := expanded.Spec()

	// 1. The public reads must carry an EXPLICIT empty security override. The
	// expanded spec cannot distinguish "absent" from "empty", so inspect the
	// source document: a missing override silently inherits the global bearer
	// requirement, which is exactly the documentation defect this guards.
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	paths, _ := document["paths"].(map[string]any)
	for _, path := range []string{"/site", "/boards", "/boards/{boardUuid}"} {
		entry, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("%s missing from the contract", path)
		}
		get, ok := entry["get"].(map[string]any)
		if !ok {
			t.Fatalf("GET %s missing from the contract", path)
		}
		security, present := get["security"]
		if !present {
			t.Fatalf("GET %s has no explicit security override; it would inherit the global bearer requirement", path)
		}
		list, ok := security.([]any)
		if !ok || len(list) != 0 {
			t.Fatalf("GET %s security = %v, want an explicit empty override", path, security)
		}
	}

	// 3. The board user search exposes an explicit scope enum, defaulting to the
	//    safest value.
	search, ok := spec.Paths.Paths["/admin/boards/{boardUuid}/user-search"]
	if !ok || search.Get == nil {
		t.Fatal("GET /admin/boards/{boardUuid}/user-search missing from the contract")
	}
	var scopeParam *struct {
		Name    string
		Default any
		Enum    []any
	}
	for _, parameter := range search.Get.Parameters {
		if parameter.Name == "scope" {
			enum := make([]any, 0, len(parameter.Enum))
			enum = append(enum, parameter.Enum...)
			scopeParam = &struct {
				Name    string
				Default any
				Enum    []any
			}{Name: parameter.Name, Default: parameter.Default, Enum: enum}
		}
	}
	if scopeParam == nil {
		t.Fatal("board user search has no scope parameter")
	}
	if scopeParam.Default != "PARTICIPANTS" {
		t.Fatalf("scope default = %v, want PARTICIPANTS", scopeParam.Default)
	}
	if len(scopeParam.Enum) != 2 {
		t.Fatalf("scope enum = %v, want PARTICIPANTS and DIRECTORY", scopeParam.Enum)
	}

	// 4. The approved template parameter projection is closed and validates.
	schema, ok := spec.Definitions["ApprovedTemplateParameter"]
	if !ok {
		t.Fatal("ApprovedTemplateParameter missing from the contract")
	}
	sample := map[string]any{
		"key": "flag", "label": "Flag", "type": "string", "required": true, "secret": true,
	}
	if err := validate.AgainstSchema(&schema, sample, strfmt.Default); err != nil {
		t.Fatalf("ApprovedTemplateParameter schema rejected a valid definition: %v", err)
	}
	// A secret default must not be required to exist, and an unknown type fails.
	invalid := map[string]any{
		"key": "flag", "label": "Flag", "type": "provider-credential", "required": true, "secret": true,
	}
	if err := validate.AgainstSchema(&schema, invalid, strfmt.Default); err == nil {
		t.Fatal("ApprovedTemplateParameter accepted an out-of-enum type")
	}
}

// TestApprovedTemplateParameterTypeEnumMatchesManifest pins the closed enum to
// exactly the types the manifest validator accepts. A published template that
// declares `type: text` is projected verbatim, so an enum that omits it would
// emit a value outside its own documented schema.
func TestApprovedTemplateParameterTypeEnumMatchesManifest(t *testing.T) {
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
	schema, ok := expanded.Spec().Definitions["ApprovedTemplateParameter"]
	if !ok {
		t.Fatal("ApprovedTemplateParameter missing from the contract")
	}
	property, ok := schema.Properties["type"]
	if !ok {
		t.Fatal("ApprovedTemplateParameter.type missing")
	}

	// The authoritative manifest set.
	manifestTypes := []string{"string", "text", "int", "bool", "choice"}
	declared := make(map[string]struct{}, len(property.Enum))
	for _, value := range property.Enum {
		name, _ := value.(string)
		declared[name] = struct{}{}
	}
	for _, name := range manifestTypes {
		if _, present := declared[name]; !present {
			t.Fatalf("type enum %v is missing the manifest type %q", property.Enum, name)
		}
	}
	if len(declared) != len(manifestTypes) {
		t.Fatalf("type enum %v must be exactly the manifest set %v", property.Enum, manifestTypes)
	}

	// Every manifest type must validate, and an unknown type must not.
	for _, name := range manifestTypes {
		sample := map[string]any{
			"key": "p", "label": "P", "type": name, "required": false, "secret": false,
		}
		if err := validate.AgainstSchema(&schema, sample, strfmt.Default); err != nil {
			t.Fatalf("type %q rejected by the contract: %v", name, err)
		}
	}
	unknown := map[string]any{
		"key": "p", "label": "P", "type": "provider-credential", "required": false, "secret": false,
	}
	if err := validate.AgainstSchema(&schema, unknown, strfmt.Default); err == nil {
		t.Fatal("the contract accepted an unknown parameter type")
	}
}
