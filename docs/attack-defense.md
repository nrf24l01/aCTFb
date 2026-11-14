# Attack+Defense (P4) — implementation handoff

Date: 2026-11-13. Status: **implemented vertical slice for the 22 documented
Attack+Defense operations**, the durable isolated worker, provider Git adapters,
signed push webhooks, idempotent awards and read-only board score integration.
Schema `011`/`012` is used as landed; **no new DDL is required and no migration,
Swagger, generated model, boot, router, Compose, integration CRUD, boards,
events, authorization, access, deployment, runtime, files or frontend file was
edited by this lane.**

## 1. Delivered surface

Participant routes under
`/api/v2/boards/{boardUuid}/modules/{moduleUuid}/attack-defense`:

| Op | Meaning |
|---|---|
| `GET /repositories` | own source/exploit clone/web URL and branch head, token-free |
| `GET /progress` | only discovered vulnerabilities; hidden ones only counted |
| `GET /runs` | own subject runs, `kind`/`status` filters |
| `POST /runs` | manual EXPLOIT/PATCH enqueue with required `Idempotency-Key` |
| `GET /runs/{runUuid}` | own run; hidden definitions filtered out |
| `GET /runs/{runUuid}/logs` | own sanitized logs |

Admin routes under
`/api/v2/admin/boards/{boardUuid}/modules/{moduleUuid}/attack-defense`:

| Op | Auth (module scope) |
|---|---|
| `GET|PUT /config` | `module.update` |
| `GET|POST /vulnerabilities` | `module.tasks.manage` |
| `GET|PATCH|DELETE /vulnerabilities/{vulnerabilityUuid}` | `module.tasks.manage` |
| `GET|POST /repositories` | `module.tasks.manage` |
| `GET|PATCH|DELETE /repositories/{repositoryBindingUuid}` | `module.tasks.manage` |
| `GET /runs`, `GET /runs/{runUuid}` | `module.submissions.read` |
| `POST /runs/{runUuid}/actions` | `module.submissions.review` |

Webhook: `POST /api/v2/webhooks/git/{repositoryBindingUuid}` (repository binding
credential only, never a user token).

All paths are `/api/v2`; no v3, no ordinary task/submission resource, no
`EXPLOIT`/`PATCH` task rows, no board route fork. The HTTP adapter registers
exactly 22 routes (`internal/attackdefense/http` route-count test).

Admin models intentionally expose hidden test selectors; participant responses
never include selectors, hidden vulnerability keys/titles, checker test names or
raw logs. Repository tokens and webhook secrets never appear in any response.

### Route-identifier note

Swagger names the admin repository path parameter `repositoryBindingUuid`, but
one `ADRepositoryAssignment` has **two** bindings and the create request creates
one assignment. The only unambiguous identifier is the assignment UUID, so the
adapter treats that path value as the assignment UUID. Webhooks use a real
`repository_bindings.id`. This naming mismatch is documented rather than hidden.

## 2. Package layout (new files only)

```text
internal/attackdefense/
  types.go, errors.go            domain model, limits, sentinel errors
  credentials.go                 integration resolver + exact AAD handshake
  gitprovider.go                 provider abstraction, URL/ref/SHA validation
  provider_github.go             real GitHub REST adapter
  provider_gitlab.go             real GitLab REST adapter
  provider_gitea.go              real Gitea REST adapter
  webhook.go                     raw-body signature verification + dedupe/parse
  checker.go                     Docker CLI isolation + strict verdict parser
  fetch.go                       exact-commit git fetch + sanitized checkout
  store*.go                      PostgreSQL reads/writes, atomic verdict tx
  service*.go                    participant/admin/webhook business rules
  worker.go                      durable run execution
  jobs.go                        NATS work-queue wake-ups
  module.go                      module-provider metadata + ScoreSourceAdapter
  http/                          the 22 route adapter
cmd/ad-worker/                   separate trusted worker process
examples/attack-defense/         safe fixtures, checker image, local acceptance
docs/attack-defense.md           this file
```

## 3. Constructors and wiring (exact exports)

