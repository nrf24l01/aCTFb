# Runtime, instances and integrations — handoff

Date: 2026-11-13. Worker lane: `internal/runtime/**`, `internal/instances/**`,
`internal/integrations/**`, `cmd/instance-worker/**`. Schema `009` + `012` and
`docs/swagger.yaml` were **not** edited by this lane.

Package direction (frozen, no cycles):

```text
internal/deployment  <-  internal/runtime  <-  internal/instances  <-  cmd/*
        ^                                            |
        +----------- internal/integrations  <---------+
```

`internal/deployment` does not import `runtime`; `runtime` imports `deployment`;
`instances`/`integrations` import both. `internal/attackdefense` is not imported.

## 1. Stable Go interfaces for boot/UI workers

`internal/runtime/driver.go`:

- `Driver`, `Registry`, `Operation`, `Target`, `ProviderResource`,
  `ProviderEndpoint`, `Plan`, `NodeState` — exactly the frozen contract.
- `OperationValidator`-style extensions: `OptionValidator`, `SettingsValidator`,
  `StaticMetadata`, `Probe`.
- `Target.Settings` is the **trusted** envelope `TargetSettings`:
  `{options, allocation, material}`. `options` is the integration's typed block,
  `allocation` is the core-issued network claim + public ports, `material` is the
  rendered env/files/cloud-init. Unknown envelope fields are rejected.
- Labels: `actfb.managed`, `actfb.instance`, `actfb.node`, `actfb.generation`,
  `actfb.operation`, `actfb.driver`. Adoption requires `managed=true` plus the
  exact instance/node; a stale generation is deleted, a future generation aborts.

`internal/runtime/registry.go` implements both deployment hooks:

- `deployment.DriverMetadataProvider` (`ListDriverMetadata`,
  `ValidateDriverOptions`) using **static** capabilities — preview/diff never
  provisions or connects.
- `ValidateSettings(driver, raw)` for integration settings.

Boot wiring:

```go
registry, err := runtime.NewRegistry(docker.New(), podman.New(),
    kubernetes.New(), proxmox.New(), kubevirt.New())

deploymentSvc, err := deployment.NewService(db, authz, sealer,
    deployment.WithDriverMetadataProvider(registry),
    deployment.WithReadPermissionProvider(instancesReadProvider))
```

`instances.NewService(db, authz, registry, sealer, keyVersion, opts...)` plus
`instances.NewOrchestrator(db, registry, authz, sealer, keyVersion, opts...)`.
`instances.NewNATSJobPublisher(natsClient)` for `POST instance` jobs.
`instances/http.NewHandler(service, auth)` and
`integrations/http.NewHandler(service, auth)` expose `RegisterRoutes(*echo.Echo)`.

`integrations.NewService(db, authz, opts...)` with `WithSealer`,
`WithURLPolicy`, `WithConnectionTester(integrations.NewNATSTester(natsClient))`,
`WithRuntime(registry)`.

## 2. Registered drivers and honest status

| Driver | Kind | Implementation | Evidence |
|---|---|---|---|
| `docker` | CONTAINER | real Docker Engine API over unix socket | **passed against the local Docker 29.9 daemon**: create, endpoint, adopt-on-retry, reset-clean marker, destroy, lease release |
| `podman` | CONTAINER | real Docker-compatible Engine API, **own** socket only | unit/contract only; rootless publication is **not** claimed (`PublicExposure=false`) |
| `kubernetes` | CONTAINER | real REST Pod/Service/NetworkPolicy | httptest contract + security shape; **real cluster acceptance BLOCKED** |
| `proxmox` | VM | real REST `api2/json` clone/UPID/inspect/start/destroy/agent | httptest contract; **real endpoint acceptance BLOCKED** |
| `kubevirt` | VM | real REST VM/DV/PVC/Service/NetworkPolicy | httptest contract; **real cluster acceptance BLOCKED** |

