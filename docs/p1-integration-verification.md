# P1 core integration verification

Date: 2026-11-13 (Europe/Moscow, UTC+03:00). Historical 2026-11-12 evidence is preserved where noted.
Verifier model: BobrSeek v4.1 Flash (`bobr-cli-proxy`, `ali/deepseek-v4.1-flash`).

Scope: `internal/httpapi/auth.go`, `internal/httpapi/auth_test.go`, `cmd/backend/**`,
`internal/integration/**`, `.github/workflows/go.yml`, root `e2e/**`, the approved profiled frontend
service in `docker-compose.dev.yaml`, and this document. No `internal/boards/**`,
`internal/authorization/**`, `internal/users/**`, migrations, generated OpenAPI, frontend product code,
`go.mod`, or other docs were edited. No commit or staging was performed.

## Verdict

**P1 is LOCALLY ACCEPTED for the approved first-slice scope: PRIVATE + INDIVIDUAL + TASKS + FLAG,
33 existing operations plus 6 additive scoped RBAC/audit operations.** The independent security review
is GREEN and accepted this slice. All review findings **P1-01 through P1-05**, plus reviewer follow-ups
**P1-03 R/L2/L3**, are fixed and verified; there is no current pending role-expiry finding.

The verifier runs below are preserved as my evidence and were not falsely rerun. The latest independent
acceptance rerun from reviewer session `ses_e30a5083bffePMz6F1cRLVoFSb` is recorded separately.

| Evidence set | Result |
|---|---|
| Latest independent P1 real-HTTP rerun | **PASS, 43.21 s** |
| Latest independent Docker-profile browser | **PASS, 12.1 s test / 12.6 s total** |
| Latest independent Go test/vet/build/race | **PASS** |
| Latest independent frontend typecheck/build + unit | **PASS, 39 tests** |
| PG expiry barriers / atomic audit / scope-IDOR / canonical six responses | **PASS** |
| My verifier P1 HTTP + nullable-schema unit | **PASS, 123.69 s** (earlier 41.14 s) |
| My verifier Docker-profile browser | **PASS, 12.4 s test / 12.9 s total** |
| My verifier P0 fast / full RustFS | **PASS, 10.01 s / 76.92 s** |

Accepted scope is deliberately bounded: PRIVATE visibility, INDIVIDUAL participation, TASKS modules,
FLAG/TEXT/AUTOMATIC tasks, no teams/public/audiences/instances/files, and additive scoped RBAC/audit.
Unsupported policy remains a real 422 negative; backend validation was not relaxed.

## Shared auth adapter

`internal/httpapi/auth.go` is the shared dependency delivered first:

- exact `AccessTokenAuthenticator` contract;
- strict Authorization Bearer only (no URL token, cookie token, forwarded identity, or extra tokens);
- standard 401 `ApiError` with UUID trace fallback and no token/password leakage;
- `CurrentPrincipal` and `PrincipalFromUser` copy only live user ID and deduplicated global roles.

`auth_test.go` passes strict-header/no-credential-leak/principal tests.

## Migration and P0 regression gates

Migration validation requires Goose **001–006** plus the P1 tables. P0 fast smoke and the full
RustFS/retention/Redis/email stack remain green at the timings above. The full-stack subtest is named
`fresh_migrations_001-006`.

## Real HTTP P1 regressions and strict response validation

The P1 test uses the isolated Compose harness plus operator-only SQL and covers the approved first
slice, including:

- admin board list fix, PRIVATE/INDIVIDUAL/OPEN and CLOSED boards, modules, FLAG tasks, publish;
- admin/owner/editor/reviewer grants, owner escalation denial, board-wide vs module-scoped editors,
  revocation/expiry and audit;
- **P1-04:** an editor assigned **two of three** modules receives exactly those two in participant
  and admin module lists, permissions metadata lists exactly those two, both assigned modules are
  editable, and the sibling is denied;
- **P1-05:** joined participant in a published board receives only approved minimal UPCOMING
  `ModuleListItem` metadata (no description/task content) until ACTIVE; `GetModule` returns **403**
  while upcoming. This is the approved minimal contract, not a remaining medium issue;
- **P1-02:** after the board end is moved into the past in the isolated DB, submit returns
  **409 `BOARD_FINISHED`** and writes no submission row/no score;
- **P1-03:** after the owner binding is committed revoked, the former owner's grant attempt returns
  **403**;
- private/unknown/forged-parent access without data leakage;
- unsupported TEAM/PUBLIC/MANUAL policy 422 without writes; intentionally custom text bounds remain
  negative cases and were not replaced;
