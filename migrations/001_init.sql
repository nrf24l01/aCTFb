
-- Clean-dev repair before first successful application (2026-11-12).
-- The trailing comma after email_send_at made this migration unparseable. A read-only
-- audit of actfb_postgres-data found no goose_db_version and no users table, so no
-- applied migration history is being rewritten. For any database where this migration
-- was actually applied, use an additive migration instead of editing this file.

-- +goose Up
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    username VARCHAR(255) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password TEXT NOT NULL,

    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    email_2fa_secret VARCHAR(40) NOT NULL DEFAULT '',
    email_send_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS users;
