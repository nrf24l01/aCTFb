# aCTFb plugin system — implementation and operations

Status: P5 implementation (manager, protocol, runner, SDK, assets, HTTP adapter, checker bridge, three external
examples). `cmd/backend` wires the manager, HTTP adapter, SDK and assets listener. The Vue iframe host remains owned by
the frontend owner. This document describes the exact contracts and what is verified.

## 1. What is installable

A plugin is a **separate package** containing:

```text
plugins/<plugin-id>/            (operator source or installed copy)
  manifest.yaml                 required, strict YAML
  bin/plugin                    required for backend.mode=exec
  assets/                       optional but required for iframe UI
  migrations/*.sql              optional; run in a restricted private schema
```

Installation is filesystem-first. `POST /api/v2/admin/plugins/install` accepts only a server-side path relative to
`ACTFB_PLUGINS_DIR/incoming` (or a `.zip` in that directory). Client uploads are never executed or treated as code.
Plugins are **not** Go `plugin.Open`, compiled sources or Vite imports.

Install layout after success:

```text
<ACTFB_PLUGINS_DIR>/<plugin-id>/<version>/   immutable package copy
<ACTFB_PLUGINS_DIR>/<plugin-id>/data/        plugin-owned runtime data directory
<ACTFB_PLUGINS_DIR>/<plugin-id>/run/plugin.sock  exec backend Unix socket
```

`validate` and `install` never start package code. `install` always leaves `status=installed`; an API request with
`enable=true` is rejected with `VALIDATION_FAILED` and the explicit enable call is required. Only a global
`plugins.manage` holder can install/enable/disable/upgrade/configure/remove; every action is audited.

## 2. Package safety

- Strict manifest decode (`KnownFields`) rejects unknown YAML keys, multiple YAML documents and oversized manifests.
- Namespaced id only: `vendor.name` (lowercase, digits, `. _ -`).
- Semantic version and `apiVersion: "1"` are required; an incompatible package is rejected before execution.
- Zip extraction rejects zip-slip entries, absolute paths, duplicate names, encrypted entries, symlinks, special files,
  too many files and oversized archives/uncompressed data.
- Directory packages reject symlinks and special files. Assets are limited to 5 MiB per file; package files
  (including a compiled backend binary) are limited to 64 MiB; total uncompressed size is bounded.
- OCI images must be pinned as `repository@sha256:<64 hex>`. `latest`/tags are rejected.
- `exec` requires an operator policy `ACTFB_PLUGIN_ALLOW_EXEC=true`; absence is a hard validation error.
- The manifest checksum (`checksum: sha256:<hex>`) covers package content excluding `manifest.yaml` (self-reference is
  impossible). The manager always computes and stores its own immutable content digest as well.
- Declared permissions must be `plugin.<id>.*`; routes must reference a declared permission and exact scope. Wildcards,
  `..`, `%`, backslashes, query/fragment characters and empty segments are rejected.
- Declared event subscriptions require `core.events.subscribe`; only registered core events are accepted.
- OCI startup is refused when the trusted runner is not configured. There is no fake OCI success path.

## 3. Lifecycle

`internal/plugins.Manager` persists desired state in `plugins` and keeps observed health separately.

- `Install` → `installed`; no backend process.
- `Enable` → provisions the private schema, runs pending plugin migrations, rotates the service token, starts the
  isolated backend, waits for `/v1/health`, then sets `enabled`.
- `Disable` → stops the backend, cancels event consumers, revokes live delegations, sets `disabled`; package and private
  schema data are retained.
- Crash / unhealthy → the route proxy returns a controlled `503`; core stays up. A health monitor from Get/proxy marks
  the plugin `unhealthy`.
- `Upgrade` → only a greater version; rejects added/removed permissions or added capabilities. Migrations run in one
  transaction over the restricted role **before** traffic switches. A failing migration leaves the previous package
  active and reports that plugin-data rollback is not promised.
- `Remove` → deletes the registry/package rows, stops routes, retains the private plugin schema/data.

Migrations are recorded in `plugin_migrations`. Core never runs plugin SQL as the core Postgres role.

## 4. Private plugin database

`ManagerOptions.PluginDB.ProvisionDSN` is an operator/manager provisioning connection. It is used only for:

