# Backend admin CLI

The production backend image already contains the operator CLI. There is no separate bootstrap image, script,
SQL procedure, or automatic admin seed.

## First admin

After the stack is healthy and migrations have been applied by the backend container:

```bash
docker compose exec backend /app/backend admin create \
  --username admin --email admin@example.com
```

The password is requested twice with hidden terminal input. The new account is enabled, has roles
`user,admin`, and is created with `email_verified = true`. This local operator action intentionally bypasses
the email challenge; no SMTP or NATS service is required.

## Commands

| Command | Behavior |
| --- | --- |
| `backend admin create --username <name> --email <address>` | Create an enabled, verified admin. |
| `backend admin list` | Print `UUID`, `USERNAME`, `EMAIL`, and `STATUS` (`enabled admin`, `enabled user`, `disabled admin`, or `disabled user`). |
| `backend admin promote --username <name>` | Add the admin role to an existing enabled user; preserve all other roles. |
| `backend admin demote --username <name>` | Remove the admin role; keep the user account and all non-admin roles. |
| `backend admin reset-password --username <name>` | Replace an admin password, bump `auth_version`, revoke all refresh sessions, and invalidate outstanding reset tokens. |

`--id <uuid>` is the exact alternative to `--username` for promote, demote, and reset-password. Exactly one
target identifier is required. The UUID comes from `admin list`.

`promote` and `demote` are idempotent. An already-admin account or already-non-admin account prints
`no change` without bumping `auth_version`, revoking sessions, or writing a duplicate audit row. `promote`
refuses disabled accounts rather than silently enabling them. `demote` refuses to remove the last enabled admin,
using the same PostgreSQL advisory lock as the HTTP access service so CLI and HTTP cannot race past that
invariant.

`reset-password` only manages accounts that currently have the admin role. It never turns the command into a
general user-password reset tool. Resetting a disabled admin is allowed; the account remains disabled until an
operator enables it through the normal admin flow.

`create` is not a general admin-user-management replacement for the HTTP admin API. It exists to bootstrap and
repair admin access. All duplicate usernames/emails fail without partial writes.

## Non-interactive password input

Use `--password-stdin` with `docker compose exec -T` for automation:

```bash
printf '%s\n' "$ADMIN_PASSWORD" | \
  docker compose exec -T backend /app/backend admin create \
    --username admin --email admin@example.com --password-stdin
```

Or from a mode-`0600` secret file:

```bash
docker compose exec -T backend /app/backend admin create \
  --username admin --email admin@example.com \
  --password-stdin < /secure/admin-password
```

Rules:

- Never pass a password as a command-line flag, argv value, or environment variable.
- stdin is read with a hard 128-byte password bound (plus one trailing record terminator). Oversized input,
  embedded newlines, or trailing extra records fail without mutation.
- Exactly one trailing `LF` or `CRLF` is accepted; otherwise one line without a newline is accepted.
- Empty passwords, NUL bytes, and passwords outside the existing 8–128 byte policy are rejected.
- Valid spaces are password material and are not trimmed.
- Without `--password-stdin`, a non-TTY invocation fails and tells the operator to use `--password-stdin`
  instead of blocking forever.

## Offline behavior and bootstrap boundary

The CLI dispatches before config validation, migrations, HTTP startup, SMTP, Redis, NATS, S3, deployment, and
plugin initialization. It reads the existing `CONFIG_FILE` (default `config.yaml`) and PostgreSQL datasource
environment. Only PostgreSQL and Argon2id settings are validated; JWT, origin, SMTP, and feature secrets are not
required.

```bash
docker compose exec backend /app/backend admin --help
docker compose exec backend /app/backend admin create --help
```

Help never reads config or connects to PostgreSQL, so it also works without a database:

```bash
docker run --rm --entrypoint /app/backend \
  ghcr.io/nrf24l01/actfb-backend:<tag> admin --help
```

The CLI does **not** apply migrations. Start the backend once so Goose applies migrations through `017`
(`017` makes `audit_events.actor_user_id` nullable for external-operator audit rows). If the schema is missing
or older, the CLI exits with an actionable message instead of running unexpected DDL.

A bare `/app/backend` invocation with no arguments is unchanged and still starts the normal HTTP server.

## Audit and trust boundary

The external operator is trusted through OS/container and PostgreSQL access; the CLI never fabricates a JWT,
login session, principal, or admin user. Mutations, audit, `auth_version` changes, and refresh-session revocation
commit in one PostgreSQL transaction. An audit failure rolls the whole operation back.

CLI audit rows have `actor_user_id = NULL` and details containing:

```json
{"source":"cli","operator":"admin-cli"}
```

The authenticated global/board audit API keeps its real-user actor contract, so these NULL-actor CLI rows are
intentionally not presented there; they remain stored for direct operator inspection. No fake user or identity
is invented.

No password, hash, token, or password length is written to stdout, stderr, logs, or audit details. `admin list`
prints only UUID, username, email, and status.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success, help, or an idempotent `no change` result. |
| `1` | Operation/configuration/database error, including missing schema and last-enabled-admin rejection. |
| `2` | Usage error: unknown top-level command/subcommand, invalid flag, missing or conflicting target, or invalid create input. |