```go
func NewService(db *postgres.DB, authz authorization.Authorizer, sealer secrets.Sealer, opts ...Option) (*Service, error)

func WithIntegrationResolver(resolver IntegrationResolver) Option
func WithGitProviderFactory(factory *GitProviderFactory) Option
func WithChecker(checker Checker) Option
func WithGitFetcher(fetcher *GitFetcher) Option
func WithRunnerImageAllowlist(images []string) Option
func WithWebhookBaseURL(baseURL string) Option
func WithRepositoryProvisioning(enabled bool) Option
func WithJobPublisher(publisher JobPublisher) Option
func WithAllowInsecureWebhookTransport(enabled bool) Option
func WithKeyVersion(version uint32) Option
func WithLogger(logger *slog.Logger) Option
func WithClock(clock func() time.Time) Option

func NewHandler(service *Service, auth httpapi.AccessTokenAuthenticator) *http.Handler
func (h *http.Handler) RegisterRoutes(e *echo.Echo)

func NewWorker(service *Service) (*Worker, error)
func (w *Worker) RunOnce(ctx context.Context, runID uuid.UUID) (*Run, error)
func (w *Worker) ProcessQueued(ctx context.Context, limit int) (int, error)

func NewPostgresIntegrationResolver(db *postgres.DB, sealer secrets.Sealer, keyVersion uint32) (*PostgresIntegrationResolver, error)
func NewDockerChecker(cfg CheckerConfig) (*DockerChecker, error)
func NewGitFetcher(cfg GitFetcherConfig) *GitFetcher
func EnsureJobStream(ctx context.Context, client *platformnats.Client) error
func NewNATSJobPublisher(client *platformnats.Client) (*NATSJobPublisher, error)
```

Boot must (outside this lane):

```go
service, err := attackdefense.NewService(db, authorizer, sealer,
    attackdefense.WithGitProviderFactory(&attackdefense.GitProviderFactory{
        AllowInsecureLoopback: cfg.AllowInsecureLoopback, // false in production
    }),
    attackdefense.WithRunnerImageAllowlist(cfg.PinnedCheckerImages),
    attackdefense.WithWebhookBaseURL("https://ctf.example/api/v2/webhooks/git"),
    attackdefense.WithJobPublisher(natsPublisher),
)
handler := adhttp.NewHandler(service, accessTokenAuthenticator)
handler.RegisterRoutes(echo)
```

The events dispatcher must register the AD event type or `ad.run.completed`
outbox rows will stay unpublished:

```go
registry := attackdefense.EventRegistry()
dispatcher := events.NewDispatcher(db, publisher,
    events.WithDispatcherRegistry(registry))
publisher := events.NewNATSPublisher(natsClient,
    events.WithNATSPublisherRegistry(registry))
```

No events-package edit is required or made.

## 4. Native module-provider metadata (no closed-enum edit)

```go
attackdefense.ModuleProviderMetadata()
// {Key:"attack_defense", ModuleType:"ATTACK_DEFENSE",
//  AllowedTaskKinds:["EXPLOIT","PATCH"], RequiresGit:true, RequiresChecker:true}

attackdefense.ValidateModuleConfiguration(moduleType, allowedTaskKinds) []Finding
attackdefense.NewModuleProvider(service) *ModuleProvider
// Info(), ValidateModule(), ScoreSource()
```

`ATTACK_DEFENSE` is already a native boards/generated enum value. The provider
descriptor lets boot/boards validate that an A+D module only enables
`EXPLOIT`/`PATCH` without adding a second enum or hardcoded feature switch.

Read-only score integration (boards consumes later; this lane writes no board
score row):

```go
type ScoreSourceAdapter interface {
    ListBoardScores(ctx, boardID) ([]SubjectScore, error)
    SubjectScore(ctx, boardID, subjectType, subjectID) (int, error)
}
func NewScoreSourceAdapter(store *Store) (ScoreSourceAdapter, error)
```

`SubmissionProvider` is intentionally a documented boundary placeholder: AD
runs are not core submissions, and the boards/Pentest admin-submission union is
owned by that worker. No parallel board API route is added.

## 5. Integration credential handshake (cross-lane)

The integration owner will export:

