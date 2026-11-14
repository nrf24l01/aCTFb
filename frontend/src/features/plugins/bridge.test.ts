import { describe, expect, it, vi } from 'vitest'
import {
  createChannelNonce,
  createRequestId,
  isAllowedPluginIframeUrl,
  isSafeVisiblePluginPath,
  isValidEnvelope,
  isValidMessageSource,
  validatePluginApiPath,
  validatePluginNavigatePath,
} from '@/features/plugins/bridge'

const pluginId = 'vendor.plugin'
const nonce = 'n'.repeat(32)
const requestId = '11111111-1111-4111-8111-111111111111'

function envelope(overrides: Record<string, unknown> = {}) {
  return {
    protocol: '1',
    pluginId,
    channelNonce: nonce,
    requestId,
    type: 'actfb.plugin.api',
    payload: { method: 'GET', path: `/api/v2/plugins/${pluginId}/items` },
    ...overrides,
  }
}

describe('plugin envelope', () => {
  it('accepts the exact protocol/plugin/nonce/request shape', () => {
    expect(isValidEnvelope(envelope(), { pluginId, channelNonce: nonce })).toBe(true)
  })

  it('rejects wrong nonce, plugin, protocol and non-UUID request ids', () => {
    expect(isValidEnvelope(envelope({ channelNonce: 'wrong' }), { pluginId, channelNonce: nonce })).toBe(false)
    expect(isValidEnvelope(envelope({ pluginId: 'other' }), { pluginId, channelNonce: nonce })).toBe(false)
    expect(isValidEnvelope(envelope({ protocol: '2' }), { pluginId, channelNonce: nonce })).toBe(false)
    expect(isValidEnvelope(envelope({ requestId: 'not-a-uuid' }), { pluginId, channelNonce: nonce })).toBe(false)
  })

  it('requires the iframe window as source and opaque origin "null"', () => {
    const fakeWindow = {} as Window
    const iframe = { contentWindow: fakeWindow } as unknown as HTMLIFrameElement
    const event = { source: fakeWindow, origin: 'null' } as MessageEvent
    expect(isValidMessageSource(event, iframe)).toBe(true)
    expect(isValidMessageSource({ ...event, origin: 'https://evil.example' } as MessageEvent, iframe)).toBe(false)
    expect(isValidMessageSource({ ...event, source: window } as MessageEvent, iframe)).toBe(false)
  })

  it('generates distinct CSPRNG nonces/request ids', () => {
    expect(createChannelNonce()).not.toBe(createChannelNonce())
    expect(createRequestId()).toMatch(/^[0-9a-f-]{36}$/)
  })
})

describe('plugin API path', () => {
  const context = { boardUuid: '22222222-2222-4222-8222-222222222222' }

  it('accepts the namespaced plugin prefix and strips /api/v2 for the central client', () => {
    const valid = validatePluginApiPath(pluginId, `/api/v2/plugins/${pluginId}/items?x=1`, context)
    expect(valid?.path).toBe(`/plugins/${pluginId}/items?x=1`)
  })

  it('normalizes a manifest-relative child route into the plugin namespace', () => {
    const valid = validatePluginApiPath(pluginId, `/boards/${context.boardUuid}/hello`, context)
    expect(valid?.path).toBe(`/plugins/${pluginId}/boards/${context.boardUuid}/hello`)
  })

  it('rejects schemes, traversal, encoded traversal and other core paths', () => {
    expect(validatePluginApiPath(pluginId, 'https://evil.example/x', context)).toBeNull()
    expect(validatePluginApiPath(pluginId, `//evil.example/api/v2/plugins/${pluginId}/x`, context)).toBeNull()
    expect(validatePluginApiPath(pluginId, `/api/v2/plugins/${pluginId}/../boards`, context)).toBeNull()
    expect(validatePluginApiPath(pluginId, `/api/v2/plugins/${pluginId}/%2e%2e/boards`, context)).toBeNull()
    expect(validatePluginApiPath(pluginId, `/api/v2/plugins/${pluginId}/%2fetc`, context)).toBeNull()
    expect(validatePluginApiPath(pluginId, '/api/v2/boards', context)).toBeNull()
    expect(validatePluginApiPath(pluginId, `/api/v2/plugins/other/x`, context)).toBeNull()
  })

  it('rejects a mismatched current board/module query context', () => {
    expect(
      validatePluginApiPath(
        pluginId,
        `/api/v2/plugins/${pluginId}/x?boardUuid=33333333-3333-4333-8333-333333333333`,
        context,
      ),
    ).toBeNull()
    expect(
      validatePluginApiPath(
        pluginId,
        `/api/v2/plugins/${pluginId}/x?boardUuid=22222222-2222-4222-8222-222222222222`,
        context,
      ),
    ).not.toBeNull()
  })
})

