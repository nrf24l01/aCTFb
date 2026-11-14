package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// dependencyNode is one persisted plugin in the resolution graph.
type dependencyNode struct {
	id       ID
	version  string
	status   Status
	manifest Manifest
}

func (n dependencyNode) enabled() bool { return n.status == StatusEnabled }

// loadDependencyNodes reads every installed plugin. override replaces/adds one
// candidate manifest so install/enable/upgrade checks reason about the exact
// post-operation graph rather than only the current rows.
func (m *Manager) loadDependencyNodes(ctx context.Context, override *dependencyNode) (map[ID]dependencyNode, error) {
	rows, err := m.db.Pool.Query(ctx, `
		SELECT id, version, status, manifest FROM plugins ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("plugins: load dependency graph: %w", err)
	}
	defer rows.Close()
	nodes := make(map[ID]dependencyNode)
	for rows.Next() {
		var node dependencyNode
		var status string
		var raw []byte
		if err := rows.Scan(&node.id, &node.version, &status, &raw); err != nil {
			return nil, err
		}
		node.status = Status(status)
		if err := json.Unmarshal(raw, &node.manifest); err != nil {
			return nil, fmt.Errorf("plugins: decode dependency manifest for %s: %w", node.id, err)
		}
		nodes[node.id] = node
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if override != nil {
		nodes[override.id] = *override
	}
	return nodes, nil
}

// checkDependencyClosure walks required edges from root. When requireEnabled is
// true every required dependency must be present and enabled; when false,
// missing dependencies are allowed (install leaves a package disabled), but an
// installed dependency with an incompatible version is still rejected.
func checkDependencyClosure(nodes map[ID]dependencyNode, root ID, requireEnabled bool) error {
	state := make(map[ID]uint8, len(nodes))
	path := make([]ID, 0, len(nodes))
	var visit func(ID) error
	visit = func(id ID) error {
		switch state[id] {
		case 1:
			return dependencyError(ErrDependencyCycle, "cycle detected: %s", formatCycle(path, id))
		case 2:
			return nil
		}
		node, exists := nodes[id]
		if !exists {
			if requireEnabled {
				return dependencyError(ErrDependencyMissing, "%s is not installed", id)
			}
			return nil
		}
		state[id] = 1
		path = append(path, id)
		for _, dependency := range node.manifest.Dependencies {
			target, present := nodes[dependency.ID]
			if !present {
				if requireEnabled {
					return dependencyError(ErrDependencyMissing, "%s requires %s, which is not installed", id, dependency.ID)
				}
				continue
			}
			ok, err := versionSatisfies(target.version, dependency.Version)
			if err != nil {
				return dependencyError(ErrDependencyIncompatible, "%s declares an invalid range for %s: %v", id, dependency.ID, err)
			}
			if !ok {
				return dependencyError(ErrDependencyIncompatible,
					"%s %s does not satisfy %s for %s", dependency.ID, target.version, dependency.Version, id)
			}
			if requireEnabled && !target.enabled() {
				return dependencyError(ErrDependencyMissing, "%s requires %s, which is not enabled", id, dependency.ID)
			}
			if err := visit(dependency.ID); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[id] = 2
		return nil
	}
	return visit(root)
}

func formatCycle(path []ID, repeated ID) string {
	start := 0
	for index, id := range path {
		if id == repeated {
			start = index
			break
		}
	}
	parts := make([]string, 0, len(path)-start+1)
	for _, id := range path[start:] {
		parts = append(parts, string(id))
	}
	parts = append(parts, string(repeated))
	return strings.Join(parts, " -> ")
}

// detectDependencyCycle validates the whole installed graph. It is used on
// install/upgrade because a new manifest can close a cycle through existing
// nodes even when the new node itself is not part of a required closure yet.
func detectDependencyCycle(nodes map[ID]dependencyNode) error {
	state := make(map[ID]uint8, len(nodes))
	path := make([]ID, 0, len(nodes))
	var visit func(ID) error
	visit = func(id ID) error {
		switch state[id] {
		case 1:
			return dependencyError(ErrDependencyCycle, "cycle detected: %s", formatCycle(path, id))
		case 2:
			return nil
		}
		node, exists := nodes[id]
		if !exists {
			return nil
		}
		state[id] = 1
		path = append(path, id)
		for _, dependency := range node.manifest.Dependencies {
			if err := visit(dependency.ID); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[id] = 2
		return nil
	}
	for id := range nodes {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// checkDependents validates reverse edges for a candidate version (upgrade) and
// optionally refuses when any dependent is enabled (disable) or installed
// (remove). Version checks are applied to every installed direct dependent.
func checkDependents(nodes map[ID]dependencyNode, target ID, candidateVersion string, enabledOnly bool) error {
	type edge struct{ from, constraint string }
	edges := make([]edge, 0)
	for _, node := range nodes {
		if node.id == target {
			continue
		}
		for _, dependency := range node.manifest.Dependencies {
			if dependency.ID != target {
				continue
			}
			edges = append(edges, edge{from: string(node.id), constraint: dependency.Version})
			if enabledOnly && !node.enabled() {
				continue
			}
			if candidateVersion != "" {
				ok, err := versionSatisfies(candidateVersion, dependency.Version)
				if err != nil {
					return dependencyError(ErrDependencyIncompatible, "%s declares an invalid range for %s: %v", node.id, target, err)
				}
				if !ok {
					return dependencyError(ErrDependencyIncompatible,
						"%s %s would break %s, which requires %s", target, candidateVersion, node.id, dependency.Version)
				}
			}
			if candidateVersion == "" && enabledOnly && node.enabled() {
				return dependencyError(ErrDependencyInUse, "%s is required by enabled plugin %s", target, node.id)
			}
		}
	}
	if candidateVersion == "" && !enabledOnly && len(edges) > 0 {
		return dependencyError(ErrDependencyInUse, "%s is required by %s", target, edges[0].from)
	}
	return nil
}

func (m *Manager) ensureInstallDependencies(ctx context.Context, manifest Manifest) error {
	candidate := dependencyNode{id: manifest.ID, version: manifest.Version, status: StatusInstalled, manifest: manifest}
	nodes, err := m.loadDependencyNodes(ctx, &candidate)
	if err != nil {
		return err
	}
	if err := detectDependencyCycle(nodes); err != nil {
		return err
	}
	return checkDependencyClosure(nodes, manifest.ID, false)
}

func (m *Manager) ensureEnableDependencies(ctx context.Context, installed Installed) error {
	nodes, err := m.loadDependencyNodes(ctx, &dependencyNode{
		id: installed.ID, version: installed.Version, status: installed.Status, manifest: installed.Manifest,
	})
	if err != nil {
		return err
	}
	if err := detectDependencyCycle(nodes); err != nil {
		return err
	}
	if err := checkDependencyClosure(nodes, installed.ID, true); err != nil {
		return err
	}
	if installed.Manifest.Frontend.Mode == FrontendTrustedJS {
		if !m.allowTrustedJS {
			return ErrTrustedJSDisabled
		}
		if !installed.TrustedJSApproved {
			return ErrTrustedJSNotApproved
		}
	}
	return nil
}

func (m *Manager) ensureUpgradeDependencies(ctx context.Context, current Installed, manifest Manifest) error {
	candidate := dependencyNode{
		id: manifest.ID, version: manifest.Version,
		status: current.Status, manifest: manifest,
	}
	nodes, err := m.loadDependencyNodes(ctx, &candidate)
	if err != nil {
		return err
	}
	if err := detectDependencyCycle(nodes); err != nil {
		return err
	}
	if err := checkDependencyClosure(nodes, manifest.ID, current.Status == StatusEnabled); err != nil {
		return err
	}
	if err := checkDependents(nodes, manifest.ID, manifest.Version, false); err != nil {
		return err
	}
	return nil
}

func (m *Manager) ensureDisableAllowed(ctx context.Context, id ID) error {
	nodes, err := m.loadDependencyNodes(ctx, nil)
	if err != nil {
		return err
	}
	return checkDependents(nodes, id, "", true)
}

func (m *Manager) ensureRemoveAllowed(ctx context.Context, id ID) error {
	nodes, err := m.loadDependencyNodes(ctx, nil)
	if err != nil {
		return err
	}
	return checkDependents(nodes, id, "", false)
}
