package files

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
	platforms3 "github.com/nrf24l01/actfb/internal/platform/s3"
)

const (
	clamAVChunkSize = 64 * 1024
	clamAVTimeout   = 30 * time.Second
	scanDownloadCap = MaxUploadBytes
)

// ScannerConfig is the scanner worker's trusted runtime policy. RequireClamAV
// is retained for operators/tests, but a file is never marked READY without a
// successful response from a real ClamAV daemon.
type ScannerConfig struct {
	RequireClamAV bool
	ClamAVAddress string
}

// Scanner validates quarantine objects with the actual bytes. It is the only
// component that writes READY/clean keys. It never exposes bucket/key values.
type Scanner struct {
	store  *store
	s3     *platforms3.Client
	config ScannerConfig
}

// NewScanner constructs the trusted worker-side scanner.
func NewScanner(db *postgres.DB, client *platforms3.Client, config ScannerConfig) (*Scanner, error) {
	if db == nil {
		return nil, errors.New("files: scanner requires postgres")
	}
	if client == nil {
		return nil, errors.New("files: scanner requires S3")
	}
	return &Scanner{store: newStore(db), s3: client, config: config}, nil
}

// Scan validates one file exactly once. Already-terminal states are idempotent
// no-ops. ErrScannerUnavailable is transient and leaves the row VALIDATING.
func (s *Scanner) Scan(ctx context.Context, fileID uuid.UUID) error {
	record, err := s.store.scanCandidate(ctx, fileID)
	if err != nil {
		return err
	}
	switch record.Status {
	case StatusReady, StatusAttached, StatusRejected, StatusExpired, StatusAwaitingUpload:
		return nil
	case StatusValidating:
	default:
		return nil
	}

	tempPath, actualSize, actualSHA, err := s.downloadBounded(ctx, record)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)

	if actualSize != record.Size {
		return s.reject(ctx, record, "FILE_SIZE_MISMATCH", "uploaded size does not match the declared size")
	}
	if !strings.EqualFold(actualSHA, record.SHA256) {
		return s.reject(ctx, record, "FILE_CHECKSUM_MISMATCH", "uploaded SHA-256 does not match the declared hash")
	}

	detected, err := detectContentType(tempPath)
	if err != nil {
		return s.reject(ctx, record, "FILE_TYPE_UNREADABLE", "could not read uploaded bytes")
	}
	if !contentTypesCompatible(record.ContentType, detected) {
		return s.reject(ctx, record, "UNSUPPORTED_FILE_TYPE", "detected content type is not compatible with the declared type")
	}
	if isZip(detected) || isZipMagic(tempPath) {
		if err := validateArchive(tempPath); err != nil {
			return s.reject(ctx, record, "UNSAFE_ARCHIVE", err.Error())
		}
	}

	if err := s.scanClamAV(ctx, tempPath); err != nil {
		if errors.Is(err, errMalwareFound) {
			return s.reject(ctx, record, "MALWARE_DETECTED", "a malware signature matched")
		}
		_ = s.store.markScannerUnavailable(ctx, record.ID)
		return fmt.Errorf("%w: %v", ErrScannerUnavailable, err)
	}

	cleanKey := fmt.Sprintf("clean/%s/%s/%s/%s", record.BoardID, record.ModuleID, record.ID, uuid.NewString())
	if err := s.uploadClean(ctx, tempPath, actualSize, detected, record.SHA256, cleanKey); err != nil {
		return fmt.Errorf("files: upload clean object: %w", err)
	}

	updated, err := s.store.markFileReadyConditional(ctx, record.ID, detected, s.s3.Bucket(), cleanKey, record.SHA256)
	if err != nil {
		_ = s.s3.Delete(ctx, cleanKey)
		return err
	}
	if !updated {
		// Another attempt or the owning submission transaction won the race.
		// The freshly written clean object is unreachable; remove the exact key.
		_ = s.s3.Delete(ctx, cleanKey)
		return nil
	}

	if record.Purpose == PurposeTaskAttachment || record.Purpose == PurposePentestAttachment {
		if _, err := s.store.attachAdministrativeFile(ctx, record.ID); err != nil {
			return err
		}
	}
	_ = s.s3.Delete(ctx, record.QuarantineKey)
	return nil
}

