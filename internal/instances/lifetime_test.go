package instances

import (
	"testing"
	"time"
)

func TestResolveLifecyclePrecedence(t *testing.T) {
	options := Options{DefaultTTL: time.Hour, MaxTTL: 7 * 24 * time.Hour, ExtensionTTL: 30 * time.Minute, MaxExtensions: 3, MaxInstancesPerSubject: 2}
	module := &ModuleInstanceSettings{
		DefaultTTLSeconds: 3600, MaxTTLSeconds: 14400, MaxConcurrentInstancesPerSubject: 4,
		ResetEnabled: false,
		Extension:    &ModuleExtensionSettings{Enabled: true, Seconds: 900, MaxCount: 3},
	}
	initial := 1800
	maxTotal := 7200
	step := 900
	count := 3

	policy, err := ResolveLifecycle(TaskLifetimeOverrides{
		InitialTTLSeconds: &initial, MaxTTLSeconds: &maxTotal,
		ExtensionSeconds: &step, ExtensionMaxCount: &count,
	}, module, options)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if policy.InitialTTLSeconds != 1800 || policy.MaxTotalTTLSeconds != 7200 {
		t.Fatalf("task overrides did not win: %+v", policy)
	}
	if policy.ExtendSeconds != 900 || policy.ExtendMaxCount != 3 || !policy.ExtendEnabled {
		t.Fatalf("extension override not applied: %+v", policy)
	}
	if policy.ResetEnabled {
		t.Fatalf("module resetEnabled=false was not applied: %+v", policy)
	}
	if policy.MaxConcurrent != 4 {
		t.Fatalf("module concurrency not applied: %+v", policy)
	}

	// Module-only settings win over the global defaults.
	moduleOnly, err := ResolveLifecycle(TaskLifetimeOverrides{}, module, options)
	if err != nil {
		t.Fatalf("resolve module: %v", err)
	}
	if moduleOnly.InitialTTLSeconds != 3600 || moduleOnly.MaxTotalTTLSeconds != 14400 {
		t.Fatalf("module settings not applied: %+v", moduleOnly)
	}

	// No module settings at all keeps the legacy global defaults.
	legacy, err := ResolveLifecycle(TaskLifetimeOverrides{}, nil, options)
	if err != nil {
		t.Fatalf("resolve legacy: %v", err)
	}
	if legacy.InitialTTLSeconds != 3600 || legacy.MaxTotalTTLSeconds != 604800 {
		t.Fatalf("legacy defaults wrong: %+v", legacy)
	}
	if !legacy.ExtendEnabled || legacy.ExtendSeconds != 1800 || legacy.ExtendMaxCount != 3 {
		t.Fatalf("legacy extension defaults wrong: %+v", legacy)
	}
	if !legacy.ResetEnabled || legacy.MaxConcurrent != 2 {
		t.Fatalf("legacy reset/concurrency defaults wrong: %+v", legacy)
	}
}

func TestResolveLifecycleRejectsContradictions(t *testing.T) {
	options := Options{}
	initial, maxTotal := 7200, 1800
	if _, err := ResolveLifecycle(TaskLifetimeOverrides{InitialTTLSeconds: &initial, MaxTTLSeconds: &maxTotal}, nil, options); err == nil {
		t.Fatal("initial > max must be rejected")
	}
	zero := 0
	if _, err := ResolveLifecycle(TaskLifetimeOverrides{InitialTTLSeconds: &zero}, nil, options); err == nil {
		t.Fatal("a zero lifetime must be rejected")
	}
	negativeCount := -1
	if _, err := ResolveLifecycle(TaskLifetimeOverrides{ExtensionMaxCount: &negativeCount}, nil, options); err == nil {
		t.Fatal("a negative extension count must be rejected")
	}
	// A short operator-configured global lifetime stays valid: the 60 s floor is
	// an administrator-facing contract, not an internal invariant.
	short, err := ResolveLifecycle(TaskLifetimeOverrides{}, nil, Options{DefaultTTL: 6 * time.Second, MaxTTL: time.Minute})
	if err != nil {
		t.Fatalf("short operator lifetime must be accepted: %v", err)
	}
	if short.InitialTTLSeconds != 6 || short.MaxTotalTTLSeconds != 60 {
		t.Fatalf("short operator lifetime wrong: %+v", short)
	}
}

func TestHardDeadlineClampsToEarliestEnd(t *testing.T) {
	created := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	boardEnd := created.Add(2 * time.Hour)
	moduleEnd := created.Add(90 * time.Minute)
	deadline := hardDeadline(created, 7*24*time.Hour, &boardEnd, &moduleEnd)
	if !deadline.Equal(moduleEnd) {
		t.Fatalf("deadline = %s, want the earliest end %s", deadline, moduleEnd)
	}
	// A missing end never removes the total-lifetime cap.
	deadline = hardDeadline(created, 2*time.Hour, nil, nil)
	if !deadline.Equal(created.Add(2 * time.Hour)) {
		t.Fatalf("deadline = %s, want created+2h", deadline)
	}
}

// The live resolver is the only legacy-policy entry point; a frozen snapshot
// policy always wins, and a legacy row follows the operator options plus the live
// module reset switch.
func TestResolveInstancePolicyFallsBackToLegacy(t *testing.T) {
	options := Options{}
	if policy := resolveInstancePolicy(nil, nil, options); policy.InitialTTLSeconds != 3600 || policy.ExtendMaxCount != 3 {
		t.Fatalf("legacy fallback wrong: %+v", policy)
	}
	if policy := resolveInstancePolicy(nil, &ModuleInstanceSettings{ResetEnabled: false}, options); policy.ResetEnabled {
		t.Fatalf("live module reset switch not applied to a legacy row: %+v", policy)
	}
	frozen := LifecyclePolicy{InitialTTLSeconds: 60, MaxTotalTTLSeconds: 120, ExtendEnabled: false, ExtendSeconds: 60, ExtendMaxCount: 0, ResetEnabled: true, MaxConcurrent: 1}
	if policy := resolveInstancePolicy(&frozen, &ModuleInstanceSettings{ResetEnabled: false}, options); policy.InitialTTLSeconds != 60 || policy.ExtendEnabled || !policy.ResetEnabled {
		t.Fatalf("frozen policy must win over module settings: %+v", policy)
	}
}