- `CREATE ROLE`/`ALTER ROLE` with `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT`;
- `CREATE SCHEMA plugin_<id> AUTHORIZATION plugin_<id>`;
- `REVOKE ALL ON SCHEMA public FROM plugin_<id>` and `search_path=plugin_<id>`.

Every plugin SQL migration and every backend connection uses the restricted login role (separate DSN). The role cannot
drop core tables, read core tables, create objects in `public`, or `SET ROLE` to the core role. The private DSN and
service token are stored encrypted in `plugin_settings` with domain-separated AES-256-GCM AAD; raw tokens are only in
memory/runner env.

## 5. Protocol (`http-v1`)

Core → plugin (Unix socket preferred, or loopback-only TCP):

```text
POST {backend}/v1/invoke
Headers: X-Actfb-Protocol: 1, X-Actfb-Plugin-Id, X-Actfb-Request-Id,
         X-Actfb-Service-Token, X-Actfb-Delegation (when an end user acts)
Body: {"protocol":"1","requestId":"uuid","operation":"api|page|event|health",
       "pluginId":"vendor.name","userUuid":"uuid|null","scope":{"boardUuid":...,"moduleUuid":...},
       "method":"GET","path":"/actual/path","query":{},"headers":{},"bodyBase64":""}
Reply: {"protocol":"1","requestId":"uuid","status":200,"headers":{},"bodyBase64":""}
```

Core enforces a 1 MiB request/response cap, no redirects, no proxy, no userinfo, loopback-only TCP and Unix sockets
created with `0600`. Timeout is manifest 1–30 s (default 5 s). Plugin service tokens are random per plugin, stored
hashed/encrypted, rotated on enable and never sent to a browser.

Plugin → core SDK (`/api/v2/plugin-sdk/v1/{authorize,board-context,events}`):

```text
Authorization: Plugin <service-token>
X-Actfb-Delegation: <core-issued HMAC delegation>
```

The delegation is tied to plugin id + actual end user + board/module scope + nonce + short expiry. The nonce is consumed
atomically on first use; replay, expiry, revocation, tampering or a plugin mismatch is rejected. A body field such as
`userUuid`, `scope` or capability list is rejected as an unknown field and never trusted. `CallContext.UserID`/`Scope` come
only from the verified delegation. Plugin permissions additionally require a live explicit `permission_grants` row,
even for a global admin; core `plugins.manage` remains a core admin permission.

## 6. Permissions and grants

Manifest permissions are registered only by the trusted manager after validation and recorded in `plugin_permissions`.
Registration grants nothing. The central `Authorize` implementation has a plugin-source branch that requires:

1. a live explicit grant from `GrantStore.HasGrant`;
2. the plugin row `status='enabled'`;
3. a scope supported by the registered definition.

The plugin proxy repeats the explicit-grant guard locally before forwarding. Grant CRUD in the plugin HTTP adapter is
limited to that plugin's `plugin.<id>.` namespace and declared scopes; it delegates writes to `authorization.GrantStore`
and never edits the core role matrix. Grant revocation is live.

Participant visibility (`GET /api/v2/plugins`) lists only enabled, API-compatible plugins with at least one active grant
for a UI route permission of the current user. Admin role alone does not make plugin pages visible.

## 7. Namespaced API proxy

`GET|POST|PUT|PATCH|DELETE /api/v2/plugins/{id}/...` only matches routes declared in the manifest:
method, exact path with `:param` segments, scope and permission. It rejects percent-encoded traversal, `..`, `//`,
backslashes, NUL, wildcard routes, oversized bodies and undeclared methods. Board/module UUIDs are taken from the actual
path and validated by the parent authorizer and live grant; `X-Actfb-BoardUuid`/`ModuleUuid` headers from the frontend
are consistency hints only and must agree with the path.

Response policy:

