# Instance lifetime — independent review fixes

Date: 2026-11-24
Review: `docs/instance-lifetime-review.md` (range `9a76564..a017594`)
Fix base: `a017594`
Scope: I1–I5, all minor findings (M1–M7), the pre-existing remote-VM configuration blocker, and the required
browser phase.

The review verdict was "no Critical, five Important". Every Important item was a real defect, not a
disagreement about design: in three cases the read model or validation disagreed with what the service actually
did, which is exactly the "button lies" class of bug this slice was supposed to remove. Nothing in the review
was regraded; where my earlier report was wrong (M6, the "GOARCH typo"), this document states the correction.

## I1 — legacy instances: read model said "no extend", service still extended

**Root cause.** `applyLifecycle` had no access to `Options`, so it hardcoded `canExtend=false` for a snapshot
without a `lifecycle` block, while `extendInstanceAtomic` resolved the same snapshot through
`policyFor` → `legacyPolicy(options)` and granted the operator's extension. Two code paths, two answers.

**RED.** `TestLegacyInstanceReadModelMatchesActionPolicyRealPostgres` (new) builds the service with
`Options{ExtensionTTL: 7m, MaxExtensions: 5}`, strips `lifecycle` from the snapshot, then asserts
`canExtend == true`, `extensionSeconds == 420`, `extensionsRemaining == 5`, **and** that `ActionTask(EXTEND)`
succeeds and moves the expiry by exactly 7 minutes. Against the old code the first assertion failed
(`canExtend` was `false`), and the old test (`TestLegacySnapshotFallsBackToGlobalPolicyRealPostgres`) locked
the wrong behaviour in.

**GREEN.** `store` now carries the service `Options` (`newStore(db, options)`, `dbStore(db, options)`), the
service builds the store **after** applying options, and both paths call one resolver
`resolveInstancePolicy(lifecycle, moduleSettings, options)`. The read model reports the operator step and
remaining credits, so the button and the API agree by construction. The old test was corrected to assert the
consistent pair instead of `canExtend=false`, and the non-default `Options` prove the operator value is the
source rather than the hardcoded default.

**Decision recorded:** main chose the spec-conform fix (derive the legacy policy) over fail-closed, so
existing live instances keep their extension capability with the current global caps and the current
board/module ends still clamp the hard deadline. No retroactive snapshot rewrite was introduced.

## I2 — legacy RESET ignored the live module setting

**Root cause.** `applyAction` used `policyForSnapshot(target.Lifecycle, s.options)`, whose legacy branch
returns the global `ResetEnabled=true`, so an admin disabling reset did not affect pre-existing instances.

**RED.** `TestLegacyResetFollowsLiveModuleSettingRealPostgres` (new): module `resetEnabled=false` + stripped
snapshot → `canReset == false` and `ActionTask(RESET)` rejected with no generation change; re-enabling the
module flips both back; an expired instance is refused even with reset enabled and the board window open.

**GREEN.** The legacy branch takes `ResetEnabled` from the live module settings
(`resolveInstancePolicy`), new instances keep their frozen policy, and `resetAllowed(now)` is the shared live
precondition (not removed, not destroying/expired, not past the deadline) used by both the read model and the
action.

## I3 — effective initial ≤ effective max was not validated

**Root cause.** `validateTaskLifetimeOverrides` compared the two values only when **both** were task
overrides. A task with only `maxTtlSeconds: 1800` inherited a 3600 module default, saved cleanly, and then
failed every start with `500 instance lifetime configuration is invalid`. Module updates validated the module
alone and never re-checked dependent tasks.

**RED.**
- `TestEffectiveLifetimeValidatedAtTaskSave` — only `maxTtlSeconds=1800` with a 3600 module default must be a
  422 mentioning `maxTtlSeconds`; an explicit `900/1800` pair must be accepted.
- `TestModuleUpdateRejectsStrandingTask` — lowering the module ceiling below an existing task override is
  rejected and the message names the task; the module is unchanged; the same guard covers an extension step
  lowered below a task's step.
- `TestTaskEditAndModuleUpdateSerializeRealPostgres` — six iterations of a concurrent task edit and module
  update; the committed pair is always usable.

