# Instance lifetime slice — independent review

Date: 2026-11-24
Range: `9a76564..a017594` (50 files, +4245/−279)
Design: `docs/superpowers/specs/2026-11-24-instance-lifetime-design.md`
Plan: `docs/superpowers/plans/2026-11-24-instance-lifetime.md`
Report: `docs/instance-lifetime-verification.md`

Review method: read-only static review of the full diff, the current tree, the migration, the generated
models, the frontend components and the new tests. Per the review brief I did **not** run builds, `go test`,
Docker/Podman, servers or browser runs, and I spawned no subagents. Real-PostgreSQL/Docker results below are
quoted from the implementer's report, not independently reproduced. The only file written is this one.

## Verdict

**Spec compliance: compliant for new instances, with two legacy-row clauses and one validation clause not
honoured.** The frozen architecture is intact: `/api/v2` unchanged, one `cmd/instance-worker` with the
existing five drivers, ids-only NATS jobs + PostgreSQL state, no new deploy route, no per-driver container,
`kind` ≠ driver preserved, migration `019` additive and `001`–`018` untouched, one binary per Alpine
`linux/amd64` image. Precedence, snapshot freeze, hard-deadline clamping, credit-once EXTEND, live deadline
re-checks, destroy-only cleanup bypass and per-subject concurrency all match the design. Deviations are
listed as I1–I3 below; none is architectural and each has a bounded fix.

**Code quality: sound, mergeable after I1–I4 and the formatting fix.** The extend transaction is correctly
ordered (row lock → DB clock → clamp → single credit → idempotency row → audit), cleanup releases
endpoints/leases only after a confirmed destroy, and the migration is honest about which layer enforces
what. No Critical finding. Test coverage of the backend contract is unusually good for a slice this size;
the frontend lifecycle path has one untested component (see I4) and one unenforced validation gap (I3).

## Critical

None.

## Important

### I1. Legacy instances: read model says "no extend", the service still extends (top risk)

`internal/instances/store.go:598-604` reports `canExtend=false` for a snapshot without a `lifecycle` block,
but `internal/instances/extend.go:130-136` resolves the same snapshot through `policyFor` →
`legacyPolicy` (`internal/instances/lifetime.go:182-189`), i.e. extensions enabled, 3 credits. The test at
`internal/instances/lifetime_pg_test.go:332-350` asserts only the read model, so the disagreement is locked
in rather than caught.

Consequences after deploy, for every instance that is live right now:
- the student panel silently loses the Extend button and prints a false reason
  (`frontend/src/features/instances/components/InstancePanel.vue:53-61`, `extensionsRemaining` is `null` for
  legacy rows, so it falls through to "No further extension budget before the deadline");
- a direct `POST …/instance/actions {"action":"EXTEND"}` still succeeds and burns a credit, so the API and
  the UI disagree about the same instance.

This contradicts the frozen invariant "missing policy in an old snapshot falls back to the global defaults"
(design §Invariants, §4) and the report's own "Honest gap 5". It is a behaviour regression for an existing
live environment, not a theoretical edge.

Fix (pick one, both small; main owns the choice):
- spec-conform: resolve the legacy policy for the read model too. `applyLifecycle` has no `Options`; either
  thread `s.options` into `getInstance`/`listInstances`, or export a package-level
  `LegacyLifecyclePolicy(options)` from `instances` and pass the resolved policy in. Then `canExtend`,
  `extensionSeconds` and `extensionsRemaining` are populated exactly as the service behaves.
- fail-closed (only if main prefers it): reject `EXTEND` when `row.Snapshot.Lifecycle == nil` with
  `CodeActionNotAllowed` in `extendInstanceAtomic`, keep `canExtend=false`, and say so in the design doc.

Smallest regression test: extend `TestLegacySnapshotFallsBackToGlobalPolicyRealPostgres` — after deleting
`lifecycle`, assert one consistent pair, e.g. `ActionTask(EXTEND)` succeeds **and** `canExtend==true`, or
`ActionTask(EXTEND)` is rejected **and** `canExtend==false`.

### I2. Legacy RESET ignores the live module setting

