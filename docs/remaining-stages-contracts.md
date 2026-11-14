# Remaining stages — bounded master contract

Date: 2026-11-13. Status: **frozen contract for the remaining P1 surface, P2–P5, migrations `007`–`013`, and the
shared Go interfaces; the seven additive migrations and Swagger/generated models are now implemented and tested
(column handoff: `docs/remaining-schema.md`).**
This is not product implementation evidence for the worker packages. The accepted baseline is the P1 first slice
(`docs/p1-core-contracts.md`, `docs/p1-security-review.md`): 39 operations (33 board + 6 RBAC), PRIVATE/INDIVIDUAL/
OPEN/CLOSED/TASKS/FLAG/TEXT/AUTOMATIC, 16 accepted answers. `docs/swagger.yaml` currently has **143 paths / 194 operations / 401 definitions**; the P1 first slice remains 39
implemented operations, and the operations listed below as remaining at freeze are implemented in the current
tree (`docs/implementation-plan.md`).

Authority: `aCTFb_CODEX_TASK_v4.md`, `docs/deployment-templates.md`, `docs/runtime-instances.md`,
`docs/plugin-system.md`, `docs/plugin-developer-guide.md`, the current repository and Swagger. Existing names (`Board`, `Module`, `Task`, `Submission`, `Instance`, `Audience`,
`AccessGroup`, `Team`, `Integration`) are preserved; no `/api/v3`, no parallel envelope, no hand-edited generated
model. Main approved the architecture choices below; they are not open questions.

## 1. Stage map and gates

| Stage | Deliverable | Migration | Gate (real evidence required) |
|---|---|---|---|
| P1R | TEAM/PUBLIC/INVITE_ONLY, access groups, audiences, MANUAL, task files | `007`, `008` | team board end-to-end scores by `TEAM` subject once; public join; invite-only; audience-gated module; manual review awards task points; file draft finalization atomic; file IDOR/zip-traversal negatives pass |
| P2 | DeploymentTemplate + Docker CONTAINER worker | `009` | one published CONTAINER manifest runs, endpoint/TTL shown, reset clean, destroy/TTL frees resources, no orphan after kill |
| P3 | generic VM + Proxmox driver + Pentest report | `009` remainder | one VM manifest runs on Proxmox; KubeVirt swap needs no common-manifest change; mixed VM+CONTAINER rejected; real endpoint required |
| P4 | Attack+Defense Git + isolated checker | `011` | one push = one logical run on redelivery; signed webhook; exploit/patch verdicts separate; sandbox limits |
| P5 | installable plugins (HTTP v1) + examples | `010` | install/enable `example.hello` and `example.score_events` after core build, no Go/Vite rebuild; crash isolation; event dedupe |

## 2. Frozen migration plan (one schema owner)

`006` is applied and is **never edited**. All work is additive. Only the schema/Swagger owner edits
`migrations/**`, `docs/swagger.yaml`, and `internal/generated/openapi/**`; each stage lands its migration before
dependent code. `014+` requires new main approval.

| File | Owner stage | Creates / alters | Required backfill and integrity |
|---|---|---|---|
| `007_p1_teams_access.sql` | P1R | `access_groups`, `access_group_members`, `board_access_groups`; `audiences`, `audience_members`, `module_audiences`; `teams`, `team_members`; `permission_grants`; `boards` team settings + invite hash/ciphertext; `submissions` subject columns | backfill subject `USER`; assert no duplicate accepted subject/task; backfill `module_audiences`; captain/team-member FKs to participants; encrypted invite ciphertext |
| `008_p1_files_manual.sql` | P1R | `submission_drafts`, `file_objects`, `submission_files`, `task_attachments`, `events_outbox`; `tasks.submission_text_policy`/`submission_files_policy`; `submissions.reviewed_by_user_id`/`review_decision` | no fabricated files; custom MANUAL policy persisted; core events transactional; file scope always board/module/task-linked |
| `009_deployment_runtime.sql` | P2/P3 | `integrations` (encrypted credentials), template/draft/version/binding tables, `instances`, `instance_nodes`, `instance_operations`, `instance_endpoints`, `instance_secret_values`, `pentest_configs`, `pentest_environments`, `pentest_submissions` | no plaintext secrets in normalized snapshot; provider IDs/labels/generation; unique live endpoint port; Pentest deadline = module/board |
| `010_plugins.sql` | P5 | `plugins`, `plugin_versions`, `plugin_permissions`, `plugin_event_deliveries`, `plugin_settings`, `plugin_migrations`, `plugin_delegations`; `modules/tasks.extension_key`, `tasks.checker_key` and CHECK updates | no auto-enable; no auto-granted permissions; HMAC delegation rows for core-issued end-user/scope |
| `011_attack_defense.sql` | P4 | `ad_configs`, `ad_vulnerabilities`, `ad_repository_assignments`, `ad_runs`, `ad_run_logs`, `git_webhook_deliveries` | unique `(repository_binding_uuid, provider_delivery_id)`; immutable normalized commit SHA; encrypted Git credentials live in `integrations` |
| `012_runtime_security.sql` | P2/P4 | `deployment_template_allowlist`, `deployment_template_allowlist_targets`, `instance_network_leases`, `repository_bindings`, `ad_run_results`, `ad_awards`; instance/operation/file columns; immutable-version trigger | exact approved targets; live lease uniqueness; RESTRICT board FK; no plaintext secrets; no double AD award |
| `013_manual_text.sql` | P1R | `submissions.submitted_text`, immutable `task_type` snapshot, insert/update guard trigger | existing rows backfill `task_type`; NULL text only for old rows; MANUAL text ≤4,000,000 bytes; FLAG text and client spoof refused; snapshot survives task type change |

Rules: no column renames/drops in `007`–`013`; obsolete columns (`modules.audience_uuids`, legacy
`proxmoxIntegrationUuid`, `templateVmid`) stay readable and are marked legacy. Polymorphic subject columns
(`subject_type`, `subject_id`) cannot carry a single FK; the service validates the subject inside the board/
submission transaction, while `submitted_by_user_id` keeps an FK to `users`. Any polymorphic/optional parent
(`task_id`, `module_id`, `scope_uuid`) is constrained by the service and documented in
`docs/remaining-schema.md`; explicit service checks are mandatory, not implied.

**P1 schema detail (handoff-level).** `007` exact columns/constraints:

```text
teams(id uuid pk, board_id uuid not null fk boards on delete cascade, name text not null,
      captain_user_id uuid not null, invite_key_hash text, invite_key_ciphertext bytea,
      invite_key_expires_at timestamptz, created_at timestamptz not null default now(),
      updated_at timestamptz not null default now(), unique(board_id,name), unique(board_id,id),
      constraint teams_captain_participant_fk
        foreign key (board_id, captain_user_id)
        references board_participants(board_id,user_id) deferrable initially deferred)
team_members(board_id uuid not null, team_id uuid not null, user_id uuid not null,
      joined_at timestamptz not null default now(), pk(board_id,user_id),
      fk(board_id,team_id) references teams(board_id,id) on delete cascade,
      fk(board_id,user_id) references board_participants(board_id,user_id) on delete cascade)
access_groups(id uuid pk, name text not null unique, created_at timestamptz not null default now())
access_group_members(group_id uuid fk access_groups on delete cascade, user_id uuid fk users,
      pk(group_id,user_id))
board_access_groups(board_id uuid fk boards on delete cascade, group_id uuid fk access_groups on delete cascade,
      pk(board_id,group_id))
audiences(id uuid pk, board_id uuid not null fk boards on delete cascade, name text not null,
      description text not null default '', unique(board_id,name), unique(board_id,id))
audience_members(audience_id uuid fk audiences on delete cascade, subject_type text check in ('USER','TEAM'),
      subject_id uuid not null, assigned_at timestamptz not null default now(),
      pk(audience_id,subject_type,subject_id))
module_audiences(board_id uuid not null, module_id uuid not null, audience_id uuid not null,
      pk(module_id,audience_id),
      fk(board_id,module_id) references modules(board_id,id) on delete cascade,
      fk(board_id,audience_id) references audiences(board_id,id) on delete cascade)
permission_grants(id uuid pk, user_id uuid not null fk users on delete cascade, permission text not null,
      board_id uuid fk boards on delete cascade, module_id uuid, granted_by uuid not null fk users,
      created_at timestamptz not null default now(), expires_at timestamptz, revoked_at timestamptz,
      check(board_id is not null or module_id is null),
      check(module_id is null or board_id is not null),
      fk(board_id,module_id) references modules(board_id,id) on delete cascade)
boards add team_min_members int, team_max_members int, team_allow_self_create bool not null default false,
      team_allow_leave_after_start bool not null default false, invite_code_hash text,
      invite_code_ciphertext bytea, invite_code_expires_at timestamptz,
      check(team_min_members is null or team_max_members is null or
            (team_min_members between 1 and 100 and team_max_members between team_min_members and 100))
submissions add subject_type text not null default 'USER' check in ('USER','TEAM'),
      subject_id uuid, submitted_by_user_id uuid
```

Invite semantics: `*_hash` is the only lookup value; `*_ciphertext` is AES-256-GCM ciphertext under
`ACTFB_CREDENTIAL_ENCRYPTION_KEY` and is returned only to authorized `GET` callers. Never store or log the plain
invite code; if the key is unavailable the GET fails closed.

