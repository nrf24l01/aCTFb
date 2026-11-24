# Instance lifetime verification

Date: 2026-11-24
Base commit: `9a7656474b770bc26fa6e332c6b913743a1552eb`
Design: `docs/superpowers/specs/2026-11-24-instance-lifetime-design.md`
Plan: `docs/superpowers/plans/2026-11-24-instance-lifetime.md`
Scope: task-instance lifetime (initial TTL, maximum total lifetime, student extension), module instance
settings persistence, and the enforcement gaps that made those settings dishonest.

Everything below was run in this session on this machine. Nothing is quoted from an earlier report.

## Delivery status (published)

The slice is merged to `main` and shipped from published GHCR images.

| Field | Value |
| --- | --- |
| Base commit | `9a7656474b770bc26fa6e332c6b913743a1552eb` |
| Shipped commit / HEAD | `5edd6d53c44986761aca863c0160278b6aa4c3bc` |
| Commits on top of base | 16 (lifetime slice + review fixes) |
| CI run | https://github.com/nrf24l01/aCTFb/actions/runs/45432689770 (success: Backend quality, Frontend quality, Integration, Publish images) |
| Images | `sha-5edd6d53c44986761aca863c0160278b6aa4c3bc` == `latest`, `linux/amd64`, all ten images |
| Contract | 152 paths / 416 definitions / 205 operations, migrations `001`–`019` |

Published-image acceptance (real, not a plan): a cleanroom deployment built from the three downloaded
`deploy/` files with generated ephemeral secrets started the **published** backend, frontend,
instance-worker, retention and events-dispatcher images, applied migrations through `019`, reported
`/ready` and `/healthz`, created an admin through the published binary's `admin create --password-stdin`, and
then drove the whole lifetime flow over the public API: module instance settings, a task override, an
approved pinned benign Docker integration, an immutable template version, a board/module-scoped allowlist,
a student start that the published worker provisioned as a real Docker workload, `EXTEND` (+900 s exactly,
one credit, idempotent replay with no second credit), the cap rejection with a server reason, `RESET` leaving
expiry and credits unchanged, a policy edit leaving the frozen snapshot untouched, and a controlled expiry
that reached `EXPIRED` with 0 live endpoints, 0 live leases and 0 provider containers. See
`docs/ghcr-verification.md` for the image evidence and `docs/instance-lifetime-review-fixes.md` for the
review-fix record.

Pentest keeps its board/module-end lifetime: there is still no independent pentest TTL and no student
extension for a pentest environment.

## Environment actually used

- Go `go1.27.3-X:nodwarf5 linux/amd64`, `GOMAXPROCS=2`, serial package runs.
- PostgreSQL `postgres:18-alpine` in a throwaway container (`actfb-lifetime-pg`, port 55432), migrations
  `001`–`019` applied.
- NATS `nats:2-alpine -js` in a throwaway container (`actfb-lifetime-nats`, port 54222).
- Docker Engine `29.9.3`, local images `alpine:3.23` and `nginx:alpine`.
- Frontend: Bun `1.4.4`, single Vite/Vitest worker.
- Swagger generator: real `swagger v0.36.7` (`~/go/bin/swagger`).

## Requirement → status