`internal/instances/service.go:440-445` uses `policyForSnapshot(target.Lifecycle, s.options)`, which returns
the global default (`ResetEnabled=true`) for a snapshot without a policy. Design §6 requires "the frozen
policy for new instances and the live module setting for legacy rows". A module whose admin disabled reset
after this deploy therefore still lets students reset pre-existing instances.

Fix: when `target.Lifecycle == nil`, resolve `ResetEnabled` from `loadSource(...).ModuleSettings` (the
service already loads `source` in `applyAction`) and fall back to the global default only when the module has
no settings. Regression test: module settings `resetEnabled=false` + legacy snapshot → RESET rejected, and
`canReset=false` in the read model.

### I3. Effective initial TTL ≤ effective max TTL is only checked when both overrides are set

`internal/boards/instance_settings.go:206-210` compares the two values only if both are task overrides. A
task that sets `maxTtlSeconds: 1800` and inherits `initialTtlSeconds` from a module default of `3600` passes
validation (1800 is inside the module ceiling) and then fails at every start in
`ResolveLifecycle` → `validate()` (`internal/instances/lifetime.go:124-126`), surfacing as
`500 "instance lifetime configuration is invalid"` (`internal/instances/service.go:275-278`). The same
happens to existing tasks when an admin later lowers `maxTtlSeconds` below their overrides: module update
validates the module only (`internal/boards/validation.go:122`) and never re-checks dependent tasks.

The design (§2) states the rule as an effective-value rule and forbids silent clamping, so the admin
currently gets a save-then-broken task and the student gets an opaque 500.

Fix (smallest): in `validateTaskLifetimeOverrides`, compute the effective initial (`overrides.InitialTTLSeconds`
?? `module.DefaultTTLSeconds` ?? global) and effective max (`overrides.MaxTTLSeconds` ?? `module.MaxTTLSeconds`
?? global) and reject `initial > max` with `body.instance.maxTtlSeconds`. Optionally add the same check in the
module update path for tasks that would be stranded. Regression test: module (default 3600, max 14400) + task
with only `MaxTTLSeconds=1800` → expect 422 with the field message (today: 201/200, then 500 at start).

### I4. Retried EXTEND gets a fresh Idempotency-Key, so a lost response burns a second credit

`frontend/src/features/instances/components/InstancePanel.vue:26-28,115-132`: `retryKeys[next]` is never
assigned a generated key — `key` stays `undefined` on the first attempt, and
`retryKeys[next] = key ?? retryKeys[next]` writes `undefined` back. `instancesApi.taskAction` generates the
key internally (`frontend/src/features/instances/api.ts:64-78`), so the retry sends a **different** key. If
the first EXTEND was applied server-side and only the response was lost (timeout/abort), the retry extends
again and spends a second credit. The comment in both files claims the opposite, and no test covers
`InstancePanel` — this is exactly the "no double credit" guarantee the feature advertises, broken in the
only client that uses it.

Fix: generate the key once per user intent in the component (`retryKeys[next] ??= createIdempotencyKey()`)
and pass it explicitly, clearing it only after success; or have `taskAction` return/accept the key it used.
Regression test (vitest, new `InstancePanel.test.ts`): reject the first call, click Extend twice, assert both
`taskAction` calls receive the same `Idempotency-Key`; assert a third click after success gets a new one.

### I5. A failed destroy during the new late-expiry path orphans the provider object

`internal/instances/orchestrator.go:261-273`: after a provider call outlives the deadline, the freshly created
resource is destroyed inline; on success nothing is persisted (correct), but if `driver.Destroy` fails the
error is returned **before** `nodeResult` (`internal/instances/store_lifecycle.go:516-535`, the only writer of
`instance_nodes.provider_resource_id`). The node keeps an empty provider id, so the later cleanup
(`abortExpired` → `nodeProviderIDs`, `internal/instances/deadline.go:95-108`) sees nothing, the DESTROY
operation short-circuits (`orchestrator.go:249-252`), and `releaseResources` marks the instance `EXPIRED`
while the labelled container/VM survives. Failed operations are documented as not retried, so only a manual
label sweep recovers it.

