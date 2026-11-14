package users

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "test-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "test-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
		Security: config.SecurityConfig{
			EmailVerifyEnabled: true,
			EmailVerifyPeriod:  5 * time.Minute,
		},
		Email: config.EmailConfig{Enabled: true},
		WebApp: config.WebAppConfig{
			AllowedOrigins: []string{"http://localhost:8080"},
		},
	}
}

type fakeSession struct {
	userID    uuid.UUID
	familyID  uuid.UUID
	expiresAt time.Time
	revoked   bool
	reason    string
}

type fakeEmailToken struct {
	userID    uuid.UUID
	expiresAt time.Time
	used      bool
}

type fakePasswordReset struct {
	userID    uuid.UUID
	expiresAt time.Time
	used      bool
}

type fakeStore struct {
	mu            sync.Mutex
	users         map[uuid.UUID]*User
	byName        map[string]uuid.UUID
	sessions      map[string]*fakeSession
	emailTokens   map[string]*fakeEmailToken
	passwordReset map[string]*fakePasswordReset
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:         make(map[uuid.UUID]*User),
		byName:        make(map[string]uuid.UUID),
		sessions:      make(map[string]*fakeSession),
		emailTokens:   make(map[string]*fakeEmailToken),
		passwordReset: make(map[string]*fakePasswordReset),
	}
}

func (s *fakeStore) createUser(_ context.Context, params createUserParams) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user := s.createUserLocked(params)
	if user == nil {
		return nil, ErrUsernameTaken
	}
	return cloneUser(user), nil
}

func (s *fakeStore) createUserWithSession(_ context.Context, params createUserParams, tokenHash []byte, expiresAt time.Time) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user := s.createUserLocked(params)
	if user == nil {
		return nil, ErrUsernameTaken
	}
	s.sessions[string(tokenHash)] = &fakeSession{userID: user.ID, familyID: uuid.New(), expiresAt: expiresAt}
	return cloneUser(user), nil
}

func (s *fakeStore) createUserLocked(params createUserParams) *User {
	if _, exists := s.byName[params.Username]; exists {
		return nil
	}
	user := &User{
		ID:             uuid.New(),
		Username:       params.Username,
		Email:          params.Email,
		Password:       params.PasswordHash,
		EmailVerified:  params.EmailVerified,
		Email2FASecret: params.EmailSecret,
		EmailSendAt:    time.Unix(0, 0),
		Roles:          []string{"user"},
		CreatedAt:      time.Now().UTC(),
		AuthVersion:    0,
	}
	s.users[user.ID] = user
	s.byName[user.Username] = user.ID
	return user
}

func (s *fakeStore) getUserByUsername(_ context.Context, username string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.byName[username]
	if !ok {
		return nil, ErrUserNotFound
	}
	return cloneUser(s.users[id]), nil
}

func (s *fakeStore) getUserByEmail(_ context.Context, email string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, user := range s.users {
		if strings.EqualFold(user.Email, email) {
			return cloneUser(user), nil
		}
	}
	return nil, ErrUserNotFound
}

func (s *fakeStore) getUserByID(_ context.Context, id uuid.UUID) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return cloneUser(user), nil
}

func (s *fakeStore) createSession(_ context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[string(tokenHash)] = &fakeSession{userID: userID, familyID: uuid.New(), expiresAt: expiresAt}
	return nil
}

func (s *fakeStore) createSessionIfAuthVersion(_ context.Context, userID uuid.UUID, expectedVersion int64, expectedPasswordHash string, tokenHash []byte, expiresAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return false, ErrUserNotFound
	}
	if user.AuthVersion != expectedVersion || user.Password != expectedPasswordHash {
		return false, nil
	}
	s.sessions[string(tokenHash)] = &fakeSession{userID: userID, familyID: uuid.New(), expiresAt: expiresAt}
	return true, nil
}

func (s *fakeStore) rotateRefreshSession(
	_ context.Context,
	oldHash, newHash []byte,
	now, newExpiresAt time.Time,
) (*User, rotationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[string(oldHash)]
	if !ok {
		return nil, rotationInvalid, nil
	}
	user := cloneUser(s.users[session.userID])
	if session.revoked {
		if !now.Before(session.expiresAt) {
			return user, rotationExpired, nil
		}
		switch session.reason {
		case sessionRevokedReasonRotated:
			s.revokeFamilyLocked(session.familyID, sessionRevokedReasonReuse)
			return user, rotationReused, nil
		case sessionRevokedReasonReuse:
			return user, rotationReused, nil
		default:
			return user, rotationInvalid, nil
		}
	}
	if !now.Before(session.expiresAt) {
		session.revoked = true
		return user, rotationExpired, nil
	}
	if user.Disabled {
		session.revoked = true
		return user, rotationDisabled, nil
	}

	session.revoked = true
	session.reason = sessionRevokedReasonRotated
	s.sessions[string(newHash)] = &fakeSession{
		userID:    user.ID,
		familyID:  session.familyID,
		expiresAt: newExpiresAt,
	}
	return user, rotationOK, nil
}

