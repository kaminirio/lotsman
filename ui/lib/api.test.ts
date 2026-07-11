import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  ApiError,
  apiFetch,
  listIncidents,
  listPods,
  getPodLogs,
  listEvents,
  getRbacEffective,
  explainIncident,
} from './api'

// Capture the (url, init) each endpoint hands to fetch so we can assert the
// URL/query building without a live control plane.
type FetchArgs = [string, RequestInit | undefined]

function mockOk(body: unknown, status = 200) {
  return vi.fn(async () => ({
    ok: true,
    status,
    statusText: 'OK',
    json: async () => body,
  })) as unknown as typeof fetch
}

function lastUrl(fetchMock: ReturnType<typeof vi.fn>): string {
  const call = fetchMock.mock.calls.at(-1) as FetchArgs
  return call[0]
}

beforeEach(() => {
  vi.restoreAllMocks()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('endpoint URL building', () => {
  it('lists incidents at the collection path', async () => {
    const f = mockOk([]) as unknown as ReturnType<typeof vi.fn>
    vi.stubGlobal('fetch', f)
    await listIncidents()
    expect(lastUrl(f)).toBe('/api/v1/incidents')
  })

  it('encodes cluster/namespace and appends the workload query', async () => {
    const f = mockOk([]) as unknown as ReturnType<typeof vi.fn>
    vi.stubGlobal('fetch', f)
    await listPods('prod', 'kube-system', 'core dns')
    expect(lastUrl(f)).toBe(
      '/api/v1/clusters/prod/namespaces/kube-system/pods?workload=core%20dns',
    )
  })

  it('omits the workload query when no workload is given', async () => {
    const f = mockOk([]) as unknown as ReturnType<typeof vi.fn>
    vi.stubGlobal('fetch', f)
    await listPods('prod', 'demo')
    expect(lastUrl(f)).toBe('/api/v1/clusters/prod/namespaces/demo/pods')
  })

  it('builds the pod-logs query with container and tail', async () => {
    const f = mockOk({}) as unknown as ReturnType<typeof vi.fn>
    vi.stubGlobal('fetch', f)
    await getPodLogs('prod', 'demo', 'api-0', 'app', 200)
    expect(lastUrl(f)).toBe(
      '/api/v1/clusters/prod/namespaces/demo/pods/api-0/logs?container=app&tail=200',
    )
  })

  it('applies default since/limit for events', async () => {
    const f = mockOk([]) as unknown as ReturnType<typeof vi.fn>
    vi.stubGlobal('fetch', f)
    await listEvents('prod', '_all')
    expect(lastUrl(f)).toBe(
      '/api/v1/clusters/prod/namespaces/_all/events?since=60m&limit=200',
    )
  })

  it('encodes the user query param for effective RBAC', async () => {
    const f = mockOk({}) as unknown as ReturnType<typeof vi.fn>
    vi.stubGlobal('fetch', f)
    await getRbacEffective('octo cat')
    expect(lastUrl(f)).toBe('/api/v1/admin/rbac/effective?user=octo+cat')
  })

  it('encodes the incident id and POSTs the explain endpoint', async () => {
    const f = mockOk({}) as unknown as ReturnType<typeof vi.fn>
    vi.stubGlobal('fetch', f)
    await explainIncident('inc/1')
    const call = f.mock.calls.at(-1) as FetchArgs
    expect(call[0]).toBe('/api/v1/incidents/inc%2F1/explain')
    expect(call[1]?.method).toBe('POST')
  })
})

describe('apiFetch behavior', () => {
  it('sends the CSRF header and JSON content type', async () => {
    const f = mockOk([]) as unknown as ReturnType<typeof vi.fn>
    vi.stubGlobal('fetch', f)
    await apiFetch('/api/v1/incidents')
    const call = f.mock.calls.at(-1) as FetchArgs
    const headers = call[1]?.headers as Record<string, string>
    expect(headers['X-Requested-With']).toBe('lotsman')
    expect(headers['Content-Type']).toBe('application/json')
    expect(call[1]?.credentials).toBe('include')
  })

  it('throws an ApiError carrying status and the server error message', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        json: async () => ({ error: 'boom' }),
      })),
    )
    await expect(apiFetch('/api/v1/incidents')).rejects.toMatchObject({
      status: 500,
      message: 'boom',
    })
    await expect(apiFetch('/api/v1/incidents')).rejects.toBeInstanceOf(ApiError)
  })

  it('returns undefined for a 204 No Content response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, status: 204, statusText: 'No Content', json: async () => ({}) })),
    )
    await expect(apiFetch('/api/v1/incidents')).resolves.toBeUndefined()
  })
})