Fix: on the destroy error, persist the id first (`nodeResult` with an `ERROR`/`UNKNOWN` state carrying
`resource.ProviderID`, or a narrow `recordProviderID`) and then return the error, so the next reconcile pass
can destroy it. Regression test: fake driver whose `Destroy` fails → assert `instance_nodes.provider_resource_id`
is non-empty and that a second reconcile issues DESTROY with that id.

## Minor

- **M1. Legacy reason text is misleading** — `InstancePanel.vue:59-60` reports "No further extension budget
  before the deadline" for a legacy row where the server would in fact grant an extension (follows I1).
- **M2. `canReset` is meaningless for PENTEST** — `internal/instances/store.go:598-604` never consults
  `pentest_configs.reset_enabled`/`max_resets`, and pentest snapshots never get a lifecycle block
  (`internal/instances/service.go:300-303`). No current consumer (`InstancePanel` is task-only, wired from
  `TaskPage.vue:237`), so this is contract noise, not a live defect.
- **M3. EXTEND does not re-check the board/module window** — `extendInstanceAtomic` guards status, expiry and
  the hard deadline but not `board.status`/`module.enabled`; with no `ends_at` a credit can be spent for time
  the worker will refuse to provision. The design only lists RUNNING + not-expired + live actor auth, so this
  is a design gap rather than an implementation bug.
- **M4. Swagger minima exceed operator reality** — `Instance.extensionSeconds` (`docs/swagger.yaml`, min 60)
  and `TaskInstanceSummary.ttlSeconds` (min 60) can legitimately be smaller when an operator configures short
  `instances.Options` (the unit tests use 6 s). Harmless in production defaults; it makes the contract
  untrue in acceptance configurations.
- **M5. Seven files are not `gofmt`-clean** (`gofmt -l`): `internal/boards/http/mappers.go`,
  `internal/boards/inputs.go`, `internal/boards/instance_settings.go`, `internal/boards/postgres_tasks.go`,
  `internal/instances/extend.go`, `internal/instances/lifetime_pg_test.go`, `internal/instances/store.go`.
  Alignment only. CI (`go.yml`) does not run a format check, so this is hygiene, not a red build.
- **M6. Report/plan bookkeeping drift** — `docs/instance-lifetime-verification.md:88` claims 24 files / 112
  frontend tests; the tree contains 113 `it/test` declarations in 24 files. The plan marks a `GOARCH` typo
  fix as done (`docs/superpowers/plans/2026-11-24-instance-lifetime.md:74`) while the report correctly states
  no defect exists. Neither affects the product; both make the report slightly untrustworthy.
- **M7. Module editor does not validate max-concurrency** — `ModuleEditorDialog.vue:144-170` checks TTL and
  extension bounds but not `instanceMaxConcurrent` (only the HTML `min`/`max` attributes, which typed input
  ignores); an out-of-range value reaches the server and returns a 422.

## Explicitly checked and found correct

- **Migration `019`** (`migrations/019_instance_lifetime.sql`): additive, every new column nullable, JSONB
  object `CHECK`, range/order/completeness/`instance_enabled` constraints, `down` drops exactly what `up`
  added, `001`–`018` untouched. `instance_extension_requests` unique `(instance_id, request_key)` with actor,
  request hash and granted outcome.
- **PATCH semantics**: omitted preserves, explicit `null` clears to inherit, driven by raw-body presence
  (`internal/boards/http/admin_helpers.go:107-133`) because the generated model collapses absent and null.
  `mergeTaskLifetimeOverrides` + `clearTaskLifetimeOverrides` keep the DB constraint satisfied. Covered by
  `TestTaskInstanceInputLifetimePresence` and the real-PG task test.
- **Precedence and freeze**: `ResolveLifecycle` (task → module → global) drives `start`; the boards read
  model resolves the participant-facing initial TTL with the same precedence through
  `EffectiveInstanceInitialTTLSeconds`, and `TestBoardsAndInstancesAgreeOnGlobalDefaults` pins the shared
  constants. `desired_snapshot.lifecycle` is frozen at CREATE and pentest deliberately gets none; later edits
  affect new instances only, while board/module ends and the hard deadline are re-read live.
