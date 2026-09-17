import { describe, expect, it } from 'vitest'
import { defaultFilters, eventQuery, readFilters, refinementCount, writeFilters, type Filters } from './filters'

const full: Filters = {
  range: { kind: 'custom', from: new Date('2026-09-01T00:00:00Z'), to: new Date('2026-09-01T06:00:00Z') },
  tenant: 'demoA',
  sources: ['aws', 'ad'],
  text: { q: '50% off', event_type: 'LogonFailed', action: 'login', user: 'demo\\eve', host: 'DC01', src_ip: '203.0.113.77' },
  severityMin: 0,
  severityMax: 7,
  tags: ['auth_failure', 'a b'],
}

describe('filters in the URL', () => {
  it('round-trips every filter', () => {
    expect(readFilters(writeFilters(full))).toEqual(full)
  })

  it('uses the API parameter names', () => {
    expect(writeFilters(full).toString()).toBe(
      'from=2026-09-01T00%3A00%3A00.000Z&to=2026-09-01T06%3A00%3A00.000Z&tenant=demoA&source=aws&source=ad' +
        '&q=50%25+off&event_type=LogonFailed&action=login&user=demo%5Ceve&host=DC01&src_ip=203.0.113.77' +
        '&severity_min=0&severity_max=7&tag=auth_failure&tag=a+b',
    )
  })

  it('leaves out the defaults', () => {
    expect(writeFilters(defaultFilters).toString()).toBe('')
    expect(readFilters(new URLSearchParams())).toEqual({ ...defaultFilters, severityMin: undefined, severityMax: undefined })
  })

  it('keeps a preset rather than its window', () => {
    const params = writeFilters({ ...defaultFilters, range: { kind: 'preset', preset: '1h' } })
    expect(params.toString()).toBe('range=1h')
    expect(readFilters(params).range).toEqual({ kind: 'preset', preset: '1h' })
  })

  it('drops values it cannot use', () => {
    const f = readFilters(
      new URLSearchParams(
        'range=1y&source=syslog&source=aws&source=aws&severity_min=high&severity_max=11&tag=%20&tag=x&tag=x&user=%20&from=yesterday&to=2026-09-01T00:00:00Z',
      ),
    )
    expect(f).toEqual({ ...defaultFilters, sources: ['aws'], tags: ['x'], severityMin: undefined, severityMax: undefined })
  })

  it('counts the refinements beyond time, tenant and source', () => {
    expect(refinementCount(full)).toBe(10)
    expect(refinementCount(defaultFilters)).toBe(0)
  })
})

describe('eventQuery', () => {
  it('works out a preset window at the time asked', () => {
    const q = eventQuery({ ...defaultFilters, range: { kind: 'preset', preset: '15m' } }, new Date('2026-09-17T10:05:30Z'))
    expect(q).toEqual({ from: '2026-09-17T09:51:00.000Z', to: '2026-09-17T10:06:00.000Z' })
  })

  it('passes every filter as the API names it, in UTC', () => {
    expect(eventQuery(full)).toEqual({
      from: '2026-09-01T00:00:00.000Z',
      to: '2026-09-01T06:00:00.000Z',
      tenant: 'demoA',
      source: ['aws', 'ad'],
      q: '50% off',
      event_type: 'LogonFailed',
      action: 'login',
      user: 'demo\\eve',
      host: 'DC01',
      src_ip: '203.0.113.77',
      severity_min: 0,
      severity_max: 7,
      tag: ['auth_failure', 'a b'],
    })
  })
})