Deterministic unsupported behaviour: a missing/foreign socket or an unsupported
capability returns `*runtime.CapabilityError` (unwraps to `ErrUnsupported`) or a
sanitized `*runtime.ProviderError`; there is no fake success and no Docker↔Podman
fallback. `podman` refuses PUBLIC endpoints. `proxmox` refuses PUBLIC exposure,
cloud-init and file injection. `kubevirt` refuses file injection. The Kubernetes
driver refuses untrusted workloads until `podPidsLimitVerified=true` (kubelet
`PodPidsLimit` is a node-level setting); Docker/Podman refuse Apply until
`dedicatedNode` + `dedicatedNodeVerified=true` (a localhost daemon shared with
the backend is **not** adequate for untrusted Pentest isolation).

Provider option/settings schemas are closed structs decoded with
`DisallowUnknownFields`. No raw `driverOptions`/settings JSON is forwarded.

### Container security model

- one dedicated `internal` network per instance (`actfb-<instance>`); no
  `host`/`bridge`/default network, no `--privileged`, no host bind mounts, no
  devices, `CapDrop=ALL`, `no-new-privileges`, read-only root with sized tmpfs.
- CPU/memory/PIDs are hard limits; disk is the tmpfs budget on the read-only
  root. Time is the instance TTL (core-owned).
- files are injected through a labelled platform **volume** and read-only
  volume-subpath mounts; a helper container runs only `/bin/sh -c sleep` from
  the digest-pinned workload image. Host paths are never bind-mounted.
- source images must be digest-pinned (`repo@sha256:…`) and registry-allowlisted.
- public endpoints are not published by Docker directly (that would require a
  non-internal network and thus egress). The worker runs a bounded TCP forwarder
  from the core-allocated host:port to the container IP:target port. The listener
  is closed only after provider destroy is confirmed; a worker restart
  re-establishes it during `Inspect`. This is a worker-host listener, not a host
  firewall change.

### Kubernetes / KubeVirt

- dedicated namespace (never `default`/`kube-system`), explicit HTTPS + bearer
  token, CA bundle, no insecure TLS, no redirects.
- Pod: no host namespaces, `automountServiceAccountToken=false`, no hostPath,
  runAsNonRoot, seccomp RuntimeDefault, drop ALL, resource requests=limits
  (CPU/memory/ephemeral-storage), same-instance-only ingress plus the published
  target ports, `Egress: []` (default-deny). Secrets carry rendered inputs.
- Service is `NodePort`; the publication port range must be inside
  `30000..32767`.
- KubeVirt clones an administrator-approved source PVC via CDI
  `dataVolumeTemplates`; bootstrapping uses `cloudInitNoCloud.secretRef`; the
  same egress-deny NetworkPolicy and NodePort Service apply. Native names/PVCs
  never come from a template author (`vmImageMap` is integration settings).

## 2a. Deployment allowlists and the binding API (this lane)

The three documented allowlist operations are served from
`instances/http` and require **global** `platform.deployment_templates.publish`:

```text
GET    /api/v2/admin/deployment-templates/{templateUuid}/allowlist
POST   /api/v2/admin/deployment-templates/{templateUuid}/allowlist
DELETE /api/v2/admin/deployment-templates/{templateUuid}/allowlist/{allowlistUuid}
```

- only an exact `deployment_template_versions` row of that template can be
  allowlisted; active duplicates conflict; revocation is template-scoped and
  idempotent;
- optional `boardUuid`/`moduleUuid` query scopes enumeration; the response is
  only ids/kind/policy, never integration URLs, credentials or package files;
- `parametersPolicy` is opaque non-secret JSON, bounded and rejected when it
  contains secret-looking keys. It is stored/returned but not interpreted until
  a policy schema is approved; binding parameters are currently validated only
  against the immutable manifest declaration (types/choices/required/secret).
- These three routes are registered by the instances handler. If the deployment
  template owner later also registers them, boot must not mount both handlers.