- **EXTEND**: `FOR UPDATE OF i` then `SELECT clock_timestamp()`, snapshot step only, base is the current
  expiry, `min(…, hardDeadline)`, zero-gain rejected without a credit, partial gain at the deadline spends
  exactly one, `EXPIRED`/`DESTROYING`/removed never revived, TEAM rows share one budget, staff goes through
  the same caps, replay returns the stored outcome. Actor is bound separately from the hash and a mismatched
  actor is rejected. Instance id is in the path and the subject is re-resolved, so there is no cross-subject
  path.
- **Worker**: expired-before-apply performs zero `Apply` calls; late expiry destroys and never advertises
  `RUNNING`; destroy-only jobs skip the deadline gate and system cleanup (`System:true`,
  `recheck`/`revalidateOperation` early return) can never be blocked by a disabled module, ended board or a
  revoked user, so dangerous cleanup still runs while new APPLY stays denied; leases/ports released only in
  `releaseResources` after destroy.
- **Concurrency**: per-module+subject advisory lock inside the create transaction, live count
  (`removed_at IS NULL`, including `ERROR`/`DESTROYING`) across different task ids, released only after the
  destroy job (covered by the two-task test).
- **RESET**: advances generation, keeps `expires_at`, `extension_count` and the anchor; honours frozen
  `resetEnabled` for new instances.
- **Config/env split**: untouched by this diff. `config.yaml` still owns app settings/JWT/origins/flags and
  `.env` stays datasource/Docker-only. The pure-`config.yaml` remote-VM gap
  (`ACTFB_PROVIDER_ALLOWED_HOSTS` env-only, `docs/configuration.md:201`) is pre-existing and correctly
  reported as a blocker, not a regression introduced here; the user's "one binary deploy for container and
  VM" goal is therefore still unmet for remote VMs by policy, not by this slice.
- **Packaging**: the reported Dockerfile architecture-variable typo does not exist. Byte-level check
  (`sed -n 31p Dockerfile | od -c`) shows the correct five-letter variable name (the one that starts
  with `GO` and ends with the letters before `=amd64`) followed by `=amd64`; the value is correct.
  A plain `grep` of this file reproduces the earlier garbled display, which is what the report already
  explained: the two middle letters of the variable name are lost in terminal rendering. No source
  change was needed and none was made. Migrations are copied with `COPY migrations /app/migrations`,
  so `019` ships.
- **Frontend logic**: minute/hour conversion is stable (`Math.round` on both directions), countdown clamps at
  zero, `remaining === null`/`NaN` handled, `!step` never renders "unlimited", `extensionsRemaining === 0`
  renders "No extensions left", Inherit sends explicit nulls, an edit sends only the lifetime payload and
  never the immutable binding or write-only secret parameters (test asserts all three absences).

## Coverage boundaries (not claims)

- Real-PostgreSQL, NATS, Docker `start`/EXTEND/expire results are the implementer's report; I did not re-run
  them. The 18-test E2E/browser harness was not re-run; per the report the only blocker is pulling
  `clamav/clamav:1.5.4`, a normal fixture download, not a genuine product blocker. No browser acceptance is
  claimed here either.
- Backend coverage gaps I would close with the fixes above: legacy EXTEND/reset (I1, I2), effective-limit
  validation (I3), failed-destroy orphan (I5), plus an HTTP-level EXTEND test (409 expired, replay 200,
  cross-actor 409) which today exists only at service level.
- Frontend coverage gap: `InstancePanel.vue` has no component test at all, which is why I4 survived 113 green
  tests. The `TaskEditorDialog`/`ModuleEditorDialog` tests are real and meaningful but do not cover the
  runtime panel.
- This is a review of the instance-lifetime slice only, not a whole-product audit; other lanes (plugins, AD,
  pentest submissions, packaging runtime scripts) were not re-assessed.

---

# Round 2 re-review: fixes `a017594..2d2461d` (36 files, +2482/−123)