- `Set-Cookie`, `Location`, CORS/hop headers and plugin CSP are stripped.
- JSON/`*+json` and `text/plain` are allowed. HTML/JS/SVG on the core origin is rejected.
- Any other body must be an attachment and is served as `application/octet-stream` with a sanitized filename.
- `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, `Referrer-Policy: no-referrer` and a
  `default-src 'none'; frame-ancestors 'none'; sandbox` CSP are set by core.

Plugin HTML/JS is never served by the API/core origin.

## 8. Events

`core.events.subscribe` plus declared `events` create one durable JetStream pull consumer per plugin/event in
`ACTFB_EVENTS`. Delivery goes to `POST /v1/events` with `X-Actfb-Event-Id` and an HMAC signature. Core records one row
per `(plugin_id,event_id)` in `plugin_event_deliveries`; retries are exponential, the fifth failed attempt becomes
`dead` (DLQ), and an explicit audited
`POST /admin/plugins/{id}/event-deliveries/{eventId}/replay` re-reads the core outbox and redelivers. Plugins must still
dedupe by `event.id`; the example score plugin uses a primary-key insert and does not double-count.

Plugins may publish only registered `plugin.<id>.*` events through the SDK. `submission.judged` and other core topics,
score/verdict mutation and arbitrary subject identities are rejected.

## 8.1 Plugin-backed task checkers (`EXTENSION` / `PLUGIN`)

A plugin may declare a **text-only** checker operation:

```yaml
permissions:
  - id: plugin.example.custom_checker.check
    scope: board
routes:
  - method: POST
    path: /boards/:boardUuid/check
    permission: plugin.example.custom_checker.check
    scope: board
checkers:
  - key: plugin.example.custom_checker.text
    extensionKey: plugin.example.custom_checker.extension
    route: /boards/:boardUuid/check
    permission: plugin.example.custom_checker.check
    scope: board
    timeoutSeconds: 1
    maxTextBytes: 1000000
```

`EXTENSION` modules require `extensionKey`; their tasks use `type=EXTENSION`,
`submission.checker=PLUGIN`, `extensionKey` and `submission.checkerKey`. Task and module configuration validates that:

- the plugin is installed, enabled and API-compatible;
- the checker/extension key is declared exactly once by an enabled plugin;
- the checker route is a declared `POST` route with the same board permission/scope;
- the acting global admin has a **live explicit grant** for that plugin permission (no auto-grant, even for admins).

Execution is core-owned and text-only. Core invokes the versioned HTTP operation
`operation=checker` with `POST /boards/<boardUuid>/check`, the actual end-user delegation, bounded text and
`maxPoints`. It never sends FLAG plaintext, accepted-answer hashes, file bytes, provider credentials or student code.
The plugin returns only `ACCEPTED`/`REJECTED`, an optional message and an optional advisory score. Core rejects
out-of-range/malformed responses, timeouts, disabled/crashed providers, missing grants and unknown keys. On
`ACCEPTED`, core awards the **frozen task points** from the authoritative task row; the plugin score is ignored.
The canonical submission, `submission.judged` outbox event, audit row and single-accepted/idempotency constraints are
written by core in the existing submission transaction. Revoking the checker permission or disabling the plugin makes
existing checker tasks fail closed with no new score. `examples/plugins/example.custom_checker` proves the operation
through a separately built binary; it does not execute student code.

## 9. Frontend integration contract (opaque iframe)

Core returns `Visible[].IframeURL` as an absolute URL:

```text
{ACTFB_PLUGIN_IFRAME_ORIGIN}/plugins/{pluginId}/assets/{frontend.entry}
```

The URL never contains a service token or delegation. The frontend owner appends a per-frame CSPRNG channel nonce in
the **fragment** (not sent to the asset server/logs), e.g.
`#actfbChannel=<nonce>&pluginId=<id>`. The host validates the initial iframe `src` origin against the configured
separate asset origin, uses `sandbox="allow-scripts allow-forms"` without `allow-same-origin`, and therefore expects
`event.origin === "null"`. Every child message must match `event.source === iframe.contentWindow`, the plugin id, the
frame nonce and a bounded request id.

Messages used by the examples:

```text
child  -> parent  actfb.plugin.ready   {}
parent -> child   actfb.plugin.context {theme,currentBoardUuid,currentModuleUuid,currentTaskUuid,pagePath}
child  -> parent  actfb.plugin.api     {method,path,body}
parent -> child   actfb.plugin.result  {status,body,errorCode}
child  -> parent  actfb.plugin.navigate{path}   (relative current-board/own-plugin paths only)
```

