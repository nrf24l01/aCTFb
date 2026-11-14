package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func seedPluginRow(t *testing.T, env *testEnv, manifest Manifest, status Status, trustedJSApproved bool) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Pool.Exec(context.Background(), `
		INSERT INTO plugins
			(id, name, version, api_version, status, manifest, manifest_digest, package_path,
			 installed_by, trusted_js_approved)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, string(manifest.ID), manifest.Name, manifest.Version, manifest.APIVersion, string(status),
		raw, digest, filepath.Join(env.root, string(manifest.ID), manifest.Version), env.admin, trustedJSApproved); err != nil {
		t.Fatalf("seed plugin %s: %v", manifest.ID, err)
	}
}

func dependencyManifest(id ID, version string, dependencies ...Dependency) Manifest {
	return Manifest{
		ID: id, Name: string(id), Version: version, APIVersion: PluginAPIVersion,
		Backend:      Backend{Mode: BackendExec, Entrypoint: "bin/plugin", Protocol: HTTPProtocolV1, TimeoutSeconds: 5},
		Dependencies: dependencies,
	}
}

func TestPluginDependencyResolutionRealPostgres(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	base := dependencyManifest("example.base", "1.5.0")
	seedPluginRow(t, env, base, StatusEnabled, false)

	// Missing and disabled dependencies fail closed on enable.
	missing := dependencyManifest("example.missing", "1.0.0", Dependency{ID: "example.never", Version: "^1.0.0"})
	seedPluginRow(t, env, missing, StatusInstalled, false)
	if err := env.manager.ensureEnableDependencies(ctx, *mustLoad(t, env, missing.ID)); !errors.Is(err, ErrDependencyMissing) {
		t.Fatalf("missing dependency enable = %v, want ErrDependencyMissing", err)
	}

	disabled := dependencyManifest("example.disabled", "1.0.0", Dependency{ID: "example.off", Version: "^1.0.0"})
	seedPluginRow(t, env, disabled, StatusInstalled, false)
	seedPluginRow(t, env, dependencyManifest("example.off", "1.0.0"), StatusInstalled, false)
	if err := env.manager.ensureEnableDependencies(ctx, *mustLoad(t, env, disabled.ID)); !errors.Is(err, ErrDependencyMissing) {
		t.Fatalf("disabled dependency enable = %v, want ErrDependencyMissing", err)
	}

	// A satisfied closure enables.
	satisfied := dependencyManifest("example.satisfied", "1.0.0", Dependency{ID: "example.base", Version: "^1.2.0"})
	seedPluginRow(t, env, satisfied, StatusInstalled, false)
	if err := env.manager.ensureEnableDependencies(ctx, *mustLoad(t, env, satisfied.ID)); err != nil {
		t.Fatalf("satisfied dependency enable: %v", err)
	}

	// Incompatible semver is rejected, including a range that only matches a
	// different major.
	incompatible := dependencyManifest("example.incompatible", "1.0.0", Dependency{ID: "example.base", Version: "^2.0.0"})
	seedPluginRow(t, env, incompatible, StatusInstalled, false)
	if err := env.manager.ensureEnableDependencies(ctx, *mustLoad(t, env, incompatible.ID)); !errors.Is(err, ErrDependencyIncompatible) {
		t.Fatalf("incompatible dependency enable = %v, want ErrDependencyIncompatible", err)
	}
	if err := env.manager.ensureInstallDependencies(ctx, incompatible); !errors.Is(err, ErrDependencyIncompatible) {
		t.Fatalf("incompatible dependency install = %v, want ErrDependencyIncompatible", err)
	}

	// Missing is allowed at install because install never enables.
	if err := env.manager.ensureInstallDependencies(ctx, missing); err != nil {
		t.Fatalf("install may leave dependencies unsatisfied: %v", err)
	}

	// A cycle is rejected through either node.
	left := dependencyManifest("example.left", "1.0.0", Dependency{ID: "example.right", Version: "^1.0.0"})
	right := dependencyManifest("example.right", "1.0.0", Dependency{ID: "example.left", Version: "^1.0.0"})
	seedPluginRow(t, env, left, StatusInstalled, false)
	seedPluginRow(t, env, right, StatusInstalled, false)
	if err := env.manager.ensureEnableDependencies(ctx, *mustLoad(t, env, left.ID)); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("cycle enable = %v, want ErrDependencyCycle", err)
	}
	if err := env.manager.ensureInstallDependencies(ctx, dependencyManifest("example.cycle", "1.0.0", Dependency{ID: "example.left", Version: "^1.0.0"})); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("cycle install = %v, want ErrDependencyCycle", err)
	}
	// Remove the intentionally cyclic rows before testing independent upgrade
	// and disable/remove edges.
	if _, err := env.db.Pool.Exec(ctx, `DELETE FROM plugins WHERE id IN ('example.left','example.right')`); err != nil {
		t.Fatal(err)
	}

	// Disable/remove refuse while an enabled dependent exists.
	dependent := dependencyManifest("example.dependent", "1.0.0", Dependency{ID: "example.base", Version: "^1.0.0"})
	seedPluginRow(t, env, dependent, StatusEnabled, false)
	if err := env.manager.ensureDisableAllowed(ctx, base.ID); !errors.Is(err, ErrDependencyInUse) {
		t.Fatalf("disable with enabled dependent = %v, want ErrDependencyInUse", err)
	}
	if err := env.manager.ensureRemoveAllowed(ctx, base.ID); !errors.Is(err, ErrDependencyInUse) {
		t.Fatalf("remove with dependent = %v, want ErrDependencyInUse", err)
	}

	// An upgrade that breaks a dependent's range is rejected before the row is
	// changed.
	upgrade := dependencyManifest("example.base", "2.0.0")
	if err := env.manager.ensureUpgradeDependencies(ctx, *mustLoad(t, env, base.ID), upgrade); !errors.Is(err, ErrDependencyIncompatible) {
		t.Fatalf("upgrade breaking dependent = %v, want ErrDependencyIncompatible", err)
	}
	if _, err := env.db.Pool.Exec(ctx, `DELETE FROM plugins WHERE id = 'example.incompatible'`); err != nil {
		t.Fatal(err)
	}
	if err := env.manager.ensureUpgradeDependencies(ctx, *mustLoad(t, env, base.ID), dependencyManifest("example.base", "1.9.0")); err != nil {
		t.Fatalf("compatible upgrade: %v", err)
	}
}

func mustLoad(t *testing.T, env *testEnv, id ID) *Installed {
	t.Helper()
	installed, err := env.manager.loadInstalled(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return installed
}

func TestPluginTrustedJSRequiresOperatorAndPerInstallApprovalPG(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	principal := authorizationPrincipal(env.admin)

	dir := filepath.Join(env.root, "incoming", "trusted")
	writeTestFile(t, filepath.Join(dir, "manifest.yaml"), []byte(`id: example.trusted
name: Trusted
version: 1.0.0
apiVersion: "1"
backend:
  mode: exec
  entrypoint: bin/plugin
  protocol: http-v1
  healthPath: /v1/health
  timeoutSeconds: 5
frontend:
  mode: trusted-js
  entry: app.js
assets: assets
permissions:
  - id: plugin.example.trusted.view
    scope: global
`), 0o640)
	writeTestFile(t, filepath.Join(dir, "bin", "plugin"), []byte("#!/bin/sh\nexit 0\n"), 0o750)
	writeTestFile(t, filepath.Join(dir, "assets", "app.js"), []byte("export default {}\n"), 0o640)

	// Operator gate off: validation reports the policy error and install fails
	// even if the caller claims to approve the risk.
	validation, err := env.manager.Validate(ctx, principal, "trusted")
	if err != nil {
		t.Fatal(err)
	}
	foundDisabled := false
	for _, finding := range validation.Findings {
		if finding.Code == "trusted_js_disabled" && finding.Severity == "error" {
			foundDisabled = true
		}
	}
	if !foundDisabled {
		t.Fatalf("trusted-js validation must fail closed while operator policy is off: %#v", validation.Findings)
	}
	if _, err := env.manager.Install(ctx, principal, InstallRequest{
		PackagePath: "trusted", ApproveTrustedJS: true,
		ApprovePermissions: []authorization.Permission{"plugin.example.trusted.view"},
	}); !errors.Is(err, ErrTrustedJSDisabled) {
		t.Fatalf("install with trusted-js policy off = %v, want ErrTrustedJSDisabled", err)
	}

	// Operator gate on, but no per-install acknowledgement: rejected.
	env.manager.allowTrustedJS = true
	if _, err := env.manager.Install(ctx, principal, InstallRequest{
		PackagePath: "trusted", ApproveTrustedJS: false,
		ApprovePermissions: []authorization.Permission{"plugin.example.trusted.view"},
	}); !errors.Is(err, ErrTrustedJSNotApproved) {
		t.Fatalf("install without explicit approval = %v, want ErrTrustedJSNotApproved", err)
	}

	installed, err := env.manager.Install(ctx, principal, InstallRequest{
		PackagePath: "trusted", ApproveTrustedJS: true,
		ApprovePermissions: []authorization.Permission{"plugin.example.trusted.view"},
	})
	if err != nil {
		t.Fatalf("explicit trusted-js install: %v", err)
	}
	if !installed.TrustedJSApproved {
		t.Fatal("explicit trusted-js approval was not persisted")
	}
	if _, err := env.db.Pool.Exec(ctx, `UPDATE plugins SET status='enabled' WHERE id='example.trusted'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.manager.TrustedBundle(ctx, principal, "example.trusted"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("trusted bundle without grant = %v, want ErrPermissionDenied", err)
	}
	if _, err := env.manager.CreatePermissionGrant(ctx, principal, "example.trusted", GrantInput{
		UserID: env.admin, Permission: "plugin.example.trusted.view",
	}); err != nil {
		t.Fatalf("grant trusted permission: %v", err)
	}
	raw, integrity, err := env.manager.TrustedBundle(ctx, principal, "example.trusted")
	if err != nil {
		t.Fatalf("trusted bundle after grant: %v", err)
	}
	if len(raw) == 0 || !strings.HasPrefix(integrity, "sha256:") {
		t.Fatalf("trusted bundle bytes/integrity = %d/%q", len(raw), integrity)
	}
	visible, err := env.manager.Visible(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	foundTrusted := false
	for _, item := range visible {
		if item.ID == "example.trusted" && item.TrustedJS != nil && item.TrustedJS.Integrity == integrity {
			foundTrusted = true
		}
	}
	if !foundTrusted {
		t.Fatalf("trusted plugin metadata missing from visible list: %#v", visible)
	}

	// Removing persisted approval or switching operator policy off must block
	// enable, not silently downgrade the sandbox.
	if _, err := env.db.Pool.Exec(ctx, `UPDATE plugins SET trusted_js_approved = FALSE WHERE id = 'example.trusted'`); err != nil {
		t.Fatal(err)
	}
	if err := env.manager.ensureEnableDependencies(ctx, *mustLoad(t, env, "example.trusted")); !errors.Is(err, ErrTrustedJSNotApproved) {
		t.Fatalf("enable without persisted approval = %v, want ErrTrustedJSNotApproved", err)
	}
	env.manager.allowTrustedJS = false
	if err := env.manager.ensureEnableDependencies(ctx, *mustLoad(t, env, "example.trusted")); !errors.Is(err, ErrTrustedJSDisabled) {
		t.Fatalf("enable with policy off = %v, want ErrTrustedJSDisabled", err)
	}
}

func TestPluginTokenRotationCloseAndRecoveryPG(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	principal := authorizationPrincipal(env.admin)
	installHello(t, env)
	capabilities, permissions := helloApprovals()
	if _, err := env.manager.Install(ctx, principal, InstallRequest{
		PackagePath: "hello", ApproveCapabilities: capabilities, ApprovePermissions: permissions,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Enable(ctx, principal, "example.hello"); err != nil {
		t.Fatalf("first enable: %v", err)
	}
	firstToken, err := env.manager.getEncryptedSetting(ctx, "example.hello", settingServiceToken)
	if err != nil || firstToken == "" {
		t.Fatalf("read first token: %q %v", firstToken, err)
	}
	if err := env.manager.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if env.manager.runtimeFor("example.hello") != nil {
		t.Fatal("Close left an owned runtime running")
	}
	if _, err := env.manager.VerifyServiceToken(ctx, firstToken); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("closed runtime token accepted: %v", err)
	}
	if _, err := env.manager.Enable(ctx, principal, "example.hello"); err != nil {
		t.Fatalf("second enable: %v", err)
	}
	secondToken, err := env.manager.getEncryptedSetting(ctx, "example.hello", settingServiceToken)
	if err != nil {
		t.Fatal(err)
	}
	if secondToken == firstToken {
		t.Fatal("service token was reused across process starts")
	}
	if _, err := env.manager.VerifyServiceToken(ctx, firstToken); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("old token accepted after restart: %v", err)
	}
	identity, err := env.manager.VerifyServiceToken(ctx, secondToken)
	if err != nil || identity.PluginID != "example.hello" {
		t.Fatalf("new token identity = %+v %v", identity, err)
	}
	if err := env.manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	failures := env.manager.RecoverEnabled(ctx)
	if len(failures) != 0 {
		t.Fatalf("recovery failures: %v", failures)
	}
	if env.manager.runtimeFor("example.hello") == nil {
		t.Fatal("RecoverEnabled did not restart an enabled plugin")
	}
	recoveredToken, err := env.manager.getEncryptedSetting(ctx, "example.hello", settingServiceToken)
	if err != nil || recoveredToken == secondToken {
		t.Fatalf("recovery did not rotate token: %q %v", recoveredToken, err)
	}
	if err := env.manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPluginRecoveryTrustedJSGatePG(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	manifest := dependencyManifest("example.trustedrec", "1.0.0")
	manifest.Frontend = Frontend{Mode: FrontendTrustedJS, Entry: "app.js"}
	manifest.Assets = "assets"
	seedPluginRow(t, env, manifest, StatusEnabled, false)
	env.manager.allowTrustedJS = true
	failures := env.manager.RecoverEnabled(ctx)
	if len(failures) == 0 {
		t.Fatal("recovery must fail closed when trusted-js install approval is absent")
	}
	installed, err := env.manager.loadInstalled(ctx, manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Status != StatusFailed {
		t.Fatalf("unapproved trusted-js recovery status = %s, want failed", installed.Status)
	}
}

func TestPluginLifecycleDependencyGuardsPG(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	principal := authorizationPrincipal(env.admin)

	base := dependencyManifest("example.base", "1.0.0")
	seedPluginRow(t, env, base, StatusEnabled, false)
	dependent := dependencyManifest("example.dependent", "1.0.0", Dependency{ID: "example.base", Version: "^1.0.0"})
	seedPluginRow(t, env, dependent, StatusEnabled, false)

	if _, err := env.manager.Disable(ctx, principal, base.ID); !errors.Is(err, ErrDependencyInUse) {
		t.Fatalf("disable with enabled dependent = %v, want ErrDependencyInUse", err)
	}
	if err := env.manager.Remove(ctx, principal, base.ID); !errors.Is(err, ErrDependencyInUse) {
		t.Fatalf("remove with enabled dependent = %v, want ErrDependencyInUse", err)
	}

	writeDependencyPackage(t, env, "base-upgrade", `id: example.base
name: Base
version: 2.0.0
apiVersion: "1"
backend:
  mode: exec
  entrypoint: bin/plugin
  protocol: http-v1
  healthPath: /v1/health
  timeoutSeconds: 5
`)
	if _, err := env.manager.Upgrade(ctx, principal, base.ID, "base-upgrade"); !errors.Is(err, ErrDependencyIncompatible) {
		t.Fatalf("upgrade breaking enabled dependent = %v, want ErrDependencyIncompatible", err)
	}

	missing := dependencyManifest("example.need", "1.0.0", Dependency{ID: "example.absent", Version: "^1.0.0"})
	seedPluginRow(t, env, missing, StatusInstalled, false)
	if _, err := env.manager.Enable(ctx, principal, missing.ID); !errors.Is(err, ErrDependencyMissing) {
		t.Fatalf("enable with missing dependency = %v, want ErrDependencyMissing", err)
	}
	incompatible := dependencyManifest("example.badrange", "1.0.0", Dependency{ID: "example.base", Version: "^9.0.0"})
	seedPluginRow(t, env, incompatible, StatusInstalled, false)
	if _, err := env.manager.Enable(ctx, principal, incompatible.ID); !errors.Is(err, ErrDependencyIncompatible) {
		t.Fatalf("enable with incompatible dependency = %v, want ErrDependencyIncompatible", err)
	}
}

func writeDependencyPackage(t *testing.T, env *testEnv, name, manifest string) {
	t.Helper()
	dir := filepath.Join(env.root, "incoming", name)
	writeTestFile(t, filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o640)
	writeTestFile(t, filepath.Join(dir, "bin", "plugin"), []byte("#!/bin/sh\nexit 0\n"), 0o750)
}

func authorizationPrincipal(id uuid.UUID) authorization.Principal {
	return authorization.Principal{UserID: id}
}
