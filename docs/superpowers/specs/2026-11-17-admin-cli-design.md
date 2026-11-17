# Backend admin CLI — design and task checklist

Status: implementation in progress (2026-11-17)

## Goal

Operators manage the first admin and later admin accounts through the existing backend binary, without
separate images, SQL snippets, shell helpers, or a bootstrap admin secret.

```bash
docker compose exec backend /app/backend admin create --username admin --email admin@example.com
docker compose exec backend /app/backend admin list
docker compose exec -T backend /app/backend admin create --username admin \
  --email admin@example.com --password-stdin < /secure/admin-password
```

## Command surface

| Command | Purpose |
| --- | --- |
| `admin create` | Create an enabled, email-verified user with roles `user,admin`. |
| `admin list` | Print UUID, username, email, and enabled/disabled + admin/user status. |
| `admin promote` | Grant `admin` to an existing user; preserve all other roles. |
| `admin demote` | Remove `admin`; preserve the account and prevent removing the last enabled admin. |
| `admin reset-password` | Reset an admin password and revoke all refresh sessions. |

Target identity for promote/demote/reset is exactly one of `--username` or `--id`. `create` requires
`--username` and `--email`. Passwords never appear in argv or environment variables.

`--password-stdin` takes a bounded single stdin record. On a terminal, the CLI prompts twice with hidden
input. A non-TTY without `--password-stdin` fails with a clear error instead of blocking.

## Bootstrap boundary

- `backend` with no arguments starts HTTP exactly as before.
- CLI dispatch happens before config validation, HTTP startup, migrations, SMTP/Redis/NATS/S3/plugin init.
- `admin --help` and `admin create --help` do not read config, connect to PostgreSQL, validate JWT/SMTP
  secrets, or start the server.
- CLI reads the existing `CONFIG_FILE` YAML / PostgreSQL datasource environment via
  `config.LoadForAdmin`; only PostgreSQL and Argon2id fields are validated.
- The CLI never applies migrations and reports a clear message on a missing/stale schema.
- The trusted operator is the OS/PostgreSQL access boundary. No fake JWT, principal, user row, or session
  is invented. Audit rows use `actor_user_id = NULL` with `details.source = "cli"` and
  `details.operator = "admin-cli"`.

## Data and security rules

- Created admins are enabled and `email_verified = true`; this local operator action intentionally bypasses
  the email challenge and does not require SMTP/NATS.
- Passwords use the existing Argon2id config and existing 8–128 byte policy; blanks, NUL bytes, and
  oversized stdin records are rejected. Valid spaces are never silently trimmed.
- Promote rejects disabled accounts; it never re-enables them implicitly.
- Demote preserves the user account and other roles. The last-enabled-admin check uses the same
  `p1-admin-users` PostgreSQL advisory lock as the HTTP access service, including the disabled-admin
  semantics of the existing implementation.
- Role changes and password resets bump `auth_version` and revoke all active refresh sessions.
- Mutation, audit, version bump, and session revocation commit in one transaction; audit failure rolls
  everything back.
- Idempotent promote/demote calls report no change without a version bump, session revocation, or
  duplicate audit row.
- Password hashes, tokens, and plaintext passwords are never printed or stored in audit details.

## Deliberate minimal schema change

`audit_events.actor_user_id` is `NOT NULL` in migration `006`, so an unauthenticated CLI operator cannot be
represented. Migration `017` makes this single column nullable. No actor user is fabricated. Existing
authenticated writes keep their current behavior; CLI writes store the operator marker in JSONB details.

## Task checklist

- [x] Add `LoadForAdmin` / admin-only config validation and tests.
- [x] Add command parsing/help/invalid-command tests, then implementation.
- [x] Add bounded stdin/password policy tests, then implementation.
- [x] Add PostgreSQL admin service tests (create/list/login, promote/demote/idempotency, last-admin
      concurrency with HTTP access, reset/session/JWT invalidation, audit rollback/no-secret scan),
      then implementation and migration `017`.
- [x] Dispatch from `cmd/backend` before HTTP bootstrap and keep no-argument startup unchanged.
- [x] Add real-PostgreSQL tests to the existing CI integration job and image help check.
- [x] Replace operator SQL first-admin docs with CLI commands; add `docs/admin-cli.md`.
- [x] Run focused tests, `go test ./...`, `go vet ./...`, and review the diff.
- [ ] Commit CLI/tests and docs/CI separately, push, and verify Actions/image.