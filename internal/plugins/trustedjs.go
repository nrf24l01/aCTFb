package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// trustedVisible returns the explicit full-trust page metadata. It is exposed
// only when the operator gate is on, the install approval is persisted, and the
// caller has at least one live grant for a permission the plugin declares.
func (m *Manager) trustedVisible(ctx context.Context, installed Installed, userID authorization.Principal) (*TrustedJS, []VisibleItem, error) {
	if installed.Manifest.Frontend.Mode != FrontendTrustedJS {
		return nil, nil, nil
	}
	if !m.allowTrustedJS || !installed.TrustedJSApproved {
		return nil, nil, nil
	}
	granted := false
	for _, permission := range installed.Manifest.Permissions {
		ok, err := m.hasAnyActiveGrant(ctx, userID.UserID, installed.ID, permission.ID)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			granted = true
			break
		}
	}
	if !granted {
		return nil, nil, nil
	}
	_, integrity, err := m.trustedBundle(ctx, installed.ID)
	if err != nil {
		return nil, nil, err
	}
	trusted := &TrustedJS{
		Entry:     installed.Manifest.Frontend.Entry,
		URL:       "/api/v2/trusted-plugins/" + string(installed.ID) + "/bundle",
		Integrity: integrity,
	}
	page := VisibleItem{
		Path:  "/plugins/" + string(installed.ID) + "/trusted",
		Label: installed.Manifest.Name,
	}
	return trusted, []VisibleItem{page}, nil
}

// TrustedBundle returns the exact installed ESM bytes and their sha256 hash.
// caller authorization is rechecked; the core service token/DSN never enters
// the browser.
func (m *Manager) TrustedBundle(ctx context.Context, principal authorization.Principal, id ID) ([]byte, string, error) {
	if principal.UserID.String() == "" {
		return nil, "", ErrPermissionDenied
	}
	installed, err := m.loadInstalled(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if installed.Status != StatusEnabled {
		return nil, "", ErrBackendUnavailable
	}
	if installed.Manifest.Frontend.Mode != FrontendTrustedJS {
		return nil, "", ErrNotFound
	}
	if !m.allowTrustedJS {
		return nil, "", ErrTrustedJSDisabled
	}
	if !installed.TrustedJSApproved {
		return nil, "", ErrTrustedJSNotApproved
	}
	granted := false
	for _, permission := range installed.Manifest.Permissions {
		ok, err := m.hasAnyActiveGrant(ctx, principal.UserID, installed.ID, permission.ID)
		if err != nil {
			return nil, "", err
		}
		if ok {
			granted = true
			break
		}
	}
	if !granted {
		return nil, "", ErrPermissionDenied
	}
	return m.trustedBundle(ctx, id)
}

func (m *Manager) trustedBundle(ctx context.Context, id ID) ([]byte, string, error) {
	installed, err := m.loadInstalled(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if installed.Status != StatusEnabled || installed.Manifest.Frontend.Mode != FrontendTrustedJS {
		return nil, "", ErrNotFound
	}
	path, info, _, err := m.ResolveAsset(ctx, id, installed.Manifest.Frontend.Entry)
	if err != nil {
		return nil, "", ErrNotFound
	}
	if info.Size() <= 0 || info.Size() > MaxAssetBytes {
		return nil, "", ErrRequestRejected
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", ErrNotFound
	}
	digest := sha256.Sum256(raw)
	return raw, "sha256:" + hex.EncodeToString(digest[:]), nil
}