`007` backfills `subject_id`/`submitted_by_user_id` from `user_id`, then sets both `NOT NULL` and adds the
`submitted_by_user_id` FK; creates
`submissions_one_accepted_per_subject_task` on `(subject_type,subject_id,task_id)` and
`submissions_one_accepted_per_user_task` on `(board_id,submitted_by_user_id,task_id)` where `status='ACCEPTED'`;
drops only the old `(user_id,task_id)` accepted index after the backfill. Before creating either unique index the
migration asserts `GROUP BY subject_type,subject_id,task_id HAVING count(*) > 1` is empty and aborts otherwise;
006 already enforced one accepted per user/task and TEAM was rejected, so no history is rewritten and no duplicate
is silently dropped. It backfills `module_audiences` with
`INSERT INTO module_audiences(board_id,module_id,audience_id) SELECT m.board_id,m.id,a.id FROM modules m CROSS JOIN
LATERAL unnest(m.audience_uuids) u JOIN audiences a ON a.id = u AND a.board_id = m.board_id ON CONFLICT DO NOTHING`.
`permission_grants` expiry is evaluated by the service with `clock_timestamp()` after every lock/wait; the DB
check is only `expires_at > created_at`.

`008` exact columns/constraints:

```text
tasks add submission_text_policy jsonb, submission_files_policy jsonb
submission_drafts(id uuid pk, board_id uuid not null fk boards on delete cascade, module_id uuid not null,
      resource_type text not null check in ('TASK','PENTEST'), task_id uuid,
      created_by_user_id uuid not null fk users, status text not null default 'OPEN'
      check in ('OPEN','CONSUMED','EXPIRED'), created_at timestamptz not null default now(),
      expires_at timestamptz not null, consumed_at timestamptz,
      fk(board_id,module_id) references modules(board_id,id) on delete cascade,
      check((resource_type='TASK' and task_id is not null) or
            (resource_type='PENTEST' and task_id is null)))
file_objects(id uuid pk, board_id uuid not null, module_id uuid not null, task_id uuid,
      draft_id uuid fk submission_drafts on delete cascade, uploader_user_id uuid not null fk users,
      purpose text not null check in ('SUBMISSION_DRAFT','TASK_ATTACHMENT','PENTEST_ATTACHMENT'),
      filename text not null, content_type text not null, size_bytes bigint not null, sha256 text not null,
      quarantine_bucket text not null, quarantine_key text not null, clean_bucket text, clean_key text,
      status text not null check in ('AWAITING_UPLOAD','VALIDATING','READY','REJECTED','ATTACHED','EXPIRED'),
      rejection_code text, created_at timestamptz not null default now(), validated_at timestamptz,
      attached_submission_id uuid fk submissions, unique(quarantine_key), unique(clean_key),
      fk(board_id,module_id) references modules(board_id,id) on delete cascade,
      fk(board_id,module_id,task_id) references tasks(board_id,module_id,id) on delete cascade,
      check((purpose='SUBMISSION_DRAFT' and draft_id is not null) or
            (purpose<>'SUBMISSION_DRAFT')))
submission_files(submission_id uuid fk submissions on delete cascade, file_id uuid fk file_objects,
      pk(submission_id,file_id))
task_attachments(task_id uuid fk tasks on delete cascade, file_id uuid fk file_objects,
      pk(task_id,file_id))
events_outbox(id uuid pk, type text not null, occurred_at timestamptz not null,
      board_id uuid, module_id uuid, subject_type text, subject_id uuid, payload jsonb not null,
      created_at timestamptz not null default now(), published_at timestamptz,
      attempts int not null default 0, last_error text)
submissions add reviewed_by_user_id uuid fk users,
      review_decision text check (review_decision is null or review_decision in ('ACCEPT','REJECT'))
```

`008` adds indexes on `file_objects(draft_id,status)`, `submission_drafts(created_by_user_id,status,expires_at)`,
`file_objects(sha256)`, and partial `events_outbox(published_at)` where `published_at IS NULL`. Admin/task
attachments use `draft_id NULL` with `purpose`/scope FKs; submission drafts require a draft id. No existing
submission is mutated. Attachment rows are written only by the boards finalization transaction; the files service
writes quarantine/clean status. `READY` is written only by the authenticated scanner worker after real
hash/size/magic/ZIP-safety checks and ClamAV when policy requires; if the scanner is unavailable the core fails
closed (`SCANNER_UNAVAILABLE`), never fake-clean. `ScanCallback` is an internal authenticated worker call, never a
public route.

## 3. Exact existing API surface to finish P1

Counts are exact from `docs/swagger.yaml`: teams **12 paths / 16 ops**, audiences **5 / 9**, access groups
**2 / 5**, drafts/files/attachments **21 / 23**. Existing models are reused; no renames.

### 3.1 Teams and TEAM scoring

Existing routes (participant): `GET|PATCH /boards/{boardUuid}/team`, `POST /boards/{boardUuid}/teams`,
`POST /boards/{boardUuid}/teams/join`, `GET /boards/{boardUuid}/team/invite-key`,
`POST /boards/{boardUuid}/team/invite-key/rotate`, `POST /boards/{boardUuid}/team/leave`,
`DELETE /boards/{boardUuid}/team/members/{userUuid}`, `POST /boards/{boardUuid}/team/captain`.
Admin: `GET|POST /admin/boards/{boardUuid}/teams`, `GET|PATCH|DELETE /admin/boards/{boardUuid}/teams/{teamUuid}`,
`POST|DELETE /admin/boards/{boardUuid}/teams/{teamUuid}/members[/{userUuid}]`.

Models: `Team` (`uuid`, `boardUuid`, `name`, `captainUuid`, `membersCount`, `maxMembers`), `TeamCreateRequest`
(`name`), `TeamJoinRequest` (`inviteKey` 16..128, `^[A-Za-z0-9_-]+$`), `TeamPatchRequest` (`name`),
`TeamInviteResponse` (`inviteKey`, nullable `expiresAt`), `TransferCaptainRequest` (`userUuid`),
`TeamSettings` (`minMembers`, `maxMembers`, `allowSelfCreate`, `allowLeaveAfterStart`; `maxMembers >= minMembers`).

Semantics: TEAM boards require `teamSettings`; INDIVIDUAL forbids it. `allowSelfCreate=false` means only staff
create teams. Invite keys are hashed for lookup and AES-GCM ciphertext for authorized `GET` display; rotate
invalidates the old key, and codes are unusable after the board window / `BOARD_TEAM_CHANGES_LOCKED`. Captain
transfer is captain-only; removal of a captain requires transfer. A user is in at most one team per board. Team
membership changes after the board starts obey `allowLeaveAfterStart`. `teams.captain_user_id` and
`team_members.user_id` are constrained to `board_participants` by a deferrable composite FK (or the service
performs the same check inside the transaction); a non-member captain is impossible.

**Scoring subject:** `ScoreSubject{type:USER|TEAM, uuid, name}` already exists in Swagger and is used by
`ScoreboardItem.subject`, `AdminSubmission.subject`, `Instance.owner`, `AudienceMember.subject`, and
`ScoreAdjustmentRequest.subjectType/subjectUuid`. INDIVIDUAL resolves `USER(user_id)`; TEAM resolves
`TEAM(team_id)` from `team_members` inside the submission transaction. Accepted uniqueness is by
`(subject_type, subject_id, task_id)`; a second unique index `(board_id, submitted_by_user_id, task_id)` prevents
the same human from scoring the same task again after switching teams. Audit/event payloads carry the subject, not
a private user id.

`submission.read_own` resolves to the caller's subject: INDIVIDUAL returns only that user's submissions; TEAM returns
the caller team's submissions to its members. Drafts remain caller-bound even inside a team: a teammate cannot
read or finalize another member's draft.

`BoardMe.team` is populated for TEAM participants; `BoardMe.score`/`place` and scoreboard rows use the same
subject. Team size/captain errors use the existing `TEAM_FULL`, `USER_ALREADY_IN_TEAM`,
`TEAM_CAPTAIN_MUST_TRANSFER`, `TEAM_MEMBER_NOT_FOUND`, `TEAM_NOT_EMPTY`, `INVALID_TEAM_INVITE_KEY`,
`BOARD_TEAM_CHANGES_LOCKED` codes.

### 3.2 Board registration, visibility and access groups

`RegistrationMode = OPEN|INVITE_ONLY|CLOSED`; `BoardVisibility = PUBLIC|PRIVATE`; `ParticipationMode =
INDIVIDUAL|TEAM`. `AdminBoardCreateRequest`/`PatchRequest` already accept `accessGroupUuids` and nullable
`teamSettings`; `BoardJoinRequest.inviteCode` is 8..128 and required only for `INVITE_ONLY`, forbidden for `OPEN`.
Validation rules already in Swagger: `PUBLIC` cannot combine with non-empty `accessGroupUuids`; TEAM requires
teamSettings; INDIVIDUAL forbids it; PUBLISH needs at least one enabled valid module.

Access groups are admin-managed user sets. Membership is assigned through `AdminUserCreateRequest`/
`AdminUserPatchRequest.accessGroupUuids` (there is no separate member route); `AccessGroup{ uuid, name,
usersCount }` and `AccessGroupsResponse`. `PRIVATE` + non-empty `board_access_groups` means a join requires
membership in at least one listed group; an access group never grants `board.update`, `module.*`,
`submission.read`, or admin permissions. Public boards always have an empty access-group set.

Add an admin read model (wire-additive) `AdminBoard` = `Board` + `teamSettings` + `accessGroupUuids`, returned by
admin create/detail/patch (`GET/POST/PATCH /admin/boards[...]`); list items stay `Board` in the current response
for handler compatibility; participant `Board` stays free of access-group ids. Add
`PUT /admin/boards/{boardUuid}/access-groups` with `{ "accessGroupUuids": [...] }` so the set is readable and
writable without changing the existing create/patch contract.

INVITE_ONLY needs a board-level code (Swagger has only the join request). Add
`GET|POST /admin/boards/{boardUuid}/invite-code` returning `BoardInviteResponse{ inviteCode, expiresAt? }`;
GET returns the current code, POST rotates it. The lookup hash and AES-GCM ciphertext are stored separately, the
code is shown only to `admin`/`board_owner`, invalidated by rotation, and required by
`BoardJoinRequest.inviteCode`. `CLOSED` never accepts a self-join code; GET fails closed if the encryption key is
unavailable.

