# Auth record retention worker

Status: **implemented, tested, and wired into the dev Compose stack.** This slice adds the separate retention
process required by `docs/p0-auth-hardening-plan.md` section 4. It does not add migrations, change the backend HTTP
API, or touch users, roles, passwords, or JWT material. Production still needs a least-privilege PostgreSQL role.

## What it deletes

Exactly three allowlisted tables are considered:

- `auth_sessions`
- `auth_email_verifications`
- `auth_password_resets`

A row is eligible only when:

```text
expires_at < (SELECT NOW()) - AUTH_RETENTION_DAYS days
```

The default grace period is **7 days**; `AUTH_RETENTION_DAYS` may be set to any integer **1..365**. The cutoff is
computed **once per pass by PostgreSQL** and reused for every table and batch, so there is a single clock and a
single strict boundary. `expires_at == cutoff` is not eligible.

Revoked sessions and used verification/reset tokens are **not** special-cased. They remain until their own
`expires_at` crosses the cutoff, because expiry/replay checks must stay authoritative. Unexpired rows are never
deleted, even when revoked or already used. Rows that are expired but still inside the grace window are also
preserved. Deleting an individual expired row never deletes a live sibling in the same refresh family.

`users` is never selected for deletion or update. The worker only sees `id` and `expires_at`; it never reads token
hashes, passwords, or secrets.

## Bounded batches

Each table is processed in independent transactions:

```sql
WITH victims AS (
    SELECT id
    FROM <allowlisted table>
    WHERE expires_at < $1
    ORDER BY expires_at, id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
DELETE FROM <allowlisted table> AS target
USING victims
WHERE target.id = victims.id;
```

Table names and SQL are fixed constants in `internal/authretention/retention.go`. No table name or SQL fragment is
read from an environment variable, flag, config file, or database row.

Bounds per pass:

| Setting | Default | Accepted |
|---|---:|---:|
| `AUTH_RETENTION_DAYS` | 7 | 1..365 |
| `--batch-size` | 500 | 1..5000 rows per transaction |
| `--max-batches` | 100 | 1..1000 transactions per table |
| `--run-timeout` | 5m | at least 1s |
| `--interval` (daemon only) | 1h | at least 1s |

The default maximum work is therefore 50,000 rows per table per pass (150,000 across all three tables). An
operator can raise or lower that budget explicitly. A full final batch at the `--max-batches` limit sets
`truncated=true` in logs so the budget can be tuned.

`FOR UPDATE SKIP LOCKED` allows two retention processes to run concurrently without blocking each other or
double-counting. Reported `deleted` counts come from `RowsAffected` and always describe actual removals. A run is
idempotent and safe after a crash; the next pass continues from the same rule.

## Running it

The command uses the existing shared configuration loader and PostgreSQL client. It deliberately skips
`ValidateBackend`, so JWT, SMTP, NATS, and HTTP settings are not required.

```bash
go build -o ./bin/retention ./cmd/retention
```

`config.LoadConfig` reads `CONFIG_FILE` (or `./config.yml` when unset). That file must contain
`datasources.postgres` with `host`, `port`, `user`, `password`, `dbname`, `sslmode`, and `timezone`. The worker fails
fast on missing/empty PostgreSQL fields and does not run migrations; migrations `001`-`005` must already be applied.

One bounded pass (recommended for cron or a Kubernetes CronJob):

```bash
CONFIG_FILE=/etc/actfb/config.yml \
AUTH_RETENTION_DAYS=7 \
./bin/retention --once --batch-size=500 --max-batches=100 --run-timeout=5m
```

Long-running daemon (default mode; runs immediately, then waits for `--interval`):

```bash
CONFIG_FILE=/etc/actfb/config.yml \
AUTH_RETENTION_DAYS=7 \
./bin/retention --interval=1h --batch-size=500 --max-batches=100 --run-timeout=5m
```

`SIGINT`/`SIGTERM` cancels the active pass, closes the PostgreSQL pool, and exits cleanly. In daemon mode a failed
pass is logged and retried after the interval; in `--once` mode a failed pass exits non-zero. Use `--help` for the
full flag list.

## Dev Compose service

`docker-compose.dev.yaml` defines a dedicated `retention` service. It uses the
existing `Dockerfile.dev`, compiles `cmd/retention`, and `exec`s the binary so
the worker is PID 1 and receives `SIGTERM` directly (same pattern as
`notification`). The default command is daemon mode; `--once` remains available
by running the binary directly.

```bash
make retention                         # build ./bin/retention
docker compose up -d retention         # daemon service
```

The Compose service exports **PostgreSQL-only** configuration. It does not
receive JWT, Redis, S3, SMTP, mail, or notification credentials.

