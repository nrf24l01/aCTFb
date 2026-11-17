# Remaining stages — schema handoff

Date: 2026-11-14 (017 added 2026-11-17). This file is the quick worker handoff for additive migrations `007`–`017`. The source of truth is
the migration SQL; this document mirrors it. `006` is applied and never edited. All migrations are Goose
`Up`/`Down`, additive, and preserve P0/P1 rows.

## Migration order

| Migration | Purpose | Depends on |
|---|---|---|
| `007_p1_teams_access.sql` | teams, access groups, audiences, scoped grants, TEAM score subjects | `006` |
| `008_p1_files_manual.sql` | drafts/files, custom MANUAL policy, transactional events outbox | `007` |
| `009_deployment_runtime.sql` | integrations, templates/bindings, instances, Pentest | `008` |
| `010_plugins.sql` | plugin registry, permissions, deliveries, settings, delegations | `009` |
| `011_attack_defense.sql` | A+D configs, vulnerabilities, assignments, runs, webhook dedup | `009` |
| `012_runtime_security.sql` | exact template allowlists, network leases, instance snapshots/FKs, immutable versions, AD bindings/results/awards, PENTEST files | `011` |
| `013_manual_text.sql` | durable MANUAL submission text + immutable task_type snapshot/guard | `012` |
| `014_plugin_trusted_js.sql` | explicit per-install trusted-js risk approval flag | `010` |
| `015_plugin_private_identities.sql` | reserved collision-resistant plugin schema/role/volume identity | `010` |
| `016_instance_text.sql` | INSTANCE submissions share durable MANUAL text guard | `013` |
| `017_admin_cli_audit.sql` | nullable `audit_events.actor_user_id` for external-operator CLI audit | `006` |

Apply in order with Goose. The current additive set ends at `17`; isolated test schemas also apply `17`.
Never edit an applied migration.

## 017 — backend admin CLI audit

- `audit_events.actor_user_id` drops `NOT NULL`; authenticated writers still store real user IDs.
- Operator CLI rows use `NULL` plus JSONB details `source=cli`, `operator=admin-cli`. No fake/system user row is
  created and no console/plaintext audit fallback is used.
- Down refuses if any NULL-actor CLI audit row exists instead of deleting operator audit history.
- Existing board/score audit queries inner-join users and therefore do not surface NULL-actor CLI rows through
  board-scoped views. The authenticated global audit API also keeps its user-joined contract and does not
  fabricate an actor for CLI rows; operator CLI audit remains stored for direct operator inspection.

## 007 — identity, teams, access, audiences, subjects

Tables:

- `access_groups(id uuid pk, name text unique 1..64, created_at)`.
- `access_group_members(group_id fk, user_id fk users, pk(group_id,user_id))`.
- `board_access_groups(board_id fk, group_id fk, pk(board_id,group_id))`.
- `audiences(id, board_id fk boards, name 1..128, description ≤20000, created_at, unique(board_id,name),
  unique(board_id,id))`.
- `audience_members(audience_id fk, subject_type USER|TEAM, subject_id, assigned_at,
  pk(audience_id,subject_type,subject_id))`; subject is polymorphic, service-validated.
- `module_audiences(board_id, module_id, audience_id, pk(module_id,audience_id),
  fk(board_id,module_id)->modules(board_id,id), fk(board_id,audience_id)->audiences(board_id,id))`.
- `teams(id, board_id fk, name 3..64, captain_user_id, invite_key_hash, invite_key_ciphertext bytea,
  invite_key_expires_at, created_at, updated_at, unique(board_id,name), unique(board_id,id))`;
  `teams_captain_participant_fk` is `(board_id,captain_user_id)->board_participants(board_id,user_id)`
  `DEFERRABLE INITIALLY DEFERRED`.
- `team_members(board_id, team_id, user_id, joined_at, pk(board_id,user_id),
  fk(board_id,team_id)->teams(board_id,id), fk(board_id,user_id)->board_participants(board_id,user_id))`.