### 3.3 Audiences and module constraints

Existing routes: `GET /boards/{boardUuid}/audiences/me`; `GET|POST /admin/boards/{boardUuid}/audiences`;
`GET|PATCH|DELETE /admin/boards/{boardUuid}/audiences/{audienceUuid}`;
`GET|POST /admin/boards/{boardUuid}/audiences/{audienceUuid}/members`;
`DELETE /admin/boards/{boardUuid}/audiences/{audienceUuid}/members/{subjectType}/{subjectUuid}`.

Models: `Audience` (`uuid`, `boardUuid`, `name`, `description`, `membersCount`), `AudienceSummary`, `AudienceMember`
(`audienceUuid`, `subject`, `assignedAt`), the audience/member list envelopes. `AudienceMemberCreateRequest`
requires `subjectType` + `subjectUuid`; the subject must match board participation mode (`USER` on INDIVIDUAL,
`TEAM` on TEAM) and already participate.

Module constraint: `Module`/`AdminModule`/`ModuleListItem.audienceUuids`; empty means available to every joined
subject, otherwise membership in at least one listed audience is required. Audiences must belong to the same
board; module windows must be inside the board window; `audienceUuids` is validated on create/patch. The existing
`modules.audience_uuids` array is legacy storage; `module_audiences` is authoritative after `007`. Scoped staff
bypass audience gating only for management of their assigned module; participant visibility still requires
membership.

### 3.4 Submission drafts, presigned files and MANUAL

Existing routes (23 ops): task draft create/read/delete; draft file presign/complete; authorized task attachment
download; authorized own submission file download; the same four for Pentest; admin task attachment
presign/complete/download/delete; admin Pentest attachment presign/complete/download/delete; admin submission file
download.

Models: `SubmissionDraft` (`uuid`, `resourceType`, `boardUuid`, `moduleUuid`, `taskUuid?`, `status`, `createdAt`,
`expiresAt`, `files[]`), `PendingFile` (`FileRef` + `status`, nullable `rejectionCode`), `FileRef` (immutable
metadata only, never bucket/key), `ScopedFilePresignRequest` (`filename`, `contentType`, `size`, `sha256`),
`ScopedFilePresignResponse` (`fileUuid`, `method=PUT`, `uploadUrl`, `requiredHeaders`, `expiresAt`),
`FileValidationStatus = AWAITING_UPLOAD|VALIDATING|READY|REJECTED|ATTACHED|EXPIRED`,
`SubmissionDraftStatus = OPEN|CONSUMED|EXPIRED`, `SubmissionPolicy`/`SubmissionTextPolicy`/
`SubmissionFilesPolicy`, `FinalizeSubmissionRequest{text?, draftUuid?}`.

Lifecycle: create draft (caller-bound, module/task-bound, short TTL) → presign each file (quarantine key, declared
type/size/hash, policy from the task, never client-selected context) → direct upload → complete triggers scanning
→ READY only after the authenticated scanner worker validates hash, size, magic and safe-ZIP rules and runs
ClamAV when policy requires; a rejected or unscanned object is never downloadable. If the scanner is unavailable
the core fails closed; there is no fake-clean path. Finalization requires the caller's OPEN, unexpired draft, every
required file READY, atomically marks files ATTACHED and draft CONSUMED before verdict/score; finalization replay
returns the original submission and writes no duplicate. No generic upload endpoint and no raw file UUIDs in
create/patch. Frozen defaults: draft TTL 15 minutes (max 60), presign URL 5 minutes, upload completion must occur
before draft expiry (`410 SUBMISSION_DRAFT_EXPIRED`), and the per-file size cap is
`SubmissionFilesPolicy.maxSizeBytes` (schema max 1 GiB).

MANUAL durable text: `013` adds `submissions.submitted_text TEXT NULL` and an immutable `task_type` snapshot.
The BEFORE INSERT trigger derives `task_type` from the authoritative task row (never from the client), rejects
non-NULL text unless the derived type is `MANUAL`, and enforces `octet_length <= 4000000`. UPDATE rejects changing
`task_type`; a later task type change does not invalidate an existing snapshot. Existing rows backfill explicit
NULL text and their real task type. `Submission.text` remains the participant-visible projection; FLAG text is
never accepted.

MANUAL: `Task.type=MANUAL`, `submission.checker=MANUAL`, `submission.mode!=NONE`; submission enters
`PENDING_REVIEW` with no score; `POST /admin/boards/{boardUuid}/submissions/{submissionUuid}/review` accepts
`ACCEPT`/`REJECT`, `ACCEPT` awards `task.points` exactly once, `REJECT` awards zero; arbitrary score overrides use
the existing audited score-adjustment endpoint. Files are attached before review; reviewers see only their scope.

### 3.5 DeploymentTemplate and Plugin resources (new, `/api/v2` only)

DeploymentTemplate (global admin writes; scoped read/bind by published allowlist):

```text
GET    /admin/deployment-templates
POST   /admin/deployment-templates
GET    /admin/deployment-templates/{templateUuid}
GET    /admin/deployment-templates/{templateUuid}/draft
PUT    /admin/deployment-templates/{templateUuid}/draft
POST   /admin/deployment-templates/{templateUuid}/draft/validate
POST   /admin/deployment-templates/{templateUuid}/draft/preview
POST   /admin/deployment-templates/{templateUuid}/publish
GET    /admin/deployment-templates/{templateUuid}/versions
GET    /admin/deployment-templates/{templateUuid}/versions/{version}
GET    /admin/deployment-templates/{templateUuid}/versions/{version}/diff?from={version}
GET    /admin/deployment-templates/{templateUuid}/allowlist
POST   /admin/deployment-templates/{templateUuid}/allowlist
DELETE /admin/deployment-templates/{templateUuid}/allowlist/{allowlistUuid}
```

Models: `DeploymentTemplate{ uuid, slug, name, description, createdAt, updatedAt }`,
`DeploymentTemplateDraft{ templateUuid, manifestYaml, files[], updatedAt }` (`files[]` =
`{path, contentBase64}` bounded, no traversal/symlink), `DeploymentTemplateValidation{ valid, findings[] }`,
`DeploymentTemplatePreview{ nodes[], endpoints[], warnings[] }`, `DeploymentTemplateVersion{ uuid, templateUuid,
version, digest, publishedAt, publishedBy }`, version/diff list envelopes, `DeploymentTemplatePublishRequest{
versionNote }`, and flat `DeploymentTemplateAllowlist`/`...Target`/`...CreateRequest`/`...ListResponse`.
`POST /admin/deployment-templates` accepts the optional `DeploymentTemplateDraftRequest` body
(`manifestYaml`, `files[]`), matching the source handler; no separate create schema is invented.
Binding is stored through the existing task/pentest config (`deploymentTemplateUuid` + `templateVersion` +
optional `parameters` and write-only `secretParameters`), not a second resource. A bind must use an active
allowlist row for the exact immutable version and one of its exact targets; only global admin mutates allowlists.
No raw target/image/driverOptions/credentials come from the frontend.

Plugins (global admin manage; participant metadata only):

```text
GET    /admin/plugins
POST   /admin/plugins/validate
POST   /admin/plugins/install
GET    /admin/plugins/{id}
POST   /admin/plugins/{id}/enable
POST   /admin/plugins/{id}/disable
POST   /admin/plugins/{id}/upgrade
DELETE /admin/plugins/{id}
GET    /admin/plugins/{id}/logs
GET    /admin/plugins/{id}/event-deliveries
POST   /admin/plugins/{id}/event-deliveries/{eventId}/replay
GET    /admin/plugins/{id}/permission-grants
POST   /admin/plugins/{id}/permission-grants
DELETE /admin/plugins/{id}/permission-grants/{grantUuid}
GET    /plugins
GET    /plugins/{id}/...        # only declared routes, namespaced proxy
```

Models: `Plugin{ id, name, version, apiVersion, status, health, author, capabilities[], permissions[], routes[],
installedAt, updatedAt }`, validate/install/upgrade requests (install requires explicit capability approval and
does not auto-enable), log envelope, `PluginVisible{ id, menu[], pages[] }` for the shell. All routes are
`/api/v2`; no v3.

Explicit permission grants are the only way a plugin permission becomes effective:
`GET|POST /admin/plugins/{id}/permission-grants` and
`DELETE /admin/plugins/{id}/permission-grants/{grantUuid}` with
`{ userUuid, permission, boardUuid?, moduleUuid?, expiresAt? }`; global admin only, audited, never auto-granted.
Install layout is filesystem-first: operator places `plugins/<id>/manifest.yaml` + `bin/` or an OCI image
reference + optional `assets/` under `ACTFB_PLUGINS_DIR/incoming`; `install` takes a server-side package path, not
an arbitrary uploaded executable, and never runs package code during validate. Plugin static assets are served by
core from `/api/v2/plugins/{id}/assets/...` with CSP; the shell does not `import` plugin JS.

### 3.6 Remaining Swagger groups (already declared)

A+D: **14 paths / 22 ops** under `/boards/{boardUuid}/modules/{moduleUuid}/attack-defense/*`,
`/admin/.../attack-defense/*`, and `POST /webhooks/git/{repositoryBindingUuid}`; models `ADConfig`,
`ADRepositoryAssignment`, `ADVulnerability*`, `ADRun*`, `GitRepositoryInfo`. `012` adds
`repository_bindings` (encrypted webhook secret, unique integration/repo/branch), `ad_run_results`, and
`ad_awards` (unique subject/vulnerability/kind, idempotent points across commits); the assignment and webhook
FKs point at repository bindings.
Pentest: **18 paths / 22 ops** including environment/actions/drafts/report; models `PentestConfig`,
`PentestModuleInfo`, `PentestEnvironmentActionRequest`.
Instances/integrations: **5 / 6** and **3 / 6**; models `Instance`, `InstanceNode`, `InstanceStatus`,
`Integration*`, `InstancePublicationPolicy`.

