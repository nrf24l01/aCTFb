package boards

import (
	"encoding/json"
	"fmt"
)

// DefaultInstanceInitialTTLSeconds is the global fallback initial lifetime when
// neither the task nor the module configures one. It must stay equal to the
// instances service default (internal/instances Options.DefaultTTL); a test
// cross-checks the two so the task summary never lies.
const DefaultInstanceInitialTTLSeconds = 3600

// MaxInstanceLifetimeSeconds is the global hard cap for a total instance
// lifetime (7 days). Module settings may lower it but never raise it.
const MaxInstanceLifetimeSeconds = 604800

// MinInstanceLifetimeSeconds is the smallest accepted lifetime or step.
const MinInstanceLifetimeSeconds = 60

// MaxInstanceExtensionSeconds is the largest accepted single extension step.
const MaxInstanceExtensionSeconds = 86400

// MaxInstanceExtensions is the largest accepted extension count.
const MaxInstanceExtensions = 100

// MaxConcurrentInstancesLimit is the largest accepted per-subject concurrency.
const MaxConcurrentInstancesLimit = 100

// TaskInstanceExtensionSettings is the persisted student extension policy.
// MaxCount zero disables extensions; it never means unlimited.
type TaskInstanceExtensionSettings struct {
	Enabled  bool
	Seconds  int
	MaxCount int
}

// TaskInstanceSettings is the persisted per-module instance policy. A nil
// pointer on Module means "no module settings": the instances service then
// falls back to its global defaults, which is the legacy behaviour.
type TaskInstanceSettings struct {
	DefaultTTLSeconds                int
	MaxTTLSeconds                    int
	MaxConcurrentInstancesPerSubject int
	ResetEnabled                     bool
	Extension                        *TaskInstanceExtensionSettings
}

type taskInstanceExtensionWire struct {
	Enabled  *bool `json:"enabled"`
	Seconds  *int  `json:"seconds"`
	MaxCount *int  `json:"maxCount"`
}

type taskInstanceSettingsWire struct {
	DefaultTTLSeconds                *int                        `json:"defaultTtlSeconds"`
	MaxTTLSeconds                    *int                        `json:"maxTtlSeconds"`
	MaxConcurrentInstancesPerSubject *int                        `json:"maxConcurrentInstancesPerSubject"`
	ResetEnabled                     *bool                       `json:"resetEnabled"`
	Extension                        *taskInstanceExtensionWire `json:"extension"`
}

// encodeTaskInstanceSettings stores the policy as JSONB. The shape matches the
// Swagger TaskInstanceModuleSettings definition exactly.
func encodeTaskInstanceSettings(settings *TaskInstanceSettings) ([]byte, error) {
	if settings == nil {
		return nil, nil
	}
	wire := taskInstanceSettingsWire{
		DefaultTTLSeconds:                intPointer(settings.DefaultTTLSeconds),
		MaxTTLSeconds:                    intPointer(settings.MaxTTLSeconds),
		MaxConcurrentInstancesPerSubject: intPointer(settings.MaxConcurrentInstancesPerSubject),
		ResetEnabled:                     boolPointer(settings.ResetEnabled),
	}
	if settings.Extension != nil {
		wire.Extension = &taskInstanceExtensionWire{
			Enabled:  boolPointer(settings.Extension.Enabled),
			Seconds:  intPointer(settings.Extension.Seconds),
			MaxCount: intPointer(settings.Extension.MaxCount),
		}
	}
	return json.Marshal(wire)
}

// decodeTaskInstanceSettings reads a stored policy. An empty or null document is
// "no settings", not an error, so legacy rows keep working.
func decodeTaskInstanceSettings(raw []byte) (*TaskInstanceSettings, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil, nil
	}
	var wire taskInstanceSettingsWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	settings := &TaskInstanceSettings{}
	if wire.DefaultTTLSeconds != nil {
		settings.DefaultTTLSeconds = *wire.DefaultTTLSeconds
	}
	if wire.MaxTTLSeconds != nil {
		settings.MaxTTLSeconds = *wire.MaxTTLSeconds
	}
	if wire.MaxConcurrentInstancesPerSubject != nil {
		settings.MaxConcurrentInstancesPerSubject = *wire.MaxConcurrentInstancesPerSubject
	}
	if wire.ResetEnabled != nil {
		settings.ResetEnabled = *wire.ResetEnabled
	}
	if wire.Extension != nil {
		extension := &TaskInstanceExtensionSettings{}
		if wire.Extension.Enabled != nil {
			extension.Enabled = *wire.Extension.Enabled
		}
		if wire.Extension.Seconds != nil {
			extension.Seconds = *wire.Extension.Seconds
		}
		if wire.Extension.MaxCount != nil {
			extension.MaxCount = *wire.Extension.MaxCount
		}
		settings.Extension = extension
	}
	return settings, nil
}