- `permission_grants(id, user_id fk, permission 1..128, board_id fk, module_id, granted_by fk, created_at,
  expires_at, revoked_at, fk(board_id,module_id)->modules(board_id,id))`; checks: module requires board;
  `expires_at > created_at`; unique active `(user_id,permission,board_id,module_id) NULLS NOT DISTINCT WHERE
  revoked_at IS NULL`. Expiry is rechecked with `clock_timestamp()` after waits, not DB `now()`.

`boards` additions: `team_min_members`, `team_max_members`, `team_allow_self_create`,
`team_allow_leave_after_start`, `invite_code_hash`, `invite_code_ciphertext bytea`, `invite_code_expires_at`.
Existing TEAM rows backfill `min=1,max=100`; INDIVIDUAL requires null team limits and false flags. Invite hashes
are lookup-only; ciphertext is AES-256-GCM under `ACTFB_CREDENTIAL_ENCRYPTION_KEY`, never plaintext.

`submissions` additions: `subject_type USER|TEAM default USER`, `subject_id`, `submitted_by_user_id`. Backfill
`subject_id=user_id`, `submitted_by_user_id=user_id`; assert no duplicate accepted `(subject_type,subject_id,task_id)`
then set not null, add `submitted_by_user_id` FK, drop the old accepted index, create:
`submissions_one_accepted_per_subject_task(subject_type,subject_id,task_id) WHERE status='ACCEPTED'`,
`submissions_one_accepted_per_user_task(board_id,submitted_by_user_id,task_id) WHERE status='ACCEPTED'`,
`submissions_subject_history_idx(board_id,subject_type,subject_id,created_at DESC)`.

`module_audiences` backfills only audiences that exist and match the module board. TEAM subject FKs are not
polymorphic; the service validates subject membership inside the submission transaction.

## 008 — files, MANUAL, events

`tasks` additions: `submission_text_policy jsonb`, `submission_files_policy jsonb` (custom MANUAL limits; null
means defaults).

- `submission_drafts(id, board_id, module_id, resource_type TASK|PENTEST, task_id nullable,
  created_by_user_id fk, status OPEN|CONSUMED|EXPIRED, created_at, expires_at not null, consumed_at,
  fk(board_id,module_id)->modules, fk(board_id,module_id,task_id)->tasks,
  check(TASK requires task_id, PENTEST forbids task_id))`.
- `file_objects(id, board_id, module_id, task_id nullable, draft_id nullable fk drafts, uploader_user_id fk,
  purpose SUBMISSION_DRAFT|TASK_ATTACHMENT|PENTEST_ATTACHMENT, filename ≤255, content_type ≤255, size_bytes >0,
  sha256 64 hex, quarantine_bucket/key, clean_bucket/key nullable, status
  AWAITING_UPLOAD|VALIDATING|READY|REJECTED|ATTACHED|EXPIRED, rejection_code, created_at, validated_at,
  attached_submission_id fk submissions, unique(quarantine_key), unique(clean_key),
  fk(board_id,module_id)->modules, fk(board_id,module_id,task_id)->tasks)`.
  Checks keep admin/task attachments `draft_id NULL` with a task, and submission drafts `draft_id NOT NULL`.
  A draft file cannot reference another board/module/task through the composite FKs.
- `submission_files(submission_id fk, file_id fk, pk)`.
- `task_attachments(task_id fk, file_id fk, pk)`.
- `events_outbox(id, type, occurred_at, board_id, module_id, subject_type, subject_id, payload jsonb,
  created_at, published_at, attempts, last_error)`; partial index on unpublished rows. Core mutations enqueue in
  the same transaction; no plaintext flags/hashes in `payload`.
- `submissions` additions: `reviewed_by_user_id fk users ON DELETE SET NULL`, `review_decision ACCEPT|REJECT`.

`READY` is written only by the authenticated scanner worker after hash/size/magic/ZIP-safety and ClamAV when
required. Scanner unavailability fails closed; no fake-clean. `ScanCallback` is internal/authenticated, never a
public route.