## 4. Model conflicts and additive compatibility

| Existing model | Conflict | Frozen additive resolution |
|---|---|---|
| `PentestConfig` | `proxmoxIntegrationUuid` is required; nodes are VM-only | keep the field optional/legacy; add `providerIntegrationUuid`, `deploymentTemplateUuid`, `templateVersion`; require exactly one legacy integration or binding; KubeVirt uses the same binding |
| `InstanceNodeConfig` | VM requires `templateVmid`, forbids `image`; no common source | add `source{type,ref}`, `resources`, typed `driverOptions`; keep `image`/`templateVmid`/`cpuCores`/`memoryMiB` as legacy aliases with mutual exclusion; `templateVmid` is Proxmox-only |
| `TaskInstanceConfig` | inline `nodes` + `providerIntegrationUuid` only | add `deploymentTemplateUuid` + `templateVersion`; forbid template ref together with inline nodes; keep inline nodes for compatibility |
| `IntegrationType` | conflates Git category and runtime driver (`PROXMOX`, `CONTAINER`) | keep enum values; add `kind` (`VM`/`CONTAINER`, null for Git) and `driver` (`proxmox`, `kubevirt`, `docker`, `podman`, `kubernetes`, null for Git); `PROXMOX` maps to VM/proxmox, `CONTAINER` requires a driver |
| `ModuleType` | fixed generated enum `TASKS`, `ATTACK_DEFENSE`, `PENTEST`; not extensible by plugins | add `EXTENSION` to the enum plus `extensionKey`; native values unchanged; plugin module types use `EXTENSION` + installed-plugin registry, never arbitrary strings |
| `TaskType` | fixed enum; plugin checkers cannot add a value | add `EXTENSION` + `extensionKey`; DB CHECK allows only native values plus `EXTENSION`; unknown values are rejected, not passed through |
| `SubmissionChecker` | `AUTOMATIC`, `MANUAL` only | add `PLUGIN` + `checkerKey`; FLAG remains TEXT/AUTOMATIC, MANUAL remains MANUAL |
| `ScoreboardItem`/`AdminSubmission`/`Instance.owner` | contract already has `ScoreSubject`; DB is user-only | `007` adds subject columns/backfill; no API rename; participant `Submission` stays own-view without subject leakage |

Do **not** rename `PROXMOX`, `CONTAINER`, `templateVmid`, `proxmoxIntegrationUuid`, or any existing enum value.
Legacy data stays readable; new code writes the normalized shape and the API emits the legacy field only as a
deprecated alias.

Composition rule: `AdminBoard` and any new `allOf` response over a closed base must be a **flat closed object**, not
`allOf` (the same unsatisfiable Swagger 2.0 defect found for the six P1 responses). `PendingFile`,
`ScoreAdjustment`, `ADRepositoryAssignment`, and `ADRun` were flattened in this round; generated structs no longer
embed `FileRef`/`ScoreAdjustmentRequest`/`ADRepositoryAssignmentRequest`/`ADRunSummary` (fields/JSON unchanged).
Update any code that relied on those embeds; do not rename fields.

## 5. Shared Go contracts (exact; these are the worker boundaries)

### 5.1 Authorization — existing contract unchanged, additive registry/grants

`internal/authorization/authorizer.go` keeps `Principal`, `Scope`, `Decision`, `BoardPermissions`, `ModulePermissions`,
and the `Authorizer` interface exactly as today; `Principal`/`Scope` are not extended.

`internal/authorization/permissions.go`:

```go
package authorization

type PermissionScope string

const (
	PermissionScopeGlobal PermissionScope = "global"
	PermissionScopeBoard  PermissionScope = "board"
	PermissionScopeModule PermissionScope = "module"
)

type PermissionDefinition struct {
	Permission  Permission
	Source      string // "core" or a plugin id "vendor.name"
	Scopes      []PermissionScope
	Description string
}

type PermissionRegistry interface {
	Register(def PermissionDefinition) error
	Lookup(permission Permission) (PermissionDefinition, bool)
	List() []PermissionDefinition
}

type Registry struct{ /* unexported fields */ }

func NewRegistry() *Registry
func CorePermissionDefinitions() []PermissionDefinition

const (
	PermissionBoardAudiencesManage              Permission = "board.audiences.manage"
	PermissionDeploymentTemplatesUse            Permission = "deployment_templates.use"
	PermissionPlatformDeploymentTemplatesPublish Permission = "platform.deployment_templates.publish"
	PermissionModuleInstancesAdmin              Permission = "module.instances.admin"
	PermissionInstanceStartOwn                  Permission = "instance.start_own"
	PermissionInstanceManageOwn                 Permission = "instance.manage_own"
	PermissionTeamManageOwn                     Permission = "team.manage_own"
	PermissionPluginsManage                     Permission = "plugins.manage"
)
```

Registration is trusted-central: core registers its definitions at boot; only the plugin manager registers plugin
definitions, and only after manifest validation. `Register` rejects duplicates, rejects non-namespaced plugin
permissions, and never grants anything. The role matrix is unchanged for existing roles; participant-only
permissions (`team.manage_own`, `instance.start_own`, `instance.manage_own`, `submission.*_own`) are effective from
`board_participants`; plugin permissions are effective only through an explicit grant.

`internal/authorization/grants.go`:

```go
package authorization

type PermissionGrant struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Permission Permission
	BoardID    *uuid.UUID
	ModuleID   *uuid.UUID
	GrantedBy  uuid.UUID
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

type GrantParams struct {
	UserID     uuid.UUID
	Permission Permission
	Scope      Scope
	GrantedBy  uuid.UUID
	ExpiresAt  *time.Time
}

type PermissionGrantChecker interface {
	HasGrant(ctx context.Context, userID uuid.UUID, permission Permission, scope Scope) (bool, error)
}

type GrantStore struct{ /* db */ }

func NewGrantStore(db *postgres.DB) *GrantStore
func (s *GrantStore) HasGrant(ctx context.Context, userID uuid.UUID, permission Permission, scope Scope) (bool, error)
func (s *GrantStore) Grant(ctx context.Context, p GrantParams) (*PermissionGrant, error)
func (s *GrantStore) Revoke(ctx context.Context, actor Principal, grantID uuid.UUID) error
```

`internal/authorization/authorizer.go` additions:

```go
type AuthorizerOption func(*AuthorizerService)

func WithPermissionRegistry(registry PermissionRegistry) AuthorizerOption
func WithGrantChecker(checker PermissionGrantChecker) AuthorizerOption
func NewAuthorizer(db *postgres.DB, opts ...AuthorizerOption) *AuthorizerService
```

`Authorize` rejects a permission not present in the registry; it evaluates the core role matrix, live
participation, then explicit grants; a grant can never exceed the registered scopes. Existing `Principal`/`Scope`
and the `Authorizer` interface remain source-compatible.

### 5.2 Access groups, audiences and ABAC policy

`internal/access/types.go` and `internal/access/service.go`:

```go
package access

type Subject struct {
	Type string // "USER" or "TEAM"
	ID   uuid.UUID
}

type Group struct {
	ID         uuid.UUID
	Name       string
	UsersCount int
}

type Audience struct {
	ID           uuid.UUID
	BoardID      uuid.UUID
	Name         string
	Description  string
	MembersCount int
}

type AudienceMember struct {
	AudienceID uuid.UUID
	Subject    Subject
	AssignedAt time.Time
}

type ListQuery struct {
	Limit  int
	Offset int
	Query  string
}

type GroupPage struct {
	Items []Group
	Total int
}

type AudiencePage struct {
	Items []Audience
	Total int
}

type MemberPage struct {
	Items []AudienceMember
	Total int
}

type AudienceInput struct {
	Name        string
	Description string
}

type AudiencePatch struct {
	Name        *string
	Description *string
}

type MemberInput struct {
	SubjectType string
	SubjectID   uuid.UUID
}

type Policy interface {
	CanJoinBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (bool, error)
	CanAccessModule(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) (bool, error)
	IsSubjectInAudience(ctx context.Context, boardID, audienceID uuid.UUID, subject Subject) (bool, error)
}

type Service interface {
	ListAccessGroups(ctx context.Context, principal authorization.Principal, q ListQuery) (GroupPage, error)
	CreateAccessGroup(ctx context.Context, principal authorization.Principal, name string) (*Group, error)
	GetAccessGroup(ctx context.Context, principal authorization.Principal, id uuid.UUID) (*Group, error)
	UpdateAccessGroup(ctx context.Context, principal authorization.Principal, id uuid.UUID, name string) (*Group, error)
	DeleteAccessGroup(ctx context.Context, principal authorization.Principal, id uuid.UUID) error

	ListAudiences(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, q ListQuery) (AudiencePage, error)
	CreateAudience(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, in AudienceInput) (*Audience, error)
	UpdateAudience(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, in AudiencePatch) (*Audience, error)
	DeleteAudience(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID) error
	ListAudienceMembers(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, q ListQuery) (MemberPage, error)
	AddAudienceMember(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, in MemberInput) (*AudienceMember, error)
	RemoveAudienceMember(ctx context.Context, principal authorization.Principal, boardID, audienceID uuid.UUID, subject Subject) error

	SetBoardAccessGroups(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, groupIDs []uuid.UUID) error
	BoardAccessGroups(ctx context.Context, boardID uuid.UUID) ([]uuid.UUID, error)
}
```

`internal/access/postgres.go` implements `Service` and `Policy` against `*postgres.DB`. `CanJoinBoard` checks
visibility, registration mode, invitation, access-group membership and the board window; `CanAccessModule` checks
joined subject plus audience membership; neither grants RBAC permissions. Boards HTTP calls these methods; it does
not query access tables directly.

### 5.3 Boards/domain: teams, subjects, manual review, file finalization

