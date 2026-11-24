# Instance lifetime policy design (frozen, approved by main)

Date: 2026-11-24
Base commit: `9a7656474b770bc26fa6e332c6b913743a1552eb`
Status: approved spec. Main owns the design and the UX contract; this file transcribes it for
implementation. The user already approved this direction explicitly.

Scope: task-instance lifetime (initial TTL, max total lifetime, student extension), module
instance settings persistence, and the enforcement gaps that make those settings honest.
Pentest keeps its existing board/module-end lifetime.

## Invariants preserved

- `/api/v2` stays canonical. No v3, no replacement API, no new public "deploy anything" route.
- One trusted executor: `cmd/instance-worker` keeps serving every registered driver
  (`docker`, `podman`, `kubernetes`, `proxmox`, `kubevirt`) through the existing registry.
  No new deployment service, no per-driver container, no generic untrusted HTTP deploy API.
- Authority stays ids-only NATS jobs + PostgreSQL state + approved immutable templates and
  target integrations. Secrets and provider credentials remain worker-only.
- AD checker (`ad-worker`) and plugin runner (`plugin-runner`) lanes are not merged.
- Additive migration only: next number is `019` (`018` is the current latest).
- Applied migrations are never rewritten. Existing instances keep working; missing policy in an
  old snapshot falls back to the global defaults.
- No implicit Docker↔Podman fallback, no fake provider success.
- Packaging stays one binary per Alpine `linux/amd64` image.

## 1. Lifetime model

Durations are anchored at **allocation/request time** (`instances.created_at`). The existing
anchor is preserved; startup time is included. There is deliberately no switch to a
"RUNNING-based" TTL, because that would need new provisioning caps.

```text
effectivePolicy = task overrides -> module settings -> global ServiceOptions defaults
hardDeadline    = min(created_at + maxTotalTtlSeconds, earliest(boardEnd, moduleEnd))
initialExpires  = min(created_at + initialTtlSeconds, hardDeadline)
```

- `maxTtlSeconds` is the **maximum total lifetime**, never the per-extension amount.
- `extension.seconds` is one extension step; `extension.maxCount` is the number of steps.
  `maxCount = 0` means "no extensions", never "unlimited".
- Board/module ends are always re-read live, including for old instances, and always clamp.

Precedence per field (first present wins):

| Field | Task override | Module setting | Global default |
|---|---|---|---|
| initial TTL | `tasks.instance_ttl_seconds` | `defaultTtlSeconds` | `Options.DefaultTTL` (1h) |
| max total TTL | `tasks.instance_max_ttl_seconds` | `maxTtlSeconds` | `Options.MaxTTL` (7d) |
| extension enabled | `tasks.instance_extension_enabled` | `extension.enabled` | enabled |
| extension step | `tasks.instance_extension_seconds` | `extension.seconds` | `Options.ExtensionTTL` (30m) |
| extension count | `tasks.instance_extension_max_count` | `extension.maxCount` | `Options.MaxExtensions` (3) |
| reset enabled | — | `resetEnabled` | enabled |
| max concurrent | — | `maxConcurrentInstancesPerSubject` | `Options.MaxInstancesPerSubject` (2) |

Legacy rows with no module settings and no task overrides therefore keep the existing
1h/7d/+30m×3 behaviour. Empty module settings are not an error.

## 2. Validation (never silent clamping of admin configuration)

- Module: `60 <= defaultTtlSeconds <= maxTtlSeconds <= 604800`;
  `1 <= maxConcurrentInstancesPerSubject <= 100`;
  `60 <= extension.seconds <= 86400`; `0 <= extension.maxCount <= 100`.
- Task initial TTL and task max total TTL must be `<= module maxTtlSeconds` and `<=` the global
  hard max. Effective initial TTL must be `<=` effective max total TTL.
- Task extension `enabled: true` is rejected when the module policy disables extensions.
- Task `extension.seconds`/`maxCount` may not exceed the module limits when the module defines
  them. A module that caps `maxCount` at 3 cannot be bypassed by a task asking for 6.
