-- Task instance lifetime policy: per-task overrides, persisted module instance
-- settings and idempotent extension requests.
--
-- Additive only. 001-018 stay unchanged. Every new column is nullable so legacy
-- rows keep their current behaviour: a NULL override means "inherit the module
-- setting", and a module with no settings row keeps the global defaults that
-- internal/instances Options already provides (1h initial, 7d maximum total,
-- +30m x3 extensions).
--
-- Bounds are enforced here as a second line of defence. Business rules that need
-- cross-field knowledge (default <= max, task <= module) stay in Go so the API
-- can return field-specific messages instead of a raw constraint error.

-- +goose Up
ALTER TABLE modules
    ADD COLUMN task_instance_settings JSONB;

ALTER TABLE modules
    ADD CONSTRAINT modules_task_instance_settings_object
        CHECK (task_instance_settings IS NULL OR jsonb_typeof(task_instance_settings) = 'object');

ALTER TABLE tasks
    ADD COLUMN instance_ttl_seconds INTEGER,
    ADD COLUMN instance_max_ttl_seconds INTEGER,
    ADD COLUMN instance_extension_enabled BOOLEAN,
    ADD COLUMN instance_extension_seconds INTEGER,
    ADD COLUMN instance_extension_max_count INTEGER;

ALTER TABLE tasks
    ADD CONSTRAINT tasks_instance_ttl_seconds_range
        CHECK (instance_ttl_seconds IS NULL OR instance_ttl_seconds BETWEEN 60 AND 604800),
    ADD CONSTRAINT tasks_instance_max_ttl_seconds_range
        CHECK (instance_max_ttl_seconds IS NULL OR instance_max_ttl_seconds BETWEEN 60 AND 604800),
    ADD CONSTRAINT tasks_instance_extension_seconds_range
        CHECK (instance_extension_seconds IS NULL OR instance_extension_seconds BETWEEN 60 AND 86400),
    ADD CONSTRAINT tasks_instance_extension_max_count_range
        CHECK (instance_extension_max_count IS NULL OR instance_extension_max_count BETWEEN 0 AND 100),
    ADD CONSTRAINT tasks_instance_lifetime_order
        CHECK (instance_ttl_seconds IS NULL OR instance_max_ttl_seconds IS NULL
               OR instance_ttl_seconds <= instance_max_ttl_seconds),
    -- Extension overrides are only meaningful together; a partial set would be
    -- ambiguous about which fields inherit.
    ADD CONSTRAINT tasks_instance_extension_complete
        CHECK (instance_extension_enabled IS NULL
               OR (instance_extension_seconds IS NOT NULL AND instance_extension_max_count IS NOT NULL)),
    -- Lifetime overrides only exist for instance-enabled tasks.
    ADD CONSTRAINT tasks_instance_lifetime_requires_enabled
        CHECK (instance_enabled
               OR (instance_ttl_seconds IS NULL AND instance_max_ttl_seconds IS NULL
                   AND instance_extension_enabled IS NULL
                   AND instance_extension_seconds IS NULL
                   AND instance_extension_max_count IS NULL));

-- One row per (instance, client Idempotency-Key). A retried EXTEND returns the
-- original outcome instead of consuming a second extension credit. The request
-- hash and actor let the service reject a key reused for a different request or
-- by a different subject without leaking the earlier response.
CREATE TABLE instance_extension_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id UUID NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    request_key TEXT NOT NULL CHECK (char_length(request_key) BETWEEN 16 AND 128),
    request_hash TEXT NOT NULL CHECK (char_length(request_hash) BETWEEN 1 AND 128),
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('USER','TEAM')),
    subject_id UUID NOT NULL,
    extension_count_after INTEGER NOT NULL CHECK (extension_count_after >= 0),
    expires_at_after TIMESTAMPTZ,
    granted_seconds INTEGER NOT NULL CHECK (granted_seconds >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (instance_id, request_key)
);
CREATE INDEX instance_extension_requests_instance_idx
    ON instance_extension_requests (instance_id, created_at DESC);

-- +goose Down
DROP TABLE instance_extension_requests;

ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS tasks_instance_lifetime_requires_enabled,
    DROP CONSTRAINT IF EXISTS tasks_instance_extension_complete,
    DROP CONSTRAINT IF EXISTS tasks_instance_lifetime_order,
    DROP CONSTRAINT IF EXISTS tasks_instance_extension_max_count_range,
    DROP CONSTRAINT IF EXISTS tasks_instance_extension_seconds_range,
    DROP CONSTRAINT IF EXISTS tasks_instance_max_ttl_seconds_range,
    DROP CONSTRAINT IF EXISTS tasks_instance_ttl_seconds_range;

ALTER TABLE tasks
    DROP COLUMN IF EXISTS instance_extension_max_count,
    DROP COLUMN IF EXISTS instance_extension_seconds,
    DROP COLUMN IF EXISTS instance_extension_enabled,
    DROP COLUMN IF EXISTS instance_max_ttl_seconds,
    DROP COLUMN IF EXISTS instance_ttl_seconds;

ALTER TABLE modules
    DROP CONSTRAINT IF EXISTS modules_task_instance_settings_object;

ALTER TABLE modules
    DROP COLUMN IF EXISTS task_instance_settings;
