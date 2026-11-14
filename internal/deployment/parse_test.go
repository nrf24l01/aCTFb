package deployment

import (
	"strings"
	"testing"
)

const validDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func sampleManifest() string {
	return `apiVersion: actfb.dev/v1alpha1
kind: DeploymentTemplate
metadata:
  name: Web security lab
  slug: web-security-lab
  description: Isolated lab
parameters:
  - key: difficulty
    label: Difficulty
    type: choice
    options: [easy, normal]
    default: easy
  - key: enabled
    label: Enabled
    type: bool
    default: true
  - key: replicas
    label: Replicas
    type: int
    default: 2
  - key: db_password
    label: Database password
    type: string
    required: true
    secret: true
generated:
  - key: lab_password
    kind: password
    length: 24
    rotateOnReset: true
nodes:
  - key: web
    name: Vulnerable website
    kind: CONTAINER
    source:
      type: OCI_IMAGE
      ref: registry.example/ctf/web@` + validDigest + `
    resources:
      cpuMillicores: 500
      memoryMiB: 512
      diskMiB: 1024
    env:
      - name: LAB_PASSWORD
        valueFrom: { generated: lab_password }
      - name: DIFFICULTY
        valueFrom: { parameter: difficulty }
      - name: STATIC
        valueFrom: { literal: hello }
    files:
      - source: files/app-config.yml.tmpl
        target: /etc/lab/config.yml
        mode: '0440'
    endpoints:
      - key: web-http
        protocol: HTTP
        targetPort: 8080
        exposure: PUBLIC
        display: { label: Web challenge }
    driverOptions:
      docker:
        init: true
      kubernetes:
        workload: Deployment
checks:
  - kind: tcp
    node: web
    endpoint: web-http
lifecycle:
  reset: RECREATE
`
}

func sampleFiles() map[string][]byte {
	return map[string][]byte{
		"files/app-config.yml.tmpl": []byte("difficulty: {{.difficulty}}\n"),
	}
}

func parseSample(t *testing.T) *Template {
	t.Helper()
	template, findings := ParseManifest([]byte(sampleManifest()))
	if len(findings) > 0 {
		t.Fatalf("unexpected parse findings: %#v", findings)
	}
	if template == nil {
		t.Fatal("expected template")
	}
	return template
}

func TestParseStrictKnownFieldsAndCanonicalDefaults(t *testing.T) {
	template := parseSample(t)
	if template.Parameters[0].Default != "easy" {
		t.Fatalf("choice default = %q", template.Parameters[0].Default)
	}
	if template.Parameters[1].Default != "true" {
		t.Fatalf("bool default = %q", template.Parameters[1].Default)
	}
	if template.Parameters[2].Default != "2" {
		t.Fatalf("int default = %q", template.Parameters[2].Default)
	}
	if got := template.Nodes[0].Endpoints[0].Label; got != "Web challenge" {
		t.Fatalf("display.label = %q", got)
	}
	if _, ok := template.Nodes[0].DriverOptions[DriverDocker]; !ok {
		t.Fatal("docker options not retained as raw JSON")
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	manifest := strings.Replace(sampleManifest(), "  description: Isolated lab", "  description: Isolated lab\n  surprise: true", 1)
	_, findings := ParseManifest([]byte(manifest))
	requireFindingCode(t, findings, "YAML_UNKNOWN_FIELD")
}

func TestParseRejectsUnknownNestedField(t *testing.T) {
	manifest := strings.Replace(sampleManifest(), "      diskMiB: 1024", "      diskMiB: 1024\n      gpu: 1", 1)
	_, findings := ParseManifest([]byte(manifest))
	requireFindingCode(t, findings, "YAML_UNKNOWN_FIELD")
}

func TestParseRejectsDuplicateKeys(t *testing.T) {
	manifest := strings.Replace(sampleManifest(), "  name: Web security lab", "  name: Web security lab\n  name: Duplicate", 1)
	_, findings := ParseManifest([]byte(manifest))
	requireFindingCode(t, findings, "YAML_DUPLICATE_KEY")
}

func TestParseRejectsWrongKind(t *testing.T) {
	manifest := strings.Replace(sampleManifest(), "kind: DeploymentTemplate", "kind: 123", 1)
	_, findings := ParseManifest([]byte(manifest))
	requireFindingCode(t, findings, "YAML_WRONG_TYPE")
}

func TestParseRejectsWrongSourceTypeShape(t *testing.T) {
	manifest := strings.Replace(sampleManifest(), "      type: OCI_IMAGE", "      type: 12", 1)
	_, findings := ParseManifest([]byte(manifest))
	requireFindingCode(t, findings, "YAML_WRONG_TYPE")
}

func TestParseRejectsAliasesAndMultipleDocuments(t *testing.T) {
	_, findings := ParseManifest([]byte("apiVersion: &v actfb.dev/v1alpha1\nkind: DeploymentTemplate\nmetadata: *v\nnodes: []\n"))
	requireFindingCode(t, findings, "YAML_ALIAS_UNSUPPORTED")

	_, findings = ParseManifest([]byte("apiVersion: actfb.dev/v1alpha1\nkind: DeploymentTemplate\nmetadata: {name: x, slug: x}\nnodes: []\n---\napiVersion: actfb.dev/v1alpha1\n"))
	requireFindingCode(t, findings, "YAML_MULTIPLE_DOCUMENTS")
}

func TestParseManifestBound(t *testing.T) {
	_, findings := ParseManifest([]byte(strings.Repeat("a", MaxManifestBytes+1)))
	requireFindingCode(t, findings, "MANIFEST_TOO_LARGE")
}

func TestParseRejectsNonStringKeys(t *testing.T) {
	_, findings := ParseManifest([]byte("apiVersion: actfb.dev/v1alpha1\nkind: DeploymentTemplate\nmetadata: {name: x, slug: x}\nnodes: []\n1: true\n"))
	requireFindingCode(t, findings, "YAML_KEY_INVALID")
}

func requireFindingCode(t *testing.T, findings []Finding, code string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Code == code && finding.Severity == SeverityError {
			return
		}
	}
	t.Fatalf("missing finding %s in %#v", code, findings)
}
