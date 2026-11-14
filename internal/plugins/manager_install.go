package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// Validate parses and validates a server-side package without executing it.
func (m *Manager) Validate(ctx context.Context, principal authorization.Principal, packagePath string) (Validation, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return Validation{}, err
	}
	prepared, err := openPackage(m.root, packagePath)
	if err != nil {
		return Validation{Valid: false, Findings: []Finding{{
			Path: packagePath, Code: "invalid_package", Message: err.Error(), Severity: "error",
		}}}, nil
	}
	defer prepared.close()

	manifest, findings, digest, err := packageManifest(prepared, m.allowExec, m.allowTrustedJS)
	if err != nil {
		return Validation{}, err
	}
	validation := Validation{
		Valid:        !hasErrorFinding(findings),
		Findings:     findings,
		ID:           manifest.ID,
		Name:         manifest.Name,
		Version:      manifest.Version,
		APIVersion:   manifest.APIVersion,
		BackendMode:  manifest.Backend.Mode,
		FrontendMode: manifest.Frontend.Mode,
		Capabilities: append([]Capability(nil), manifest.Capabilities...),
		Permissions:  append([]PermissionDecl(nil), manifest.Permissions...),
	}
	if validation.Valid {
		if err := m.registryLookupConflict(manifest); err != nil {
			validation.Valid = false
			validation.Findings = append(validation.Findings, Finding{
				Path: "permissions", Code: "permission_conflict", Message: err.Error(), Severity: "error",
			})
		}
	}
	_ = digest
	_ = writeAudit(ctx, m.db.Pool, principal, auditActionPluginValidate, string(manifest.ID), map[string]any{
		"valid":    validation.Valid,
		"findings": len(validation.Findings),
	})
	return validation, nil
}