Date: 2026-11-24
Fix report: `docs/instance-lifetime-review-fixes.md`
Scope: my round-1 findings I1–I5 and M1–M7 plus the remote-VM allowlist, and any regression the fixes
introduced. Read-only static review of the fix diff and the current code at every changed site. Per the brief
I ran no builds, tests, Docker/Podman, servers or browser, and spawned no subagents. The reported real-PG,
race, Docker and 19-test browser results are the implementer's, not reproduced here.

## Verdict

**Round-1 Important findings: all five fixed correctly and consistently, not papered over.** The fixes share
one resolver instead of adding a second opinion, move the authoritative validation into the mutation
transaction, and persist state before external calls. No redesign: `/api/v2`, one `cmd/instance-worker`, the
five drivers, ids-only jobs and the additive `019` schema are untouched (`git diff` over
`migrations/`, `docs/swagger.yaml`, `internal/generated/` is empty). `config.yaml`/`.env` split unchanged
apart from the additive `integrations.allowed_hosts`.

**One new Important finding** (narrow crash/DB-error window in the new late-expiry bookkeeping) and four
Minors. Counts for this round: **Critical 0, Important 1, Minor 4.**

**Push readiness: not yet — fix N1 first (about 3 lines + one regression test).** Everything else in this
range is green; I would not push N1 as-is because it turns a handled failure into a permanently stuck row.

## Round-1 findings: verified fixed

### I1 — legacy read model vs action (fixed, and the fix is the spec-conform one)

`resolveInstancePolicy` (`internal/instances/lifetime.go:219-228`) is now the single resolver: frozen policy
wins, otherwise `legacyPolicy(options)` plus the **live** module reset switch. `store` carries `Options`
(`internal/instances/store.go:19-27`) and the service builds the store **after** applying options
(`internal/instances/service.go:124-131`), which is the ordering that makes the non-default-Options test
meaningful. The read model resolves it per instance (`internal/instances/store.go:444-445`) and the action
path uses the same function (`internal/instances/service.go:451`); `extendInstanceAtomic` keeps
`policyFor(snapshot, options)` (`internal/instances/extend.go:143`), which resolves legacy rows to exactly the
same initial/max/step/count, so `canExtend`, `extensionSeconds`, `extensionsRemaining` and the actual grant
now agree by construction. The non-default `Options{ExtensionTTL: 7m, MaxExtensions: 5}` assertion is what
proves the operator value is the source, not the 30 min/3 default. The old test that locked in
`canExtend=false` was corrected rather than deleted. Board/module ends still clamp live, and no snapshot was
rewritten retroactively.

### I2 — legacy RESET follows the live module switch (fixed)

Same resolver for the read model and `applyAction`, plus `resetAllowed(now)` (`internal/instances/store.go:640-648`)
as one shared precondition used by both (`store.go:634`, `service.go:434`). Verified for the four cases the
test names: module disabled → refused and `canReset=false`; re-enabled → both flip; expired → refused with
reset enabled; new instances keep their frozen value.

Note one intentional tightening: RESET is now also refused when `expires_at` has passed but the row is still
`RUNNING` (previously the API allowed it and only the worker blocked it). The read model and the action agree,
so this is the "honest capability" the slice promised, not a regression.

### I3 — effective pair validated, under one advisory lock (fixed)

`EffectiveLifetimeSeconds` (`internal/boards/instance_settings.go:253-269`) resolves the pair with the
documented precedence and `validateTaskLifetimeOverrides` compares the **effective** values
(`instance_settings.go:206-215`), so the `maxTtlSeconds: 1800` over a 3600 module default case is now a 422
instead of a later 500. The authoritative check moved into the mutation transaction
(`validateTaskLifetimeTx`, `internal/boards/instance_policy_tx.go:40-49`, called from
`postgres_tasks.go:221-233` and `:284-295`) and module updates re-check every instance-enabled task
(`validateModuleLifetimeTx`, `instance_policy_tx.go:64-102`, called from `postgres_modules.go:201-209`) with
the offender named. Both take `pg_advisory_xact_lock` on the same per-module key
(`instance_policy_tx.go:16-24`), and because every writer takes that lock first there is no lock-order
inversion with the module row update or the per-subject instance lock (different key, different package).
The serialize test asserts the committed pair is always usable, which is the invariant that matters.

### I4 — retried EXTEND reuses its key (fixed)

