import { describe, expect, it } from 'vitest'
import { session } from './api/session'
import { ApiError } from './api/client'
import { clearOnSignOut, newQueryClient } from './queryClient'
import { adminSession } from './test/api'

describe('the query client', () => {
  it("forgets one user's data when their session ends", () => {
    const client = newQueryClient()
    const stop = clearOnSignOut(client)
    session.start(adminSession)
    client.setQueryData(['tenants'], { items: [{ id: 'demoA', name: 'Demo A' }] })
    expect(client.getQueryData(['tenants'])).toBeDefined()
    session.end('signed_out')
    expect(client.getQueryData(['tenants'])).toBeUndefined()
    stop()
  })

  it('retries only what may succeed next time', () => {
    const retry = newQueryClient().getDefaultOptions().queries!.retry as (failures: number, error: Error) => boolean
    const refusal = new ApiError(403, { code: 'tenant_not_permitted', message: '' }, '')
    const timeout = new ApiError(503, { code: 'search_timeout', message: '' }, '')
    const down = new ApiError(503, { code: 'database_unavailable', message: '' }, '')
    expect(retry(0, refusal)).toBe(false)
    expect(retry(0, timeout)).toBe(false)
    expect(retry(0, down)).toBe(true)
    expect(retry(0, new TypeError('Failed to fetch'))).toBe(true)
    expect(retry(2, down)).toBe(false)
  })
})
