package plugins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/plugins/process"
	"github.com/nrf24l01/actfb/internal/plugins/protocol"
)

// Enable provisions the private schema, applies migrations before any request
// is served, rotates the service token, starts the isolated backend and waits
// for health. Installation never calls this method.
func (m *Manager) Enable(ctx context.Context, principal authorization.Principal, id ID) (*Installed, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, err
	}
	// Start/commit is an owned state transition. A client disconnect after the
	// request is accepted must not leave a started backend with an installed
	// status row.
	opCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	installed, err := m.loadInstalled(opCtx, id)
	if err != nil {
		return nil, err
	}
	if installed.Manifest.APIVersion != PluginAPIVersion {
		m.setStatus(opCtx, id, StatusIncompatible)
		return nil, ErrIncompatible
	}
	if installed.Status == StatusEnabled && m.runtimeFor(id) != nil {
		return installed, nil
	}
	if installed.Manifest.Backend.Mode == BackendExec && !m.allowExec {
		return nil, ErrExecDisabled
	}
	if err := m.ensureEnableDependencies(opCtx, *installed); err != nil {
		m.logger.Warn("plugin enable preflight failed", "plugin", id, "error", err)
		return nil, err
	}
	// A stale runtime (crash/health failure or an enabled row recovered after a
	// core restart) must be stopped before a fresh start.
	if m.runtimeFor(id) != nil {
		if err := m.stopRuntime(opCtx, id); err != nil {
			return nil, err
		}
	}
	if err := m.startInstalled(opCtx, *installed); err != nil {
		m.logger.Warn("plugin start failed", "plugin", id, "error", err)
		m.setStatusFailed(opCtx, id, err)
		return nil, err
	}
	if err := m.setEnabled(opCtx, principal, id, true); err != nil {
		m.logger.Warn("plugin enable status commit failed", "plugin", id, "error", err)
		_ = m.stopRuntime(opCtx, id)
		return nil, err
	}
	return m.loadInstalled(opCtx, id)
}

// Disable stops the backend, closes routes and revokes every live delegation,
// while preserving private schema data and encrypted settings.
func (m *Manager) Disable(ctx context.Context, principal authorization.Principal, id ID) (*Installed, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, err
	}
	if _, err := m.loadInstalled(ctx, id); err != nil {
		return nil, err
	}
	if err := m.ensureDisableAllowed(ctx, id); err != nil {
		return nil, err
	}
	if err := m.stopRuntime(ctx, id); err != nil {
		return nil, err
	}
	if err := m.issuer.RevokePlugin(ctx, id); err != nil {
		m.logger.Warn("failed to revoke plugin delegations", "plugin", id, "error", err)
	}
	if err := m.setEnabled(ctx, principal, id, false); err != nil {
		return nil, err
	}
	return m.loadInstalled(ctx, id)
}

