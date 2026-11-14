-- P5: installable plugin registry, permissions, event deliveries, encrypted settings, SDK delegations.
-- Additive only. 006 stays unchanged; down removes only objects created here.

-- +goose Up
-- Extensible native enums: EXTENSION uses extension_key; PLUGIN checker uses checker_key.
ALTER TABLE modules ADD COLUMN extension_key TEXT;
ALTER TABLE modules DROP CONSTRAINT IF EXISTS modules_type_check;
ALTER TABLE modules ADD CONSTRAINT modules_type_check
    CHECK (type IN ('TASKS','ATTACK_DEFENSE','PENTEST','EXTENSION'));
ALTER TABLE modules ADD CONSTRAINT modules_extension_key_check
    CHECK (type <> 'EXTENSION' OR extension_key IS NOT NULL);
ALTER TABLE tasks ADD COLUMN extension_key TEXT;
ALTER TABLE tasks ADD COLUMN checker_key TEXT;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_type_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_type_check
    CHECK (type IN ('FLAG','MANUAL','INSTANCE','EXPLOIT','PATCH','PENTEST_REPORT','EXTENSION'));
ALTER TABLE tasks ADD CONSTRAINT tasks_extension_key_check
    CHECK (type <> 'EXTENSION' OR extension_key IS NOT NULL);
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_submission_checker_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_submission_checker_check
    CHECK (submission_checker IN ('AUTOMATIC','MANUAL','PLUGIN'));
ALTER TABLE tasks ADD CONSTRAINT tasks_checker_key_check
    CHECK (submission_checker <> 'PLUGIN' OR checker_key IS NOT NULL);

CREATE TABLE plugins (
    id TEXT PRIMARY KEY CHECK (id ~ '^[a-z0-9]+(?:[._-][a-z0-9]+)+$'),
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    api_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('discovered','installed','enabled','disabled','incompatible','unhealthy','failed')),
    manifest JSONB NOT NULL,
    manifest_digest TEXT NOT NULL,
    package_path TEXT NOT NULL,
    installed_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    installed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    enabled_at TIMESTAMPTZ,
    disabled_at TIMESTAMPTZ
);

CREATE TABLE plugin_versions (
    plugin_id TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    version TEXT NOT NULL,
    manifest_digest TEXT NOT NULL,
    package_path TEXT NOT NULL,
    installed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id, version)
);

CREATE TABLE plugin_permissions (
    plugin_id TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    permission TEXT NOT NULL CHECK (permission ~ '^plugin\.[a-z0-9_.-]+$'),
    scopes TEXT[] NOT NULL CHECK (cardinality(scopes) > 0),
    description TEXT NOT NULL DEFAULT '',
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id, permission)
);

CREATE TABLE plugin_event_deliveries (
    plugin_id TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    event_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed','dead')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error TEXT,
    next_attempt_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    PRIMARY KEY (plugin_id, event_id)
);
CREATE INDEX plugin_event_deliveries_pending_idx
    ON plugin_event_deliveries (status, next_attempt_at) WHERE status IN ('pending','failed');

CREATE TABLE plugin_settings (
    plugin_id TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    key TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128),
    value_ciphertext BYTEA NOT NULL,
    key_version INTEGER NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id, key)
);

CREATE TABLE plugin_migrations (
    plugin_id TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    version TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id, version)
);

-- Core-issued HMAC delegation: token is not stored; nonce/hash allow replay/revocation checks.
CREATE TABLE plugin_delegations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plugin_id TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    board_id UUID,
    module_id UUID,
    nonce UUID NOT NULL,
    token_hash BYTEA NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    UNIQUE (nonce),
    UNIQUE (token_hash)
);
CREATE INDEX plugin_delegations_lookup_idx
    ON plugin_delegations (plugin_id, expires_at) WHERE revoked_at IS NULL;

-- +goose Down
-- Plugin/extension-specific rows cannot satisfy native-only checks; down removes
-- only those rows, never P0/P1 native data.
DELETE FROM tasks WHERE type = 'EXTENSION' OR submission_checker = 'PLUGIN';
DELETE FROM modules WHERE type = 'EXTENSION';
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_checker_key_check;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_submission_checker_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_submission_checker_check
    CHECK (submission_checker IN ('AUTOMATIC','MANUAL'));
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_extension_key_check;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_type_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_type_check
    CHECK (type IN ('FLAG','MANUAL','INSTANCE','EXPLOIT','PATCH','PENTEST_REPORT'));
ALTER TABLE tasks DROP COLUMN IF EXISTS checker_key;
ALTER TABLE tasks DROP COLUMN IF EXISTS extension_key;
ALTER TABLE modules DROP CONSTRAINT IF EXISTS modules_extension_key_check;
ALTER TABLE modules DROP CONSTRAINT IF EXISTS modules_type_check;
ALTER TABLE modules ADD CONSTRAINT modules_type_check
    CHECK (type IN ('TASKS','ATTACK_DEFENSE','PENTEST'));
ALTER TABLE modules DROP COLUMN IF EXISTS extension_key;

DROP TABLE IF EXISTS plugin_delegations;
DROP TABLE IF EXISTS plugin_migrations;
DROP TABLE IF EXISTS plugin_settings;
DROP TABLE IF EXISTS plugin_event_deliveries;
DROP TABLE IF EXISTS plugin_permissions;
DROP TABLE IF EXISTS plugin_versions;
DROP TABLE IF EXISTS plugins;