Binding API for the board/backend owner (no boards/schema edits needed):

```go
result, err := instancesService.BindInstance(ctx, actor, instances.BindInput{
    BoardID: b, ModuleID: m, TaskID: &task, Scope: instances.ScopeTask,
    TemplateID: template, TemplateVersion: version,
    Parameters: map[string]string{...}, SecretParameters: map[string]string{...},
})
summary, err := instancesService.GetBinding(ctx, actor, b, m, &task)
```

- requires `deployment_templates.use` (or `module.instances.admin`);
- requires an active `012` allowlist for the **exact** immutable version and the
  module/board scope, and copies only its exact targets (ambiguous target kinds
  are rejected);
- parameters are validated against the immutable manifest; secret parameters
  are sealed with `instances.BindingSecretAAD(bindingID)` and never returned by
  `GetBinding`, lists, logs or HTTP;
- the binding row and `tasks.deployment_binding_id` (or
  `pentest_configs.deployment_binding_id`) are persisted in one transaction.

Boot wiring for canonical admin PENTEST rows:

```go
boardService := boards.NewService(db, authz,
    boards.WithSubmissionSource(instances.NewExternalSubmissionSource(instancesService)))
```

`NewExternalSubmissionSource` implements `boards.ExternalSubmissionSource`
(`ListSubmissions`/`GetSubmission`/`ReviewSubmission`) with live reviewer
role/module-scope checks, board/module parent matching, idempotent review, and
report text + attached-file metadata. No duplicate core routes are added.

## 3. Core-owned allocation and instances

- `instances.desired_snapshot` is written **at queue time**: version id, digest,
  binding id, non-secret parameters, secret parameter **names**, generated-value
  references, node/integration plan, TTL, request hash. No secret values.
- `instance_network_leases` claim comes from trusted integration settings
  (`network.kind/bridge/vlan/namespace`), never from the manifest.
- public ports are allocated from `integrations.publication_policy`, inserted
  into `instance_endpoints` (`allocated_port`, `released_at IS NULL`) before the
  provider call; the DB unique live-port index arbitrates races.
- endpoints/leases are released only in `releaseResources`, after the worker
  confirmed provider destroy; status becomes `EXPIRED` with `removed_at`.
- Six statuses only: `PROVISIONING/RUNNING/RESETTING/DESTROYING/ERROR/EXPIRED`.
- RESET increments `generation`, queues `RESET` ops and rotates
  `rotateOnReset` generated values. DESTROY queues `DESTROY`; a job for an old
  generation is canceled and can never create a future generation.
- TTL expiration queues cleanup; a worker kill leaves operations
  `QUEUED`/`RUNNING` and `PendingJobs` reconciles them. Drivers adopt only exact
  labels; stale owned generations and platform volumes are removed.
- `cmd/instance-worker` consumes `ACTFB_INSTANCES` / `actfb.instances.jobs`
  (JetStream, explicit ack), runs a 30 s reconcile/expiry loop, and answers
  `actfb.integrations.test.request` with an **id-only** payload.

Worker revalidation (never trusts NATS): live roles via `Authorizer`,
board/module participation/team membership, module audience, board/module
window, and operation generation. `authorized_context` is loaded from the DB by
operation id.

`instances.Options` defaults: TTL 1 h, max TTL 7 d, extension 30 min, max 3
extensions, max 2 instances per subject (enforced by core; the DB still owns
uniqueness).

### Binding / allowlist

A task instance requires `tasks.deployment_binding_id`; Pentest requires
`pentest_configs.deployment_binding_id`. The service loads the immutable version,
verifies digest against the queued snapshot, and requires an **active** `012`
allowlist row for that exact version plus that exact `(kind, integration)` in
`deployment_template_allowlist_targets`. Inline nodes/raw images/raw targets are
rejected. Owner/editor permission `deployment_templates.use` is checked by the
deployment service; this lane enforces the allowlist at start.

