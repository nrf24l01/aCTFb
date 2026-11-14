import { apiRequest } from '@/shared/api/client'

export const PLUGIN_PROTOCOL = '1'

export interface PluginEnvelope<T = unknown> {
  protocol: '1'
  pluginId: string
  channelNonce: string
  requestId: string
  type:
    | 'actfb.plugin.ready'
    | 'actfb.plugin.context'
    | 'actfb.plugin.api'
    | 'actfb.plugin.result'
    | 'actfb.plugin.navigate'
  payload: T
}

export interface PluginContext {
  theme: 'rose-pine'
  boardUuid?: string
  moduleUuid?: string
  taskUuid?: string
}

export interface PluginApiPayload {
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  path: string
  body?: unknown
}

export interface PluginResultPayload {
  status: number
  body?: unknown
  errorCode?: string
}

export interface PluginNavigatePayload {
  path: string
}

const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/
const TYPES = new Set([
  'actfb.plugin.ready',
  'actfb.plugin.context',
  'actfb.plugin.api',
  'actfb.plugin.result',
  'actfb.plugin.navigate',
])

export function createChannelNonce(): string {
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
}

export function createRequestId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export function isValidEnvelope(
  raw: unknown,
  expected: { pluginId: string; channelNonce: string },
): raw is PluginEnvelope {
  if (!raw || typeof raw !== 'object') return false
  const value = raw as Record<string, unknown>
  return (
    value.protocol === PLUGIN_PROTOCOL &&
    value.pluginId === expected.pluginId &&
    value.channelNonce === expected.channelNonce &&
    typeof value.requestId === 'string' &&
    UUID_RE.test(value.requestId) &&
    typeof value.type === 'string' &&
    TYPES.has(value.type) &&
    value.payload !== null &&
    typeof value.payload === 'object'
  )
}

export function isValidMessageSource(
  event: Pick<MessageEvent, 'source' | 'origin'>,
  iframe: HTMLIFrameElement,
): boolean {
  return event.source === iframe.contentWindow && event.origin === 'null'
}

/**
 * The iframe is opaque (sandbox without allow-same-origin), so its origin is
 * "null" at runtime. The initial navigable URL is validated against the
 * configured separate asset origin instead; a package field cannot choose it.
 */
export function isAllowedPluginIframeUrl(raw: string, configuredOrigin: string, appHost: string): boolean {
  let url: URL
  let origin: URL
  try {
    url = new URL(raw)
    origin = new URL(configuredOrigin)
  } catch {
    return false
  }
  if (origin.protocol !== 'http:' && origin.protocol !== 'https:') return false
  if (origin.pathname !== '/' || origin.search || origin.hash) return false
  if (!url.host || url.origin !== origin.origin) return false
  const configuredHost = origin.hostname
  if (!configuredHost || configuredHost === appHost) return false
  return url.protocol === 'http:' || url.protocol === 'https:'
}

function hasTraversal(raw: string): boolean {
  const lower = raw.toLowerCase()
  return (
    lower.includes('..') ||
    lower.includes('%2e') ||
    lower.includes('%2f') ||
    lower.includes('%5c') ||
    lower.includes('%252e') ||
    lower.includes('\\') ||
    /[\x00-\x1f]/.test(raw)
  )
}

function parseRelative(raw: string): URL | null {
  if (!raw.startsWith('/')) return null
  if (raw.startsWith('//')) return null
  if (hasTraversal(raw)) return null
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(raw)) return null
  try {
    return new URL(raw, 'https://plugin.invalid')
  } catch {
    return null
  }
}

/**
 * Strict relative plugin path. Returns the path after /api/v2 so the central
 * API client can add auth/401 handling; rejects schemes, traversal, encoded
 * traversal, non-plugin paths and mismatched current board/module context.
 */
export function validatePluginApiPath(
  pluginId: string,
  rawPath: string,
  context: { boardUuid?: string; moduleUuid?: string },
): { path: string; url: URL } | null {
  const parsed = parseRelative(rawPath)
  if (!parsed) return null
  if (parsed.pathname.includes('//')) return null
  const apiPrefix = `/api/v2/plugins/${encodeURIComponent(pluginId)}/`
  const relativePrefix = `/plugins/${encodeURIComponent(pluginId)}/`
  let pathname = parsed.pathname
  if (pathname.startsWith(apiPrefix)) {
    pathname = pathname.slice('/api/v2'.length)
  } else if (pathname.startsWith(relativePrefix)) {
    // Already in the plugin namespace.
  } else {
    // A child plugin sends its manifest-relative route, e.g. /boards/<uuid>/hello.
    if (pathname.startsWith('/api/') || pathname.startsWith('/plugins/')) return null
    pathname = relativePrefix + pathname.replace(/^\/+/, '')
  }
  const url = new URL(`${pathname}${parsed.search}`, 'https://plugin.invalid')
  if (url.searchParams.has('boardUuid') && context.boardUuid && url.searchParams.get('boardUuid') !== context.boardUuid) return null
  if (url.searchParams.has('moduleUuid') && context.moduleUuid && url.searchParams.get('moduleUuid') !== context.moduleUuid) return null
  return { path: `${pathname}${parsed.search}`, url }
}

/** Sidebar metadata paths are local plugin pages only; never external URLs. */
export function isSafeVisiblePluginPath(pluginId: string, rawPath: string): boolean {
  const url = parseRelative(rawPath)
  if (!url) return false
  if (url.pathname.includes('//')) return false
  return url.pathname === `/plugins/${pluginId}` || url.pathname.startsWith(`/plugins/${pluginId}/`)
}

