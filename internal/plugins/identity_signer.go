package plugins

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const (
	delegationPrefix = "actfb.d1"
	delegationDomain = "actfb.delegation.v1."
	// MaxDelegationTTL is the hard upper bound for a core-issued delegation.
	MaxDelegationTTL = 15 * time.Minute
)

var (
	ErrDelegationExpired  = fmt.Errorf("%w: expired", ErrDelegationInvalid)
	ErrDelegationRevoked  = fmt.Errorf("%w: revoked", ErrDelegationInvalid)
	ErrDelegationTampered = fmt.Errorf("%w: signature mismatch", ErrDelegationInvalid)
)

type delegationPayload struct {
	Version   int    `json:"v"`
	PluginID  string `json:"p"`
	UserID    string `json:"u"`
	BoardID   string `json:"b,omitempty"`
	ModuleID  string `json:"m,omitempty"`
	Nonce     string `json:"n"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// DelegationSigner implements DelegationIssuer with HMAC-SHA256 tokens and the
// plugin_delegations table. Only the token hash and nonce are persisted; the raw
// token exists in memory/headers and is never logged or returned to a browser.
type DelegationSigner struct {
	db     *postgres.DB
	key    []byte
	now    func() time.Time
	maxTTL time.Duration
}

// DelegationOption configures the signer.
type DelegationOption func(*DelegationSigner)

// WithDelegationClock overrides the wall clock in tests.
func WithDelegationClock(now func() time.Time) DelegationOption {
	return func(signer *DelegationSigner) {
		if now != nil {
			signer.now = now
		}
	}
}

// NewDelegationIssuer constructs the HMAC delegation issuer. The service key is
// the decoded ACTFB_PLUGIN_SERVICE_KEY (32 bytes).
func NewDelegationIssuer(db *postgres.DB, serviceKey []byte, opts ...DelegationOption) (*DelegationSigner, error) {
	if db == nil || db.Pool == nil {
		return nil, errors.New("plugins: delegation issuer requires a database")
	}
	if len(serviceKey) < 32 {
		return nil, errors.New("plugins: delegation signing key must be at least 32 bytes")
	}
	signer := &DelegationSigner{
		db:     db,
		key:    append([]byte(nil), serviceKey...),
		now:    func() time.Time { return time.Now().UTC() },
		maxTTL: MaxDelegationTTL,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(signer)
		}
	}
	return signer, nil
}

var _ DelegationIssuer = (*DelegationSigner)(nil)

// Issue mints and persists one short-lived delegation.
func (s *DelegationSigner) Issue(
	ctx context.Context,
	identity Identity,
	userID uuid.UUID,
	scope authorization.Scope,
	ttl time.Duration,
) (Delegation, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return Delegation{}, errors.New("plugins: delegation issuer is not configured")
	}
	if identity.PluginID == "" || userID == uuid.Nil {
		return Delegation{}, errors.New("plugins: delegation requires plugin id and user id")
	}
	if scope.ModuleID != nil && scope.BoardID == uuid.Nil {
		return Delegation{}, errors.New("plugins: module scope requires a board")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if ttl > s.maxTTL {
		ttl = s.maxTTL
	}
	now := s.now().UTC()
	nonce := uuid.New()
	payload := delegationPayload{
		Version:   1,
		PluginID:  string(identity.PluginID),
		UserID:    userID.String(),
		Nonce:     nonce.String(),
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
	}
	if scope.BoardID != uuid.Nil {
		payload.BoardID = scope.BoardID.String()
	}
	if scope.ModuleID != nil {
		payload.ModuleID = scope.ModuleID.String()
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Delegation{}, fmt.Errorf("plugins: encode delegation: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(encoded)
	token := delegationPrefix + "." + body + "." + s.sign(body)

	hash := delegationTokenHash(token)
	if _, err := s.db.Pool.Exec(ctx, `
		INSERT INTO plugin_delegations
			(plugin_id, user_id, board_id, module_id, nonce, token_hash, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, string(identity.PluginID), userID, nullableUUID(scope.BoardID), nullableUUIDPtr(scope.ModuleID),
		nonce, hash, now, time.Unix(payload.ExpiresAt, 0).UTC()); err != nil {
		// A missing plugin row is an expected denial, not a panic.
		if isForeignKeyViolation(err) {
			return Delegation{}, ErrNotFound
		}
		return Delegation{}, fmt.Errorf("plugins: persist delegation: %w", err)
	}

	return Delegation{
		Token:     token,
		PluginID:  identity.PluginID,
		UserID:    userID,
		Scope:     scope,
		Nonce:     nonce,
		IssuedAt:  now,
		ExpiresAt: time.Unix(payload.ExpiresAt, 0).UTC(),
	}, nil
}