func (s *Scanner) reject(ctx context.Context, record *FileRecord, code, reason string) error {
	if _, err := s.store.markFileRejected(ctx, record.ID, code); err != nil {
		return err
	}
	_ = s.s3.Delete(ctx, record.QuarantineKey)
	_ = reason // reasons are internal and never persisted/logged by this layer
	return nil
}

func (s *Scanner) downloadBounded(ctx context.Context, record *FileRecord) (string, int64, string, error) {
	if record.Size <= 0 || record.Size > scanDownloadCap {
		return "", 0, "", reject("FILE_TOO_LARGE", "declared size exceeds the scan cap")
	}
	object, err := s.s3.Get(ctx, record.QuarantineKey)
	if err != nil {
		return "", 0, "", fmt.Errorf("files: download quarantine object: %w", err)
	}
	defer object.Body.Close()

	temp, err := os.CreateTemp("", "actfb-scan-*")
	if err != nil {
		return "", 0, "", fmt.Errorf("files: create scan temp file: %w", err)
	}
	tempPath := temp.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tempPath)
		}
	}()

	hash := sha256.New()
	limited := io.LimitReader(object.Body, record.Size+1)
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), limited)
	if copyErr != nil {
		temp.Close()
		return "", 0, "", fmt.Errorf("files: copy quarantine object: %w", copyErr)
	}
	if err := temp.Close(); err != nil {
		return "", 0, "", fmt.Errorf("files: close scan temp file: %w", err)
	}
	if written != record.Size {
		return tempPath, written, hex.EncodeToString(hash.Sum(nil)), nil
	}
	remove = false
	return tempPath, written, hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Scanner) uploadClean(ctx context.Context, tempPath string, size int64, contentType, sha256Hex, key string) error {
	file, err := os.Open(tempPath)
	if err != nil {
		return fmt.Errorf("open validated temp file: %w", err)
	}
	defer file.Close()

	if err := s.s3.Put(ctx, key, file, size, platforms3.PutOptions{
		ContentType: contentType,
		IfNoneMatch: "*",
		Metadata: map[string]string{
			"actfb-sha256": sha256Hex,
			"actfb-size":   strconv.FormatInt(size, 10),
		},
	}); err != nil {
		return err
	}
	head, err := s.s3.Head(ctx, key)
	if err != nil {
		return fmt.Errorf("verify clean object: %w", err)
	}
	if head.Size != size {
		return fmt.Errorf("clean object size mismatch")
	}
	return nil
}

var (
	errMalwareFound = errors.New("clamav: malware found")
	errNotClean     = errors.New("clamav: response was not clean")
)

func (s *Scanner) scanClamAV(ctx context.Context, tempPath string) error {
	address := strings.TrimSpace(s.config.ClamAVAddress)
	if address == "" {
		return errors.New("ClamAV address is not configured")
	}
	dialer := net.Dialer{Timeout: clamAVTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect ClamAV: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(clamAVTimeout))

	file, err := os.Open(tempPath)
	if err != nil {
		return fmt.Errorf("open scan temp file: %w", err)
	}
	defer file.Close()

	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return fmt.Errorf("write ClamAV command: %w", err)
	}
	chunk := make([]byte, clamAVChunkSize)
	var length [4]byte
	for {
		n, readErr := file.Read(chunk)
		if n > 0 {
			binary.BigEndian.PutUint32(length[:], uint32(n))
			if _, err := conn.Write(length[:]); err != nil {
				return fmt.Errorf("write ClamAV length: %w", err)
			}
			if _, err := conn.Write(chunk[:n]); err != nil {
				return fmt.Errorf("write ClamAV chunk: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read scan temp file: %w", readErr)
		}
	}
	binary.BigEndian.PutUint32(length[:], 0)
	if _, err := conn.Write(length[:]); err != nil {
		return fmt.Errorf("write ClamAV terminator: %w", err)
	}

	reader := bufio.NewReader(io.LimitReader(conn, 4096))
	response, err := reader.ReadString(0)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read ClamAV response: %w", err)
	}
	clean := strings.TrimRight(response, "\x00\r\n")
	if strings.Contains(clean, "FOUND") {
		return errMalwareFound
	}
	if !strings.Contains(clean, "OK") {
		return fmt.Errorf("%w: %s", errNotClean, clean)
	}
	return nil
}