```go
func integrations.CredentialAAD(id uuid.UUID) []byte {
    return []byte("actfb:integration-credentials:v1:" + id.String())
}
```

The AD package calls `integrations.CredentialAAD(id)` directly (its
`attackdefense.CredentialAAD` wrapper returns the same bytes) and decrypts
through `integrations.OpenCredentials`, so the AAD/envelope is never forked.
The integration package does not import AD, so there is no cycle.
`PostgresIntegrationResolver` reads `integrations` where `category='GIT'`,
requires an encrypted credential and the matching key version, and parses:

```json
{"token":"...","username":"..."}        // token auth (username optional)
{"username":"...","password":"..."}     // basic auth
"raw-token-string"                       // accepted for operator compatibility
```

Decrypted credentials exist only in the webhook/worker process. Provider API
headers are never logged. If the integration’s Git settings contain a
non-empty `repositoryAllowlist`, repository assignment and the worker enforce an
exact match (case-insensitive for GitHub). A configured
`settings.webhookBaseURL` overrides the service default for hook registration. Gitea uses `Authorization: token`, GitLab
`PRIVATE-TOKEN`, GitHub `Bearer` plus `X-GitHub-Api-Version`.

Repository webhook secrets are AD-owned ciphertext under
`repository_bindings.webhook_secret_ciphertext` with AAD
`actfb:repository-binding-webhook-secret:v1:<bindingUUID>`; each binding gets a
32-byte CSPRNG secret. No plaintext secret is stored or returned.

## 6. Webhook contract

1. Read raw bytes with a **2 MiB** hard limit before parsing.
2. Require `application/json`; reject form payloads.
3. Load the binding by UUID, decrypt the per-binding secret, fail closed if the
   secret/key version is unavailable.
4. Verify raw bytes:
   - **GitHub**: `X-Hub-Signature-256: sha256=<hex>`, HMAC-SHA256,
     constant-time compare. `X-GitHub-Event` must be `push`; delivery id from
     `X-GitHub-Delivery`.
   - **Gitea**: `X-Gitea-Signature` hex HMAC-SHA256 (falls back to
     `X-Hub-Signature-256`), constant-time; `X-Gitea-Event`/`X-GitHub-Event`
     must be `push`; delivery from `X-Gitea-Delivery`/`X-GitHub-Delivery`.
   - **GitLab**: `X-Gitlab-Token` constant-time comparison, `X-Gitlab-Event`
     `Push Hook`, delivery from `X-Gitlab-Event-UUID`. GitLab’s token is **not**
     treated as an HMAC; newer GitLab signing tokens are noted as a future
     additive path. Non-TLS GitLab transport is accepted only from loopback or
     with the explicit local-test option; production must terminate TLS.
5. Normalize and validate: `refs/heads/<branch>` exactly equals the stored
   binding branch; repository identity equals the stored binding repository;
   `after` is 40/64 hex, lowercased; all-zero/deleted pushes and `HEAD` are
   rejected; tags/other refs are unsupported.
6. Trusted actor/source: assignments referencing the binding are read from DB;
   source binding ⇒ `PATCH`, exploit binding ⇒ `EXPLOIT`; subject is the stored
   assignment subject; `requested_by` is the participant user or team captain.
   No user id, subject, branch or repository from JSON is trusted.
7. Board/module window and `allowedTaskTypes` are checked server-side before a
   run row is inserted.
8. Dedupe:
   - same provider delivery id + same SHA ⇒ `409 WEBHOOK_ALREADY_PROCESSED`;
   - same SHA through a different delivery id ⇒ one logical run, `202`;
   - same delivery id + different SHA ⇒ `409 WEBHOOK_REPLAYED`, delivery marked
     rejected, no run;
   - the unique `(module, subject, kind, lower(commit_sha))` index makes the
     logical-run rule durable.
9. Jobs are wake-ups only: the response is `202` after the DB commit; a lost
   NATS message is recovered by worker reconciliation.

## 7. Worker, jobs and execution checks

`cmd/ad-worker` is a separate process; the HTTP backend never executes
participant code and does not need Docker/Git/credentials.

