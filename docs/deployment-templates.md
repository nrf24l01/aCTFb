# Deployment templates

Status: **implemented template contract + CRUD/validate/preview/publish/versions/diff**, 2026-11-13.
Owner scope: `internal/deployment/**`, `docs/deployment-templates.md`. Migrations `009`/`012`, Swagger
`DeploymentTemplate*` definitions and generated models are owned elsewhere and were not edited by this slice.

This document is the operator/developer contract for the provider-independent deployment template engine. It is
not the runtime orchestrator and it does not provision infrastructure.

## 1. Package direction and exports

`internal/deployment` owns the frozen §5.5 types (`Kind`, `SourceType`, `Exposure`, `Metadata`, `Parameter`,
`GeneratedValue`, `Source`, `Resources`, `EnvVar`, `EnvSource`, `FileSpec`, `Endpoint`, `Bootstrap`, `Check`,
`Lifecycle`, `Node`, `Template`, `TemplateVersion`, `Binding`, `Subject`, `DesiredNode`, `DesiredDeployment`,
`Capabilities`, `InstancePublicationPolicy`, `Finding`, `ValidationResult`, `Validator`). Every type carries
camelCase `json` and `yaml` tags.

Import direction is frozen:

```text
internal/runtime      -> internal/deployment
internal/instances    -> internal/deployment + internal/runtime
internal/deployment   -> never internal/runtime
```

`internal/deployment` therefore defines the read-only dependency callbacks the future runtime/instances workers
implement:

```go
type DriverMetadataProvider interface {
    ListDriverMetadata(ctx context.Context) ([]DriverMetadata, error)
    ValidateDriverOptions(ctx context.Context, driver string, node Node, options json.RawMessage) []Finding
}
```

`DriverMetadata` is `{Driver, Kind, Capabilities}`. The provider is metadata-only: it must not provision,
inspect a live resource, mutate provider state or return credentials. A test double can count calls; there is no
`Apply` method on this interface, so validate/preview cannot reach infrastructure.

The approved constructor is:

```go
func NewService(
    db *postgres.DB,
    authz authorization.Authorizer,
    sealer secrets.Sealer,
    options ...Option,
) (*Service, error)
```

Options: `WithDriverMetadataProvider`, `WithReadPermissionProvider`, `WithLogger`. `sealer` is accepted for the
binding/secret-parameter phase; published template snapshots never contain plaintext secret parameter values.
`nil` authz is only useful for unit-level preview helpers; HTTP CRUD returns `ErrNoAuthorizer`/`ErrNoDatabase`.

Standalone rendering for a trusted caller:

```go
func Render(ctx context.Context, input RenderInput) (*RenderedDeployment, error)
```

`RenderInput` accepts manifest/files plus `Parameters map[string]string` and `Generated map[string]string`.
Secret values live only in the trusted caller; they are never persisted in `deployment_template_versions`.

## 2. Manifest contract

YAML is accepted at the API boundary; the backend stores the normalized typed model in PostgreSQL. The envelope is
exactly:

```yaml
apiVersion: actfb.dev/v1alpha1
kind: DeploymentTemplate
metadata:
  name: Web security lab
  slug: web-security-lab
  description: Isolated lab
parameters: [...]
generated: [...]
nodes: [...]
checks: [...]
lifecycle:
  reset: RECREATE
```

### Node kinds vs drivers

`Kind` is only `VM` or `CONTAINER`. Drivers are separate trusted administration choices: `proxmox`, `kubevirt`
(VM) and `docker`, `podman`, `kubernetes` (CONTAINER). `driverOptions` must be namespaced by one of those driver
names. No native Proxmox VMID is a universal field; VM `source.ref` is a stable logical template/image name.

- `CONTAINER` requires `source.type: OCI_IMAGE` and an image pinned as `image@sha256:<64 lowercase hex>`.
- `VM` requires `source.type: VM_TEMPLATE` or `VM_IMAGE` with a logical reference. All-digit refs (native VMID)
  are rejected.
- Placeholders such as `REPLACE_WITH...`, `placeholder`, inline credentials and all-zero placeholder digests are
  rejected.
- A manifest mixing `VM` and `CONTAINER` is rejected deterministically (`MIXED_KIND_UNSUPPORTED`) until a supported
  inter-node network policy exists. There is no fallback.

### Common resources and kind-specific fields

`resources.cpuMillicores`, `resources.memoryMiB` and `resources.diskMiB` are mandatory and unit-explicit.

