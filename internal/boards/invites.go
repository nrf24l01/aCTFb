package boards

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func newInviteCode() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate invite code: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashInviteCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func boardInviteAAD(boardID uuid.UUID) []byte {
	return []byte("actfb.board.invite.v1:" + boardID.String())
}

func teamInviteAAD(boardID, teamID uuid.UUID) []byte {
	return []byte("actfb.team.invite.v1:" + boardID.String() + ":" + teamID.String())
}

func (s *Service) sealInvite(code string, aad []byte) ([]byte, error) {
	if s.sealer == nil {
		return nil, ErrDependencyUnavailable
	}
	return s.sealer.Seal([]byte(code), aad)
}

func (s *Service) openInvite(ciphertext []byte, aad []byte) (string, error) {
	if s.sealer == nil {
		return "", ErrDependencyUnavailable
	}
	plain, err := s.sealer.Open(ciphertext, aad)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func inviteExpiry(now time.Time) *time.Time {
	value := now.Add(30 * 24 * time.Hour)
	return &value
}
