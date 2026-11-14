package deployment

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	maxArchiveEntries   = MaxFiles + 1
	maxArchiveRatio     = 200
	maxArchiveInputSize = MaxPackageBytes + MaxManifestBytes
)

// ArchiveImport is an alias for ImportArchive.
func ArchiveImport(data []byte) ([]byte, []PackageFile, []Finding) { return ImportArchive(data) }

// ImportArchive reads a deployment package ZIP entirely in memory. It never
// extracts to the filesystem, never follows symlinks and never executes a
// package file.
//
// Security bounds: bounded compressed/uncompressed input, 257 entries maximum,
// 1 MiB per file, 64 KiB manifest, 16 MiB total, a compression-ratio limit,
// duplicate-name rejection, encrypted-entry rejection and traversal-safe paths.
func ImportArchive(data []byte) ([]byte, []PackageFile, []Finding) {
	if len(data) == 0 {
		return nil, nil, []Finding{errorFinding("archive", "ARCHIVE_EMPTY", "package archive is empty")}
	}
	if len(data) > maxArchiveInputSize {
		return nil, nil, []Finding{errorFinding("archive", "ARCHIVE_TOO_LARGE", "package archive input exceeds the allowed size")}
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, []Finding{errorFinding("archive", "ARCHIVE_INVALID", "package is not a valid ZIP archive")}
	}
	if len(reader.File) > maxArchiveEntries {
		return nil, nil, []Finding{errorFinding("archive", "ARCHIVE_ENTRY_LIMIT", fmt.Sprintf("archive contains more than %d entries", maxArchiveEntries))}
	}

	var manifest []byte
	files := make([]PackageFile, 0, len(reader.File))
	findings := make([]Finding, 0)
	seen := make(map[string]struct{}, len(reader.File))
	total := int64(0)
	for _, entry := range reader.File {
		name := entry.Name
		if strings.HasSuffix(name, "/") {
			continue
		}
		if entry.Flags&0x1 != 0 {
			findings = append(findings, errorFinding("archive."+name, "ARCHIVE_ENCRYPTED_ENTRY", "encrypted archive entries are not allowed"))
			continue
		}
		if entry.Mode()&^0o777 != 0 && !entry.Mode().IsRegular() {
			findings = append(findings, errorFinding("archive."+name, "ARCHIVE_ENTRY_TYPE_UNSAFE", "archive entries must be regular files"))
			continue
		}
		if finding := validateArchiveName(name); finding != nil {
			findings = append(findings, *finding)
			continue
		}
		if _, exists := seen[name]; exists {
			findings = append(findings, errorFinding("archive."+name, "ARCHIVE_DUPLICATE_ENTRY", "archive entry name must be unique"))
			continue
		}
		seen[name] = struct{}{}
		if entry.UncompressedSize64 > uint64(MaxFileBytes) && name != "manifest.yaml" {
			findings = append(findings, errorFinding("archive."+name, "ARCHIVE_FILE_TOO_LARGE", fmt.Sprintf("archive entry exceeds %d bytes", MaxFileBytes)))
			continue
		}
		if name == "manifest.yaml" && entry.UncompressedSize64 > uint64(MaxManifestBytes) {
			findings = append(findings, errorFinding("archive.manifest.yaml", "MANIFEST_TOO_LARGE", fmt.Sprintf("manifest exceeds %d bytes", MaxManifestBytes)))
			continue
		}
		if entry.CompressedSize64 > 0 && entry.UncompressedSize64 > entry.CompressedSize64*maxArchiveRatio {
			findings = append(findings, errorFinding("archive."+name, "ARCHIVE_RATIO_EXCEEDED", "archive entry compression ratio is unsafe"))
			continue
		}
		limit := int64(MaxFileBytes)
		if name == "manifest.yaml" {
			limit = MaxManifestBytes
		}
		content, err := readArchiveEntry(entry, limit)
		if err != nil {
			findings = append(findings, errorFinding("archive."+name, "ARCHIVE_ENTRY_UNREADABLE", "archive entry could not be read safely"))
			continue
		}
		if name == "manifest.yaml" {
			manifest = content
			continue
		}
		total += int64(len(content))
		if total > MaxPackageBytes {
			findings = append(findings, errorFinding("archive", "PACKAGE_TOO_LARGE", fmt.Sprintf("package exceeds %d bytes", MaxPackageBytes)))
			return nil, nil, findings
		}
		files = append(files, PackageFile{Path: name, Content: content})
	}
	if len(findings) > 0 {
		return nil, nil, findings
	}
	if len(manifest) == 0 {
		return nil, nil, []Finding{errorFinding("archive.manifest.yaml", "ARCHIVE_MANIFEST_REQUIRED", "archive must contain manifest.yaml")}
	}
	return manifest, files, nil
}

func validateArchiveName(name string) *Finding {
	if strings.ContainsAny(name, `\`+"\x00") || strings.HasPrefix(name, "/") {
		return ptrFinding(errorFinding("archive."+name, "ARCHIVE_PATH_UNSAFE", "archive entry path must be relative without backslashes or NUL"))
	}
	cleaned := path.Clean(name)
	if cleaned != name || cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return ptrFinding(errorFinding("archive."+name, "ARCHIVE_PATH_UNSAFE", "archive entry path contains traversal or redundant segments"))
	}
	if finding := validatePackagePath(name, "archive."+name); finding != nil {
		return finding
	}
	return nil
}

func readArchiveEntry(entry *zip.File, limit int64) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("archive entry exceeds limit")
	}
	return content, nil
}
