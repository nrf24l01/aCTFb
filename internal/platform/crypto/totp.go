package crypto

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"time"

	"github.com/pquerna/otp/totp"
)

func GenerateTOTPSecret() (string, error) {
	secret := make([]byte, 20)

	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}

	return base32.StdEncoding.
		WithPadding(base32.NoPadding).
		EncodeToString(secret), nil
}

func GenerateTOTPCode(secret string, opts_time time.Time) (string, error) {
	code, err := totp.GenerateCode(secret, time.Now(), opts_time)
	if err != nil {
		return "", fmt.Errorf("generate totp code: %w", err)
	}

	return code, nil
}

func ValidateTOTPCode(secret, code string, opts_time time.Time) bool {
	return totp.Validate(code, secret, opts_time)
}
