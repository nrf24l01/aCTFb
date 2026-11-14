// Package plugins implements the installable, process-isolated plugin system.
//
// Core never loads plugin Go code into its own process. Plugins are discovered
// and installed from a filesystem/OCI package, run as separate processes or
// containers, and communicate over the versioned http-v1 protocol. The types in
// this file are the frozen shared contract; see docs/remaining-stages-contracts.md
// §5.6.
package plugins

import (
	"encoding/json"
	"time"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// ID is a stable, namespaced plugin identifier such as "example.hello".
type ID string

// Capability is a core data/action capability explicitly approved at install.
type Capability string

const (
	CapabilityBoardsRead      Capability = "core.boards.read"
	CapabilitySubmissionsRead Capability = "core.submissions.read"
	CapabilityEventsSubscribe Capability = "core.events.subscribe"
)

// Status is the persisted desired lifecycle state.
type Status string

const (
	StatusDiscovered   Status = "discovered"
	StatusInstalled    Status = "installed"
	StatusEnabled      Status = "enabled"
	StatusDisabled     Status = "disabled"
	StatusIncompatible Status = "incompatible"
	StatusUnhealthy    Status = "unhealthy"
	StatusFailed       Status = "failed"
)

// BackendMode selects how the backend process is started.
type BackendMode string

const (
	BackendOCI  BackendMode = "oci"
	BackendExec BackendMode = "exec"
)

// FrontendMode selects how a plugin UI is mounted.
type FrontendMode string

const (
	FrontendIframe    FrontendMode = "iframe"
	FrontendTrustedJS FrontendMode = "trusted-js"
)

// Backend is the manifest-declared backend command/container contract.
type Backend struct {
	Mode           BackendMode `yaml:"mode" json:"mode"`
	Image          string      `yaml:"image,omitempty" json:"image,omitempty"`
	Entrypoint     string      `yaml:"entrypoint,omitempty" json:"entrypoint,omitempty"`
	Protocol       string      `yaml:"protocol" json:"protocol"`
	HealthPath     string      `yaml:"healthPath,omitempty" json:"healthPath,omitempty"`
	TimeoutSeconds int         `yaml:"timeoutSeconds,omitempty" json:"timeoutSeconds,omitempty"`
}

// Frontend is the manifest-declared browser extension point.
type Frontend struct {
	Mode  FrontendMode `yaml:"mode" json:"mode"`
	Entry string       `yaml:"entry,omitempty" json:"entry,omitempty"`
}

// PermissionDecl declares one namespaced permission requested by a plugin.
type PermissionDecl struct {
	ID    authorization.Permission `yaml:"id" json:"id"`
	Scope string                   `yaml:"scope" json:"scope"`
}

// CheckerDecl declares one versioned text-only checker operation. A checker is
// reachable only through an enabled, compatible plugin and a declared route; it
// is not a free-form callback URL.
type CheckerDecl struct {
	Key            string                   `yaml:"key" json:"key"`
	ExtensionKey   string                   `yaml:"extensionKey" json:"extensionKey"`
	Route          string                   `yaml:"route" json:"route"`
	Permission     authorization.Permission `yaml:"permission" json:"permission"`
	Scope          string                   `yaml:"scope" json:"scope"`
	TimeoutSeconds int                      `yaml:"timeoutSeconds,omitempty" json:"timeoutSeconds,omitempty"`
	MaxTextBytes   int                      `yaml:"maxTextBytes,omitempty" json:"maxTextBytes,omitempty"`
}

// Route declares one namespaced API route under /api/v2/plugins/{id}.
type Route struct {
	Method     string                   `yaml:"method" json:"method"`
	Path       string                   `yaml:"path" json:"path"`
	Permission authorization.Permission `yaml:"permission" json:"permission"`
	Scope      string                   `yaml:"scope" json:"scope"`
	UI         bool                     `yaml:"ui,omitempty" json:"ui,omitempty"`
}

// Manifest is the validated, immutable plugin package manifest.
type Manifest struct {
	ID           ID               `yaml:"id" json:"id"`
	Name         string           `yaml:"name" json:"name"`
	Version      string           `yaml:"version" json:"version"`
	APIVersion   string           `yaml:"apiVersion" json:"apiVersion"`
	Description  string           `yaml:"description,omitempty" json:"description,omitempty"`
	Author       string           `yaml:"author,omitempty" json:"author,omitempty"`
	Backend      Backend          `yaml:"backend" json:"backend"`
	Frontend     Frontend         `yaml:"frontend" json:"frontend"`
	Dependencies []Dependency     `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Permissions  []PermissionDecl `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	Capabilities []Capability     `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Routes       []Route          `yaml:"routes,omitempty" json:"routes,omitempty"`
	Checkers     []CheckerDecl    `yaml:"checkers,omitempty" json:"checkers,omitempty"`
	Events       []string         `yaml:"events,omitempty" json:"events,omitempty"`
	ConfigSchema json.RawMessage  `yaml:"configSchema,omitempty" json:"configSchema,omitempty"`
	Migrations   string           `yaml:"migrations,omitempty" json:"migrations,omitempty"`
	Assets       string           `yaml:"assets,omitempty" json:"assets,omitempty"`
	Checksum     string           `yaml:"checksum,omitempty" json:"checksum,omitempty"`
}

// Health is the last observed backend health result.
type Health struct {
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

// Installed is the persisted plugin registry row plus observed health.
type Installed struct {
	ID                ID        `json:"id"`
	Version           string    `json:"version"`
	Status            Status    `json:"status"`
	Health            Health    `json:"health"`
	Manifest          Manifest  `json:"manifest"`
	TrustedJSApproved bool      `json:"trustedJSApproved"`
	InstalledAt       time.Time `json:"installedAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// InstallRequest is the Go service input. It deliberately has no Enable field:
// installation always leaves the plugin in status=installed.
type InstallRequest struct {
	PackagePath         string
	ApproveCapabilities []Capability
	ApprovePermissions  []authorization.Permission
	// ApproveTrustedJS is the explicit operator acknowledgement that a
	// trusted-js frontend runs with the platform origin's capabilities. It is
	// never inferred from the manifest or an operator flag.
	ApproveTrustedJS bool
}

// Visible is the participant-safe plugin shell metadata.
type Visible struct {
	ID        ID            `json:"id"`
	Name      string        `json:"name"`
	Menu      []VisibleItem `json:"menu"`
	Pages     []VisibleItem `json:"pages"`
	TrustedJS *TrustedJS    `json:"trustedJS,omitempty"`
}

// VisibleItem is one iframe/trusted page/menu entry. TrustedJS pages carry no
// iframe URL; the browser fetches and verifies the bundle explicitly.
type VisibleItem struct {
	Path      string `json:"path"`
	Label     string `json:"label"`
	IframeURL string `json:"iframeUrl"`
}

// TrustedJS is the explicit full-trust frontend contract. The bundle executes
// in the platform origin and is not sandboxed; Integrity is sha256:<hex> of the
// exact bytes served by URL.
type TrustedJS struct {
	Entry     string `json:"entry"`
	URL       string `json:"url"`
	Integrity string `json:"integrity"`
}

// Validation is the result of a non-executing package validation. The manifest
// metadata fields are additive: the admin UI can present the exact capability
// and permission set for explicit approval without guessing or approving all.
type Validation struct {
	Valid        bool             `json:"valid"`
	Findings     []Finding        `json:"findings"`
	ID           ID               `json:"id,omitempty"`
	Name         string           `json:"name,omitempty"`
	Version      string           `json:"version,omitempty"`
	APIVersion   string           `json:"apiVersion,omitempty"`
	BackendMode  BackendMode      `json:"backendMode,omitempty"`
	FrontendMode FrontendMode     `json:"frontendMode,omitempty"`
	Dependencies []Dependency     `json:"dependencies,omitempty"`
	Capabilities []Capability     `json:"capabilities,omitempty"`
	Permissions  []PermissionDecl `json:"permissions,omitempty"`
}

// Finding is one validation error or warning.
type Finding struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// Valid reports whether no error-severity finding exists.
func (v Validation) ValidResult() bool {
	for _, finding := range v.Findings {
		if finding.Severity == "error" {
			return false
		}
	}
	return true
}
