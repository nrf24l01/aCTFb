package instances

import (
	"fmt"
	"time"
)

// Lifetime bounds. They mirror the Swagger contract and the migration 019 CHECK
// constraints; the database is the last line of defence, not the first.
const (
	MinLifetimeSeconds  = 60
	MaxLifetimeSeconds  = 604800
	MaxExtensionSeconds = 86400
	MaxExtensionCount   = 100
	MaxConcurrent       = 100
)

// LifecyclePolicy is the secrets-free effective instance lifetime policy. It is
// frozen into the desired snapshot at CREATE so later task/module edits cannot
// retroactively change an existing instance. Current board/module ends and the
// hard deadline are still applied live.
type LifecyclePolicy struct {
	InitialTTLSeconds  int64 `json:"initialTtlSeconds"`
	MaxTotalTTLSeconds int64 `json:"maxTotalTtlSeconds"`
	ExtendEnabled      bool  `json:"extendEnabled"`
	ExtendSeconds      int64 `json:"extendSeconds"`
	ExtendMaxCount     int   `json:"extendMaxCount"`
	ResetEnabled       bool  `json:"resetEnabled"`
	MaxConcurrent      int   `json:"maxConcurrent"`
}

// ModuleExtensionSettings mirrors modules.task_instance_settings.extension.
type ModuleExtensionSettings struct {
	Enabled  bool
	Seconds  int
	MaxCount int
}

// ModuleInstanceSettings mirrors the persisted modules.task_instance_settings
// document. A nil pointer means the legacy behaviour: global defaults apply.
type ModuleInstanceSettings struct {
	DefaultTTLSeconds                int
	MaxTTLSeconds                    int
	MaxConcurrentInstancesPerSubject int
	ResetEnabled                     bool
	Extension                        *ModuleExtensionSettings
}

// TaskLifetimeOverrides mirrors the nullable task lifetime columns. Nil means
// "inherit the module setting".
type TaskLifetimeOverrides struct {
	InitialTTLSeconds *int
	MaxTTLSeconds     *int
	ExtensionEnabled  *bool
	ExtensionSeconds  *int
	ExtensionMaxCount *int
}

// ResolveLifecycle applies the documented precedence: task override, then
// module setting, then the global service defaults. It never silently clamps an
// administrator's value; contradictions are reported so the caller fails closed.
func ResolveLifecycle(task TaskLifetimeOverrides, module *ModuleInstanceSettings, options Options) (LifecyclePolicy, error) {
	global := normalizedOptions(options)
	policy := LifecyclePolicy{
		InitialTTLSeconds:  int64(global.DefaultTTL / time.Second),
		MaxTotalTTLSeconds: int64(global.MaxTtl() / time.Second),
		ExtendEnabled:      true,
		ExtendSeconds:      int64(global.ExtensionTTL / time.Second),
		ExtendMaxCount:     global.MaxExtensions,
		ResetEnabled:       true,
		MaxConcurrent:      global.MaxInstancesPerSubject,
	}
	if module != nil {
		if module.DefaultTTLSeconds > 0 {
			policy.InitialTTLSeconds = int64(module.DefaultTTLSeconds)
		}
		if module.MaxTTLSeconds > 0 {
			policy.MaxTotalTTLSeconds = int64(module.MaxTTLSeconds)
		}
		if module.MaxConcurrentInstancesPerSubject > 0 {
			policy.MaxConcurrent = module.MaxConcurrentInstancesPerSubject
		}
		policy.ResetEnabled = module.ResetEnabled
		if extension := module.Extension; extension != nil {
			policy.ExtendEnabled = extension.Enabled
			if extension.Seconds > 0 {
				policy.ExtendSeconds = int64(extension.Seconds)
			}
			policy.ExtendMaxCount = extension.MaxCount
		}
	}
	if task.InitialTTLSeconds != nil {
		policy.InitialTTLSeconds = int64(*task.InitialTTLSeconds)
	}
	if task.MaxTTLSeconds != nil {
		policy.MaxTotalTTLSeconds = int64(*task.MaxTTLSeconds)
	}
	if task.ExtensionEnabled != nil {
		policy.ExtendEnabled = *task.ExtensionEnabled
	}
	if task.ExtensionSeconds != nil {
		policy.ExtendSeconds = int64(*task.ExtensionSeconds)
	}
	if task.ExtensionMaxCount != nil {
		policy.ExtendMaxCount = *task.ExtensionMaxCount
	}
	if err := policy.validate(); err != nil {
		return LifecyclePolicy{}, err
	}
	return policy, nil
}

