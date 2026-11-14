package authorization

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// PermissionScope is the resource scope in which a registered permission may
// be granted or checked.
type PermissionScope string

const (
	PermissionScopeGlobal PermissionScope = "global"
	PermissionScopeBoard  PermissionScope = "board"
	PermissionScopeModule PermissionScope = "module"
)

// PermissionDefinition is the trusted-central catalog entry for one permission.
// Source is "core" or a plugin id such as "vendor.name". Plugin permissions are
// always named plugin.<id>.<name>.
type PermissionDefinition struct {
	Permission  Permission
	Source      string
	Scopes      []PermissionScope
	Description string
}

// PermissionRegistry is the single permission catalog used by Authorize and
// explicit permission-grant validation.
type PermissionRegistry interface {
	Register(def PermissionDefinition) error
	Lookup(permission Permission) (PermissionDefinition, bool)
	List() []PermissionDefinition
}

// Registry is the thread-safe implementation of PermissionRegistry. NewRegistry
// bootstraps all core permissions; plugin managers may register namespaced
// permissions later without granting them to anyone.
type Registry struct {
	mu   sync.RWMutex
	defs map[Permission]PermissionDefinition
}

var (
	_ PermissionRegistry = (*Registry)(nil)

	pluginSourcePattern     = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)+$`)
	pluginPermissionPattern = regexp.MustCompile(`^plugin\.[a-z0-9_.-]+$`)
)

// NewRegistry returns a registry pre-populated with the core catalog.
func NewRegistry() *Registry {
	registry := &Registry{defs: make(map[Permission]PermissionDefinition, len(AllPermissions))}
	for _, definition := range CorePermissionDefinitions() {
		if err := registry.Register(definition); err != nil {
			panic(fmt.Sprintf("authorization: invalid core permission definition %q: %v", definition.Permission, err))
		}
	}
	return registry
}

// CorePermissionDefinitions returns the exact core catalog and supported scopes.
func CorePermissionDefinitions() []PermissionDefinition {
	definitions := make([]PermissionDefinition, 0, len(AllPermissions))
	for _, permission := range AllPermissions {
		definitions = append(definitions, PermissionDefinition{
			Permission:  permission,
			Source:      "core",
			Scopes:      corePermissionScopes(permission),
			Description: string(permission),
		})
	}
	return definitions
}

func corePermissionScopes(permission Permission) []PermissionScope {
	switch permission {
	case PermissionBoardCreate,
		PermissionPlatformUsersManage,
		PermissionPlatformAccessGroupsManage,
		PermissionPlatformIntegrationsManage,
		PermissionPlatformAuditRead,
		PermissionPlatformDeploymentTemplatesPublish,
		PermissionPluginsManage:
		return []PermissionScope{PermissionScopeGlobal}
	case PermissionBoardRead,
		PermissionBoardUpdate,
		PermissionBoardPublish,
		PermissionBoardDelete,
		PermissionBoardStaffManage,
		PermissionBoardParticipantsManage,
		PermissionBoardRatingManage,
		PermissionBoardAuditRead,
		PermissionBoardAudiencesManage,
		PermissionModuleCreate,
		PermissionTeamManageOwn:
		return []PermissionScope{PermissionScopeBoard}
	case PermissionModuleUpdate,
		PermissionModuleDelete,
		PermissionModuleTasksManage,
		PermissionModuleSubmissionsRead,
		PermissionModuleSubmissionsReview,
		PermissionModuleInstancesAdmin,
		PermissionSubmissionCreateOwn,
		PermissionSubmissionReadOwn,
		PermissionDeploymentTemplatesUse,
		PermissionInstanceStartOwn,
		PermissionInstanceManageOwn:
		return []PermissionScope{PermissionScopeBoard, PermissionScopeModule}
	default:
		return []PermissionScope{PermissionScopeGlobal}
	}
}

// Register adds a trusted definition. Duplicate cross-source definitions and
// non-identical reloads are rejected; an identical reload is idempotent.
func (r *Registry) Register(def PermissionDefinition) error {
	if r == nil {
		return fmt.Errorf("authorization: nil permission registry")
	}
	def.Permission = Permission(strings.TrimSpace(string(def.Permission)))
	def.Source = strings.TrimSpace(def.Source)
	if def.Permission == "" {
		return fmt.Errorf("authorization: permission is required")
	}
	if def.Source == "" {
		return fmt.Errorf("authorization: permission source is required")
	}
	scopes, err := normalizeScopes(def.Scopes)
	if err != nil {
		return err
	}
	def.Scopes = scopes

	if def.Source == "core" {
		expected, ok := corePermissionDefinition(def.Permission)
		if !ok {
			return fmt.Errorf("authorization: %q is not a core permission", def.Permission)
		}
		def.Scopes = expected.Scopes
	} else {
		if !pluginSourcePattern.MatchString(def.Source) {
			return fmt.Errorf("authorization: invalid plugin source %q", def.Source)
		}
		if !pluginPermissionPattern.MatchString(string(def.Permission)) ||
			!strings.HasPrefix(string(def.Permission), "plugin."+def.Source+".") {
			return fmt.Errorf("authorization: plugin permission %q must be namespaced under plugin.%s.", def.Permission, def.Source)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.defs == nil {
		r.defs = make(map[Permission]PermissionDefinition)
	}
	if existing, ok := r.defs[def.Permission]; ok {
		if existing.Source != def.Source {
			return fmt.Errorf("authorization: permission %q already registered by source %q", def.Permission, existing.Source)
		}
		if equalDefinition(existing, def) {
			return nil
		}
		return fmt.Errorf("authorization: permission %q already registered with a different definition", def.Permission)
	}
	r.defs[def.Permission] = cloneDefinition(def)
	return nil
}

func (r *Registry) Lookup(permission Permission) (PermissionDefinition, bool) {
	if r == nil {
		return PermissionDefinition{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[permission]
	if !ok {
		return PermissionDefinition{}, false
	}
	return cloneDefinition(def), true
}

func (r *Registry) List() []PermissionDefinition {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	definitions := make([]PermissionDefinition, 0, len(r.defs))
	for _, def := range r.defs {
		definitions = append(definitions, cloneDefinition(def))
	}
	r.mu.RUnlock()
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].Source != definitions[j].Source {
			return definitions[i].Source < definitions[j].Source
		}
		return definitions[i].Permission < definitions[j].Permission
	})
	return definitions
}

// RegisterPluginPermissions is a convenience for the plugin manager. It is
// still trusted-central: the caller must have validated the manifest.
func RegisterPluginPermissions(registry PermissionRegistry, pluginID string, definitions []PermissionDefinition) error {
	if registry == nil {
		return fmt.Errorf("authorization: nil permission registry")
	}
	for _, definition := range definitions {
		if definition.Source == "" {
			definition.Source = pluginID
		}
		if definition.Source != pluginID {
			return fmt.Errorf("authorization: plugin permission %q source %q does not match plugin %q", definition.Permission, definition.Source, pluginID)
		}
		if err := registry.Register(definition); err != nil {
			return err
		}
	}
	return nil
}

func corePermissionDefinition(permission Permission) (PermissionDefinition, bool) {
	if !ValidPermission(permission) {
		return PermissionDefinition{}, false
	}
	return PermissionDefinition{
		Permission:  permission,
		Source:      "core",
		Scopes:      corePermissionScopes(permission),
		Description: string(permission),
	}, true
}

func normalizeScopes(scopes []PermissionScope) ([]PermissionScope, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("authorization: permission scopes are required")
	}
	seen := make(map[PermissionScope]struct{}, len(scopes))
	result := make([]PermissionScope, 0, len(scopes))
	for _, scope := range scopes {
		switch scope {
		case PermissionScopeGlobal, PermissionScopeBoard, PermissionScopeModule:
		default:
			return nil, fmt.Errorf("authorization: invalid permission scope %q", scope)
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func cloneDefinition(def PermissionDefinition) PermissionDefinition {
	def.Scopes = append([]PermissionScope(nil), def.Scopes...)
	return def
}

func equalDefinition(left, right PermissionDefinition) bool {
	if left.Permission != right.Permission || left.Source != right.Source || left.Description != right.Description {
		return false
	}
	if len(left.Scopes) != len(right.Scopes) {
		return false
	}
	for index := range left.Scopes {
		if left.Scopes[index] != right.Scopes[index] {
			return false
		}
	}
	return true
}

// DefinitionAllowsScope reports whether a definition explicitly supports the
// requested scope. Global grants never satisfy board/module scopes and vice
// versa.
func DefinitionAllowsScope(def PermissionDefinition, scope PermissionScope) bool {
	for _, candidate := range def.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}