| Field | Bounds |
|---|---:|
| CPU | `1..128000` millicores |
| Memory | `8..1048576` MiB |
| Disk | `1..10485760` MiB |
| Nodes | `1..20` |
| Parameters | `0..64` |
| Generated values | `0..64`, length `8..128` |
| Env per node | `0..128` |
| Files per node | `0..64` |
| Endpoints per node | `0..32` |
| Checks | `0..64` |

`env` is CONTAINER-only. `bootstrap.cloudInitFile` is VM-only. A `valueFrom` sets exactly one of `generated`,
`parameter` or `literal`. Secret-looking literals (`TOKEN`, `PASSWORD`, `SECRET`, provider access keys, PEM private
keys) are rejected; use a `secret: true` parameter reference instead.

`files[].source` is a relative package path; `files[].target` must be an absolute, traversal-free POSIX path;
`mode` is `0xxx` octal. Neither inline file paths nor archive entries may contain `..`, leading `/`, backslashes,
NUL or symlinks. Public host/port/domain fields are not part of an endpoint or node; public allocation belongs to
the trusted integration `InstancePublicationPolicy` and runtime orchestrator.

### Parameters, defaults and generated values

`Parameter.Type` is `string`, `text`, `int`, `bool` or `choice`. YAML-native booleans/integers are canonicalized
to strings at parse time. Choice defaults must match a declared option. Secret parameters must not carry a
plaintext default. Generated `kind` is `uuid`, `password`, `hex` or `base64`; values are generated per instance,
never per template, and preview always substitutes `[REDACTED]`.

## 3. Strict parsing

`ParseManifest` enforces, before typed decoding:

- input non-empty and **≤ 64 KiB**;
- exactly one YAML document;
- no aliases/anchors or merge keys;
- string mapping keys only;
- no duplicate keys at any depth;
- bounded depth/node count;
- no filesystem or environment reads.

Typed decoding then rejects unknown fields at every level (`DisallowUnknownFields`), including custom
`parameters[].default` and endpoint `display: {label: ...}` decoding. Findings contain a stable path/code, never
raw manifest content.

## 4. Validation and capabilities

`ValidateTemplate(ctx, template, files, provider)` performs provider-independent checks plus metadata/capability
checks. `Validate`/`Preview` perform **no** provisioning, mutation or driver Apply.

For a namespaced option block:

- unknown driver namespace → error `DRIVER_OPTION_UNKNOWN`;
- known driver for the other kind → warning `DRIVER_OPTION_UNUSED` (never executed);
- driver metadata absent → warning `DRIVER_OPTION_NOT_VERIFIED`;
- driver metadata present → its strict `ValidateDriverOptions` callback is called. Its relative path is prefixed
  with `nodes[i].driverOptions.<driver>`; raw option payloads are never forwarded to another driver.

Capability checks fail friendly when a selected driver lacks source type, cloud-init, file injection, public
exposure, snapshot reset or node-count support. If no driver metadata is registered, validation returns an explicit
warning rather than silently passing. Provider credentials are never part of a manifest or driver option block;
secret-looking keys in `driverOptions` are rejected.

## 5. Preview, rendering and secret safety

`BuildPreview` returns nodes (`CREATE` plans), endpoints and warnings. It renders referenced file templates in
memory with default/sample parameter values and redacted generated values, then discards the rendered bytes. A
secret value or file content never appears in the preview response, warnings or diff.

Rendering uses `text/template` with `missingkey=error`, no custom `FuncMap`, and therefore no `os`, environment,
file or network functions. Recursion is bounded by cancellation and the output budget: 1 MiB per rendered file and
16 MiB per rendered package. Missing/unknown parameters and generated values produce bounded validation findings.

`PublishedMetadata`/`MetadataForVersion` are the core-page-safe projection: no files, no rendered values, no secret
parameter defaults. Diff entries for files use only content digests and never include file bytes; secret parameters
are represented as `[REDACTED]`.

## 6. RBAC

All 11 admin template operations require the central permission `platform.deployment_templates.publish` through
`authorization.Authorizer` with global scope. There is no role-name or local permission list in this package.

Scoped read/bind is intentionally not faked. `ReadPermissionProvider` defines `CanReadTemplate` and
`CanBindTemplate` for the migration-012 `deployment_template_allowlist` / future instances worker. When the
provider is absent, only the global publish permission can read; no broad grant is invented. The admin HTTP
adapter still uses `httpapi.RequireUser` and the central authorizer behind the service.

## 7. HTTP API

`internal/deployment/http.Handler.RegisterRoutes` installs exactly the existing expanded Swagger operations under
`/api/v2/admin/deployment-templates`:

