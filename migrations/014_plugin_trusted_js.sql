-- P5: record the explicit per-install trusted-js risk acknowledgement.
-- Additive only: the operator-wide ACTFB_PLUGIN_ALLOW_TRUSTED_JS gate remains
-- off by default and is checked independently of this persisted approval.

-- +goose Up
ALTER TABLE plugins ADD COLUMN trusted_js_approved BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE plugins DROP COLUMN IF EXISTS trusted_js_approved;