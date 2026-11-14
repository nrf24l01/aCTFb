package integration

import (
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"github.com/google/uuid"
)

func TestP1NullableSwaggerNormalization(t *testing.T) {
	root := repositoryRoot(t)
	doc := p1LoadLiveSchema(t, root)
	definition, ok := doc.Spec().Definitions["BoardRoleBinding"]
	if !ok {
		t.Fatal("source Swagger is missing BoardRoleBinding")
	}
	if _, ok := definition.Properties["expiresAt"].Extensions["x-nullable"]; !ok {
		t.Fatal("source BoardRoleBinding.expiresAt is missing x-nullable:true")
	}
	if _, ok := definition.Properties["revokedAt"].Extensions["x-nullable"]; !ok {
		t.Fatal("source BoardRoleBinding.revokedAt is missing x-nullable:true")
	}
	if _, ok := definition.Properties["moduleUuid"].Extensions["x-nullable"]; !ok {
		t.Fatal("source BoardRoleBinding.moduleUuid is missing x-nullable:true")
	}

	normalized := p1NormalizeNullableSchema(definition)
	validateValue := func(value map[string]any) *validate.Result {
		validator := validate.NewSchemaValidator(&normalized, doc.Spec(), "", strfmt.Default)
		return validator.Validate(value)
	}

	base := func() map[string]any {
		return map[string]any{
			"uuid":       uuid.NewString(),
			"boardUuid":  uuid.NewString(),
			"moduleUuid": nil,
			"user": map[string]any{
				"uuid":     uuid.NewString(),
				"username": "normalized-user",
			},
			"role": "board_editor",
			"grantedBy": map[string]any{
				"uuid":     uuid.NewString(),
				"username": "normalized-admin",
			},
			"expiresAt": nil,
			"revokedAt": nil,
			"createdAt": time.Now().UTC().Format(time.RFC3339),
		}
	}

	if result := validateValue(base()); !result.IsValid() {
		t.Fatalf("null nullable fields were rejected: %v", result.Errors)
	}

	nonNull := base()
	nonNull["moduleUuid"] = uuid.NewString()
	nonNull["expiresAt"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	nonNull["revokedAt"] = time.Now().UTC().Format(time.RFC3339)
	if result := validateValue(nonNull); !result.IsValid() {
		t.Fatalf("non-null nullable fields with valid constraints were rejected: %v", result.Errors)
	}

	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "invalid UUID",
			mutate: func(value map[string]any) {
				value["moduleUuid"] = "not-a-uuid"
			},
		},
		{
			name: "invalid date",
			mutate: func(value map[string]any) {
				value["expiresAt"] = "not-a-date"
			},
		},
		{
			name: "invalid role enum",
			mutate: func(value map[string]any) {
				value["role"] = "admin"
			},
		},
		{
			name: "extra property",
			mutate: func(value map[string]any) {
				value["unexpected"] = true
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			value := base()
			testCase.mutate(value)
			if result := validateValue(value); result.IsValid() {
				t.Fatalf("invalid value passed validation: %v", value)
			}
		})
	}
}