- join, wrong/correct flag, own history, scoreboard, idempotency replay/conflict, already-solved,
  concurrent double-score prevention, Argon2id PHC flag storage and log scrub.

Task creation fixtures now omit the optional `submission.text` policy
(`{"mode":"TEXT","checker":"AUTOMATIC"}`), matching the backend’s real default and the frontend policy,
without relaxing backend validation.

### Strict live schema validation

Actual HTTP JSON is validated with the existing `go-openapi/loads` + `validate` dependencies against
the expanded source `docs/swagger.yaml` for the six canonical models:

```text
Board, Module, Task, AdminModule, AdminTask, AdminSubmission
```

All six live responses pass. This also proves no `{UUID:...}` scalar wrapper and no fake instance
nodes/instance config for disabled instances in these responses. `BoardMe.team`,
`AdminModule.taskInstanceSettings`, and `SubmissionPolicy.files` zero-object guards remain.

`BoardRoleBinding` is validated live with a test-only Swagger 2.0 adapter. The source schema already
declares `x-nullable: true` on `moduleUuid`, `expiresAt`, and `revokedAt`; the stock JSON Schema
validator ignores that vendor extension. The adapter recursively rewrites each `x-nullable:true`
schema into `anyOf[original-without-x-nullable, {"type":"null"}]` after expansion, retaining every
original constraint (`allOf`, required, additionalProperties, enum, pattern, format). It does not
globally relax validation. Unit cases prove nulls are accepted while invalid UUIDs, dates, role enums,
and extra properties are still rejected. Live BoardRoleBinding responses with null module/expiry/
revoked and with a non-null proper module UUID both validate. This corrects the earlier residual:
there is no API defect here.

## Docker-profile frontend and browser acceptance

`docker-compose.dev.yaml` now has a `frontend` profile service:

- builds pinned `frontend/Dockerfile.dev` (`oven/bun:1.4.4-alpine` digest);
- `VITE_DEV_PROXY_TARGET=http://backend:8080`, `VITE_API_BASE_URL=/api`;
- loopback `${FRONTEND_PORT:-5173}:5173`;
- bind `./frontend:/app` plus own `frontend-node-modules:/app/node_modules` volume;
- depends on healthy `backend`, on `actfb-net`.

Default P0 startup does not include the profile. The isolated runner starts the profile, verifies a
real `POST /api/v2/auth/login` through the Docker frontend proxy, and then runs Playwright against
`http://localhost:5173`. The browser flow has **no admin-list workaround**: it creates the board in
the UI, asserts the board appears in the real admin list, clicks it, creates a TASKS module and FLAG
task, publishes, then a separate student joins/submits/history/scoreboard. A second independent
student/scorer verifies both 100-point scoreboard rows. Mobile student runs at 390×844 with the flag
field and submit button visible, no horizontal overflow, and a recorded full-page screenshot. Outsider
context sees private-board denial and no management/task actions.

## Frontend gates

```text
bun install --frozen-lockfile  PASS
bun run typecheck             PASS
bun run test                  PASS, 8 files / 39 tests
bun run build                 PASS, 691 ms
```

## CI

`.github/workflows/go.yml` has real PostgreSQL/Redis, P0 Docker, RustFS full-stack, frontend Bun,
P1 real-HTTP, and Playwright browser jobs plus frontend/e2e path triggers. `actionlint` passes.
Remote GitHub execution remains unverified; no push or commit was performed.

## Residual gaps

- `submission.read` remains a post-read audit completeness item; it is non-blocking for the accepted
  first slice.
- Remote GitHub CI remains unverified locally. No commit or push was performed.
- Existing MinIO object data was not touched. Any MinIO-to-RustFS migration remains a conditional
  manual operator action, not part of P1 acceptance.

No pending P1-01..P1-05, RBAC role-expiry, atomic-audit, L2, or L3 finding remains.

## Evidence

- Reviewer acceptance session `ses_e30a5083bffePMz6F1cRLVoFSb`: latest 43.21 s P1 HTTP and 12.1 s/12.6 s Docker-profile browser rerun.
- `/tmp/opencode/p1-validator-followup.log` — final P1 HTTP + nullable-schema unit PASS
- `/tmp/opencode/p1-followup-e2e.log` — final Docker-profile Playwright PASS
- `/tmp/opencode/actfb-e2e-098c00ae/` — profile browser artifacts/proxy login
- `/tmp/opencode/p1-followup-p0.log`
- `/tmp/opencode/p1-followup-fullstack.log`
- `/tmp/opencode/p1-followup-go-*.log`
- `/tmp/opencode/p1-followup-bun-install.log`