// Install validates and activates a package in the registry. It never enables
// the plugin and never executes package code.
func (m *Manager) Install(ctx context.Context, principal authorization.Principal, in InstallRequest) (*Installed, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, err
	}
	prepared, err := openPackage(m.root, in.PackagePath)
	if err != nil {
		return nil, err
	}
	defer prepared.close()

	manifest, findings, digest, err := packageManifest(prepared, m.allowExec, m.allowTrustedJS)
	if err != nil {
		return nil, err
	}
	trustedJSApproved, err := m.approveTrustedJS(manifest, in.ApproveTrustedJS)
	if err != nil {
		return nil, err
	}
	if hasErrorFinding(findings) {
		return nil, fmt.Errorf("%w: package validation failed: %s", ErrInvalidPackage, findingSummary(findings))
	}
	if err := approvalsMatch(manifest, in.ApproveCapabilities, in.ApprovePermissions); err != nil {
		return nil, err
	}
	if err := m.registryLookupConflict(manifest); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}
	if err := m.ensureInstallDependencies(ctx, manifest); err != nil {
		return nil, err
	}

	var existing string
	err = m.db.Pool.QueryRow(ctx, `SELECT version FROM plugins WHERE id = $1`, string(manifest.ID)).Scan(&existing)
	if err == nil {
		return nil, ErrAlreadyInstalled
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("plugins: check existing plugin: %w", err)
	}

	destination := filepath.Join(m.root, string(manifest.ID), manifest.Version)
	if _, err := os.Lstat(destination); err == nil {
		return nil, fmt.Errorf("%w: package destination already exists", ErrAlreadyInstalled)
	}
	if err := copyPackage(prepared.dir, destination); err != nil {
		return nil, err
	}

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		_ = os.RemoveAll(filepath.Join(m.root, string(manifest.ID)))
		return nil, fmt.Errorf("plugins: encode manifest: %w", err)
	}

	tx, err := m.db.Pool.Begin(ctx)
	if err != nil {
		_ = os.RemoveAll(filepath.Join(m.root, string(manifest.ID)))
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := m.now()
	if _, err := tx.Exec(ctx, `
		INSERT INTO plugins
			(id, name, version, api_version, status, manifest, manifest_digest, package_path, installed_by, trusted_js_approved, installed_at, updated_at)
		VALUES ($1, $2, $3, $4, 'installed', $5, $6, $7, $8, $9, $10, $10)
	`, string(manifest.ID), manifest.Name, manifest.Version, manifest.APIVersion, manifestJSON, digest, destination, principal.UserID, trustedJSApproved, now); err != nil {
		_ = os.RemoveAll(filepath.Join(m.root, string(manifest.ID)))
		if isUniqueViolation(err) {
			return nil, ErrAlreadyInstalled
		}
		return nil, fmt.Errorf("plugins: insert plugin: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO plugin_versions (plugin_id, version, manifest_digest, package_path, installed_at)
		VALUES ($1, $2, $3, $4, $5)
	`, string(manifest.ID), manifest.Version, digest, destination, now); err != nil {
		_ = os.RemoveAll(filepath.Join(m.root, string(manifest.ID)))
		return nil, fmt.Errorf("plugins: insert plugin version: %w", err)
	}
	if err := insertPluginPermissions(ctx, tx, manifest); err != nil {
		_ = os.RemoveAll(filepath.Join(m.root, string(manifest.ID)))
		return nil, err
	}
	if err := writeAudit(ctx, tx, principal, auditActionPluginInstall, string(manifest.ID), map[string]any{
		"version":      manifest.Version,
		"digest":       digest,
		"capabilities": manifest.Capabilities,
		"backendMode":  manifest.Backend.Mode,
	}); err != nil {
		_ = os.RemoveAll(filepath.Join(m.root, string(manifest.ID)))
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		_ = os.RemoveAll(filepath.Join(m.root, string(manifest.ID)))
		return nil, fmt.Errorf("plugins: commit install: %w", err)
	}
	if err := registerManifestPermissions(m.registry, manifest); err != nil {
		// Compensate: a plugin whose permissions cannot be registered must not
		// remain installed.
		_, _ = m.db.Pool.Exec(ctx, `DELETE FROM plugins WHERE id = $1`, string(manifest.ID))
		_ = os.RemoveAll(filepath.Join(m.root, string(manifest.ID)))
		return nil, fmt.Errorf("plugins: register permissions: %w", err)
	}

	return m.loadInstalled(ctx, manifest.ID)
}

func (m *Manager) registryLookupConflict(manifest Manifest) error {
	for _, declaration := range manifest.Permissions {
		existing, ok := m.registry.Lookup(declaration.ID)
		if !ok {
			continue
		}
		if existing.Source != string(manifest.ID) {
			return fmt.Errorf("permission %s is already registered by %s", declaration.ID, existing.Source)
		}
		// The same plugin may validate an upgrade package with identical
		// definitions. A changed scope still conflicts because the registry has
		// no safe unregister operation.
		if len(existing.Scopes) != 1 || existing.Scopes[0] != authorization.PermissionScope(declaration.Scope) {
			return fmt.Errorf("permission %s is already registered with a different scope", declaration.ID)
		}
	}
	return nil
}

// approveTrustedJS enforces both operator policy and the per-install explicit
// acknowledgement. A manifest can never select trusted mode silently.
func (m *Manager) approveTrustedJS(manifest Manifest, approved bool) (bool, error) {
	if manifest.Frontend.Mode != FrontendTrustedJS {
		if approved {
			return false, fmt.Errorf("%w: approveTrustedJS is only valid for a trusted-js manifest", ErrValidation)
		}
		return false, nil
	}
	if !m.allowTrustedJS {
		return false, ErrTrustedJSDisabled
	}
	if !approved {
		return false, ErrTrustedJSNotApproved
	}
	return true, nil
}

func approvalsMatch(manifest Manifest, capabilities []Capability, permissions []authorization.Permission) error {
	requestedCapabilities := make(map[Capability]struct{}, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		requestedCapabilities[capability] = struct{}{}
	}
	approvedCapabilities := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if _, exists := approvedCapabilities[capability]; exists {
			return fmt.Errorf("%w: duplicate approved capability %s", ErrValidation, capability)
		}
		approvedCapabilities[capability] = struct{}{}
	}
	if len(requestedCapabilities) != len(approvedCapabilities) {
		return fmt.Errorf("%w: approveCapabilities must exactly cover the manifest capabilities", ErrValidation)
	}
	for capability := range requestedCapabilities {
		if _, ok := approvedCapabilities[capability]; !ok {
			return fmt.Errorf("%w: capability %s was not approved", ErrValidation, capability)
		}
	}

	requestedPermissions := make(map[authorization.Permission]struct{}, len(manifest.Permissions))
	for _, declaration := range manifest.Permissions {
		requestedPermissions[declaration.ID] = struct{}{}
	}
	approvedPermissions := make(map[authorization.Permission]struct{}, len(permissions))
	for _, permission := range permissions {
		if _, exists := approvedPermissions[permission]; exists {
			return fmt.Errorf("%w: duplicate approved permission %s", ErrValidation, permission)
		}
		approvedPermissions[permission] = struct{}{}
	}
	if len(requestedPermissions) != len(approvedPermissions) {
		return fmt.Errorf("%w: approvePermissions must exactly cover the manifest permissions", ErrValidation)
	}
	for permission := range requestedPermissions {
		if _, ok := approvedPermissions[permission]; !ok {
			return fmt.Errorf("%w: permission %s was not approved", ErrValidation, permission)
		}
	}
	return nil
}

func insertPluginPermissions(ctx context.Context, tx pgx.Tx, manifest Manifest) error {
	for _, declaration := range manifest.Permissions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO plugin_permissions (plugin_id, permission, scopes, description)
			VALUES ($1, $2, $3, $4)
		`, string(manifest.ID), string(declaration.ID), []string{declaration.Scope}, string(declaration.ID)); err != nil {
			return fmt.Errorf("plugins: insert permission: %w", err)
		}
	}
	return nil
}

func registerManifestPermissions(registry authorization.PermissionRegistry, manifest Manifest) error {
	definitions := make([]authorization.PermissionDefinition, 0, len(manifest.Permissions))
	for _, declaration := range manifest.Permissions {
		definitions = append(definitions, authorization.PermissionDefinition{
			Permission:  declaration.ID,
			Source:      string(manifest.ID),
			Scopes:      []authorization.PermissionScope{authorization.PermissionScope(declaration.Scope)},
			Description: string(declaration.ID),
		})
	}
	if len(definitions) == 0 {
		return nil
	}
	return authorization.RegisterPluginPermissions(registry, string(manifest.ID), definitions)
}

func findingSummary(findings []Finding) string {
	parts := make([]string, 0, 3)
	for _, finding := range findings {
		if finding.Severity != "error" {
			continue
		}
		parts = append(parts, finding.Path+": "+finding.Message)
		if len(parts) == 3 {
			parts = append(parts, "...")
			break
		}
	}
	return strings.Join(parts, "; ")
}