func detectContentType(tempPath string) (string, error) {
	file, err := os.Open(tempPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	return http.DetectContentType(head[:n]), nil
}

func normalizeMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func contentTypesCompatible(declared, detected string) bool {
	declared = normalizeMediaType(declared)
	detected = normalizeMediaType(detected)
	if declared == "" || declared == "*/*" || declared == "application/octet-stream" {
		return true
	}
	if detected == "" || detected == "application/octet-stream" {
		return true
	}
	if declared == detected {
		return true
	}
	declaredGroup, _, _ := strings.Cut(declared, "/")
	detectedGroup, _, _ := strings.Cut(detected, "/")
	if declaredGroup == "image" && detectedGroup == "image" {
		return true
	}
	if isZip(detected) {
		return true
	}
	if detected == "text/plain" {
		switch declared {
		case "application/json", "application/xml", "application/yaml", "application/x-yaml",
			"application/javascript", "application/x-sh", "application/sql", "text/csv",
			"application/x-httpd-php", "application/x-python", "image/svg+xml":
			return true
		}
		if declaredGroup == "text" {
			return true
		}
	}
	if detected == "application/pdf" && declared == "application/pdf" {
		return true
	}
	return false
}

func isZip(contentType string) bool {
	switch normalizeMediaType(contentType) {
	case "application/zip", "application/x-zip-compressed", "application/x-zip":
		return true
	default:
		return false
	}
}

func isZipMagic(tempPath string) bool {
	file, err := os.Open(tempPath)
	if err != nil {
		return false
	}
	defer file.Close()
	head := make([]byte, 4)
	n, _ := io.ReadFull(file, head)
	return n == 4 && head[0] == 'P' && head[1] == 'K' &&
		(head[2] == 3 || head[2] == 5 || head[2] == 7)
}

func validateArchive(tempPath string) error {
	reader, err := zip.OpenReader(tempPath)
	if err != nil {
		return fmt.Errorf("archive is malformed")
	}
	defer reader.Close()
	if len(reader.File) > maxArchiveEntries {
		return fmt.Errorf("archive contains too many entries")
	}
	var uncompressedTotal uint64
	for _, entry := range reader.File {
		name := strings.ReplaceAll(entry.Name, `\`, "/")
		clean := path.Clean(name)
		if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") ||
			path.IsAbs(clean) || strings.Contains(clean, "/../") || strings.Contains(name, "\x00") ||
			(len(clean) >= 2 && clean[1] == ':') {
			return fmt.Errorf("archive entry path is unsafe")
		}
		if entry.Mode()&os.ModeSymlink != 0 || entry.Mode()&os.ModeDevice != 0 {
			return fmt.Errorf("archive contains a link or device entry")
		}
		if entry.Flags&0x1 != 0 {
			return fmt.Errorf("encrypted archive entries are not supported")
		}
		if entry.UncompressedSize64 > uint64(maxArchiveUnpacked) {
			return fmt.Errorf("archive entry expands beyond the cap")
		}
		uncompressedTotal += entry.UncompressedSize64
		if uncompressedTotal > uint64(maxArchiveUnpacked) {
			return fmt.Errorf("archive expands beyond the cap")
		}
		if entry.CompressedSize64 > 0 && entry.UncompressedSize64/entry.CompressedSize64 > maxArchiveRatio {
			return fmt.Errorf("archive compression ratio is unsafe")
		}
	}
	return nil
}