## 009 — deployment, instances, Pentest

- `integrations(id, category GIT|COMPUTE, type legacy/extensible enum, kind VM|CONTAINER nullable,
  driver proxmox|kubevirt|docker|podman|kubernetes nullable, name unique, base_url,
  credentials_ciphertext bytea, credentials_key_version, settings jsonb, publication_policy jsonb, status,
  created_by, timestamps)`; checks enforce Git (no kind/driver) vs VM/Proxmox/KubeVirt vs CONTAINER/Docker/
  Podman/Kubernetes. Legacy `PROXMOX`/`CONTAINER` values remain; no renames.
- `deployment_templates`, `deployment_template_drafts(template_id pk, manifest_yaml, files jsonb, updated_by)`,
  `deployment_template_versions(id, template_id, version, digest, manifest jsonb, files jsonb, published_by,
  published_at, unique(template_id,version), unique(template_id,id))`.
- `deployment_bindings(id, board_id, module_id, task_id nullable, template_version_id fk, parameters jsonb,
  secret_parameters_ciphertext bytea, secret_key_version, lifecycle jsonb, created_by, timestamps,
  fk(board_id,module_id)->modules, fk(board_id,module_id,task_id)->tasks, unique scope NULLS NOT DISTINCT)`.
- `deployment_binding_targets(binding_id, kind, integration_id, pk)`.
- `tasks.deployment_binding_id`, `modules.deployment_binding_id` (nullable FKs; inline legacy nodes remain
  readable; template ref and inline nodes are mutually exclusive by service).
- `instances(id, board_id, module_id, scope TASK|PENTEST, scope_uuid, subject_type, subject_id, status,
  created_at, started_at, expires_at, extension_count, generation, idempotency_key unique, provider_labels jsonb)`.
- `instance_nodes(id, instance_id, key, name, kind, status, provider_resource_id, provider_labels, generation,
  unique(instance_id,key), unique(instance_id,id))`.
- `instance_operations(id, instance_id, node_key, operation, generation, idempotency_key, status, error,
  unique(instance_id,node_key,generation,operation))`.
- `instance_endpoints(id, instance_id, node_key, key, type, host, port, url, exposure, allocated_port,
  released_at, unique(instance_id,node_key,key))`; partial unique `(host,port) WHERE released_at IS NULL` so a port
  is not reused until destroy is confirmed.
- `instance_secret_values(instance_id, node_key, key, ciphertext bytea, key_version, created_at, pk)`.
- `pentest_configs(module_id pk, deployment_binding_id, reset_enabled, max_resets 0..100, submission_policy jsonb,
  created_by, updated_at)`.
- `pentest_environments(id, module_id, subject_type, subject_id, instance_id, status, resets_used,
  created_at, expires_at, unique(module_id,subject_type,subject_id))`.
- `pentest_submissions(id, board_id, module_id, subject_type, subject_id, status, text, score_awarded, message,
  idempotency_key, request_hash, created_at, reviewed_at, reviewed_by_user_id, review_decision,
  fk(board_id,module_id)->modules, unique(module_id,subject_type,subject_id,idempotency_key))`;
  `pentest_submissions_one_accepted(module_id,subject_type,subject_id) WHERE status='ACCEPTED'`.
- `pentest_submission_files(submission_id, file_id, pk)`.

Pentest expiry is the effective module/board end, no independent TTL. PENTEST reports map to the canonical
`AdminSubmission.resourceType=PENTEST` with optional `taskUuid`; the service unions `pentest_submissions` into the
admin read shape. Secrets (integration credentials, binding secret parameters, generated instance values) are
AES-256-GCM ciphertext, never in the normalized template snapshot.

## 010 — plugins

- `plugins(id namespaced text pk, name, version, api_version, status discovered|installed|enabled|disabled|
  incompatible|unhealthy|failed, manifest jsonb, manifest_digest, package_path, installed_by, installed_at,
  updated_at, enabled_at, disabled_at)`.