**GREEN.** `EffectiveLifetimeSeconds(overrides, module)` computes the effective pair with the documented
precedence and the comparison moved onto it, so a partial override cannot slip through. The authoritative
check runs inside the mutation transaction (`validateTaskLifetimeTx`), module updates re-check every
instance-enabled task (`validateModuleLifetimeTx`) and name the offender, and both take the same
`pg_advisory_xact_lock` per module (`lockModuleInstancePolicy`), so a concurrent task edit and module update
cannot commit a combination neither validated.

## I4 — a retried EXTEND generated a new Idempotency-Key

**Root cause.** `InstancePanel` read `retryKeys[next]` before ever assigning it, so the first attempt passed
`undefined`, `taskAction` generated a key internally, and the retry generated a different one. A lost
response therefore spent a second credit — the exact guarantee the feature advertises.

**RED.** New `InstancePanel.test.ts`: the first call rejects (simulated lost network), the second must receive
the **same** key; after a confirmed success the next click must use a different key; the server step/remaining
render; the disabled state shows the server reason.

**GREEN.** The component owns the key (`retryKeys.value[next] ?? createIdempotencyKey()`), keeps it across
failures, clears it only on success or an instance identity change, and ignores a second click while one
action is pending. `instancesApi.taskAction`/`adminAction` accept an explicit key. A failed action no longer
replaces the whole panel with an error, so the retry button stays reachable. `RESET`/`DESTROY` use the same
mechanism.

## I5 — a failed destroy during the late-expiry path orphaned the provider object

**Root cause.** After a provider call outlived the deadline, the resource was destroyed inline and the error
returned **before** `nodeResult` — the only writer of `instance_nodes.provider_resource_id`. A failed destroy
left no id, so `abortExpired` saw nothing, the DESTROY short-circuited and `releaseResources` marked the
instance `EXPIRED` while the labelled container survived.

**RED.** `TestLateExpiryFailedDestroyPersistsProviderIDRealPostgres` (new): a driver whose `Apply` expires the
instance and whose first `Destroy` fails. Asserts the provider id is durable, `removed_at IS NULL`, the
endpoint and network lease are still held, no node is `RUNNING`, no `instance.started` event exists, and that
`PendingJobs` + `HandleJob` retries with the **same** id, releases exactly once, and emits only
`instance.expired`.

**GREEN.** `stageExpiredResource` persists the provider id, sets the node `DESTROYING` and inserts a `QUEUED`
DESTROY operation in one transaction **before** any external call. A failed destroy leaves that op queued, so
the next reconcile claims it with the real id. Only a confirmed destroy leads to `releaseResources`. A driver
that reports `RUNNING` with an empty id is an error rather than a fake success.

**Accepted consequence.** The late-expiry path now ends the job successfully when the destroy confirmed (the
environment is expired, not provisioned), which is why the earlier test's expectation was updated. It still
never advertises `RUNNING`.

## Minor findings

| ID | Finding | Resolution |
|---|---|---|
| M1 | misleading legacy reason text | replaced with "Extensions are not available for this instance"; the legacy case now reports a real capability (I1) |
| M2 | `canReset` meaningless for PENTEST | `applyPentestCapabilities` reads the live `pentest_configs.reset_enabled` and the consumed reset budget; a pentest never exposes extension fields. Covered by `TestPentestCapabilitiesReflectConfigRealPostgres` |
| M3 | EXTEND skipped the board/module window | the window and the DB clock are re-read **under the row lock**; a paused board or disabled module rejects before any credit or idempotency row. Covered by `TestExtendRejectsClosedWindowWithoutSpendingCreditRealPostgres` (board paused, module disabled) |
| M4 | Swagger minima exceed operator reality | contract left at 60 s (production rule) and documented in `docs/runtime-instances.md`: the operator `Options` may be shorter for acceptance tests only |
| M5 | seven files not `gofmt`-clean | `gofmt -w` applied; `gofmt -l internal/ cmd/` is empty |
| M6 | report/plan bookkeeping drift | report corrected to the real test counts; the plan no longer claims a `GOARCH` fix (no defect existed — `Dockerfile:31` already reads the correct five-letter variable, verified with `od -c` and a real `linux/amd64` build) |
| M7 | module editor did not validate max concurrency | client-side 1..100 with a field message, covered by a new component test |

## Pre-existing blocker: remote VM allowlist was env-only

A deployment that keeps every app setting in `config.yaml` could not approve a Proxmox/KubeVirt/Kubernetes
host without inventing `ACTFB_PROVIDER_ALLOWED_HOSTS`.