func (s *fakeStore) revokeSession(_ context.Context, tokenHash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.sessions[string(tokenHash)]; ok {
		s.revokeFamilyLocked(session.familyID, sessionRevokedReasonLogout)
	}
	return nil
}

func (s *fakeStore) revokeAllSessions(_ context.Context, userID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range s.sessions {
		if session.userID == userID && !session.revoked {
			session.revoked = true
			session.reason = sessionRevokedReasonPasswordChange
		}
	}
	return nil
}

func (s *fakeStore) revokeFamilyLocked(familyID uuid.UUID, reason string) {
	for _, session := range s.sessions {
		if session.familyID == familyID && !session.revoked {
			session.revoked = true
			session.reason = reason
		}
	}
}

func (s *fakeStore) markEmailSent(_ context.Context, userID uuid.UUID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	user.EmailSendAt = at
	return nil
}

func (s *fakeStore) getEmailState(_ context.Context, userID uuid.UUID) (*emailState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return nil, ErrUserNotFound
	}
	return &emailState{
		Email:    user.Email,
		Username: user.Username,
		Secret:   user.Email2FASecret,
		Verified: user.EmailVerified,
		SentAt:   user.EmailSendAt,
	}, nil
}

func (s *fakeStore) markEmailVerified(_ context.Context, userID uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return false, ErrUserNotFound
	}
	if user.EmailVerified {
		return false, nil
	}
	user.EmailVerified = true
	return true, nil
}

func (s *fakeStore) createEmailVerification(_ context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, token := range s.emailTokens {
		if token.userID == userID && !token.used {
			token.used = true
		}
	}
	s.emailTokens[string(tokenHash)] = &fakeEmailToken{userID: userID, expiresAt: expiresAt}
	return nil
}

func (s *fakeStore) consumeEmailVerification(_ context.Context, tokenHash []byte, now time.Time) (uuid.UUID, emailVerificationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	token, ok := s.emailTokens[string(tokenHash)]
	if !ok {
		return uuid.Nil, emailVerificationInvalid, nil
	}
	if token.used {
		return token.userID, emailVerificationUsed, nil
	}
	if !now.Before(token.expiresAt) {
		return token.userID, emailVerificationExpired, nil
	}
	user := s.users[token.userID]
	if user == nil {
		return uuid.Nil, emailVerificationInvalid, nil
	}
	token.used = true
	if user.EmailVerified {
		return token.userID, emailVerificationAlreadyVerified, nil
	}
	user.EmailVerified = true
	return token.userID, emailVerificationOK, nil
}

func (s *fakeStore) revokeEmailVerification(_ context.Context, tokenHash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token, ok := s.emailTokens[string(tokenHash)]; ok {
		token.used = true
	}
	return nil
}

func (s *fakeStore) changePasswordIfCurrent(_ context.Context, userID uuid.UUID, expectedVersion int64, expectedPasswordHash, newPasswordHash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return false, ErrUserNotFound
	}
	if user.AuthVersion != expectedVersion || user.Password != expectedPasswordHash {
		return false, nil
	}
	user.Password = newPasswordHash
	user.AuthVersion++
	for _, session := range s.sessions {
		if session.userID == userID && !session.revoked {
			session.revoked = true
			session.reason = sessionRevokedReasonPasswordChange
		}
	}
	now := time.Now()
	for _, reset := range s.passwordReset {
		if reset.userID == userID && !reset.used && now.Before(reset.expiresAt) {
			reset.used = true
		}
	}
	return true, nil
}

func (s *fakeStore) createPasswordReset(_ context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, reset := range s.passwordReset {
		if reset.userID == userID && !reset.used && now.Before(reset.expiresAt) {
			reset.used = true
		}
	}
	s.passwordReset[string(tokenHash)] = &fakePasswordReset{userID: userID, expiresAt: expiresAt}
	return nil
}

