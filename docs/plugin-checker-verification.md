# Plugin checker verification (P5 gap closure)

Date: 2026-11-13. Scope: `internal/plugins/**`, `internal/boards/**`, `cmd/backend/**`,
`examples/plugins/**`, `docs/plugin-system.md`, `docs/plugin-developer-guide.md`, contract-test coverage.
No migration, dependency or schema change was needed; current `013_manual_text.sql` was used. The existing canonical
Swagger/generated models already declare `ModuleType.EXTENSION`, `TaskType.EXTENSION`, `SubmissionChecker.PLUGIN`,
`extensionKey` and `SubmissionPolicy.checkerKey`, so no Swagger regeneration was necessary. Contract tests now validate
a plugin-checker submission policy and EXTENSION task model.

## Gap closed

Before this slice `plugins.CheckerV1`, `boards.PluginChecker` and `tasks.checker_key` existed only as declarations.
There was no manifest checker operation, no versioned HTTP invocation, no board persistence/validation, and no example.
Now:

- `Manifest.Checkers[]` declares a text-only checker key, extension key, declared `POST` route, board permission/scope,
  timeout and max text size.
- `EXTENSION` modules persist/validate `extension_key`; `EXTENSION` tasks persist/validate `extension_key` and
  `checker_key` with `submission.mode=TEXT`, `checker=PLUGIN`.
- Module/task configuration requires an enabled compatible plugin and a **live explicit admin grant** for the checker
  permission. There is no auto-grant, including for global admins.
- Core invokes the checker through the enabled plugin manager over the versioned `operation=checker` HTTP v1 operation,
  with the actual end-user delegation. The checker receives bounded text/config only: no flags, accepted-answer hashes,
  file bytes, provider credentials or student code. It does not execute student code.
- Core validates strict JSON, verdict and score bounds. `ACCEPTED` awards the **frozen task points**; the plugin score
  is ignored. `REJECTED` awards nothing. Invalid verdict/score/message, timeout, disabled/crashed provider, revoked
  grant and unknown checker key fail closed with no scored submission.
- The canonical submission, `submission.judged` outbox event, audit row, idempotency, one-accepted constraint and
  authoritative point snapshot remain core-owned in the existing submission transaction.
- `cmd/backend` now wires the scoped `/api/v2/plugin-sdk/v1/*` service-token/delegation endpoints and
  `boards.WithPluginChecker`; the examples use the real delegation path.
- Fixed a protocol envelope bug: `bodyBase64` now uses standard base64 as documented (previously Raw URL encoding made
  separately built plugins reject the body).

## Files

- `internal/plugins/contracts.go`, `manifest.go`: checker declaration, strict validation, digest coverage.
- `internal/plugins/checker.go`: provider lookup, grant/config validation, delegation, HTTP checker invocation, result
  bounds and fail-closed errors.
- `internal/plugins/protocol/httpv1.go`: additive `OperationChecker`.
- `internal/boards/types.go`, `inputs.go`, `validation.go`, `external.go`, `service_admin_modules.go`,
  `service_admin_tasks.go`, `service_tasks.go`, `service_admin_boards.go`, `postgres_modules.go`, `postgres_tasks.go`,
  `postgres_submissions.go`: EXTENSION/PLUGIN persistence, configuration and core-owned verdict path.
- `internal/boards/http/*`: request/mapper support for `extensionKey`, `checkerKey`, EXTENSION module type.
- `cmd/backend/plugin_checker.go`, `main.go`, `routes_integration.go`: boards↔manager adapter and SDK route wiring.
- `examples/plugins/example.custom_checker/**`: separate buildable module, own `go.mod`, no core import.
- Tests: `internal/plugins/checker_pg_test.go`, `internal/boards/plugin_checker_validation_test.go`,
  `internal/boards/http/plugin_checker_mapping_test.go`, contract tests.
- Docs: this file, `docs/plugin-system.md`, `docs/plugin-developer-guide.md`.

## Test evidence

Passed locally with real PostgreSQL 18 (migrations `001..013`) and real plugin child processes over Unix sockets:

```bash
ACTFB_TEST_POSTGRES_DSN='postgres://actfb:actfb@127.0.0.1:55441/actfb?sslmode=disable' \
ACTFB_TEST_NATS_URL='nats://127.0.0.1:14222' \
go test ./internal/plugins -count=1
# ok

ACTFB_TEST_POSTGRES_DSN='postgres://actfb:actfb@127.0.0.1:55441/actfb?sslmode=disable' \
go test ./internal/boards/... -count=1
# ok

go test ./internal/contracttests ./cmd/backend -count=1
# ok

ACTFB_TEST_POSTGRES_DSN='postgres://actfb:actfb@127.0.0.1:55441/actfb?sslmode=disable' \
go test -race ./internal/plugins -run TestPluginCheckerEndToEndPG -count=1
# ok

go vet ./internal/plugins/... ./internal/boards/... ./cmd/backend
# ok

go test ./...
# ok (all packages)
```

`TestPluginCheckerEndToEndPG` covers: separate build/install/enable; no auto-grant rejection; EXTENSION module/task;
unknown checker key; real process HTTP check with service token + core SDK delegation and actual user proof; accepted
canonical submission at frozen task points; audit and `submission.judged` score; idempotent replay returns the same
submission; second accepted attempt rejected; malicious out-of-range score rejected with no row; timeout fail-closed;
grant revoke blocks configuration and execution; disable blocks execution; re-enable succeeds; crash→503/no score;
final scoreboard 80 for accepted task points 50+30.

Existing P5 lifecycle/proxy/event tests, boards P1 tests and contract tests still pass after the additive SQL/mapping
changes.

## Mandatory spec items: implemented / blocked

- `Manifest.Dependencies` is implemented with strict declaration validation, semantic ranges, cycle detection and
  missing/disabled/incompatible guards on install, enable, upgrade, disable and remove. Real-PostgreSQL tests cover
  both the resolver and the actual lifecycle methods.
- `trusted-js` package acceptance and browser mounting are implemented behind two explicit gates: operator
  `ACTFB_PLUGIN_ALLOW_TRUSTED_JS=true` (default false) and per-install/upgrade approval persisted by migration `014`.
  The host verifies the exact bundle SHA-256 before importing a self-contained ESM and never receives JWTs or
  service tokens. Ordinary addons continue to use the opaque separate-origin iframe.
- Real external OCI plugin acceptance remains blocked by absent image/registry credentials; the trusted runner path is
  compiled and policy-tested, but not claimed as production-verified.
- Plugin checker results intentionally support only `ACCEPTED`/`REJECTED`. `PENDING_REVIEW` for plugin checkers is not
  implemented; the core fails closed rather than creating an unreviewable row.
- Vue iframe host/sidebar implementation remains frontend-owned; the opaque iframe lifecycle is
  browser-verified. Trusted-js rendering is deliberately not mounted.
- `docs/all-stages-verification.md` and `docs/implementation-plan.md` record the checker,
  dependency resolution and gated trusted-js acceptance as closed; external OCI credentials remain
  the honest blocker.
