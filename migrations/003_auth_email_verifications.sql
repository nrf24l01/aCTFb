-- Additive email-verification token schema for the P0 auth flow.
-- The existing users.email_verified / users.email_2fa_secret columns remain unchanged;
-- this table supports hashed, single-use verification tokens without a users rewrite.
-- token_hash stores a hash of the verification token, never the raw token.

-- +goose Up
CREATE TABLE auth_email_verifications (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT auth_email_verifications_token_hash_key UNIQUE (token_hash)
);

CREATE INDEX auth_email_verifications_user_id_idx
    ON auth_email_verifications (user_id);
CREATE INDEX auth_email_verifications_expires_at_idx
    ON auth_email_verifications (expires_at);

-- +goose Down
DROP TABLE IF EXISTS auth_email_verifications;