func (s *fakeStore) consumePasswordReset(_ context.Context, tokenHash []byte, now time.Time, newPasswordHash string) (uuid.UUID, passwordResetStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reset, ok := s.passwordReset[string(tokenHash)]
	if !ok {
		return uuid.Nil, passwordResetInvalid, nil
	}
	if reset.used {
		return reset.userID, passwordResetUsed, nil
	}
	if !now.Before(reset.expiresAt) {
		return reset.userID, passwordResetExpired, nil
	}
	user := s.users[reset.userID]
	if user == nil {
		return uuid.Nil, passwordResetInvalid, nil
	}
	if user.Disabled {
		reset.used = true
		return user.ID, passwordResetInvalid, nil
	}
	reset.used = true
	user.Password = newPasswordHash
	user.AuthVersion++
	for _, session := range s.sessions {
		if session.userID == user.ID && !session.revoked {
			session.revoked = true
			session.reason = sessionRevokedReasonPasswordChange
		}
	}
	for _, other := range s.passwordReset {
		if other.userID == user.ID && !other.used && now.Before(other.expiresAt) {
			other.used = true
		}
	}
	return user.ID, passwordResetOK, nil
}

func (s *fakeStore) revokePasswordReset(_ context.Context, tokenHash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reset, ok := s.passwordReset[string(tokenHash)]; ok {
		reset.used = true
	}
	return nil
}

func (s *fakeStore) updatePassword(_ context.Context, userID uuid.UUID, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	user.Password = passwordHash
	return nil
}

func (s *fakeStore) updateUsername(_ context.Context, userID uuid.UUID, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	if id, exists := s.byName[username]; exists && id != userID {
		return ErrUsernameTaken
	}
	delete(s.byName, user.Username)
	user.Username = username
	s.byName[username] = userID
	return nil
}

func cloneUser(user *User) *User {
	if user == nil {
		return nil
	}
	copyUser := *user
	copyUser.Roles = append([]string(nil), user.Roles...)
	return &copyUser
}

type fakePublisher struct {
	token       string
	to          string
	username    string
	taskID      uuid.UUID
	resetToken  string
	resetTo     string
	resetUser   string
	resetTaskID uuid.UUID
	err         error
	resetErr    error
}

func (p *fakePublisher) PublishEmailVerificationTask(_ context.Context, to, username, code string, taskID uuid.UUID) error {
	p.to = to
	p.username = username
	p.token = code
	p.taskID = taskID
	return p.err
}

func (p *fakePublisher) PublishPasswordResetTask(_ context.Context, to, username, token string, taskID uuid.UUID) error {
	p.resetTo = to
	p.resetUser = username
	p.resetToken = token
	p.resetTaskID = taskID
	return p.resetErr
}

func TestAccessTokenRoundTripAndStrictValidation(t *testing.T) {
	now := time.Now()
	userID := uuid.New()
	token, err := signAccessToken(userID, 7, "secret", 15*time.Minute, now)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	claims, err := parseAccessToken(token, "secret", now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != userID.String() {
		t.Fatalf("subject = %q, want %q", claims.Subject, userID)
	}
	if claims.AuthVersion != 7 {
		t.Fatalf("ver = %d, want 7", claims.AuthVersion)
	}

	if _, err := parseAccessToken(token, "other-secret", now); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong secret error = %v, want ErrUnauthorized", err)
	}
	if _, err := parseAccessToken(token, "secret", now.Add(16*time.Minute)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired error = %v, want ErrUnauthorized", err)
	}

	tamperedParts := strings.Split(token, ".")
	if len(tamperedParts) != 3 {
		t.Fatal("signed token is not compact JWT")
	}
	signature := []byte(tamperedParts[2])
	if signature[0] == 'A' {
		signature[0] = 'B'
	} else {
		signature[0] = 'A'
	}
	tampered := tamperedParts[0] + "." + tamperedParts[1] + "." + string(signature)
	if _, err := parseAccessToken(tampered, "secret", now); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tampered error = %v, want ErrUnauthorized", err)
	}

	if _, err := parseAccessToken("eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.e30.", "secret", now); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("alg=none error = %v, want ErrUnauthorized", err)
	}
}

func TestRegisterLoginRefreshRotationAndReplay(t *testing.T) {
	store := newFakeStore()
	service := newUserService(store, testConfig(), nil, slog.Default())
	ctx := context.Background()

	registered, err := service.Register(ctx, RegisterInput{
		Username: "alice",
		Email:    "Alice@Example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if registered.AccessToken == "" || registered.RefreshToken == "" {
		t.Fatal("register returned empty tokens")
	}
	if registered.User.Email != "alice@example.test" {
		t.Fatalf("email = %q, want normalized", registered.User.Email)
	}

	loggedIn, err := service.Login(ctx, "alice", "correct horse battery")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if loggedIn.User.ID != registered.User.ID {
		t.Fatal("login returned a different user")
	}

	rotated, err := service.Refresh(ctx, registered.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.RefreshToken == registered.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}

	if _, err := service.Refresh(ctx, registered.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("replay error = %v, want ErrRefreshTokenReused", err)
	}
	if _, err := service.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("post-reuse refresh error = %v, want ErrRefreshTokenReused", err)
	}
}

