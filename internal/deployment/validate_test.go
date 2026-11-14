package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type fakeMetadataProvider struct {
	metadata            []DriverMetadata
	listCalls           int
	optionsCalls        int
	applyCalls          int
	invalidOptionPath   string
	invalidOptionCode   string
	invalidOptionSuffix string
}

func (p *fakeMetadataProvider) ListDriverMetadata(context.Context) ([]DriverMetadata, error) {
	p.listCalls++
	return p.metadata, nil
}

func (p *fakeMetadataProvider) ValidateDriverOptions(_ context.Context, driver string, node Node, options json.RawMessage) []Finding {
	p.optionsCalls++
	if len(options) == 0 || string(options) == "null" || !strings.HasPrefix(strings.TrimSpace(string(options)), "{") {
		return []Finding{errorFinding("", "DRIVER_OPTION_TYPE_INVALID", "driver options must be an object")}
	}
	if p.invalidOptionPath != "" {
		return []Finding{errorFinding(p.invalidOptionPath, p.invalidOptionCode, "invalid driver option")}
	}
	return nil
}

func fullContainerProvider() *fakeMetadataProvider {
	return &fakeMetadataProvider{metadata: []DriverMetadata{
		{Driver: DriverDocker, Kind: KindContainer, Capabilities: Capabilities{
			Kind: KindContainer, SourceTypes: []SourceType{SourceOCIImage},
			FileInjection: true, PublicExposure: true, MaxNodes: 20,
		}},
		{Driver: DriverKubernetes, Kind: KindContainer, Capabilities: Capabilities{
			Kind: KindContainer, SourceTypes: []SourceType{SourceOCIImage},
			FileInjection: true, PublicExposure: true, MaxNodes: 20,
		}},
		{Driver: DriverProxmox, Kind: KindVM, Capabilities: Capabilities{
			Kind: KindVM, SourceTypes: []SourceType{SourceVMTemplate, SourceVMImage},
			CloudInit: true, FileInjection: true, PublicExposure: true, SnapshotReset: true, MaxNodes: 20,
		}},
		{Driver: DriverKubeVirt, Kind: KindVM, Capabilities: Capabilities{
			Kind: KindVM, SourceTypes: []SourceType{SourceVMTemplate, SourceVMImage},
			CloudInit: true, FileInjection: true, PublicExposure: true, SnapshotReset: true, MaxNodes: 20,
		}},
	}}
}

func validateSample(t *testing.T, template *Template, files map[string][]byte, provider DriverMetadataProvider) ValidationResult {
	t.Helper()
	result, err := ValidateTemplate(context.Background(), template, files, provider)
	if err != nil {
		t.Fatalf("ValidateTemplate: %v", err)
	}
	return result
}

func TestValidateSampleValid(t *testing.T) {
	result := validateSample(t, parseSample(t), sampleFiles(), fullContainerProvider())
	if !result.Valid {
		t.Fatalf("expected valid, findings=%#v", result.Findings)
	}
}

func TestValidateMissingAndUnsafeFiles(t *testing.T) {
	template := parseSample(t)
	template.Nodes[0].Files[0].Source = "../escape"
	template.Nodes[0].Files[0].Target = "/etc/../shadow"
	template.Nodes[0].Files[0].Mode = "0999"
	result := validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "FILE_PATH_UNSAFE")
	requireFindingCode(t, result.Findings, "FILE_TARGET_UNSAFE")
	requireFindingCode(t, result.Findings, "FILE_MODE_INVALID")

	template = parseSample(t)
	template.Nodes[0].Files[0].Source = "missing.yaml"
	result = validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "FILE_NOT_FOUND")
}