// Verify validates the signature, expiry and live DB row. A revoked row (for
// example after disable or grant revocation) fails immediately.
func (s *DelegationSigner) Verify(ctx context.Context, token string) (Delegation, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return Delegation{}, errors.New("plugins: delegation issuer is not configured")
	}
	payload, err := s.parse(token)
	if err != nil {
		return Delegation{}, err
	}
	moduleScope := authorization.Scope{}
	if payload.BoardID != "" {
		boardID, err := uuid.Parse(payload.BoardID)
		if err != nil {
			return Delegation{}, ErrDelegationTampered
		}
		moduleScope.BoardID = boardID
	}
	if payload.ModuleID != "" {
		moduleID, err := uuid.Parse(payload.ModuleID)
		if err != nil {
			return Delegation{}, ErrDelegationTampered
		}
		moduleScope.ModuleID = &moduleID
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return Delegation{}, ErrDelegationTampered
	}
	nonce, err := uuid.Parse(payload.Nonce)
	if err != nil {
		return Delegation{}, ErrDelegationTampered
	}

	var storedPlugin, storedUser string
	var storedBoard, storedModule *uuid.UUID
	var storedNonce uuid.UUID
	var issuedAt, expiresAt time.Time
	err = s.db.Pool.QueryRow(ctx, `
		SELECT plugin_id, user_id::text, board_id, module_id, nonce, issued_at, expires_at
		FROM plugin_delegations
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > clock_timestamp()
	`, delegationTokenHash(token)).Scan(
		&storedPlugin, &storedUser, &storedBoard, &storedModule, &storedNonce, &issuedAt, &expiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// Distinguish revoked/expired for operators without leaking token data.
		var state string
		stateErr := s.db.Pool.QueryRow(ctx, `
			SELECT CASE
				WHEN revoked_at IS NOT NULL THEN 'revoked'
				WHEN expires_at <= clock_timestamp() THEN 'expired'
				ELSE 'missing'
			END
			FROM plugin_delegations WHERE token_hash = $1
		`, delegationTokenHash(token)).Scan(&state)
		if stateErr == nil {
			if state == "revoked" {
				return Delegation{}, ErrDelegationRevoked
			}
			if state == "expired" {
				return Delegation{}, ErrDelegationExpired
			}
		}
		return Delegation{}, ErrDelegationInvalid
	}
	if err != nil {
		return Delegation{}, fmt.Errorf("plugins: verify delegation: %w", err)
	}
	if storedPlugin != payload.PluginID || storedUser != payload.UserID || storedNonce != nonce ||
		!sameUUID(storedBoard, payload.BoardID) || !sameUUID(storedModule, payload.ModuleID) {
		return Delegation{}, ErrDelegationTampered
	}
	if !expiresAt.After(s.now()) {
		return Delegation{}, ErrDelegationExpired
	}
	// The nonce is one-time: consume the live row atomically. Concurrent calls
	// with the same token cannot both succeed, and a replay of a token from an
	// earlier request is rejected even within its short TTL.
	command, err := s.db.Pool.Exec(ctx, `
		UPDATE plugin_delegations
		SET revoked_at = clock_timestamp()
		WHERE token_hash = $1 AND revoked_at IS NULL
	`, delegationTokenHash(token))
	if err != nil {
		return Delegation{}, fmt.Errorf("plugins: consume delegation nonce: %w", err)
	}
	if command.RowsAffected() != 1 {
		return Delegation{}, ErrDelegationRevoked
	}

	return Delegation{
		Token:     token,
		PluginID:  ID(storedPlugin),
		UserID:    userID,
		Scope:     moduleScope,
		Nonce:     nonce,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

// Revoke revokes one raw token if it exists.
func (s *DelegationSigner) Revoke(ctx context.Context, token string) error {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return errors.New("plugins: delegation issuer is not configured")
	}
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE plugin_delegations
		SET revoked_at = clock_timestamp()
		WHERE token_hash = $1 AND revoked_at IS NULL
	`, delegationTokenHash(token))
	return err
}

// RevokePlugin revokes every active delegation for a plugin. Manager calls this
// on disable/remove so a disabled plugin cannot continue SDK calls.
func (s *DelegationSigner) RevokePlugin(ctx context.Context, pluginID ID) error {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return errors.New("plugins: delegation issuer is not configured")
	}
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE plugin_delegations
		SET revoked_at = clock_timestamp()
		WHERE plugin_id = $1 AND revoked_at IS NULL
	`, string(pluginID))
	return err
}

func (s *DelegationSigner) parse(token string) (delegationPayload, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 4 || parts[0] != "actfb" || parts[1] != "d1" || parts[2] == "" || parts[3] == "" {
		return delegationPayload{}, ErrDelegationInvalid
	}
	expected := s.sign(parts[2])
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[3])) != 1 {
		return delegationPayload{}, ErrDelegationTampered
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return delegationPayload{}, ErrDelegationInvalid
	}
	var payload delegationPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return delegationPayload{}, ErrDelegationInvalid
	}
	if payload.Version != 1 || payload.PluginID == "" || payload.UserID == "" ||
		payload.Nonce == "" || payload.IssuedAt <= 0 || payload.ExpiresAt <= 0 {
		return delegationPayload{}, ErrDelegationInvalid
	}
	if payload.ExpiresAt <= s.now().Unix() {
		return delegationPayload{}, ErrDelegationExpired
	}
	if payload.ExpiresAt-payload.IssuedAt > int64(s.maxTTL/time.Second)+5 {
		return delegationPayload{}, ErrDelegationInvalid
	}
	return payload, nil
}

func (s *DelegationSigner) sign(body string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(delegationDomain))
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func delegationTokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return sum[:]
}

func nullableUUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}

func nullableUUIDPtr(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return *value
}

func sameUUID(stored *uuid.UUID, payload string) bool {
	if payload == "" {
		return stored == nil
	}
	if stored == nil {
		return false
	}
	return stored.String() == payload
}
