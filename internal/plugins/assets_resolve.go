package plugins

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ResolveAsset returns a regular, non-symlink file inside the current enabled
// plugin's declared assets directory. The returned path is never served by the
// core API origin.
func (m *Manager) ResolveAsset(ctx context.Context, id ID, relative string) (string, fs.FileInfo, Manifest, error) {
	installed, err := m.loadInstalled(ctx, id)
	if err != nil {
		return "", nil, Manifest{}, err
	}
	if installed.Status != StatusEnabled {
		return "", nil, Manifest{}, ErrBackendUnavailable
	}
	if installed.Manifest.Assets == "" {
		return "", nil, Manifest{}, ErrNotFound
	}
	if err := validateRelativePackagePath(relative); err != nil {
		return "", nil, Manifest{}, ErrNotFound
	}
	packagePath := filepath.Join(m.root, string(id), installed.Version)
	assetsRoot := filepath.Join(packagePath, filepath.FromSlash(installed.Manifest.Assets))
	if err := ensurePathWithin(packagePath, assetsRoot); err != nil {
		return "", nil, Manifest{}, ErrNotFound
	}
	target := filepath.Join(assetsRoot, filepath.FromSlash(relative))
	if err := ensurePathWithin(assetsRoot, target); err != nil {
		return "", nil, Manifest{}, ErrNotFound
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", nil, Manifest{}, ErrNotFound
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > MaxAssetBytes {
		return "", nil, Manifest{}, ErrNotFound
	}
	if strings.HasPrefix(info.Name(), ".") {
		return "", nil, Manifest{}, ErrNotFound
	}
	return target, info, installed.Manifest, nil
}

// AssetServerOrigins exposes the configured separated origin for boot wiring of
// the cmd/plugin-assets listener. It is not a secret.
func (m *Manager) AssetServerOrigins() (assetOrigin, platformOrigin string) {
	if m == nil {
		return "", ""
	}
	return m.assetOrigin, m.platformOrigin
}