| Requirement | Status | Evidence |
|---|---|---|
| Per-task `ttlSeconds` is real and persisted | **implemented** | `tasks.instance_ttl_seconds`; `TaskInstanceInput.TTLSeconds`; `TestTaskLifetimeOverridesPersistAndValidateAgainstModule` |
| Per-task `maxTtlSeconds` (60..604800) | **implemented** | Swagger + `TaskInstanceConfig.MaxTTLSeconds`; same test |
| Per-task extension policy reusing the module `extension` fields | **implemented** | `TaskInstanceModuleSettingsExtension` promoted to a definition and reused; `TaskInstanceConfig.extension` |
| Null/omitted in CREATE inherits | **implemented** | `taskInstanceInput` presence mapping; `TestTaskInstanceInputLifetimePresence` |
| PATCH omitted preserves, explicit null resets to inherit | **implemented** | `mergeTaskLifetimeOverrides`; same test |
| "Inherit module defaults" clears all overrides explicitly | **implemented** | `lifetimePayload()` sends explicit nulls; frontend test `inherits the module policy by sending explicit nulls` |
| Module `taskInstanceSettings` persisted (was `UNSUPPORTED`) | **implemented** | `modules.task_instance_settings`; `TestModuleInstanceSettingsPersistAndReload` |
| Module settings bound validation, field-specific messages | **implemented** | `TestModuleInstanceSettingsRejectInvalidBounds` (6 cases) |
| Module maximum ≤ global hard max 7 d | **implemented** | `validateTaskInstanceSettings` |
| Task override may only narrow the module ceiling | **implemented** | `validateTaskLifetimeOverrides`; 4 rejection cases in the task test |
| `maxTtlSeconds` is maximum total, not per extension | **implemented** | `hardDeadline(createdAt, maxTotal, boardEnd, moduleEnd)` |
| Anchor = request/allocation `created_at`, startup included | **implemented** | expiry computed in the INSERT with the DB clock; `TestTaskLifetimeOverrideAndSnapshotFreezeRealPostgres` asserts ≈30 min from the request |
| `hardDeadline = min(created_at + maxTotal, earliest(boardEnd, moduleEnd))` | **implemented** | `hardDeadline`; `TestHardDeadlineClampsToEarliestEnd` |
| `initialExpires = min(created_at + initial, hardDeadline)` | **implemented** | `LEAST(...)` in `createInstance` |
| Policy frozen in the snapshot at CREATE | **implemented** | `desired_snapshot.lifecycle`; snapshot-freeze test |
| Later edits affect new instances only | **implemented** | snapshot-freeze test rewrites module settings and re-reads the snapshot |
| Old snapshots without a policy fall back to global | **implemented** | `policyForSnapshot`; `TestLegacySnapshotFallsBackToGlobalPolicyRealPostgres` |
| Board/module ends always live, even for old instances | **implemented** | `liveDeadline` + `hardDeadline` at extend time |
| Student EXTEND only when RUNNING and before expiry | **implemented** | `extendInstanceAtomic` guards; expired/removed test |
| Server uses the snapshotted step; student never chooses | **implemented** | `policy.ExtendSeconds` only |
| `newExpiry = min(currentExpiry + step, hardDeadline)` (never from "now") | **implemented** | extend SQL uses the current expiry |
| No actual increase → reject, no credit consumed | **implemented** | `CodeMaxLifetime` before the credit update |
| Partial increase at the deadline consumes exactly one credit | **implemented** | `TestExtendAtHardDeadlineConsumesOneCreditForPartialGainRealPostgres` |
| Retried `Idempotency-Key` never consumes a second credit | **implemented** | `instance_extension_requests`; replay assertion in `TestExtendConsumesOneCreditAndReplaysIdempotentlyRealPostgres` |
| Lock + DB clock after the wait, one transaction | **implemented** | `FOR UPDATE OF i` then `SELECT clock_timestamp()` |
| Never revives EXPIRED/DESTROYING/removed | **implemented** | `TestExtendRejectsExpiredAndSpendsNoCreditRealPostgres` |
| TEAM members share one budget | **implemented** | the row lock is on the instance; `maxCount` is a single column |
| Staff EXTEND uses the same caps | **implemented** | `AdminAction` routes EXTEND through `extendInstance`; pentest still rejects EXTEND |
| Worker re-reads current expiry + board/module window before apply | **implemented** | `liveDeadline.blocked` before the operation loop |
| Zero driver Apply calls for an already-expired job | **implemented** | `TestWorkerSkipsExpiredJobWithoutProviderCallRealPostgres` asserts `driver.applied == 0` |
| Late expiry during a provider call is not advertised RUNNING | **implemented** | `TestWorkerLateExpiryDoesNotAdvertiseRunningRealPostgres` |
| Leases/ports released only after confirmed destroy | **implemented** | `releaseResources` after DESTROY; unchanged ordering |
| `maxConcurrentInstancesPerSubject` enforced | **implemented** | advisory lock + live count in `createInstance`; `TestMaxConcurrentInstancesEnforcedRealPostgres` |
| Capacity released only after confirmed destroy | **implemented** | same test: start is refused while DESTROYING, allowed after the destroy job |
| Module `resetEnabled` honoured for task RESET | **implemented** | `TestResetDisabledByModulePolicyRealPostgres` |
| RESET does not change expiry/credit/anchor | **implemented** | `TestResetDoesNotExtendLifetimeRealPostgres` |
| Admin instance inventory shows expiry/used/remaining | **implemented** | existing `Instance` read model plus new `extensionSeconds`/`extensionsRemaining`/`canExtend`/`canReset` |
| Student panel: server-driven Extend, clamped countdown, reason | **implemented** | `InstancePanel.vue`; countdown ticks from `expiresAt` |
| Zero count never rendered as "unlimited" | **implemented** | `extendDisabledReason` reports "No extensions left" |
| Regular non-INSTANCE tasks show no lifetime fields | **implemented** | `v-if="form.type === 'INSTANCE'"` |
| Task edit never resends the immutable binding | **implemented** | frontend test `edits only the lifetime policy and never resends the binding` |
| Real Docker expiry cleanup | **implemented** | `TestRealDockerExpiryCleanupAndExtend` |
| Pentest keeps board/module-end lifetime, no new TTL | **implemented (unchanged)** | `start` pentest branch; EXTEND rejected for pentest |
| No new API version, no new public deploy endpoint | **preserved** | `/api/v2` only; no new paths (152 before and after) |
| One executor, five drivers, ids-only jobs | **preserved** | `cmd/instance-worker` registry unchanged |
| Additive migration only, no rewrite of applied ones | **preserved** | `019_instance_lifetime.sql`; `001`–`018` untouched |
| Packaging `GOARCH` | **no defect found** | see below |

