// Package integrations owns admin-managed provider integrations. Credentials
// are write-only, sealed with AES-256-GCM under the deployment key and
// decrypted only in the trusted worker. The HTTP backend never returns them and
// never contacts a provider directly.
package integrations

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
)

// Category separates Git integrations from compute providers.
type Category string

const (
	CategoryGit     Category = "GIT"
	CategoryCompute Category = "COMPUTE"
)

// Type is the legacy/extensible integration type enum.
type Type string

const (
	TypeGitLab     Type = "GITLAB"
	TypeGitHub     Type = "GITHUB"
	TypeGitea      Type = "GITEA"
	TypeProxmox    Type = "PROXMOX"
	TypeContainer  Type = "CONTAINER"
	TypeKubeVirt   Type = "KUBEVIRT"
	TypeDocker     Type = "DOCKER"
	TypePodman     Type = "PODMAN"
	TypeKubernetes Type = "KUBERNETES"
)

// Status is the last trusted connection result.
type Status string

const (
	StatusUnknown   Status = "UNKNOWN"
	StatusConnected Status = "CONNECTED"
	StatusError     Status = "ERROR"
)

// Integration is the redacted read model. It never contains credentials.
type Integration struct {
	UUID                  uuid.UUID
	Category              Category
	Type                  Type
	Kind                  deployment.Kind
	Driver                string
	Name                  string
	BaseURL               string
	CredentialsConfigured bool
	Settings              json.RawMessage
	Publication           *deployment.InstancePublicationPolicy
	Status                Status
	CreatedBy             uuid.UUID
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// CreateInput is the validated service input. Credentials are write-only.
type CreateInput struct {
	Type        Type
	Kind        string
	Driver      string
	Name        string
	BaseURL     string
	Credentials map[string]string
	Settings    json.RawMessage
	Publication *deployment.InstancePublicationPolicy
}

// PatchInput uses pointers so PATCH never clears omitted fields.
type PatchInput struct {
	Type        *Type
	Kind        *string
	Driver      *string
	Name        *string
	BaseURL     *string
	Credentials map[string]string
	Settings    *json.RawMessage
	Publication *deployment.InstancePublicationPolicy
}

// ListQuery paginates the admin list.
type ListQuery struct {
	Limit  int
	Offset int
}

// Page is a bounded list result.
type Page struct {
	Items []Integration
	Total int64
}

// GitSettings is the closed settings schema for Git integrations. Provider
// tokens/passwords belong to the write-only credentials field.
type GitSettings struct {
	RepositoryAllowlist []string `json:"repositoryAllowlist,omitempty"`
	WebhookBaseURL      string   `json:"webhookBaseURL,omitempty"`
}

// CredentialAAD is the frozen domain-separated AAD for the credentials column.
// The AD worker imports this helper and opens the same blob; the format must
// never change silently.
func CredentialAAD(integrationID uuid.UUID) []byte {
	return []byte("actfb:integration-credentials:v1:" + integrationID.String())
}

// TestResult is the trusted worker reply for a connection test.
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ConnectionTester performs a real provider check through the trusted worker.
// There is deliberately no in-process default: an unavailable tester returns a
// dependency error instead of a fake success.
type ConnectionTester interface {
	Test(ctx context.Context, integrationID uuid.UUID) (TestResult, error)
}

// GitConnectionTester is an optional callback for Git providers so the AD lane
// can implement its own HTTPS client without this package importing it.
type GitConnectionTester interface {
	TestGit(ctx context.Context, in GitTestRequest) (TestResult, error)
}

// GitTestRequest carries decrypted Git credentials to a trusted callback. It is
// never serialized to clients or logs.
type GitTestRequest struct {
	IntegrationID uuid.UUID
	Type          Type
	BaseURL       string
	Credentials   map[string]string
	Settings      json.RawMessage
}
