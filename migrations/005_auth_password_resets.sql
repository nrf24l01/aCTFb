-- Additive password-reset and auth-version schema.
-- Existing users backfill auth_version = 0; original migrations are not rewritten.
-- Reset tokens are stored only as hashes and are single-use via used_at.

-- +goose Up
ALTER TABLE users
    ADD COLUMN auth_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT users_auth_version_nonnegative CHECK (auth_version >= 0);

CREATE TABLE auth_password_resets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT auth_password_resets_token_hash_key UNIQUE (token_hash)
);

CREATE INDEX auth_password_resets_user_id_idx ON auth_password_resets (user_id);
CREATE INDEX auth_password_resets_expires_at_idx ON auth_password_resets (expires_at);

-- +goose Down
DROP TABLE IF EXISTS auth_password_resets;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_auth_version_nonnegative;
ALTER TABLE users DROP COLUMN IF EXISTS auth_version;