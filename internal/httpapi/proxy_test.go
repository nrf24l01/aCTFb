package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestTrustedProxyResolution(t *testing.T) {
	tests := []struct {
		name       string
		cidrs      []string
		remoteAddr string
		xff        []string
		wantIP     string
	}{
		{
			name:       "untrusted peer ignores spoofed XFF",
			remoteAddr: "203.0.113.10:1234",
			xff:        []string{"198.51.100.7"},
			wantIP:     "203.0.113.10",
		},
		{
			name:       "trusted peer honors XFF",
			cidrs:      []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        []string{"198.51.100.7"},
			wantIP:     "198.51.100.7",
		},
		{
			name:       "chain drops trusted hops from the right",
			cidrs:      []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.2:1234",
			xff:        []string{"198.51.100.9, 10.0.0.4, 10.0.0.3"},
			wantIP:     "198.51.100.9",
		},
		{
			name:       "all trusted hops fall back to leftmost",
			cidrs:      []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.2:1234",
			xff:        []string{"10.0.0.7, 10.0.0.6"},
			wantIP:     "10.0.0.7",
		},
		{
			name:       "malformed chain fails closed to transport",
			cidrs:      []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        []string{"198.51.100.7, not-an-ip"},
			wantIP:     "10.0.0.5",
		},
		{
			name:       "empty chain entry fails closed to transport",
			cidrs:      []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        []string{"198.51.100.7, , 10.0.0.6"},
			wantIP:     "10.0.0.5",
		},
		{
			name:       "multiple XFF headers are combined",
			cidrs:      []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        []string{"198.51.100.7", "10.0.0.6"},
			wantIP:     "198.51.100.7",
		},
		{
			name:       "IPv6 trusted peer and forwarded client",
			cidrs:      []string{"::1/128"},
			remoteAddr: "[::1]:1234",
			xff:        []string{"2001:db8::1"},
			wantIP:     "2001:db8::1",
		},
		{
			name:       "XFF hop with port is accepted",
			cidrs:      []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        []string{"198.51.100.7:443"},
			wantIP:     "198.51.100.7",
		},
		{
			name:       "trusted peer without XFF uses transport",
			cidrs:      []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			wantIP:     "10.0.0.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			if err := ConfigureTrustedProxies(e, tt.cidrs); err != nil {
				t.Fatalf("ConfigureTrustedProxies() error = %v", err)
			}
			e.GET("/ip", func(c *echo.Context) error {
				value, _ := c.Get(ClientIPKey).(string)
				return c.String(http.StatusOK, value)
			})

			req := httptest.NewRequest(http.MethodGet, "/ip", nil)
			req.RemoteAddr = tt.remoteAddr
			for _, xff := range tt.xff {
				req.Header.Add("X-Forwarded-For", xff)
			}

			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantIP {
				t.Fatalf("client_ip = %q, want %q", got, tt.wantIP)
			}
		})
	}
}

func TestTrustedProxyIgnoresForwardedHeader(t *testing.T) {
	e := echo.New()
	if err := ConfigureTrustedProxies(e, []string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("ConfigureTrustedProxies() error = %v", err)
	}
	e.GET("/ip", func(c *echo.Context) error {
		value, _ := c.Get(ClientIPKey).(string)
		return c.String(http.StatusOK, value)
	})

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("Forwarded", "for=198.51.100.7")

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if got := strings.TrimSpace(rec.Body.String()); got != "10.0.0.5" {
		t.Fatalf("client_ip = %q, want transport peer 10.0.0.5", got)
	}
}

func TestTrustedProxyRejectsOversizedChain(t *testing.T) {
	e := echo.New()
	if err := ConfigureTrustedProxies(e, []string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("ConfigureTrustedProxies() error = %v", err)
	}
	e.GET("/ip", func(c *echo.Context) error {
		value, _ := c.Get(ClientIPKey).(string)
		return c.String(http.StatusOK, value)
	})

	entries := make([]string, 0, maxForwardedForEntries+1)
	for i := 0; i < maxForwardedForEntries+1; i++ {
		entries = append(entries, "198.51.100."+strconv.Itoa(i+1))
	}

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", strings.Join(entries, ","))

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if got := strings.TrimSpace(rec.Body.String()); got != "10.0.0.5" {
		t.Fatalf("oversized chain should fail closed, got %q", got)
	}
}

func TestConfigureTrustedProxiesRejectsInvalidCIDR(t *testing.T) {
	if err := ConfigureTrustedProxies(echo.New(), []string{"0.0.0.0/0"}); err == nil {
		t.Fatal("ConfigureTrustedProxies() error = nil, want all-address rejection")
	}
}
