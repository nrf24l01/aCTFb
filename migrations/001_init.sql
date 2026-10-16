
-- +goose Up
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    username VARCHAR(255) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password TEXT NOT NULL,

    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    email_2fa_secret VARCHAR(40) NOT NULL DEFAULT '',
    email_send_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
);

-- +goose Down
DROP TABLE IF EXISTS users;
