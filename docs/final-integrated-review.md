# Final integrated review — uncommitted aCTFb work

Date: 2026-11-14
Reviewer: subagent (static review; no product-code edits)
Scope: working tree (`main`, uncommitted), 234 tracked changes (~33.4k diff lines)
plus 693 untracked files, focused on the high-risk cross-feature boundaries named by the
main manager: plugin checker, plugin dependencies, trusted-js approval, plugin SDK/delegation
and private PostgreSQL isolation, Pentest allowlist/binding/sealing, MANUAL/INSTANCE/013/014,
AD/Pentest scoring and IDOR, INSTANCE binding compensation, file/scanner authorization,
and the e2e service-lifecycle harness.

## Evidence capture

Read-only review package (not committed):

```text
/tmp/opencode/final-review/git-status.txt
/tmp/opencode/final-review/untracked.txt
/tmp/opencode/final-review/untracked-excluding-binaries.txt
/tmp/opencode/final-review/diff-stat.txt
/tmp/opencode/final-review/tracked.diff
/tmp/opencode/final-review/untracked-sources.tar.gz
```

The tracked diff and a tar of untracked sources are captured there. The two untracked root
binaries (`backend`, `events-dispatcher`) were deliberately excluded from the tar. No
files were deleted, staged, committed, or pushed.

The existing reported test evidence (Docker 6/6, real browser, 001–014 PostgreSQL migrations,
Go/P1/race on changed packages, frontend 68 tests) was **not rerun**. This review is static code
and supporting-document inspection, not a replacement for those runs.

## Verdict

**Not complete.** The reported green gates are plausible and several security boundaries are
implemented well, but this pass found one plugin-isolation defect that should block the plugin
claim, plus unclosed mandatory/functional seams that are not reflected in the top-level
“completion pass” wording. Do not describe the product as locally complete or production-ready.

Counts: **Critical 1, Important 9, Medium/Minor 10.** External provider credentials (OCI
registry, live Proxmox/KubeVirt/VM, GitHub/GitLab, remote CI) are **unverified blockers, not
code findings** and are listed separately at the end.

---

## Critical

### C1. Two distinct plugin IDs can share one private PostgreSQL role/schema (cross-plugin data access)

**Location**
- `internal/plugins/manager_db.go:138` `pluginSchemaName`
- `internal/plugins/manager_db.go:59-60` (`role := schema`)
- `internal/plugins/manager_db.go:72-103` (`CREATE/ALTER ROLE`, `CREATE SCHEMA ...`,
  `REVOKE ALL ON SCHEMA public`, `search_path`)
- `internal/plugins/manager_lifecycle.go:487` (`VolumeName = "actfb-plugin-data-" + pluginSchemaName(...)`)

**Exploit / consequence**
`pluginSchemaName` replaces every character outside `[a-z0-9_]` with `_` and appends a hash
**only when the name is longer than 63 characters**. Consequently all of these valid plugin IDs
(the manifest pattern is `^[a-z0-9]+(?:[._-][a-z0-9]+)+$`) map to the same schema/role
`plugin_example_score_events`:

```text
example.score_events      (the shipped example)
example_score_events      (also a valid ID)
example-score-events      (also a valid ID)
```

