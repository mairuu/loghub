import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '../api/client'
import { session } from '../api/session'
import { adminSession, apiError, json, mockApi, requests, tenants, viewerSession } from '../test/api'
import { renderApp } from '../test/render'

function event(id: string, extra: Partial<Schemas['Event']> = {}): Schemas['Event'] {
  return {
    id,
    '@timestamp': '2026-09-17T03:04:05Z',
    received_at: '2026-09-17T03:04:06Z',
    tenant: 'demoA',
    source: 'ad',
    event_type: 'LogonFailed',
    action: 'login',
    severity: 8,
    src_ip: '203.0.113.77',
    user: 'demo\\eve',
    host: 'DC01',
    raw: { event_id: 4625, marker: `raw-${id}` },
    _tags: ['auth_failure'],
    ...extra,
  }
}

const searches = () => requests.filter((r) => new URL(r.url).pathname === '/api/v1/events').map((r) => new URL(r.url).searchParams)

describe('search', () => {
  it('lists events and loads the next page with the cursor alone', async () => {
    mockApi({
      'GET /api/v1/tenants': () => json(tenants),
      'GET /api/v1/alerts': () => json({ items: [] }),
      'GET /api/v1/events': (req) =>
        new URL(req.url).searchParams.get('cursor') === 'next'
          ? json({ items: [event('2', { event_type: 'Second page' })] })
          : json({ items: [event('1')], next_cursor: 'next' }),
    })
    session.start(adminSession)
    renderApp('/search?q=eve&source=ad')

    const table = await screen.findByRole('table')
    // An admin searching every tenant sees which tenant each event is from.
    expect(within(table).getByRole('columnheader', { name: 'Tenant' })).toBeInTheDocument()
    expect(within(table).getByText('2026-09-17')).toBeInTheDocument()
    expect(within(table).getByText('10:04:05')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
    await within(table).findByText('Second page')
    const [first, second] = searches()
    expect(first.get('q')).toBe('eve')
    expect(first.getAll('source')).toEqual(['ad'])
    expect(first.has('from') && first.has('to')).toBe(true)
    expect(Object.fromEntries(second)).toEqual({ q: 'eve', source: 'ad', cursor: 'next', limit: '50' })
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
  })

  it('shows an event in full, and narrows the search by its values', async () => {
    mockApi({
      'GET /api/v1/tenants': () => json(tenants),
      'GET /api/v1/alerts': () => json({ items: [] }),
      'GET /api/v1/events': () => json({ items: [event('1')] }),
    })
    session.start(viewerSession)
    const { router } = renderApp('/search')
    const table = await screen.findByRole('table')
    expect(within(table).queryByRole('columnheader', { name: 'Tenant' })).toBeNull()

    await userEvent.click(within(table).getByRole('button', { name: /Show details of the LogonFailed event/ }))
    expect(within(table).getByText(/"marker": "raw-1"/)).toBeInTheDocument()

    await userEvent.click(within(table).getByRole('button', { name: 'Search for Source IP: 203.0.113.77' }))
    await waitFor(() => expect(router.state.location.search).toBe('?src_ip=203.0.113.77'))
    await userEvent.click(await screen.findByRole('button', { name: /Show details/ }))
    await userEvent.click(screen.getByRole('button', { name: 'auth_failure' }))
    await waitFor(() => expect(router.state.location.search).toBe('?src_ip=203.0.113.77&tag=auth_failure'))
    expect(screen.getByRole('button', { name: 'Remove Tag: auth_failure' })).toBeInTheDocument()
  })

  it('applies the refinements typed in', async () => {
    mockApi({
      'GET /api/v1/tenants': () => json(tenants),
      'GET /api/v1/alerts': () => json({ items: [] }),
      'GET /api/v1/events': () => json({ items: [] }),
    })
    session.start(adminSession)
    const { router } = renderApp('/search')
    await screen.findByText('No events match. Try a longer time range or fewer filters.')

    await userEvent.click(screen.getByRole('button', { name: 'More filters' }))
    await userEvent.type(screen.getByLabelText('Host'), 'DC01')
    await userEvent.type(screen.getByLabelText('Tags, all required'), 'a, b,,a')
    await userEvent.type(screen.getByLabelText('Minimum severity'), '9')
    await userEvent.type(screen.getByLabelText('Maximum severity'), '3')
    expect(screen.getByText('The minimum is above the maximum.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    expect(router.state.location.search).toBe('')

    await userEvent.clear(screen.getByLabelText('Maximum severity'))
    await userEvent.type(screen.getByRole('searchbox'), 'needle')
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() => expect(router.state.location.search).toBe('?q=needle&host=DC01&severity_min=9&tag=a&tag=b'))
    await waitFor(() => expect(searches().at(-1)?.get('host')).toBe('DC01'))

    await userEvent.click(screen.getByRole('button', { name: 'Clear all' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
  })

  it('explains a filter the server refuses', async () => {
    mockApi({
      'GET /api/v1/tenants': () => json(tenants),
      'GET /api/v1/alerts': () => json({ items: [] }),
      'GET /api/v1/events': () => apiError(400, 'invalid_parameter', 'src_ip must be an IP address'),
    })
    session.start(adminSession)
    renderApp('/search?src_ip=nope')
    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't search. src_ip must be an IP address")
  })
})