- `plugin_versions(plugin_id, version, manifest_digest, package_path, installed_at, pk)`.
- `plugin_permissions(plugin_id, permission `plugin.*`, scopes text[], description, registered_at, pk)`.
- `plugin_event_deliveries(plugin_id, event_id, status pending|delivered|failed|dead, attempts, last_error,
  next_attempt_at, delivered_at, pk(plugin_id,event_id))`.
- `plugin_settings(plugin_id, key, value_ciphertext bytea, key_version, updated_at, pk)`.
- `plugin_migrations(plugin_id, version, applied_at, pk)`.
- 010 also enables extensible enums: `modules.extension_key`, `tasks.extension_key`, `tasks.checker_key`; DB checks
  allow `EXTENSION`/`PLUGIN` only with the matching key; native values are unchanged. Down deletes only
  extension/PLUGIN rows that cannot satisfy native checks, restores native-only checks, and drops the columns.
- `plugin_delegations(id, plugin_id, user_id, board_id, module_id, nonce unique, token_hash bytea unique,
  issued_at, expires_at, revoked_at)`; core-issued HMAC delegation, token not stored. Plugin-supplied user/scope is
  never trusted.

Install leaves `status=installed`; `enable=true` is rejected; only the explicit enable endpoint changes state.
Plugin private schemas are manager-provisioned, not core Goose.

## 015 — private plugin identity reservation

- `plugin_private_identities(plugin_id PK, schema_name UNIQUE, role_name UNIQUE, volume_name UNIQUE, created_at)`.
- New plugin names always include a stable 64-bit SHA-256 suffix of the full ID; distinct allowed IDs
  such as `example.score_events`, `example_score_events` and `example-score-events` cannot collide.
- Existing installations adopt their legacy schema/role/volume on first use. A unique conflict fails
  closed with an operator-repair error; core never silently renames or drops private data.
- Down drops only the reservation table; plugin schemas/roles/volumes are untouched.

## 016 — INSTANCE durable text

- Widens the 013 text check/trigger to `task_type IN ('MANUAL','INSTANCE')`; FLAG remains text-free.
- Down refuses to silently discard existing INSTANCE text.

## 014 — trusted-js explicit approval

- `plugins.trusted_js_approved BOOLEAN NOT NULL DEFAULT FALSE`.
- Set only by `Install`/`UpgradeWithApproval` after both the operator-wide `ACTFB_PLUGIN_ALLOW_TRUSTED_JS=true`
  gate and explicit per-install/upgrade approval. `Enable` re-checks the persisted flag and the live operator gate.
- Browser mounting is intentionally not implemented; the opaque iframe remains the only rendered mode.
- Down drops only the column; plugin rows and private schemas survive.

## 013 — durable MANUAL submission text

- `submissions.submitted_text TEXT NULL` (explicit NULL backfill for every existing row).
- `submissions.task_type TEXT NOT NULL` snapshot, backfilled from the authoritative `tasks.type`, check native enum.
- `submissions_manual_text_check`: `submitted_text IS NULL OR (task_type='MANUAL' AND octet_length(submitted_text)
  <= 4000000)`.
- `submissions_manual_text_guard` BEFORE INSERT OR UPDATE OF `submitted_text`, `task_type`:
  - INSERT derives `task_type` from the exact `(board_id,module_id,task_id)` task; a missing task raises
    `23503` so the existing FK behavior is preserved. Client-supplied `task_type` is overwritten.
  - UPDATE rejects changing `task_type` (snapshot immutable).
  - Non-NULL `submitted_text` is rejected unless the derived/snapshot type is `MANUAL`; byte size >4,000,000 is
    rejected. FLAG text is never accepted.
- A later task type change leaves the old submission snapshot untouched; no confidentiality regression and no
  destructive task-type CHECK hack. Down drops trigger/function, constraints, and both columns; P0/P1 rows survive,
  but new MANUAL text is not restored by a schema down.