// validateTaskInstanceSettings enforces the persisted module policy bounds.
// Messages are field-specific; the admin value is never silently clamped.
func validateTaskInstanceSettings(settings *TaskInstanceSettings) error {
	if settings == nil {
		return nil
	}
	if settings.DefaultTTLSeconds < MinInstanceLifetimeSeconds || settings.DefaultTTLSeconds > MaxInstanceLifetimeSeconds {
		return Invalid("body.taskInstanceSettings.defaultTtlSeconds",
			fmt.Sprintf("must be between %d and %d seconds", MinInstanceLifetimeSeconds, MaxInstanceLifetimeSeconds), "OUT_OF_RANGE")
	}
	if settings.MaxTTLSeconds < MinInstanceLifetimeSeconds || settings.MaxTTLSeconds > MaxInstanceLifetimeSeconds {
		return Invalid("body.taskInstanceSettings.maxTtlSeconds",
			fmt.Sprintf("must be between %d and %d seconds", MinInstanceLifetimeSeconds, MaxInstanceLifetimeSeconds), "OUT_OF_RANGE")
	}
	if settings.MaxTTLSeconds < settings.DefaultTTLSeconds {
		return Invalid("body.taskInstanceSettings.maxTtlSeconds",
			"must be greater than or equal to defaultTtlSeconds", "INVALID_VALUE")
	}
	if settings.MaxConcurrentInstancesPerSubject < 1 || settings.MaxConcurrentInstancesPerSubject > MaxConcurrentInstancesLimit {
		return Invalid("body.taskInstanceSettings.maxConcurrentInstancesPerSubject",
			fmt.Sprintf("must be between 1 and %d", MaxConcurrentInstancesLimit), "OUT_OF_RANGE")
	}
	if extension := settings.Extension; extension != nil {
		if extension.Seconds < MinInstanceLifetimeSeconds || extension.Seconds > MaxInstanceExtensionSeconds {
			return Invalid("body.taskInstanceSettings.extension.seconds",
				fmt.Sprintf("must be between %d and %d seconds", MinInstanceLifetimeSeconds, MaxInstanceExtensionSeconds), "OUT_OF_RANGE")
		}
		if extension.MaxCount < 0 || extension.MaxCount > MaxInstanceExtensions {
			return Invalid("body.taskInstanceSettings.extension.maxCount",
				fmt.Sprintf("must be between 0 and %d", MaxInstanceExtensions), "OUT_OF_RANGE")
		}
	}
	return nil
}

// TaskLifetimeOverrides are the nullable per-task lifetime overrides. Nil means
// "inherit the module setting"; a non-nil value is an explicit admin choice.
type TaskLifetimeOverrides struct {
	InitialTTLSeconds  *int
	MaxTTLSeconds      *int
	ExtensionEnabled   *bool
	ExtensionSeconds   *int
	ExtensionMaxCount  *int
}

// empty reports whether no override is configured at all.
func (o TaskLifetimeOverrides) empty() bool {
	return o.InitialTTLSeconds == nil && o.MaxTTLSeconds == nil &&
		o.ExtensionEnabled == nil && o.ExtensionSeconds == nil && o.ExtensionMaxCount == nil
}

// validateTaskLifetimeOverrides checks a task override against the module policy
// and the global caps. Overrides may only narrow the module ceiling.
func validateTaskLifetimeOverrides(overrides TaskLifetimeOverrides, module *TaskInstanceSettings, enabled bool) error {
	if overrides.empty() {
		return nil
	}
	if !enabled {
		return Invalid("body.instance", "lifetime overrides require instance.enabled=true", "INVALID_VALUE")
	}
	if overrides.InitialTTLSeconds != nil {
		value := *overrides.InitialTTLSeconds
		if value < MinInstanceLifetimeSeconds || value > MaxInstanceLifetimeSeconds {
			return Invalid("body.instance.ttlSeconds",
				fmt.Sprintf("must be between %d and %d seconds", MinInstanceLifetimeSeconds, MaxInstanceLifetimeSeconds), "OUT_OF_RANGE")
		}
		if module != nil && module.MaxTTLSeconds > 0 && value > module.MaxTTLSeconds {
			return Invalid("body.instance.ttlSeconds",
				fmt.Sprintf("must not exceed the module maximum of %d seconds", module.MaxTTLSeconds), "INVALID_VALUE")
		}
	}
	if overrides.MaxTTLSeconds != nil {
		value := *overrides.MaxTTLSeconds
		if value < MinInstanceLifetimeSeconds || value > MaxInstanceLifetimeSeconds {
			return Invalid("body.instance.maxTtlSeconds",
				fmt.Sprintf("must be between %d and %d seconds", MinInstanceLifetimeSeconds, MaxInstanceLifetimeSeconds), "OUT_OF_RANGE")
		}
		if module != nil && module.MaxTTLSeconds > 0 && value > module.MaxTTLSeconds {
			return Invalid("body.instance.maxTtlSeconds",
				fmt.Sprintf("must not exceed the module maximum of %d seconds", module.MaxTTLSeconds), "INVALID_VALUE")
		}
	}
	if overrides.InitialTTLSeconds != nil && overrides.MaxTTLSeconds != nil &&
		*overrides.InitialTTLSeconds > *overrides.MaxTTLSeconds {
		return Invalid("body.instance.maxTtlSeconds",
			"must be greater than or equal to ttlSeconds", "INVALID_VALUE")
	}
	if overrides.ExtensionEnabled != nil || overrides.ExtensionSeconds != nil || overrides.ExtensionMaxCount != nil {
		if overrides.ExtensionSeconds == nil || overrides.ExtensionMaxCount == nil {
			return Invalid("body.instance.extension",
				"extension requires both seconds and maxCount", "REQUIRED")
		}
		seconds := *overrides.ExtensionSeconds
		if seconds < MinInstanceLifetimeSeconds || seconds > MaxInstanceExtensionSeconds {
			return Invalid("body.instance.extension.seconds",
				fmt.Sprintf("must be between %d and %d seconds", MinInstanceLifetimeSeconds, MaxInstanceExtensionSeconds), "OUT_OF_RANGE")
		}
		count := *overrides.ExtensionMaxCount
		if count < 0 || count > MaxInstanceExtensions {
			return Invalid("body.instance.extension.maxCount",
				fmt.Sprintf("must be between 0 and %d", MaxInstanceExtensions), "OUT_OF_RANGE")
		}
		enabledOverride := true
		if overrides.ExtensionEnabled != nil {
			enabledOverride = *overrides.ExtensionEnabled
		}
		if module != nil && module.Extension != nil {
			if enabledOverride && !module.Extension.Enabled {
				return Invalid("body.instance.extension.enabled",
					"the module disables student extensions", "INVALID_VALUE")
			}
			if module.Extension.MaxCount > 0 && count > module.Extension.MaxCount {
				return Invalid("body.instance.extension.maxCount",
					fmt.Sprintf("must not exceed the module maximum of %d", module.Extension.MaxCount), "INVALID_VALUE")
			}
			if seconds > module.Extension.Seconds {
				return Invalid("body.instance.extension.seconds",
					fmt.Sprintf("must not exceed the module step of %d seconds", module.Extension.Seconds), "INVALID_VALUE")
			}
		}
	}
	return nil
}

