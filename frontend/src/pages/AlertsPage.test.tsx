import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '../api/client'
import { session } from '../api/session'
import { adminSession, apiError, json, mockApi, requests, tenants, viewerSession } from '../test/api'
import { renderApp } from '../test/render'

const rule: Schemas['AlertRule'] = {
  id: '12',
  tenant: 'demoA',
  name: 'Repeated failed logins',
  tags: ['auth_failure'],
  group_by: 'src_ip',
  threshold: 5,
  window_minutes: 5,
  cooldown_minutes: 5,
  webhook_url: 'https://hooks.example.com/secret-path?token=abc',
  created_at: '2026-09-17T09:00:00Z',
}

const alert: Schemas['Alert'] = {
  id: '301',
  rule_id: '12',
  rule_name: 'Repeated failed logins',
  tenant: 'demoA',
  group_by: 'src_ip',
  group_key: '203.0.113.77',
  count: 6,
  window_start: '2026-09-17T10:00:00Z',
  window_end: '2026-09-17T10:05:00Z',
  created_at: '2026-09-17T10:05:31Z',
}

describe('alerts', () => {
  it('lists alerts with a search for their events, and rules without their webhook secrets', async () => {
    mockApi({
      'GET /api/v1/tenants': () => json(tenants),
      'GET /api/v1/alerts': () => json({ items: [alert] }),
      'GET /api/v1/alert-rules': () => json({ items: [rule] }),
    })
    session.start(adminSession)
    renderApp('/alerts')

    const raised = await screen.findByRole('region', { name: 'Raised alerts' })
    const link = await within(raised).findByRole('link', { name: 'View events' })
    await waitFor(() => expect(link).toHaveAttribute('href', expect.stringContaining('tag=auth_failure')))
    expect(link.getAttribute('href')).toBe(
      '/search?from=2026-09-17T10%3A00%3A00.000Z&to=2026-09-17T10%3A05%3A00.000Z&tenant=demoA&src_ip=203.0.113.77&tag=auth_failure',
    )
    expect(within(raised).getByText('2026-09-17 17:00–17:05')).toBeInTheDocument()

    const rules = screen.getByRole('region', { name: 'Rules' })
    expect(await within(rules).findByText('hooks.example.com')).toBeInTheDocument()
    expect(rules).not.toHaveTextContent('secret-path')
    expect(within(rules).getByText('5 from one source IP within 5 min')).toBeInTheDocument()
  })

  it('marks new alerts in the navigation until they are seen', async () => {
    mockApi({
      'GET /api/v1/tenants': () => json(tenants),
      'GET /api/v1/alerts': () => json({ items: [alert] }),
      'GET /api/v1/alert-rules': () => json({ items: [] }),
      'GET /api/v1/events/top': () => json({ field: 'user', from: alert.window_start, to: alert.window_end, items: [] }),
      'GET /api/v1/events/timeline': () => json({ from: alert.window_start, to: alert.window_end, interval: '1m', buckets: [] }),
    })
    session.start(viewerSession)
    // Seen on an earlier visit: the alert is newer.
    localStorage.setItem('loghub.alertsSeen.demoA', '2026-09-17T10:05:30Z')
    const { router } = renderApp('/')
    expect(await screen.findByRole('link', { name: 'Alerts, new alerts' })).toBeInTheDocument()
    await router.navigate('/alerts')
    await waitFor(() => expect(screen.getByRole('link', { name: 'Alerts' })).toBeInTheDocument())
    expect(localStorage.getItem('loghub.alertsSeen.demoA')).toBe(alert.created_at)
    await router.navigate('/')
    await screen.findByRole('link', { name: 'Search' })
    expect(screen.queryByRole('link', { name: 'Alerts, new alerts' })).toBeNull()
    // A viewer can't add rules.
    expect(screen.queryByRole('button', { name: 'New rule' })).toBeNull()
  })

  it('lets an admin add a rule, and shows what the API refused', async () => {
    const created: unknown[] = []
    mockApi({
      'GET /api/v1/tenants': () => json(tenants),
      'GET /api/v1/alerts': () => json({ items: [] }),
      'GET /api/v1/alert-rules': () => json({ items: [] }),
      'POST /api/v1/alert-rules': async (req) => {
        const body = await req.json()
        created.push(body)
        if (body.threshold > 100) {
          return apiError(422, 'invalid_request', 'the request contains invalid fields', {
            errors: [{ field: 'threshold', code: 'out_of_range', message: 'threshold is too high for this demo' }],
          })
        }
        return json({ ...rule, ...body, id: '13', cooldown_minutes: body.window_minutes, created_at: '2026-09-17T11:00:00Z' }, 201)
      },
    })
    session.start(adminSession)
    renderApp('/alerts?tenant=demoB')

    await userEvent.click(await screen.findByRole('button', { name: 'New rule' }))
    const form = screen.getByRole('form', { name: 'New alert rule' })
    await userEvent.click(within(form).getByRole('button', { name: 'Fill in the failed-login example' }))
    await userEvent.clear(within(form).getByLabelText('Reach'))
    await userEvent.type(within(form).getByLabelText('Reach'), '500')
    await userEvent.click(within(form).getByRole('button', { name: 'Add rule' }))

    expect(await within(form).findByText('threshold is too high for this demo')).toBeInTheDocument()
    expect(within(form).getByLabelText('Reach')).toHaveAttribute('aria-invalid', 'true')
    expect(created[0]).toEqual({
      tenant: 'demoB',
      name: 'Repeated failed logins from one address',
      tags: ['auth_failure'],
      group_by: 'src_ip',
      threshold: 500,
      window_minutes: 5,
    })

    await userEvent.clear(within(form).getByLabelText('Reach'))
    await userEvent.type(within(form).getByLabelText('Reach'), '5')
    await userEvent.selectOptions(within(form).getByLabelText('Events from one'), 'user')
    await userEvent.type(within(form).getByLabelText('Webhook URL (optional)'), 'https://hooks.example.com/x')
    await userEvent.click(within(form).getByRole('button', { name: 'Add rule' }))

    expect(await screen.findByText(/Rule “Repeated failed logins from one address” was added for demoB/)).toBeInTheDocument()
    expect(screen.queryByRole('form', { name: 'New alert rule' })).toBeNull()
    expect(created[1]).toMatchObject({ group_by: 'user', threshold: 5, webhook_url: 'https://hooks.example.com/x' })
    // The list is fetched again to show the new rule.
    await waitFor(() => expect(requests.filter((r) => r.url.includes('/api/v1/alert-rules') && r.method === 'GET')).toHaveLength(2))
  })
})
