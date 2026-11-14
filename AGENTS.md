# aCTFb — instructions for Codex

You are working in the **aCTFb** repository, not Proxier.

## Mandatory specifications

Read these three files before editing the project:
1. `aCTFb_CODEX_TASK_v4.md` — main specification and development priorities.
2. `aCTFb_DEPLOYMENT_TZ_v3.md` — deployment template and VM/container runtime contracts.
3. `aCTFb_PLUGIN_SYSTEM_TZ.md` — installable, CTFd-style addon system.

They complement one another. For deployment, use the deployment specification; for addons, the plugin specification; for general behavior, the main one. If documents conflict or diverge from the working code, identify this explicitly and propose a compatible migration; don't silently override working APIs.

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