- Go handoff: `boards.Submission`/`SubmissionParams` add `SubmittedText *string` and the persisted `TaskType`
  snapshot; `Submission.text` is the participant projection. Boards inserts `submitted_text` only when the column
  exists and never sends it to FLAG; the trigger is the final authority.

## 012 — runtime security invariants

- `deployment_template_allowlist(id, template_version_id fk versions, board_id fk boards nullable,
  module_id nullable, parameters_policy jsonb, approved_by fk users, created_at, expires_at, revoked_at)`:
  module requires board; composite FK `(board_id,module_id)->modules(board_id,id)`; active unique
  `(template_version_id,board_id,module_id) NULLS NOT DISTINCT WHERE revoked_at IS NULL`; expiry after created.
- `deployment_template_allowlist_targets(allowlist_id fk, kind VM|CONTAINER, integration_id fk integrations,
  pk(allowlist_id,kind,integration_id))`. Only global admin mutates allowlists; owner/editor with
  `deployment_templates.use` may bind only an allowlisted immutable version to these exact targets. No client
  image/driverOptions/credentials/raw target.
- `instance_network_leases(id, instance_id fk instances, integration_id fk integrations, kind,
  allocation_key, claim jsonb, leased_at, released_at)`; unique live `(integration_id,allocation_key) WHERE
  released_at IS NULL`; claim comes from the integration trusted network pool, never client manifest.
- `instances` additions: `binding_id fk deployment_bindings`, `template_version_id fk versions`,
  `desired_snapshot jsonb NOT NULL DEFAULT '{}'`, `request_actor_id fk users`, `removed_at`. Board/module FK
  changed to `ON DELETE RESTRICT`; board deletion returns a safe 409 until provider cleanup. Live owner/scope unique
  `(board_id,module_id,scope,scope_uuid,subject_type,subject_id) WHERE removed_at IS NULL`.
- `instance_operations` additions: `requested_by fk users`, `authorized_context jsonb`, `desired_snapshot jsonb`.
  Desired snapshots contain secret references only; actual generated values stay in `instance_secret_values`.
- `deployment_template_versions` gets a `BEFORE UPDATE OR DELETE` trigger that raises; published versions are
  immutable at the DB layer. Down drops only the trigger/function.
- `repository_bindings(id, integration_id fk, repository_ref, branch, webhook_secret_ciphertext,
  webhook_secret_key_version, created_by, created_at, unique(integration_id,repository_ref,branch))`.
  `ad_repository_assignments` adds `source_binding_id`/`exploit_binding_id` FKs.
  `git_webhook_deliveries.repository_binding_id` is re-pointed to `repository_bindings` only if the table is empty;
  otherwise 012 aborts with an explicit message (no old row is deleted or guessed).
- `ad_run_results(run_id, vulnerability_id, status, safe_verdict jsonb, score_awarded, pk(run_id,vulnerability_id))`.
- `ad_awards(module_id, subject_type, subject_id, vulnerability_id, kind EXPLOIT|PATCH, run_id, points,
  awarded_at, unique(subject_type,subject_id,vulnerability_id,kind))` gives idempotent points across commits.
- `file_objects.attached_pentest_submission_id` nullable FK `pentest_submissions`, with a check that core
  `attached_submission_id` and PENTEST attachment are never both set. PENTEST reports remain in
  `pentest_submissions`; no generalized core submission table is added.

## 011 — Attack+Defense

- `ad_configs(module_id pk, default_source_branch, default_exploit_branch, runner_image, run_timeout_seconds 1..3600,
  max_concurrent_runs_per_subject 1..10, manual_rerun_enabled, created_by, updated_at)`.
- `ad_vulnerabilities(id, module_id, key, title, description, exploit_points, patch_points,
  exploit_test_selector, patch_test_selector, timestamps, unique(module_id,key))`.
- `ad_repository_assignments(id, module_id, subject_type, subject_id, source_integration_id,
  source_repository, source_branch, exploit_integration_id, exploit_repository, exploit_branch, created_by,
  created_at, unique(module_id,subject_type,subject_id))` plus unique source/exploit repo+branch indexes.
