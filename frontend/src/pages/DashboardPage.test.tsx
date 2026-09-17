import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { session } from '../api/session'
import { adminSession, json, mockApi, requests, tenants, viewerSession } from '../test/api'
import { renderApp } from '../test/render'

const tops: Record<string, { value: string; count: number }[]> = {
  src_ip: [
    { value: '203.0.113.77', count: 42 },
    { value: '10.0.0.1', count: 7 },
  ],
  user: [{ value: 'demo\\eve', count: 5 }],
  event_type: [],
  host: [{ value: 'DC01', count: 12_345 }],
}

function dashboardApi() {
  mockApi({
    'GET /api/v1/tenants': () => json(tenants),
    'GET /api/v1/alerts': () =>
      json({
        items: [
          {
            id: '1',
            rule_id: '2',
            rule_name: 'Repeated failed logins',
            tenant: 'demoA',
            group_by: 'src_ip',
            group_key: '203.0.113.77',
            count: 6,
            window_start: '2026-09-17T10:00:00Z',
            window_end: '2026-09-17T10:05:00Z',
            created_at: '2026-09-17T10:05:31Z',
          },
        ],
      }),
    'GET /api/v1/events/top': (req) => {
      const field = new URL(req.url).searchParams.get('field')!
      return json({ field, from: '2026-09-16T10:00:00Z', to: '2026-09-17T10:00:00Z', items: tops[field] })
    },
    'GET /api/v1/events/timeline': () =>
      json({
        from: '2026-09-17T09:00:00Z',
        to: '2026-09-17T10:00:00Z',
        interval: '30m',
        buckets: [
          { start: '2026-09-17T09:00:00Z', count: 3 },
          { start: '2026-09-17T09:30:00Z', count: 1_000 },
        ],
      }),
    'GET /api/v1/events': () => json({ items: [] }),
  })
}

const countRequests = () => requests.filter((r) => /\/events\/(top|timeline)$/.test(new URL(r.url).pathname)).map((r) => new URL(r.url))

describe('the dashboard', () => {
  it('shows the top values, the total and recent alerts', async () => {
    dashboardApi()
    session.start(adminSession)
    renderApp('/')

    const ips = await screen.findByRole('region', { name: 'Top source IPs' })
    await within(ips).findByText('203.0.113.77')
    expect(
      within(ips)
        .getAllByRole('button')
        .map((b) => b.textContent),
    ).toEqual(['203.0.113.7742', '10.0.0.17'])
    expect(await screen.findByText('12,345')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Top event types' })).getByText('No event types in this range.')).toBeInTheDocument()
    expect(await screen.findByText('1,003 events · 30-minute buckets · select a bar to search it')).toBeInTheDocument()
    expect(await screen.findByText('Repeated failed logins')).toBeInTheDocument()

    // Each count asks about the same window, the last 24 hours to the next minute.
    const urls = countRequests()
    expect(urls.map((u) => u.searchParams.get('field') ?? 'timeline').sort()).toEqual(['event_type', 'host', 'src_ip', 'timeline', 'user'])
    const windows = new Set(urls.map((u) => `${u.searchParams.get('from')} ${u.searchParams.get('to')}`))
    expect(windows.size).toBe(1)
    const [from, to] = [...windows][0].split(' ').map(Date.parse)
    expect(to - from).toBe(24 * 3600_000)
    expect(to % 60_000).toBe(0)
  })

  it('searches for a value that is picked', async () => {
    dashboardApi()
    session.start(adminSession)
    const { router } = renderApp('/?range=1h&tenant=demoB&source=aws')
    const users = await screen.findByRole('region', { name: 'Top users' })
    await userEvent.click(await within(users).findByRole('button', { name: /demo\\eve/ }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/search'))
    expect(router.state.location.search).toBe('?range=1h&tenant=demoB&source=aws&user=demo%5Ceve')
  })

  it('counts with the filters chosen', async () => {
    dashboardApi()
    session.start(adminSession)
    const { router } = renderApp('/')
    await screen.findByRole('option', { name: 'Demo B (demoB)' })

    await userEvent.selectOptions(screen.getByLabelText('Tenant'), 'demoB')
    await userEvent.click(screen.getByRole('button', { name: 'AWS' }))
    await userEvent.click(screen.getByRole('button', { name: 'Active Directory' }))
    await userEvent.selectOptions(screen.getByLabelText(/Time range/), '7d')
    // Sources keep the order they are listed in.
    expect(router.state.location.search).toBe('?range=7d&tenant=demoB&source=aws&source=ad')

    await waitFor(() => {
      const last = countRequests().at(-1)!
      expect(last.searchParams.get('tenant')).toBe('demoB')
      expect(last.searchParams.getAll('source')).toEqual(['aws', 'ad'])
      expect(Date.parse(last.searchParams.get('to')!) - Date.parse(last.searchParams.get('from')!)).toBe(7 * 86_400_000)
    })
  })

  it("shows a viewer their tenant, and doesn't offer others", async () => {
    dashboardApi()
    session.start(viewerSession)
    renderApp('/')
    await screen.findByRole('region', { name: 'Top users' })
    expect(screen.queryByRole('combobox', { name: 'Tenant' })).toBeNull()
    expect(await screen.findByText('Demo A (demoA)')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Upload' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Search' })).toBeInTheDocument()
    expect(countRequests().every((u) => !u.searchParams.has('tenant'))).toBe(true)
  })

  it("tells a viewer once that another tenant's link can't be shown", async () => {
    dashboardApi()
    session.start(viewerSession)
    const { router } = renderApp('/?tenant=demoB&range=1h')
    expect(await screen.findByText(/This link asks for tenant demoB, and you can only see your own, demoA\./)).toHaveRole('status')
    expect(screen.queryByRole('region', { name: 'Top users' })).toBeNull()
    expect(countRequests()).toEqual([])

    await userEvent.click(screen.getByRole('button', { name: 'Show demoA' }))
    expect(router.state.location.search).toBe('?range=1h')
    await screen.findByRole('region', { name: 'Top users' })
    await waitFor(() => expect(countRequests()).toHaveLength(5))
  })

  it('carries its filters to search, and only there', async () => {
    dashboardApi()
    session.start(adminSession)
    const { router } = renderApp('/?range=1h&tenant=demoB')
    const nav = await screen.findByRole('navigation', { name: 'Main' })
    expect(within(nav).getByRole('link', { name: 'Search' })).toHaveAttribute('href', '/search?range=1h&tenant=demoB')
    expect(within(nav).getByRole('link', { name: 'Alerts' })).toHaveAttribute('href', '/alerts')
    await router.navigate('/alerts?tenant=demoA')
    await waitFor(() => expect(within(nav).getByRole('link', { name: 'Dashboard' })).toHaveAttribute('href', '/'))
  })

  it('offers an admin every tenant and the upload page', async () => {
    dashboardApi()
    session.start(adminSession)
    renderApp('/')
    const picker = await screen.findByRole('combobox', { name: 'Tenant' })
    await waitFor(() =>
      expect(
        within(picker)
          .getAllByRole('option')
          .map((o) => o.textContent),
      ).toEqual(['All tenants', 'Demo A (demoA)', 'Demo B (demoB)']),
    )
    expect(screen.getByRole('link', { name: 'Upload' })).toBeInTheDocument()
  })
})
