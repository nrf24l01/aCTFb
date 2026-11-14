package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

const (
	corsAllowMethods  = "GET,HEAD,POST,PATCH,PUT,DELETE,OPTIONS"
	corsAllowHeaders  = "Authorization,Content-Type"
	corsExposeHeaders = "X-Request-ID,Retry-After"
)

// corsMiddleware implements the exact-origin, credentialed CORS policy used by
// the API. It intentionally has no wildcard mode. Echo's stock middleware
// package pulls an additional dependency that is outside this slice's module
// scope, so this keeps the same security semantics with fewer moving parts.
func corsMiddleware(allowedOrigins []string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			res := c.Response()

			res.Header().Add(echo.HeaderVary, echo.HeaderOrigin)

			origin := req.Header.Get(echo.HeaderOrigin)
			if origin == "" {
				return next(c)
			}

			allowedOrigin, allowed := matchOrigin(allowedOrigins, origin)
			preflight := req.Method == http.MethodOptions &&
				req.Header.Get(echo.HeaderAccessControlRequestMethod) != ""

			if !allowed {
				if preflight {
					// Denied preflight: no ACAO/ACAC and no internal detail.
					return c.NoContent(http.StatusNoContent)
				}
				// Non-preflight requests still execute without CORS headers; the
				// browser blocks the response. Never rewrite the response body.
				return next(c)
			}

			res.Header().Set(echo.HeaderAccessControlAllowOrigin, allowedOrigin)
			res.Header().Set(echo.HeaderAccessControlAllowCredentials, "true")

			if !preflight {
				res.Header().Set(echo.HeaderAccessControlExposeHeaders, corsExposeHeaders)
				return next(c)
			}

			res.Header().Add(echo.HeaderVary, echo.HeaderAccessControlRequestMethod)
			res.Header().Add(echo.HeaderVary, echo.HeaderAccessControlRequestHeaders)
			res.Header().Set(echo.HeaderAccessControlAllowMethods, corsAllowMethods)
			res.Header().Set(echo.HeaderAccessControlAllowHeaders, corsAllowHeaders)

			return c.NoContent(http.StatusNoContent)
		}
	}
}

func matchOrigin(allowedOrigins []string, origin string) (string, bool) {
	for _, allowed := range allowedOrigins {
		if strings.EqualFold(allowed, origin) {
			// Return the request's serialized origin so the ACAO header matches
			// the browser byte-for-byte even if the allowlist entry used
			// different letter casing.
			return origin, true
		}
	}
	return "", false
}