`InstancePanel.vue:118-137` now generates one key per intent, keeps it across failures, deletes it only after
a confirmed success, and the `watch` on `instance.value?.uuid` (`:149-155`) drops it when the identity
changes; `if (busy.value) return` prevents a double submit. `taskAction` already accepted an explicit key, so
the wire is unchanged. `InstancePanel.test.ts` asserts the same key on retry, a different key after success,
the server step/remaining render, and the disabled reason — the coverage gap that let I4 through is closed.
The error alert moving inside the panel is required for the retry button to stay reachable and does not hide
the instance.

### I5 — provider id durable before destroy (fixed, plus the multi-node follow-up)

`stageExpiredResource` (`internal/instances/store_lifecycle.go:754-...`) writes the provider id, node
`DESTROYING` and a `QUEUED` DESTROY op for the **current generation** in one transaction before any external
call; a failed destroy leaves that op queued so `PendingJobs` retries it with the persisted id
(`orchestrator.go:284-294`). Empty-id `RUNNING` is an error rather than a fake success
(`orchestrator.go:274-281`). `finishExpiredDuringApply` (`orchestrator.go:600-...`) refuses to release while
any DESTROY is still `QUEUED`/`RUNNING` and while any node still owns a provider object, queueing destroy for
the survivors instead — so a multi-node template holds ports and leases until the last provider ACK, and the
release happens exactly once (both asserted by `TestLateExpiryFailedDestroyPersistsProviderIDRealPostgres`
and `TestLateExpiryWithAnotherLiveNodeQueuesDestroyRealPostgres`). Duplicate provider destroys are avoided
because the staged op is `SUCCEEDED` and the id is cleared, so a later `destroyInstance` hits
`ON CONFLICT … DO NOTHING` and the empty-id destroy is a no-op. No `instance.started`/`created` event is
emitted on this path, and the no-RUNNING guarantee is preserved.

### Minors M1–M7

M1 legacy reason text now truthful (`InstancePanel.vue:63`). M2 `applyPentestCapabilities`
(`store.go:653-666`) reads the live `pentest_configs.reset_enabled` and the consumed budget, never exposes
extension fields, and both joins are 1:1 (`pentest_configs.module_id` is a PK, `pentest_environments` is
unique on module+subject), so `QueryRow` cannot silently pick a stale row. M3 EXTEND re-reads the board/module
window under the row lock with the post-wait clock (`extend.go:137-142`, `liveWindowError:212-...`) and
rejects before any credit or idempotency row. M4 resolved as a documented ruling (design §7a) with the
contract left at 60 s. M5 `gofmt -l internal/ cmd/` is now empty (re-verified). M6 the plan no longer claims a
`GOARCH` fix and the verification report's browser gap is corrected. M7 the module editor validates
concurrency 1..100 with a field message plus a component test.

## N1 (new, Important) — "destroy confirmed" bookkeeping is not atomic, and the wrong side is written first

`internal/instances/orchestrator.go:295-298`:

```go
_ = o.store.markOperation(ctx, cleanupOp, "SUCCEEDED", "")
if err := o.store.confirmStagedDestroy(ctx, instance.ID, operation.NodeKey); err != nil {
    return err
}
```

Two separate statements. If the worker is killed, or the second statement fails with a transient DB error,
between them, the durable state is: DESTROY op `SUCCEEDED`, `instance_nodes.provider_resource_id` still set,
node `DESTROYING`. That state cannot heal itself:

- `pendingJobs` only returns `QUEUED`/`RUNNING` ops (`store_lifecycle.go:688`), so the `SUCCEEDED` op is
  never re-queued.
- `expireDue` only selects `RUNNING/ERROR/PROVISIONING/RESETTING` (`store_lifecycle.go:635`), so once the row
  is `DESTROYING` it is never re-queued either; while still `PROVISIONING` it is re-queued but
  `destroyInstance` hits `ON CONFLICT … DO NOTHING` (`:398`) and appends the existing op id anyway, so the job
  claims nothing (`claimOperations` requires `status IN ('QUEUED','RUNNING')`) and `expireDue`'s
  `len(ids)==0` release branch is skipped because the slice is not empty.
