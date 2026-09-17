import { describe, expect, it } from 'vitest'
import type { Schemas } from '../api/client'
import { alertSearch } from './alertSearch'

const alert: Schemas['Alert'] = {
  id: '301',
  rule_id: '12',
  rule_name: 'Repeated failed logins',
  tenant: 'demoA',
  group_by: 'src_ip',
  group_key: '203.0.113.77',
  count: 5,
  window_start: '2026-09-17T10:00:00Z',
  window_end: '2026-09-17T10:05:00Z',
  created_at: '2026-09-17T10:05:31Z',
}

const rule: Schemas['AlertRule'] = {
  id: '12',
  tenant: 'demoA',
  name: 'Repeated failed logins',
  source: 'ad',
  action: 'login',
  severity_min: 3,
  tags: ['auth_failure'],
  group_by: 'src_ip',
  threshold: 5,
  window_minutes: 5,
  cooldown_minutes: 5,
  created_at: '2026-09-17T09:00:00Z',
}

describe('alertSearch', () => {
  it("searches the rule's filter, the group and the window", () => {
    expect(alertSearch(alert, rule)).toBe(
      'from=2026-09-17T10%3A00%3A00.000Z&to=2026-09-17T10%3A05%3A00.000Z&tenant=demoA&source=ad' +
        '&action=login&src_ip=203.0.113.77&severity_min=3&tag=auth_failure',
    )
  })

  it('leaves out a destination address, which search cannot filter on', () => {
    const search = new URLSearchParams(alertSearch({ ...alert, group_by: 'dst_ip' }, { ...rule, group_by: 'dst_ip' }))
    expect(search.has('src_ip')).toBe(false)
    expect(search.get('tag')).toBe('auth_failure')
  })

  it('still searches the group and window without the rule', () => {
    expect(alertSearch({ ...alert, group_by: 'user', group_key: 'eve' }, undefined)).toBe(
      'from=2026-09-17T10%3A00%3A00.000Z&to=2026-09-17T10%3A05%3A00.000Z&tenant=demoA&user=eve',
    )
  })
})