| Compose input | Default | Worker flag / use |
|---|---:|---|
| `AUTH_RETENTION_DAYS` | `7` | env read by `cmd/retention` |
| `AUTH_RETENTION_INTERVAL` | `1h` | `--interval` |
| `AUTH_RETENTION_BATCH_SIZE` | `500` | `--batch-size` |
| `AUTH_RETENTION_MAX_BATCHES` | `100` | `--max-batches` |
| `AUTH_RETENTION_RUN_TIMEOUT` | `5m` | `--run-timeout` |
| `RETENTION_POSTGRES_USER` | dev backend user | optional least-privilege override |
| `RETENTION_POSTGRES_PASSWORD` | dev backend password | optional least-privilege override |

Startup ordering:

1. PostgreSQL becomes healthy.
2. The **backend** becomes healthy at `GET /ready` (verified BusyBox `wget`
   inside the existing dev image; the scoped Dockerfile does not install `curl`).
3. The backend has already applied migrations `001`–`005` before it listens.
4. Only then does Compose start `retention`.

The backend healthcheck uses the container's fixed port `8080`; `APP_PORT`
only changes the host port mapping. A temporary backend readiness failure may
mark it unhealthy but Docker does not restart it by itself.

The retention service uses `restart: unless-stopped` and bounded resources:
`cpus: 1.0`, `mem_limit: 1g`, `pids_limit: 256`. The memory allowance includes
the Go development build performed at container startup, so it is intentionally
larger than the runtime worker's steady-state needs.

The development Compose fallback uses the existing PostgreSQL development
role, which is **not** least-privilege. Production deployments must provide a
role limited to `SELECT` and `DELETE` on `auth_sessions`,
`auth_email_verifications`, and `auth_password_resets` via
`RETENTION_POSTGRES_USER` / `RETENTION_POSTGRES_PASSWORD`. No role creation or
credential is embedded in migrations.

## Logs and secrecy

Logs are JSON on stdout and contain only operational metadata:

- startup: PostgreSQL host/port/database (never password), retention days, batch size, max batches, mode, interval
- per table: table name, deleted count, batches, truncated flag, cutoff
- summary: cutoff, retention days, total deleted, truncated
- failures: error text and rows deleted before the error

No raw tokens, token hashes, passwords, JWT secrets, or row bodies are logged.

## Verification

Unit and option-boundary tests (no database required):

```bash
go test ./internal/authretention ./cmd/retention -count=1
```

Real-PostgreSQL tests use a disposable server and an isolated `actfb_test_retention_*` schema. They apply migrations
`001`-`005`, then drop only that generated schema:

```bash
ACTFB_TEST_POSTGRES_DSN='postgres://user:password@127.0.0.1:55439/actfb?sslmode=disable' \
  go test ./internal/authretention -run 'TestRun|TestDeleteBatch' -v -count=1
```

Observed on 2026-11-12: `PASS` in 0.577 s; the same suite passed under `-race` in 1.843 s. Covered gates:

- expired older than 7 days in all three tables removed; expired inside the grace window preserved
- unexpired active **and** unexpired revoked sessions preserved; unexpired used verification/reset rows preserved
- strict `expires_at < cutoff` boundary pinned directly: the exact cutoff instant and a newer row survive, an older
  row is removed, with one database clock per pass
- custom `AUTH_RETENTION_DAYS=1` applied through the database cutoff
- batch size and per-table batch budget enforced; continuation run removes the remainder; third run is a no-op
- two concurrent runs report exactly as many deletions as actually occurred, with no double count
- user row (email, password, `auth_version`, roles) unchanged; users are never deleted
- canceled context returns `context.Canceled` and deletes nothing
- invalid days/batch/budget values rejected before work starts

Command-level smoke was also run against a separate isolated schema on the disposable PostgreSQL at
`127.0.0.1:55439` (the `public` schema was not touched):

- one-shot `--once` deleted 3 old rows (one per table), preserved the unexpired/revoked/used rows, and left
  `users` unchanged; exit status 0
- daemon mode performed its first pass, then exited 0 on `SIGTERM` with `shutdown signal received`
- the smoke schemas were dropped; no non-test schema or database was modified

```bash
go build ./...
go vet ./...
go test ./... -count=1
go vet ./internal/authretention ./cmd/retention
```

All passed on the final rerun (the default `go test ./...` skips the opt-in real-PostgreSQL retention tests because
`ACTFB_TEST_POSTGRES_DSN` is unset). An earlier full-repo run was transiently blocked by a concurrent in-progress
change in `internal/users/http`; that change settled and the final repository-wide run passed. The retention
packages passed their own tests and `go vet` independently throughout.

## Remaining deployment work

The dev Compose entry is implemented. Production still needs:

- a provisioned least-privilege PostgreSQL role restricted to `SELECT`/`DELETE`
  on the three allowlisted tables; Compose exposes optional credential overrides
  but the dev fallback uses the backend development role
- metrics/alerts for deletion totals, truncation, and pass latency
- production scheduling approval (for example hourly daemon vs daily one-shot)
- application-level audit history for password resets/session revocations (none
  exists today)