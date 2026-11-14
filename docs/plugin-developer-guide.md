# Plugin developer guide

This guide describes the **P5 installable addon contract**. A plugin is a separately built process plus static assets
and SQL migrations. It is not imported into aCTFb, not compiled with the core and not loaded with `plugin.Open`.

## 1. Package layout

```text
my-plugin/
  manifest.yaml
  bin/plugin                 # backend.mode=exec; built separately
  assets/index.html          # iframe UI entry
  assets/app.js
  migrations/001_init.sql    # optional
```

Ship the directory or a `.zip` below `ACTFB_PLUGINS_DIR/incoming`. Zip entries must be relative regular files; symlinks,
`..`, absolute paths, duplicate names and encrypted entries are rejected.

Build the backend separately from core, for example:

```bash
cd examples/plugins/example.hello
go build -o bin/plugin .
```

## 2. Manifest v1

```yaml
id: example.hello                 # lowercase vendor.name; stable
name: Example Hello
version: 1.2.0                    # semantic version
apiVersion: "1"                   # only Plugin API v1
description: Optional, max 20 KiB
author: Team
backend:
  mode: exec                      # exec | oci
  entrypoint: bin/plugin          # exec only, relative, executable
  # image: registry.example/plugin@sha256:<64 hex>   # oci, pinned digest only
  protocol: http-v1               # only http-v1
  healthPath: /v1/health
  timeoutSeconds: 5               # 1..30, default 5
frontend:
  mode: iframe                    # iframe is the default safe UI
  entry: index.html               # inside assets
# dependencies are resolved from installed plugins; install never enables them
dependencies:
  - id: example.base
    version: "^1.0.0"
permissions:
  - id: plugin.example.hello.view
    scope: board                  # global | board | module
  - id: plugin.example.hello.manage
    scope: board
capabilities:
  - core.boards.read
  - core.events.subscribe
events:
  - submission.judged             # documented core events only
routes:
  - method: GET
    path: /boards/:boardUuid/hello
    permission: plugin.example.hello.view
    scope: board
    ui: true
  - method: POST
    path: /boards/:boardUuid/hello/messages
    permission: plugin.example.hello.view
    scope: board
assets: assets
migrations: migrations
# checksum: sha256:<hex>          # content excluding manifest.yaml
```

Rules:

- Unknown YAML keys are rejected.
- Dependency ranges use semver (`^`, `~`, comparisons, AND/OR); missing/disabled/incompatible
  dependencies block enable, cycles are rejected, and install never auto-enables anything.
- `frontend.mode: trusted-js` is full-trust platform-origin code, not a sandbox. It requires the
  operator gate plus explicit per-install/upgrade approval; the bundle must be a self-contained ESM
  and is SHA-256-verified by the host before import.
- Plugin permissions must start with `plugin.<id>.`; scopes must be declared.
- Each route's permission must be declared, scopes must match, and wildcard/encoded/dot-segment paths are rejected.
- `apiVersion` must be exactly `"1"`.
- `exec` requires operator `ACTFB_PLUGIN_ALLOW_EXEC=true`. Prefer OCI for production.
- OCI `image` must be `repository@sha256:<64 hex>`.
- Static assets ≤5 MiB/file; package files ≤64 MiB; total uncompressed package ≤256 MiB; zip archive ≤32 MiB.

## 2.1 Plugin checkers (EXTENSION tasks)

A plugin checker must be **text-only** and declare a versioned operation:

```yaml
permissions:
  - id: plugin.example.custom_checker.check
    scope: board
routes:
  - method: POST
    path: /boards/:boardUuid/check
    permission: plugin.example.custom_checker.check
    scope: board
capabilities:
  - core.boards.read
checkers:
  - key: plugin.example.custom_checker.text
    extensionKey: plugin.example.custom_checker.extension
    route: /boards/:boardUuid/check
    permission: plugin.example.custom_checker.check
    scope: board
    timeoutSeconds: 1
    maxTextBytes: 1000000
```

Create an `EXTENSION` module with `extensionKey`, then an `EXTENSION` task with the same extension key and
`submission.mode=TEXT`, `submission.checker=PLUGIN`, `submission.checkerKey=<checker key>`. The acting admin must have an
explicit live grant for the checker permission; being admin is not enough. Core calls the plugin with
`operation=checker`, a short-lived end-user delegation, bounded text and `maxPoints`. The plugin must not expect flags,
accepted hashes, files, credentials or student code, and must not execute student code. It returns:

```json
{"verdict":"ACCEPTED","score":0,"message":"optional"}
```