func TestLoginGenericFailureAndDisabledUser(t *testing.T) {
	store := newFakeStore()
	service := newUserService(store, testConfig(), nil, slog.Default())
	ctx := context.Background()

	if _, err := service.Login(ctx, "missing", "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user error = %v, want ErrInvalidCredentials", err)
	}

	result, err := service.Register(ctx, RegisterInput{
		Username: "bob",
		Email:    "bob@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := service.Login(ctx, "bob", "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v, want ErrInvalidCredentials", err)
	}

	store.mu.Lock()
	store.users[result.User.ID].Disabled = true
	store.mu.Unlock()

	if _, err := service.Login(ctx, "bob", "correct horse battery"); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled login error = %v, want ErrUserDisabled", err)
	}
}

func TestRefreshExpiredAndLogout(t *testing.T) {
	store := newFakeStore()
	service := newUserService(store, testConfig(), nil, slog.Default())
	ctx := context.Background()

	result, err := service.Register(ctx, RegisterInput{
		Username: "carol",
		Email:    "carol@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	hash, err := hashRefreshToken(result.RefreshToken, service.refreshSecret())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store.mu.Lock()
	store.sessions[string(hash)].expiresAt = time.Now().Add(-time.Minute)
	store.mu.Unlock()

	if _, err := service.Refresh(ctx, result.RefreshToken); !errors.Is(err, ErrRefreshTokenExpired) {
		t.Fatalf("expired refresh error = %v, want ErrRefreshTokenExpired", err)
	}

	loggedIn, err := service.Login(ctx, "carol", "correct horse battery")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := service.Logout(ctx, loggedIn.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := service.Refresh(ctx, loggedIn.RefreshToken); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("refresh after logout error = %v, want ErrRefreshTokenInvalid", err)
	}
	if err := service.Logout(ctx, ""); err != nil {
		t.Fatalf("idempotent logout: %v", err)
	}
}

func TestLoggedOutTokenReplayDoesNotRevokeIndependentLogin(t *testing.T) {
	store := newFakeStore()
	service := newUserService(store, testConfig(), nil, slog.Default())
	ctx := context.Background()

	first, err := service.Register(ctx, RegisterInput{
		Username: "ivan",
		Email:    "ivan@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.Logout(ctx, first.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}

	second, err := service.Login(ctx, "ivan", "correct horse battery")
	if err != nil {
		t.Fatalf("independent login: %v", err)
	}
	newer, err := service.Refresh(ctx, second.RefreshToken)
	if err != nil {
		t.Fatalf("independent refresh: %v", err)
	}

	if _, err := service.Refresh(ctx, first.RefreshToken); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("logged-out replay error = %v, want ErrRefreshTokenInvalid", err)
	}
	if _, err := service.Refresh(ctx, newer.RefreshToken); err != nil {
		t.Fatalf("newer independent session was revoked by logged-out replay: %v", err)
	}
}

func TestRotatedReplayRevokesOnlyItsFamily(t *testing.T) {
	store := newFakeStore()
	service := newUserService(store, testConfig(), nil, slog.Default())
	ctx := context.Background()

	familyA, err := service.Register(ctx, RegisterInput{
		Username: "judy",
		Email:    "judy@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	rotatedA, err := service.Refresh(ctx, familyA.RefreshToken)
	if err != nil {
		t.Fatalf("rotate family A: %v", err)
	}

	familyB, err := service.Login(ctx, "judy", "correct horse battery")
	if err != nil {
		t.Fatalf("independent login: %v", err)
	}

	if _, err := service.Refresh(ctx, familyA.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("rotated replay error = %v, want ErrRefreshTokenReused", err)
	}
	if _, err := service.Refresh(ctx, rotatedA.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("family A successor error = %v, want ErrRefreshTokenReused", err)
	}
	if _, err := service.Refresh(ctx, familyB.RefreshToken); err != nil {
		t.Fatalf("independent family B was revoked: %v", err)
	}
}

func TestExpiredRotatedReplayDoesNotEscalateFamily(t *testing.T) {
	store := newFakeStore()
	service := newUserService(store, testConfig(), nil, slog.Default())
	ctx := context.Background()

	first, err := service.Register(ctx, RegisterInput{
		Username: "karl",
		Email:    "karl@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	successor, err := service.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}

	firstHash, err := hashRefreshToken(first.RefreshToken, service.refreshSecret())
	if err != nil {
		t.Fatalf("hash old token: %v", err)
	}
	store.mu.Lock()
	store.sessions[string(firstHash)].expiresAt = time.Now().Add(-time.Minute)
	store.mu.Unlock()

	if _, err := service.Refresh(ctx, first.RefreshToken); !errors.Is(err, ErrRefreshTokenExpired) {
		t.Fatalf("expired replay error = %v, want ErrRefreshTokenExpired", err)
	}
	if _, err := service.Refresh(ctx, successor.RefreshToken); err != nil {
		t.Fatalf("expired replay revoked family successor: %v", err)
	}
}

func TestAuthenticateRejectsDisabledUserFromLiveStore(t *testing.T) {
	store := newFakeStore()
	service := newUserService(store, testConfig(), nil, slog.Default())
	ctx := context.Background()

	result, err := service.Register(ctx, RegisterInput{
		Username: "dave",
		Email:    "dave@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	store.mu.Lock()
	store.users[result.User.ID].Disabled = true
	store.mu.Unlock()

	if _, err := service.AuthenticateAccessToken(ctx, result.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled auth error = %v, want ErrUnauthorized", err)
	}
}

func TestEmailVerificationTokenIsOpaqueAndSingleUse(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	result, err := service.Register(ctx, RegisterInput{
		Username: "erin",
		Email:    "erin@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if result.User.EmailVerified {
		t.Fatal("user should start unverified when email verification is enabled")
	}

	if err := service.AskEmailVerification(ctx, result.User.ID); err != nil {
		t.Fatalf("ask verification: %v", err)
	}
	if len(publisher.token) != 43 {
		t.Fatalf("published token length = %d, want base64url 32 bytes (43)", len(publisher.token))
	}
	if strings.Contains(publisher.token, ".") {
		t.Fatalf("published token still looks like userUUID.code: %q", publisher.token)
	}
	if _, plaintextStored := store.emailTokens[publisher.token]; plaintextStored {
		t.Fatal("plaintext verification token was stored as the map key")
	}
	tokenHash, err := hashEmailVerificationToken(publisher.token, service.refreshSecret())
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if _, hashed := store.emailTokens[string(tokenHash)]; !hashed {
		t.Fatal("hashed verification token was not persisted")
	}
	if publisher.to != "erin@example.test" || publisher.username != "erin" {
		t.Fatalf("unexpected email target: %q %q", publisher.to, publisher.username)
	}

	if err := service.ConfirmEmailVerification(ctx, publisher.token); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := service.ConfirmEmailVerification(ctx, publisher.token); !errors.Is(err, ErrVerificationTokenUsed) {
		t.Fatalf("second confirm error = %v, want ErrVerificationTokenUsed", err)
	}
	if err := service.AskEmailVerification(ctx, result.User.ID); !errors.Is(err, ErrEmailAlreadyVerified) {
		t.Fatalf("ask after verified error = %v, want ErrEmailAlreadyVerified", err)
	}
	if err := service.ConfirmEmailVerification(ctx, "not-a-token"); !errors.Is(err, ErrVerificationTokenInvalid) {
		t.Fatalf("invalid token error = %v, want ErrVerificationTokenInvalid", err)
	}
}

func TestEmailVerificationExpiredAndRateLimited(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	result, err := service.Register(ctx, RegisterInput{
		Username: "frank",
		Email:    "frank@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := service.AskEmailVerification(ctx, result.User.ID); err != nil {
		t.Fatalf("ask: %v", err)
	}
	tokenHash, err := hashEmailVerificationToken(publisher.token, service.refreshSecret())
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}

	store.mu.Lock()
	store.emailTokens[string(tokenHash)].expiresAt = time.Now().Add(-time.Minute)
	store.mu.Unlock()

	if err := service.ConfirmEmailVerification(ctx, publisher.token); !errors.Is(err, ErrVerificationTokenExpired) {
		t.Fatalf("expired token error = %v, want ErrVerificationTokenExpired", err)
	}
	if err := service.AskEmailVerification(ctx, result.User.ID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("resend error = %v, want ErrRateLimited", err)
	}
}

func TestEmailVerificationResendRevokesPreviousToken(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	result, err := service.Register(ctx, RegisterInput{
		Username: "gina",
		Email:    "gina@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := service.AskEmailVerification(ctx, result.User.ID); err != nil {
		t.Fatalf("first ask: %v", err)
	}
	firstToken := publisher.token

	store.mu.Lock()
	store.users[result.User.ID].EmailSendAt = time.Now().Add(-2 * EmailVerificationResendCooldown)
	store.mu.Unlock()

	if err := service.AskEmailVerification(ctx, result.User.ID); err != nil {
		t.Fatalf("second ask: %v", err)
	}
	secondToken := publisher.token
	if firstToken == secondToken {
		t.Fatal("resend reused the previous token")
	}

	if err := service.ConfirmEmailVerification(ctx, firstToken); !errors.Is(err, ErrVerificationTokenUsed) {
		t.Fatalf("old token error = %v, want ErrVerificationTokenUsed", err)
	}
	if err := service.ConfirmEmailVerification(ctx, secondToken); err != nil {
		t.Fatalf("new token confirm: %v", err)
	}
}

func TestEmailVerificationPublishFailureClearsTokenAndDoesNotRecordSend(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{err: errors.New("nats unavailable")}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	result, err := service.Register(ctx, RegisterInput{
		Username: "frankie",
		Email:    "frankie@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := service.AskEmailVerification(ctx, result.User.ID); err == nil {
		t.Fatal("ask should fail when publish fails")
	}
	if publisher.token == "" {
		t.Fatal("publisher should have received the token before failing")
	}
	if err := service.ConfirmEmailVerification(ctx, publisher.token); !errors.Is(err, ErrVerificationTokenUsed) {
		t.Fatalf("unsent token confirm error = %v, want ErrVerificationTokenUsed", err)
	}

	store.mu.Lock()
	sentAt := store.users[result.User.ID].EmailSendAt
	store.mu.Unlock()
	if !sentAt.Equal(time.Unix(0, 0)) {
		t.Fatalf("send time was recorded after failed publish: %v", sentAt)
	}

	publisher.err = nil
	if err := service.AskEmailVerification(ctx, result.User.ID); err != nil {
		t.Fatalf("retry after failed publish: %v", err)
	}
}

func TestPasswordResetFlowImmediatelyInvalidatesAccessAndRefresh(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	registered, err := service.Register(ctx, RegisterInput{
		Username: "laura",
		Email:    "laura@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, registered.AccessToken); err != nil {
		t.Fatalf("access token before reset: %v", err)
	}

	if err := service.RequestPasswordReset(ctx, "  LAURA@example.test "); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	if len(publisher.resetToken) != 43 {
		t.Fatalf("reset token length = %d, want 43", len(publisher.resetToken))
	}
	if publisher.resetTo != "laura@example.test" || publisher.resetUser != "laura" {
		t.Fatalf("unexpected reset target: %q %q", publisher.resetTo, publisher.resetUser)
	}

	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "new correct horse"); err != nil {
		t.Fatalf("confirm reset: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, registered.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old access token error = %v, want ErrUnauthorized", err)
	}
	if _, err := service.Refresh(ctx, registered.RefreshToken); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("old refresh token error = %v, want ErrRefreshTokenInvalid", err)
	}
	if _, err := service.Login(ctx, "laura", "correct horse battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password login error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.Login(ctx, "laura", "new correct horse"); err != nil {
		t.Fatalf("new password login: %v", err)
	}
	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "third password"); !errors.Is(err, ErrResetTokenUsed) {
		t.Fatalf("replayed reset error = %v, want ErrResetTokenUsed", err)
	}

	updated, err := service.GetUser(ctx, registered.User.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.AuthVersion != 1 {
		t.Fatalf("auth_version = %d, want 1", updated.AuthVersion)
	}
}

func TestPasswordResetExpiredInvalidAndAntiEnumeration(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	if _, err := service.Register(ctx, RegisterInput{
		Username: "mason",
		Email:    "mason@example.test",
		Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := service.RequestPasswordReset(ctx, "missing@example.test"); err != nil {
		t.Fatalf("unknown account request: %v", err)
	}
	if publisher.resetToken != "" {
		t.Fatal("unknown account published a reset token")
	}

	if err := service.RequestPasswordReset(ctx, "mason@example.test"); err != nil {
		t.Fatalf("known account request: %v", err)
	}
	resetHash, err := hashPasswordResetToken(publisher.resetToken, service.refreshSecret())
	if err != nil {
		t.Fatalf("hash reset token: %v", err)
	}
	store.mu.Lock()
	store.passwordReset[string(resetHash)].expiresAt = time.Now().Add(-time.Minute)
	store.mu.Unlock()

	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "new correct horse"); !errors.Is(err, ErrResetTokenExpired) {
		t.Fatalf("expired reset error = %v, want ErrResetTokenExpired", err)
	}
	if err := service.ConfirmPasswordReset(ctx, "not-a-token", "new correct horse"); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("invalid reset error = %v, want ErrResetTokenInvalid", err)
	}

	masonID := mustUserID(t, store, "mason")
	store.mu.Lock()
	store.users[masonID].Disabled = true
	store.mu.Unlock()
	publisher.resetToken = ""
	if err := service.RequestPasswordReset(ctx, "mason@example.test"); err != nil {
		t.Fatalf("disabled account request: %v", err)
	}
	if publisher.resetToken != "" {
		t.Fatal("disabled account published a reset token")
	}
}

func TestPasswordResetResendInvalidatesPreviousToken(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	if _, err := service.Register(ctx, RegisterInput{
		Username: "rosa",
		Email:    "rosa@example.test",
		Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.RequestPasswordReset(ctx, "rosa@example.test"); err != nil {
		t.Fatalf("first request: %v", err)
	}
	firstToken := publisher.resetToken
	if err := service.RequestPasswordReset(ctx, "rosa@example.test"); err != nil {
		t.Fatalf("second request: %v", err)
	}
	secondToken := publisher.resetToken
	if firstToken == secondToken {
		t.Fatal("resend reused the previous reset token")
	}
	if err := service.ConfirmPasswordReset(ctx, firstToken, "new correct horse"); !errors.Is(err, ErrResetTokenUsed) {
		t.Fatalf("old reset token error = %v, want ErrResetTokenUsed", err)
	}
	if err := service.ConfirmPasswordReset(ctx, secondToken, "new correct horse"); err != nil {
		t.Fatalf("new reset token confirm: %v", err)
	}
}

func TestPasswordResetResendPreservesExpiredStatus(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	if _, err := service.Register(ctx, RegisterInput{
		Username: "sara",
		Email:    "sara@example.test",
		Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.RequestPasswordReset(ctx, "sara@example.test"); err != nil {
		t.Fatalf("first request: %v", err)
	}
	expiredToken := publisher.resetToken
	expiredHash, err := hashPasswordResetToken(expiredToken, service.refreshSecret())
	if err != nil {
		t.Fatalf("hash expired token: %v", err)
	}
	store.mu.Lock()
	store.passwordReset[string(expiredHash)].expiresAt = time.Now().Add(-time.Minute)
	store.mu.Unlock()

	if err := service.RequestPasswordReset(ctx, "sara@example.test"); err != nil {
		t.Fatalf("second request: %v", err)
	}
	activeToken := publisher.resetToken
	if err := service.ConfirmPasswordReset(ctx, expiredToken, "new correct horse"); !errors.Is(err, ErrResetTokenExpired) {
		t.Fatalf("expired token after resend error = %v, want ErrResetTokenExpired", err)
	}
	if err := service.ConfirmPasswordReset(ctx, activeToken, "new correct horse"); err != nil {
		t.Fatalf("active token confirm: %v", err)
	}
}

func TestPasswordResetIssuedBeforeDisableCannotConfirm(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	registered, err := service.Register(ctx, RegisterInput{
		Username: "tina",
		Email:    "tina@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.RequestPasswordReset(ctx, "tina@example.test"); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	originalHash := registered.User.Password
	originalVersion := registered.User.AuthVersion

	tinaID := mustUserID(t, store, "tina")
	store.mu.Lock()
	store.users[tinaID].Disabled = true
	store.mu.Unlock()

	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "attacker password"); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("disabled confirm error = %v, want ErrResetTokenInvalid", err)
	}
	store.mu.Lock()
	user := store.users[tinaID]
	passwordUnchanged := user.Password == originalHash
	versionUnchanged := user.AuthVersion == originalVersion
	store.mu.Unlock()
	if !passwordUnchanged || !versionUnchanged {
		t.Fatal("disabled reset mutated password/auth_version")
	}

	store.mu.Lock()
	store.users[tinaID].Disabled = false
	store.mu.Unlock()
	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "attacker password"); !errors.Is(err, ErrResetTokenUsed) {
		t.Fatalf("re-enabled token error = %v, want ErrResetTokenUsed", err)
	}
}

func TestPasswordResetPublishFailureRevokesToken(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{resetErr: errors.New("smtp unavailable")}
	service := newUserService(store, testConfig(), publisher, slog.Default())
	ctx := context.Background()

	if _, err := service.Register(ctx, RegisterInput{
		Username: "nina",
		Email:    "nina@example.test",
		Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.RequestPasswordReset(ctx, "nina@example.test"); err != nil {
		t.Fatalf("request reset should stay generic: %v", err)
	}
	if publisher.resetToken == "" {
		t.Fatal("publisher did not receive a token before failure")
	}
	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "new correct horse"); !errors.Is(err, ErrResetTokenUsed) {
		t.Fatalf("undelivered reset token error = %v, want ErrResetTokenUsed", err)
	}
}

func TestPasswordResetRequestWithoutEmailDeliveryIsGeneric(t *testing.T) {
	store := newFakeStore()
	cfg := testConfig()
	cfg.Email.Enabled = false
	service := newUserService(store, cfg, nil, slog.Default())
	ctx := context.Background()

	if _, err := service.Register(ctx, RegisterInput{
		Username: "peter",
		Email:    "peter@example.test",
		Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.RequestPasswordReset(ctx, "peter@example.test"); err != nil {
		t.Fatalf("request without email delivery should be generic: %v", err)
	}
}

func TestPasswordResetDoesNotLogTokenOrEmail(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	store := newFakeStore()
	publisher := &fakePublisher{}
	service := newUserService(store, testConfig(), publisher, logger)
	ctx := context.Background()

	if _, err := service.Register(ctx, RegisterInput{
		Username: "olga",
		Email:    "olga@example.test",
		Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.RequestPasswordReset(ctx, "olga@example.test"); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	output := logs.String()
	for _, secret := range []string{publisher.resetToken, "olga@example.test"} {
		if strings.Contains(output, secret) {
			t.Fatalf("logs leaked password-reset material: %q", secret)
		}
	}
}

func mustUserID(t *testing.T, store *fakeStore, username string) uuid.UUID {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	id, ok := store.byName[username]
	if !ok {
		t.Fatalf("user %q not found", username)
	}
	return id
}

func TestChangePasswordRevokesRefreshSessions(t *testing.T) {
	store := newFakeStore()
	service := newUserService(store, testConfig(), nil, slog.Default())
	ctx := context.Background()

	result, err := service.Register(ctx, RegisterInput{
		Username: "grace",
		Email:    "grace@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := service.ChangePassword(ctx, result.User.ID, "wrong password", "new correct horse"); !errors.Is(err, ErrCurrentPasswordIncorrect) {
		t.Fatalf("wrong current password error = %v, want ErrCurrentPasswordIncorrect", err)
	}
	if err := service.ChangePassword(ctx, result.User.ID, "correct horse battery", "new correct horse"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, result.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old access token after password change error = %v, want ErrUnauthorized", err)
	}
	if _, err := service.Login(ctx, "grace", "correct horse battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password error = %v, want ErrInvalidCredentials", err)
	}
	updated, err := service.GetUser(ctx, result.User.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.AuthVersion != 1 {
		t.Fatalf("auth_version after password change = %d, want 1", updated.AuthVersion)
	}
	if _, err := service.Login(ctx, "grace", "new correct horse"); err != nil {
		t.Fatalf("new password login: %v", err)
	}
	if _, err := service.Refresh(ctx, result.RefreshToken); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("refresh after password change error = %v, want ErrRefreshTokenInvalid", err)
	}
}

func TestServiceDoesNotLogSecrets(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	store := newFakeStore()
	service := newUserService(store, testConfig(), nil, logger)
	ctx := context.Background()

	result, err := service.Register(ctx, RegisterInput{
		Username: "heidi",
		Email:    "heidi@example.test",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := service.Refresh(ctx, result.RefreshToken); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := service.Refresh(ctx, result.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("replay: %v", err)
	}

	output := logs.String()
	for _, secret := range []string{result.AccessToken, result.RefreshToken, "correct horse battery"} {
		if strings.Contains(output, secret) {
			t.Fatalf("logs leaked a secret: %q", secret)
		}
	}
}

func TestPasswordResetTokenIsOpaqueAndDomainSeparated(t *testing.T) {
	token, err := generatePasswordResetToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(token) != 43 {
		t.Fatalf("token length = %d, want 43", len(token))
	}

	resetHash, err := hashPasswordResetToken(token, "secret")
	if err != nil {
		t.Fatalf("reset hash: %v", err)
	}
	refreshHash, err := hashRefreshToken(token, "secret")
	if err != nil {
		t.Fatalf("refresh hash: %v", err)
	}
	verificationHash, err := hashEmailVerificationToken(token, "secret")
	if err != nil {
		t.Fatalf("verification hash: %v", err)
	}
	if bytes.Equal(resetHash, refreshHash) || bytes.Equal(resetHash, verificationHash) {
		t.Fatal("password reset HMAC is not domain-separated")
	}

	again, err := generatePasswordResetToken()
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	if token == again {
		t.Fatal("password reset token generator repeated a value")
	}
}