// EffectiveInstanceInitialTTLSeconds resolves the initial lifetime used for a
// NEW instance of this task: task override, then module default, then the global
// default. It returns nil when the task does not require an instance.
func EffectiveInstanceInitialTTLSeconds(task Task, globalDefault int) *int {
	if !task.InstanceEnabled {
		return nil
	}
	if task.InstanceTTLSeconds != nil {
		value := *task.InstanceTTLSeconds
		return &value
	}
	if task.ModuleInstanceSettings != nil && task.ModuleInstanceSettings.DefaultTTLSeconds > 0 {
		value := task.ModuleInstanceSettings.DefaultTTLSeconds
		return &value
	}
	if globalDefault <= 0 {
		globalDefault = DefaultInstanceInitialTTLSeconds
	}
	value := globalDefault
	return &value
}

func intPointer(value int) *int    { return &value }
func boolPointer(value bool) *bool { return &value }

// moduleSettingsFor returns the parent module policy or nil for legacy modules.
func moduleSettingsFor(module *Module) *TaskInstanceSettings {
	if module == nil {
		return nil
	}
	return module.TaskInstanceSettings
}

// taskLifetimeOverrides projects a stored task onto the override shape.
func taskLifetimeOverrides(task Task) TaskLifetimeOverrides {
	return TaskLifetimeOverrides{
		InitialTTLSeconds: task.InstanceTTLSeconds,
		MaxTTLSeconds:     task.InstanceMaxTTLSeconds,
		ExtensionEnabled:  task.InstanceExtensionEnabled,
		ExtensionSeconds:  task.InstanceExtensionSeconds,
		ExtensionMaxCount: task.InstanceExtensionMaxCount,
	}
}

// mergeTaskLifetimeOverrides applies presence-aware PATCH semantics: an omitted
// field preserves the stored override, an explicit null clears it (inherit) and
// a value replaces it.
func mergeTaskLifetimeOverrides(task *Task, input TaskInstanceInput) {
	if input.TTLSeconds.Set {
		task.InstanceTTLSeconds = input.TTLSeconds.Value
	}
	if input.MaxTTLSeconds.Set {
		task.InstanceMaxTTLSeconds = input.MaxTTLSeconds.Value
	}
	if input.ExtensionEnabled.Set {
		task.InstanceExtensionEnabled = input.ExtensionEnabled.Value
	}
	if input.ExtensionSeconds.Set {
		task.InstanceExtensionSeconds = input.ExtensionSeconds.Value
	}
	if input.ExtensionMaxCount.Set {
		task.InstanceExtensionMaxCount = input.ExtensionMaxCount.Value
	}
}

// clearTaskLifetimeOverrides removes every override so the task inherits again.
func clearTaskLifetimeOverrides(task *Task) {
	task.InstanceTTLSeconds = nil
	task.InstanceMaxTTLSeconds = nil
	task.InstanceExtensionEnabled = nil
	task.InstanceExtensionSeconds = nil
	task.InstanceExtensionMaxCount = nil
}