`internal/boards/types.go` additions/changes:

```go
package boards

type ScoreSubject struct {
	Type string // "USER" or "TEAM"
	ID   uuid.UUID
}

type Team struct {
	ID            uuid.UUID
	BoardID       uuid.UUID
	Name          string
	CaptainUserID uuid.UUID
	MembersCount  int
	MaxMembers    int
}

type TeamMember struct {
	UserID   uuid.UUID
	Username string
	JoinedAt time.Time
}

type TeamSettings struct {
	MinMembers            int
	MaxMembers            int
	AllowSelfCreate       bool
	AllowLeaveAfterStart  bool
}

type TeamInvite struct {
	InviteKey string
	ExpiresAt *time.Time
}

type Submission struct {
	ID                uuid.UUID
	BoardID           uuid.UUID
	ModuleID          uuid.UUID
	TaskID            uuid.UUID
	Subject           ScoreSubject
	SubmittedByUserID uuid.UUID
	Status            string
	ScoreAwarded      *int
	Message           *string
	SubmittedText     *string
	TaskType          string
	Files             []files.Ref
	IdempotencyKey    string
	CreatedAt         time.Time
	ReviewedAt        *time.Time
	ReviewedByUserID  *uuid.UUID
	requestHash       []byte
}

type ScoreboardItem struct {
	Place   int
	Subject ScoreSubject
	Score   int
	FirstAt *time.Time
}

type SubmitInput struct {
	Text    string
	DraftID *uuid.UUID
}

type ReviewInput struct {
	Decision string // "ACCEPT" or "REJECT"
	Message  *string
}

type SubmissionParams struct {
	BoardID           uuid.UUID
	ModuleID          uuid.UUID
	TaskID            uuid.UUID
	Subject           ScoreSubject
	SubmittedByUserID uuid.UUID
	DraftID           *uuid.UUID
	Status            string
	ScoreAwarded      *int
	Message           *string
	SubmittedText     *string
	IdempotencyKey    string
	RequestHash       []byte
	Accepted          bool
}

type ReviewParams struct {
	BoardID          uuid.UUID
	SubmissionID     uuid.UUID
	ReviewedByUserID uuid.UUID
	Status           string
	ScoreAwarded     *int
	Message          *string
	RequestHash      []byte
	ReviewedAt       time.Time
}
```

`internal/boards/service_teams.go` (new) exposes:

```go
func (s *Service) GetMyTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*Team, error)
func (s *Service) CreateTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, name string) (*Team, error)
func (s *Service) UpdateMyTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, name string) (*Team, error)
func (s *Service) JoinTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, inviteKey string) (*Team, error)
func (s *Service) TeamInviteKey(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*TeamInvite, error)
func (s *Service) RotateTeamInviteKey(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*TeamInvite, error)
func (s *Service) LeaveTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) error
func (s *Service) RemoveTeamMember(ctx context.Context, principal authorization.Principal, boardID, userID uuid.UUID) error
func (s *Service) TransferCaptain(ctx context.Context, principal authorization.Principal, boardID, userID uuid.UUID) (*Team, error)
```

`internal/boards/store.go` additions:

```go
CreateTeam(ctx context.Context, p CreateTeamParams, audit AuditEventParams) (*Team, error)
GetTeam(ctx context.Context, boardID, teamID uuid.UUID) (*Team, error)
GetTeamByMember(ctx context.Context, boardID, userID uuid.UUID) (*Team, error)
ListTeamMembers(ctx context.Context, boardID, teamID uuid.UUID, limit, offset int) ([]TeamMember, int, error)
JoinTeam(ctx context.Context, p JoinTeamParams, audit AuditEventParams) (*Team, error)
LeaveTeam(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error
RemoveTeamMember(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error
TransferCaptain(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) (*Team, error)
SetTeamInvite(ctx context.Context, p SetTeamInviteParams, audit AuditEventParams) error
GetTeamInvite(ctx context.Context, boardID, teamID uuid.UUID) (*TeamInvite, error)
ResolveSubject(ctx context.Context, boardID, userID uuid.UUID) (ScoreSubject, error)
```

Exact store parameter types:

```go
type CreateTeamParams struct {
	BoardID       uuid.UUID
	Name          string
	CaptainUserID uuid.UUID
}

type JoinTeamParams struct {
	BoardID uuid.UUID
	TeamID  uuid.UUID
	UserID  uuid.UUID
}

type SetTeamInviteParams struct {
	BoardID       uuid.UUID
	TeamID        uuid.UUID
	InviteKeyHash string
	ExpiresAt     *time.Time
}
```

`CreateSubmission`/`ReviewSubmission` take `Subject` and `SubmittedByUserID`; `CreateSubmission` also accepts
`DraftID` and, in the same transaction, verifies the draft via `files.Verifier`, marks files `ATTACHED` and the
draft `CONSUMED`, then inserts the submission and the `submission.judged` outbox row. The subject is resolved and
locked inside that transaction. `ReviewSubmission` uses `ReviewInput`; `ACCEPT` awards `task.points`, `REJECT`
awards zero; FLAG auto-review remains `SUBMISSION_REVIEW_NOT_ALLOWED`.

### 5.4 Files service

`internal/files/types.go`:

```go
package files

type Ref struct {
	UUID        uuid.UUID
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
}

type Status string

const (
	StatusAwaitingUpload Status = "AWAITING_UPLOAD"
	StatusValidating     Status = "VALIDATING"
	StatusReady          Status = "READY"
	StatusRejected       Status = "REJECTED"
	StatusAttached       Status = "ATTACHED"
	StatusExpired        Status = "EXPIRED"
)

type PendingFile struct {
	Ref
	Status        Status
	RejectionCode *string
}

type Draft struct {
	UUID         uuid.UUID
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string // "TASK" or "PENTEST"
	TaskID       *uuid.UUID
	Status       DraftStatus
	CreatedAt    time.Time
	ExpiresAt    time.Time
	Files        []PendingFile
}

type DraftStatus string

const (
	DraftOpen     DraftStatus = "OPEN"
	DraftConsumed DraftStatus = "CONSUMED"
	DraftExpired  DraftStatus = "EXPIRED"
)

type Policy struct {
	Required            bool
	MinCount            int
	MaxCount            int
	MaxSizeBytes        int64
	AllowedContentTypes []string
}
```

`internal/files/service.go`:

```go
type CreateDraftInput struct {
	Actor        authorization.Principal
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
	TTL          time.Duration
}

type PresignInput struct {
	Actor        authorization.Principal
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
	DraftID      uuid.UUID
	Filename     string
	ContentType  string
	Size         int64
	SHA256       string
	Policy       Policy
}

type PresignResult struct {
	FileUUID        uuid.UUID
	Method          string
	UploadURL       string
	RequiredHeaders map[string]string
	ExpiresAt       time.Time
}

type CompleteInput struct {
	Actor    authorization.Principal
	DraftID  uuid.UUID
	FileUUID uuid.UUID
}

type DownloadInput struct {
	Actor        authorization.Principal
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
	SubmissionID *uuid.UUID
	DraftID      *uuid.UUID
	FileUUID     uuid.UUID
}

type DownloadResult struct {
	URL       string
	ExpiresAt time.Time
}

type ScanResult struct {
	FileUUID            uuid.UUID
	Clean               bool
	DetectedContentType string
	SHA256              string
	Reason              string
}

type VerifyDraftInput struct {
	Actor        authorization.Principal
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
	DraftID      uuid.UUID
	Policy       Policy
}

type VerifiedDraft struct {
	Draft Draft
	Ready []Ref
}

type Service interface {
	CreateDraft(ctx context.Context, in CreateDraftInput) (*Draft, error)
	GetDraft(ctx context.Context, actor authorization.Principal, draftID uuid.UUID) (*Draft, error)
	DeleteDraft(ctx context.Context, actor authorization.Principal, draftID uuid.UUID) error
	Presign(ctx context.Context, in PresignInput) (*PresignResult, error)
	Complete(ctx context.Context, in CompleteInput) (*PendingFile, error)
	Download(ctx context.Context, in DownloadInput) (*DownloadResult, error)
	DeleteAttachment(ctx context.Context, actor authorization.Principal, fileID uuid.UUID) error
	ScanCallback(ctx context.Context, in ScanResult) error
	VerifyDraft(ctx context.Context, in VerifyDraftInput) (*VerifiedDraft, error)
}
```

`Complete` verifies caller/draft ownership and declared hash/size, moves the object to quarantine, starts
validation, and returns `AWAITING_UPLOAD`/`VALIDATING`; `ScanCallback` is the only path to `READY` (clean object is
copied/pinned into the clean bucket); `Download` re-authorizes the exact resource and returns a short-lived URL
with attachment disposition; `VerifyDraft` is read-only and is called by boards inside the submission transaction.
Even teammates cannot read/finalize another member's draft. No bucket/key/version appears in `Ref`.

### 5.5 Deployment and runtime

`internal/deployment/types.go`:

