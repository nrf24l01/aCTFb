-- Additive auth/session schema for the P0 auth flow.
-- Global roles only: scoped board/module roles (board_owner, board_editor, reviewer)
-- belong in separate bindings per aCTFb_CODEX_TASK_v4.md section 4B.
-- token_hash stores a hash of the refresh token, never the raw token.
-- Swagger Me.roles is unique; auth code must normalize/de-duplicate roles when writing.

-- +goose Up
ALTER TABLE users
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN roles TEXT[] NOT NULL DEFAULT ARRAY['user']::TEXT[],
    ADD CONSTRAINT users_roles_allowed
        CHECK (cardinality(roles) > 0 AND roles <@ ARRAY['user', 'admin']::TEXT[]);

CREATE TABLE auth_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT auth_sessions_token_hash_key UNIQUE (token_hash)
);

CREATE INDEX auth_sessions_user_id_idx ON auth_sessions (user_id);
CREATE INDEX auth_sessions_expires_at_idx ON auth_sessions (expires_at);
CREATE INDEX auth_sessions_active_user_idx
    ON auth_sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS auth_sessions;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_roles_allowed;
ALTER TABLE users DROP COLUMN IF EXISTS roles;
ALTER TABLE users DROP COLUMN IF EXISTS disabled;
ALTER TABLE users DROP COLUMN IF EXISTS created_at;