func (p LifecyclePolicy) validate() error {
	if p.InitialTTLSeconds < MinLifetimeSeconds || p.InitialTTLSeconds > MaxLifetimeSeconds {
		return fmt.Errorf("instances: initial lifetime %d is outside %d..%d", p.InitialTTLSeconds, MinLifetimeSeconds, MaxLifetimeSeconds)
	}
	if p.MaxTotalTTLSeconds < MinLifetimeSeconds || p.MaxTotalTTLSeconds > MaxLifetimeSeconds {
		return fmt.Errorf("instances: max total lifetime %d is outside %d..%d", p.MaxTotalTTLSeconds, MinLifetimeSeconds, MaxLifetimeSeconds)
	}
	if p.InitialTTLSeconds > p.MaxTotalTTLSeconds {
		return fmt.Errorf("instances: initial lifetime %d exceeds max total lifetime %d", p.InitialTTLSeconds, p.MaxTotalTTLSeconds)
	}
	if p.ExtendSeconds < MinLifetimeSeconds || p.ExtendSeconds > MaxExtensionSeconds {
		return fmt.Errorf("instances: extension step %d is outside %d..%d", p.ExtendSeconds, MinLifetimeSeconds, MaxExtensionSeconds)
	}
	if p.ExtendMaxCount < 0 || p.ExtendMaxCount > MaxExtensionCount {
		return fmt.Errorf("instances: extension count %d is outside 0..%d", p.ExtendMaxCount, MaxExtensionCount)
	}
	if p.MaxConcurrent < 1 || p.MaxConcurrent > MaxConcurrent {
		return fmt.Errorf("instances: max concurrent %d is outside 1..%d", p.MaxConcurrent, MaxConcurrent)
	}
	return nil
}

// normalizedOptions fills any zero option with the frozen default so a partially
// configured service never produces a zero-length lifetime.
func normalizedOptions(options Options) Options {
	if options.DefaultTTL <= 0 {
		options.DefaultTTL = time.Hour
	}
	if options.MaxTTL <= 0 {
		options.MaxTTL = 7 * 24 * time.Hour
	}
	if options.ExtensionTTL <= 0 {
		options.ExtensionTTL = 30 * time.Minute
	}
	if options.MaxExtensions < 0 {
		options.MaxExtensions = 0
	}
	if options.MaxInstancesPerSubject <= 0 {
		options.MaxInstancesPerSubject = 2
	}
	return options
}

// legacyPolicy is the compatibility policy for instances created before the
// lifecycle snapshot existed. It uses the global defaults only.
func legacyPolicy(options Options) LifecyclePolicy {
	policy, err := ResolveLifecycle(TaskLifetimeOverrides{}, nil, options)
	if err != nil {
		// The defaults are valid by construction; this is unreachable.
		return LifecyclePolicy{
			InitialTTLSeconds:  3600,
			MaxTotalTTLSeconds: 604800,
			ExtendEnabled:      true,
			ExtendSeconds:      1800,
			ExtendMaxCount:     3,
			ResetEnabled:       true,
			MaxConcurrent:      2,
		}
	}
	return policy
}

// policyFor returns the frozen policy of a snapshot, or the legacy fallback when
// the snapshot predates the lifecycle block.
func policyFor(snapshot DesiredSnapshot, options Options) LifecyclePolicy {
	if snapshot.Lifecycle != nil {
		return *snapshot.Lifecycle
	}
	return legacyPolicy(options)
}

// hardDeadline is the absolute latest expiry: the total lifetime cap measured
// from creation, further limited by the current board/module end. Board/module
// ends are always read live, including for instances with an old snapshot.
func hardDeadline(createdAt time.Time, maxTotalTTL time.Duration, ends ...*time.Time) time.Time {
	deadline := createdAt.Add(maxTotalTTL)
	for _, end := range ends {
		if end != nil && end.Before(deadline) {
			deadline = *end
		}
	}
	return deadline
}

// policyForSnapshot resolves the policy of an already loaded instance. A nil
// lifecycle means the instance predates the frozen policy, so the global
// defaults apply.
func policyForSnapshot(lifecycle *LifecyclePolicy, options Options) LifecyclePolicy {
	if lifecycle != nil {
		return *lifecycle
	}
	return legacyPolicy(options)
}
