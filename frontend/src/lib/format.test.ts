import { describe, expect, it } from 'vitest'
import { formatAgo, formatDateTime, fromLocalInput, toLocalInput, zoneLabel } from './format'

// vite.config.ts runs the tests in Asia/Bangkok, UTC+07:00.
describe('times', () => {
  it('are shown in the browser zone', () => {
    expect(formatDateTime('2026-09-17T20:05:03Z')).toBe('2026-09-18 03:05:03')
    expect(formatDateTime('2026-09-17T20:05:03Z', false)).toBe('2026-09-18 03:05')
    expect(zoneLabel()).toBe('UTC+07:00')
  })

  it('round-trip through a datetime-local input in local time', () => {
    const d = new Date('2026-09-17T20:05:00Z')
    expect(toLocalInput(d)).toBe('2026-09-18T03:05')
    expect(fromLocalInput('2026-09-18T03:05')?.toISOString()).toBe('2026-09-17T20:05:00.000Z')
  })

  it('refuse input that is not a date-time', () => {
    expect(fromLocalInput('')).toBeNull()
    expect(fromLocalInput('2026-09-18')).toBeNull()
    expect(fromLocalInput('2026-13-40T99:99')).toBeNull()
  })

  it('say roughly how long ago', () => {
    const now = new Date('2026-09-17T12:00:00Z')
    expect(formatAgo('2026-09-17T11:59:30Z', now)).toBe('just now')
    expect(formatAgo('2026-09-17T11:57:00Z', now)).toBe('3 min ago')
    expect(formatAgo('2026-09-17T09:00:00Z', now)).toBe('3 h ago')
    expect(formatAgo('2026-09-15T12:00:00Z', now)).toBe('2 d ago')
  })
})
