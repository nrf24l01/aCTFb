// Package site owns the singleton organizer-controlled landing document.
// It is intentionally small: a title plus Markdown intro, editable only by a
// live global admin and readable through a public, whitelisted projection.
package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

const (
	maxTitleLength = 128
	maxIntroBytes  = 128 * 1024
	adminAction    = "site.update"
	resourceName   = "site_settings"
	fallbackTitle  = "Welcome"
)

var (
	ErrNoDatabase   = errors.New("site: database is not configured")
	ErrUnauthorized = errors.New("site: authentication required")
	ErrForbidden    = errors.New("site: forbidden")
	ErrInvalidInput = errors.New("site: invalid input")
)

// PublicSettings is the only shape anonymous callers may receive. Draft content
// is withheld while the document is unpublished.
type PublicSettings struct {
	Title         string
	IntroMarkdown string
	Published     bool
	UpdatedAt     time.Time
}

// AdminSettings additionally exposes the last editor identity to admins only.
type AdminSettings struct {
	PublicSettings
	UpdatedByUserID *uuid.UUID
}

// UpdateInput uses pointers so omitted fields stay unchanged.
type UpdateInput struct {
	Title         *string
	IntroMarkdown *string
	Published     *bool
}

// Service persists the singleton row against live PostgreSQL.
type Service struct {
	db *postgres.DB
}

// NewService constructs the site service.
func NewService(db *postgres.DB) *Service {
	return &Service{db: db}
}

// fallbackIntro is the meaningful built-in landing content used when no site
// document is published. It is deliberately generic: an unpublished draft must
// never leak its own title or body to an anonymous visitor.
const fallbackIntro = `This is the challenge board. Browse the open boards to get started.

- Sign in to join a board and submit answers.
- Organizers can edit this introduction under **Admin → Site**.`

// Public returns the anonymous projection. An unpublished document discloses
// neither its draft title nor its draft body: it returns the safe fallback.
func (s *Service) Public(ctx context.Context) (*PublicSettings, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, ErrNoDatabase
	}
	settings, err := loadSettings(ctx, s.db.Pool)
	if err != nil {
		return nil, err
	}
	if !settings.Published {
		return &PublicSettings{
			Title:         fallbackTitle,
			IntroMarkdown: fallbackIntro,
			Published:     false,
			UpdatedAt:     settings.UpdatedAt,
		}, nil
	}
	public := settings.PublicSettings
	return &public, nil
}

// Admin returns the full document to a live global admin.
func (s *Service) Admin(ctx context.Context, principal authorization.Principal) (*AdminSettings, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, ErrNoDatabase
	}
	if _, err := requireGlobalAdmin(ctx, s.db.Pool, principal); err != nil {
		return nil, err
	}
	return loadSettings(ctx, s.db.Pool)
}

