package attackdefense

import "testing"

func TestEventRegistryDeclaresADRunCompletion(t *testing.T) {
	registry := EventRegistry()
	if err := registry.ValidateCorePublish(EventADRunCompleted); err != nil {
		t.Fatalf("AD completion event is not declared for publish: %v", err)
	}
	if EventADRunCompleted == "" {
		t.Fatal("AD completion event type is empty")
	}
}
