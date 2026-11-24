package boards_test

import (
	"testing"

	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/instances"
)

// TestBoardsAndInstancesAgreeOnGlobalDefaults keeps the boards read model and the
// instances service from drifting apart on the documented global fallback. It
// lives in the external test package because internal/instances imports
// internal/boards.
func TestBoardsAndInstancesAgreeOnGlobalDefaults(t *testing.T) {
	if boards.DefaultInstanceInitialTTLSeconds != instances.DefaultInitialLifetimeSeconds {
		t.Fatalf("initial TTL defaults disagree: boards=%d instances=%d",
			boards.DefaultInstanceInitialTTLSeconds, instances.DefaultInitialLifetimeSeconds)
	}
	if boards.MaxInstanceLifetimeSeconds != instances.MaxLifetimeSeconds {
		t.Fatalf("max lifetime caps disagree: boards=%d instances=%d",
			boards.MaxInstanceLifetimeSeconds, instances.MaxLifetimeSeconds)
	}
	if boards.MaxInstanceExtensionSeconds != instances.MaxExtensionSeconds {
		t.Fatalf("extension step caps disagree: boards=%d instances=%d",
			boards.MaxInstanceExtensionSeconds, instances.MaxExtensionSeconds)
	}
	if boards.MaxInstanceExtensions != instances.MaxExtensionCount {
		t.Fatalf("extension count caps disagree: boards=%d instances=%d",
			boards.MaxInstanceExtensions, instances.MaxExtensionCount)
	}
}
