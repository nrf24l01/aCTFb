package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-openapi/loads"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/users"
)

type fakeAuth struct{ user *users.User }

func (f fakeAuth) AuthenticateAccessToken(context.Context, string) (*users.User, error) {
	return f.user, nil
}

type fakeService struct {
	templateID uuid.UUID
	manifest   string
	draft      *deployment.Draft
	published  *deployment.TemplateVersion
}

func newFakeService() *fakeService {
	templateID := uuid.New()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	return &fakeService{
		templateID: templateID,
		manifest:   "apiVersion: actfb.dev/v1alpha1\nkind: DeploymentTemplate\nmetadata: {name: Lab, slug: lab}\nnodes: []\n",
		draft: &deployment.Draft{
			TemplateUUID: templateID,
			ManifestYAML: []byte("apiVersion: actfb.dev/v1alpha1\nkind: DeploymentTemplate\nmetadata: {name: Lab, slug: lab}\nnodes: []\n"),
			Files:        map[string][]byte{},
			UpdatedAt:    now,
		},
		published: &deployment.TemplateVersion{
			UUID: uuid.New(), TemplateID: templateID, Version: 1, Digest: "sha256:abc",
			PublishedAt: now, PublishedBy: uuid.New(),
		},
	}
}

func (f *fakeService) CreateTemplate(_ context.Context, _ authorization.Principal, input deployment.CreateInput) (*deployment.TemplateRecord, error) {
	if len(input.ManifestYAML) > 0 {
		f.manifest = string(input.ManifestYAML)
	}
	return &deployment.TemplateRecord{
		UUID: f.templateID, Slug: "lab", Name: "Lab", Description: "",
		CreatedAt: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
	}, nil
}

func (f *fakeService) ListTemplates(context.Context, authorization.Principal, int, int) ([]deployment.TemplateRecord, int64, error) {
	record, _ := f.CreateTemplate(context.Background(), authorization.Principal{}, deployment.CreateInput{})
	return []deployment.TemplateRecord{*record}, 1, nil
}

func (f *fakeService) GetTemplate(context.Context, authorization.Principal, uuid.UUID) (*deployment.TemplateRecord, error) {
	return f.CreateTemplate(context.Background(), authorization.Principal{}, deployment.CreateInput{})
}

func (f *fakeService) GetDraft(context.Context, authorization.Principal, uuid.UUID) (*deployment.Draft, error) {
	return f.draft, nil
}

func (f *fakeService) SaveDraft(_ context.Context, _ authorization.Principal, _ uuid.UUID, manifest []byte, files []deployment.FileInput) (*deployment.Draft, error) {
	f.draft.ManifestYAML = manifest
	f.draft.Files = map[string][]byte{}
	for _, file := range files {
		f.draft.Files[file.Path] = []byte(file.ContentBase64)
	}
	return f.draft, nil
}

func (f *fakeService) ValidateDraft(context.Context, authorization.Principal, uuid.UUID) (*deployment.ValidationResult, error) {
	return &deployment.ValidationResult{Valid: true, Findings: []deployment.Finding{}}, nil
}

func (f *fakeService) PreviewDraft(context.Context, authorization.Principal, uuid.UUID) (*deployment.Preview, *deployment.ValidationResult, error) {
	return &deployment.Preview{
		Nodes: []deployment.PreviewNode{{
			Key: "web", Name: "Web", Kind: deployment.KindContainer, Action: "CREATE",
			Findings: []deployment.Finding{},
		}},
		Endpoints: []deployment.PreviewEndpoint{{
			Key: "http", NodeKey: "web", Protocol: "HTTP", Exposure: deployment.ExposurePublic,
		}},
		Warnings: []string{},
	}, &deployment.ValidationResult{Valid: true, Findings: []deployment.Finding{}}, nil
}

func (f *fakeService) Publish(context.Context, authorization.Principal, uuid.UUID) (*deployment.TemplateVersion, error) {
	return f.published, nil
}

func (f *fakeService) ListVersions(context.Context, authorization.Principal, uuid.UUID, int, int) ([]deployment.TemplateVersion, int64, error) {
	return []deployment.TemplateVersion{*f.published}, 1, nil
}

func (f *fakeService) GetVersion(context.Context, authorization.Principal, uuid.UUID, int) (*deployment.TemplateVersion, error) {
	return f.published, nil
}

