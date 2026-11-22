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

// Public returns the anonymous projection. Unpublished documents expose the
// title and the published flag but never the draft Markdown body.
func (s *Service) Public(ctx context.Context) (*PublicSettings, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, ErrNoDatabase
	}
	settings, err := loadSettings(ctx, s.db.Pool)
	if err != nil {
		return nil, err
	}
	public := settings.PublicSettings
	if !public.Published {
		public.IntroMarkdown = ""
	}
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

	actorID, err := requireGlobalAdmin(ctx, tx, principal)
	if err != nil {
		return nil, err
	}

	current, err := loadSettings(ctx, tx)
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

func loadSettings(ctx context.Context, q rowQuerier) (*AdminSettings, error) {
	var settings AdminSettings
	var updatedBy *uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT title, intro_markdown, published, updated_at, updated_by_user_id
		FROM site_settings WHERE id = true
	`).Scan(&settings.Title, &settings.IntroMarkdown, &settings.Published, &settings.UpdatedAt, &updatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		// A deployment upgraded before 018 or a manually truncated table still
		// gets a safe default instead of a 500.
		return &AdminSettings{
			PublicSettings: PublicSettings{Title: "Welcome", Published: false, UpdatedAt: time.Now().UTC()},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("site: load settings: %w", err)
	}
	settings.UpdatedAt = settings.UpdatedAt.UTC()
	settings.UpdatedByUserID = updatedBy
	return &settings, nil
}

func requireGlobalAdmin(ctx context.Context, q rowQuerier, principal authorization.Principal) (uuid.UUID, error) {
	if principal.UserID == uuid.Nil {
		return uuid.Nil, ErrUnauthorized
	}
	var disabled bool
	var roles []string
	err := q.QueryRow(ctx, `
		SELECT disabled, COALESCE(roles, ARRAY['user']::text[]) FROM users WHERE id = $1
	`, principal.UserID).Scan(&disabled, &roles)
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
