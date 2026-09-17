import { isSource, type Source } from '../api/enums'
import type { paths } from '../api/schema'
import { defaultPreset, isPreset, windowOf, type TimeRange } from './timeRange'

/** The query parameters every event endpoint shares: search, top values and the timeline. */
export type EventQuery = Omit<NonNullable<paths['/api/v1/events']['get']['parameters']['query']>, 'limit' | 'cursor' | 'order'>

/**
 * What the dashboard and search are showing. Both pages keep it in the URL
 * under the API's own parameter names, so a link carries it from one to the
 * other, and a reload or a shared link shows the same thing.
 */
export interface Filters {
  range: TimeRange
  /** Empty for every tenant the caller may read. */
  tenant: string
  /** Empty for every source. */
  sources: Source[]
  text: TextFilters
  severityMin?: number
  severityMax?: number
  tags: string[]
}

export const textFilters = [
  { key: 'q', label: 'Text' },
  { key: 'event_type', label: 'Event type' },
  { key: 'action', label: 'Action' },
  { key: 'user', label: 'User' },
  { key: 'host', label: 'Host' },
  { key: 'src_ip', label: 'Source IP' },
] as const

export type TextFilter = (typeof textFilters)[number]['key']
export type TextFilters = Partial<Record<TextFilter, string>>

export const defaultFilters: Filters = {
  range: { kind: 'preset', preset: defaultPreset },
  tenant: '',
  sources: [],
  text: {},
  tags: [],
}

function readSeverity(value: string | null): number | undefined {
  if (value === null || value.trim() === '') return undefined
  const n = Number(value)
  return Number.isInteger(n) && n >= 0 && n <= 10 ? n : undefined
}

function readRange(params: URLSearchParams): TimeRange {
  const from = new Date(params.get('from') ?? '')
  const to = new Date(params.get('to') ?? '')
  if (!Number.isNaN(from.getTime()) && !Number.isNaN(to.getTime())) return { kind: 'custom', from, to }
  const preset = params.get('range') ?? ''
  return { kind: 'preset', preset: isPreset(preset) ? preset : defaultPreset }
}

/** The filters a URL describes. Values the UI can't show are dropped rather than refused. */
export function readFilters(params: URLSearchParams): Filters {
  const text: TextFilters = {}
  for (const { key } of textFilters) {
    const value = params.get(key)?.trim()
    if (value) text[key] = value
  }
  return {
    range: readRange(params),
    tenant: params.get('tenant')?.trim() ?? '',
    sources: [...new Set(params.getAll('source').filter(isSource))],
    text,
    severityMin: readSeverity(params.get('severity_min')),
    severityMax: readSeverity(params.get('severity_max')),
    tags: [
      ...new Set(
        params
          .getAll('tag')
          .map((t) => t.trim())
          .filter(Boolean),
      ),
    ],
  }
}

/** The URL parameters for filters, leaving out what is the default. */
export function writeFilters(f: Filters): URLSearchParams {
  const params = new URLSearchParams()
  if (f.range.kind === 'custom') {
    params.set('from', f.range.from.toISOString())
    params.set('to', f.range.to.toISOString())
  } else if (f.range.preset !== defaultPreset) {
    params.set('range', f.range.preset)
  }
  if (f.tenant) params.set('tenant', f.tenant)
  f.sources.forEach((s) => params.append('source', s))
  for (const { key } of textFilters) {
    const value = f.text[key]?.trim()
    if (value) params.set(key, value)
  }
  if (f.severityMin !== undefined) params.set('severity_min', String(f.severityMin))
  if (f.severityMax !== undefined) params.set('severity_max', String(f.severityMax))
  f.tags.forEach((t) => params.append('tag', t))
  return params
}

/** The API query for filters, with a preset's window worked out at now. */
export function eventQuery(f: Filters, now: Date = new Date()): EventQuery {
  const { from, to } = windowOf(f.range, now)
  const q: EventQuery = { from: from.toISOString(), to: to.toISOString() }
  if (f.tenant) q.tenant = f.tenant
  if (f.sources.length) q.source = f.sources
  for (const { key } of textFilters) {
    const value = f.text[key]?.trim()
    if (value) q[key] = value
  }
  if (f.severityMin !== undefined) q.severity_min = f.severityMin
  if (f.severityMax !== undefined) q.severity_max = f.severityMax
  if (f.tags.length) q.tag = f.tags
  return q
}

/** How many filters beyond time, tenant and source are set. */
export function refinementCount(f: Filters): number {
  return (
    Object.values(f.text).filter(Boolean).length +
    (f.severityMin !== undefined ? 1 : 0) +
    (f.severityMax !== undefined ? 1 : 0) +
    f.tags.length
  )
}
