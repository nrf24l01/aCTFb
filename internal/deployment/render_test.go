package deployment

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderStrictParametersAndCancellation(t *testing.T) {
	template := parseSample(t)
	files := map[string][]byte{
		"files/app-config.yml.tmpl": []byte("difficulty: {{.difficulty}}\npassword: {{.db_password}}\n"),
	}
	rendered, err := Render(context.Background(), RenderInput{
		Template:   *template,
		Files:      files,
		Parameters: map[string]string{"difficulty": "easy", "db_password": "s3cret", "enabled": "true", "replicas": "2"},
		Generated:  map[string]string{"lab_password": "generated-secret"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := string(rendered.Nodes[0].Files[0].Content); !strings.Contains(got, "password: s3cret") {
		t.Fatalf("rendered file = %q", got)
	}

	_, err = Render(context.Background(), RenderInput{
		Template:   *template,
		Files:      files,
		Parameters: map[string]string{"difficulty": "easy", "enabled": "true", "replicas": "2"},
		Generated:  map[string]string{"lab_password": "generated-secret"},
	})
	requireValidationFinding(t, err, "TEMPLATE_PARAMETER_MISSING")

	_, err = Render(context.Background(), RenderInput{
		Template:   *template,
		Files:      files,
		Parameters: map[string]string{"difficulty": "easy", "db_password": "s3cret", "enabled": "true", "replicas": "2", "unknown": "x"},
		Generated:  map[string]string{"lab_password": "generated-secret"},
	})
	requireValidationFinding(t, err, "TEMPLATE_PARAMETER_UNKNOWN")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Render(ctx, RenderInput{
		Template:   *template,
		Files:      files,
		Parameters: map[string]string{"difficulty": "easy", "db_password": "s3cret", "enabled": "true", "replicas": "2"},
		Generated:  map[string]string{"lab_password": "generated-secret"},
	})
	if err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestRenderNoEnvironmentFunctionsAndOutputLimit(t *testing.T) {
	template := baseTemplateWithNodes(Node{
		Key: "web", Name: "Web", Kind: KindContainer,
		Source:    Source{Type: SourceOCIImage, Ref: "registry.example/x@" + validDigest},
		Resources: Resources{CPUMillicores: 100, MemoryMiB: 128, DiskMiB: 100},
		Files:     []FileSpec{{Source: "file.tmpl", Target: "/tmp/file", Mode: "0440"}},
	})
	_, err := Render(context.Background(), RenderInput{
		Template: *template,
		Files:    map[string][]byte{"file.tmpl": []byte(`{{env "HOME"}}`)},
	})
	requireValidationFinding(t, err, "TEMPLATE_SYNTAX_INVALID")

	_, err = Render(context.Background(), RenderInput{
		Template: *template,
		Files:    map[string][]byte{"file.tmpl": []byte(`{{printf "%02000000d" 1}}`)},
	})
	requireValidationFinding(t, err, "TEMPLATE_RENDER_FAILED")
}

func TestBuildPreviewRedactsSecretsAndDoesNotRenderInfrastructure(t *testing.T) {
	template := parseSample(t)
	provider := fullContainerProvider()
	preview, result, err := BuildPreview(context.Background(), template, sampleFiles(), provider)
	if err != nil {
		t.Fatalf("BuildPreview: %v", err)
	}
	if !result.Valid {
		t.Fatalf("preview invalid: %#v", result.Findings)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"s3cret", "generated-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("preview leaked %q: %s", secret, encoded)
		}
	}
	if strings.Contains(string(encoded), "difficulty: easy") {
		t.Fatalf("preview leaked rendered file content: %s", encoded)
	}
	if provider.applyCalls != 0 {
		t.Fatalf("preview applied infrastructure: %d", provider.applyCalls)
	}
}

func TestMetadataProjectionHidesFilesAndSecretDefaults(t *testing.T) {
	version := TemplateVersion{
		Version: 1, Digest: "sha256:abc",
		Manifest: Template{Metadata: Metadata{Name: "Lab", Slug: "lab"}, Parameters: []Parameter{
			{Key: "public", Label: "Public", Type: "string", Default: "ok"},
			{Key: "secret", Label: "Secret", Type: "string", Secret: true, Default: ""},
		}},
		Files: map[string][]byte{"secret.txt": []byte("do-not-leak")},
	}
	metadata := MetadataForVersion(version)
	encoded, _ := json.Marshal(metadata)
	if strings.Contains(string(encoded), "do-not-leak") || strings.Contains(string(encoded), "secret.txt") {
		t.Fatalf("metadata leaked files: %s", encoded)
	}
	if metadata.Parameters[1].Default != nil {
		t.Fatal("secret parameter default must not be projected")
	}
}

func TestComputeDigestStableAndDiffMasksValues(t *testing.T) {
	first := map[string][]byte{"a": []byte("1"), "b": []byte("2")}
	second := map[string][]byte{"b": []byte("2"), "a": []byte("1")}
	if ComputeDigest([]byte("manifest"), first) != ComputeDigest([]byte("manifest"), second) {
		t.Fatal("digest must not depend on map order")
	}
	if ComputeDigest([]byte("manifest"), first) == ComputeDigest([]byte("manifest"), map[string][]byte{"a": []byte("1")}) {
		t.Fatal("digest must include file set")
	}

	from := TemplateVersion{Version: 1, Manifest: Template{
		Parameters: []Parameter{{Key: "api_token", Label: "Token", Type: "string", Secret: true, Default: ""}},
		Nodes: []Node{{Key: "web", Name: "Web", Kind: KindContainer,
			Source:    Source{Type: SourceOCIImage, Ref: "registry/x@" + validDigest},
			Resources: Resources{CPUMillicores: 100, MemoryMiB: 128, DiskMiB: 100}}},
	}, Files: map[string][]byte{"secret.txt": []byte("one")}}
	to := TemplateVersion{Version: 2, Manifest: Template{
		Parameters: []Parameter{{Key: "api_token", Label: "Token", Type: "string", Secret: true, Default: ""}},
		Nodes: []Node{{Key: "web", Name: "Web", Kind: KindContainer,
			Source:    Source{Type: SourceOCIImage, Ref: "registry/x@" + validDigest},
			Resources: Resources{CPUMillicores: 100, MemoryMiB: 128, DiskMiB: 100}}},
	}, Files: map[string][]byte{"secret.txt": []byte("two-secret")}}
	diff := DiffVersions(from, to)
	encoded, _ := json.Marshal(diff)
	if strings.Contains(string(encoded), "one") || strings.Contains(string(encoded), "two-secret") {
		t.Fatalf("diff leaked file contents: %s", encoded)
	}
	foundFile := false
	for _, change := range diff.Changes {
		if change.Kind == DiffKindFile && change.Change == DiffChanged {
			foundFile = true
		}
	}
	if !foundFile {
		t.Fatalf("expected file change: %#v", diff.Changes)
	}
}

func requireValidationFinding(t *testing.T, err error, code string) {
	t.Helper()
	validation, ok := AsValidationError(err)
	if !ok {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	for _, finding := range validation.Findings {
		if finding.Code == code {
			return
		}
	}
	t.Fatalf("missing finding %s in %#v", code, validation.Findings)
}