Only `ACCEPTED`/`REJECTED` are accepted. Core ignores `score` and awards the task's frozen points; out-of-range scores,
unknown verdicts, timeouts, disabled providers, missing grants and malformed responses fail closed without a scored
submission. The canonical submission/outbox/audit/idempotency transaction remains core-owned. Example:
`examples/plugins/example.custom_checker`.

## 3. Backend transport (`http-v1`)

Listen on the Unix socket provided in `ACTFB_PLUGIN_SOCKET` (preferred) or a loopback address (explicitly configured).
Never bind a public interface. OCI backends must listen on container port `8080/tcp`; the trusted runner publishes it on
loopback only. Required endpoints:

```text
GET  /v1/health   -> {"status":"ok"}
POST /v1/invoke   -> core→plugin request/reply envelope (api|page|event|health|checker)
POST /v1/events   -> signed core event delivery
```

Invoke request body:

```json
{
  "protocol": "1",
  "requestId": "uuid",
  "operation": "api",
  "pluginId": "example.hello",
  "userUuid": "uuid-or-null",
  "scope": {"boardUuid": "uuid-or-null", "moduleUuid": null},
  "method": "GET",
  "path": "/boards/<actual-board>/hello",
  "query": {},
  "headers": {"Accept": "application/json"},
  "bodyBase64": ""
}
```

Reply:

```json
{"protocol":"1","requestId":"same-uuid","status":200,
 "headers":{"Content-Type":"application/json"},"bodyBase64":"..."}
```

The core enforces a 1 MiB cap, no redirects, no proxies, loopback-only TCP and a 1–30 s timeout. Requests include
`X-Actfb-Service-Token`; verify it in constant time. Do not log it, persist it or send it to the browser. A core request
that acts for a user also carries `X-Actfb-Delegation`; forward that header only to the core SDK.

## 4. Plugin→core SDK

Every SDK call needs both `Authorization: Plugin <service-token>` and `X-Actfb-Delegation: <token>`:

```text
POST /api/v2/plugin-sdk/v1/authorize
     {"permission":"plugin.example.hello.view","boardUuid":"...","moduleUuid":null}
     -> {"allowed":true,"reason":"allowed"}

POST /api/v2/plugin-sdk/v1/board-context
     {"boardUuid":"...","moduleUuid":null}
     -> {"boardUuid":"...","subjectType":"USER","subjectUuid":"...","permissions":[...]}

POST /api/v2/plugin-sdk/v1/events
     {"event":{"id":"uuid","type":"plugin.example.hello.ping","boardId":"...",
               "subjectType":"USER","subjectId":"...","payload":{}}}
```

The delegation is request-scoped and **single use**. Do not put it in a cache, URL, local storage or log. The core
derives the user and scope from it; sending `userUuid`, `scope` or a capability list in the SDK body is rejected. An SDK
`authorize` call succeeds only when the admin has explicitly granted that permission, even if the user is a global
admin. Grants can be revoked at any time and take effect immediately.

Plugins cannot publish `submission.judged`, other core topics, scores or verdicts. The only publishable events are
registered `plugin.<plugin-id>.*` types, and the event board/subject must match the delegation.

## 5. Private PostgreSQL schema

If `migrations/` is declared, the manager provisions `plugin_<sanitized-id>` and a restricted login role. The backend
receives:

```text
ACTFB_PLUGIN_DSN = postgres://plugin_<id>:<random>@host/db?search_path=plugin_<id>
ACTFB_PLUGIN_SCHEMA = plugin_<id>
```

The role has no access to `public` and core tables, cannot create in `public`, and cannot `SET ROLE`. Migrations are
executed by the manager over this role in one transaction before requests are served; migration files must be
transactional (no `COMMIT`/`ROLLBACK`/`SET ROLE`, no `CREATE INDEX CONCURRENTLY`). Never connect with the core
Postgres role and never try to write core scoring tables. On disable/remove the schema and data are retained.

Store plugin settings in your own schema. Never store the service token, delegation, browser JWT, provider credentials or
raw user data in HTML/metadata.

## 6. Routes and permissions

Declared routes are exposed under `/api/v2/plugins/<id>/...`. Only the declared method and exact path match; the core
checks the route scope and the live explicit grant before forwarding. Board/module UUIDs come from the actual path and
are validated for parent ownership; a route for board A cannot be used with a grant for board B. The frontend may send
`X-Actfb-BoardUuid`/`X-Actfb-ModuleUuid`; these are consistency hints and a mismatch is rejected.

Responses must be JSON, `*+json`, `text/plain`, or an attachment. The core strips `Set-Cookie`, `Location` and plugin
CSP; it never serves plugin HTML/JS/SVG from the API origin. Put those only in `assets/`.

## 7. Iframe bridge

Plugin UI is served from a **separate asset origin**, for example `http://127.0.0.2:8091`, while the platform may be
`http://localhost:8080`. The host appends a per-frame CSPRNG nonce in the URL fragment:

```text
http://127.0.0.2:8091/plugins/example.hello/assets/index.html#actfbChannel=<nonce>&pluginId=example.hello
```

The fragment is not sent to the asset server/logs. The iframe uses `sandbox="allow-scripts allow-forms"` without
`allow-same-origin`; therefore `event.origin === "null"`. Your page must:

1. Read `actfbChannel` and `pluginId` from `location.hash`.
2. Post `actfb.plugin.ready` with `{protocol:"1",pluginId,channelNonce,requestId,type,payload}`.
3. Accept only messages where `event.source === window.parent`, `event.origin === "null"`, `protocol==="1"`,
   `pluginId` and `channelNonce` match, and `requestId` is a bounded UUID/string.
4. Use `actfb.plugin.api {method,path,body}` for API calls. The parent forwards only this plugin's namespace and returns
   `actfb.plugin.result {status,body,errorCode}` with the same request id.
5. Navigate only to relative paths in the current board or this plugin. Never open a token-bearing URL.
6. Never call core directly with fetch/XHR; the browser must not hold a JWT/service token for a plugin.

Example code is in `examples/plugins/*/assets/index.html`. The page receives context as
`actfb.plugin.context` (`theme`, current board/module/task, page path) and must not request other boards.

## 8. Events

Declare `core.events.subscribe` and the exact core event names. Core creates a durable JetStream consumer per
plugin/event and POSTs `/v1/events` with `X-Actfb-Event-Id` and `X-Actfb-Signature`. Acknowledge quickly with 2xx.
Retries are at-least-once; after the retry budget the delivery is `dead`/DLQ and an audited admin replay can resend it.
Always dedupe by `event.id` (primary key/unique constraint) and keep only minimal derived data. An event can never
change a core verdict or score; the plugin may only update its own private tables.

## 9. Build, validate and install locally

1. Build the plugin backend separately:

   ```bash
   cd examples/plugins/example.hello && go build -o bin/plugin .
   cd examples/plugins/example.score_events && go build -o bin/plugin .
   cd examples/plugins/example.custom_checker && go build -o bin/plugin .
   ```

2. Copy the package directory (with `manifest.yaml`, binary, assets, migrations) under
   `<ACTFB_PLUGINS_DIR>/incoming/<package-name>`.
3. If the plugin uses the core SDK, set `ACTFB_PLUGIN_CORE_SDK_URL` to the mounted
   `/api/v2/plugin-sdk/v1` endpoint before enabling it. The backend now registers the SDK service-token/delegation
   endpoints; they are never browser routes.

4. As a global `plugins.manage` administrator:

   ```http
   POST /api/v2/admin/plugins/validate
   {"packagePath":"example.hello"}

   POST /api/v2/admin/plugins/install
   {"packagePath":"example.hello",
    "approveCapabilities":["core.boards.read"],
    "approvePermissions":["plugin.example.hello.view","plugin.example.hello.manage"]}

   POST /api/v2/admin/plugins/example.hello/enable
   ```

   `install` never enables. `enable=true` in install is rejected. Grant the plugin permission explicitly to a user for a
   board; being an admin is not enough:

   ```http
   POST /api/v2/admin/plugins/example.hello/permission-grants
   {"userUuid":"...","permission":"plugin.example.hello.view","boardUuid":"..."}
   ```

5. The core binary and Vue bundle are not rebuilt. The new route/UI appears after enable if the user has a grant.

Disable:

```http
POST /api/v2/admin/plugins/example.hello/disable
```

Routes return controlled `503` while disabled; private data remains. Re-enable keeps the data. Upgrade uses
`POST /api/v2/admin/plugins/{id}/upgrade` with a package containing a greater version. Upgrades cannot add/remove
permissions or add capabilities; do an explicit remove/reinstall for that.

## 10. Acceptance checklist for an addon

- Manifest/package validates with no code execution.
- Install leaves `installed`; enable is explicit and requires approvals.
- UI appears without Go/Vue rebuild; page uses only the bridge and asset origin.
- No grant → direct URL and SDK calls are denied; grant for board A cannot read board B; revoke is instant.
- Disable closes routes, keeps data; re-enable restores it.
- Crash returns `503`, core stays healthy; logs are bounded and token-redacted.
- Migrations run before traffic; a failed upgrade leaves the previous version serving.
- Event replay does not duplicate rows.
- Plugin checker tasks accept only bounded text; core awards frozen task points, ignores the plugin score and fails
  closed on timeout/revocation/disable/unknown key.
- HTML/JS/SVG cannot be served from the core API origin; no token is present in iframe metadata/URLs.
- Private schema role cannot drop core tables, create in `public`, or `SET ROLE`.