```go
package deployment

type Kind string

const (
	KindVM        Kind = "VM"
	KindContainer Kind = "CONTAINER"
)

type SourceType string

const (
	SourceOCIImage  SourceType = "OCI_IMAGE"
	SourceVMImage   SourceType = "VM_IMAGE"
	SourceVMTemplate SourceType = "VM_TEMPLATE"
)

type Exposure string

const (
	ExposurePublic  Exposure = "PUBLIC"
	ExposurePrivate Exposure = "PRIVATE"
)

type Metadata struct {
	Name        string
	Slug        string
	Description string
}

type Parameter struct {
	Key      string
	Label    string
	Type     string // string, int, bool, choice, text
	Options  []string
	Default  string
	Required bool
	Secret   bool
}

type GeneratedValue struct {
	Key          string
	Kind         string // uuid, password, hex, base64
	Length       int
	RotateOnReset bool
}

type Source struct {
	Type SourceType
	Ref  string
}

type Resources struct {
	CPUMillicores int
	MemoryMiB     int
	DiskMiB       int
}

type EnvVar struct {
	Name      string
	ValueFrom EnvSource
}

type EnvSource struct {
	Generated *string
	Parameter *string
	Literal   *string
}

type FileSpec struct {
	Source string
	Target string
	Mode   string
}

type Endpoint struct {
	Key        string
	Protocol   string
	TargetPort int
	Exposure   Exposure
	Label      string
}

type Bootstrap struct {
	CloudInitFile string
}

type Check struct {
	Kind     string
	Node     string
	Endpoint string
}

type Lifecycle struct {
	Reset string // RECREATE or SNAPSHOT
}

type Node struct {
	Key           string
	Name          string
	Kind          Kind
	Source        Source
	Resources     Resources
	Env           []EnvVar
	Bootstrap     *Bootstrap
	Files         []FileSpec
	Endpoints     []Endpoint
	DriverOptions map[string]json.RawMessage
}

type Template struct {
	APIVersion  string
	Kind        string
	Metadata    Metadata
	Parameters  []Parameter
	Generated   []GeneratedValue
	Nodes       []Node
	Checks      []Check
	Lifecycle   Lifecycle
}

type TemplateVersion struct {
	UUID        uuid.UUID
	TemplateID  uuid.UUID
	Version     int
	Digest      string
	Manifest    Template
	Files       map[string][]byte
	PublishedAt time.Time
	PublishedBy uuid.UUID
}

type Binding struct {
	UUID              uuid.UUID
	BoardID           uuid.UUID
	ModuleID          uuid.UUID
	TaskID            *uuid.UUID
	TemplateVersionID uuid.UUID
	Targets           map[Kind]uuid.UUID
	Parameters        map[string]string
	Lifecycle         Lifecycle
}

type Subject struct {
	Type string // USER or TEAM
	ID   uuid.UUID
}

type DesiredNode struct {
	Node
	IntegrationUUID uuid.UUID
}

type DesiredDeployment struct {
	BindingID  uuid.UUID
	InstanceID uuid.UUID
	BoardID    uuid.UUID
	ModuleID   uuid.UUID
	Subject    Subject
	Nodes      []DesiredNode
	ExpiresAt  time.Time
}

type Capabilities struct {
	Kind               Kind
	SourceTypes        []SourceType
	CloudInit          bool
	FileInjection      bool
	PublicExposure     bool
	SnapshotReset      bool
	PersistentVolumes  bool
	MaxNodes           int
	DriverOptionsSchema json.RawMessage
}

type InstancePublicationPolicy struct {
	PublicTCPHost  string
	PublicPortFrom int
	PublicPortTo   int
	HTTPBaseDomain string
	ForceHTTPS     bool
}

type Finding struct {
	Path     string
	Code     string
	Message  string
	Severity string // error or warning
}

type ValidationResult struct {
	Valid    bool
	Findings []Finding
}

type Validator interface {
	Validate(ctx context.Context, version TemplateVersion, capabilities map[Kind]Capabilities) (ValidationResult, error)
}
```

`internal/runtime/driver.go`:

```go
package runtime

type OperationKind string

const (
	OperationCreate  OperationKind = "CREATE"
	OperationReset   OperationKind = "RESET"
	OperationDestroy OperationKind = "DESTROY"
)

type Operation struct {
	ID         uuid.UUID
	InstanceID uuid.UUID
	NodeKey    string
	Generation int64
	Kind       OperationKind
}

type Target struct {
	IntegrationUUID uuid.UUID
	Driver          string
	Kind            deployment.Kind
	BaseURL         string
	Credentials     map[string]string
	Settings        json.RawMessage
	Publication     deployment.InstancePublicationPolicy
}

type NodeState string

const (
	NodeStateUnknown      NodeState = "UNKNOWN"
	NodeStateProvisioning NodeState = "PROVISIONING"
	NodeStateRunning      NodeState = "RUNNING"
	NodeStateError        NodeState = "ERROR"
	NodeStateDestroyed    NodeState = "DESTROYED"
)

type ProviderEndpoint struct {
	Key      string
	Type     string
	Host     string
	Port     int
	URL      *string
	Exposure deployment.Exposure
}

type ProviderResource struct {
	ProviderID string
	State      NodeState
	Endpoints  []ProviderEndpoint
	Message    string
}

type Plan struct {
	NodeKey      string
	Action       string // CREATE, UPDATE, RECREATE, DELETE, NONE
	ProviderHint string
	Findings     []deployment.Finding
}

type Driver interface {
	Name() string
	Kind() deployment.Kind
	Capabilities(ctx context.Context, target Target) (deployment.Capabilities, error)
	Validate(ctx context.Context, target Target, node deployment.Node) []deployment.Finding
	Plan(ctx context.Context, target Target, desired deployment.Node, current *ProviderResource) (Plan, error)
	Apply(ctx context.Context, target Target, desired deployment.Node, op Operation) (ProviderResource, error)
	Inspect(ctx context.Context, target Target, providerID string) (ProviderResource, error)
	Destroy(ctx context.Context, target Target, providerID string, op Operation) error
}

type Registry interface {
	Register(driver Driver) error
	Get(name string) (Driver, bool)
	List() []Driver
}
```

Drivers never receive Board/User/Score/ACL data. `Target.Credentials` is decrypted only in the trusted worker;
driver errors are mapped to safe findings. The same `deployment.Node` must work for `proxmox`/`kubevirt` (VM) and
`docker`/`podman`/`kubernetes` (CONTAINER); unsupported capability is a `Validate` error, never ignored.

Package direction is frozen: `internal/runtime` imports `internal/deployment`; **`internal/deployment` must not
import `internal/runtime`** (no cycle). The instance orchestrator lives in a separate `internal/instances` package
that imports both. `SourceKind` remains `VM`/`CONTAINER`; `driver` is separate (`proxmox`, `kubevirt`, `docker`,
`podman`, `kubernetes`); legacy `PROXMOX`/`CONTAINER` values are aliases only, never renamed. Provider resources
carry stable labels `actfb.instance`, `actfb.node`, `actfb.generation`; every operation has an idempotency key and
monotonic generation. A public endpoint port is not released until `Destroy` is confirmed (`instance_endpoints`
row `released_at` set only after provider confirmation).

Bindings are exact: an owner/editor with `deployment_templates.use` may bind only an active
`deployment_template_allowlist` row for the immutable version and one of its `allowlist_targets` integrations; no
raw image, driverOptions, credentials or arbitrary target comes from the client. `TaskInstanceConfig`/`PentestConfig`
carry optional non-secret `parameters` and write-only `secretParameters`; secret values are sealed into
`deployment_bindings.secret_parameters_ciphertext` and never appear in responses/preview/diff.

Runtime allocation is core-owned: `instance_network_leases` claims a `(integration_id,allocation_key)` from the
integration trusted network pool with a typed `claim` JSONB (bridge/VLAN/namespace), never from the client
manifest. Live lease uniqueness is enforced until `released_at`; ports/leases are released only after provider
destroy is confirmed. `instances` stores `binding_id`, `template_version_id`, a secret-free
`desired_snapshot`, `request_actor_id` and `removed_at`; board/module FK is `ON DELETE RESTRICT` so a board delete
cannot erase provider inventory before cleanup. A live owner/scope unique index prevents duplicate uncleaned
ERROR/EXPIRED instances. `instance_operations` stores `requested_by`, `authorized_context` and the frozen
`desired_snapshot`; actual generated values remain in `instance_secret_values`. Published
`deployment_template_versions` are immutable via a DB trigger (UPDATE/DELETE raises).

Pentest has no independent TTL: `expires_at` is the effective module/board end. The canonical submission is
`resourceType=PENTEST` with optional `task_id`; the DB mapping is a dedicated `pentest_submissions` table with
module/subject scope and a unique accepted index, and the admin list unions both tables under the same
`AdminSubmission` shape. `file_objects.attached_pentest_submission_id` links report files and the DB check prevents
both core and PENTEST attachments on one file. No polymorphic FK is faked; the service enforces module/board
ownership explicitly.

### 5.6 Plugins

Package split (disjoint files for parallel work): `internal/plugins/contracts.go` (types below),
`internal/plugins/service.go` (interface), `internal/plugins/manager.go` (lifecycle),
`internal/plugins/protocol/httpv1.go` (wire envelopes/signature/limits), `internal/plugins/process/runner.go`
(exec/OCI process lifecycle).

`internal/plugins/contracts.go`:

```go
package plugins

type ID string

type Capability string

const (
	CapabilityBoardsRead      Capability = "core.boards.read"
	CapabilitySubmissionsRead Capability = "core.submissions.read"
	CapabilityEventsSubscribe Capability = "core.events.subscribe"
)

type Status string

const (
	StatusDiscovered   Status = "discovered"
	StatusInstalled    Status = "installed"
	StatusEnabled      Status = "enabled"
	StatusDisabled     Status = "disabled"
	StatusIncompatible Status = "incompatible"
	StatusUnhealthy    Status = "unhealthy"
	StatusFailed       Status = "failed"
)

type BackendMode string

const (
	BackendOCI  BackendMode = "oci"
	BackendExec BackendMode = "exec"
)

type FrontendMode string

const (
	FrontendIframe    FrontendMode = "iframe"
	FrontendTrustedJS FrontendMode = "trusted-js"
)

type Backend struct {
	Mode           BackendMode
	Image          string
	Entrypoint     string
	Protocol       string // must be "http-v1"
	HealthPath     string
	TimeoutSeconds int
}

type Frontend struct {
	Mode  FrontendMode
	Entry string
}

type PermissionDecl struct {
	ID    authorization.Permission
	Scope string // global, board, module
}

type Route struct {
	Method     string
	Path       string
	Permission authorization.Permission
	Scope      string
	UI         bool
}

type Manifest struct {
	ID           ID
	Name         string
	Version      string
	APIVersion   string
	Description  string
	Author       string
	Backend      Backend
	Frontend     Frontend
	Permissions  []PermissionDecl
	Capabilities []Capability
	Routes       []Route
	Events       []string
	ConfigSchema json.RawMessage
	Migrations   string
	Assets       string
	Checksum     string
}

type Health struct {
	Status    string
	Message   string
	CheckedAt time.Time
}

type Installed struct {
	ID          ID
	Version     string
	Status      Status
	Health      Health
	Manifest    Manifest
	InstalledAt time.Time
	UpdatedAt   time.Time
}

type InstallRequest struct {
	PackagePath          string
	ApproveCapabilities  []Capability
	ApprovePermissions   []authorization.Permission
}

type Visible struct {
	ID     ID
	Name   string
	Menu   []VisibleItem
	Pages  []VisibleItem
}

type VisibleItem struct {
	Path      string
	Label     string
	IframeURL string
}

type Validation struct {
	Valid    bool
	Findings []Finding
}

type Finding struct {
	Path     string
	Code     string
	Message  string
	Severity string
}
```

