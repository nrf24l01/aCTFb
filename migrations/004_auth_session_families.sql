-- Refresh-token family and revocation-reason migration.
-- Additive only: migration 002 is not rewritten. Existing sessions remain valid.
-- New sessions set a random family_id; rotation must inherit the family.
-- Reuse revokes only the affected family. revoked_reason is diagnostic metadata;
-- no check constraint is required by the approved contract.

-- +goose Up
ALTER TABLE auth_sessions ADD COLUMN family_id UUID;
UPDATE auth_sessions SET family_id = id WHERE family_id IS NULL;
ALTER TABLE auth_sessions ALTER COLUMN family_id SET NOT NULL;

ALTER TABLE auth_sessions ADD COLUMN revoked_reason TEXT;
UPDATE auth_sessions
SET revoked_reason = 'legacy'
WHERE revoked_at IS NOT NULL AND revoked_reason IS NULL;

CREATE INDEX auth_sessions_family_id_idx ON auth_sessions (family_id);

-- +goose Down
DROP INDEX IF EXISTS auth_sessions_family_id_idx;
ALTER TABLE auth_sessions DROP COLUMN IF EXISTS revoked_reason;
ALTER TABLE auth_sessions DROP COLUMN IF EXISTS family_id;