## Commands and results

```text
go build ./...                                  BUILD OK
go vet ./...                                    VET OK
go test ./... -count=1                          50 packages ok, 0 FAIL
   (ACTFB_TEST_POSTGRES_DSN + ACTFB_TEST_NATS_HOST/PORT set)
go test -race -p 1 ./internal/instances ./internal/boards ./internal/boards/http
                                                ok / ok / ok
swagger generate model -f docs/swagger.yaml     drift_count=0 (regeneration is reproducible)
                                                152 paths, 416 definitions, 205 operations
scripts/test-production-packaging.sh            267 checks passed, 0 failed
                                                 (actual image arch checks SKIP: ACTFB_TEST_IMAGES not set)
bun run typecheck                               clean
bun run test:unit                               24 files / 112 tests passed
bunx vitest run ModuleEditorDialog.test.ts      5 tests passed (new instance-settings case)
bunx vitest run TaskEditorDialog.lifetime.test.ts 4 tests passed
e2e/run-full-e2e.sh (shipped source)             19/19 passed, 0 new containers/volumes,
                                                 core+Vue hashes unchanged
bun run build                                   built
```

Targeted new tests (real PostgreSQL, all actually executed, not skipped):

```text
--- PASS: TestTaskLifetimeOverrideAndSnapshotFreezeRealPostgres
--- PASS: TestExtendConsumesOneCreditAndReplaysIdempotentlyRealPostgres
--- PASS: TestExtendRejectsExpiredAndSpendsNoCreditRealPostgres
--- PASS: TestExtendAtHardDeadlineConsumesOneCreditForPartialGainRealPostgres
--- PASS: TestResetDoesNotExtendLifetimeRealPostgres
--- PASS: TestResetDisabledByModulePolicyRealPostgres
--- PASS: TestMaxConcurrentInstancesEnforcedRealPostgres
--- PASS: TestLegacySnapshotFallsBackToGlobalPolicyRealPostgres
--- PASS: TestWorkerSkipsExpiredJobWithoutProviderCallRealPostgres
--- PASS: TestWorkerLateExpiryDoesNotAdvertiseRunningRealPostgres
--- PASS: TestRealDockerExpiryCleanupAndExtend           (real Docker, alpine:3.23, 6.19s)
--- PASS: TestModuleInstanceSettingsPersistAndReload
--- PASS: TestModuleInstanceSettingsRejectInvalidBounds
--- PASS: TestTaskLifetimeOverridesPersistAndValidateAgainstModule
--- PASS: TestTaskLifetimeRequiresModuleExtensionWhenEnabling
--- PASS: TestDisablingInstanceClearsLifetimeOverrides
--- PASS: TestBoardsAndInstancesAgreeOnGlobalDefaults
--- PASS: TestTaskInstanceInputLifetimePresence
--- PASS: TestModuleInstanceSettingsMapping
--- PASS: TestResolveLifecyclePrecedence / RejectsContradictions
--- PASS: TestHardDeadlineClampsToEarliestEnd
--- PASS: TestPolicyForSnapshotFallsBackToLegacy
```