func TestValidateResourceBoundaries(t *testing.T) {
	template := parseSample(t)
	template.Nodes[0].Resources = Resources{CPUMillicores: 0, MemoryMiB: MinMemoryMiB - 1, DiskMiB: MaxDiskMiB + 1}
	result := validateSample(t, template, sampleFiles(), fullContainerProvider())
	count := 0
	for _, finding := range result.Findings {
		if finding.Code == "RESOURCE_BOUNDS" {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("resource findings = %d, want 3: %#v", count, result.Findings)
	}

	template = parseSample(t)
	template.Nodes[0].Resources = Resources{CPUMillicores: MaxCPUMillicores, MemoryMiB: MinMemoryMiB, DiskMiB: MinDiskMiB}
	if result := validateSample(t, template, sampleFiles(), fullContainerProvider()); !result.Valid {
		t.Fatalf("boundary values should be valid: %#v", result.Findings)
	}
}

func TestValidateEnvOneOfAndSecretInlining(t *testing.T) {
	template := parseSample(t)
	empty := ""
	template.Nodes[0].Env = append(template.Nodes[0].Env, EnvVar{Name: "EMPTY", ValueFrom: EnvSource{Literal: &empty}})
	parameter := "difficulty"
	template.Nodes[0].Env = append(template.Nodes[0].Env, EnvVar{Name: "TWO", ValueFrom: EnvSource{Parameter: &parameter, Literal: &empty}})
	secret := "AKIAABCDEFGHIJKLMNOP"
	template.Nodes[0].Env = append(template.Nodes[0].Env, EnvVar{Name: "PROXMOX_TOKEN", ValueFrom: EnvSource{Literal: &secret}})
	result := validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "ENV_SOURCE_ONE_OF")
	requireFindingCode(t, result.Findings, "ENV_SECRET_INLINE")
	requireFindingCode(t, result.Findings, "ENV_CREDENTIAL_INLINE")
}

func TestValidateUnknownDriverOptionsAndWrongKind(t *testing.T) {
	template := parseSample(t)
	template.Nodes[0].DriverOptions["evil"] = json.RawMessage(`{}`)
	template.Nodes[0].DriverOptions[DriverProxmox] = json.RawMessage(`{"storage":"local"}`)
	template.Nodes[0].DriverOptions[DriverPodman] = json.RawMessage(`true`)
	result := validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "DRIVER_OPTION_UNKNOWN")
	requireFindingCode(t, result.Findings, "DRIVER_OPTION_TYPE_INVALID")
	requireFindingCodeAny(t, result.Findings, "DRIVER_OPTION_UNUSED")
}

func TestValidateDriverOptionCallbackPathAndNoApply(t *testing.T) {
	provider := fullContainerProvider()
	provider.invalidOptionPath = "init"
	provider.invalidOptionCode = "DRIVER_OPTION_INVALID"
	result := validateSample(t, parseSample(t), sampleFiles(), provider)
	requireFindingCode(t, result.Findings, "DRIVER_OPTION_INVALID")
	pathFound := false
	for _, finding := range result.Findings {
		if finding.Code == "DRIVER_OPTION_INVALID" && finding.Path == "nodes[0].driverOptions.docker.init" {
			pathFound = true
		}
	}
	if !pathFound {
		t.Fatalf("callback path was not namespaced: %#v", result.Findings)
	}
	if provider.optionsCalls == 0 {
		t.Fatal("expected metadata option validation callback")
	}
	if provider.applyCalls != 0 {
		t.Fatalf("preview/validate must never apply, applyCalls=%d", provider.applyCalls)
	}
}

func TestValidateRejectsInlineSecretsInDriverOptions(t *testing.T) {
	template := parseSample(t)
	template.Nodes[0].DriverOptions[DriverDocker] = json.RawMessage(`{"password":"hunter2"}`)
	result := validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "DRIVER_OPTION_SECRET_INLINE")
}

func TestValidateRejectsCredentialsInDefaultsAndFiles(t *testing.T) {
	template := parseSample(t)
	template.Parameters[0].Default = "AKIAABCDEFGHIJKLMNOP"
	result := validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "PARAMETER_CREDENTIAL_INLINE")

	result = validateSample(t, parseSample(t), map[string][]byte{
		"files/app-config.yml.tmpl": []byte("key: -----BEGIN PRIVATE KEY-----\nabc\n"),
	}, fullContainerProvider())
	requireFindingCode(t, result.Findings, "FILE_CREDENTIAL_INLINE")
}

func TestValidateSourcePolicies(t *testing.T) {
	template := parseSample(t)
	template.Nodes[0].Source.Ref = "registry.example/ctf/web@sha256:" + strings.Repeat("0", 64)
	result := validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "SOURCE_REF_PLACEHOLDER")

	template = parseSample(t)
	template.Nodes[0].Source.Ref = "registry.example/ctf/web"
	result = validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "SOURCE_REF_NOT_PINNED")

	vm := vmNode()
	vm.Source.Ref = "12345"
	template = baseTemplateWithNodes(vm)
	result = validateSample(t, template, map[string][]byte{}, fullContainerProvider())
	requireFindingCode(t, result.Findings, "SOURCE_REF_NATIVE_VMID")
}

