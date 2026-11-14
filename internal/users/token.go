package users

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	accessTokenType         = "access"
	maxAccessToken          = 8192
	refreshTokenLen         = 32
	passwordResetTokenLen   = 32
	passwordResetHMACDomain = "actfb:password-reset:v1:"
)

type accessClaims struct {
	Subject     string `json:"sub"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
	JTI         string `json:"jti"`
	Type        string `json:"typ"`
	AuthVersion int64  `json:"ver"`
}

type jwtHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

// signAccessToken creates a compact HS256 JWT using only standard library
// primitives. The caller owns secret/ttl validation.
func signAccessToken(userID uuid.UUID, authVersion int64, secret string, ttl time.Duration, now time.Time) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("%w: access token secret is empty", ErrAuthConfiguration)
	}
	if ttl <= 0 {
		return "", fmt.Errorf("%w: access token expiry must be positive", ErrAuthConfiguration)
	}

	headerBytes, err := json.Marshal(jwtHeader{Algorithm: "HS256", Type: "JWT"})
	if err != nil {
		return "", fmt.Errorf("marshal jwt header: %w", err)
	}

	claims := accessClaims{
		Subject:     userID.String(),
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(ttl).Unix(),
		JTI:         uuid.NewString(),
		Type:        accessTokenType,
		AuthVersion: authVersion,
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal jwt claims: %w", err)
	}

	encoding := base64.RawURLEncoding
	unsigned := encoding.EncodeToString(headerBytes) + "." + encoding.EncodeToString(claimsBytes)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	signature := encoding.EncodeToString(mac.Sum(nil))

	return unsigned + "." + signature, nil
}

// parseAccessToken performs strict HS256 validation: exactly three parts,
// supported header, valid signature, valid UUID subject, access token type and
// an expiry in the future. It intentionally does not accept alg=none or
// asymmetric algorithms.
func parseAccessToken(token, secret string, now time.Time) (*accessClaims, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("%w: access token secret is empty", ErrAuthConfiguration)
	}
	if token == "" || len(token) > maxAccessToken {
		return nil, ErrUnauthorized
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrUnauthorized
	}

	encoding := base64.RawURLEncoding

	headerBytes, err := encoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrUnauthorized
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, ErrUnauthorized
	}
	if header.Algorithm != "HS256" || header.Type != "JWT" {
		return nil, ErrUnauthorized
	}

	expectedMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = expectedMAC.Write([]byte(parts[0] + "." + parts[1]))
	expectedSignature := expectedMAC.Sum(nil)

	actualSignature, err := encoding.DecodeString(parts[2])
	if err != nil || len(actualSignature) != sha256.Size {
		return nil, ErrUnauthorized
	}
	if subtle.ConstantTimeCompare(actualSignature, expectedSignature) != 1 {
		return nil, ErrUnauthorized
	}

	claimsBytes, err := encoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrUnauthorized
	}
	var claims accessClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, ErrUnauthorized
	}

	if claims.Type != accessTokenType {
		return nil, ErrUnauthorized
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return nil, ErrUnauthorized
	}
	if claims.ExpiresAt <= now.Unix() {
		return nil, ErrUnauthorized
	}
	if claims.IssuedAt > now.Add(time.Minute).Unix() {
		return nil, ErrUnauthorized
	}
	if claims.JTI == "" {
		return nil, ErrUnauthorized
	}

	return &claims, nil
}

func generateRefreshToken() (string, error) {
	raw := make([]byte, refreshTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashRefreshToken(token, secret string) ([]byte, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("%w: refresh token secret is empty", ErrAuthConfiguration)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(token))
	return mac.Sum(nil), nil
}

func generateEmailVerificationToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate email verification token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashEmailVerificationToken(token, secret string) ([]byte, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("%w: email verification token secret is empty", ErrAuthConfiguration)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(token))
	return mac.Sum(nil), nil
}

func generatePasswordResetToken() (string, error) {
	raw := make([]byte, passwordResetTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate password reset token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashPasswordResetToken(token, secret string) ([]byte, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("%w: password reset token secret is empty", ErrAuthConfiguration)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(passwordResetHMACDomain))
	_, _ = mac.Write([]byte(token))
	return mac.Sum(nil), nil
}
