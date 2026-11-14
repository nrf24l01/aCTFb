# Secrets and events shared foundation

Status: implemented for the remaining-stages foundation. This package is the
single handoff surface for the identity, files, deployment, plugin, and boot
workers. No migrations, generated files, Compose, or feature handlers are changed
here.

## Secrets — `internal/platform/secrets`

Exact exports:

```go
package secrets

type Sealer interface {
    Seal(plaintext, aad []byte) ([]byte, error)
    Open(blob, aad []byte) ([]byte, error)
}

func NewCipher(base64Key string, keyVersion uint32) (*Cipher, error)
func ValidateKey(base64Key string) error
```

Errors: `ErrInvalidKey`, `ErrInvalidKeyVersion`, `ErrInvalidEnvelope`,
`ErrUnknownKeyVersion`, `ErrDecrypt`. They never include key material or
plaintext.

Ciphertext envelope:

```text
"ACTFBGCM" (8) | envelope format 1 (1) | key version uint32 BE (4) |
nonce (12) | AES-256-GCM ciphertext+tag
```

- Key: base64-decoded **exactly 32 bytes**; no default key.
- Key version must be non-zero.
- Nonces are generated with `crypto/rand`; no nonce reuse.
- The envelope header and the caller AAD are bound into GCM AAD together.
- `Open` rejects malformed magic/format, unknown key versions, tampering, wrong
  keys, and AAD domain mismatch.
- Use stable domain-separated AAD strings, for example:
  - `actfb.integration.credentials.v1`
  - `actfb.deployment.binding-secrets.v1`
  - `actfb.instance.secret-values.v1`
  - `actfb.team.invite-key.v1`
  - `actfb.board.invite-code.v1`
  - `actfb.plugin.settings.v1`

The same general cipher is valid for team/board invite tokens, integration
credentials, plugin private DB DSNs/settings, deployment binding secrets and
instance values. Ciphertext is stored in the `*_ciphertext` columns from
`009`/`010`; never store plaintext in the DB.

Feature APIs must fail closed when the key is absent; they must not return fake
success. Constructing the cipher is independent of `Deployment.Enabled`: other
workers may set `ACTFB_CREDENTIAL_ENCRYPTION_KEY` and use the general sealer
without enabling runtime.

## Events — `internal/events`

Exact shared contracts:

```go
type Event struct {
    ID          uuid.UUID       `json:"id"`
    Type        string          `json:"type"`
    OccurredAt  time.Time       `json:"occurredAt"`
    BoardID     *uuid.UUID      `json:"boardId,omitempty"`
    ModuleID    *uuid.UUID      `json:"moduleId,omitempty"`
    SubjectType string          `json:"subjectType,omitempty"`
    SubjectID   *uuid.UUID      `json:"subjectId,omitempty"`
    Payload     json.RawMessage `json:"payload"`
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

`Tx` is source-compatible with `pgx.Tx` and every `pgx.Tx` implementation.

Additional exports:

```go
const (
    StreamName = "ACTFB_EVENTS"
    SubjectPrefix = "actfb.events."
    EventInstanceCreated = "instance.created"
    EventInstanceStarted = "instance.started"
    EventInstanceStopped = "instance.stopped"
    EventInstanceDestroyed = "instance.destroyed"
    EventInstanceExpired = "instance.expired"
    SubjectTypeUser = "USER"
    SubjectTypeTeam = "TEAM"
)

func SubjectFor(eventType string) string

func NewOutbox(db *postgres.DB, opts ...OutboxOption) *PostgresOutbox
func WithRegistry(registry *Registry) OutboxOption
func NewRegistry() *Registry
func CoreRegistry() *Registry
func (r *Registry) RegisterCore(eventType string) error
func (r *Registry) RegisterCoreSchema(eventType string, validator PayloadValidator) error
func (r *Registry) RegisterPlugin(pluginID string, eventTypes ...string) error
func (r *Registry) RegisterPluginSchema(pluginID, eventType string, validator PayloadValidator) error
func (r *Registry) Validate(eventType string) error
func (r *Registry) ValidateEvent(event Event) error
func (r *Registry) ValidateCorePublish(eventType string) error
func (r *Registry) ValidatePluginPublish(pluginID, eventType string) error
func ValidateSubmissionJudgedPayload(raw json.RawMessage) error

func EnsureStream(ctx context.Context, client *platformnats.Client) error
func NewNATSPublisher(client *platformnats.Client, opts ...NATSPublisherOption) *NATSPublisher
func WithNATSPublisherRegistry(registry *Registry) NATSPublisherOption

