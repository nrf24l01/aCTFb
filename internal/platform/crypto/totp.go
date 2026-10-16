package crypto

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

type TotpGenerator struct {
	opts totp.ValidateOpts
}

func NewTotpGenerator(period time.Duration) *TotpGenerator {
	return &TotpGenerator{
		opts: totp.ValidateOpts{
			Period:    uint(period / time.Second),
			Skew:      0,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		},
	}
}

func GenerateTOTPSecret() (string, error) {
	secret := make([]byte, 20)

	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}

	return base32.StdEncoding.
		WithPadding(base32.NoPadding).
		EncodeToString(secret), nil
}

func (tg *TotpGenerator) GenerateTOTPCode(secret string) (string, error) {
	code, err := totp.GenerateCodeCustom(secret, time.Now(), tg.opts)
	if err != nil {
		return "", fmt.Errorf("generate totp code: %w", err)
	}

	return code, nil
}

func (tg *TotpGenerator) ValidateTOTPCode(secret, code string) (bool, error) {
	return totp.ValidateCustom(code, secret, time.Now(), tg.opts)
}