- `ad_runs(id, module_id, repository_assignment_id, subject_type, subject_id, kind EXPLOIT|PATCH, commit_sha,
  status QUEUED|RUNNING|PASSED|FAILED|ERROR|CANCELED, provider_delivery_id, requested_by, timestamps, error)`;
  unique logical run `(module_id,subject_type,subject_id,kind,lower(commit_sha))`.
- `ad_run_logs(run_id, seq, line ≤4000, created_at, pk)`.
- `git_webhook_deliveries(id, repository_binding_id, provider_delivery_id, commit_sha, status, received_at,
  processed_at, error, unique(repository_binding_id,provider_delivery_id))`.

Git credentials are encrypted in `integrations`; webhook HMAC is verified on raw bytes, provider delivery ID is
deduped, and the fixed normalized commit SHA is checked out once.

## Down/up behavior

Down order is reverse dependency: `011` → `010` → `009` → `008` → `007`. `007` down drops subject columns and
restores the old `(user_id,task_id)` accepted index; all seeded P0/P1 rows survive. `008` down drops file/event
tables and policy columns; `009` drops runtime/template tables and binding columns; `010`/`011` drop only their
objects. Re-up is idempotent at version `11`.

## Generated model / Go handoff

Swagger now has **401 definitions** (P1 had 358): new AdminBoard/BoardInviteResponse, PermissionGrant,
DeploymentTemplate*/Allowlist, Plugin*, ValidationFinding. Generated with `swagger v0.36.7`; all 401 model files are
present and generation is checksum-reproducible. Additive fields/enums:

- `ModuleType`/`TaskType` add `EXTENSION`; `Module`/`AdminModule`/`Task`/`AdminTask`/list/create/patch add nullable
  `extensionKey`; `SubmissionPolicy` adds nullable `checkerKey`; `SubmissionChecker` adds `PLUGIN`.
- `PentestConfig.ProxmoxIntegrationUUID` remains `*UUID` but is no longer required; added
  `ProviderIntegrationUUID`, `DeploymentTemplateUUID`, `TemplateVersion` pointers and made `nodes` optional.
- `PendingFile`, `ScoreAdjustment`, `ADRepositoryAssignment`, `ADRun` are now flat closed models (no `allOf`);
  generated structs no longer embed `FileRef`/`ScoreAdjustmentRequest`/`ADRepositoryAssignmentRequest`/
  `ADRunSummary`; fields and JSON stay the same. Update any code that used those embedded structs.
- `AdminBoard` is a flat closed admin read model; `AdminBoardsResponse.items` stays `Board` to keep the current
  handler compiling, while admin detail/create/patch schemas expose `AdminBoard`.
- New endpoints: board invite code, permission grants, deployment templates/versions, plugins/events; all under
  `/api/v2`, no v3.

Do not hand-edit generated files. Regenerate with:

```bash
swagger generate model -f docs/swagger.yaml -t ./internal/generated/openapi
```

## Verification

- `swagger validate docs/swagger.yaml` — valid (unused-response warnings only).
- Model generation — 399 files, reproducible checksum.
- `go build ./...` / `go test ./...` — PASS on the current tree.
- Real PostgreSQL `TestMigrationsRemainingStages` — PASS for `007`–`016` backfill, constraints, down/up, idempotent
  re-up, and preservation of seeded P0/P1 rows; run with `ACTFB_TEST_POSTGRES_DSN=...`.
- `internal/contracttests` — P1 six responses plus new AdminBoard/BoardInviteResponse/PermissionGrant/
  DeploymentTemplateDraft/Plugin/PendingFile/ScoreAdjustment/ADRun schema validation PASS.

Blocked acceptance: no real Proxmox/KubeVirt/Kubernetes/GitLab/GitHub/Gitea endpoint/credentials. Contract tests and
fake-driver tests are allowed; fake success adapters are not.