# aCTFb — instructions for Codex

You are working in the **aCTFb** repository, not Proxier.

## Mandatory specifications

Read these current sources before editing the project:
1. `aCTFb_CODEX_TASK_v4.md` — product scope, priorities, and delivery gates.
2. `docs/swagger.yaml` — canonical `/api/v2` HTTP contract; `docs/p1-core-contracts.md` and
   `docs/access-authorization.md` — current Board/Module/Task domain and scoped RBAC.
3. `docs/architecture.puml` — infrastructure and trust-boundary diagram.
4. `docs/deployment-templates.md` and `docs/runtime-instances.md` — DeploymentTemplate, VM/CONTAINER
   `kind` vs driver, immutable versions, preview/plan, runtime lifecycle, driver capabilities, and RBAC.
5. `docs/plugin-system.md` and `docs/plugin-developer-guide.md` — installable CTFd-style addons, Plugin API
   v1, process isolation, permissions/grants, and frontend iframe contract.
6. `docs/production.md` and `deploy/README.md` — GHCR-only operator bundle: `config.yaml` owns app
   settings/JWT/origins/flags/worker limits, `.env` is datasource/Docker-only, plus TLS, profiles, admin
   bootstrap, and rollback.

These documents complement one another and preserve the invariants of the removed historical root TZ snapshots
(`aCTFb_DEPLOYMENT_TZ_v3.md`, `aCTFb_PLUGIN_SYSTEM_TZ.md`): kind ≠ driver, immutable template versions, no
fake provider success, process-isolated addons without core rebuilds, explicit permissions, and no unsafe
provider options. If documents conflict or diverge from the working code, identify this explicitly and propose
a compatible migration; don't silently override working APIs.

## Source of truth

The CURRENT repository and `docs/swagger.yaml` have priority over the old archived audit.
The `proxier/docs/` folder included with this setup is a **reference only** for visual language and template/deployment UX. Never copy Proxier domain models or proxy features into aCTFb.

## Work practices

- Begin by inspecting the repository and validating the build/migrations; maintain `docs/implementation-plan.md` with implemented/partial/missing/blocked statuses.
- Implement vertical slices (database → service → API → UI → tests), not demos or stubs described as functional.
- Delegate independent bounded tasks to actual `ali/deepseek-v4.1-flash` subagents where available, with non-overlapping write scopes; check model selection rather than claiming delegation based on a prompt alone.
- The main agent owns contracts, merges, review, and integration tests.
- Preserve security boundaries for untrusted CTF code and deployment credentials; never embed secrets in the repo.
- After each slice, report changed files, tests actually run, remaining blockers, and next slice.

## Start-up order

1. Verify toolchain, `go test ./...`, `go vet ./...`, migrations, and currently implemented packages.
2. Repair broken boot/auth flow and health/readiness.
3. Implement stable core contracts and an end-to-end board/task path.
4. Add plugin and runtime extension points without hardcoding providers.
5. Grow the UI using Proxier reference styles, and implement deployment/workers and the remaining features in dependency order.