// Update validates, persists and audits the change in one transaction.
func (s *Service) Update(ctx context.Context, principal authorization.Principal, in UpdateInput) (*AdminSettings, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, ErrNoDatabase
	}
	if err := validateUpdate(in); err != nil {
		return nil, err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("site: begin update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// FOR SHARE pins the admin row for the transaction so a concurrent demotion
	// cannot race the write, matching the access service's write paths.
	actorID, err := requireGlobalAdminForShare(ctx, tx, principal)
	if err != nil {
		return nil, err
	}

	// Lock the singleton row before reading it. Without this, two concurrent
	// saves of different fields would each write the whole row and silently
	// overwrite the other's change.
	current, err := loadSettingsForUpdate(ctx, tx)
	if err != nil {
		return nil, err
	}
	next := current
	changed := make([]string, 0, 3)
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		next.Title = title
		changed = append(changed, "title")
	}
	if in.IntroMarkdown != nil {
		next.IntroMarkdown = *in.IntroMarkdown
		changed = append(changed, "introMarkdown")
	}
	if in.Published != nil {
		next.Published = *in.Published
		changed = append(changed, "published")
	}
	next.UpdatedByUserID = &actorID
	next.UpdatedAt = time.Now().UTC()

	if _, err := tx.Exec(ctx, `
		UPDATE site_settings
		SET title = $1, intro_markdown = $2, published = $3, updated_by_user_id = $4, updated_at = $5
		WHERE id = true
	`, next.Title, next.IntroMarkdown, next.Published, actorID, next.UpdatedAt); err != nil {
		return nil, fmt.Errorf("site: update settings: %w", err)
	}

	details, err := json.Marshal(map[string]any{
		"changed":     changed,
		"titleLength": utf8.RuneCountInString(next.Title),
		"introBytes":  len(next.IntroMarkdown),
		"published":   next.Published,
	})
	if err != nil {
		return nil, fmt.Errorf("site: encode audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, action, resource_type, details)
		VALUES ($1, $2, $3, $4)
	`, actorID, adminAction, resourceName, details); err != nil {
		return nil, fmt.Errorf("site: write audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("site: commit update: %w", err)
	}
	return next, nil
}

func validateUpdate(in UpdateInput) error {
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" || utf8.RuneCountInString(title) > maxTitleLength {
			return ErrInvalidInput
		}
	}
	if in.IntroMarkdown != nil && len(*in.IntroMarkdown) > maxIntroBytes {
		return ErrInvalidInput
	}
	return nil
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// loadSettingsForUpdate reads the singleton with a row lock inside the caller's
// transaction, so the read-modify-write of the document is serialized.
func loadSettingsForUpdate(ctx context.Context, q rowQuerier) (*AdminSettings, error) {
	return loadSettingsWithQuery(ctx, q, `
		SELECT title, intro_markdown, published, updated_at, updated_by_user_id
		FROM site_settings WHERE id = true FOR UPDATE
	`)
}

func loadSettings(ctx context.Context, q rowQuerier) (*AdminSettings, error) {
	return loadSettingsWithQuery(ctx, q, `
		SELECT title, intro_markdown, published, updated_at, updated_by_user_id
		FROM site_settings WHERE id = true
	`)
}

func loadSettingsWithQuery(ctx context.Context, q rowQuerier, query string) (*AdminSettings, error) {
	var settings AdminSettings
	var updatedBy *uuid.UUID
	err := q.QueryRow(ctx, query).Scan(
		&settings.Title, &settings.IntroMarkdown, &settings.Published, &settings.UpdatedAt, &updatedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// A deployment upgraded before 018 or a manually truncated table still
		// gets a safe default instead of a 500.
		return &AdminSettings{
			PublicSettings: PublicSettings{
				Title:         fallbackTitle,
				IntroMarkdown: fallbackIntro,
				Published:     false,
				UpdatedAt:     time.Now().UTC(),
			},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("site: load settings: %w", err)
	}
	settings.UpdatedAt = settings.UpdatedAt.UTC()
	settings.UpdatedByUserID = updatedBy
	return &settings, nil
}

func requireGlobalAdminForShare(ctx context.Context, q rowQuerier, principal authorization.Principal) (uuid.UUID, error) {
	return requireGlobalAdminWithLock(ctx, q, principal, " FOR SHARE")
}

func requireGlobalAdmin(ctx context.Context, q rowQuerier, principal authorization.Principal) (uuid.UUID, error) {
	return requireGlobalAdminWithLock(ctx, q, principal, "")
}

func requireGlobalAdminWithLock(ctx context.Context, q rowQuerier, principal authorization.Principal, lock string) (uuid.UUID, error) {
	if principal.UserID == uuid.Nil {
		return uuid.Nil, ErrUnauthorized
	}
	var disabled bool
	var roles []string
	err := q.QueryRow(ctx, `
		SELECT disabled, COALESCE(roles, ARRAY['user']::text[]) FROM users WHERE id = $1
	`+lock, principal.UserID).Scan(&disabled, &roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrUnauthorized
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("site: load live user: %w", err)
	}
	if disabled {
		return uuid.Nil, ErrUnauthorized
	}
	for _, role := range roles {
		if strings.EqualFold(strings.TrimSpace(role), "admin") {
			return principal.UserID, nil
		}
	}
	return uuid.Nil, ErrForbidden
}