### Pentest

- deadline is the effective module/board end; no independent TTL/reset beyond
  `reset_enabled`/`max_resets`.
- `POST …/pentest/submissions` verifies an OPEN caller-scoped draft through the
  injected `files` verifier, then atomically inserts `pentest_submissions`,
  marks files `ATTACHED` + `attached_pentest_submission_id`, links
  `pentest_submission_files`, and writes an audit row. Text-only submissions are
  allowed per policy; idempotent replay returns the original row.
- `ReviewPentestSubmission` is the internal review hook (ACCEPT/REJECT,
  unique accepted per module/subject). The board lane owns the canonical
  `AdminSubmission` union and any points source.

## 4. Integrations CRUD

- write-only `credentials`; AES-256-GCM, key version persisted; GET/list return
  only `credentialsConfigured`. Provider error bodies/URLs with query secrets are
  never logged or returned.
- **frozen cross-lane AAD**: `integrations.CredentialAAD(id)` =
  `[]byte("actfb:integration-credentials:v1:"+id.String())`. The AD worker
  imports this helper (its local copy must be deleted).
- **binding secret AAD** (new, for the board/deployment lane):
  `instances.BindingSecretAAD(bindingID)` =
  `[]byte("actfb:deployment-binding-secrets:v1:"+bindingID.String())`.
  Whoever writes `deployment_bindings.secret_parameters_ciphertext` must use
  this exact helper or the worker cannot apply the binding.
- SSRF: HTTPS only, no userinfo/query/fragment/traversal, exact host allowlist
  from `ACTFB_PROVIDER_ALLOWED_HOSTS` (empty = deny remote URLs). Docker/Podman
  may omit `base_url` (unix socket only).
- strict credential key allowlists per type; strict typed settings;
  inline secret-looking settings keys are rejected.
- `POST …/test` is delegated to the trusted worker over NATS; a missing tester is
  `503 DEPENDENCY_UNAVAILABLE`, a failed provider check `502
  INTEGRATION_CONNECTION_FAILED` + stored `ERROR`. No fake happy path.
- delete returns `409 INTEGRATION_IN_USE` while a binding target, allowlist
  target, repository binding, live network lease or AD assignment references it.

## 5. HTTP routes registered by this lane

Task/admin instances:
`POST|GET /api/v2/boards/{b}/modules/{m}/tasks/{t}/instance`,
`POST …/instance/actions`, `GET /api/v2/admin/instances`,
`GET /api/v2/admin/instances/{i}`, `POST /api/v2/admin/instances/{i}/actions`.

Pentest:
`GET /boards/{b}/modules/{m}/pentest`, `GET|POST …/pentest/environment`
(POST is the only creation route, 202 + Idempotency-Key),
`POST …/pentest/environment/actions`,
`POST|GET …/pentest/submissions` (GET returns `SubmissionListResponse` for the
caller subject),
`GET|PUT /admin/boards/{b}/modules/{m}/pentest/config`,
`GET …/pentest/environments`, `GET|POST …/environments/{e}[/actions]`.

Integrations: `GET|POST /api/v2/admin/integrations`,
`GET|PATCH|DELETE /api/v2/admin/integrations/{i}`,
`POST /api/v2/admin/integrations/{i}/test`.

## 6. Configuration / operations

- `ACTFB_CREDENTIAL_ENCRYPTION_KEY` + `ACTFB_CREDENTIAL_KEY_VERSION` (required by
  the worker; the backend seals only).
- `ACTFB_PROVIDER_ALLOWED_HOSTS=pve.example.com,k8s.example.com`.
- `ACTFB_INSTANCE_WORKER_DOCKER_SOCKET`, `…_PODMAN_SOCKET` (defaults:
  `DOCKER_HOST`/`/var/run/docker.sock`, Podman rootless or `/run/podman/podman.sock`).
