package http

import (
	"encoding/json"
	"testing"

	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

// rawInstanceBody parses a JSON body into the raw presence map the handler uses
// to distinguish an omitted field from an explicit null.
func rawInstanceBody(t *testing.T, body string) rawBody {
	t.Helper()
	var raw rawBody
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode raw body: %v", err)
	}
	return raw
}

func TestTaskInstanceInputLifetimePresence(t *testing.T) {
	// Omitted: nothing is set, so create inherits and patch preserves.
	var omitted models.TaskInstanceConfig
	if err := json.Unmarshal([]byte(`{"enabled":true}`), &omitted); err != nil {
		t.Fatal(err)
	}
	input := taskInstanceInput(&omitted, rawInstanceBody(t, `{"instance":{"enabled":true}}`))
	if input.TTLSeconds.Set || input.MaxTTLSeconds.Set || input.ExtensionEnabled.Set {
		t.Fatalf("omitted lifetime fields must stay unset: %+v", input)
	}

	// Explicit null: set with a nil value, which resets to inherit.
	var nulls models.TaskInstanceConfig
	if err := json.Unmarshal([]byte(`{"enabled":true,"ttlSeconds":null,"extension":null}`), &nulls); err != nil {
		t.Fatal(err)
	}
	input = taskInstanceInput(&nulls, rawInstanceBody(t, `{"instance":{"enabled":true,"ttlSeconds":null,"extension":null}}`))
	if !input.TTLSeconds.Set || input.TTLSeconds.Value != nil {
		t.Fatalf("explicit null ttlSeconds must be a set/nil override: %+v", input.TTLSeconds)
	}
	if !input.ExtensionEnabled.Set || input.ExtensionEnabled.Value != nil {
		t.Fatalf("explicit null extension must be a set/nil override: %+v", input.ExtensionEnabled)
	}
	if !input.ExtensionSeconds.Set || !input.ExtensionMaxCount.Set {
		t.Fatalf("explicit null extension must reset every extension field: %+v", input)
	}

	// Present values.
	var values models.TaskInstanceConfig
	body := `{"enabled":true,"ttlSeconds":1800,"maxTtlSeconds":7200,"extension":{"enabled":true,"seconds":900,"maxCount":6}}`
	if err := json.Unmarshal([]byte(body), &values); err != nil {
		t.Fatal(err)
	}
	input = taskInstanceInput(&values, rawInstanceBody(t, `{"instance":`+body+`}`))
	if !input.TTLSeconds.Set || input.TTLSeconds.Value == nil || *input.TTLSeconds.Value != 1800 {
		t.Fatalf("ttlSeconds not mapped: %+v", input.TTLSeconds)
	}
	if !input.MaxTTLSeconds.Set || input.MaxTTLSeconds.Value == nil || *input.MaxTTLSeconds.Value != 7200 {
		t.Fatalf("maxTtlSeconds not mapped: %+v", input.MaxTTLSeconds)
	}
	if !input.ExtensionEnabled.Set || input.ExtensionEnabled.Value == nil || !*input.ExtensionEnabled.Value {
		t.Fatalf("extension.enabled not mapped: %+v", input.ExtensionEnabled)
	}
	if input.ExtensionSeconds.Value == nil || *input.ExtensionSeconds.Value != 900 {
		t.Fatalf("extension.seconds not mapped: %+v", input.ExtensionSeconds)
	}
	if input.ExtensionMaxCount.Value == nil || *input.ExtensionMaxCount.Value != 6 {
		t.Fatalf("extension.maxCount not mapped: %+v", input.ExtensionMaxCount)
	}
}

func TestModuleInstanceSettingsMapping(t *testing.T) {
	if moduleInstanceSettings(nil) != nil {
		t.Fatal("nil settings must stay nil")
	}
	enabled := true
	seconds := int64(900)
	count := int64(6)
	defaultTTL := int64(1800)
	maxTTL := int64(7200)
	concurrent := int64(3)
	settings := moduleInstanceSettings(&models.TaskInstanceModuleSettings{
		DefaultTTLSeconds: &defaultTTL, MaxTTLSeconds: &maxTTL,
		MaxConcurrentInstancesPerSubject: &concurrent, ResetEnabled: &enabled,
		Extension: &models.TaskInstanceModuleSettingsExtension{Enabled: &enabled, Seconds: &seconds, MaxCount: &count},
	})
	if settings.DefaultTTLSeconds != 1800 || settings.MaxTTLSeconds != 7200 || settings.MaxConcurrentInstancesPerSubject != 3 {
		t.Fatalf("settings not mapped: %+v", settings)
	}
	if !settings.ResetEnabled || settings.Extension == nil || settings.Extension.MaxCount != 6 {
		t.Fatalf("extension not mapped: %+v", settings.Extension)
	}

	roundTrip := taskInstanceSettingsModel(settings)
	if roundTrip == nil || roundTrip.MaxTTLSeconds == nil || *roundTrip.MaxTTLSeconds != 7200 {
		t.Fatalf("reverse mapping failed: %+v", roundTrip)
	}
	if roundTrip.Extension == nil || roundTrip.Extension.Seconds == nil || *roundTrip.Extension.Seconds != 900 {
		t.Fatalf("reverse extension mapping failed: %+v", roundTrip.Extension)
	}
}