**RED.** `TestProviderAllowedHostsPrecedence` (yaml only / legacy env fallback / yaml wins over env / secure
empty default), `TestLoadConfigReadsProviderAllowlist`, `TestShippedProductionConfigAllowsNoRemoteProviderByDefault`,
`TestProviderURLPolicyUsesYAMLAllowlist` (backend helper denies an unlisted host and plain-HTTP loopback),
`TestProviderURLPolicyEmptyDeniesRemote`.

**GREEN.** `integrations.allowed_hosts` is a typed YAML list on the existing `IntegrationsConfig`; the shipped
`deploy/config.yaml` ships `[]`, which denies every remote provider URL. The legacy env value still works and
applies only when the YAML list is empty, so an inherited environment cannot widen a YAML policy. The backend
resolves it once (`providerURLPolicy`) for integration CRUD and the `instance-worker` resolves the same list
for connection tests and provider calls. The SSRF guard itself is untouched: no wildcard, no disabled TLS, no
plain HTTP, no loopback trust. Credentials remain write-only integration records.

## Verification actually run

All commands below were run in this session on this machine.

```text
gofmt -l internal/ cmd/                          (empty)
go build ./...                                   OK
go vet ./...                                     OK
go test ./... -count=1                           50 packages ok, 0 FAIL
   (real PostgreSQL 18 + NATS JetStream on throwaway containers)
go test -race -p 1 ./internal/instances ./internal/boards ./internal/boards/http ./internal/config ./cmd/backend
                                                 ok / ok / ok / ok / ok
swagger generate model -f docs/swagger.yaml      drift 0 (152 paths, 416 definitions, 205 operations)
scripts/test-production-packaging.sh             267 checks passed, 0 failed
bun run typecheck                                clean
bun run test:unit                                25 files / 118 tests passed
bun run build                                    built
```

New/extended tests (all executed, none skipped):

```text
--- PASS: TestLegacyInstanceReadModelMatchesActionPolicyRealPostgres
--- PASS: TestLegacyResetFollowsLiveModuleSettingRealPostgres
--- PASS: TestExtendRejectsClosedWindowWithoutSpendingCreditRealPostgres (board paused, module disabled)
--- PASS: TestPentestCapabilitiesReflectConfigRealPostgres
--- PASS: TestLateExpiryFailedDestroyPersistsProviderIDRealPostgres
--- PASS: TestEffectiveLifetimeValidatedAtTaskSave
--- PASS: TestModuleUpdateRejectsStrandingTask
--- PASS: TestTaskEditAndModuleUpdateSerializeRealPostgres
--- PASS: TestProviderAllowedHostsPrecedence (4 sub-cases)
--- PASS: TestLoadConfigReadsProviderAllowlist
--- PASS: TestShippedProductionConfigAllowsNoRemoteProviderByDefault
--- PASS: TestProviderURLPolicyUsesYAMLAllowlist
--- PASS: TestProviderURLPolicyEmptyDeniesRemote
--- PASS: TestLegacySnapshotFallsBackToGlobalPolicyRealPostgres (corrected expectation)
--- PASS: TestWorkerLateExpiryDoesNotAdvertiseRunningRealPostgres (corrected expectation)
frontend: InstancePanel.test.ts 4 tests, ModuleEditorDialog concurrency case, 118 total
e2e/run-full-e2e.sh (full stack, real Docker)   19 passed, 0 owned containers/volumes, hashes unchanged
```

## Browser phase

`clamav/clamav:1.5.4` was pulled normally through Docker (`docker pull`, exit 0) and both pinned digests
(`clamav@sha256:7054e3…`, `rustfs@sha256:b81d16…`) are present, so the full harness had no fixture blocker.

