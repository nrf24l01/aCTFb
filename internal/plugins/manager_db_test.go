package plugins

import "testing"

func TestPluginSchemaNameIsCollisionResistantAndBounded(t *testing.T) {
	ids := []ID{
		"example.score_events",
		"example_score_events",
		"example-score-events",
		"example.score.events",
		"example.score-events",
		"vendor.a_very_long_but_valid_plugin_identifier_with_many_segments",
	}
	seen := make(map[string]ID, len(ids))
	for _, id := range ids {
		name := pluginSchemaName(id)
		if len(name) == 0 || len(name) > 63 {
			t.Fatalf("schema name for %q has length %d, want 1..63", id, len(name))
		}
		if previous, exists := seen[name]; exists {
			t.Fatalf("schema collision: %q and %q both map to %q", previous, id, name)
		}
		seen[name] = id
	}
}

func TestPluginSchemaNameStableForSameID(t *testing.T) {
	id := ID("example.score_events")
	if first, second := pluginSchemaName(id), pluginSchemaName(id); first != second {
		t.Fatalf("schema name is not stable: %q != %q", first, second)
	}
}