| Method | Path | Success |
|---|---|---|
| GET | `/` | `200 DeploymentTemplateListResponse` |
| POST | `/` | `201 DeploymentTemplate` |
| GET | `/{templateUuid}` | `200 DeploymentTemplate` |
| GET | `/{templateUuid}/draft` | `200 DeploymentTemplateDraft` |
| PUT | `/{templateUuid}/draft` | `200 DeploymentTemplateDraft` |
| POST | `/{templateUuid}/draft/validate` | `200 DeploymentTemplateValidation` |
| POST | `/{templateUuid}/draft/preview` | `200 DeploymentTemplatePreview`, or `422` validation envelope |
| POST | `/{templateUuid}/publish` | `201 DeploymentTemplateVersion` |
| GET | `/{templateUuid}/versions` | `200 DeploymentTemplateVersionListResponse` |
| GET | `/{templateUuid}/versions/{version}` | `200 DeploymentTemplateVersion` |
| GET | `/{templateUuid}/versions/{version}/diff?from={version}` | `200 DeploymentTemplateDiff` |

Request bodies use the generated `DeploymentTemplateDraftRequest` / `DeploymentTemplatePublishRequest` models;
responses use the generated models and the shared `ApiError`/`ValidationError` envelope. Unknown JSON fields,
malformed bodies, invalid UUIDs/versions and oversized bodies are rejected. Published-version responses do not
include files or manifests. `POST /` accepts an optional generated draft request (useful for initial import) or
creates a bounded skeleton draft when no body is supplied.

Boot wiring and frontend integration are owned by the main agent; this package only exposes `RegisterRoutes`.

## 8. Persistence and immutability

Drafts live in `deployment_template_drafts` (`manifest_yaml`, inline `files` JSONB). A draft may be semantically
invalid; its size, file paths and base64 content are still bounded.

Publishing runs in one transaction:

1. `pg_advisory_xact_lock(hashtextextended(template_id::text, 0))`;
2. lock/read the template and draft;
3. strict parse + validate;
4. `MAX(version)+1` and insert an immutable row;
5. commit.

The uniqueness constraint `(template_id, version)` is the final concurrency guard. The digest is a stable
`sha256:` over the **raw** draft manifest plus normalized, sorted file paths/contents; map order does not affect it.
Published versions are never updated or deleted by the service. Migration `012` additionally installs a
`BEFORE UPDATE OR DELETE` trigger that raises on any published-version mutation.

`ImportArchive` is available as an optional helper: fully in-memory ZIP import with entry count, per-file/total
size, compression-ratio, duplicate-name, encryption, traversal, symlink and missing-manifest checks. It never
extracts to the filesystem and never executes package content. There is deliberately no public ZIP HTTP endpoint.

## 9. Tests actually present

- `parse_test.go`: known/unknown fields, duplicates, wrong types, aliases/multi-doc, size bound, non-string keys.
- `validate_test.go`: source pin/placeholder/native VMID, resources, env one-of/secret inlining, unsafe files,
  driver options namespace/callback path, capability gates, mixed kinds, bounds, no-metadata warning, no Apply.
- `render_test.go`: strict missing/unknown parameters, no environment functions, output limit, cancellation,
  preview/secret masking, metadata projection, digest stability, diff content masking.
- `archive_test.go`: ZIP-slip, symlink, zip-bomb ratio, size, duplicate entries, missing manifest, no filesystem use.
- `service_pg_test.go`: real PostgreSQL `009`–`012`, admin/non-admin authorization, validate/preview with zero
  Apply, publish v1/v2, immutability, concurrent publish advisory-lock allocation, secret/file non-leakage,
  oversize rejection, missing template.
- `http/handler_test.go`: all 11 routes and expanded-Swagger response validation with a fake service/auth.
- `http/handler_pg_test.go`: real PostgreSQL + central DB authorizer + real service, all routes validated against
  the expanded Swagger definitions, and a non-admin 403.

Run:

```bash
go test ./internal/deployment/... -count=1
ACTFB_TEST_POSTGRES_DSN='postgres://.../actfb?sslmode=disable' \
  go test ./internal/deployment/... -count=1 -v
```

## 10. Externally blocked / out of this slice

- No trusted runtime registry implementation is supplied here; `DriverMetadataProvider` must be implemented by the
  runtime/instances owner with real Proxmox, KubeVirt, Docker, Podman and Kubernetes metadata. External provider
  endpoints/credentials are unavailable, so capability acceptance for real drivers remains blocked.
- No `ReadPermissionProvider` implementation for 012 allowlists is supplied; that belongs to the instances/task
  binding worker. No broad read fallback exists.
- No boot wiring, route registration in `cmd/backend`, frontend editor, instance orchestrator, binding API, worker,
  NATS jobs, provider adapters or secret-parameter sealing flow is included.
- No `go.mod`/`go.sum` dependency was added.