NATS: work-queue stream `ACTFB_AD_JOBS`, subject `actfb.ad.jobs`, durable
consumer `ad-worker`; payload is exactly `{"runId":"<uuid>"}`. The DB run row is
the source of truth. Duplicate messages are harmless because the worker claims
`QUEUED → RUNNING` atomically.

Before any provider call or container start the worker re-reads current DB
state: module type, live board/module window, allowed task kind, config, both
bindings, assignment integrity, current subject participation/team, and the
runner image allowlist. A stale queued run outside the window becomes
`ERROR BOARD_NOT_ACTIVE` without executing code.

Git fetch: host `git` subprocess with an **argument array** (no shell), HTTPS
(loopback HTTP only with explicit local-test option), host must match the
integration, protocol allowlist denies `file`/`ssh`/`ext`/`git`, redirects
disabled, hooks disabled, credential helper cleared, exact commit fetched then
`checkout --detach` and `rev-parse HEAD` equality verified. The token is supplied
only through a private `GIT_ASKPASS` script/environment, never argv, config or
logs. `.git`, symlinks, special files and submodules never reach the checker;
the sanitized checkout is bounded by file count/bytes.

Checker (Docker CLI, trusted host worker):

```text
--network none --read-only --cap-drop ALL --security-opt no-new-privileges
--pids-limit N --memory M --cpus C --user 65534:65534
--tmpfs /tmp:rw,noexec,nosuid,nodev,size=... --workdir /work
--mount type=bind,source=<sanitized source>,target=/source,readonly
--mount type=bind,source=<sanitized exploit>,target=/exploit,readonly
```

No host network, Docker socket, credential/DB/NATS environment, writable host
mount, added capability or student-selected image/command/path. `--rm` plus a
unique name and forced cleanup on timeout. The image must match an operator
allowlist entry and an immutable `name@sha256:<64>` reference; mutable tags are
rejected at config write and at run time. The worker chmods only the checkout
ancestors for unprivileged traversal; private git/askpass directories stay
`0700`.

## 8. Checker JSON contract and awards

The only accepted stdout marker is exactly one harness line:

```text
ACTFB_VERDICT:{"vulnerabilities":[{"key":"auth","status":"PASSED",
  "securityFixed":true,"regressionPassed":true,
  "securityTests":{"fixed":1},"regressionTests":{"passed":1},
  "message":"..."}]}
```

Validation is server-side: unknown/duplicate vulnerability keys, unknown JSON
fields (including any `points` attempt), invalid statuses, out-of-bounds
counters, oversized verdicts and a missing/multiple marker reject the run. Test
selectors appearing in messages/logs are redacted before storage.

Semantics:

- `EXPLOIT` PASSED ⇒ award `vulnerabilities.exploit_points` once per
  `(subject_type, subject_id, vulnerability_id, 'EXPLOIT')`.
- `PATCH` PASSED requires **both** `securityFixed=true` and
  `regressionPassed=true`; otherwise it is stored as `FAILED` and awards
  nothing.
- Points always come from `ad_vulnerabilities`; the checker cannot choose a
  score.
- `ad_run_results` and `ad_awards` are written with the run lock, result rows,
  audit row and `ad.run.completed` outbox row in one transaction. Unique awards
  make commits/replays idempotent; no `submissions`/score-adjustment row is
  written (“no extra score”).
- Progress returns only FOUND/PATCHED vulnerabilities; `currentPatchValid`
  reflects the newest PATCH result, while an earlier award remains evidence.
- Participant run details include only results for vulnerabilities already
  discovered (exploit-awarded); admin details include sanitized counters.
- `ad.run.completed` payload carries ids/status/counts/commit SHA only — never
  selectors, logs, tokens or raw checker output.

## 9. Local acceptance and evidence

Commands actually run during implementation:

