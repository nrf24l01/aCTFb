// Package secrets provides the shared AES-256-GCM envelope used for all
// application secrets (credentials, invite keys, plugin settings, DSNs).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

const (
	envelopeMagic = "ACTFBGCM"
	keyLen        = 32
	formatV1      = byte(1)
	headerLen     = len(envelopeMagic) + 1 + 4
)

var (
	ErrInvalidKey        = errors.New("secrets: invalid key")
	ErrInvalidKeyVersion = errors.New("secrets: invalid key version")
	ErrInvalidEnvelope   = errors.New("secrets: invalid ciphertext envelope")
	ErrUnknownKeyVersion = errors.New("secrets: unknown key version")
	ErrDecrypt           = errors.New("secrets: decryption failed")
)

// Sealer encrypts and authenticates application secrets.
//
// The same AAD bytes must be supplied to Open. Callers must use a stable,
// domain-separated AAD such as "actfb.integration.credentials.v1" so ciphertext
// cannot be moved between different secret columns.
type Sealer interface {
	Seal(plaintext, aad []byte) ([]byte, error)
	Open(blob, aad []byte) ([]byte, error)
}

// Cipher is an AES-256-GCM Sealer with a versioned, AAD-bound envelope.
type Cipher struct {
	aead       cipher.AEAD
	keyVersion uint32
	prefix     []byte
}

var _ Sealer = (*Cipher)(nil)

// NewCipher decodes a base64 32-byte key and constructs a cipher for keyVersion.
// There is deliberately no default key.
func NewCipher(base64Key string, keyVersion uint32) (*Cipher, error) {
	if keyVersion == 0 {
		return nil, ErrInvalidKeyVersion
	}

	key, err := decodeKey(base64Key)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrInvalidKey
	}

	prefix := make([]byte, headerLen)
	copy(prefix, envelopeMagic)
	prefix[len(envelopeMagic)] = formatV1
	binary.BigEndian.PutUint32(prefix[len(envelopeMagic)+1:], keyVersion)

	return &Cipher{
		aead:       aead,
		keyVersion: keyVersion,
		prefix:     prefix,
	}, nil
}

// ValidateKey checks a base64-encoded 32-byte key without retaining it.
func ValidateKey(base64Key string) error {
	_, err := decodeKey(base64Key)
	return err
}

func decodeKey(base64Key string) ([]byte, error) {
	raw := strings.TrimSpace(base64Key)
	if raw == "" {
		return nil, ErrInvalidKey
	}

	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil || len(key) != keyLen {
		return nil, ErrInvalidKey
	}

	return key, nil
}

func (c *Cipher) Seal(plaintext, aad []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrInvalidKey
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrDecrypt
	}

	boundAAD := boundAAD(c.prefix, aad)
	ciphertext := c.aead.Seal(nil, nonce, plaintext, boundAAD)

	blob := make([]byte, 0, len(c.prefix)+len(nonce)+len(ciphertext))
	blob = append(blob, c.prefix...)
	blob = append(blob, nonce...)
	blob = append(blob, ciphertext...)

	return blob, nil
}

func (c *Cipher) Open(blob, aad []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrInvalidKey
	}
	if len(blob) < headerLen+c.aead.NonceSize()+c.aead.Overhead() {
		return nil, ErrInvalidEnvelope
	}
	if string(blob[:len(envelopeMagic)]) != envelopeMagic || blob[len(envelopeMagic)] != formatV1 {
		return nil, ErrInvalidEnvelope
	}

	version := binary.BigEndian.Uint32(blob[len(envelopeMagic)+1 : headerLen])
	if version != c.keyVersion {
		return nil, ErrUnknownKeyVersion
	}

	nonceStart := headerLen
	nonceEnd := nonceStart + c.aead.NonceSize()
	nonce := blob[nonceStart:nonceEnd]
	ciphertext := blob[nonceEnd:]

	boundAAD := boundAAD(blob[:headerLen], aad)
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, boundAAD)
	if err != nil {
		return nil, ErrDecrypt
	}

	return plaintext, nil
}

// KeyVersion reports the key version this cipher accepts.
func (c *Cipher) KeyVersion() uint32 {
	if c == nil {
		return 0
	}
	return c.keyVersion
}

func boundAAD(header, aad []byte) []byte {
	bound := make([]byte, 0, len(header)+len(aad))
	bound = append(bound, header...)
	bound = append(bound, aad...)
	return bound
}