- Every violation returns a field-specific message; admin input is never silently raised or
  lowered. Only the *effective deadline* is clamped (by the hard deadline), which is documented.

## 3. Snapshot freeze

`DesiredSnapshot` gains a secrets-free `lifecycle` block frozen at instance CREATE:
initial TTL, max total TTL, extension enabled/step/count, reset enabled, max concurrent.
Later task/module policy edits affect **new** instances only. Current board/module ends and
the hard deadline still apply live to old instances.

## 4. Extension semantics

- Participant `EXTEND` is allowed only while `RUNNING` and before `expires_at`, with live actor
  re-authorization (USER/TEAM membership, module audience, scoped permission). The actor is
  never fabricated.
- The student never picks a duration. The server uses the snapshotted step.
- `newExpiresAt = min(currentExpiresAt + step, hardDeadline)`; the base is the current expiry,
  never "now", so an extension can never shorten a deadline.
- No actual increase → rejected, credit not consumed. Partial increase at the hard deadline →
  consumes exactly one credit and reports the real new expiry.
- Idempotency: `instance_extension_requests (instance_id, request_key)` unique, with actor and
  request hash. A retried `Idempotency-Key` returns the original outcome and never consumes a
  second credit.
- The mutation, credit increment, idempotency row and audit event are one transaction with the
  instance row locked and the DB clock read **after** the lock wait. A last-second EXTEND never
  resurrects `EXPIRED`/`DESTROYING` and never cancels queued cleanup.
- TEAM members share the instance row, so `maxCount` is a single shared budget.
- Staff `EXTEND` uses the same caps. There is no silent admin bypass.

## 5. Worker enforcement

Before `Apply`/`Reset` for every node the worker re-reads from the database: current
`instances.expires_at`, current board/module window, and live authorization. The frozen
`authorized_context` may further restrict but never overrides an earlier live deadline.
A queued job whose deadline already passed performs **zero** driver `Apply` calls.

If the deadline passes while a provider call is in flight, the node is not advertised `RUNNING`;
the operation is cleaned up and destroy is queued. Leases and ports are released only after a
confirmed provider destroy. The existing 30 s expiry/reconcile loop is kept, so cleanup is
"within the interval", not a millisecond promise.

## 6. Concurrency

`maxConcurrentInstancesPerSubject` is enforced with a per-module+subject advisory lock so two
parallel task starts cannot both pass. Live rows (`removed_at IS NULL`, including
`ERROR`/`DESTROYING`) count as allocated until confirmed cleanup, so capacity is released after
destroy, not before. The existing per-scope unique index is retained.

`resetEnabled` is honoured for native task `RESET` using the frozen policy for new instances and
the live module setting for legacy rows.

## 7. UX contract (frozen by main)

Task editor, INSTANCE section:

- Approved template name/version + existing typed parameters (unchanged, immutable on edit).
- `Lifetime`: `Inherit module defaults` | `Custom`.
  - Custom: `Initial lifetime` (number + minutes/hours), `Max total lifetime` in Advanced,
    `Allow student extensions` toggle with `step` and `max extensions`.
  - `Inherit` clears **all** overrides explicitly; it never writes zero.
  - Helper text resolves the current module values instead of guessing globals.
- Regular non-instance tasks never show lifetime fields.
- Saving only the policy never clears the immutable binding or blank parameters.

Student instance panel: server-provided `canExtend`, `extensionSeconds`, `extensionsRemaining`;
countdown from the server `expiresAt` clamped at zero; `Extend +15 min` disabled with a readable
reason. Empty/zero count is never rendered as "unlimited".

Admin inventory keeps using the existing instance read model (expires/used/remaining).

## 8. Out of scope

- No per-task `starts_at`/`ends_at` (they do not exist; board/module windows remain).
- No new Pentest TTL. Pentest stays board/module-end based and is documented as such.
- No new VM provider credentials or live VM acceptance. Proxmox/KubeVirt/Kubernetes remain
  contract-tested only and are reported as blocked, not "tested".
- No `ACTFB_PROVIDER_ALLOWED_HOSTS` redesign; the existing env-only operator allowlist stands
  and is documented as the current VM blocker.
