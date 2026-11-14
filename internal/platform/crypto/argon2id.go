package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/nrf24l01/actfb/internal/config"
	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidHash   = errors.New("invalid argon2id hash")
	ErrInvalidParams = errors.New("invalid argon2id parameters")
)

// The verifier is deliberately bounded because PHC strings can originate from
// an untrusted database row or import. Keep these limits comfortably above the
// OWASP Argon2id recommendation (m=19456 KiB, t=2, p=1, 32-byte key) while
// preventing attacker-controlled parameters from triggering huge allocations
// or unbounded CPU work.
const (
	maxEncodedHashLen = 512

	minTime = 1
	maxTime = 10

	minMemoryKiB = 8 * 1024
	maxMemoryKiB = 256 * 1024

	minThreads = 1
	maxThreads = 16

	minKeyLen = 16
	maxKeyLen = 64

	minSaltLen = 8
	maxSaltLen = 64
)

func validateCostParams(iterations, memoryKiB, threads int) error {
	if iterations < minTime || iterations > maxTime {
		return fmt.Errorf(
			"%w: time must be between %d and %d, got %d",
			ErrInvalidParams,
			minTime,
			maxTime,
			iterations,
		)
	}

	if memoryKiB < minMemoryKiB || memoryKiB > maxMemoryKiB {
		return fmt.Errorf(
			"%w: memory must be between %d and %d KiB, got %d",
			ErrInvalidParams,
			minMemoryKiB,
			maxMemoryKiB,
			memoryKiB,
		)
	}

	if threads < minThreads || threads > maxThreads {
		return fmt.Errorf(
			"%w: threads must be between %d and %d, got %d",
			ErrInvalidParams,
			minThreads,
			maxThreads,
			threads,
		)
	}

	if memoryKiB < 8*threads {
		return fmt.Errorf(
			"%w: memory must be at least 8 KiB per thread, got %d KiB for %d threads",
			ErrInvalidParams,
			memoryKiB,
			threads,
		)
	}

	return nil
}

func validateKeyLen(keyLen int) error {
	if keyLen < minKeyLen || keyLen > maxKeyLen {
		return fmt.Errorf(
			"%w: key length must be between %d and %d bytes, got %d",
			ErrInvalidParams,
			minKeyLen,
			maxKeyLen,
			keyLen,
		)
	}

	return nil
}

func validateParams(c config.Argon2idConfig) error {
	if err := validateCostParams(c.Time, c.Memory, c.Threads); err != nil {
		return err
	}

	return validateKeyLen(c.KeyLen)
}

func decodeCanonicalBase64(value string) ([]byte, error) {
	encoder := base64.RawStdEncoding

	decoded, err := encoder.DecodeString(value)
	if err != nil {
		return nil, err
	}

	// Reject alternate spellings that decode successfully but are not the
	// canonical encoding (for example non-zero trailing padding bits).
	if encoder.EncodeToString(decoded) != value {
		return nil, errors.New("non-canonical base64")
	}

	return decoded, nil
}

func Hash(password string, c config.Argon2idConfig) (string, error) {
	if err := validateParams(c); err != nil {
		return "", err
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(password),
		salt,
		uint32(c.Time),
		uint32(c.Memory),
		uint8(c.Threads),
		uint32(c.KeyLen),
	)

	encoder := base64.RawStdEncoding

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		c.Memory,
		c.Time,
		c.Threads,
		encoder.EncodeToString(salt),
		encoder.EncodeToString(hash),
	), nil
}

func VerifyArgon2id(password, encodedHash string) (bool, error) {
	if len(encodedHash) == 0 || len(encodedHash) > maxEncodedHashLen {
		return false, ErrInvalidHash
	}

	parts := strings.Split(encodedHash, "$")

	if len(parts) != 6 ||
		parts[0] != "" ||
		parts[1] != "argon2id" ||
		parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, ErrInvalidHash
	}

	var (
		memoryKiB  int
		iterations int
		threads    int
	)

	n, err := fmt.Sscanf(
		parts[3],
		"m=%d,t=%d,p=%d",
		&memoryKiB,
		&iterations,
		&threads,
	)
	if err != nil || n != 3 ||
		parts[3] != fmt.Sprintf(
			"m=%d,t=%d,p=%d",
			memoryKiB,
			iterations,
			threads,
		) {
		return false, ErrInvalidHash
	}

	// Validate attacker-controlled parameters before passing them to Argon2,
	// which would otherwise allocate based on them or panic on invalid values.
	if err := validateCostParams(iterations, memoryKiB, threads); err != nil {
		return false, ErrInvalidHash
	}

	salt, err := decodeCanonicalBase64(parts[4])
	if err != nil || len(salt) < minSaltLen || len(salt) > maxSaltLen {
		return false, ErrInvalidHash
	}

	expectedHash, err := decodeCanonicalBase64(parts[5])
	if err != nil || validateKeyLen(len(expectedHash)) != nil {
		return false, ErrInvalidHash
	}

	actualHash := argon2.IDKey(
		[]byte(password),
		salt,
		uint32(iterations),
		uint32(memoryKiB),
		uint8(threads),
		uint32(len(expectedHash)),
	)

	return subtle.ConstantTimeCompare(
		actualHash,
		expectedHash,
	) == 1, nil
}
