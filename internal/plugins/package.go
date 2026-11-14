package plugins

import (
	"archive/zip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MaxPackageArchiveBytes bounds an uploaded/placed plugin zip.
	MaxPackageArchiveBytes int64 = 32 << 20
	// MaxPackageUncompressedBytes bounds one extracted plugin package.
	MaxPackageUncompressedBytes int64 = 256 << 20
	// MaxPackageFileBytes bounds one package file, including a compiled plugin
	// backend binary. Static assets have a stricter limit (MaxAssetBytes).
	MaxPackageFileBytes int64 = 64 << 20
	// MaxPackageFiles bounds the file count of a plugin package.
	MaxPackageFiles = 4096
	// MaxAssetBytes bounds one plugin static asset.
	MaxAssetBytes int64 = 5 << 20
)

var errUnsafeArchive = fmt.Errorf("%w: unsafe archive entry", ErrInvalidPackage)

// preparedPackage is a validated, read-only source directory.
type preparedPackage struct {
	dir     string
	cleanup func()
}

// close removes any temporary extraction directory.
func (p *preparedPackage) close() {
	if p != nil && p.cleanup != nil {
		p.cleanup()
	}
}

// openPackage resolves a server-side package path strictly below
// root/incoming. Directories are used in place; .zip archives are extracted to
// a bounded temporary directory with zip-slip, symlink and size checks.
func openPackage(root, packagePath string) (*preparedPackage, error) {
	if strings.TrimSpace(packagePath) == "" {
		return nil, fmt.Errorf("%w: packagePath is required", ErrInvalidPackage)
	}
	incoming := filepath.Join(root, "incoming")
	if err := os.MkdirAll(incoming, 0o750); err != nil {
		return nil, fmt.Errorf("plugins: create incoming directory: %w", err)
	}
	incomingReal, err := filepath.EvalSymlinks(incoming)
	if err != nil {
		return nil, fmt.Errorf("plugins: resolve incoming directory: %w", err)
	}

	candidate := packagePath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(incoming, candidate)
	}
	candidate = filepath.Clean(candidate)
	original, err := os.Lstat(candidate)
	if err != nil {
		return nil, fmt.Errorf("%w: package path does not exist", ErrInvalidPackage)
	}
	if original.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: package path must not be a symlink", ErrInvalidPackage)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return nil, fmt.Errorf("%w: package path does not exist", ErrInvalidPackage)
	}
	relative, err := filepath.Rel(incomingReal, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("%w: packagePath must stay inside %s", ErrInvalidPackage, incoming)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, fmt.Errorf("%w: stat package: %v", ErrInvalidPackage, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: package root must not be a symlink", ErrInvalidPackage)
	}

	switch {
	case info.IsDir():
		if err := verifyPackageTree(resolved); err != nil {
			return nil, err
		}
		return &preparedPackage{dir: resolved}, nil
	case info.Mode().IsRegular() && strings.HasSuffix(strings.ToLower(resolved), ".zip"):
		if info.Size() <= 0 || info.Size() > MaxPackageArchiveBytes {
			return nil, fmt.Errorf("%w: archive size must be between 1 and %d bytes", ErrInvalidPackage, MaxPackageArchiveBytes)
		}
		tempDir, err := os.MkdirTemp(filepath.Join(root, ".tmp"), "extract-*")
		if err != nil {
			if mkErr := os.MkdirAll(filepath.Join(root, ".tmp"), 0o750); mkErr != nil {
				return nil, fmt.Errorf("plugins: create temporary directory: %w", mkErr)
			}
			tempDir, err = os.MkdirTemp(filepath.Join(root, ".tmp"), "extract-*")
			if err != nil {
				return nil, fmt.Errorf("plugins: create temporary directory: %w", err)
			}
		}
		cleanup := func() { _ = os.RemoveAll(tempDir) }
		if err := extractZip(resolved, tempDir); err != nil {
			cleanup()
			return nil, err
		}
		if err := verifyPackageTree(tempDir); err != nil {
			cleanup()
			return nil, err
		}
		return &preparedPackage{dir: tempDir, cleanup: cleanup}, nil
	default:
		return nil, fmt.Errorf("%w: packagePath must be a directory or .zip archive", ErrInvalidPackage)
	}
}

