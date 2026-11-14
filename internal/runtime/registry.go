package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/nrf24l01/actfb/internal/deployment"
)

// OptionValidator is implemented by every driver with a closed, typed option
// schema. ValidateOptions decodes only the block namespaced for that driver and
// must reject unknown fields. It never contacts the provider and never echoes
// raw values.
type OptionValidator interface {
	ValidateOptions(raw json.RawMessage) []deployment.Finding
}

// SettingsValidator validates the administrator-supplied integration settings
// block of a driver. Unknown fields are rejected and provider credentials are
// never accepted here.
type SettingsValidator interface {
	ValidateSettings(raw json.RawMessage) []deployment.Finding
}

// StaticMetadata is implemented by every adapter so template preview/diff can
// describe supported capabilities without contacting a provider and without
// provisioning anything.
type StaticMetadata interface {
	StaticCapabilities() deployment.Capabilities
}

// DriverRegistry is the trusted, central driver registry implementation.
type DriverRegistry struct {
	mu      sync.RWMutex
	drivers map[string]Driver
}

var _ Registry = (*DriverRegistry)(nil)

// NewRegistry builds a registry from the supplied adapters.
func NewRegistry(drivers ...Driver) (*DriverRegistry, error) {
	registry := &DriverRegistry{drivers: make(map[string]Driver, len(drivers))}
	for _, driver := range drivers {
		if err := registry.Register(driver); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Register adds one trusted adapter. Duplicate names are rejected; a driver is
// never silently replaced.
func (r *DriverRegistry) Register(driver Driver) error {
	if r == nil {
		return fmt.Errorf("runtime: nil registry")
	}
	if driver == nil {
		return fmt.Errorf("runtime: nil driver")
	}
	name := strings.TrimSpace(driver.Name())
	if name == "" {
		return fmt.Errorf("runtime: driver name is required")
	}
	if name != strings.ToLower(name) {
		return fmt.Errorf("runtime: driver name %q must be lowercase", name)
	}
	switch driver.Kind() {
	case deployment.KindVM, deployment.KindContainer:
	default:
		return fmt.Errorf("runtime: driver %q has unsupported kind %q", name, driver.Kind())
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.drivers[name]; exists {
		return fmt.Errorf("runtime: driver %q is already registered", name)
	}
	r.drivers[name] = driver
	return nil
}

// Get returns a registered driver by name.
func (r *DriverRegistry) Get(name string) (Driver, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	driver, ok := r.drivers[strings.TrimSpace(name)]
	return driver, ok
}

// List returns drivers sorted by name.
func (r *DriverRegistry) List() []Driver {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.drivers))
	for name := range r.drivers {
		names = append(names, name)
	}
	sort.Strings(names)
	drivers := make([]Driver, 0, len(names))
	for _, name := range names {
		drivers = append(drivers, r.drivers[name])
	}
	return drivers
}

// ListDriverMetadata implements deployment.DriverMetadataProvider. It reports
// only static metadata: no provider connection is made during preview.
func (r *DriverRegistry) ListDriverMetadata(_ context.Context) ([]deployment.DriverMetadata, error) {
	drivers := r.List()
	metadata := make([]deployment.DriverMetadata, 0, len(drivers))
	for _, driver := range drivers {
		static, ok := driver.(StaticMetadata)
		if !ok {
			// An adapter without static metadata is not advertised as usable.
			continue
		}
		metadata = append(metadata, deployment.DriverMetadata{
			Driver:       driver.Name(),
			Kind:         driver.Kind(),
			Capabilities: static.StaticCapabilities(),
		})
	}
	return metadata, nil
}

// ValidateDriverOptions implements deployment.DriverMetadataProvider. Unknown
// option fields are errors, and a driver without a closed schema is not treated
// as "anything goes": it fails closed.
func (r *DriverRegistry) ValidateDriverOptions(_ context.Context, driver string, node deployment.Node, options json.RawMessage) []deployment.Finding {
	registered, ok := r.Get(driver)
	if !ok {
		return []deployment.Finding{{
			Path:     "",
			Code:     "DRIVER_UNKNOWN",
			Message:  "driver is not registered by this runtime",
			Severity: deployment.SeverityError,
		}}
	}
	if registered.Kind() != node.Kind {
		return []deployment.Finding{{
			Path:     "",
			Code:     "DRIVER_KIND_MISMATCH",
			Message:  "driver kind does not match the node kind",
			Severity: deployment.SeverityError,
		}}
	}
	validator, ok := registered.(OptionValidator)
	if !ok {
		return []deployment.Finding{{
			Path:     "",
			Code:     "DRIVER_OPTION_SCHEMA_UNAVAILABLE",
			Message:  "driver option schema is not available; options are refused",
			Severity: deployment.SeverityError,
		}}
	}
	return validator.ValidateOptions(options)
}

// ValidateSettings dispatches strict integration-settings validation. A driver
// without a closed schema fails closed.
func (r *DriverRegistry) ValidateSettings(driver string, raw json.RawMessage) []deployment.Finding {
	registered, ok := r.Get(driver)
	if !ok {
		return []deployment.Finding{{Path: "", Code: "DRIVER_UNKNOWN", Message: "driver is not registered by this runtime", Severity: deployment.SeverityError}}
	}
	validator, ok := registered.(SettingsValidator)
	if !ok {
		return []deployment.Finding{{Path: "", Code: "DRIVER_SETTINGS_SCHEMA_UNAVAILABLE", Message: "driver settings schema is not available; settings are refused", Severity: deployment.SeverityError}}
	}
	return validator.ValidateSettings(raw)
}

// Probe performs a read-only provider check for the integration test endpoint.
// Drivers without a probe are reported as unsupported instead of a fake success.
func (r *DriverRegistry) Probe(ctx context.Context, target Target) error {
	driver, ok := r.Get(target.Driver)
	if !ok {
		return &CapabilityError{Driver: target.Driver, Capability: "probe", Detail: "driver is not registered"}
	}
	prober, ok := driver.(Probe)
	if !ok {
		return &CapabilityError{Driver: driver.Name(), Capability: "probe"}
	}
	return prober.Probe(ctx, target)
}

// ValidateTarget performs the shared target checks used by every adapter:
// driver/kind consistency, base URL policy is enforced by the integrations
// layer, and a digest-pinned OCI source is mandatory for container nodes.
func ValidateTarget(target Target) error {
	if strings.TrimSpace(target.Driver) == "" {
		return &CapabilityError{Driver: "", Capability: "driver"}
	}
	switch target.Kind {
	case deployment.KindVM, deployment.KindContainer:
	default:
		return &CapabilityError{Driver: target.Driver, Capability: "kind", Detail: string(target.Kind)}
	}
	return nil
}