`internal/plugins/service.go`:

```go
type Service interface {
	List(ctx context.Context, principal authorization.Principal) ([]Installed, error)
	Validate(ctx context.Context, principal authorization.Principal, packagePath string) (Validation, error)
	Install(ctx context.Context, principal authorization.Principal, in InstallRequest) (*Installed, error)
	Get(ctx context.Context, principal authorization.Principal, id ID) (*Installed, error)
	Enable(ctx context.Context, principal authorization.Principal, id ID) (*Installed, error)
	Disable(ctx context.Context, principal authorization.Principal, id ID) (*Installed, error)
	Upgrade(ctx context.Context, principal authorization.Principal, id ID, packagePath string) (*Installed, error)
	Remove(ctx context.Context, principal authorization.Principal, id ID) error
	Logs(ctx context.Context, principal authorization.Principal, id ID, limit int) ([]string, error)
	Visible(ctx context.Context, principal authorization.Principal) ([]Visible, error)
	Proxy(ctx context.Context, in ProxyRequest) (ProxyResponse, error)
}

type ProxyRequest struct {
	PluginID ID
	Actor    authorization.Principal
	Method   string
	Path     string
	Query    url.Values
	Headers  http.Header
	Body     []byte
}

type ProxyResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
}
```

`internal/plugins/identity.go`:

```go
type Identity struct {
	PluginID     ID
	Capabilities []Capability
	Permissions  []authorization.Permission
}

type Delegation struct {
	Token     string
	PluginID  ID
	UserID    uuid.UUID
	Scope     authorization.Scope
	Nonce     uuid.UUID
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type DelegationIssuer interface {
	Issue(ctx context.Context, identity Identity, userID uuid.UUID, scope authorization.Scope, ttl time.Duration) (Delegation, error)
	Verify(ctx context.Context, token string) (Delegation, error)
}

type CallContext struct {
	Identity   Identity
	Delegation Delegation
	UserID     uuid.UUID
	Scope      authorization.Scope
	RequestID  string
}

type BoardContext struct {
	BoardID     uuid.UUID
	ModuleID    *uuid.UUID
	SubjectType string
	SubjectID   uuid.UUID
	Permissions []authorization.Permission
}

type CoreProxy interface {
	VerifyDelegation(ctx context.Context, token string) (Delegation, error)
	Authorize(ctx context.Context, call CallContext, permission authorization.Permission) (authorization.Decision, error)
	BoardContext(ctx context.Context, call CallContext, boardID uuid.UUID) (BoardContext, error)
	PublishEvent(ctx context.Context, call CallContext, event events.Event) error
}
```

Swagger exposes `PluginInstallRequest.enable` only so the API can reject `true`; the Go `InstallRequest` deliberately
omits it and installation always leaves `status=installed`.

`CallContext` and `Identity` are passed by value and are immutable. The core proxy never trusts a plugin-supplied
`UserID`/scope: every plugin→core SDK call must carry a core-issued short-lived HMAC delegation token
(`X-Actfb-Delegation`) bound to plugin id, end-user, scope, expiry and one-time nonce; `VerifyDelegation` is the
only source of the end-user/scope, and `Identity` service token alone is insufficient. `Service.Validate` and
`Install` require `plugins.manage`; install leaves `status=installed` and never auto-enables — an `enable=true`
request is rejected with a validation error, and only `POST /admin/plugins/{id}/enable` enables.

### 5.7 Core events

`internal/events/events.go`:

```go
package events

type Event struct {
	ID          uuid.UUID
	Type        string
	OccurredAt  time.Time
	BoardID     *uuid.UUID
	ModuleID    *uuid.UUID
	SubjectType string
	SubjectID   *uuid.UUID
	Payload     json.RawMessage
}

type SubmissionJudgedPayload struct {
	SubmissionUUID uuid.UUID `json:"submissionUuid"`
	BoardUUID      uuid.UUID `json:"boardUuid"`
	ModuleUUID     uuid.UUID `json:"moduleUuid"`
	TaskUUID       uuid.UUID `json:"taskUuid"`
	SubjectType    string    `json:"subjectType"`
	SubjectUUID    uuid.UUID `json:"subjectUuid"`
	Status         string    `json:"status"`
	ScoreAwarded   *int      `json:"scoreAwarded"`
	OccurredAt     time.Time `json:"occurredAt"`
}

const EventSubmissionJudged = "submission.judged"

type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Outbox interface {
	Enqueue(ctx context.Context, tx Tx, event Event) error
}

type Publisher interface {
	Publish(ctx context.Context, event Event) error
}
```

`submission.judged` contains no flag, answer hash, request hash, submitted text or file bytes. Outbox insert is in
the same transaction as the verdict; the dispatcher delivers at least once and dedupes by event ID. Core task
events keep `taskUuid`; AD/Pentest events use their own registered typed payloads (e.g. `ad.run.completed`,
`pentest.report.judged`) and must not put a fake task UUID into the core submission event schema.

## 6. Plugin protocol HTTP v1 (chosen primary)

`http-v1` is the primary, versioned protocol; gRPC is not required for the first release. Core↔plugin contract:

```text
Core -> plugin   POST {backendBaseURL}/v1/invoke
Headers: X-Actfb-Protocol: 1, X-Actfb-Plugin-Id, X-Actfb-Request-Id, X-Actfb-Service-Token
Body:    {"protocol":"1","requestId":"uuid","operation":"api|page|event|health",
          "pluginId":"vendor.name","userUuid":"uuid|null",
          "scope":{"boardUuid":"uuid|null","moduleUuid":"uuid|null"},
          "method":"GET","path":"...","query":{},"headers":{},"bodyBase64":""}
Reply:   {"protocol":"1","requestId":"uuid","status":200,"headers":{},"bodyBase64":""}

Core -> plugin   POST {backendBaseURL}/v1/events
Headers: X-Actfb-Event-Id, X-Actfb-Signature: sha256=<hmac>
Body:    {"protocol":"1","event":{"id":"uuid","type":"submission.judged",...}}

Plugin -> core   POST /api/v2/plugin-sdk/v1/authorize | /board-context | /events
Headers: Authorization: Plugin <service-token>, X-Actfb-Delegation: <short-lived-hmac>

Health:  GET {backendBaseURL}/v1/health -> {"status":"ok"}
```

Rules: loopback/explicit plugin network only; no redirects or userinfo; 1 MiB request/response caps; timeout from
manifest (1..30 s, default 5 s); service token is random per plugin, stored hashed, rotated on enable, never sent to
the browser/iframe; plugin proxy routes only the declared methods/paths under `/api/v2/plugins/{id}/...`; a crashed
or disabled plugin returns controlled `503` and core stays healthy. Event delivery is at-least-once; plugins dedupe
by `event.id`, and cannot mutate verdict/score except through a future explicit core command. Plugin SDK calls must
present a core-issued HMAC delegation token; the core derives end-user/scope from that token and rejects a
missing/expired/replayed nonce. A plugin-supplied `userUuid` in the body is never trusted.

The iframe bridge is fixed for an **opaque** sandbox: untrusted iframes use
`sandbox="allow-scripts allow-forms"` (no `allow-same-origin`), so `event.origin === "null"`. On creation the host
validates `iframe.src` origin against the configured separate asset/host origin; it never compares the opaque
`event.origin` with that http(s) origin (impossible). Every message must satisfy
`event.source === iframe.contentWindow` and `event.origin === "null"`, and carry a per-iframe CSPRNG nonce and
request id. Message types are `actfb.plugin.context`, `actfb.plugin.navigate`, `actfb.plugin.result` with
`{requestId, nonce, payload}`. Parent CSP restricts `frame-src` to the configured plugin asset origin. The host
never sends access/refresh tokens, cookies, service tokens or provider credentials. `trusted-js` remains a
separate, explicit same-origin-risk mode and is not the sandbox.

Plugin assets are served only from the configured separate asset-server origin, never from a core/API path that
also serves HTML. The core plugin API proxy strips `Set-Cookie`, `Location`, `Content-Security-Policy` rewrites and
other untrusted hop headers, and never renders plugin HTML/JS/SVG on the core origin; plugin responses are limited
to JSON/text-plain or `Content-Disposition: attachment`.

Plugin private data: the manager provisions `plugin_<slug>` schema/database with a restricted role that has no
grants on core tables; core proxies only scoped Board/Submission/files metadata through `CoreProxy`. Plugin
migrations run before enable; disabling preserves data.

## 7. Secrets and configuration

New config fields (`internal/config`), env-only, no secrets in DB plaintext or logs:

