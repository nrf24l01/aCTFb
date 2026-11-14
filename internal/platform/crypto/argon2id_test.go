package crypto

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/nrf24l01/actfb/internal/config"
)

func testArgon2Config() config.Argon2idConfig {
	return config.Argon2idConfig{
		Time:    1,
		Memory:  8 * 1024,
		Threads: 1,
		KeyLen:  16,
	}
}

func mustHash(t *testing.T, password string, cfg config.Argon2idConfig) string {
	t.Helper()

	hash, err := Hash(password, cfg)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	return hash
}

func hashWithoutPanic(t *testing.T, password string, cfg config.Argon2idConfig) (hash string, err error) {
	t.Helper()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Hash() panicked instead of returning an error: %v", recovered)
		}
	}()

	return Hash(password, cfg)
}

func encodedWithParams(t *testing.T, encodedHash, params string) string {
	t.Helper()

	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		t.Fatalf("unexpected encoded hash shape: %q", encodedHash)
	}

	parts[3] = params

	return strings.Join(parts, "$")
}

func encodedWithPart(t *testing.T, encodedHash string, index int, value string) string {
	t.Helper()

	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		t.Fatalf("unexpected encoded hash shape: %q", encodedHash)
	}

	parts[index] = value

	return strings.Join(parts, "$")
}

// nonCanonicalBase64 changes only padding bits of the final character, so the
// decoded bytes stay the same while the textual encoding becomes non-canonical.
func nonCanonicalBase64(t *testing.T, encoded string) string {
	t.Helper()

	if encoded == "" {
		t.Fatal("cannot make an empty base64 string non-canonical")
	}

	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

	last := encoded[len(encoded)-1]
	index := strings.IndexByte(alphabet, last)
	if index < 0 {
		t.Fatalf("unexpected base64 character %q", last)
	}

	return encoded[:len(encoded)-1] + string(alphabet[index^1])
}

func TestHashAndVerifyRoundtrip(t *testing.T) {
	cfg := testArgon2Config()
	password := "correct horse battery staple"

	encodedHash := mustHash(t, password, cfg)
	if !strings.HasPrefix(encodedHash, "$argon2id$v=19$") {
		t.Fatalf("Hash() = %q, want argon2id PHC prefix", encodedHash)
	}

	ok, err := VerifyArgon2id(password, encodedHash)
	if err != nil {
		t.Fatalf("VerifyArgon2id() error = %v", err)
	}
	if !ok {
		t.Fatal("VerifyArgon2id() = false for the correct password")
	}
}

func TestVerifyAcceptsOWASPParameters(t *testing.T) {
	cfg := config.Argon2idConfig{
		Time:    2,
		Memory:  19456,
		Threads: 1,
		KeyLen:  32,
	}
	password := "valid PHC hash"

	ok, err := VerifyArgon2id(password, mustHash(t, password, cfg))
	if err != nil {
		t.Fatalf("VerifyArgon2id() error = %v", err)
	}
	if !ok {
		t.Fatal("VerifyArgon2id() = false for a valid OWASP-parameter hash")
	}
}

func TestVerifyWrongPassword(t *testing.T) {
	encodedHash := mustHash(t, "right password", testArgon2Config())

	ok, err := VerifyArgon2id("wrong password", encodedHash)
	if err != nil {
		t.Fatalf("VerifyArgon2id() error = %v", err)
	}
	if ok {
		t.Fatal("VerifyArgon2id() = true for a wrong password")
	}
}

func TestHashUsesRandomSalt(t *testing.T) {
	cfg := testArgon2Config()

	first := mustHash(t, "same password", cfg)
	second := mustHash(t, "same password", cfg)

	if first == second {
		t.Fatal("Hash() produced identical hashes; salt is not random")
	}

	firstParts := strings.Split(first, "$")
	secondParts := strings.Split(second, "$")
	if len(firstParts) != 6 || len(secondParts) != 6 {
		t.Fatalf("unexpected encoded hash shape: %q, %q", first, second)
	}
	if firstParts[4] == secondParts[4] {
		t.Fatal("Hash() produced identical salts")
	}

	for _, encodedHash := range []string{first, second} {
		ok, err := VerifyArgon2id("same password", encodedHash)
		if err != nil {
			t.Fatalf("VerifyArgon2id() error = %v", err)
		}
		if !ok {
			t.Fatalf("VerifyArgon2id() = false for hash %q", encodedHash)
		}
	}
}

