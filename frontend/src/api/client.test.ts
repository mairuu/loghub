import { describe, expect, it } from 'vitest'
import { apiError, json, mockApi, requests, viewerSession } from '../test/api'
import { api, ApiError, unwrap } from './client'
import { session } from './session'

describe('the API client', () => {
  it('sends the session token', async () => {
    mockApi({ 'GET /api/v1/tenants': () => json({ items: [] }) })
    await unwrap(api.GET('/api/v1/tenants'))
    expect(requests[0].headers.get('Authorization')).toBeNull()

    session.start(viewerSession)
    await unwrap(api.GET('/api/v1/tenants'))
    expect(requests[1].headers.get('Authorization')).toBe('Bearer viewer-token')
  })

  it('repeats array parameters and escapes the rest', async () => {
    mockApi({ 'GET /api/v1/events': () => json({ items: [] }) })
    await unwrap(
      api.GET('/api/v1/events', {
        params: { query: { source: ['aws', 'ad'], tag: ['a b'], q: '50%+x', from: '2026-09-01T00:00:00+07:00', tenant: undefined } },
      }),
    )
    const url = new URL(requests[0].url)
    expect(url.searchParams.getAll('source')).toEqual(['aws', 'ad'])
    expect(url.searchParams.getAll('tag')).toEqual(['a b'])
    expect(url.searchParams.get('q')).toBe('50%+x')
    // An unescaped + would reach the server as a space.
    expect(url.searchParams.get('from')).toBe('2026-09-01T00:00:00+07:00')
    expect(url.searchParams.has('tenant')).toBe(false)
  })

  it('reports the error envelope', async () => {
    mockApi({
      'POST /api/v1/alert-rules': () =>
        apiError(422, 'invalid_request', 'the request contains invalid fields', {
          errors: [{ field: 'threshold', code: 'out_of_range', message: 'threshold must be an integer of at least 1' }],
        }),
    })
    const err = await unwrap(
      api.POST('/api/v1/alert-rules', { body: { tenant: 'demoA', name: 'x', group_by: 'user', threshold: 0, window_minutes: 5 } }),
    ).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({
      status: 422,
      code: 'invalid_request',
      message: 'the request contains invalid fields',
      requestId: 'req-1',
      fieldErrors: [{ field: 'threshold', code: 'out_of_range', message: 'threshold must be an integer of at least 1' }],
    })
  })

  it('reports an answer without the envelope', async () => {
    mockApi({})
    const err = await unwrap(api.GET('/api/v1/tenants')).catch((e: unknown) => e)
    expect(err).toMatchObject({ status: 404, code: 'unexpected_response', message: 'The server answered 404' })
  })

  it('ends a session whose token is refused', async () => {
    mockApi({ 'GET /api/v1/tenants': () => apiError(401, 'invalid_token', 'the token is invalid or has expired; sign in again') })
    session.start(viewerSession)
    await expect(unwrap(api.GET('/api/v1/tenants'))).rejects.toMatchObject({ code: 'invalid_token' })
    expect(session.get()).toBeNull()
    expect(session.endedBecause()).toBe('refused')
    expect(sessionStorage.length).toBe(0)
  })
})

describe('the session', () => {
  it('ends when its token expires', () => {
    session.start({ ...viewerSession, expires_at: new Date(Date.now() - 1000).toISOString() })
    expect(session.snapshot()).not.toBeNull()
    expect(session.get()).toBeNull()
    expect(session.endedBecause()).toBe('expired')
  })

  it('is kept for the tab', () => {
    session.start(viewerSession)
    expect(JSON.parse(sessionStorage.getItem('loghub.session')!)).toEqual(viewerSession)
    session.end('signed_out')
    expect(sessionStorage.getItem('loghub.session')).toBeNull()
  })
})