Parent CSP must restrict `frame-src` to the configured asset origin. The asset server sends
`default-src 'self'; connect-src 'none'; object-src 'none'; frame-ancestors <platform-origin>` and refuses unknown
MIME types/traversal. Example pages use only the bridge and never call core directly with browser credentials.

`trusted-js` remains an explicitly dangerous same-origin mode and is not a sandbox. Core implements manifest
validation, the operator-wide `ACTFB_PLUGIN_ALLOW_TRUSTED_JS` gate (default false), per-install/upgrade approval and
migration `014` persistence. The browser fetches the exact enabled bundle from an authenticated platform-origin route,
verifies the advertised `sha256:<hex>` before import, and mounts the self-contained ESM into a dedicated full-trust
host. It receives only pluginId/theme/context props, never JWT, refresh cookies, service tokens or provider secrets.
Production CSP must allow the verified blob module import (`script-src` includes `blob:`); the normal iframe path does not.

## 10. OCI plugin runner (trusted sidecar)

`cmd/plugin-runner` is a separate trusted process. It exposes a tiny loopback control API (`start/stop/status/logs`)
authenticated by `ACTFB_PLUGIN_RUNNER_TOKEN`. It is the only process allowed to talk to the Docker socket.

`process.DockerRunner` starts pinned images with: read-only rootfs, `CapDrop: ALL`, `no-new-privileges`, no host
mounts/socket, separate named data volume at `/data`, tmpfs `/tmp`, memory/CPU/PID limits, and a loopback-only
published port. The main backend never opens the Docker socket; `ManagerOptions.Runner` must be a remote runner.

If no runner is configured, an OCI enable fails honestly. This environment has no OCI image/provider credential, so the
verified MVP acceptance uses separately built `exec` examples.

## 11. Operational configuration

Existing config/env fields (already present in `internal/config`):

| Variant | Meaning |
|---|---|
| `ACTFB_PLUGINS_ENABLED` | enable plugin worker wiring |
| `ACTFB_PLUGINS_DIR` | install root, default `./plugins` |
| `ACTFB_PLUGIN_SERVICE_KEY` | base64 32-byte HMAC key for service tokens/delegations/event signatures |
| `ACTFB_PLUGIN_IFRAME_ORIGIN` | public asset/iframe origin, must differ from platform origin (e.g. `http://127.0.0.2:8091` vs `http://localhost:8080`) |
| `ACTFB_PLUGIN_ASSET_ADDR` | asset listener address, must match the origin host/port |
| `ACTFB_PLUGIN_HTTP_TIMEOUT` | 1–30 s, default 5 s |
| `ACTFB_PLUGIN_MAX_BODY_BYTES` | ≤1 MiB |
| `ACTFB_PLUGIN_ALLOW_EXEC` | explicit operator permission for local executables |
| `ACTFB_PLUGIN_RUNNER_ADDR` / `ACTFB_PLUGIN_RUNNER_TOKEN` | plugin-runner loopback control |
| `ACTFB_DOCKER_SOCKET` | Docker API socket, read only by plugin-runner |

`ManagerOptions` additionally requires explicit `DB`, `Sealer`, `Permissions`, `Authorizer`, `GrantStore`, `ServiceKey`,
`Root`, `AssetOrigin/IframeOrigin` and optionally `PluginDB`, `NATS`, `CoreSDKURL`, `Runner`, `PlatformOrigin`.
Construct the `Authorizer` and `GrantStore` with the **same** `authorization.PermissionRegistry` instance; otherwise
plugin grant validation and proxy authorization see different catalogs. Boot must also pass the decoded 32-byte service
key and the same secrets sealer used for other app secrets.

## 12. Exact public exports added

- `plugins`: frozen `Manifest`, `Installed`, `InstallRequest`, `Visible`, `Validation`, `Finding`, `Identity`,
  `Delegation`, `CallContext`, `BoardContext`, `CoreProxy`, `Service`, `ProxyRequest/Response`;
  `NewManager(ManagerOptions)`, `NewService(ManagerOptions)`, lifecycle/service methods, grant/event admin methods,
  `RecoverEnabled`, `NewCoreProxy`, `VerifyDelegation`, `VerifyServiceToken`, `Issuer`, `ResolveAsset`,
  `AssetServerOrigins`.