func NewDispatcher(db *postgres.DB, publisher Publisher, opts ...DispatcherOption) *Dispatcher
func (d *Dispatcher) Run(ctx context.Context) error
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error)
```

### Outbox rules

`Enqueue` inserts into `events_outbox` with exactly the `008` columns:

```text
id, type, occurred_at, board_id, module_id, subject_type, subject_id, payload
```

- The caller transaction is mandatory. The outbox never begins, commits, or
  rolls back it.
- `ON CONFLICT (id) DO NOTHING` makes event identity idempotent.
- Empty payload becomes `{}`; zero `occurred_at` becomes current UTC time.
- Unknown event types are rejected.
- Payloads must be JSON objects and are scanned recursively for forbidden keys
  (flags, answers, hashes, text, binary/file bytes, passwords/tokens/credentials).
  Nested keys are checked; arrays are traversed.
- `submission.judged` has a strict allowed-field schema and no flag, answer hash,
  request hash, submitted text, or file bytes.
- Subject type must be `USER`/`TEAM` and must be paired with a subject ID.

### Registry and plugin publishing

- Core types are registered explicitly. `CoreRegistry()` includes
  `submission.judged` and the `instance.*` names above.
- Plugin IDs must be namespaced, e.g. `vendor.plugin`.
- Plugin event types must be `plugin.<pluginID>.<event>` and must be explicitly
  declared with `RegisterPlugin`/`RegisterPluginSchema`.
- `ValidatePluginPublish` rejects core topics (`submission.judged`) with
  `ErrPluginCoreTopic`, and cross-plugin/undeclared topics with
  `ErrEventNotDeclared`. The plugin `CoreProxy.PublishEvent` must call this method;
  do not let plugins forge core or `submission.judged` topics.
- Plugin payload validators may be registered with `RegisterPluginSchema`; generic
  forbidden-key checks always apply.

### Dispatcher and delivery

- `NewDispatcher` claims unpublished rows with a short transaction:
  `SELECT ... FOR UPDATE SKIP LOCKED ... LIMIT batch`, increments `attempts`, and
  commits. Publishing happens outside the DB transaction.
- Publisher: NATS JetStream stream `ACTFB_EVENTS`, subject
  `actfb.events.<type>`, message ID = event UUID (`Nats-Msg-Id`).
- On success: `published_at=NOW(), last_error=NULL`.
- On failure: `last_error` is recorded and the row remains unpublished.
- Delivery is at-least-once, not exactly-once. A crash after publish but before
  the mark is retried and JetStream deduplicates by message ID within its window.
- Invalid/legacy rows are recorded with `last_error` and do not block other rows.
- The dispatcher process is `cmd/events-dispatcher`; it loads shared config,
  connects PostgreSQL/NATS, ensures the stream, and drains gracefully on
  SIGINT/SIGTERM.

## Configuration handoff

Non-secret defaults are in `config.dev.yml`; secrets are env-only.

```go
type Config struct {
    // ...
    Deployment DeploymentConfig `yaml:"deployment"`
    Plugins    PluginsConfig    `yaml:"plugins"`
    Files      FilesConfig      `yaml:"files"`
}

type DeploymentConfig struct {
    CredentialKey string `env:"ACTFB_CREDENTIAL_ENCRYPTION_KEY"`
    KeyVersion    uint32 `env:"ACTFB_CREDENTIAL_KEY_VERSION"` // default 1
    Enabled       bool   `env:"ACTFB_RUNTIME_ENABLED"`        // default false
}

type PluginsConfig struct {
    Enabled             bool          `env:"ACTFB_PLUGINS_ENABLED"`        // default false
    Root                string        `env:"ACTFB_PLUGINS_DIR"`            // default ./plugins
    ServiceKey          string        `env:"ACTFB_PLUGIN_SERVICE_KEY"`     // base64 32 bytes; required when enabled
    IframeOrigin        string        `env:"ACTFB_PLUGIN_IFRAME_ORIGIN"`   // default http://127.0.0.2:8091
    AssetsListenAddress string        `env:"ACTFB_PLUGIN_ASSET_ADDR"`      // default 127.0.0.2:8091
    HTTPTimeout         time.Duration `env:"ACTFB_PLUGIN_HTTP_TIMEOUT"`    // default 5s, max 30s
    MaxBodyBytes        int64         `env:"ACTFB_PLUGIN_MAX_BODY_BYTES"`  // default 1 MiB
    AllowExec           bool          `env:"ACTFB_PLUGIN_ALLOW_EXEC"`      // default false
}

type FilesConfig struct {
    Enabled       bool   `env:"ACTFB_FILES_ENABLED"` // default false
    ScanKey       string `env:"ACTFB_FILE_SCAN_KEY"` // base64 32 bytes when enabled
    ClamAVAddress string `env:"CLAMAV_ADDRESS"`
    RequireClamAV bool   `env:"CLAMAV_REQUIRED"` // default true; explicit false is dev-only
}
```

Validation behavior:

- Deployment/plugin/file keys are validated whenever provided; required only
  when the corresponding feature is enabled.
- `deployment.credential_key` may be set while runtime is disabled so the
  general sealer can be used for team/board invites and other secrets.
- `plugins.iframe_origin` must be an exact HTTP(S) origin, with no path, query,
  fragment, or userinfo. When plugins are enabled its **host** must differ from
  every `web_app.allowed_origins` host, not merely the port.
- `plugins.assets_listen_address` and `files.clamav_address` are `host:port`
  listen/connect addresses, not URLs.
- `files.require_clamav` defaults true. An explicit false is accepted only as an
  explicit development policy; it must never produce a fake `READY`/clean result.
- `Config.String()` shows only enabled flags, versions, the plugin root path,
  timeouts/limits, and `*_key_set`/URL-presence booleans; secret values and raw
  URL settings never appear.

The iframe bridge uses an opaque sandbox, so browser `event.origin` is `"null"`;
that is a frontend concern and must not be compared to the configured HTTP
origin. No automatic origin trust or URL userinfo/query settings are introduced.

## Verification

- `go test ./internal/platform/secrets ./internal/events ./internal/config`
- `go test -race ./internal/platform/secrets ./internal/events ./internal/config`
- Real opt-in PostgreSQL + NATS:

```bash
ACTFB_TEST_POSTGRES_DSN='postgres://postgres:dev-password@127.0.0.1:56380/actfb?sslmode=disable' \
ACTFB_TEST_NATS_HOST=127.0.0.1 ACTFB_TEST_NATS_PORT=56381 \
  go test -race ./internal/events -run TestOutboxRollbackIdentityAndDispatch -v
```

The real test applies migrations through `008`, verifies rollback/identity,
retries a failed publish, marks the row published, checks the delivered subject
and `Nats-Msg-Id`, rejects secret payload fields, and verifies JetStream dedupe.