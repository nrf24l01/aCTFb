# Instance lifetime implementation plan (vertical slices)

Date: 2026-11-24
Spec: `docs/superpowers/specs/2026-11-24-instance-lifetime-design.md`
Base commit: `9a7656474b770bc26fa6e332c6b913743a1552eb`
Status: implemented. Evidence in `docs/instance-lifetime-verification.md`. Serial, one agent, Docker only, no subagents.

Gates: `go build ./...`, `go vet ./...`, `go test ./...`, real-PostgreSQL packages with
`ACTFB_TEST_POSTGRES_DSN`, one real-Docker expiry test, `bun run typecheck`, `bun run test:unit`,
`bun run build`, Swagger regeneration with the real `swagger v0.36.7` generator.

## Slice 1: contract + schema (committed separately)

- [x] Swagger `TaskInstanceConfig`: add optional `maxTtlSeconds` (60..604800) and
      `extension` reusing `TaskInstanceModuleSettingsExtension`; document inherit/null semantics.
- [x] Swagger `Instance`: add optional read-only `canExtend`, `extensionSeconds`,
      `extensionsRemaining`.
- [x] Regenerate models with `swagger generate model -f docs/swagger.yaml` (v0.36.7); keep every
      pre-existing model and the 205 operations / 152 paths unchanged.
- [x] Migration `019_instance_lifetime.sql`:
      `tasks.instance_ttl_seconds`, `instance_max_ttl_seconds`, `instance_extension_enabled`,
      `instance_extension_seconds`, `instance_extension_max_count` (nullable = inherit);
      `modules.task_instance_settings JSONB` (nullable); `instance_extension_requests`
      idempotency table. Goose up/down, bounds checks, no rewrite of `001`-`018`.
- [x] Real-PG migration test: legacy rows stay NULL, down/up is clean.

## Slice 2: boards module settings persistence

- [x] `boards.Module` + `TaskInstanceSettings` domain type; `moduleColumns`/`scanModuleRow`.
- [x] `AdminCreateModule`/`AdminUpdateModule` accept `taskInstanceSettings` instead of returning
      `UNSUPPORTED`; validate bounds and `default <= max <= global hard max`.
- [x] `adminModuleModel` maps the persisted settings (nil stays omitted).
- [x] Task TTL fields: `TaskInstanceInput` gains values, `CreateTaskParams`/`UpdateTaskParams`
      persist them, `Task` reads them back, `adminTaskModel.InstanceConfig` returns them.

## Slice 3: instances lifecycle service

- [x] `instances.LifecyclePolicy` + `ResolveLifecycle(task, module, options)` exported resolver
      used by both `instances` and the boards read model (narrow injected seam).
- [x] `start`: resolve policy, freeze into `DesiredSnapshot.Lifecycle`, compute
      `initialExpires` from `created_at` + hard deadline, enforce `maxConcurrent` under an
      advisory lock per module+subject.
- [x] `extend`: snapshot step/count, DB clock after lock, idempotency table, credit once,
      hard-deadline clamp, reject no-increase, never revive `EXPIRED`/`DESTROYING`.
- [x] `reset`: honour frozen `resetEnabled`.
- [x] Instance read model exposes `canExtend`, `extensionSeconds`, `extensionsRemaining`.
- [x] Old snapshots without `lifecycle` fall back to global defaults.

## Slice 4: worker enforcement

- [x] `Orchestrator.HandleJob`: before each node apply/reset re-read live `expires_at`, board
      window, module window and live auth; zero driver calls when already expired.
- [x] Fresh re-check after provider ready before flipping `RUNNING`; cleanup on late expiry.
- [x] Expired-before-apply test asserts zero `Apply` calls; fake provider blocking past the
      deadline leaves no `RUNNING` and no leaked port.

## Slice 5: UI

- [x] `ModuleEditorDialog`: existing TTL/concurrency/reset fields persist and reload; add
      extension enabled/step/count.
- [x] `TaskEditorDialog`: `Lifetime` Inherit/Custom, initial TTL, advanced max total TTL,
      student-extension toggle + step + max count; inherit clears overrides explicitly.
- [x] `InstancePanel`: server `canExtend`/`extensionSeconds`/`extensionsRemaining`, clamped
      countdown, readable disabled reason, idempotent extend.
- [x] `shared/types/api.ts` updated to the generated contract.

## Slice 6: tests, docs, packaging

- [x] Real-PG tests: precedence, inherit/null semantics, stale module limits, snapshot freeze,
      extend idempotency/credit-once/cap, TEAM shared budget, reset does not extend,
      maxConcurrent 2 parallel tasks, capacity release on destroy.
- [x] One real-Docker test: expired fixture row → reconcile destroys container, endpoint and
      lease released, no port leak; `EXTEND` over real HTTP increments expiry.
- [x] `GOARCH` typo fix in `Dockerfile` build stage + packaging check that `linux/amd64` is real.
- [x] Docs: `docs/runtime-instances.md`, `docs/configuration.md`, `docs/deployment-templates.md`
      lifetime sections; verification report `docs/instance-lifetime-verification.md`.
- [x] Final: `go build`, `go vet`, `go test`, frontend gates, review diff under
      `/tmp/opencode/instance-lifetime-review.diff`.