| Field | Env | Contract |
|---|---|---|
| `Deployment.CredentialKey` | `ACTFB_CREDENTIAL_ENCRYPTION_KEY` | base64 32 bytes; AES-256-GCM; `credentials_ciphertext` + `credentials_key_version` in DB; decrypt only in trusted worker |
| `Deployment.KeyVersion` | `ACTFB_CREDENTIAL_KEY_VERSION` | small integer; supports rotation |
| `Plugins.Root` | `ACTFB_PLUGINS_DIR` | filesystem install root; default `./plugins` |
| `Plugins.ServiceKey` | `ACTFB_PLUGIN_SERVICE_KEY` | base64 32 bytes; HMAC service tokens/event signatures/delegations |
| `Plugins.AssetOrigin` | `ACTFB_PLUGIN_ASSET_ORIGIN` | separate asset-server origin for iframe HTML/JS, no core cookies/JWT |
| `Plugins.HTTPTimeout` | `ACTFB_PLUGIN_HTTP_TIMEOUT` | default 5 s, max 30 s |
| `Plugins.MaxBodyBytes` | `ACTFB_PLUGIN_MAX_BODY_BYTES` | default 1 MiB |

`IntegrationCreateRequest.credentials` and `AdminTaskCreateRequest.acceptedAnswers` remain write-only; no GET/list
returns them. `InstancePublicationPolicy` is the only source of public host/port allocation. Provider endpoints are
SSRF-allowlisted; no per-request URL from clients.

Deployment secrets: `integrations.credentials_ciphertext`, `deployment_bindings.secret_parameters_ciphertext` and
`instance_secret_values.ciphertext` are AES-256-GCM under the same external key with a key version. The normalized
`DeploymentTemplate`/`DeploymentTemplateVersion` snapshot contains no plaintext provider credentials, binding secret
parameters or generated instance values; those are decrypted only in the trusted worker at apply time.

## 8. Parallel ownership and merge order

Order: schema/Swagger first per stage → interfaces compile → feature workers in parallel → boot owner wires →
UI owner implements → integration tests. No two workers edit the same file. The exact lane table is in
`docs/implementation-plan.md` (“Remaining stages”); the hard rules are:

- Schema/Swagger owner: `migrations/007..013`, `docs/swagger.yaml`, `internal/generated/openapi/**`, schema contract
  tests only.
- Identity/access/authz: `internal/authorization/**`, `internal/access/**`.
- Boards/P1: `internal/boards/**`, `internal/boards/http/**`; may call files/events/access contracts but not edit
  them.
- Files: `internal/files/**` including its HTTP handler package.
- Deployment/runtime: `internal/deployment/**`, `internal/runtime/**`, Docker driver; no boards edits.
- Plugins: `internal/plugins/**`, `cmd/plugin-runner/**`, `examples/plugins/**`; no core authz edits. Inside the
  lane, `internal/plugins/protocol/**` (HTTP v1 envelopes/signing) and `internal/plugins/process/**` (exec/OCI
  runner) are disjoint file sets; the manager/service lives in `internal/plugins/*.go`.
- Frontend: `frontend/**`, one owner, sequential phases.
- Boot/integration: `cmd/backend/**`, `internal/httpapi/**`, `docker-compose.dev.yaml`, `internal/integration/**`,
  `e2e/**`, after constructors/interfaces freeze; one owner at a time.

Identity/access also owns `internal/access/http/**` for `/admin/access-groups`; board audience/team endpoints stay
in `internal/boards/http/**` and call the access service. No worker edits another lane's HTTP package.

## 9. Acceptance and negative tests

Required per stage (real DB/HTTP unless marked fake-driver):

- P1R: TEAM board create/join/leave/captain/invite; **double team score** denied; same user cannot re-score via a
  second team; PUBLIC board join and PRIVATE denial; INVITE_ONLY code rotation/replay and ciphertext GET; access-group
  gating; audience module denial/allow; MANUAL accept/reject and no duplicate points; file IDOR (other user's
  draft/file/download), draft cross-board/task substitution, expired draft, rejected/unclean file, **zip traversal**
  on template/plugin import, oversized/wrong-type file, double finalize; scanner unavailable fails closed.
- P2: validate/preview create no infrastructure; immutable version UPDATE/DELETE rejected; allowlist exact-target
  binding; foreign module allowlist rejected; driver capability rejection; Docker CONTAINER run/reset/destroy/TTL;
  worker kill/retry no duplicate; endpoint port/network lease not released until destroy confirmed; live lease unique
  until released_at; board delete blocked by RESTRICT; template generation reproducible; no plaintext secrets in
  normalized snapshot; secretParameters write-only.
- P3: one VM manifest on Proxmox; kind≠driver; mixed VM+CONTAINER rejected; templateVmid not required in common
  spec; Pentest deadline equals module/board end; `pentest_submissions` maps to canonical PENTEST admin view; fake
  driver contract tests where the real endpoint is absent.
- P4: signed webhook, duplicate delivery/commit SHA **webhook dedup**, one run per push, isolated checker limits,
  no host socket/secrets, separate exploit/patch verdicts, encrypted Git credentials; AD awards unique per
  subject/vulnerability/kind and never double across commits; repository binding FKs valid.
- P5: install/enable/disable/upgrade/remove; install never auto-enables; **plugin crash does not build/restart
  core**; capability proxy denial; permission not auto-granted; delegation token required and plugin-supplied user
  id ignored; opaque iframe (`origin==="null"`, source/contentWindow/nonce checks) and separate asset origin; API
  proxy strips Set-Cookie/Location and never renders HTML/JS/SVG; event replay dedupe; path traversal; no Go/Vite
  rebuild.
- Schema: `013` durable MANUAL text: old rows explicit NULL, MANUAL text ≤4,000,000 bytes, FLAG text/INSERT spoof
  rejected by the trigger, task type change leaves the snapshot intact, down/up preserves P1 rows.
- Schema: real PostgreSQL migration test for `007`–`013` with backfill, down/up, idempotent re-up, and preservation
  of P0/P1 rows; allowlist foreign-module rejection, live lease uniqueness/release, immutable-version trigger,
  board-delete RESTRICT, and AD award uniqueness; public goose DB stays at 6 until the final migration set is
  approved.

Blocked acceptance (not code bugs): no real Proxmox/KubeVirt/Kubernetes/GitLab/GitHub/Gitea endpoint or credentials
is configured. Fake adapters that return success are forbidden; contract tests with a fake driver are allowed.

Read-only tool inventory (2026-11-13): Go `1.27.3`; Docker `29.9.3` with a present socket; Podman `6.1.4`; Bun
`1.4.4`; Git `2.56.0`; protoc `36.1`; psql `18.6`; swagger `v0.36.7`; actionlint via cached `go run` (`v1.7.12`).
No provider credential variables are present; a kubeconfig exists but was not read/validated, and no aCTFb
integration endpoint is configured. Proxmox/K8s/Git acceptance therefore stays blocked until an operator supplies
real endpoints; only parser/capability/contract tests can pass locally.

## 10. Open source-contract handoffs (do not invent)

- **Deployment create body:** source `deployment.Handler.CreateTemplate` decodes the optional
  `DeploymentTemplateDraftRequest` (`manifestYaml`, `files[]`); Swagger now carries that optional body.
- **Pentest environment create:** source service `StartPentestEnvironment` exists and requires `Idempotency-Key`;
  Swagger now declares `POST /boards/{boardUuid}/modules/{moduleUuid}/pentest/environment` → `202 Instance`.
  The instances HTTP handler still needs to register it; `GET .../pentest/submissions` also lacks a handler.
- **Scoped template enumeration:** `deployment.Service.PublishedMetadata` and `ReadPermissionProvider` exist, but
  there is no HTTP list of allowlisted versions/targets for an owner/editor. Proposed op (needs source handler):
  `GET /admin/deployment-templates/{templateUuid}/versions/{version}/metadata?boardUuid=&moduleUuid=&taskUuid=`
  → 200 flat `DeploymentTemplatePublishedMetadata{uuid, templateId, version, digest, name, slug, description,
  parameters[{key,label,type,options,required,secret,default?}], nodes[{key,name,kind,action}]}`; no secret/file
  fields. The allowlist target set is read from the active `deployment_template_allowlist` for that scope. Add to
  Swagger only after the source handler exists.
- **INSTANCE task binding:** `TaskInstanceConfig` now carries `deploymentTemplateUuid`, `templateVersion`,
  non-secret `parameters` and write-only `secretParameters`; the boards task create/patch handler must persist the
  binding and select only an allowlisted immutable version/target. This is a boards worker handoff, not a second
  route.
- **Plugin validation metadata:** actual `plugins.Validation` is only `{valid, findings[]}`; install requires exact
  `approveCapabilities`/`approvePermissions`. Do not invent approved-capability metadata in the validation response;
  the UI reads installed manifest metadata via `GET /admin/plugins/{id}` after install.
- **`board.audiences.manage`** is now in `PermissionCode`; the core catalog already uses it. No duplicate grant API.
- **Remaining handler gaps** tracked in `docs/all-stages-verification.md`: deployment allowlist handlers, admin
  audit-log, admin module/task/scoreboard/batch visibility, and Pentest list/create. Add handlers to the owning
  package; do not fake success.
- **Board delete:** instances FK is `ON DELETE RESTRICT`; the boards delete handler must map the restrict/FK failure
  to a safe `409` (e.g. board state conflict) after cleanup, never delete provider inventory.
- **A+D/plugin scoring:** keep the existing boards scoreboard seam (A+D awards, canonical PENTEST submissions,
  plugin checker verdicts) inside the submission/scoreboard transaction; no parallel score route.

## 11. Handoff rules

- Main approves this contract; then schema owner lands `007`+`008` and Swagger models before P1R workers start.
- A worker who needs a change to another lane's interface stops and reports; no silent local fork.
- Every merge updates `docs/implementation-plan.md` status and lists changed files, migrations, commands and
  blockers.
- Do not mark a stage accepted without its gate evidence; do not claim the whole project complete, the full
  153-operation API, production readiness, or plugin/runtime completion.