// Remove deletes the registry row and installed package versions but retains
// the private plugin schema/database and data directory.
func (m *Manager) Remove(ctx context.Context, principal authorization.Principal, id ID) error {
	if err := m.requireManage(ctx, principal); err != nil {
		return err
	}
	if _, err := m.loadInstalled(ctx, id); err != nil {
		return err
	}
	if err := m.ensureRemoveAllowed(ctx, id); err != nil {
		return err
	}
	if err := m.stopRuntime(ctx, id); err != nil {
		return err
	}
	if err := m.issuer.RevokePlugin(ctx, id); err != nil {
		m.logger.Warn("failed to revoke plugin delegations", []any{"plugin", id, "error", err}...)
	}

	rows, err := m.db.Pool.Query(ctx, `SELECT package_path FROM plugin_versions WHERE plugin_id = $1`, string(id))
	if err != nil {
		return fmt.Errorf("plugins: list installed package paths: %w", err)
	}
	paths := make([]string, 0, 2)
	for rows.Next() {
		var packagePath string
		if err := rows.Scan(&packagePath); err != nil {
			rows.Close()
			return err
		}
		paths = append(paths, packagePath)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := m.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := writeAudit(ctx, tx, principal, auditActionPluginRemove, string(id), map[string]any{
		"privateDataRetained": true,
	}); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM plugins WHERE id = $1`, string(id))
	if err != nil {
		return fmt.Errorf("plugins: remove plugin: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("plugins: commit remove: %w", err)
	}
	for _, packagePath := range paths {
		if withinRoot(m.root, packagePath) {
			_ = os.RemoveAll(packagePath)
		}
	}
	return nil
}

// Upgrade installs a greater version, rejects any newly requested permissions
// or capabilities, applies migrations before switching traffic, and keeps the
// previous version running if migration/start fails.
func (m *Manager) Upgrade(ctx context.Context, principal authorization.Principal, id ID, packagePath string) (*Installed, error) {
	return m.UpgradeWithApproval(ctx, principal, id, packagePath, false)
}

// UpgradeWithApproval is Upgrade plus the explicit trusted-js risk
// acknowledgement required when the new package uses that frontend mode.
func (m *Manager) UpgradeWithApproval(ctx context.Context, principal authorization.Principal, id ID, packagePath string, approveTrustedJS bool) (*Installed, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, err
	}
	current, err := m.loadInstalled(ctx, id)
	if err != nil {
		return nil, err
	}
	prepared, err := openPackage(m.root, packagePath)
	if err != nil {
		return nil, err
	}
	defer prepared.close()

	manifest, findings, digest, err := packageManifest(prepared, m.allowExec, m.allowTrustedJS)
	if err != nil {
		return nil, err
	}
	trustedJSApproved, err := m.approveTrustedJS(manifest, approveTrustedJS)
	if err != nil {
		return nil, err
	}
	if hasErrorFinding(findings) {
		return nil, fmt.Errorf("%w: package validation failed: %s", ErrInvalidPackage, findingSummary(findings))
	}
	if manifest.ID != id {
		return nil, fmt.Errorf("%w: package id %s does not match %s", ErrValidation, manifest.ID, id)
	}
	if compareVersions(manifest.Version, current.Version) <= 0 {
		return nil, fmt.Errorf("%w: upgrade version must be greater than %s", ErrValidation, current.Version)
	}
	if err := upgradeRequestsAreSubset(*current, manifest); err != nil {
		return nil, err
	}
	if err := m.ensureUpgradeDependencies(ctx, *current, manifest); err != nil {
		return nil, err
	}

	wasEnabled := current.Status == StatusEnabled
	if wasEnabled {
		if err := m.stopRuntime(ctx, id); err != nil {
			return nil, err
		}
	}
	destination := filepath.Join(m.root, string(id), manifest.Version)
	if _, err := os.Lstat(destination); err == nil {
		return nil, fmt.Errorf("%w: package destination already exists", ErrAlreadyInstalled)
	}
	if err := copyPackage(prepared.dir, destination); err != nil {
		if wasEnabled {
			_ = m.startInstalled(ctx, *current)
		}
		return nil, err
	}
	dsn, _, err := m.ensurePrivateDatabase(ctx, id)
	if err != nil {
		_ = os.RemoveAll(destination)
		if wasEnabled {
			_ = m.startInstalled(ctx, *current)
		}
		return nil, err
	}
	if err := m.runPluginMigrations(ctx, id, destination, manifest.Migrations, dsn); err != nil {
		_ = os.RemoveAll(destination)
		if wasEnabled {
			_ = m.startInstalled(ctx, *current)
		}
		return nil, fmt.Errorf("%w: plugin migrations were rolled back; previous version remains active: %v", ErrValidation, err)
	}

	manifestJSON, err := jsonMarshal(manifest)
	if err != nil {
		_ = os.RemoveAll(destination)
		return nil, err
	}
	now := m.now()
	tx, err := m.db.Pool.Begin(ctx)
	if err != nil {
		_ = os.RemoveAll(destination)
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE plugins
		SET name = $2, version = $3, api_version = $4, manifest = $5,
		    manifest_digest = $6, package_path = $7, trusted_js_approved = $8, updated_at = $9
		WHERE id = $1
	`, string(id), manifest.Name, manifest.Version, manifest.APIVersion, manifestJSON, digest, destination, trustedJSApproved, now); err != nil {
		_ = os.RemoveAll(destination)
		return nil, fmt.Errorf("plugins: update plugin version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO plugin_versions (plugin_id, version, manifest_digest, package_path, installed_at)
		VALUES ($1, $2, $3, $4, $5)
	`, string(id), manifest.Version, digest, destination, now); err != nil {
		_ = os.RemoveAll(destination)
		return nil, fmt.Errorf("plugins: insert plugin version: %w", err)
	}
	if err := writeAudit(ctx, tx, principal, auditActionPluginUpgrade, string(id), map[string]any{
		"fromVersion": current.Version,
		"toVersion":   manifest.Version,
		"digest":      digest,
	}); err != nil {
		_ = os.RemoveAll(destination)
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		_ = os.RemoveAll(destination)
		return nil, fmt.Errorf("plugins: commit upgrade: %w", err)
	}

	if wasEnabled {
		updated, loadErr := m.loadInstalled(ctx, id)
		if loadErr != nil {
			return nil, loadErr
		}
		if startErr := m.startInstalled(ctx, *updated); startErr != nil {
			// Honest rollback: restore the previous registry/package and restart
			// it. The new migration batch was already committed; rollback of
			// plugin data migrations is not promised.
			_ = m.rollbackUpgrade(ctx, id, *current, destination)
			return nil, fmt.Errorf("%w: new version failed to start; previous version restarted where possible: %v", ErrValidation, startErr)
		}
	}
	return m.loadInstalled(ctx, id)
}

func (m *Manager) rollbackUpgrade(ctx context.Context, id ID, previous Installed, newDestination string) error {
	manifestJSON, err := jsonMarshal(previous.Manifest)
	if err != nil {
		return err
	}
	digest := ""
	if err := m.db.Pool.QueryRow(ctx, `
		SELECT manifest_digest FROM plugin_versions WHERE plugin_id = $1 AND version = $2
	`, string(id), previous.Version).Scan(&digest); err != nil {
		computed, digestErr := ManifestDigest(previous.Manifest)
		if digestErr != nil {
			return digestErr
		}
		digest = computed
	}
	_, err = m.db.Pool.Exec(ctx, `
		UPDATE plugins
		SET name = $2, version = $3, api_version = $4, manifest = $5,
		    manifest_digest = $6, package_path = $7,
		    trusted_js_approved = $8, updated_at = clock_timestamp()
		WHERE id = $1
	`, string(id), previous.Manifest.Name, previous.Version, previous.Manifest.APIVersion,
		manifestJSON, digest, filepath.Join(m.root, string(id), previous.Version), previous.TrustedJSApproved)
	if err == nil {
		_ = os.RemoveAll(newDestination)
		_ = m.startInstalled(ctx, previous)
	}
	return err
}

func upgradeRequestsAreSubset(current Installed, next Manifest) error {
	currentCapabilities := make(map[Capability]struct{}, len(current.Manifest.Capabilities))
	for _, capability := range current.Manifest.Capabilities {
		currentCapabilities[capability] = struct{}{}
	}
	for _, capability := range next.Capabilities {
		if _, ok := currentCapabilities[capability]; !ok {
			return fmt.Errorf("%w: upgrade may not add capability %s; remove and reinstall with explicit approval", ErrValidation, capability)
		}
	}
	currentPermissions := make(map[authorization.Permission]string, len(current.Manifest.Permissions))
	for _, declaration := range current.Manifest.Permissions {
		currentPermissions[declaration.ID] = declaration.Scope
	}
	nextPermissions := make(map[authorization.Permission]string, len(next.Permissions))
	for _, declaration := range next.Permissions {
		nextPermissions[declaration.ID] = declaration.Scope
	}
	if len(currentPermissions) != len(nextPermissions) {
		return fmt.Errorf("%w: upgrades may not add or remove declared permissions", ErrValidation)
	}
	for permission, scope := range currentPermissions {
		if nextPermissions[permission] != scope {
			return fmt.Errorf("%w: upgrade may not change permission %s", ErrValidation, permission)
		}
	}
	nextCheckers := make(map[string]CheckerDecl, len(next.Checkers))
	for _, checker := range next.Checkers {
		nextCheckers[checker.Key] = checker
	}
	for _, checker := range current.Manifest.Checkers {
		candidate, ok := nextCheckers[checker.Key]
		if !ok || candidate.ExtensionKey != checker.ExtensionKey ||
			candidate.Route != checker.Route || candidate.Permission != checker.Permission || candidate.Scope != checker.Scope {
			return fmt.Errorf("%w: upgrade may not remove or change checker %s", ErrValidation, checker.Key)
		}
	}
	return nil
}

// Logs returns bounded backend logs with service tokens redacted.
func (m *Manager) Logs(ctx context.Context, principal authorization.Principal, id ID, limit int) ([]string, error) {
	if err := m.requireManage(ctx, principal); err != nil {
		return nil, err
	}
	if _, err := m.loadInstalled(ctx, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	runtime := m.runtimeFor(id)
	if runtime == nil || runtime.handle == nil {
		return []string{}, nil
	}
	runner := m.runnerFor(runtime.installed.Manifest)
	if runner == nil {
		return []string{}, nil
	}
	lines, err := runner.Logs(ctx, runtime.handle, limit)
	if err != nil {
		return nil, fmt.Errorf("plugins: read logs: %w", err)
	}
	token := runtime.token
	for index, line := range lines {
		if token != "" {
			line = strings.ReplaceAll(line, token, "[redacted]")
		}
		lines[index] = line
	}
	return lines, nil
}

// RecoverEnabled restarts previously enabled plugins in dependency order. It
// re-runs the same dependency and trusted-js gates as Enable, rotates every
// service token (a pre-restart token is never reused), and marks individual
// failures without failing core startup.
func (m *Manager) RecoverEnabled(ctx context.Context) []error {
	rows, err := m.db.Pool.Query(ctx, `
		SELECT id, version, status, manifest, trusted_js_approved, installed_at, updated_at
		FROM plugins WHERE status = 'enabled' ORDER BY id
	`)
	if err != nil {
		return []error{fmt.Errorf("plugins: list enabled for recovery: %w", err)}
	}
	items := make([]Installed, 0)
	for rows.Next() {
		installed, scanErr := scanInstalled(rows)
		if scanErr != nil {
			rows.Close()
			return []error{scanErr}
		}
		items = append(items, installed)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return []error{err}
	}
	failures := make([]error, 0)
	ordered, orderErrors := orderEnabledByDependencies(items)
	failures = append(failures, orderErrors...)
	for _, installed := range ordered {
		if err := m.ensureEnableDependencies(ctx, installed); err != nil {
			m.setStatusFailed(ctx, installed.ID, err)
			failures = append(failures, fmt.Errorf("%s: %w", installed.ID, err))
			continue
		}
		if err := m.startInstalled(ctx, installed); err != nil {
			m.setStatusFailed(ctx, installed.ID, err)
			failures = append(failures, fmt.Errorf("%s: %w", installed.ID, err))
		}
	}
	return failures
}

// orderEnabledByDependencies returns enabled plugins after their enabled
// dependencies. Cycles are reported and excluded rather than started in an
// arbitrary order.
func orderEnabledByDependencies(items []Installed) ([]Installed, []error) {
	byID := make(map[ID]Installed, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	state := make(map[ID]uint8, len(items))
	ordered := make([]Installed, 0, len(items))
	failures := make([]error, 0)
	var visit func(Installed) bool
	visit = func(item Installed) bool {
		switch state[item.ID] {
		case 1:
			failures = append(failures, dependencyError(ErrDependencyCycle, "recovery cycle includes %s", item.ID))
			return false
		case 2:
			return true
		}
		state[item.ID] = 1
		ok := true
		for _, dependency := range item.Manifest.Dependencies {
			target, present := byID[dependency.ID]
			if !present {
				continue
			}
			if !visit(target) {
				ok = false
			}
		}
		state[item.ID] = 2
		if ok {
			ordered = append(ordered, item)
		}
		return ok
	}
	for _, item := range items {
		visit(item)
	}
	return ordered, failures
}

func (m *Manager) startInstalled(ctx context.Context, installed Installed) error {
	manifest := installed.Manifest
	if manifest.Backend.Mode == BackendExec && !m.allowExec {
		return ErrExecDisabled
	}
	packagePath := filepath.Join(m.root, string(installed.ID), installed.Version)
	if err := m.verifyInstalledPackage(ctx, installed.ID, packagePath); err != nil {
		return err
	}
	if err := m.checkCheckerConflicts(manifest); err != nil {
		return err
	}
	dsn, schema, err := m.ensurePrivateDatabase(ctx, installed.ID)
	if err != nil {
		return err
	}
	privateIdentity, err := m.ensurePrivateIdentity(ctx, installed.ID)
	if err != nil {
		return err
	}
	if err := m.runPluginMigrations(ctx, installed.ID, filepath.Join(m.root, string(installed.ID), installed.Version), manifest.Migrations, dsn); err != nil {
		return err
	}
	token, err := m.runtimeToken(ctx, installed.ID)
	if err != nil {
		return err
	}
	runner := m.runnerFor(manifest)
	if runner == nil {
		if manifest.Backend.Mode == BackendOCI {
			return errors.New("plugins: no trusted plugin-runner is configured for OCI backends; core does not open the Docker socket")
		}
		return errors.New("plugins: no backend runner is configured")
	}

	dataDir := filepath.Join(m.root, string(installed.ID), "data")
	socketPath := ""
	if manifest.Backend.Mode == BackendExec {
		socketPath = filepath.Join(m.root, string(installed.ID), "run", "plugin.sock")
	}
	env := map[string]string{
		"ACTFB_PLUGIN_ID":            string(installed.ID),
		"ACTFB_PLUGIN_SERVICE_TOKEN": token,
		"ACTFB_PLUGIN_DATA_DIR":      dataDir,
	}
	if socketPath != "" {
		env["ACTFB_PLUGIN_SOCKET"] = socketPath
	}
	if m.coreSDKURL != "" {
		env["ACTFB_PLUGIN_CORE_SDK_URL"] = m.coreSDKURL
	}
	if dsn != "" {
		env["ACTFB_PLUGIN_DSN"] = dsn
		env["ACTFB_PLUGIN_SCHEMA"] = schema
	}
	timeout := time.Duration(manifest.Backend.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = m.httpTimeout
	}
	spec := process.Spec{
		PluginID:      string(installed.ID),
		Mode:          string(manifest.Backend.Mode),
		Image:         manifest.Backend.Image,
		Entrypoint:    manifest.Backend.Entrypoint,
		WorkDir:       packagePath,
		SocketPath:    socketPath,
		DataDir:       dataDir,
		VolumeName:    privateIdentity.Volume,
		ContainerPort: 8080,
		Timeout:       timeout,
		Env:           env,
	}
	handle, err := runner.Start(ctx, spec)
	if err != nil {
		return fmt.Errorf("plugins: start backend: %w", err)
	}
	clientOptions := protocol.ClientOptions{
		BaseURL:      handle.BaseURL,
		SocketPath:   handle.SocketPath,
		ServiceToken: token,
		Timeout:      timeout,
		MaxBodyBytes: m.maxBody,
	}
	if handle.SocketPath == "" && handle.BaseURL == "" {
		_ = runner.Stop(ctx, handle)
		return errors.New("plugins: backend runner returned no endpoint")
	}
	client, err := protocol.NewClient(clientOptions)
	if err != nil {
		_ = runner.Stop(ctx, handle)
		return err
	}
	identity := Identity{
		PluginID:     installed.ID,
		Capabilities: append([]Capability(nil), manifest.Capabilities...),
		Permissions:  manifestPermissionIDs(manifest),
	}
	runtime := &runtimeBackend{
		installed: installed,
		identity:  identity,
		token:     token,
		handle:    handle,
		client:    client,
		dsn:       dsn,
		schema:    schema,
		startedAt: m.now(),
		health:    Health{Status: "unknown", CheckedAt: m.now()},
	}
	m.mu.Lock()
	if existing := m.runtime[installed.ID]; existing != nil {
		m.mu.Unlock()
		_ = runner.Stop(ctx, handle)
		return fmt.Errorf("plugins: %s is already running", installed.ID)
	}
	m.runtime[installed.ID] = runtime
	m.mu.Unlock()

	// The enabled plugin must not fail startup merely because the admin HTTP
	// request disconnected. The health wait is bounded independently.
	if err := waitForHealth(context.WithoutCancel(ctx), client, manifest.Backend.HealthPath, startupTimeout(timeout)); err != nil {
		_ = m.stopRuntime(context.Background(), installed.ID)
		return fmt.Errorf("%w: backend health check failed: %v", ErrBackendUnavailable, err)
	}
	runtime.setHealth(Health{Status: "healthy", CheckedAt: m.now()})
	runtimeCtx, cancel := context.WithCancel(context.Background())
	runtime.cancel = cancel
	if err := m.startEventConsumers(runtimeCtx, runtime); err != nil {
		cancel()
		_ = m.stopRuntime(context.Background(), installed.ID)
		return err
	}
	m.startMonitor(runtimeCtx, runtime)
	return nil
}

func (m *Manager) verifyInstalledPackage(ctx context.Context, id ID, packagePath string) error {
	var storedDigest string
	if err := m.db.Pool.QueryRow(ctx, `
		SELECT manifest_digest FROM plugin_versions WHERE plugin_id = $1 AND package_path = $2
	`, string(id), packagePath).Scan(&storedDigest); err != nil {
		return fmt.Errorf("plugins: installed package digest is unavailable: %w", err)
	}
	digest, err := packageFullDigest(packagePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(digest, storedDigest) {
		return fmt.Errorf("%w: installed package content changed after installation", ErrInvalidPackage)
	}
	return nil
}

func (m *Manager) runnerFor(manifest Manifest) process.Runner {
	if manifest.Backend.Mode == BackendExec {
		if m.runner != nil {
			return m.runner
		}
		if m.execRunner == nil {
			m.execRunner = process.NewExecRunner()
		}
		return m.execRunner
	}
	return m.runner
}

func startupTimeout(backendTimeout time.Duration) time.Duration {
	timeout := backendTimeout + 5*time.Second
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	return timeout
}

func waitForHealth(ctx context.Context, client *protocol.Client, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := client.Health(probeCtx, path)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (m *Manager) startMonitor(ctx context.Context, runtime *runtimeBackend) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
				_, err := runtime.client.Health(probeCtx, runtime.installed.Manifest.Backend.HealthPath)
				probeCancel()
				if err != nil {
					runtime.setHealth(Health{Status: "unhealthy", Message: "health probe failed", CheckedAt: m.now()})
					_, _ = m.db.Pool.Exec(ctx, `
						UPDATE plugins SET status = 'unhealthy', updated_at = clock_timestamp()
						WHERE id = $1 AND status = 'enabled'
					`, string(runtime.installed.ID))
					continue
				}
				runtime.setHealth(Health{Status: "healthy", CheckedAt: m.now()})
				_, _ = m.db.Pool.Exec(ctx, `
					UPDATE plugins SET status = 'enabled', updated_at = clock_timestamp()
					WHERE id = $1 AND status = 'unhealthy'
				`, string(runtime.installed.ID))
			}
		}
	}()
}

// Close stops every runtime owned by this manager. Backend shutdown calls it
// before process exit so plugin children/containers do not outlive the core.
// It never signals processes that are not in the manager's runtime map.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.RLock()
	ids := make([]ID, 0, len(m.runtime))
	for id := range m.runtime {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	var firstErr error
	for _, id := range ids {
		if err := m.stopRuntime(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Manager) stopRuntime(ctx context.Context, id ID) error {
	m.mu.Lock()
	runtime := m.runtime[id]
	delete(m.runtime, id)
	m.mu.Unlock()
	if runtime == nil {
		return nil
	}
	if runtime.cancel != nil {
		runtime.cancel()
	}
	for _, consumer := range runtime.consumers {
		consumer.Stop()
	}
	runner := m.runnerFor(runtime.installed.Manifest)
	if runner != nil && runtime.handle != nil {
		if err := runner.Stop(ctx, runtime.handle); err != nil {
			m.logger.Warn("plugin stop failed", "plugin", id, "error", err)
		}
	}
	return nil
}

func (m *Manager) probeHealth(ctx context.Context, id ID) (Health, bool) {
	runtime := m.runtimeFor(id)
	if runtime == nil {
		return Health{Status: "unhealthy", Message: "backend is not running", CheckedAt: m.now()}, false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := runtime.client.Health(probeCtx, runtime.installed.Manifest.Backend.HealthPath); err != nil {
		health := Health{Status: "unhealthy", Message: "backend health probe failed", CheckedAt: m.now()}
		runtime.setHealth(health)
		return health, false
	}
	health := Health{Status: "healthy", CheckedAt: m.now()}
	runtime.setHealth(health)
	return health, true
}

func (m *Manager) runtimeToken(ctx context.Context, id ID) (string, error) {
	token, err := protocol.NewServiceToken()
	if err != nil {
		return "", err
	}
	if err := m.putEncryptedSetting(ctx, id, settingServiceToken, token); err != nil {
		return "", err
	}
	if err := m.putEncryptedSetting(ctx, id, settingTokenHash, base64Encode(protocol.HashServiceToken(token))); err != nil {
		return "", err
	}
	return token, nil
}

func base64Encode(raw []byte) string {
	// The http-v1 envelope uses standard base64 (not URL-safe), matching the
	// documented contract and all separately built plugins.
	return base64.StdEncoding.EncodeToString(raw)
}

func (m *Manager) setEnabled(ctx context.Context, actor authorization.Principal, id ID, enabled bool) error {
	tx, err := m.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	action := auditActionPluginDisable
	status := StatusDisabled
	if enabled {
		action = auditActionPluginEnable
		status = StatusEnabled
	}
	if _, err := tx.Exec(ctx, `
		UPDATE plugins
		SET status = $2,
		    enabled_at = CASE WHEN $3 THEN clock_timestamp() ELSE enabled_at END,
		    disabled_at = CASE WHEN $3 THEN NULL ELSE clock_timestamp() END,
		    updated_at = clock_timestamp()
		WHERE id = $1
	`, string(id), string(status), enabled); err != nil {
		return fmt.Errorf("plugins: update status: %w", err)
	}
	if err := writeAudit(ctx, tx, actor, action, string(id), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Manager) setStatusFailed(ctx context.Context, id ID, cause error) {
	message := "plugin operation failed"
	if cause != nil {
		message = truncate(cause.Error(), 2000)
	}
	_, _ = m.db.Pool.Exec(ctx, `
		UPDATE plugins SET status = 'failed', updated_at = clock_timestamp() WHERE id = $1
	`, string(id))
	m.mu.RLock()
	runtime := m.runtime[id]
	m.mu.RUnlock()
	if runtime != nil {
		runtime.setHealth(Health{Status: "unhealthy", Message: message, CheckedAt: m.now()})
	}
}

func (m *Manager) setStatus(ctx context.Context, id ID, status Status) {
	_, _ = m.db.Pool.Exec(ctx, `
		UPDATE plugins SET status = $2, updated_at = clock_timestamp() WHERE id = $1
	`, string(id), string(status))
}

func truncate(value string, size int) string {
	if len(value) <= size {
		return value
	}
	return value[:size]
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func withinRoot(root, candidate string) bool {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	resolvedCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func compareVersions(left, right string) int {
	a, aErr := parseSemVersion(left)
	b, bErr := parseSemVersion(right)
	if aErr != nil || bErr != nil {
		// Both values passed manifest validation before this point; keep a
		// deterministic fallback rather than treating malformed input as equal.
		return strings.Compare(left, right)
	}
	return compareSemVersion(a, b)
}

func manifestPermissionIDs(manifest Manifest) []authorization.Permission {
	permissions := make([]authorization.Permission, 0, len(manifest.Permissions))
	for _, declaration := range manifest.Permissions {
		permissions = append(permissions, declaration.ID)
	}
	return permissions
}
