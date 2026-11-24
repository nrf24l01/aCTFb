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