describe('plugin navigation and iframe origin', () => {
  it('allows only current board and own plugin routes', () => {
    expect(validatePluginNavigatePath(pluginId, '/boards/22222222-2222-4222-8222-222222222222', { boardUuid: '22222222-2222-4222-8222-222222222222' })).not.toBeNull()
    expect(validatePluginNavigatePath(pluginId, `/plugins/${pluginId}/page`, {})).not.toBeNull()
    expect(validatePluginNavigatePath(pluginId, '/boards/33333333-3333-4333-8333-333333333333', { boardUuid: '22222222-2222-4222-8222-222222222222' })).toBeNull()
    expect(validatePluginNavigatePath(pluginId, 'https://evil.example', {})).toBeNull()
    expect(validatePluginNavigatePath(pluginId, '/logout', {})).toBeNull()
  })

  it('requires the exact configured separate origin and a different host', () => {
    expect(isAllowedPluginIframeUrl('http://127.0.0.2:8091/page', 'http://127.0.0.2:8091', 'ctf.example')).toBe(true)
    expect(isAllowedPluginIframeUrl('http://127.0.0.3:8091/page', 'http://127.0.0.2:8091', 'ctf.example')).toBe(false)
    expect(isAllowedPluginIframeUrl('http://127.0.0.2:8091/page', 'http://127.0.0.2:8091', '127.0.0.2')).toBe(false)
    expect(isAllowedPluginIframeUrl('https://127.0.0.2:8091/page', 'http://127.0.0.2:8091', 'ctf.example')).toBe(false)
  })

  it('only accepts local plugin page paths from sidebar metadata', () => {
    expect(isSafeVisiblePluginPath(pluginId, `/plugins/${pluginId}/page`)).toBe(true)
    expect(isSafeVisiblePluginPath(pluginId, 'https://evil.example/page')).toBe(false)
    expect(isSafeVisiblePluginPath(pluginId, '/logout')).toBe(false)
    expect(isSafeVisiblePluginPath(pluginId, `/plugins/other/page`)).toBe(false)
  })
})
import { PluginBridge } from '@/features/plugins/bridge'

describe('plugin host bridge runtime', () => {
  it('sends only theme/current context after ready and never tokens or Web Storage', async () => {
    const postMessage = vi.fn()
    const fakeWindow = { postMessage } as unknown as Window
    const iframe = { contentWindow: fakeWindow } as unknown as HTMLIFrameElement
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    const api = vi.fn()
    const bridge = new PluginBridge({
      iframe,
      pluginId,
      context: {
        theme: 'rose-pine',
        boardUuid: '22222222-2222-4222-8222-222222222222',
        moduleUuid: '33333333-3333-4333-8333-333333333333',
      },
      navigate: vi.fn(),
      api: api as never,
    })
    bridge.start()

    const ready = new MessageEvent('message', {
      data: {
        protocol: '1',
        pluginId,
        channelNonce: bridge.nonce,
        requestId: createRequestId(),
        type: 'actfb.plugin.ready',
        payload: {},
      },
    })
    Object.defineProperty(ready, 'source', { value: fakeWindow })
    Object.defineProperty(ready, 'origin', { value: 'null' })
    window.dispatchEvent(ready)

    expect(postMessage).toHaveBeenCalledTimes(1)
    const envelope = postMessage.mock.calls[0][0] as Record<string, unknown>
    expect(envelope.type).toBe('actfb.plugin.context')
    expect(envelope.channelNonce).toBe(bridge.nonce)
    expect(envelope.payload).toEqual({
      theme: 'rose-pine',
      boardUuid: '22222222-2222-4222-8222-222222222222',
      moduleUuid: '33333333-3333-4333-8333-333333333333',
    })
    expect(JSON.stringify(envelope)).not.toMatch(/accessToken|refresh|serviceToken|credentials|cookie/i)
    expect(setItem).not.toHaveBeenCalled()

    postMessage.mockClear()
    const forged = new MessageEvent('message', {
      data: {
        protocol: '1',
        pluginId,
        channelNonce: bridge.nonce,
        requestId: createRequestId(),
        type: 'actfb.plugin.api',
        payload: { method: 'GET', path: `/api/v2/plugins/${pluginId}/x` },
      },
    })
    Object.defineProperty(forged, 'source', { value: fakeWindow })
    Object.defineProperty(forged, 'origin', { value: 'https://evil.example' })
    window.dispatchEvent(forged)
    expect(api).not.toHaveBeenCalled()
    expect(postMessage).not.toHaveBeenCalled()

    bridge.stop()
    setItem.mockRestore()
  })
})
