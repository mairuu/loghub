import { useId, useState } from 'react'
import { sourceLabels, sources, type Source } from '../api/enums'
import { useTenants } from '../api/queries'
import { useSession } from '../api/session'
import type { Filters } from '../lib/filters'
import { formatTimeOfDay, fromLocalInput, toLocalInput, zoneLabel } from '../lib/format'
import { isPreset, rangePresets, windowOf, type TimeRange } from '../lib/timeRange'
import { cx } from '../lib/classes'
import { Button, inputClass } from './ui'

interface Props {
  filters: Filters
  onChange: (f: Filters) => void
  onRefresh?: () => void
  refreshing?: boolean
  /** When the data shown was fetched, in milliseconds since the epoch. */
  updatedAt?: number
}

/** Time range, tenant and sources: the filters the dashboard and search share. */
export function FilterBar({ filters, onChange, onRefresh, refreshing, updatedAt }: Props) {
  return (
    <div className="flex flex-wrap items-end gap-x-4 gap-y-3 rounded-lg border border-line bg-surface p-3">
      <RangePicker
        // A new range from outside resets what is being typed.
        key={JSON.stringify(filters.range)}
        range={filters.range}
        onChange={(range) => onChange({ ...filters, range })}
      />
      <TenantPicker value={filters.tenant} onChange={(tenant) => onChange({ ...filters, tenant })} />
      <SourcePicker value={filters.sources} onChange={(s) => onChange({ ...filters, sources: s })} />
      {onRefresh && (
        <div className="ml-auto flex items-center gap-2">
          {updatedAt ? <span className="text-xs text-muted">Updated {formatTimeOfDay(new Date(updatedAt))}</span> : null}
          <Button onClick={onRefresh} disabled={refreshing} aria-label="Refresh now">
            <span aria-hidden className={cx('inline-block', refreshing && 'animate-spin')}>
              ↻
            </span>
            Refresh
          </Button>
        </div>
      )}
    </div>
  )
}

function RangePicker({ range, onChange }: { range: TimeRange; onChange: (r: TimeRange) => void }) {
  const id = useId()
  const [draft, setDraft] = useState(() => {
    const w = windowOf(range)
    return { from: toLocalInput(w.from), to: toLocalInput(w.to) }
  })
  const [custom, setCustom] = useState(range.kind === 'custom')
  const from = fromLocalInput(draft.from)
  const to = fromLocalInput(draft.to)
  const problem = !from || !to ? 'Enter both times.' : from >= to ? 'From must be before To.' : ''

  return (
    <div className="flex flex-wrap items-end gap-2">
      <div className="space-y-1">
        <label htmlFor={`${id}-range`} className="block text-xs font-medium text-muted">
          Time range <span className="font-normal">({zoneLabel()})</span>
        </label>
        <select
          id={`${id}-range`}
          className={cx(inputClass, 'w-44')}
          value={custom ? 'custom' : range.kind === 'preset' ? range.preset : 'custom'}
          onChange={(e) => {
            const value = e.target.value
            if (isPreset(value)) {
              setCustom(false)
              onChange({ kind: 'preset', preset: value })
            } else {
              setCustom(true)
            }
          }}
        >
          {rangePresets.map((p) => (
            <option key={p.id} value={p.id}>
              {p.label}
            </option>
          ))}
          <option value="custom">Custom range…</option>
        </select>
      </div>
      {custom && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (from && to && !problem) onChange({ kind: 'custom', from, to })
          }}
        >
          <div className="space-y-1">
            <label htmlFor={`${id}-from`} className="block text-xs font-medium text-muted">
              From
            </label>
            <input
              id={`${id}-from`}
              type="datetime-local"
              className={inputClass}
              value={draft.from}
              onChange={(e) => setDraft({ ...draft, from: e.target.value })}
            />
          </div>
          <div className="space-y-1">
            <label htmlFor={`${id}-to`} className="block text-xs font-medium text-muted">
              To
            </label>
            <input
              id={`${id}-to`}
              type="datetime-local"
              className={inputClass}
              value={draft.to}
              onChange={(e) => setDraft({ ...draft, to: e.target.value })}
            />
          </div>
          <Button type="submit" variant="primary" disabled={Boolean(problem)} title={problem || undefined}>
            Apply
          </Button>
        </form>
      )}
    </div>
  )
}

/** A choice of tenant for an admin, and the viewer's own tenant for a viewer. */
export function TenantPicker({ value, onChange }: { value: string; onChange: (tenant: string) => void }) {
  const id = useId()
  const s = useSession()
  const tenants = useTenants()
  const items = tenants.data?.items ?? []

  if (s?.role !== 'admin') {
    const own = items.find((t) => t.id === s?.tenant)
    return (
      <div className="space-y-1">
        <span className="block text-xs font-medium text-muted">Tenant</span>
        <p className="py-1.5 text-sm font-medium" title="Viewers see only their own tenant">
          {own ? `${own.name} (${own.id})` : s?.tenant}
        </p>
      </div>
    )
  }
  // A tenant named in the URL is kept even if it isn't listed.
  const listed = !value || items.some((t) => t.id === value)
  return (
    <div className="space-y-1">
      <label htmlFor={`${id}-tenant`} className="block text-xs font-medium text-muted">
        Tenant
      </label>
      <select id={`${id}-tenant`} className={cx(inputClass, 'w-44')} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">All tenants</option>
        {items.map((t) => (
          <option key={t.id} value={t.id}>
            {t.name} ({t.id})
          </option>
        ))}
        {!listed && <option value={value}>{value}</option>}
      </select>
    </div>
  )
}

function SourcePicker({ value, onChange }: { value: Source[]; onChange: (s: Source[]) => void }) {
  const toggle = (s: Source) =>
    onChange(value.includes(s) ? value.filter((v) => v !== s) : sources.filter((v) => v === s || value.includes(v)))
  return (
    <fieldset className="space-y-1">
      <legend className="mb-1 block text-xs font-medium text-muted">
        Sources {value.length === 0 && <span className="font-normal">(all)</span>}
      </legend>
      <div className="flex flex-wrap gap-1">
        {sources.map((s) => {
          const on = value.includes(s)
          return (
            <button
              key={s}
              type="button"
              aria-pressed={on}
              onClick={() => toggle(s)}
              className={cx(
                'rounded-full border px-2.5 py-1 text-xs font-medium transition',
                'focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent',
                on ? 'border-accent bg-accent text-on-accent' : 'border-line bg-surface text-muted hover:text-fg',
              )}
            >
              {sourceLabels[s]}
            </button>
          )
        })}
        {value.length > 0 && (
          <button type="button" onClick={() => onChange([])} className="px-1.5 text-xs text-accent hover:underline">
            Clear
          </button>
        )}
      </div>
    </fieldset>
  )
}
