const minute = 60_000

// Events are kept for 7 days, so no preset reaches further back.
export const rangePresets = [
  { id: '15m', label: 'Last 15 minutes', minutes: 15 },
  { id: '1h', label: 'Last hour', minutes: 60 },
  { id: '6h', label: 'Last 6 hours', minutes: 6 * 60 },
  { id: '24h', label: 'Last 24 hours', minutes: 24 * 60 },
  { id: '7d', label: 'Last 7 days', minutes: 7 * 24 * 60 },
] as const

export type PresetId = (typeof rangePresets)[number]['id']
export const defaultPreset: PresetId = '24h'

export type TimeRange = { kind: 'preset'; preset: PresetId } | { kind: 'custom'; from: Date; to: Date }

export interface Window {
  from: Date
  to: Date
}

export function isPreset(id: string): id is PresetId {
  return rangePresets.some((p) => p.id === id)
}

/**
 * The window a range covers at now. A preset ends at the start of the next
 * minute, so every request within a minute asks about the same window, and an
 * event stamped a few seconds ahead of this clock still shows.
 */
export function windowOf(range: TimeRange, now: Date = new Date()): Window {
  if (range.kind === 'custom') return { from: range.from, to: range.to }
  const { minutes } = rangePresets.find((p) => p.id === range.preset)!
  const to = Math.floor(now.getTime() / minute) * minute + minute
  return { from: new Date(to - minutes * minute), to: new Date(to) }
}

export function describeRange(range: TimeRange): string {
  if (range.kind === 'preset') return rangePresets.find((p) => p.id === range.preset)!.label
  return 'Custom range'
}
