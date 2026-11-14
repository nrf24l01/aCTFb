package plugins

import "errors"

var (
	// ErrNotFound is returned when a plugin id is unknown.
	ErrNotFound = errors.New("plugins: plugin not found")
	// ErrAlreadyInstalled is returned when a different package/version for an
	// existing id is installed without the explicit upgrade operation.
	ErrAlreadyInstalled = errors.New("plugins: plugin already installed")
	// ErrInvalidPackage indicates an unsafe or malformed server-side package.
	ErrInvalidPackage = errors.New("plugins: invalid package")
	// ErrManifestInvalid indicates a manifest that failed strict validation.
	ErrManifestInvalid = errors.New("plugins: manifest invalid")
	// ErrIncompatible indicates an unsupported apiVersion.
	ErrIncompatible = errors.New("plugins: incompatible plugin api version")
	// ErrPermissionDenied indicates a missing core plugins.manage or plugin
	// permission grant.
	ErrPermissionDenied = errors.New("plugins: permission denied")
	// ErrExecDisabled indicates an exec package was supplied while the operator
	// has not explicitly enabled ACTFB_PLUGIN_ALLOW_EXEC.
	ErrExecDisabled = errors.New("plugins: exec backend is disabled by operator policy; use a pinned OCI image")
	// ErrBackendUnavailable is the controlled 503 condition for a disabled,
	// stopped, crashed or unhealthy plugin backend.
	ErrBackendUnavailable = errors.New("plugins: plugin backend unavailable")
	// ErrRouteNotFound indicates the request is not a declared plugin route.
	ErrRouteNotFound = errors.New("plugins: route not declared by plugin")
	// ErrRequestRejected indicates an unsafe proxy request (traversal, size,
	// method, header or content type).
	ErrRequestRejected = errors.New("plugins: request rejected")
	// ErrDelegationInvalid covers missing, malformed, expired or replayed tokens.
	ErrDelegationInvalid = errors.New("plugins: delegation invalid")
	// ErrCheckerNotFound indicates no enabled plugin declares the checker key
	// or extension key.
	ErrCheckerNotFound = errors.New("plugins: checker provider not found")
	// ErrCheckerUnavailable indicates a disabled/crashed/unreachable checker
	// plugin or a missing approved permission grant. It always fails closed.
	ErrCheckerUnavailable = errors.New("plugins: checker provider unavailable")
	// ErrCheckerInvalid indicates a malformed response, unsupported verdict,
	// timeout or an out-of-bounds score/body from the checker plugin.
	ErrCheckerInvalid = errors.New("plugins: checker response rejected")
	// ErrValidation is the legacy alias for a rejected install request.
	ErrValidation = errors.New("plugins: validation failed")
	// ErrDependencyMissing indicates a required plugin is not installed or not
	// enabled at the moment it is needed.
	ErrDependencyMissing = errors.New("plugins: required dependency is missing or disabled")
	// ErrDependencyIncompatible indicates a dependency version does not satisfy
	// the declared semantic-version range, or an upgrade would break a
	// dependent's range.
	ErrDependencyIncompatible = errors.New("plugins: dependency version incompatible")
	// ErrDependencyCycle indicates a dependency cycle across installed plugins.
	ErrDependencyCycle = errors.New("plugins: dependency cycle")
	// ErrDependencyInUse indicates a disable/remove operation would break an
	// installed or enabled dependent.
	ErrDependencyInUse = errors.New("plugins: plugin has dependents")
	// ErrTrustedJSDisabled indicates trusted-js package acceptance is off by
	// operator policy.
	ErrTrustedJSDisabled = errors.New("plugins: trusted-js frontend is disabled by operator policy")
	// ErrTrustedJSNotApproved indicates the operator did not explicitly approve
	// the trusted-js risk for this installation.
	ErrTrustedJSNotApproved = errors.New("plugins: trusted-js frontend was not explicitly approved")
	// ErrIdentityConflict indicates a legacy plugin's schema/role/volume would
	// collide with another reserved identity. Operator repair is required; core
	// never silently renames or drops private plugin data.
	ErrIdentityConflict = errors.New("plugins: private plugin identity conflict")
)