// verifyPackageTree rejects symlinks, special files, oversized files and
// non-UTF8-normalized traversal names before any package code runs.
func verifyPackageTree(root string) error {
	var total int64
	files := 0
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("%w: walk package: %v", ErrInvalidPackage, walkErr)
		}
		if current == root {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return fmt.Errorf("%w: resolve package entry", ErrInvalidPackage)
		}
		if err := validateRelativePackagePath(filepath.ToSlash(relative)); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidPackage, relative, err)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("%w: stat %s: %v", ErrInvalidPackage, relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %s is not allowed", ErrInvalidPackage, relative)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: special file %s is not allowed", ErrInvalidPackage, relative)
		}
		files++
		if files > MaxPackageFiles {
			return fmt.Errorf("%w: package contains more than %d files", ErrInvalidPackage, MaxPackageFiles)
		}
		if info.Size() < 0 || info.Size() > MaxPackageFileBytes {
			return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidPackage, relative, MaxPackageFileBytes)
		}
		total += info.Size()
		if total > MaxPackageUncompressedBytes {
			return fmt.Errorf("%w: package exceeds %d uncompressed bytes", ErrInvalidPackage, MaxPackageUncompressedBytes)
		}
		if info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
			return fmt.Errorf("%w: %s has setuid/setgid bits", ErrInvalidPackage, relative)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// extractZip performs the archive checks the filesystem walk cannot see:
// duplicate names, absolute/zip-slip paths, encrypted entries and links.
func extractZip(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("%w: open zip: %v", ErrInvalidPackage, err)
	}
	defer reader.Close()

	seen := make(map[string]struct{}, len(reader.File))
	var total int64
	for _, entry := range reader.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		clean := path.Clean(name)
		if clean == "." || clean == "" || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("%w: zip entry %q escapes the package", ErrInvalidPackage, entry.Name)
		}
		if strings.ContainsRune(name, 0) {
			return fmt.Errorf("%w: zip entry contains NUL", ErrInvalidPackage)
		}
		if !entry.Mode().IsDir() && !entry.Mode().IsRegular() {
			return fmt.Errorf("%w: zip entry %q is not a regular file", ErrInvalidPackage, entry.Name)
		}
		if _, exists := seen[clean]; exists {
			return fmt.Errorf("%w: duplicate zip entry %q", ErrInvalidPackage, entry.Name)
		}
		seen[clean] = struct{}{}
		if entry.Flags&0x1 != 0 {
			return fmt.Errorf("%w: encrypted zip entries are not supported", ErrInvalidPackage)
		}
		total += int64(entry.UncompressedSize64)
		if total > MaxPackageUncompressedBytes {
			return fmt.Errorf("%w: archive exceeds %d uncompressed bytes", ErrInvalidPackage, MaxPackageUncompressedBytes)
		}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		relative, err := filepath.Rel(destination, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: zip entry %q escapes the extraction directory", ErrInvalidPackage, entry.Name)
		}
		if entry.Mode().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return fmt.Errorf("plugins: create archive directory: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("plugins: create archive parent: %w", err)
		}
		source, err := entry.Open()
		if err != nil {
			return fmt.Errorf("plugins: open archive entry: %w", err)
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err != nil {
			source.Close()
			return fmt.Errorf("plugins: create extracted file: %w", err)
		}
		written, copyErr := io.Copy(file, io.LimitReader(source, MaxPackageFileBytes+1))
		closeErr := file.Close()
		source.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("plugins: extract archive entry: %v", errors.Join(copyErr, closeErr))
		}
		if written > MaxPackageFileBytes {
			return fmt.Errorf("%w: extracted entry %q exceeds %d bytes", ErrInvalidPackage, entry.Name, MaxPackageFileBytes)
		}
	}
	return nil
}

// packageDigest computes a deterministic content digest excluding manifest.yaml
// so a manifest can carry a checksum of its own payload.
func packageDigest(root string) (string, error) {
	return packageDigestWithManifest(root, false)
}

