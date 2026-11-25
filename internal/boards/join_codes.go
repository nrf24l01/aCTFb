package boards

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// Board enrollment codes are the only self-enrollment credential. A board code
// is shown once to an authorized organizer and stored as a hash, so neither the
// join path nor the catalog needs the credential sealer or the runtime feature
// flag.
const (
	// joinCodeAlphabet is Crockford base32 without ambiguous letters. 26
	// characters carry 130 bits of entropy.
	joinCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	joinCodeLength   = 26
	joinCodeMinLen   = 8
	joinCodeMaxLen   = 64
	// joinCodeDomain separates the enrollment-code hash from every other hash
	// in the system so a stored digest cannot be replayed elsewhere.
	joinCodeDomain = "actfb.board.join-code.v1:"
)

// NormalizeJoinCode applies the documented, predictable normalization: trim
// outer whitespace and uppercase. It never rewrites interior characters, so a
// custom code keeps every letter the organizer chose. Generated codes already
// exclude the ambiguous Crockford letters, so no folding is needed.
func NormalizeJoinCode(raw string) string {
	trimmed := strings.TrimFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || r == '\uFEFF'
	})
	return strings.ToUpper(trimmed)
}

// ValidJoinCodeShape reports whether a normalized code may be used as a custom
// board code: 8..64 characters of A-Z, 0-9 and hyphen, no leading/trailing
// hyphen. It is a shape check only; the backend never claims strength.
func ValidJoinCodeShape(normalized string) bool {
	if len(normalized) < joinCodeMinLen || len(normalized) > joinCodeMaxLen {
		return false
	}
	if normalized[0] == '-' || normalized[len(normalized)-1] == '-' {
		return false
	}
	for index := 0; index < len(normalized); index++ {
		ch := normalized[index]
		switch {
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '-':
		default:
			return false
		}
	}
	return true
}

// newJoinCode returns a cryptographically random board code. crypto/rand is the
// only source; no clock, counter or math/rand fallback is used.
func newJoinCode() (string, error) {
	buffer := make([]byte, joinCodeLength)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	out := make([]byte, joinCodeLength)
	for index, value := range buffer {
		out[index] = joinCodeAlphabet[int(value)%len(joinCodeAlphabet)]
	}
	return string(out), nil
}

// hashJoinCode is the stored lookup digest for a normalized code.
func hashJoinCode(normalized string) string {
	sum := sha256.Sum256([]byte(joinCodeDomain + normalized))
	return hex.EncodeToString(sum[:])
}