```bash
# Unit/provider/verdict/webhook tests
go test ./internal/attackdefense/... ./cmd/ad-worker/

# Real PostgreSQL (migrations 001–012 applied per fresh schema)
ACTFB_TEST_POSTGRES_DSN='postgres://postgres:dev-password@127.0.0.1:55443/actfb?sslmode=disable' \
  go test ./internal/attackdefense/... -count=1

# Real local Gitea: create private repos, push, exact-SHA fetch,
# provider hook registration (idempotent) and a real signed delivery
bash examples/attack-defense/local-gitea/run-acceptance.sh

# Real pinned Docker checker: EXPLOIT on vulnerable source, PATCH on patched
# source, PATCH rejected on vulnerable source
bash examples/attack-defense/local-checker/run-acceptance.sh

# Full worker path: real Gitea + real Docker checker + real PostgreSQL
ACTFB_TEST_POSTGRES_DSN=... ACTFB_TEST_GITEA_URL=... \
ACTFB_TEST_GITEA_TOKEN=... ACTFB_TEST_AD_DOCKER_IMAGE='registry/checker@sha256:...' \
  go test ./internal/attackdefense -run TestWorkerRealGiteaAndDockerAcceptance -count=1 -v
```

Observed local evidence:

- migrations through `012` apply cleanly; webhook dedupe/replay/subject/actor
  rows verified;
- exploit/patch awards idempotent across commits; patch needs both signals;
  progress score 250 for one exploit (100) + one patch (150); zero core
  submissions; `ad.run.completed` outbox rows present;
- manual run idempotency, cancel, retry, rate limit and cross-module IDOR
  negatives pass;
- local Gitea 1.24.7 (`gitea/gitea@sha256:9189…`) real repo create/get, branch
  head, commit check, exact-SHA `git` checkout and real webhook delivery pass;
- real checker image (Python 3.13-alpine pinned digest) runs the three expected
  verdicts under the isolation flags;
- full worker run produced PASSED EXPLOIT and PATCH runs, two awards, no
  submissions and no selector leak in student logs.

### External blockers (honest)

- **No GitHub/GitLab live endpoint or credentials** were configured. Their
  adapters are verified against `httptest` API contracts (paths, auth headers,
  repo/branch/commit shapes, idempotent hooks), not live providers. They return
  real HTTP errors; there is no success-returning fake adapter. Mark live
  GitHub/GitLab acceptance **BLOCKED** until an operator supplies endpoints.
- Local Gitea and the local Docker checker are **test-only**. They are not a
  production-untrusted-node claim. Hostile participant code requires a
  dedicated isolated worker node with the operator’s own Docker/runtime
  hardening and monitoring.
- The events dispatcher needs the registry wiring in §3; without it the
  `ad.run.completed` outbox rows remain unpublished (core is still consistent).
  `cmd/backend`/router wiring is owned by the boot owner, not this lane.

## 10. Deliberate behavior decisions / limits

- `GET admin/config` before first save returns documented defaults with an empty
  `runnerImage`; runs stay unavailable until an admin saves an allowlisted
  pinned digest. No fake runner image is invented.
- Manual run of an already completed logical commit returns the existing run
  rather than creating a second one; admin `RETRY` resets a failed/error/
  canceled run to `QUEUED` (same run UUID) and clears old results. Awards are
  never deleted.
- Admin assignment delete is refused when runs exist (`REPOSITORY_BINDING_IN_USE`)
  because `ad_runs.repository_assignment_id` is `ON DELETE CASCADE`; history is
  audit/score evidence. Repository bindings are shared and are not auto-deleted.
- Vulnerability delete is refused once awards exist (`VULNERABILITY_IN_USE`).
- Repository provisioning is off by default because the Swagger assignment
  contract references existing repositories. With
  `WithRepositoryProvisioning(true)` and a webhook base URL, the service calls
  the real provider API to create private repositories/hooks idempotently. The
  same rule accepts existing operator-provisioned repos.
- Updating an assignment creates/reuses bindings; old bindings are left intact
  (other subjects may share them). No binding-row deletion endpoint is invented.
- `Idempotency-Key` for manual runs and admin actions is stored/read through the
  existing `audit_events` JSON details; no new table/column is used. This is
  durable across restarts. An optional future audit-expression index is a
  performance optimization only, not a correctness requirement; **no DDL is
  requested or written by this lane.**
- `repository_bindings` and `ad_awards`/`ad_run_results` come from `012` as
  landed; `011`’s webhook table FK is re-pointed by `012`. This package assumes
  the database is at version `12`.