Real Docker evidence: the instance reached `RUNNING` with exactly one owned container, EXTEND moved the
expiry by ~15 min over the service API, the expired row was cleaned up by `ExpiredJobs` + `HandleJob`, and
afterwards `docker ps -a --filter label=actfb.managed=true` returned 0 containers, 0 live
`instance_endpoints` and 0 live `instance_network_leases`. A second expiry pass was idempotent.

Owned-resource hygiene: `docker ps -a --filter label=actfb.managed=true` → 0. The eight `actfb-<uuid>`
networks still on this host were created 2026-11-13/14, before this session; none belongs to this run.

## Packaging: the reported `GOARCH` typo does not exist

The task said to fix `GOARmd64` → `GOARmd64`. The file already contains the correct token:

```text
$ sed -n 31p Dockerfile | od -c
... C G O _ E N A B L E D = 0   G O O S = l i n u x   G O A R C H = a m d 6 4 ...
```

The earlier read-only report showed `GOARmd64` because the tool output pipeline in that session collapsed the
literal `md` substring; `grep -c GOARmd64 Dockerfile` returns 0 and `grep -c GOARmd64` returns 1. No source
change was made, and none is needed. The architecture claim was verified by building the binary instead:

```text
$ CGO_ENABLED=0 GOOS=linux GOARmd64 go build -o /tmp/opencode/instance-worker-amd64 ./cmd/instance-worker
$ file /tmp/opencode/instance-worker-amd64
ELF 64-bit LSB executable, x86-64, ... statically linked
```

`scripts/test-production-packaging.sh` already asserts both the Dockerfile token and `platform: linux/amd64`
in bake/Compose; all 267 static checks pass.

## Honest gaps and exceptions

1. **No live VM acceptance.** Proxmox, KubeVirt and Kubernetes are unchanged: real REST clients with
   `httptest` contract tests only. No provider endpoint or credentials exist here, so no VM lifetime path was
   executed. The lifetime policy is provider-independent (it lives in `instances`/`desired_snapshot`, not in a
   driver), and the VM contract tests were not modified, but this is **not** a claim that VM expiry was tested
   live.
2. **`ACTFB_PROVIDER_ALLOWED_HOSTS` is still env-only.** Remote VM integrations remain undeliverable from a
   pure `config.yaml` deployment until an operator sets that env var. This slice does not redesign it.
3. **Failed provider operations are still not retried automatically.** A failed operation is `FAILED` and the
   instance stays `ERROR` until expiry cleanup. This is unchanged and now documented in
   `docs/runtime-instances.md`.
4. **Cleanup latency is bounded by the 30 s reconcile interval**, not immediate. Documented, not changed.
5. **Legacy instances report `canExtend=false`.** An instance whose snapshot predates the lifecycle block has
   no frozen extension policy, so the read model refuses to advertise an extension the service would compute
   from a different source. It still expires normally and can be reset/destroyed.
6. **Module settings are TASKS-only.** A PENTEST module rejects `taskInstanceSettings`, consistent with
   "pentest keeps board/module-end lifetime".
7. **Commit split.** The planned four commits became: one contracts/persistence commit, one
   lifecycle/worker commit, one frontend commit, and one tests/docs commit. The first attempt bundled them;
   it was split with `git reset --soft` before any further work, so the history is still the intended shape.
8. **Browser acceptance: superseded.** This report originally stated no browser run was possible. That was a
   wrong call: the ClamAV fixture is a normal Docker dependency and it was pulled successfully. The full
   harness now runs 19/19 including a dedicated instance-lifetime browser test; see
   `docs/instance-lifetime-review-fixes.md` for the browser evidence and `docs/instance-lifetime-review.md`
   for the independent findings this report's gaps produced.
