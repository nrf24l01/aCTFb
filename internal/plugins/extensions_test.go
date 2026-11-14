package plugins

import (
	"testing"
)

func TestExtensionRegistryValidation(t *testing.T) {
	registry := NewExtensionRegistry()
	definition := ExtensionDefinition{
		Type: ExtensionChecker, Key: "example.custom_checker", APIVersion: ExtensionAPIVersion,
		Operations: []ExtensionOperation{{
			Name: "check", Method: "POST", Path: "/check",
		}},
	}
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(definition); err != nil {
		t.Fatalf("identical reload must be idempotent: %v", err)
	}
	if def, ok := registry.Lookup(ExtensionChecker, "example.custom_checker"); !ok || def.Key != definition.Key {
		t.Fatalf("lookup = %#v %v", def, ok)
	}
	if err := registry.Register(ExtensionDefinition{Type: "other", Key: "example.x"}); err == nil {
		t.Fatal("invalid extension type must be rejected")
	}
	if err := registry.Register(ExtensionDefinition{Type: ExtensionChecker, Key: "nonspaced"}); err == nil {
		t.Fatal("non-namespaced key must be rejected")
	}
	if err := registry.Register(ExtensionDefinition{
		Type: ExtensionRuntime, Key: "example.runtime", APIVersion: "2",
	}); err == nil {
		t.Fatal("unsupported apiVersion must be rejected")
	}
	if err := registry.Register(ExtensionDefinition{
		Type: ExtensionModule, Key: "example.module",
		Operations: []ExtensionOperation{{Name: "x", Method: "GET", Path: "/../evil"}},
	}); err == nil {
		t.Fatal("unsafe operation path must be rejected")
	}
	if len(registry.List()) != 1 {
		t.Fatalf("list = %#v", registry.List())
	}
}
