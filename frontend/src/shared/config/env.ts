const rawBase = import.meta.env.VITE_API_BASE_URL?.trim()

/**
 * Build-time API prefix. The version segment is appended here exactly once.
 * VITE_API_BASE_URL defaults to /api; passing /api/v2 is tolerated but the
 * duplicate version segment is removed.
 */
export function normalizeApiBaseUrl(value: string | undefined): string {
  const base = (value || '/api').replace(/\/+$/, '')
  return base.endsWith('/v2') ? base.slice(0, -3) : base
}

export const apiBaseUrl = normalizeApiBaseUrl(rawBase)
export const apiV2BaseUrl = `${apiBaseUrl}/v2`
/**
 * Separate origin for opaque plugin iframes. The production deployment must
 * serve plugin assets here (default local dev: http://127.0.0.2:8091).
 * A package field can never override this value.
 */
export const pluginIframeOrigin =
  import.meta.env.VITE_PLUGIN_IFRAME_ORIGIN?.trim() || 'http://127.0.0.2:8091'
