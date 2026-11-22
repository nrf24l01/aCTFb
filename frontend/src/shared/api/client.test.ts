import { beforeEach, describe, expect, it, vi } from 'vitest'

const base = '/api/v2'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function tokenResponse(token = 'access-token-value-123456'): Response {
  return jsonResponse({ accessToken: token, tokenType: 'Bearer', expiresIn: 900 })
}

async function loadClient() {
  vi.resetModules()
  return import('@/shared/api/client')
}

beforeEach(() => {
  vi.unstubAllGlobals()
})

describe('api client token handling', () => {
  it('keeps the access token out of browser storage', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ok: true }))
    vi.stubGlobal('fetch', fetchMock)

    const client = await loadClient()
    client.setAccessToken('access-token-value-123456')
    await client.apiRequest('/boards')

    expect(setItem).not.toHaveBeenCalled()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [, init] = fetchMock.mock.calls[0]
    expect(init.credentials).toBe('include')
    expect(init.headers.Authorization).toBe('Bearer access-token-value-123456')
    setItem.mockRestore()
  })

  it('refreshes once after a 401 and retries the request with the new token', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 401 }))
      .mockResolvedValueOnce(tokenResponse('fresh-token-value-123456'))
      .mockResolvedValueOnce(jsonResponse({ items: [] }))
    vi.stubGlobal('fetch', fetchMock)

    const client = await loadClient()
    const result = await client.apiRequest<{ items: unknown[] }>('/boards')

    expect(result).toEqual({ items: [] })
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(fetchMock.mock.calls[1][0]).toBe(`${base}/auth/refresh`)
    expect(fetchMock.mock.calls[2][1].headers.Authorization).toBe('Bearer fresh-token-value-123456')
  })

  it('shares one refresh between concurrent 401 responses', async () => {
    let refreshCount = 0
    const fetchMock = vi.fn(async (input: string) => {
      if (input.endsWith('/auth/refresh')) {
        refreshCount += 1
        return tokenResponse('shared-refresh-token-123')
      }
      const auth = String(input)
      const call = fetchMock.mock.calls.filter(([url]) => String(url) === auth).length
      if (auth.endsWith('/boards') && call <= 2) return new Response(null, { status: 401 })
      return jsonResponse({ url: auth })
    })
    vi.stubGlobal('fetch', fetchMock)

    const client = await loadClient()
    await Promise.all([client.apiRequest('/boards'), client.apiRequest('/boards')])

    expect(refreshCount).toBe(1)
  })

  it('clears the session when refresh is rejected', async () => {
    const lost = vi.fn()
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 401 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        code: 'REFRESH_TOKEN_INVALID',
        message: 'Invalid refresh token',
        traceId: 't',
        timestamp: '2026-01-01T00:00:00Z',
        path: '/api/v2/boards',
      }), { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)

    const client = await loadClient()
    client.setAccessToken('stale-token-value-123456')
    client.onAuthLost(lost)

    await expect(client.apiRequest('/boards')).rejects.toMatchObject({
      status: 401,
      code: 'REFRESH_TOKEN_INVALID',
    })
    expect(lost).toHaveBeenCalledTimes(1)
    expect(client.getAccessToken()).toBeNull()
  })

  it('clears the session when the retried request also returns 401', async () => {
    const lost = vi.fn()
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 401 }))
      .mockResolvedValueOnce(tokenResponse('fresh-but-rejected-123'))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        code: 'UNAUTHORIZED',
        message: 'Authentication required or credentials invalid',
        traceId: 'trace-2',
        timestamp: '2026-01-01T00:00:00Z',
        path: '/api/v2/boards',
      }), { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)

    const client = await loadClient()
    client.onAuthLost(lost)

    await expect(client.apiRequest('/boards')).rejects.toMatchObject({ status: 401 })
    expect(lost).toHaveBeenCalledTimes(1)
    expect(client.getAccessToken()).toBeNull()
  })

  it('passes Idempotency-Key through without altering it', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ status: 'REJECTED' }))
    vi.stubGlobal('fetch', fetchMock)

    const client = await loadClient()
    await client.apiRequest('/boards/b/modules/m/tasks/t/submissions', {
      method: 'POST',
      body: { text: 'flag' },
      headers: { 'Idempotency-Key': 'fixed-key-for-retry-1234' },
    })

    expect(fetchMock.mock.calls[0][1].headers['Idempotency-Key']).toBe('fixed-key-for-retry-1234')
  })
})
describe('apiRequest empty-body handling', () => {
  it('treats a 201 with no body as success', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 201 }))
    vi.stubGlobal('fetch', fetchMock)
    const client = await loadClient()
    client.setAccessToken('access-token-value-123456')
    await expect(
      client.apiPost<void>('/admin/access-groups/x/members', { userUuid: 'y' }),
    ).resolves.toBeUndefined()
  })
})
