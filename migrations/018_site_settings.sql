-- Organizer-controlled public landing content. This is a singleton row with
-- the id fixed to true, so there is exactly one site settings document.
-- intro_markdown is bounded to 128 KiB by the service and by the check below.
-- Existing deployments get a meaningful, editable default intro; it is never
-- a hardcoded page rendered by the frontend.

-- +goose Up
CREATE TABLE site_settings (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    title text NOT NULL DEFAULT 'Welcome',
    intro_markdown text NOT NULL DEFAULT '',
    published boolean NOT NULL DEFAULT true,
    updated_by_user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT site_settings_title_len CHECK (char_length(title) <= 128),
    CONSTRAINT site_settings_intro_bytes CHECK (octet_length(intro_markdown) <= 131072)
);

INSERT INTO site_settings (id, title, intro_markdown, published)
VALUES (
    true,
    'Welcome',
    'This is the challenge board. Browse the open boards to get started.

- Sign in to join a board and submit answers.
- Organizers can edit this introduction under **Admin → Site**.',
    true
)
ON CONFLICT (id) DO NOTHING;

-- +goose Down
DROP TABLE site_settings;