- only `cmd/instance-worker` should receive the container socket. The HTTP
  backend has no Docker socket, no provider credentials and no Docker driver.

## 7. Verification actually run

```text
go build ./internal/runtime/... ./internal/integrations/... ./internal/instances/... ./cmd/instance-worker
go vet  ./internal/runtime/... ./internal/integrations/... ./internal/instances/...
go test ./internal/runtime/...                    # includes real Docker E2E (skip if absent)
ACTFB_TEST_POSTGRES_DSN=… go test ./internal/instances/ ./internal/integrations/
```

Covered by tests:

- real Docker (`nginx:alpine` must be present locally; test skips otherwise):
  apply/endpoint HTTP 200, same-generation retry adopts (no
  duplicate), reset advances generation and serves the clean marker, destroy
  removes containers/volume/forwarder, repeated destroy is idempotent,
  shared-worker policy refused before any daemon call.
- Docker/Podman/Kubernetes/Proxmox/KubeVirt contract tests: strict option/settings
  rejection, no privileged/hostPath/hostNetwork, digest-pinned sources,
  egress-deny NetworkPolicy, NodePort allocation, Proxmox logical-template
  mapping + allowed VMID pool + UPID polling, KubeVirt approved PVC clone,
  no host disk.
- real PostgreSQL (`007`–`012`): provisioning snapshot/leases/ports, immutable
  version match, worker apply → `RUNNING`, redelivery no-op, reset generation,
  destroy releases endpoint/lease and sets `removed_at`, live-role revalidation
  denial, expiry cleanup, PENTEST start/reset-limit/submit-idempotency/review,
  integration CRUD + encrypted credentials + AAD + in-use delete refusal + real
  worker test failure mapping. Real HTTP+PG tests cover: allowlist
  create/list/revoke + duplicate conflict + non-admin denial; binding exact
  version/targets + sealed secret parameters + missing-secret rejection +
  revoked-allowlist rejection; PENTEST POST environment/GET list, private report
  text + attached file metadata, no storage-key leak, another participant sees
  an empty list, cross-board/module parent mismatch, live reviewer revocation,
  and idempotent ACCEPT vs conflicting REJECT.

## 8. Blockers / gaps (honest)

1. **Real Proxmox/Kubernetes/KubeVirt acceptance is BLOCKED**: no endpoint or
   credentials are configured. Only httptest contract + security-shape tests ran.
2. **Podman rootless endpoint publication** is not proven; the adapter refuses
   PUBLIC endpoints. A rootful dedicated Podman worker is required for parity.
3. **Swagger defect**: `/boards/{b}/modules/{m}/pentest/environment` contains a
   duplicate `post:` key and a duplicated `startPentestEnvironment` operationId
   (YAML keys 6015/6191). The route is implemented once (202 + Idempotency-Key);
   the schema owner should remove the duplicate. GET still never provisions.
4. **Inline task instance nodes are not stored** by the current board schema
   (only `deployment_binding_id` exists). This lane returns
   `INSTANCE_TEMPLATE_NOT_FOUND` until the board/deployment lane persists inline
   config or a binding. `PentestConfig` PUT rejects non-empty inline `nodes` and
   raw provider ids.
5. **Binding secret AAD is new**: the board/deployment lane must use
   `instances.BindingSecretAAD` when sealing `secret_parameters_ciphertext`
   (cross-lane handshake). Until then bindings with secret parameters will fail
   apply closed.
6. Canonical admin PENTEST list/get/review is exported through
   `NewExternalSubmissionSource`; boot must pass it to
   `boards.WithSubmissionSource`. Points remain 0 unless a later points source is
   approved; `ReviewPentestSubmission`/adapter review is idempotent by decision.
7. Boot wiring (`cmd/backend`, `internal/httpapi`) is owned by another worker and
   was not edited. `cmd/backend` currently fails to build because of that lane's
   in-progress plugin changes; this lane's packages build and test independently.