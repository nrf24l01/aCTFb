package podman

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/runtime/containerapi"
)

func TestPodmanNeverFallsBackToDocker(t *testing.T) {
	driver, err := New(WithSocket("/nonexistent/actfb-podman.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if driver.Name() != deployment.DriverPodman {
		t.Fatalf("driver name = %s", driver.Name())
	}
	settings, err := runtime.EncodeTargetSettings(runtime.TargetSettings{Options: json.RawMessage(`{"socket":"/nonexistent/actfb-podman.sock"}`)})
	if err != nil {
		t.Fatal(err)
	}
	err = driver.(runtime.Probe).Probe(context.Background(), runtime.Target{
		IntegrationUUID: uuid.New(), Driver: deployment.DriverPodman, Kind: deployment.KindContainer, Settings: settings,
	})
	if err == nil {
		t.Fatal("podman probe must fail when the podman socket is absent")
	}
	if !errors.Is(err, containerapi.ErrUnavailable) && !errors.Is(err, runtime.ErrProviderUnavailable) {
		t.Fatalf("expected a provider-unavailable error, got %v", err)
	}
}

func TestPodmanRejectsDockerSettingsShapes(t *testing.T) {
	driver, err := New()
	if err != nil {
		t.Fatal(err)
	}
	validator := driver.(runtime.SettingsValidator)
	if findings := validator.ValidateSettings(json.RawMessage(`{"endpoint":"tcp://127.0.0.1:2375"}`)); len(findings) == 0 {
		t.Fatal("remote podman endpoint must be refused")
	}
	if findings := validator.ValidateSettings(json.RawMessage(`{"unknown":true}`)); len(findings) == 0 {
		t.Fatal("unknown podman settings must be refused")
	}
}

func TestPodmanAdvertisesPrivateExposureOnly(t *testing.T) {
	driver, err := New()
	if err != nil {
		t.Fatal(err)
	}
	static, ok := driver.(runtime.StaticMetadata)
	if !ok {
		t.Fatal("podman driver must expose static metadata")
	}
	if static.StaticCapabilities().PublicExposure {
		t.Fatal("unverified rootless podman publication must not be advertised as public")
	}
}
