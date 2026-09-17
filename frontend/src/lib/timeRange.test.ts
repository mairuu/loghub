import { describe, expect, it } from 'vitest'
import { windowOf } from './timeRange'

describe('windowOf', () => {
  it('ends a preset at the start of the next minute', () => {
    const w = windowOf({ kind: 'preset', preset: '1h' }, new Date('2026-09-17T10:05:30.250Z'))
    expect(w.to.toISOString()).toBe('2026-09-17T10:06:00.000Z')
    expect(w.from.toISOString()).toBe('2026-09-17T09:06:00.000Z')
  })

  it('moves on at a whole minute, not before', () => {
    const at = (t: string) => windowOf({ kind: 'preset', preset: '15m' }, new Date(t)).to.toISOString()
    expect(at('2026-09-17T10:05:59.999Z')).toBe('2026-09-17T10:06:00.000Z')
    expect(at('2026-09-17T10:06:00.000Z')).toBe('2026-09-17T10:07:00.000Z')
  })

  it('reaches back as far as each preset says', () => {
    const now = new Date('2026-09-17T10:05:00Z')
    const days = (preset: '24h' | '7d') => {
      const w = windowOf({ kind: 'preset', preset }, now)
      return (w.to.getTime() - w.from.getTime()) / 86_400_000
    }
    expect(days('24h')).toBe(1)
    expect(days('7d')).toBe(7)
  })

  it('leaves a custom range as it is', () => {
    const from = new Date('2026-09-01T00:00:00Z')
    const to = new Date('2026-09-02T00:00:00Z')
    expect(windowOf({ kind: 'custom', from, to }, new Date())).toEqual({ from, to })
  })
})