func TestHashRejectsZeroParams(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.Argon2idConfig)
	}{
		{name: "zero time", mutate: func(c *config.Argon2idConfig) { c.Time = 0 }},
		{name: "zero memory", mutate: func(c *config.Argon2idConfig) { c.Memory = 0 }},
		{name: "zero threads", mutate: func(c *config.Argon2idConfig) { c.Threads = 0 }},
		{name: "zero key length", mutate: func(c *config.Argon2idConfig) { c.KeyLen = 0 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testArgon2Config()
			test.mutate(&cfg)

			_, err := hashWithoutPanic(t, "password", cfg)
			if !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("Hash() error = %v, want ErrInvalidParams", err)
			}
		})
	}
}

func TestHashRejectsNegativeAndOverflowParams(t *testing.T) {
	maxInt := int(^uint(0) >> 1)

	tests := []struct {
		name   string
		mutate func(*config.Argon2idConfig)
	}{
		{name: "negative time", mutate: func(c *config.Argon2idConfig) { c.Time = -1 }},
		{name: "negative memory", mutate: func(c *config.Argon2idConfig) { c.Memory = -1 }},
		{name: "negative threads", mutate: func(c *config.Argon2idConfig) { c.Threads = -1 }},
		{name: "negative key length", mutate: func(c *config.Argon2idConfig) { c.KeyLen = -1 }},
		{name: "overflow time", mutate: func(c *config.Argon2idConfig) { c.Time = maxInt }},
		{name: "overflow memory", mutate: func(c *config.Argon2idConfig) { c.Memory = maxInt }},
		{name: "overflow threads", mutate: func(c *config.Argon2idConfig) { c.Threads = maxInt }},
		{name: "overflow key length", mutate: func(c *config.Argon2idConfig) { c.KeyLen = maxInt }},
		{name: "too small memory", mutate: func(c *config.Argon2idConfig) { c.Memory = 1024 }},
		{name: "too large memory", mutate: func(c *config.Argon2idConfig) { c.Memory = 256*1024 + 1 }},
		{name: "too large time", mutate: func(c *config.Argon2idConfig) { c.Time = 11 }},
		{name: "too large threads", mutate: func(c *config.Argon2idConfig) { c.Threads = 17 }},
		{name: "too small key length", mutate: func(c *config.Argon2idConfig) { c.KeyLen = 8 }},
		{name: "too large key length", mutate: func(c *config.Argon2idConfig) { c.KeyLen = 65 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testArgon2Config()
			test.mutate(&cfg)

			_, err := hashWithoutPanic(t, "password", cfg)
			if !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("Hash() error = %v, want ErrInvalidParams", err)
			}
		})
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	valid := mustHash(t, "password", testArgon2Config())

	salt7 := base64.RawStdEncoding.EncodeToString(make([]byte, 7))
	salt65 := base64.RawStdEncoding.EncodeToString(make([]byte, 65))
	hash15 := base64.RawStdEncoding.EncodeToString(make([]byte, 15))
	hash65 := base64.RawStdEncoding.EncodeToString(make([]byte, 65))

	tests := []struct {
		name        string
		encodedHash string
	}{
		{name: "empty", encodedHash: ""},
		{name: "not a PHC string", encodedHash: "password"},
		{name: "wrong algorithm", encodedHash: encodedWithPart(t, valid, 1, "argon2i")},
		{name: "wrong version", encodedHash: encodedWithPart(t, valid, 2, "v=16")},
		{name: "missing field", encodedHash: strings.Join(strings.Split(valid, "$")[:5], "$")},
		{name: "extra field", encodedHash: valid + "$extra"},
		{name: "empty salt", encodedHash: encodedWithPart(t, valid, 4, "")},
		{name: "invalid salt base64", encodedHash: encodedWithPart(t, valid, 4, "!!!!")},
		{name: "non-canonical salt base64", encodedHash: encodedWithPart(t, valid, 4, nonCanonicalBase64(t, strings.Split(valid, "$")[4]))},
		{name: "salt too short", encodedHash: encodedWithPart(t, valid, 4, salt7)},
		{name: "salt too long", encodedHash: encodedWithPart(t, valid, 4, salt65)},
		{name: "empty hash", encodedHash: encodedWithPart(t, valid, 5, "")},
		{name: "invalid hash base64", encodedHash: encodedWithPart(t, valid, 5, "!!!!")},
		{name: "non-canonical hash base64", encodedHash: encodedWithPart(t, valid, 5, nonCanonicalBase64(t, strings.Split(valid, "$")[5]))},
		{name: "hash too short", encodedHash: encodedWithPart(t, valid, 5, hash15)},
		{name: "hash too long", encodedHash: encodedWithPart(t, valid, 5, hash65)},
		{name: "missing parameter", encodedHash: encodedWithParams(t, valid, "m=8192,t=1")},
		{name: "reordered parameters", encodedHash: encodedWithParams(t, valid, "t=1,m=8192,p=1")},
		{name: "leading whitespace", encodedHash: encodedWithParams(t, valid, " m=8192,t=1,p=1")},
		{name: "zero time", encodedHash: encodedWithParams(t, valid, "m=8192,t=0,p=1")},
		{name: "zero memory", encodedHash: encodedWithParams(t, valid, "m=0,t=1,p=1")},
		{name: "zero threads", encodedHash: encodedWithParams(t, valid, "m=8192,t=1,p=0")},
		{name: "too much time", encodedHash: encodedWithParams(t, valid, "m=8192,t=11,p=1")},
		{name: "too much memory", encodedHash: encodedWithParams(t, valid, "m=262145,t=1,p=1")},
		{name: "too many threads", encodedHash: encodedWithParams(t, valid, "m=8192,t=1,p=17")},
		{name: "parameter overflow", encodedHash: encodedWithParams(t, valid, "m=4294967296,t=1,p=1")},
		{name: "too long encoded hash", encodedHash: strings.Repeat("a", maxEncodedHashLen+1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ok, err := VerifyArgon2id("password", test.encodedHash)
			if ok {
				t.Fatal("VerifyArgon2id() = true for a malformed hash")
			}
			if !errors.Is(err, ErrInvalidHash) {
				t.Fatalf("VerifyArgon2id() error = %v, want ErrInvalidHash", err)
			}
		})
	}
}

func TestVerifyRejectsExcessiveParameters(t *testing.T) {
	valid := mustHash(t, "password", testArgon2Config())

	tests := []struct {
		name   string
		params string
	}{
		{name: "maximum uint32 memory", params: "m=4294967295,t=1,p=1"},
		{name: "maximum uint32 time", params: "m=8192,t=4294967295,p=1"},
		{name: "maximum uint32 threads", params: "m=8192,t=1,p=4294967295"},
		{name: "threads byte overflow", params: "m=8192,t=1,p=256"},
		{name: "negative time", params: "m=8192,t=-1,p=1"},
		{name: "negative memory", params: "m=-1,t=1,p=1"},
		{name: "negative threads", params: "m=8192,t=1,p=-1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ok, err := VerifyArgon2id("password", encodedWithParams(t, valid, test.params))
			if ok {
				t.Fatal("VerifyArgon2id() = true for excessive parameters")
			}
			if !errors.Is(err, ErrInvalidHash) {
				t.Fatalf("VerifyArgon2id() error = %v, want ErrInvalidHash", err)
			}
		})
	}
}