/** Child navigation may only stay inside the current board or its plugin. */
export function validatePluginNavigatePath(
  pluginId: string,
  rawPath: string,
  context: { boardUuid?: string },
): string | null {
  const url = parseRelative(rawPath)
  if (!url) return null
  if (url.pathname.includes('//')) return null
  const allowed =
    (context.boardUuid && (url.pathname === `/boards/${context.boardUuid}` || url.pathname.startsWith(`/boards/${context.boardUuid}/`))) ||
    url.pathname === `/plugins/${pluginId}` ||
    url.pathname.startsWith(`/plugins/${pluginId}/`)
  if (!allowed) return null
  if (url.pathname.startsWith('/auth') || url.pathname.startsWith('/logout')) return null
  return `${url.pathname}${url.search}`
}

export interface BridgeOptions {
  iframe: HTMLIFrameElement
  pluginId: string
  context: PluginContext
  navigate: (path: string) => void
  api?: typeof apiRequest
  nonce?: string
}

const MAX_IN_FLIGHT = 8

export class PluginBridge {
  readonly nonce: string
  private readonly iframe: HTMLIFrameElement
  private readonly pluginId: string
  private readonly context: PluginContext
  private readonly navigate: (path: string) => void
  private readonly api: typeof apiRequest
  private readonly pending = new Map<string, ReturnType<typeof setTimeout>>()
  private readonly handler: (event: MessageEvent) => void
  private ready = false

  constructor(options: BridgeOptions) {
    this.iframe = options.iframe
    this.pluginId = options.pluginId
    this.nonce = options.nonce || createChannelNonce()
    this.context = options.context
    this.navigate = options.navigate
    this.api = options.api ?? apiRequest
    this.handler = (event) => void this.onMessage(event)
  }

  start(): void {
    // The child is an opaque-origin iframe and reads the channel nonce from its
    // own URL hash before it can send the authenticated ready message. Set the
    // hash before attaching the listener; a hash-only navigation reloads the
    // opaque document so it runs with the nonce.
    const target = new URL(this.iframe.src, window.location.href)
    target.hash = new URLSearchParams({
      actfbChannel: this.nonce,
      pluginId: this.pluginId,
    }).toString()
    if (this.iframe.src !== target.toString()) this.iframe.src = target.toString()
    window.addEventListener('message', this.handler)
  }

  stop(): void {
    window.removeEventListener('message', this.handler)
    for (const timer of this.pending.values()) clearTimeout(timer)
    this.pending.clear()
    this.ready = false
  }

  private send<T>(type: PluginEnvelope['type'], payload: T, requestId = createRequestId()): string {
    const envelope: PluginEnvelope<T> = {
      protocol: '1',
      pluginId: this.pluginId,
      channelNonce: this.nonce,
      requestId,
      type,
      payload,
    }
    this.iframe.contentWindow?.postMessage(envelope, '*')
    return requestId
  }

  private sendResult(requestId: string, payload: PluginResultPayload): void {
    this.send('actfb.plugin.result', payload, requestId)
  }

  private async onMessage(event: MessageEvent): Promise<void> {
    if (!isValidMessageSource(event, this.iframe)) return
    if (!isValidEnvelope(event.data, { pluginId: this.pluginId, channelNonce: this.nonce })) return
    const message = event.data
    if (message.type === 'actfb.plugin.ready') {
      if (this.ready) return
      this.ready = true
      this.send('actfb.plugin.context', this.context)
      return
    }
    if (message.type === 'actfb.plugin.api') {
      if (this.pending.size >= MAX_IN_FLIGHT) {
        this.sendResult(message.requestId, { status: 429, errorCode: 'PLUGIN_BRIDGE_BUSY' })
        return
      }
      const payload = message.payload as PluginApiPayload
      if (!payload || !['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].includes(payload.method)) {
        this.sendResult(message.requestId, { status: 400, errorCode: 'INVALID_PLUGIN_REQUEST' })
        return
      }
      const validated = validatePluginApiPath(this.pluginId, payload.path, this.context)
      if (!validated) {
        this.sendResult(message.requestId, { status: 400, errorCode: 'INVALID_PLUGIN_PATH' })
        return
      }
      const timer = setTimeout(() => {
        this.pending.delete(message.requestId)
        this.sendResult(message.requestId, { status: 504, errorCode: 'PLUGIN_BRIDGE_TIMEOUT' })
      }, 15_000)
      this.pending.set(message.requestId, timer)
      try {
        const result = await this.api<unknown>(validated.path, {
          method: payload.method,
          body: payload.method === 'GET' ? undefined : payload.body,
        })
        this.sendResult(message.requestId, { status: 200, body: result })
      } catch (error) {
        const status =
          error && typeof error === 'object' && 'status' in error && typeof (error as { status: unknown }).status === 'number'
            ? (error as { status: number }).status
            : 500
        const errorCode =
          error && typeof error === 'object' && 'code' in error ? String((error as { code: unknown }).code) : 'PLUGIN_PROXY_ERROR'
        this.sendResult(message.requestId, { status, errorCode })
      } finally {
        const active = this.pending.get(message.requestId)
        if (active) clearTimeout(active)
        this.pending.delete(message.requestId)
      }
      return
    }
    if (message.type === 'actfb.plugin.navigate') {
      const payload = message.payload as PluginNavigatePayload
      const path = payload && typeof payload.path === 'string'
        ? validatePluginNavigatePath(this.pluginId, payload.path, this.context)
        : null
      if (!path) {
        this.sendResult(message.requestId, { status: 400, errorCode: 'INVALID_PLUGIN_NAVIGATION' })
        return
      }
      this.navigate(path)
      this.sendResult(message.requestId, { status: 200 })
    }
  }
}