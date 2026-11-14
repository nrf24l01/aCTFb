package deployment

import (
	"encoding/base64"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// PackageFile is one inline package file. Path is relative to the package root;
// Content is the decoded bytes. No filesystem path is ever used to read a file.
type PackageFile struct {
	Path    string
	Content []byte
}

// FileInput is the transport shape shared with the generated
// DeploymentTemplateFile model. It is decoded in memory only.
type FileInput struct {
	Path          string
	ContentBase64 string
}

var (
	filePathPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*(?:/[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*)*$`)
	fileModePattern   = regexp.MustCompile(`^0[0-7]{3}$`)
	absolutePathCheck = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
)

// DecodeFiles validates and decodes inline package files. It enforces:
//   - 256 files maximum, 1 MiB per file and 16 MiB total;
//   - relative, traversal-free, backslash/NUL-free paths;
//   - unique path names and strict standard base64 content.
func DecodeFiles(inputs []FileInput) ([]PackageFile, []Finding) {
	if len(inputs) > MaxFiles {
		return nil, []Finding{errorFinding("files", "FILE_LIMIT_EXCEEDED", fmt.Sprintf("at most %d files are allowed", MaxFiles))}
	}
	files := make([]PackageFile, 0, len(inputs))
	findings := make([]Finding, 0)
	seen := make(map[string]struct{}, len(inputs))
	total := int64(0)
	for index, input := range inputs {
		fieldPath := fmt.Sprintf("files[%d]", index)
		if finding := validatePackagePath(input.Path, fieldPath+".path"); finding != nil {
			findings = append(findings, *finding)
			continue
		}
		if _, exists := seen[input.Path]; exists {
			findings = append(findings, errorFinding(fieldPath+".path", "FILE_DUPLICATE_PATH", "package file path must be unique"))
			continue
		}
		seen[input.Path] = struct{}{}
		if input.ContentBase64 == "" {
			findings = append(findings, errorFinding(fieldPath+".contentBase64", "FILE_CONTENT_REQUIRED", "file content is required"))
			continue
		}
		content, err := base64.StdEncoding.DecodeString(input.ContentBase64)
		if err != nil {
			findings = append(findings, errorFinding(fieldPath+".contentBase64", "FILE_CONTENT_INVALID", "file content must be standard base64"))
			continue
		}
		if len(content) > MaxFileBytes {
			findings = append(findings, errorFinding(fieldPath+".contentBase64", "FILE_TOO_LARGE", fmt.Sprintf("file exceeds %d bytes", MaxFileBytes)))
			continue
		}
		total += int64(len(content))
		if total > MaxPackageBytes {
			findings = append(findings, errorFinding("files", "PACKAGE_TOO_LARGE", fmt.Sprintf("package exceeds %d bytes", MaxPackageBytes)))
			return nil, findings
		}
		files = append(files, PackageFile{Path: input.Path, Content: content})
	}
	return files, findings
}

func validatePackagePath(value, fieldPath string) *Finding {
	if value == "" {
		return ptrFinding(errorFinding(fieldPath, "FILE_PATH_REQUIRED", "file path is required"))
	}
	if len(value) > MaxPathLength {
		return ptrFinding(errorFinding(fieldPath, "FILE_PATH_TOO_LONG", fmt.Sprintf("file path exceeds %d characters", MaxPathLength)))
	}
	if strings.ContainsAny(value, "\x00\\") || strings.HasPrefix(value, "/") {
		return ptrFinding(errorFinding(fieldPath, "FILE_PATH_UNSAFE", "file path must be relative and must not contain NUL or backslashes"))
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ptrFinding(errorFinding(fieldPath, "FILE_PATH_UNSAFE", "file path must not contain empty, . or .. segments"))
		}
	}
	if !filePathPattern.MatchString(value) {
		return ptrFinding(errorFinding(fieldPath, "FILE_PATH_UNSAFE", "file path contains unsupported characters"))
	}
	return nil
}

func validateTargetPath(value, fieldPath string) *Finding {
	if value == "" {
		return ptrFinding(errorFinding(fieldPath, "FILE_TARGET_REQUIRED", "file target is required"))
	}
	if len(value) > MaxPathLength {
		return ptrFinding(errorFinding(fieldPath, "FILE_TARGET_TOO_LONG", fmt.Sprintf("file target exceeds %d characters", MaxPathLength)))
	}
	if strings.ContainsAny(value, "\x00\\") || !strings.HasPrefix(value, "/") {
		return ptrFinding(errorFinding(fieldPath, "FILE_TARGET_UNSAFE", "file target must be an absolute POSIX path without NUL or backslashes"))
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "/" || strings.Contains(cleaned, "/../") || strings.HasSuffix(cleaned, "/..") {
		return ptrFinding(errorFinding(fieldPath, "FILE_TARGET_UNSAFE", "file target must not contain traversal or redundant segments"))
	}
	if !absolutePathCheck.MatchString(cleaned) {
		return ptrFinding(errorFinding(fieldPath, "FILE_TARGET_UNSAFE", "file target contains unsupported characters"))
	}
	return nil
}

func validateMode(value, fieldPath string) *Finding {
	if value == "" {
		return nil
	}
	if !fileModePattern.MatchString(value) {
		return ptrFinding(errorFinding(fieldPath, "FILE_MODE_INVALID", "file mode must be a four-digit octal string such as 0440"))
	}
	return nil
}

func fileMap(files []PackageFile) map[string][]byte {
	result := make(map[string][]byte, len(files))
	for _, file := range files {
		result[file.Path] = append([]byte(nil), file.Content...)
	}
	return result
}

func sortedFilePaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	return paths
}

func encodeFiles(files map[string][]byte) []FileInput {
	inputs := make([]FileInput, 0, len(files))
	for _, filePath := range sortedFilePaths(files) {
		inputs = append(inputs, FileInput{
			Path:          filePath,
			ContentBase64: base64.StdEncoding.EncodeToString(files[filePath]),
		})
	}
	return inputs
}

func ptrFinding(finding Finding) *Finding { return &finding }