func (f *fakeService) DiffVersions(context.Context, authorization.Principal, uuid.UUID, int, int) (*deployment.Diff, error) {
	before, after := "sha256:one", "sha256:two"
	return &deployment.Diff{FromVersion: 1, ToVersion: 2, Changes: []deployment.DiffEntry{{
		Kind: deployment.DiffKindFile, Path: "files/app.yml", Change: deployment.DiffChanged,
		Before: &before, After: &after,
	}}}, nil
}

func TestHandlerRoutesAndCanonicalSchemas(t *testing.T) {
	service := newFakeService()
	auth := fakeAuth{user: &users.User{ID: uuid.New(), Roles: []string{"admin"}}}
	handler := NewHandler(service, auth)
	e := echo.New()
	handler.RegisterRoutes(e)

	base := "/api/v2/admin/deployment-templates"
	templatePath := base + "/" + service.templateID.String()

	specPath, err := filepath.Abs(filepath.Join("..", "..", "..", "docs", "swagger.yaml"))
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

	assertSchema := func(t *testing.T, schemaName string, body []byte) {
		t.Helper()
		schema, ok := schemas[schemaName]
		if !ok {
			t.Fatalf("missing Swagger definition %s", schemaName)
		}
		var decoded any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("%s response JSON: %v", schemaName, err)
		}
		if err := validate.AgainstSchema(&schema, decoded, strfmt.Default); err != nil {
			t.Fatalf("%s expanded schema validation: %v\n%s", schemaName, err, body)
		}
	}

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		status     int
		schemaName string
	}{
		{"list", http.MethodGet, base, "", http.StatusOK, "DeploymentTemplateListResponse"},
		{"create", http.MethodPost, base, `{"manifestYaml":"apiVersion: actfb.dev/v1alpha1\nkind: DeploymentTemplate\nmetadata: {name: Lab, slug: lab}\nnodes: []\n","files":[]}`, http.StatusCreated, "DeploymentTemplate"},
		{"get", http.MethodGet, templatePath, "", http.StatusOK, "DeploymentTemplate"},
		{"draft", http.MethodGet, templatePath + "/draft", "", http.StatusOK, "DeploymentTemplateDraft"},
		{"put", http.MethodPut, templatePath + "/draft", `{"manifestYaml":"apiVersion: actfb.dev/v1alpha1\nkind: DeploymentTemplate\nmetadata: {name: Lab, slug: lab}\nnodes: []\n","files":[{"path":"app.yml","contentBase64":"eA=="}]}`, http.StatusOK, "DeploymentTemplateDraft"},
		{"validate", http.MethodPost, templatePath + "/draft/validate", "", http.StatusOK, "DeploymentTemplateValidation"},
		{"preview", http.MethodPost, templatePath + "/draft/preview", "", http.StatusOK, "DeploymentTemplatePreview"},
		{"publish", http.MethodPost, templatePath + "/publish", `{"versionNote":"first"}`, http.StatusCreated, "DeploymentTemplateVersion"},
		{"versions", http.MethodGet, templatePath + "/versions", "", http.StatusOK, "DeploymentTemplateVersionListResponse"},
		{"version", http.MethodGet, templatePath + "/versions/1", "", http.StatusOK, "DeploymentTemplateVersion"},
		{"diff", http.MethodGet, templatePath + "/versions/2/diff?from=1", "", http.StatusOK, "DeploymentTemplateDiff"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(testCase.method, testCase.path, bytes.NewBufferString(testCase.body))
			request.Header.Set("Authorization", "Bearer token")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			e.ServeHTTP(response, request)
			if response.Code != testCase.status {
				t.Fatalf("status = %d want %d body=%s", response.Code, testCase.status, response.Body.String())
			}
			assertSchema(t, testCase.schemaName, response.Body.Bytes())
		})
	}
}

func TestHandlerRejectsUnauthorizedUnknownFieldsAndBadUUID(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(service, fakeAuth{user: &users.User{ID: uuid.New(), Roles: []string{"admin"}}})
	e := echo.New()
	handler.RegisterRoutes(e)
	base := "/api/v2/admin/deployment-templates"

	request := httptest.NewRequest(http.MethodGet, base, nil)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPut, base+"/"+service.templateID.String()+"/draft", bytes.NewBufferString(`{"manifestYaml":"x","files":[],"surprise":true}`))
	request.Header.Set("Authorization", "Bearer token")
	response = httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown field status = %d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, base+"/not-a-uuid", nil)
	request.Header.Set("Authorization", "Bearer token")
	response = httptest.NewRecorder()
	e.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad uuid status = %d body=%s", response.Code, response.Body.String())
	}
}
