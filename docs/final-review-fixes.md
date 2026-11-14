# Final integrated review — fix ledger

Date: 2026-11-14
Source: `docs/final-integrated-review.md` (1 Critical, 9 Important, 10 Medium/Minor).
Scope owned by the main manager; no subagents were used. No files were staged, committed, or pushed.

## Verdict

All **Critical and Important** findings are fixed and verified. All ten Medium/Minor findings were
inspected; true security/functional issues were fixed, and the one residual PostgreSQL hardening
claim is explicitly deferred with rationale. External credentials remain unverified blockers, not
code findings.

## Critical

### C1 — private plugin schema/role/volume collision — FIXED

- `pluginSchemaName` now always appends a stable 64-bit SHA-256 suffix of the **full** plugin ID.
  `example.score_events`, `example_score_events` and `example-score-events` map to distinct names.
- Migration `015_plugin_private_identities.sql` reserves `(plugin_id, schema, role, volume)` with
  unique constraints. Existing installations adopt their legacy names on first use; ambiguity fails
  closed with `ErrIdentityConflict`; no rename or data drop.
- The reserved volume is used consistently for the OCI data volume.
- Tests:
  - `TestPluginSchemaNameIsCollisionResistantAndBounded`
  - `TestPluginPrivateIdentityDoesNotCollidePG`

## Important

### I1 — token reuse/orphaned plugin runtimes — FIXED

- Every process start rotates the service token; old tokens are rejected by `VerifyServiceToken`.
- `Manager.Close` stops all owned runtimes and is wired into backend shutdown.
- Exec children get `Pdeathsig=SIGKILL`; plugin-runner reconciles only its exact
  `actfb.plugin.runner=<id>` + `actfb.managed=true` containers on boot and `StopAll`s tracked
  containers on shutdown.
- Tests: `TestPluginTokenRotationCloseAndRecoveryPG`, `TestDockerRunnerReconcilesOnlyOwnedRunnerAndStopsAll`.

### I2 — INSTANCE submissions silently rejected — FIXED

- INSTANCE follows the MANUAL submission/review/text/files path.
- Migration `016_instance_text.sql` widens the durable text guard to `MANUAL,INSTANCE`; FLAG text
  remains impossible.
- Tests: `TestSubmissionPolicyAndInstanceTaskRealPostgres`.

### I3 — boot recovery not wired / bypasses gates — FIXED

- Backend calls `RecoverEnabled` after manager/assets setup.
- Recovery starts dependencies first, re-runs dependency and trusted-js gates, rotates tokens, and
  marks individual failures without failing core.
- Tests: `TestRecoveryDependencyOrderAndCycle`, `TestPluginRecoveryTrustedJSGatePG`,
  `TestPluginTokenRotationCloseAndRecoveryPG`.

### I4 — text policy not enforced / empty TEXT_OR_FILES — FIXED

- Persisted `required/minLength/maxLength` are enforced in Unicode runes; storage is additionally
  capped at 4,000,000 bytes.
- `TEXT_OR_FILES` requires at least one of text or a verified draft.
- Tests: `TestSubmissionPolicyAndInstanceTaskRealPostgres` (short, long, valid, empty OR, Unicode
  policy path, no silent REJECTED rows).

### I5 — PENTEST draft/file reuse — FIXED

- The draft row is locked and consumed in the submission transaction; a second key is rejected.
- Files must be READY, unattached on both canonical and Pentest links, and match
  board/module/uploader; a file cannot move to a second report.
- Same-key replay returns the original before draft verification.
- Tests: `TestPentestDraftConsumptionAndFileImmutabilityRealPostgres`,
  `TestPentestAndAllowlistRoutesHTTP`.

### I6 — removed participant retained instance read — FIXED

- Instance reads now require the live participant row, current USER/TEAM subject and module
  audience; removal/revocation denies endpoint reads immediately. Staff admin fallback remains.
- Test: `TestRemovedParticipantLosesInstanceReadRealPostgres`.

### I7 — TEAM own score returned 0/place 1 — FIXED

- `/scoreboard/me` resolves the current subject and matches `Subject.Type + Subject.ID`, equal to
  the canonical scoreboard place; fallback keeps the real team subject/name.
- Test: `TestTeamMyScoreboardMatchesCanonicalPlaceRealPostgres`.

### I8 — exec PID-reuse/broad-kill hazard — FIXED

- The runner records `/proc/<pid>/stat` start time at start and refuses group signals when the
  leader is reaped or the start time no longer matches. No PID-only kill fallback.
