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

var ErrInvalidHash = errors.New("invalid argon2id hash")

func Hash(password string, c config.Argon2idConfig) (string, error) {
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
	if len(encodedHash) > 512 {
		return false, ErrInvalidHash
	}

	parts := strings.Split(encodedHash, "$")

	if len(parts) != 6 ||
		parts[0] != "" ||
		parts[1] != "argon2id" ||
		parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, ErrInvalidHash
	}

	var cfg config.Argon2idConfig

	n, err := fmt.Sscanf(
		parts[3],
		"m=%d,t=%d,p=%d",
		&cfg.Memory,
		&cfg.Time,
		&cfg.Threads,
	)
	if err != nil || n != 3 ||
		parts[3] != fmt.Sprintf(
			"m=%d,t=%d,p=%d",
			cfg.Memory,
			cfg.Time,
			cfg.Threads,
		) {
		return false, ErrInvalidHash
	}

	encoder := base64.RawStdEncoding

	salt, err := encoder.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, ErrInvalidHash
	}

	expectedHash, err := encoder.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}

	cfg.KeyLen = len(expectedHash)

	actualHash := argon2.IDKey(
		[]byte(password),
		salt,
		uint32(cfg.Time),
		uint32(cfg.Memory),
		uint8(cfg.Threads),
		uint32(cfg.KeyLen),
	)

	return subtle.ConstantTimeCompare(
		actualHash,
		expectedHash,
	) == 1, nil
}