The new browser test (`e2e/tests/full-stage.spec.ts`, "instance lifetime: module settings, task overrides, real
extend cap and reset") runs against the real app stack: ephemeral PostgreSQL 18 + NATS JetStream + RustFS +
ClamAV containers, the real `backend`, `instance-worker`, `events-dispatcher`, `file-scanner`, the real Vite
frontend and the real Docker Engine. It asserts, in order:

1. the module editor persists 30 min default / 2 h max / 15 min step / 6 extensions and the reopened editor
   shows those values (previously `UNSUPPORTED`);
2. a task created with a narrowed 30 min / 2 h / 15 min ×2 policy round-trips through the editor, keeps its
   overrides after a metadata save, keeps its immutable binding row, and `Inherit` turns every override into
   `null` without touching the binding;
3. the student starts the real Docker instance, sees the server step and `2 left`, clicks `Extend +15 min`
   twice with each HTTP call adding exactly 15 minutes and decrementing the credit, then sees the button
   disabled with "No extensions left" (a real cap, not an intercepted response);
4. `Reset` leaves `expiresAt` and `extensionCount` unchanged;
5. `Destroy` releases the environment.

Result of the full harness run:

```text
19 passed (5.1m)
owned Docker resources: 0 new containers, 0 new volumes
core and Vue artifact hashes unchanged
```

Iterating on this test surfaced three fixture mistakes of mine (the module and task Advanced sections are
collapsed `<details>`, the task Advanced block only exists in Custom mode, and the admin task read model never
exposed the template id, so the binding is asserted against the real row). All three were test-only fixes; no
product behaviour was changed to make the test pass.

## Round-2 scoped review (N1–N5)

The independent re-review of `a017594..2d2461d` closed I1–I5 and raised one new Important finding plus four
minors. All five are fixed below.

### N1 — "destroy confirmed" bookkeeping is now one transaction (Important)

**Root cause.** The confirmed destroy was recorded by two separate statements:
`markOperation(cleanupOp, "SUCCEEDED")` and then `confirmStagedDestroy(...)`. A crash or a transient DB error
between them left the durable state `DESTROY op SUCCEEDED` + `node DESTROYING` + provider id still set. No
scheduler could heal it: `pendingJobs` only returns `QUEUED`/`RUNNING`, `expireDue` skips `DESTROYING`, and a
later `destroyInstance` hits `ON CONFLICT … DO NOTHING`. The provider object was already gone, so this was a
permanent leak of the *reservation* (public port + network lease), in the very path that existed for
durability.

**Fix.** `completeStagedDestroy` (`internal/instances/store_lifecycle.go`) does the whole confirmation in one
transaction:

1. `SELECT … FROM instances WHERE id = $1 FOR UPDATE` (locks the instance; a newer generation or an
   already-removed row is a no-op commit);
2. clear `instance_nodes.provider_resource_id`, set the node `DESTROYED`, **at the exact generation**;
3. mark the matching `DESTROY` operation `SUCCEEDED`;
4. release endpoints/leases and expire the instance **only when no node still owns a provider object**;
5. commit.

A failure at *any* statement rolls the whole thing back, so the operation stays claimable with its provider id
and the retry destroys with the same id. Both the late-expiry path and the normal `DESTROY` path now go
through it (the normal path previously cleared nothing, and released on `destroyAll` without checking the
other nodes).

**Recovery for the exact broken state.** `repairStuckStagedDestroys` re-queues a `DESTROY` op that is
`SUCCEEDED` while its own node is still `DESTROYING` with a provider id at the same generation. It is
deliberately narrow: never `CREATE`/`RESET`, never a finished node, never another generation, and it requires
the instance generation to match, so a successful apply or an unrelated provider id can never be replayed.
`expireDue` and `pendingJobs` both run it before selecting work, which closes the "`DESTROYING` rows are never
re-selected" gap without broadening the expiry selector into a perpetual retry loop.

**A failed `DESTROY` stays retryable.** `failOperation` puts a failed `DESTROY` back to `QUEUED` instead of
`FAILED` (destroying is idempotent and the object still exists), while a failed `CREATE`/`RESET` still becomes
`FAILED`. Marking a destroy `FAILED` was the other way to strand the reservation.

**RED → GREEN.**

| Test | What it proves |
|---|---|
| `TestStuckStagedDestroySelfHealsRealPostgres` | seeds exactly the reviewer's broken state, then only the scheduler (`ExpiredJobs` + `PendingJobs` + `HandleJob`) is used → `EXPIRED`, `removed_at` set, 0 live endpoints/leases, no provider id left |
| `TestStagedDestroyCompletionRollsBackOnDBErrorRealPostgres` | a Postgres trigger raises on the **last** statement (lease release) after the node/op writes; everything rolls back (lease held, id intact, op not `SUCCEEDED`), then the retry completes |
| `TestStuckDestroyRecoveryUsesSameProviderIDRealPostgres` | every `Destroy` call in recovery uses the **persisted** id, and a provider that reports the object gone still settles |
| `TestFailedDestroyStaysRetryableRealPostgres` | a failed destroy leaves a `QUEUED`/`RUNNING` op and held lease, and the retry releases exactly once with the same id |
| `TestPartialFailureDoesNotClobberCleanupQueueRealPostgres` | a failing `RESET` job does not mark the queued cleanup `DESTROY` op `FAILED` |

No test calls a helper to bypass the scheduler: every one goes through `ExpiredJobs`/`PendingJobs`/`HandleJob`.
The purpose-built generation checks, `warning`-free expired system auth and dedup semantics are unchanged.

### N2 — package count in the report (Minor)

Corrected to 50 packages with test files. The frontend (25 files / 118 tests) and browser (19 tests) counts
were already exact.

### N3 — a zero extension budget cannot be raised by a task (Minor)

`validateTaskLifetimeOverrides` previously compared `count > module.Extension.MaxCount` only when
`MaxCount > 0`, so `{enabled: true, maxCount: 0}` — "no extensions" — accepted a task override of 3 through the
API. Now `maxCount == 0` rejects any extension override with `body.instance.extension.maxCount: the module
grants no extensions (maxCount is 0)`, independent of `enabled`. A task with no override still inherits the
zero budget and starts with `canExtend=false`.

**RED → GREEN:** `TestTaskCannotRaiseExtensionsOverZeroModuleBudget` — module `{enabled:true, seconds:900,
maxCount:0}`, task override `maxCount:1` → 422 naming `extension.maxCount`, no task row created; a task with no
override is accepted.

### N4 — an explicit `allowed_hosts: []` now denies despite the legacy env (Minor)

`integrations.allowed_hosts` is presence-aware: `IntegrationsConfig.UnmarshalYAML` records whether the key was
written, and `ProviderAllowedHosts` falls back to `ACTFB_PROVIDER_ALLOWED_HOSTS` **only when the key is absent**.
An explicit list, including `[]`, is authoritative, so an inherited environment can neither widen nor silently
re-open a policy the operator wrote down. The legacy variable still works for a config that omits the key, and
the SSRF guard is untouched (no wildcard, no TLS relaxation, no loopback trust).

**RED → GREEN:** `TestExplicitEmptyAllowlistDeniesDespiteLegacyEnv` (absent → legacy list; explicit `[]` → empty
deny; non-empty YAML → ignores env), `TestProviderURLPolicyExplicitEmptyDeniesDespiteLegacyEnv` and
`TestProviderURLPolicyAbsentKeyUsesLegacyEnv` (backend helper), `TestShippedProductionConfigAllowsNoRemoteProviderByDefault`.
Docs and `deploy/config.yaml` now state the presence rule.

### N5 — dead legacy resolver removed (Minor)

`policyForSnapshot` had no production caller. It is deleted; the test now asserts `resolveInstancePolicy`
directly (frozen policy wins; a legacy row takes the operator options plus the live module reset switch), so
there is exactly one legacy entry point.

## Commands run for this round

```text
gofmt -l internal/ cmd/                                   (empty)
go build ./...                                            OK
go vet ./...                                              OK
go test ./... -count=1                                    50 packages ok, 0 FAIL
go test -race -p 1 ./internal/instances ./internal/config ./internal/boards ./internal/boards/http
                                                          ok / ok / ok / ok
go test ./internal/instances -run TestRealDockerExpiryCleanupAndExtend
                                                          PASS (real Docker, 6.2s), 0 owned containers after
migration 019 down -> 18, up -> 19                        clean
scripts/test-production-packaging.sh                      267 checks passed, 0 failed
```

Frontend is unchanged in this round (`git diff --stat` over `frontend/` is empty), so its 25 files / 118 tests
and the 19/19 browser result from `2d2461d` stand as previously reported; the browser suite was **not** re-run
for pure DB bookkeeping changes. Swagger and generated models are unchanged (no regeneration).

## Corrections to my earlier report

- **`GOARCH` typo:** there is no defect. The earlier read-only report displayed a garbled variable name; the
  file has always contained the correct token and a real `linux/amd64` build was verified. The plan item is
  removed rather than marked done.
- **Frontend test count:** the previous report said 24 files / 112 tests; the tree now has 25 files / 118
  tests, all executed.
- **Legacy behaviour:** the earlier "honest gap 5" (legacy instances report `canExtend=false`) was a defect,
  not an acceptable limitation, and is fixed here.