- Test: `TestTerminateProcessGroupRejectsStaleIdentity`; full `-race` plugin suite passes.

### I9 — trusted-js browser mounting absent — FIXED

- Default OFF operator gate + explicit per-install/upgrade approval + migration `014`.
- Visible metadata exposes only an enabled, approved, granted trusted bundle with a `sha256:<hex>`
  integrity value.
- `/api/v2/trusted-plugins/{id}/bundle` serves the exact self-contained ESM with no JWT/service
  token. `TrustedPluginHost` fetches it with the in-memory access token, verifies SHA-256 before
  import, mounts in a separate full-trust host, and passes only pluginId/theme/context props.
- Ordinary plugins remain on the opaque separate-origin iframe.
- Browser test: `trusted-js default-deny approval and verified full-trust ESM mount` (default-deny
  checkbox, explicit opt-in, grant-gated, integrity verification, rendered bundle).

## Medium / Minor

| # | Finding | Disposition |
|---|---|---|
| 1 | PENTEST reset limit race | **FIXED** — advisory lock + row lock + conditional increment; `TestPentestConcurrentResetLimitRealPostgres` proves exactly 2 winners |
| 2 | `parametersPolicy` stored but unenforced | **FIXED fail-closed** — non-empty policy rejected until a schema exists; binding test updated |
| 3 | re-bind wipes sealed secrets | **FIXED** — `COALESCE` preserves existing ciphertext/key version; retention test added |
| 4 | task binding can target foreign task UUID | **FIXED** — scoped UPDATE with `RowsAffected` check; unknown task test added |
| 5 | prerelease upgrade rejected | **FIXED** — semver parser/comparator; `TestCompareVersionsHandlesPrerelease` |
| 6 | `trusted_js_approved` lost on upgrade rollback | **FIXED** — rollback restores the previous flag |
| 7 | E2E cleanup narrower than claimed | **FIXED** — before/after exact Docker container+volume diff, label-scoped runner reconciliation, automated zero-resource assertion in the successful run |
| 8 | status documentation inconsistent/overstated | **FIXED** — implementation plan, all-stages ledger, schema/runbook/plugin/frontend docs reconciled; trusted-js no longer called missing |
| 9 | PUBLIC schema claim stronger than code | **DEFERRED** — PostgreSQL `PUBLIC` grants are inherited independently of the plugin role; `REVOKE ... FROM <role>` cannot remove them without a cluster-wide `REVOKE ... FROM PUBLIC`. No core table grants privileges to `PUBLIC` today and the restricted role cannot create/read core tables. Documented as deployment hardening, not silently treated as fixed |
| 10 | root binaries not ignored | **FIXED** — `/backend` and `/events-dispatcher` added to `.gitignore` |

## Tests and evidence

```text
go build ./... && go vet ./...                                    PASS
go test -p 1 ./... (real PostgreSQL)                              PASS  /tmp/opencode/last-go.log
go test -race -p 1 ./internal/plugins ./internal/instances ./internal/boards
                                                                     PASS
go test ./internal/platform/postgres -run 'TestMigrationsCleanSchema|TestMigrationsRemainingStages'
                                                                     PASS (001–016)
cd frontend && bun run typecheck && bun run test -- --maxWorkers=1 && bun run build
                                                                     PASS (13 files / 68 tests)
bash e2e/test-service-lifecycle.sh                                 PASS
E2E_KEEP=1 E2E_WORKDIR=/tmp/opencode/actfb-review-final3 ./e2e/run-full-e2e.sh
                                                                     PASS 7/7, 0 new containers/volumes
```

Artifacts:

- `/tmp/opencode/actfb-review-final3/playwright-report/`
- `/tmp/opencode/actfb-review-final3/backend.log`, `summary.txt`
- `/tmp/opencode/last-go.log`
- `/tmp/opencode/review-fe-build-final.log`

## Residual limits

- Real external OCI registry/images, live Proxmox/KubeVirt/VM, GitHub/GitLab credentials and remote
  CI remain unverified external blockers.
- PID reuse is prevented by the `/proc` start-time identity check; a real kernel PID-reuse event is
  not deterministically reproducible, so the regression uses a mismatched recorded start time plus
  a live unrelated group.
- Medium #9 remains deployment hardening (PUBLIC schema USAGE inheritance), documented above.
- The A+D browser test seeds `ad_awards` through the isolated PostgreSQL fixture to test the
  canonical scoreboard; real checker acceptance is separately covered by
  `TestGiteaRealAcceptance` and `TestDockerCheckerRealBenign`.