// packageFullDigest covers every installed file, including manifest.yaml, and
// is persisted for tamper detection before enable/start.
func packageFullDigest(root string) (string, error) {
	return packageDigestWithManifest(root, true)
}

func packageDigestWithManifest(root string, includeManifest bool) (string, error) {
	type entryDigest struct {
		name string
		sum  string
		mode os.FileMode
	}
	entries := make([]entryDigest, 0, 64)
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if !includeManifest && filepath.ToSlash(relative) == "manifest.yaml" {
			return nil
		}
		info, err := os.Stat(current)
		if err != nil {
			return err
		}
		file, err := os.Open(current)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, io.LimitReader(file, MaxAssetBytes+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		entries = append(entries, entryDigest{name: filepath.ToSlash(relative), sum: hex.EncodeToString(hash.Sum(nil)), mode: info.Mode().Perm()})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("plugins: digest package: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	hash := sha256.New()
	for _, entry := range entries {
		_, _ = io.WriteString(hash, entry.name)
		_, _ = io.WriteString(hash, "\x00")
		_, _ = io.WriteString(hash, entry.sum)
		_, _ = io.WriteString(hash, "\x00")
		_, _ = io.WriteString(hash, fmt.Sprintf("%o", entry.mode))
		_, _ = io.WriteString(hash, "\x00")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// copyPackage installs a validated source tree into a fresh version directory.
// The caller is responsible for the final registry transaction; if that fails
// it should remove the returned directory.
func copyPackage(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("plugins: create plugin directory: %w", err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("plugins: generate staging name: %w", err)
	}
	stage := destination + ".staging-" + hex.EncodeToString(random[:])
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o750); err != nil {
		return fmt.Errorf("plugins: create staging directory: %w", err)
	}
	err := filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(stage, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %s is not allowed", ErrInvalidPackage, relative)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		mode := os.FileMode(0o640)
		if info.Mode()&0o111 != 0 {
			mode = 0o750
		}
		in, err := os.Open(current)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(in, MaxPackageFileBytes+1))
		inCloseErr := in.Close()
		closeErr := out.Close()
		if copyErr != nil || inCloseErr != nil || closeErr != nil {
			return errors.Join(copyErr, inCloseErr, closeErr)
		}
		return nil
	})
	if err != nil {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("plugins: copy package: %w", err)
	}
	if err := os.Rename(stage, destination); err != nil {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("plugins: activate package: %w", err)
	}
	return nil
}

// packageManifest loads and validates manifest.yaml from a prepared package.
func packageManifest(prepared *preparedPackage, allowExec bool, allowTrustedJS ...bool) (Manifest, []Finding, string, error) {
	if prepared == nil {
		return Manifest{}, nil, "", ErrInvalidPackage
	}
	manifestPath := filepath.Join(prepared.dir, "manifest.yaml")
	info, err := os.Lstat(manifestPath)
	if err != nil {
		return Manifest{}, []Finding{{Path: "manifest.yaml", Code: "missing_manifest", Message: "manifest.yaml is required at the package root", Severity: "error"}}, "", nil
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Manifest{}, []Finding{{Path: "manifest.yaml", Code: "invalid_manifest", Message: "manifest.yaml must be a regular file", Severity: "error"}}, "", nil
	}
	if info.Size() > MaxManifestBytes {
		return Manifest{}, []Finding{{Path: "manifest.yaml", Code: "manifest_too_large", Message: "manifest.yaml exceeds the size limit", Severity: "error"}}, "", nil
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, nil, "", fmt.Errorf("plugins: read manifest: %w", err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return Manifest{}, []Finding{{Path: "manifest.yaml", Code: "invalid_manifest", Message: err.Error(), Severity: "error"}}, "", nil
	}
	manifest = NormalizeManifest(manifest)
	findings := ValidateManifest(manifest, allowExec, allowTrustedJS...)
	findings = append(findings, validatePackageContents(prepared.dir, manifest)...)
	if hasErrorFinding(findings) {
		return manifest, findings, "", nil
	}
	contentDigest, err := packageDigest(prepared.dir)
	if err != nil {
		return manifest, nil, "", err
	}
	if manifest.Checksum != "" {
		expected := manifest.Checksum
		if !strings.HasPrefix(expected, "sha256:") {
			expected = "sha256:" + expected
		}
		if !strings.EqualFold(expected, contentDigest) {
			findings = append(findings, Finding{
				Path: "checksum", Code: "checksum_mismatch",
				Message: "package content does not match the manifest checksum", Severity: "error",
			})
			return manifest, findings, "", nil
		}
	}
	fullDigest, err := packageFullDigest(prepared.dir)
	if err != nil {
		return manifest, nil, "", err
	}
	return manifest, findings, fullDigest, nil
}

// validatePackageContents checks declared files exist and stay inside their
// declared directories.
func validatePackageContents(root string, manifest Manifest) []Finding {
	findings := make([]Finding, 0, 4)
	add := func(path, code, message string) {
		findings = append(findings, Finding{Path: path, Code: code, Message: message, Severity: "error"})
	}

	if manifest.Backend.Mode == BackendExec {
		target := filepath.Join(root, filepath.FromSlash(manifest.Backend.Entrypoint))
		if err := ensurePathWithin(root, target); err != nil {
			add("backend.entrypoint", "entrypoint_escape", err.Error())
		} else if info, err := os.Lstat(target); err != nil || !info.Mode().IsRegular() {
			add("backend.entrypoint", "missing_entrypoint", "exec entrypoint must exist as a regular file in the package")
		} else if info.Mode()&0o111 == 0 {
			add("backend.entrypoint", "entrypoint_not_executable", "exec entrypoint must have an executable bit")
		}
	}

	assetsRoot := ""
	if manifest.Assets != "" {
		assetsRoot = filepath.Join(root, filepath.FromSlash(manifest.Assets))
		if err := ensurePathWithin(root, assetsRoot); err != nil {
			add("assets", "assets_escape", err.Error())
		} else if info, err := os.Lstat(assetsRoot); err != nil || !info.IsDir() {
			add("assets", "missing_assets", "declared assets directory does not exist")
		} else if err := filepath.WalkDir(assetsRoot, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() > MaxAssetBytes {
				relative, _ := filepath.Rel(assetsRoot, current)
				add("assets/"+filepath.ToSlash(relative), "asset_too_large", "static assets must be at most 5 MiB")
			}
			return nil
		}); err != nil {
			add("assets", "invalid_assets", "assets directory cannot be read")
		}
	}
	if manifest.Frontend.Mode != "" && manifest.Frontend.Entry != "" {
		if assetsRoot == "" {
			add("frontend.entry", "missing_assets", "frontend requires an assets directory")
		} else {
			entry := filepath.Join(assetsRoot, filepath.FromSlash(manifest.Frontend.Entry))
			if err := ensurePathWithin(assetsRoot, entry); err != nil {
				add("frontend.entry", "entry_escape", err.Error())
			} else if info, err := os.Lstat(entry); err != nil || !info.Mode().IsRegular() {
				add("frontend.entry", "missing_frontend_entry", "frontend.entry must exist inside assets")
			}
		}
	}

	if manifest.Migrations != "" {
		migrationsRoot := filepath.Join(root, filepath.FromSlash(manifest.Migrations))
		if err := ensurePathWithin(root, migrationsRoot); err != nil {
			add("migrations", "migrations_escape", err.Error())
		} else if info, err := os.Lstat(migrationsRoot); err != nil || !info.IsDir() {
			add("migrations", "missing_migrations", "declared migrations directory does not exist")
		} else {
			entries, err := os.ReadDir(migrationsRoot)
			if err != nil {
				add("migrations", "unreadable_migrations", "migrations directory cannot be read")
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".sql") {
					add("migrations/"+entry.Name(), "invalid_migration_file", "migrations directory may contain only .sql files")
				}
			}
		}
	}
	return findings
}

// ensurePathWithin resolves the parent so a symlinked directory cannot escape.
func ensurePathWithin(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("path escapes its declared directory")
	}
	parent := filepath.Dir(targetAbs)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		// A missing file is handled by the caller's existence check.
		return nil
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil
	}
	relative, err = filepath.Rel(resolvedRoot, resolvedParent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("path escapes its declared directory through a symlink")
	}
	return nil
}
