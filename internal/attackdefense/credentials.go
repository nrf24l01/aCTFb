package attackdefense

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/integrations"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

// CredentialAAD is the cross-lane handshake with the integration CRUD worker.
//
// The integration owner exports integrations.CredentialAAD(id uuid.UUID)
// []byte returning exactly:
//
//	[]byte("actfb:integration-credentials:v1:" + id.String())
//
// The AD trusted worker and the webhook process must decrypt
// integrations.credentials_ciphertext with those exact AAD bytes. Until the
// exported function lands, this function is the single source of the same bytes
// on this side. No fork, no alternate prefix.
func CredentialAAD(id uuid.UUID) []byte {
	// Single source of truth: the integration owner's exported function.
	return integrations.CredentialAAD(id)
}

// RepositoryWebhookSecretAAD is the AD-owned, domain-separated AAD for
// repository_bindings.webhook_secret_ciphertext. It is bound to the binding id
// so ciphertext cannot be moved between repository bindings.
func RepositoryWebhookSecretAAD(bindingID uuid.UUID) []byte {
	return []byte("actfb:repository-binding-webhook-secret:v1:" + bindingID.String())
}

// IntegrationCredentials is the decrypted Git integration credential shape.
// The documented JSON handshake is {"token":"...","username":"..."} for token
// authentication, or {"username":"...","password":"..."} for basic
// authentication. A raw string is accepted as a token for operator
// compatibility, never as an anonymous credential.
type IntegrationCredentials struct {
	Username string
	Token    string
	Password string
}

// Empty reports whether no usable credential was supplied.
func (c IntegrationCredentials) Empty() bool {
	return c.Token == "" && (c.Username == "" || c.Password == "")
}

// Integration is the trusted read model used by AD services. It contains
// decrypted credentials and must never be returned by an HTTP adapter.
type Integration struct {
	ID          uuid.UUID
	Category    string
	Type        string
	Name        string
	BaseURL     string
	Status      string
	Credentials IntegrationCredentials
	Settings    json.RawMessage
}

// IntegrationResolver resolves a Git integration and decrypts credentials in
// the trusted process. It intentionally imports nothing from the integration
// worker; the bytes-level AAD handshake keeps the direction one-way.
type IntegrationResolver interface {
	ResolveIntegration(ctx context.Context, integrationID uuid.UUID) (*Integration, error)
}

// PostgresIntegrationResolver reads integrations directly and decrypts with the
// shared sealer. It fails closed when the sealer or ciphertext is absent.
type PostgresIntegrationResolver struct {
	db         *postgres.DB
	sealer     secrets.Sealer
	keyVersion uint32
}

// NewPostgresIntegrationResolver constructs the shared resolver. keyVersion is
// used only when the sealer does not expose KeyVersion (for example a test
// double); production ciphers expose it.
func NewPostgresIntegrationResolver(db *postgres.DB, sealer secrets.Sealer, keyVersion uint32) (*PostgresIntegrationResolver, error) {
	if db == nil || db.Pool == nil {
		return nil, ErrNoDatabase
	}
	if sealer == nil {
		return nil, ErrNoSealer
	}
	if keyVersion == 0 {
		keyVersion = 1
	}
	return &PostgresIntegrationResolver{db: db, sealer: sealer, keyVersion: keyVersion}, nil
}

// ResolveIntegration implements IntegrationResolver.
func (r *PostgresIntegrationResolver) ResolveIntegration(ctx context.Context, integrationID uuid.UUID) (*Integration, error) {
	if r == nil || r.db == nil || r.db.Pool == nil {
		return nil, ErrNoDatabase
	}
	if integrationID == uuid.Nil {
		return nil, ErrIntegrationNotFound
	}
	var (
		record     Integration
		baseURL    *string
		ciphertext []byte
		keyVersion *int
		settings   []byte
	)
	err := r.db.Pool.QueryRow(ctx, `
		SELECT id, category, type, name, base_url, status,
		       credentials_ciphertext, credentials_key_version, settings
		FROM integrations
		WHERE id = $1
	`, integrationID).Scan(
		&record.ID, &record.Category, &record.Type, &record.Name,
		&baseURL, &record.Status, &ciphertext, &keyVersion, &settings,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrIntegrationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attackdefense: read integration: %w", err)
	}
	if record.Category != "GIT" {
		return nil, ErrIntegrationNotGit
	}
	record.BaseURL = strings.TrimSpace(deref(baseURL))
	record.Settings = append(json.RawMessage(nil), settings...)
	if len(ciphertext) == 0 {
		return nil, ErrIntegrationCredentials
	}
	if keyVersion == nil || *keyVersion < 1 {
		return nil, ErrIntegrationCredentials
	}
	if versioner, ok := r.sealer.(interface{ KeyVersion() uint32 }); ok && versioner.KeyVersion() != uint32(*keyVersion) {
		return nil, ErrIntegrationCredentials
	} else if !ok && r.keyVersion != uint32(*keyVersion) {
		return nil, ErrIntegrationCredentials
	}
	// Decrypt through the integration owner's exported helper so the AAD and
	// envelope are never forked.
	values, err := integrations.OpenCredentials(r.sealer, integrationID, integrations.SealedCredentials{
		Ciphertext: ciphertext,
		KeyVersion: uint32(*keyVersion),
	})
	if err != nil {
		return nil, ErrIntegrationCredentials
	}
	decode := mapAny(values)
	credentials := IntegrationCredentials{
		Username: firstString(decode, "username", "user", "login"),
		Token:    firstString(decode, "token", "accessToken", "access_token", "apiToken", "api_token", "personalAccessToken"),
		Password: firstString(decode, "password", "pass"),
	}
	if credentials.Empty() {
		return nil, ErrIntegrationCredentials
	}
	record.Credentials = credentials
	return &record, nil
}

// RepositoryAllowed enforces the integration's optional Git settings
// repositoryAllowlist. An empty allowlist means the integration owner permits
// provider access for any repository the credential can see; a non-empty list is
// exact (GitHub is case-insensitive).
func RepositoryAllowed(integration *Integration, repository string) bool {
	if integration == nil || len(integration.Settings) == 0 {
		return true
	}
	var settings struct {
		RepositoryAllowlist []string `json:"repositoryAllowlist"`
	}
	if err := json.Unmarshal(integration.Settings, &settings); err != nil {
		return false
	}
	if len(settings.RepositoryAllowlist) == 0 {
		return true
	}
	for _, allowed := range settings.RepositoryAllowlist {
		if allowed == repository {
			return true
		}
		if strings.EqualFold(integration.Type, "GITHUB") && strings.EqualFold(allowed, repository) {
			return true
		}
	}
	return false
}

func mapAny(values map[string]string) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text, ok := value.(string); ok {
				text = strings.TrimSpace(text)
				if text != "" && len(text) <= 4096 {
					return text
				}
			}
		}
	}
	return ""
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