- Result: the row keeps its public port and network lease indefinitely and needs manual SQL. The provider
  object itself is already gone, so this is not an orphan container — it is a permanent leak of the
  *reservation*, in the path whose whole purpose was durability.

Minimal fix (either, both small):
1. do both writes in one transaction (mark the op `SUCCEEDED`, clear the id, set the node `DESTROYED`), or
2. swap the order — `confirmStagedDestroy` first, then `markOperation`. Then a crash in either window leaves
   the op `QUEUED` with an empty id, and the retry self-heals: `applyOperation` treats an empty id as a no-op,
   the op is marked `SUCCEEDED`, `destroyAll` triggers `releaseResources`. The driver already treats a missing
   resource as success (`internal/runtime/containerapi/driver.go:771-782`).

Smallest regression test (deterministic, no crash hook needed): seed the post-crash state directly — node
`DESTROYING` with a provider id, DESTROY op `SUCCEEDED` — then run `ExpiredJobs` + `PendingJobs` +
`HandleJob` and assert the instance reaches `EXPIRED` with `removed_at` set and 0 live endpoints/leases. It
fails against the current code and passes after either fix.

## N2–N5 (new, Minor)

- **N2. Report package count is off by one.** `docs/instance-lifetime-review-fixes.md:150` says
  "49 packages ok"; the tree has 50 packages containing test files (`go list` over `./...`, 63 packages
  total). Frontend counts are exact (25 files / 118 `it`/`test` declarations) and the browser count is exact
  (19 tests across the three specs, and `run-full-e2e.sh` exports the `E2E_DOCKER_*`/`E2E_INSTANCE_PORT`
  values the new test skips without). Not verified by rerun.
- **N3. A module may cap extensions at zero and a task may still raise it via the API.**
  `internal/boards/instance_settings.go:240` only compares `count > module.Extension.MaxCount` when
  `module.Extension.MaxCount > 0`, and a module `{enabled:true, seconds:900, maxCount:0}` is accepted. A direct
  `POST/PATCH` can then set a task `extension.maxCount: 3` over a module whose zero means "no extensions"
  (the task editor UI blocks it, so this is API-only). Pre-existing from `a017594`, not introduced by the
  fixes; one `>=`/`==0` branch away from honest.
- **N4. `allowed_hosts: []` cannot override a set legacy env var, and the comments imply it can.**
  `deploy/config.yaml:100-106` and `docs/configuration.md:212-215` say "empty denies every remote provider
  URL", while `internal/config/load.go:300-312` falls back to `ACTFB_PROVIDER_ALLOWED_HOSTS` whenever the YAML
  list is empty, and its own comment says an inherited environment "cannot widen a YAML-configured policy"
  (true only for a non-empty YAML list). **No security regression**: env-only was the prior behaviour, the
  worker now also validates remote compute base URLs (`cmd/instance-worker/test_connection.go:68-74`), the
  SSRF guard is untouched, and no credentials enter `config.yaml`. It is a documentation/semantics mismatch
  for the operator who explicitly wants an empty allowlist on a host that still exports the old variable.
- **N5. `policyForSnapshot` is now dead in production.** `internal/instances/lifetime.go:207-212` is only
  referenced by `TestPolicyForSnapshotFallsBackToLegacy`; the live paths use `resolveInstancePolicy` and
  `policyFor`. Keeping a second legacy resolver alive invites the exact drift I1 was about — either delete it
  and re-point the test, or make `policyFor` call `resolveInstancePolicy`.

## Boundaries of this round

- Static review only. No build, test, race, Docker, PostgreSQL, NATS or browser run; the fix report's green
  results (real PG 18 + NATS, real Docker late-expiry/idempotency/concurrency, `-race` on the changed
  packages, 118 frontend tests, 19/19 browser) are taken as reported. The new browser test is genuine (real
  UI interactions plus API assertions on persisted values), so the earlier "no browser acceptance" limitation
  is correctly retired.
- Not re-audited: plugins, AD, pentest submissions, packaging runtime scripts, and the rest of the product.
  This round covers I1–I5, M1–M7, the allowlist change and regressions in the 36 changed files.
