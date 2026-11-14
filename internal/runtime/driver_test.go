package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
)

type fakeDriver struct {
	name       string
	kind       deployment.Kind
	validated  json.RawMessage
	capability bool
}

func (f *fakeDriver) Name() string          { return f.name }
func (f *fakeDriver) Kind() deployment.Kind { return f.kind }
func (f *fakeDriver) Capabilities(context.Context, Target) (deployment.Capabilities, error) {
	if !f.capability {
		return deployment.Capabilities{}, &CapabilityError{Driver: f.name, Capability: "test"}
	}
	return deployment.Capabilities{Kind: f.kind}, nil
}
func (f *fakeDriver) Validate(context.Context, Target, deployment.Node) []deployment.Finding {
	return nil
}
func (f *fakeDriver) Plan(context.Context, Target, deployment.Node, *ProviderResource) (Plan, error) {
	return Plan{Action: "NONE"}, nil
}
func (f *fakeDriver) Apply(context.Context, Target, deployment.Node, Operation) (ProviderResource, error) {
	return ProviderResource{}, nil
}
func (f *fakeDriver) Inspect(context.Context, Target, string) (ProviderResource, error) {
	return ProviderResource{}, nil
}
func (f *fakeDriver) Destroy(context.Context, Target, string, Operation) error { return nil }
func (f *fakeDriver) ValidateOptions(raw json.RawMessage) []deployment.Finding {
	var options struct {
		Mode string `json:"mode"`
	}
	if err := StrictUnmarshal(raw, &options); err != nil {
		return []deployment.Finding{ErrorFinding("", "DRIVER_OPTION_INVALID", err.Error())}
	}
	return nil
}

type fakeStaticDriver struct{ fakeDriver }

func (f *fakeStaticDriver) StaticCapabilities() deployment.Capabilities {
	return deployment.Capabilities{Kind: f.kind, MaxNodes: 1}
}

func TestRegistryRejectsDuplicateAndUnknownDrivers(t *testing.T) {
	registry, err := NewRegistry(&fakeStaticDriver{fakeDriver{name: "alpha", kind: deployment.KindContainer, capability: true}})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := registry.Register(&fakeDriver{name: "alpha", kind: deployment.KindContainer}); err == nil {
		t.Fatal("duplicate driver must be rejected")
	}
	if err := registry.Register(&fakeDriver{name: "Bad", kind: deployment.KindVM}); err == nil {
		t.Fatal("mixed-case driver name must be rejected")
	}
	if _, ok := registry.Get("missing"); ok {
		t.Fatal("unknown driver must not resolve")
	}
	findings := registry.ValidateDriverOptions(context.Background(), "missing", deployment.Node{Kind: deployment.KindContainer}, json.RawMessage(`{}`))
	if len(findings) != 1 || findings[0].Code != "DRIVER_UNKNOWN" {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestRegistryRejectsOpenOptionSchemas(t *testing.T) {
	registry, err := NewRegistry(&fakeStaticDriver{fakeDriver{name: "alpha", kind: deployment.KindContainer, capability: true}})
	if err != nil {
		t.Fatal(err)
	}
	findings := registry.ValidateDriverOptions(context.Background(), "alpha", deployment.Node{Kind: deployment.KindContainer}, json.RawMessage(`{"mode":"x","surprise":true}`))
	if len(findings) != 1 || findings[0].Severity != deployment.SeverityError {
		t.Fatalf("unknown option field must be an error, got %+v", findings)
	}
}

func TestStaticMetadataDoesNotProvision(t *testing.T) {
	registry, err := NewRegistry(
		&fakeStaticDriver{fakeDriver{name: "alpha", kind: deployment.KindContainer, capability: true}},
		&fakeDriver{name: "open", kind: deployment.KindVM, capability: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := registry.ListDriverMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 || metadata[0].Driver != "alpha" {
		t.Fatalf("metadata must include only adapters with static capabilities: %+v", metadata)
	}
}

func TestLabelsOwnershipAndGeneration(t *testing.T) {
	instanceID := uuid.New()
	labels := Labels("docker", Operation{InstanceID: instanceID, NodeKey: "web", Generation: 3, Kind: OperationCreate})
	if !Owns(labels, instanceID, "web") {
		t.Fatal("labels must be owned")
	}
	if Owns(labels, uuid.New(), "web") || Owns(labels, instanceID, "db") {
		t.Fatal("foreign instance/node must not be owned")
	}
	if Generation(labels) != 3 {
		t.Fatalf("generation = %d", Generation(labels))
	}
	labels[LabelManaged] = "false"
	if Owns(labels, instanceID, "web") {
		t.Fatal("unmanaged objects must never be adopted")
	}
}

func TestStrictTargetSettingsRejectUnknownFields(t *testing.T) {
	_, err := DecodeTargetSettings(json.RawMessage(`{"options":{},"allocation":{"network":{"kind":"bridge","key":"x"}},"evil":1}`))
	if err == nil {
		t.Fatal("unknown envelope field must be rejected")
	}
	settings, err := DecodeTargetSettings(json.RawMessage(`{"options":{"a":1},"allocation":{"network":{"kind":"bridge","key":"x"},"ports":{"web":{"host":"127.0.0.1","port":18080}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if settings.Allocation == nil || settings.Allocation.Ports["web"].Port != 18080 {
		t.Fatalf("allocation not decoded: %+v", settings.Allocation)
	}
}

func TestValidateOCIDigestRequiresImmutableReference(t *testing.T) {
	valid := "nginx@sha256:713fc2ebfd92e8217db451477182ee91944b123d2b1ee9fd5a8c6e91ca6c3a11"
	if err := ValidateOCIDigest(valid); err != nil {
		t.Fatalf("valid digest rejected: %v", err)
	}
	for _, ref := range []string{
		"nginx:latest",
		"nginx:alpine",
		"https://nginx@sha256:abc",
		"nginx@sha256:short",
		"../nginx@sha256:713fc2ebfd92e8217db451477182ee91944b123d2b1ee9fd5a8c6e91ca6c3a11",
	} {
		if err := ValidateOCIDigest(ref); err == nil {
			t.Fatalf("reference %q must be rejected", ref)
		}
	}
}

func TestCapabilityErrorIsUnsupported(t *testing.T) {
	err := &CapabilityError{Driver: "docker", Capability: "snapshot_reset"}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatal("capability errors must unwrap to ErrUnsupported")
	}
}

func TestSanitizeName(t *testing.T) {
	if got := SanitizeName("actfb", "ABC", "node_1"); got != "actfb-abc-node-1" {
		t.Fatalf("sanitize = %q", got)
	}
	if got := SanitizeName("///"); got != "actfb" {
		t.Fatalf("empty sanitize = %q", got)
	}
}
