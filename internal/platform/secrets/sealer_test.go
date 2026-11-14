package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
)

func testKey(version byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{version}, keyLen))
}

func TestCipherRoundTripAADBound(t *testing.T) {
	cipher, err := NewCipher(testKey(1), 1)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	plaintext := []byte("integration credentials: token-value")
	aad := []byte("actfb.integration.credentials.v1")

	blob, err := cipher.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if bytes.Contains(blob, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}

	opened, err := cipher.Open(blob, aad)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("Open() = %q, want %q", opened, plaintext)
	}

	if _, err := cipher.Open(blob, []byte("actfb.team.invite.v1")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("cross-context Open() error = %v, want ErrDecrypt", err)
	}
}

func TestCipherUsesFreshNonces(t *testing.T) {
	cipher, err := NewCipher(testKey(2), 2)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	first, err := cipher.Seal([]byte("same"), nil)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	second, err := cipher.Seal([]byte("same"), nil)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two seals produced identical ciphertext")
	}
}

func TestCipherRejectsTamperAndVersionMismatch(t *testing.T) {
	cipher, err := NewCipher(testKey(3), 3)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	blob, err := cipher.Seal([]byte("secret"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	t.Run("ciphertext tamper", func(t *testing.T) {
		tampered := append([]byte(nil), blob...)
		tampered[len(tampered)-1] ^= 0xff
		if _, err := cipher.Open(tampered, []byte("aad")); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("Open(tampered) error = %v, want ErrDecrypt", err)
		}
	})

	t.Run("header tamper", func(t *testing.T) {
		tampered := append([]byte(nil), blob...)
		tampered[0] ^= 0xff
		if _, err := cipher.Open(tampered, []byte("aad")); !errors.Is(err, ErrInvalidEnvelope) {
			t.Fatalf("Open(tampered header) error = %v, want ErrInvalidEnvelope", err)
		}
	})

	t.Run("unknown key version", func(t *testing.T) {
		other, err := NewCipher(testKey(4), 4)
		if err != nil {
			t.Fatalf("NewCipher(other) error = %v", err)
		}
		if _, err := other.Open(blob, []byte("aad")); !errors.Is(err, ErrUnknownKeyVersion) {
			t.Fatalf("Open(wrong version) error = %v, want ErrUnknownKeyVersion", err)
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		other, err := NewCipher(testKey(5), 3)
		if err != nil {
			t.Fatalf("NewCipher(other key) error = %v", err)
		}
		if _, err := other.Open(blob, []byte("aad")); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("Open(wrong key) error = %v, want ErrDecrypt", err)
		}
	})
}

func TestCipherRejectsInvalidKeys(t *testing.T) {
	if _, err := NewCipher("", 1); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("empty key error = %v, want ErrInvalidKey", err)
	}
	if _, err := NewCipher("not-base64", 1); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("non-base64 key error = %v, want ErrInvalidKey", err)
	}
	if _, err := NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 31)), 1); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("short key error = %v, want ErrInvalidKey", err)
	}
	if _, err := NewCipher(testKey(1), 0); !errors.Is(err, ErrInvalidKeyVersion) {
		t.Fatalf("zero version error = %v, want ErrInvalidKeyVersion", err)
	}
	if err := ValidateKey(testKey(1)); err != nil {
		t.Fatalf("ValidateKey(valid) error = %v", err)
	}
}

func TestCipherEmptyPlaintextAndGenericErrors(t *testing.T) {
	const rawKey = "0123456789abcdef0123456789abcdef"
	cipher, err := NewCipher(base64.StdEncoding.EncodeToString([]byte(rawKey)), 1)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	blob, err := cipher.Seal(nil, nil)
	if err != nil {
		t.Fatalf("Seal(nil) error = %v", err)
	}
	opened, err := cipher.Open(blob, nil)
	if err != nil || len(opened) != 0 {
		t.Fatalf("Open(empty) = (%q, %v), want empty", opened, err)
	}

	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 0x01
	_, err = cipher.Open(tampered, nil)
	if err == nil {
		t.Fatal("Open(tampered) error = nil")
	}
	if strings.Contains(err.Error(), rawKey) || strings.Contains(err.Error(), "decryption failed") == false {
		t.Fatalf("error %q is not the generic decryption error", err)
	}
}

func TestCipherConcurrentUse(t *testing.T) {
	cipher, err := NewCipher(testKey(6), 6)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			aad := []byte("concurrent")
			blob, err := cipher.Seal([]byte("value"), aad)
			if err != nil {
				t.Errorf("Seal() error = %v", err)
				return
			}
			if _, err := cipher.Open(blob, aad); err != nil {
				t.Errorf("Open() error = %v", err)
			}
		}()
	}
	wg.Wait()
}
