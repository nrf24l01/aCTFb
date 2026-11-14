// Package assets serves plugin HTML/JS/CSS from the configured separate asset
// origin. It never shares a listener or cookie jar with the core API origin.
package assets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/plugins"
)

// Options configures the separated asset listener.
type Options struct {
	Manager           *plugins.Manager
	Origin            string
	ListenAddress     string
	PlatformOrigin    string
	ReadHeaderTimeout time.Duration
	Logger            *slog.Logger
}

// Server is an HTTP server bound to the plugin asset origin only.
type Server struct {
	manager        *plugins.Manager
	origin         string
	listenAddress  string
	platformOrigin string
	http           *http.Server
	logger         *slog.Logger
}

// NewServer validates the origin/listener split and builds the asset handler.
func NewServer(options Options) (*Server, error) {
	if options.Manager == nil {
		return nil, errors.New("plugins/assets: manager is required")
	}
	if err := config.ValidateAllowedOrigin(options.Origin); err != nil {
		return nil, fmt.Errorf("plugins/assets: invalid origin: %w", err)
	}
	parsed, err := url.Parse(options.Origin)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("plugins/assets: origin must be absolute")
	}
	if strings.TrimSpace(options.ListenAddress) == "" {
		options.ListenAddress = parsed.Host
	}
	listenHost, listenPort, err := net.SplitHostPort(options.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("plugins/assets: invalid listen address: %w", err)
	}
	originHost, originPort, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return nil, fmt.Errorf("plugins/assets: invalid origin host: %w", err)
	}
	if listenHost != originHost || listenPort != originPort {
		return nil, errors.New("plugins/assets: listen address must match the configured asset origin")
	}
	if err := config.ValidateAllowedOrigin(options.PlatformOrigin); err != nil {
		return nil, fmt.Errorf("plugins/assets: invalid platform origin: %w", err)
	}
	if strings.EqualFold(strings.TrimRight(options.PlatformOrigin, "/"), strings.TrimRight(options.Origin, "/")) {
		return nil, errors.New("plugins/assets: asset origin must differ from the platform origin")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.ReadHeaderTimeout <= 0 {
		options.ReadHeaderTimeout = 5 * time.Second
	}
	server := &Server{
		manager:        options.Manager,
		origin:         strings.TrimRight(options.Origin, "/"),
		listenAddress:  options.ListenAddress,
		platformOrigin: strings.TrimRight(options.PlatformOrigin, "/"),
		logger:         options.Logger,
	}
	server.http = &http.Server{
		Addr:              options.ListenAddress,
		Handler:           server,
		ReadHeaderTimeout: options.ReadHeaderTimeout,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	return server, nil
}

// ListenAndServe blocks until shutdown.
func (s *Server) ListenAndServe() error {
	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully closes the asset listener.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// ServeHTTP serves only /plugins/{id}/assets/{relative-path}.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.RawQuery != "" || r.URL.Fragment != "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(segments) < 4 || segments[0] != "plugins" || segments[2] != "assets" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	pluginID := plugins.ID(segments[1])
	relative := strings.Join(segments[3:], "/")
	if relative == "" || strings.Contains(relative, "..") || strings.Contains(relative, "%") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	filePath, info, manifest, err := s.manager.ResolveAsset(r.Context(), pluginID, relative)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	contentType, safe := assetContentType(filePath)
	if !safe {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy(filePath, manifest))
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (s *Server) contentSecurityPolicy(filePath string, manifest plugins.Manifest) string {
	extension := strings.ToLower(path.Ext(filePath))
	frameAncestors := "frame-ancestors " + s.platformOrigin
	if extension == ".svg" {
		return "default-src 'none'; style-src 'unsafe-inline'; img-src data:; " + frameAncestors + "; sandbox"
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'unsafe-inline'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'none'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-src 'none'",
		frameAncestors,
	}, "; ")
}

func assetContentType(filePath string) (string, bool) {
	extension := strings.ToLower(path.Ext(filePath))
	switch extension {
	case ".html", ".htm":
		return "text/html; charset=utf-8", true
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8", true
	case ".css":
		return "text/css; charset=utf-8", true
	case ".json":
		return "application/json", true
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".ico":
		return "image/x-icon", true
	case ".woff":
		return "font/woff", true
	case ".woff2":
		return "font/woff2", true
	case ".svg":
		return "image/svg+xml", true
	case ".txt":
		return "text/plain; charset=utf-8", true
	default:
		if detected := mime.TypeByExtension(extension); detected != "" {
			// Unknown/active types are rejected rather than inherited from the
			// host MIME database.
			return "", false
		}
		return "", false
	}
}