The second plugin's `ensurePrivateDatabase` executes `ALTER ROLE plugin_example_score_events
LOGIN PASSWORD <new>`, `CREATE SCHEMA IF NOT EXISTS ...`, `ALTER SCHEMA ... OWNER`, and then
stores its own DSN. That:

1. rotates the first plugin's database password, invalidating the first plugin's stored DSN;
2. gives the second plugin full read/write access to the first plugin's tables and data;
3. shares the same OCI data volume via `VolumeName`, so file data is shared even without SQL;
4. lets two tenant plugins violate the documented “separate private schema” guarantee.

The plugin author/target does not need a core vulnerability: install is admin-approved, but
packages are third-party code and the platform explicitly promises per-plugin isolation. This is
a confidential-integrity boundary failure, not a cosmetic naming bug.

**Smallest fix**
Generate a collision-resistant identifier for every plugin, e.g. always append a stable hash of
the full ID: `plugin_<sanitized-and-truncated>_<sha256/8hex(fullID)>`, and use the same value for
the role, schema and volume. Better, persist the mapping in a `plugin_schema_names` table with
`UNIQUE(plugin_id)` and `UNIQUE(schema_name)` and refuse provisioning on conflict. Handle
existing collisions with a migration/repair path rather than silently reusing a schema.

**Test proposal**
Real-PostgreSQL test installing/enabling two colliding IDs, assert distinct `current_schema()`,
distinct role OIDs and that each role cannot read the other schema. Add a unit test table over
`example.score_events` / `example_score_events` / `example-score-events` asserting unique names.
Also assert the OCI `VolumeName` values differ.

---

## Important

### I1. Plugin service token is reused across core restarts; orphaned plugin processes/containers are never stopped

**Location**
- `internal/plugins/manager_lifecycle.go:35-37`, `:51-52` (`rotate := installed.Status != StatusEnabled`)
- `internal/plugins/manager_lifecycle.go:686-707` (`runtimeToken` returns the stored token when
  `rotate == false`)
- `internal/plugins/manager_lifecycle.go:393-421` (`RecoverEnabled` starts with `rotateToken=false`)
- `internal/plugins/process/runner.go:241-264` (exec `Stop` only when explicitly called)
- `cmd/backend/main.go` (no `pluginManager.Close` / runtime shutdown hook)
- `cmd/plugin-runner/main.go` (shutdown stops the HTTP server, not plugin containers)

**Exploit / consequence**
`ExecRunner` starts the plugin as a child process with `Setpgid: true`, and `DockerRunner`
starts a long-lived container. Neither is tied to the core process lifetime. After a backend
restart or crash the old plugin process/container can survive with:

- the same `ACTFB_PLUGIN_SERVICE_TOKEN` from `plugin_settings`;
- its private DSN, so it can continue reading/writing plugin data;
- its JetStream consumer connection.

The new core has no runtime entry, so the plugin appears “enabled” but returns 503. If an API
`Enable` is issued for the still-`enabled` row, `rotate == false`, the new process receives the
same token, and `Manager.VerifyServiceToken` (`internal/plugins/coreproxy.go:25-42`) will accept
the **old orphan** as the current plugin identity. Disabling first also does not stop the orphan,
because `stopRuntime` has no runtime entry; it only rotates the token on the subsequent enable.
This breaks the “disable stops the backend / service identity is per running instance” contract.

**Smallest fix**
Rotate the service token on every process start (or bind the identity to a process/start nonce
and verify that nonce), add a `Manager.Close`/shutdown path called from `cmd/backend`, and make
the plugin-runner stop/relabel its containers on shutdown. On boot, reconcile exact
`actfb.plugin` labels/IDs: adopt or kill only the exact managed instances, never broad-kill.

**Test proposal**
Start a plugin, kill the backend process without calling Disable, restart, and assert the old
PID/container is gone or has an invalid token. Re-enable and assert the old token is rejected by
`VerifyServiceToken` while the new one succeeds. Also assert shutdown leaves zero
`actfb-plugin-*` containers/processes for the owned project.

### I2. `INSTANCE` task submissions are silently auto-rejected

**Location**
- `internal/boards/service_tasks.go:271-389` — branches exist only for `FLAG`, `EXTENSION`,
  `MANUAL`
- `internal/boards/validation.go:184-228` — `INSTANCE` is a first-class task type requiring
  `MANUAL` checker and a non-`NONE` submission mode

**Exploit / consequence**
For `TaskTypeInstance` the default variables remain `status = REJECTED`, `score = nil`,
`accepted = false`. No text-policy check, no draft verification, no file attachment and no
`PENDING_REVIEW` path run. `CreateSubmission` then persists a `REJECTED` row. A participant who
submits the required text/files for an INSTANCE task can never get it accepted or reviewed; the
task is effectively unusable through the documented submission route. This is a cross-feature
boards↔instances contract break.

**Smallest fix**
Treat `INSTANCE` exactly like `MANUAL` for submission/review (same policy, draft/files, and
`PENDING_REVIEW`), or return a typed “submissions not supported for INSTANCE” error instead of
silently rejecting. Add a real-PG test submitting an INSTANCE task with TEXT and
TEXT_AND_FILES and asserting `PENDING_REVIEW` and attached files.

### I3. Plugin boot recovery is not wired; if wired as written it bypasses dependency and trusted-js gates

**Location**
- `internal/plugins/manager_lifecycle.go:390-422` (`RecoverEnabled` exists, never called)
- `internal/plugins/manager_dependencies.go:212-234` (enable-time dependency/trusted-js gate)
- `cmd/backend/main.go` / `cmd/backend/routes_integration.go` (no recovery call)

**Exploit / consequence**
After a core restart, `plugins.status` remains `enabled` while the runtime map is empty, so the
database says grants/plugin state are active while every plugin route is 503. This is a direct
availability and status-truthfulness failure. If recovery is wired as-is, it starts plugins
without `ensureEnableDependencies` and without the trusted-js operator/approval check, so a
dependent plugin can run while its dependency is missing/disabled/failed, and a trusted-js
backend can start even if `ACTFB_PLUGIN_ALLOW_TRUSTED_JS` is now false or the install approval
is no longer valid. (`startInstalled` only checks checker-key conflicts.)

**Smallest fix**
Call `RecoverEnabled` once after migrations/manager construction, and inside it call
`ensureEnableDependencies` plus the trusted-js gate before `startInstalled`; mark individual
plugins failed without failing core. Rotate the token as in I1.

**Test proposal**
Enable A and B(A→B), restart the manager, assert both recover in dependency order; disable B and
restart, assert A is not started; flip `ACTFB_PLUGIN_ALLOW_TRUSTED_JS` off and assert the
trusted-js plugin does not start.

### I4. Per-task text policy (`required/minLength/maxLength`) is not enforced at submission; empty `TEXT_OR_FILES` is accepted

**Location**
- `internal/boards/service_tasks.go:171-175` (only the global 1,000,000-rune cap)
- `internal/boards/service_tasks.go:352-366` (only “text != ''” for some modes)
- `internal/boards/types.go:96` (`maxTaskTextLength = 1_000_000`)
- `internal/boards/validation.go:242-285` validates policy shape, not runtime use

**Exploit / consequence**
Admins can configure a task with `text.minLength`, `text.maxLength` and `required=false`, but
submission ignores all of them. A task configured for, say, 10–100 characters accepts 1,000,000
characters, and a task with `required=false` still rejects empty text for `TEXT` mode.
`TEXT_OR_FILES` with neither text nor a draft passes the early checks (the draft is required only
for `FILES` and `TEXT_AND_FILES`), producing an empty `PENDING_REVIEW` row. This is a policy
bypass plus a submission-storage/DoS lever.

**Smallest fix**
In `SubmitTask`, after loading the task, evaluate `task.TextPolicy` for `MANUAL`/`INSTANCE`
(required, min/max rune length) and require at least one of text or draft for
`TEXT_OR_FILES`. Reject empty submissions before `CreateSubmission`.

**Test proposal**
Table-driven service tests for required/min/max on `TEXT`, `TEXT_AND_FILES`, and `TEXT_OR_FILES`,
including an empty OR submission, asserting 422 and no row.

### I5. PENTEST draft is not consumed and the same clean file can be re-attached/moved between submissions

**Location**
- `internal/instances/service_pentest.go:415-484` (`insertPentestSubmission`)
- `internal/instances/service_pentest.go:459-466` (`UPDATE file_objects ... WHERE id=$1 AND
  status='READY' AND attached_submission_id IS NULL`)
- Compare the correct task path: `internal/boards/postgres_submissions.go:565-572`
  (`UPDATE submission_drafts SET status='CONSUMED' ...`)

**Exploit / consequence**
The PENTEST submission transaction never marks the draft `CONSUMED`. A participant can submit
the same verified draft repeatedly with different idempotency keys. The file update also omits
`attached_pentest_submission_id IS NULL`, so a READY file already attached to an earlier PENTEST
submission is re-pointed to the newest one while both `pentest_submission_files` rows remain.
This is an integrity/immutability violation and can confuse reviewer evidence; it is not a
cross-board IDOR because `VerifyDraft` is actor/board/module scoped, but it violates the
“submitted evidence is immutable” expectation.

**Smallest fix**
In the same transaction: `SELECT ... FOR UPDATE` the draft, require `status='OPEN'`, and set
`status='CONSUMED'`; only attach files with `status='READY' AND attached_submission_id IS NULL
AND attached_pentest_submission_id IS NULL` and verify `board_id/module_id/uploader_user_id` in
the WHERE clause. Add a partial unique/check constraint for one active pentest attachment.

**Test proposal**
Submit the same draft twice with two idempotency keys; assert the second is rejected and the file
still points at submission #1. Concurrent submit/review test asserting one file cannot appear on
two PENTEST submissions.

### I6. Removed participants keep read access to their live instances/endpoints

**Location**
- `internal/instances/service.go:596-607` (`authorizeRead`)
- `internal/boards/service_admin_submissions.go:246-266` (`AdminRemoveParticipant`)
- `internal/instances/service_pentest.go:87-104` (`PentestEnvironment` delegates to `Get`)

**Exploit / consequence**
For a `USER` instance, `authorizeRead` returns success whenever `actor.UserID == SubjectID`.
It never rechecks `board_participants`, access policy, or `subjectInModuleAudience`. Removing a
participant deletes the board participant row but does not destroy/revoke their instances. A
removed/kicked user can still `GET` the instance and see endpoint URLs/status until TTL expiry
or explicit destroy; `PentestEnvironment` inherits that read path. Orchestrator operations are
revalidated, but read access and live connectivity are the sensitive part for a lab.

**Smallest fix**
Make `authorizeRead` require current participation/audience (the same checks used by
`authorizeParticipant`/`ensureAudience`), and consider destroying or reassigning instances on
participant removal. Add a test: start → remove participant → GET returns 404/403, not endpoints.

### I7. `/boards/{id}/scoreboard/me` returns 0/place 1 for TEAM boards

**Location**
- `internal/boards/service_tasks.go:485-517` (`GetMyScoreboardEntry`)
- `internal/boards/service_tasks.go:519-575` (`mergedScoreboard`)

**Exploit / consequence**
`mergedScoreboard` builds TEAM rows with `Subject.Type = TEAM` and `UserID` empty. The
“my entry” loop matches only `items[i].UserID == principal.UserID`, so every team member falls
through to the fallback `{UserID: principal.UserID, Score: 0, Place: 1}`. The UI displays
“Your score 0 / Your place 1” even when the team is winning. This is a score-scope bug, not a
privacy leak.

**Smallest fix**
Resolve the principal’s score subject (`ResolveSubject`) and match on
`Subject.Type` + `Subject.ID`; keep `UserID` only for the INDIVIDUAL convenience field. Add a
TEAM real-PG/service test with accepted points and assert the team’s score/place.

### I8. `ExecRunner` group termination has a PID-reuse/broad-kill hazard

**Location**
- `internal/plugins/process/runner.go:241-264` (`Stop`)
- `internal/plugins/process/runner.go:330-356` (`terminateProcessGroup`)

**Exploit / consequence**
`terminateProcessGroup` sends `SIGTERM`/`SIGKILL` to `-pid` using the PID recorded at start. It
does not check `/proc/<pid>/stat` start time and does not skip when `cmd.ProcessState` is already
set (the child was reaped). If the plugin process exits and the PID is reused as a different
process-group leader, `Stop` can signal an unrelated group (including a user/parent tree in the
worst case). The e2e harness was explicitly fixed for this exact trap; the plugin exec runner
still has it.

**Smallest fix**
Record the `/proc/<pid>/stat` start time (or use `pidfd`/`PidfdOpen` on Linux) at `Start` and
verify it before any group signal; if the process was already reaped, only `Wait`. Never fall
back to a PID-only kill. Add a unit/integration test with a stale PID/start-time and a live
unrelated group leader, asserting no signal is delivered.

### I9. Mandatory trusted-js UI mounting is still absent (spec gap, not a sandbox bug)

**Location**
- `internal/plugins/manifest.go:162-174`, `internal/plugins/manager_install.go:193-207` (accept +
  persist approval)
- `frontend/src/features/plugins/components/PluginFrame.vue:45-56` (only iframe is rendered)
- The removed historical plugin TZ §6 (retained in `docs/plugin-system.md` §9) requires a `trusted-js` ESM
  mode mounted into a Vue slot with
  pinned assets and explicit approval.

**Exploit / consequence**
The backend correctly gates package acceptance behind `ACTFB_PLUGIN_ALLOW_TRUSTED_JS=false`
(default) plus per-install approval, and the browser never mounts trusted bundles. That is a
safe state, but the mandatory spec feature is not implemented. `docs/implementation-plan.md:295`
says “implemented except external OCI and trusted-js browser mounting”; the current wording is
honest about the mount, but a “completed” local product claim is not accurate against the
mandatory spec unless the main manager explicitly accepts the deviation.

**Smallest fix**
Either implement a platform-origin, digest-pinned ESM loader with explicit approval and CSP
review, or record the deviation as an accepted scope reduction in the spec/implementation plan.
Do not weaken the iframe default; the iframe path is the correct security baseline.

---

## Medium / Minor

1. **PENTEST reset limit is raceable.** `internal/instances/service.go:395-444` reads
   `pentestResetsUsed`, queues `resetInstance`, then calls `incrementPentestResets` without a
   lock and ignores its error; `internal/instances/store_lifecycle.go:187` has no per-environment
   lock. Concurrent RESETs can exceed `maxResets`. Fix: conditional `UPDATE ... WHERE
   resets_used < max` under an advisory/xact lock, queue only if updated. Test with concurrent
   resets.
2. **`parametersPolicy` is stored but never enforced.** `internal/instances/binding.go:329-376`
   validates only the immutable manifest; `docs/runtime-instances.md:134-136` documents the
   deferral. If an operator relies on the policy to restrict values (limits/images), this is a
   policy bypass. Fix: reject non-empty policy until a schema exists, or implement and test it.
3. **Re-bind with no secrets wipes sealed secrets.** `internal/instances/binding.go:259-296`
   sets `secret_parameters_ciphertext`/`secret_key_version` to NULL when `SecretParameters` is
   empty, including the “reuse existing binding row” path used by a Pentest template-version
   change. Fix: `COALESCE`/preserve ciphertext when no replacement is supplied, or require all
   declared required secrets on rebind. Test version-change + secret retention.
4. **Binding can target a task UUID from another board/module.** `internal/instances/binding.go:308-312`
   ignores `UPDATE tasks ... RowsAffected`; an internal caller can create an orphan binding.
   Validate the task row (board/module/type) in the transaction and fail on zero affected rows.
5. **Prerelease plugin upgrades are rejected.** `internal/plugins/manager_lifecycle.go:792-811`
   uses a numeric-only `compareVersions` instead of the semver parser, so `1.0.0-beta → 1.0.0` is
   “not greater”. Use `parseSemVersion`/`compareSemVersion` and add prerelease tests.
6. **`trusted_js_approved` is not restored on upgrade rollback.** `internal/plugins/manager_lifecycle.go:288-315`
   omits the flag, so after a failed trusted-js upgrade the manifest/flag pair can be
   inconsistent (either an iframe manifest with `true`, or a restored trusted-js manifest with
   `false` after the upgrade tx set it false). Persist the previous flag in rollback; test both
   directions.
7. **E2E cleanup is narrower than the “0 containers” claim.** `e2e/run-full-e2e.sh:63-71` removes
   only the fixed infra container names and `${PROJECT}-clamav-db`; it does not remove
   `actfb-plugin-*` containers or `actfb.managed=true` instance containers/volumes. A failure or
   SIGTERM before the in-test Destroy leaves provider resources. The lifecycle test does not
   exercise real PID reuse (it fakes the start time). Add label-filtered cleanup + an automated
   post-run assertion, and test a failure path and a real reused PID/start-time pair.
8. **Status documentation is internally inconsistent / overstates closure.**
   - `docs/implementation-plan.md:280` still says AccessGroup/Audience/Team is “missing … explicit
     422 today”, while `:290` says TEAM/PUBLIC/INVITE_ONLY is implemented and browser-verified.
   - `:295` says the plugin system is implemented except external OCI and trusted-js mounting,
     omitting I1 (no shutdown/orphan cleanup), I3 (no boot recovery), and C1 (schema collision).
   - `docs/all-stages-verification.md` “completion pass … remaining local seams were closed” is
     broader than the actual state (INSTANCE submissions, text policy, recovery, cleanup).
   - The browser A+D score test (`e2e/tests/full-stage.spec.ts:164-213`) seeds `ad_awards` via
     `psql`, not through the real checker; real checker acceptance exists separately in
     Gitea/Docker tests. State that distinction explicitly.
   - “0 owned containers/processes” is a manual post-run observation, not an assertion in the
     harness.
9. **The private-role/public-schema claim is stronger than the code.** `internal/plugins/manager_db.go:98`
   uses `REVOKE ALL ON SCHEMA public FROM <role>`, which does not remove privileges inherited via
   `PUBLIC`. The role likely retains `USAGE` on `public`; no core table is PUBLIC-readable today,
   and no `SECURITY DEFINER` core function exists, so this is hardening/documentation, not a
   current data-read exploit. To match the docs and `plugin-developer-guide.md`, verify with
   `has_schema_privilege` and revoke PUBLIC usage/default function execute in a controlled way,
   or change the documentation.
10. **Untracked 58 MB `backend` and 21 MB `events-dispatcher` binaries are not ignored.**
    `.gitignore` ignores `bin/` but not these root artifacts. Add them to `.gitignore` (and
    remove from the working tree separately) so a future broad `git add` cannot commit binaries.
    Also `aCTFb_Codex_kit.zip` is untracked; decide intentionally.

---

## Positive findings (verified statically)

These boundaries are implemented in the expected direction and should not be regressed:

- **Plugin checker cannot authoritatively score.** `internal/boards/service_tasks.go:329-350`
  uses only the frozen `task.Points`; `result.Score` is deliberately ignored, and
  `internal/boards/postgres_submissions.go:159-165` re-checks it inside the locked transaction.
  Plugin calls happen before `CreateSubmission`, so no transaction lock is held during the HTTP
  call; checker timeouts are bounded (`internal/plugins/checker.go:103-127`).
- **Checker does not receive answers/flags.** `boards.PluginCheckRequest` is constructed with
  text only (`internal/boards/service_tasks.go:319-325`); no answer hashes/flag bytes/files are
  passed. The checker's shared permission is namespaced and live-grant checked
  (`internal/plugins/checker.go:213-226`), and revocation/disable/timeout fail closed.
- **Plugin manifest strictness/dependencies.** Strict YAML, route/permission/checker namespace
  validation, exact upgrade subset, cycle detection, semver ranges and disable/remove dependent
  guards are present (`internal/plugins/manifest.go`, `dependencies.go`,
  `manager_dependencies.go`). The gap is recovery, not the normal lifecycle.
- **Plugin SDK identity/delegation.** Service token is compared in constant time against live
  runtimes; delegation is HMAC-signed, DB-backed, scope-bound, expiry-checked, one-time nonce,
  and bound to the plugin ID (`internal/plugins/identity_signer.go`, `sdk/server.go`,
  `coreproxy.go`). User/scope always come from the delegation, never the request body.
- **Opaque iframe.** `sandbox="allow-scripts allow-forms"` with no `allow-same-origin`,
  source+`origin === 'null'` checks, 192-bit nonce, and no token in the iframe URL
  (`frontend/src/features/plugins/bridge.ts`,
  `frontend/src/features/plugins/components/PluginFrame.vue`).
- **Pentest binding/allowlist.** Binding selects an exact immutable `template_version_id`, exact
  `deployment_template_allowlist` scope, exact target rows and validates manifest parameters;
  secrets are sealed with binding-ID AAD (`internal/instances/binding.go:169-327`). Orchestrator
  re-validates live role/team/audience/window before each operation
  (`internal/instances/orchestrator.go:378-430`).
- **A+D awards are core-owned and idempotent.** Points come from `ad_vulnerabilities`, not
  checker JSON; `INSERT ... ON CONFLICT DO NOTHING` plus a run lock prevents double awards and
  keeps the outbox/audit in the same transaction (`internal/attackdefense/store_verdict.go:41-103`,
  `worker.go:230-275`).
- **013 guard.** MANUAL text is derived from the authoritative task on INSERT, task type is
  immutable on UPDATE, and non-MANUAL text is rejected by both trigger and check constraint
  (`migrations/013_manual_text.sql:18-58`).
- **File state machine.** Scanner READY transition is conditional on `VALIDATING` + live OPEN
  draft; SHA/size/content-type are rechecked; EICAR handling, download scope checks and
  `submissionFileInScope` subject/team checks are present (`internal/files/store.go`,
  `service.go:1083-1178`, `files/store.go:744-816`).
- **E2E process ownership.** `e2e/lib/service-lifecycle.sh` uses per-service process groups,
  `/proc` start-time checks, bounded TERM→KILL inside the owned PGID, and status-preserving
  traps; `e2e/test-service-lifecycle.sh` checks `/proc` child disappearance and a TERM-ignoring
  descendant. This is materially better than the prior PID-only cleanup. The remaining gap is
  resource cleanup breadth, not the group-kill logic.

---

## Spec / status assessment (mandatory vs optional)

- **Mandatory and implemented:** installable manager lifecycle (install → explicit enable →
  disable → upgrade with data retention), namespaced plugin routes/permissions, checker
  extension point, `example.hello`, `example.score_events`, `example.custom_checker`, private
  schema provisioning (with C1 caveat), iframe UI, encrypted settings, event dedupe/replay,
  dependency semver/cycle guards, default-off trusted-js package acceptance, 013/014, AD
  canonical awards, PENTEST Docker path, instance binding compensation.
- **Mandatory but not met:** trusted-js browser mounting (I9; docs classify it as a non-goal,
  so require explicit acceptance), genuine per-plugin database isolation (C1), and boot
  recovery/clean shutdown of plugin runtimes (I1/I3). These prevent a truthful “local complete”
  statement.
- **Optional / explicitly deferred or non-mandatory for this slice:** PENDING_REVIEW for plugin
  checkers (fails closed), plugin-provided deployment drivers/integrations beyond the documented
  acceptance examples, production multi-instance Redis/TLS/least-privilege deployment, remote CI.
- **External and unverified, not code bugs:** real OCI registry/image credentials, live
  Proxmox/KubeVirt/VM endpoints, GitHub/GitLab credentials, remote GitHub Actions execution,
  production S3/IAM/rotation. Do not count these as defects, but do not call them verified.

## Coverage limits

This was a **focused** review, not an exhaustive line-by-line audit of every changed file. I did
not run builds, tests, Docker, Compose, migrations, or the browser suite, and did not re-verify
the reported 6/6 evidence. I did not exhaustively review the full 1.3 MB Swagger diff, every
frontend feature, the full deployment template validator/renderer, auth/retention internals, all
194 route handlers, or all 693 untracked files. Findings above are based on direct inspection of
the named cross-feature paths and the current docs; absence of a finding in an unexamined area is
not a clean bill of health.

## Recommended blocking order

1. C1 schema/role collision (security isolation).
2. I1/I3 plugin shutdown/recovery/token lifecycle (security + availability).
3. I2 INSTANCE submission branch (core task contract).
4. I4 text policy and I5 PENTEST draft/file integrity.
5. I6 stale instance read authorization.
6. I7 TEAM own-score, I8 exec PID-reuse, I9 trusted-js decision/sign-off.
7. Reconcile docs/status and add the missing regression tests before any “complete” claim.