func TestValidateMixedKindRejectedDeterministically(t *testing.T) {
	template := baseTemplateWithNodes(parseSample(t).Nodes[0], vmNode())
	result := validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "MIXED_KIND_UNSUPPORTED")
	found := false
	for _, finding := range result.Findings {
		if finding.Code == "MIXED_KIND_UNSUPPORTED" {
			found = true
			if finding.Path != "nodes" {
				t.Fatalf("mixed-kind finding path = %q", finding.Path)
			}
		}
	}
	if !found {
		t.Fatalf("mixed-kind finding missing: %#v", result.Findings)
	}
}

func TestValidateCapabilityGates(t *testing.T) {
	vm := vmNode()
	vm.Bootstrap = &Bootstrap{CloudInitFile: "cloud-init.yaml"}
	vm.Files = []FileSpec{{Source: "cloud-init.yaml", Target: "/etc/cloud.yaml", Mode: "0440"}}
	vm.Endpoints = []Endpoint{{Key: "ssh", Protocol: "SSH", TargetPort: 22, Exposure: ExposurePublic}}
	vm.DriverOptions = map[string]json.RawMessage{DriverProxmox: json.RawMessage(`{}`)}
	template := baseTemplateWithNodes(vm)
	provider := &fakeMetadataProvider{metadata: []DriverMetadata{{
		Driver: DriverProxmox, Kind: KindVM, Capabilities: Capabilities{
			Kind: KindVM, SourceTypes: []SourceType{SourceVMTemplate},
			CloudInit: false, FileInjection: false, PublicExposure: false,
		},
	}}}
	files := map[string][]byte{"cloud-init.yaml": []byte("#cloud-config\n")}
	result := validateSample(t, template, files, provider)
	requireFindingCode(t, result.Findings, "BOOTSTRAP_CAPABILITY_UNSUPPORTED")
	requireFindingCode(t, result.Findings, "FILE_CAPABILITY_UNSUPPORTED")
	requireFindingCode(t, result.Findings, "PUBLIC_EXPOSURE_CAPABILITY_UNSUPPORTED")

	template.Lifecycle.Reset = ResetSnapshot
	result = validateSample(t, template, files, provider)
	requireFindingCode(t, result.Findings, "SNAPSHOT_CAPABILITY_UNSUPPORTED")
}

func TestValidateNoMetadataIsWarningNotSilent(t *testing.T) {
	result := validateSample(t, parseSample(t), sampleFiles(), nil)
	if !result.Valid {
		t.Fatalf("template should remain valid with explicit warning, findings=%#v", result.Findings)
	}
	requireFindingCodeAny(t, result.Findings, "DRIVER_METADATA_UNAVAILABLE")
}

func TestValidateParameterGeneratedAndNodeBounds(t *testing.T) {
	template := parseSample(t)
	template.Generated[0].Length = 4
	result := validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "GENERATED_LENGTH_INVALID")

	template = parseSample(t)
	for index := 0; index < MaxParameters; index++ {
		template.Parameters = append(template.Parameters, Parameter{Key: fmt.Sprintf("p_%d", index), Label: "x", Type: "string"})
	}
	result = validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "PARAMETER_LIMIT_EXCEEDED")

	template = parseSample(t)
	for index := 0; index < MaxNodes; index++ {
		node := parseSample(t).Nodes[0]
		node.Key = fmt.Sprintf("node-%d", index)
		template.Nodes = append(template.Nodes, node)
	}
	result = validateSample(t, template, sampleFiles(), fullContainerProvider())
	requireFindingCode(t, result.Findings, "NODE_LIMIT_EXCEEDED")
}

func requireFindingCodeAny(t *testing.T, findings []Finding, code string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Code == code {
			return
		}
	}
	t.Fatalf("missing finding %s in %#v", code, findings)
}

func vmNode() Node {
	return Node{
		Key: "workstation", Name: "Workstation", Kind: KindVM,
		Source:    Source{Type: SourceVMTemplate, Ref: "ubuntu-workstation-lab"},
		Resources: Resources{CPUMillicores: 2000, MemoryMiB: 2048, DiskMiB: 10240},
	}
}

func baseTemplateWithNodes(nodes ...Node) *Template {
	return &Template{
		APIVersion: APIVersion, Kind: KindName,
		Metadata:  Metadata{Name: "Lab", Slug: "lab", Description: ""},
		Nodes:     nodes,
		Lifecycle: Lifecycle{Reset: ResetRecreate},
	}
}
