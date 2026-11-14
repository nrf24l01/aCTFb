-- P5: reserve the exact private schema/role/volume identity for every plugin.
-- New names always include a stable hash of the full plugin ID. Existing rows
-- are adopted with their legacy names on first use; ambiguity fails closed and
-- never renames or drops private plugin data.

-- +goose Up
CREATE TABLE plugin_private_identities (
    plugin_id TEXT PRIMARY KEY REFERENCES plugins(id) ON DELETE CASCADE,
    schema_name TEXT NOT NULL UNIQUE
        CHECK (schema_name ~ '^[a-z_][a-z0-9_]{0,62}$'),
    role_name TEXT NOT NULL UNIQUE
        CHECK (role_name ~ '^[a-z_][a-z0-9_]{0,62}$'),
    volume_name TEXT NOT NULL UNIQUE
        CHECK (char_length(volume_name) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS plugin_private_identities;