- `plugins/protocol`: `Client`, `ClientOptions`, `NewClient`, `Invoke`, `InvokeWithDelegation`, `DeliverEvent`, `Health`,
  `SignEvent`, `VerifyEventSignature`, `NewServiceToken`, `HashServiceToken`, `VerifyServiceToken`, envelope types and
  `OperationChecker`.
- `plugins`: `ValidateChecker`, `CheckChecker` and the `CheckerDecl`/`CheckerRequest`/`CheckerResult` contract.
- `plugin-checker` glue: `boards.PluginCheckerConfigValidator` + `cmd/backend/plugin_checker.go` adapter.
- `plugins/process`: `Runner`, `Spec`, `Handle`, `ExecRunner`, `DockerRunner`, `Supervisor`, `RemoteRunner`,
  `NewDockerRunner`, `NewSupervisor`, `NewRemoteRunner`.
- `plugins/assets`: `NewServer`, `Server.ListenAndServe/Shutdown/ServeHTTP`.
- `plugins/sdk`: `NewServer`, `PathAuthorize/PathBoardContext/PathEvents`.
- `plugins/http`: `NewHandler(manager, authenticator).RegisterRoutes(*echo.Echo)`.
- `plugins` extension contracts: `ExtensionAPIVersion`, `ExtensionType`, `ExtensionOperation`, `ExtensionDefinition`,
  `ExtensionRegistry`, `NewExtensionRegistry`, and typed `CheckerV1`, `RuntimeDriverV1`, `ModuleProviderV1`,
  `IntegrationProviderV1` interfaces with normalized non-secret request/result types.
- `cmd/plugin-runner`: trusted OCI supervisor binary.

## 13. Test evidence and blockers

Verified locally with real PostgreSQL 18 and NATS JetStream:

```bash
go test ./internal/plugins/... ./cmd/plugin-runner
ACTFB_TEST_POSTGRES_DSN='postgres://.../actfb?sslmode=disable' \
  ACTFB_TEST_NATS_URL='nats://127.0.0.1:14222' \
  go test ./internal/plugins -count=1 -v
```

Covered: strict manifest/zip traversal/symlink/checksum rejection; install→enabled separation and HTTP `enable=true`
rejection; lifecycle install/enable/disable/upgrade/remove; data retention; crash→503/core-still-answering;
migration-before-traffic and failed-upgrade rollback; private restricted role cannot drop core tables/create in public/
`SET ROLE`; explicit grants for admins; live revocation; grant namespace/scope rejection; delegation tamper/missing/
wrong-token/replayed nonce; SDK body identity injection; visible metadata filtering; iframe URL has no token; route
traversal/hints; response header/content-type stripping; real JetStream delivery/replay dedupe/DLQ; OCI refusal without
trusted runner; plugin checker install→EXTENSION module/task→real UDS checker HTTP→canonical submission/score-once,
idempotency, grant revoke/disable/crash/timeout/malicious-score fail-closed (`docs/plugin-checker-verification.md`).

Blockers/limits:

- `cmd/backend` wires `plugins.NewManager`, `pluginhttp.NewHandler`, `sdk.NewServer(manager.NewCoreProxy())`, the
  assets server and `boards.WithPluginChecker`. The periodic `RetryDueDeliveries`/`RecoverEnabled` call remains an
  integration-owner boot decision. The Vue iframe shell is still frontend-owned.
- No live OCI image/registry credential or real plugin-runner Docker acceptance was available; only OCI policy/refusal
  and Docker runner construction are covered. Do not claim external OCI production acceptance.
- `trusted-js` browser mounting is implemented as an explicit full-trust host behind the operator gate plus
  per-install approval and SHA-256 verification; iframe mode remains the default safe UI for ordinary addons.
- Manifest `Dependencies` is implemented with semantic ranges, cycle detection and missing/disabled/incompatible guards
  across install, enable, upgrade, disable and remove.
- Upgrade cannot add/remove permissions or add capabilities; use explicit remove/reinstall with approval.
- Plugin event signatures use the configured core service key; the plugin runtime env allowlist intentionally contains
  no extra event key. Plugins must rely on the private socket/transport and dedupe by event id.
- The nested example modules have their own `go.mod`/`go.sum` and are not part of `go test ./...`; build/install them
  separately as documented.