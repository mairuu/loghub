import { Fragment, useState, type ReactNode } from 'react'
import type { Schemas } from '../api/client'
import { sourceLabels } from '../api/enums'
import { textFilters, type TextFilter } from '../lib/filters'
import { formatDate, formatDateTime, formatTimeOfDay } from '../lib/format'
import { cx, severityTone } from '../lib/classes'
import { Badge } from './ui'

type Event = Schemas['Event']

/** A filter an event's value can be added to. */
export type Narrowing = TextFilter | 'tag'

interface Props {
  events: Event[]
  showTenant: boolean
  /** Called to narrow the search to a value shown in an event. */
  onFilter: (filter: Narrowing, value: string) => void
}

const address = (ip?: string, port?: number) => (ip ? (port !== undefined ? `${ip.includes(':') ? `[${ip}]` : ip}:${port}` : ip) : '')

export function EventTable({ events, showTenant, onFilter }: Props) {
  const [open, setOpen] = useState<Set<string>>(new Set())
  const toggle = (key: string) =>
    setOpen((prev) => {
      const next = new Set(prev)
      if (!next.delete(key)) next.add(key)
      return next
    })
  const columns = showTenant ? 9 : 8

  return (
    <div className="-mx-4 overflow-x-auto">
      <table className="w-full min-w-225 text-left text-sm">
        <thead className="border-b border-line text-xs text-muted">
          <tr>
            <th scope="col" className="py-2 pr-2 pl-4 font-medium">
              Time
            </th>
            {showTenant && (
              <th scope="col" className="px-2 py-2 font-medium">
                Tenant
              </th>
            )}
            <th scope="col" className="px-2 py-2 font-medium">
              Source
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              Event type
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              Action
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              Sev.
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              Source → destination
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              User
            </th>
            <th scope="col" className="py-2 pr-4 pl-2 font-medium">
              Host
            </th>
          </tr>
        </thead>
        <tbody>
          {events.map((e) => {
            const key = `${e.id}@${e['@timestamp']}`
            const expanded = open.has(key)
            const src = address(e.src_ip, e.src_port)
            const dst = address(e.dst_ip, e.dst_port)
            return (
              <Fragment key={key}>
                <tr
                  className={cx('border-b border-line align-top hover:bg-sunken', expanded && 'bg-sunken')}
                  onClick={() => {
                    // Selecting text to copy it shouldn't fold the row.
                    if (!window.getSelection()?.toString()) toggle(key)
                  }}
                >
                  <td className="py-1.5 pr-2 pl-4 whitespace-nowrap">
                    <button
                      type="button"
                      aria-expanded={expanded}
                      aria-label={`${expanded ? 'Hide' : 'Show'} details of the ${e.event_type ?? ''} event at ${formatDateTime(e['@timestamp'])}`}
                      className="inline-flex items-center gap-1.5 rounded text-left focus-visible:outline-2 focus-visible:outline-accent"
                      onClick={(ev) => {
                        ev.stopPropagation()
                        toggle(key)
                      }}
                    >
                      <span aria-hidden className={cx('inline-block w-3 text-muted transition-transform', expanded && 'rotate-90')}>
                        ▸
                      </span>
                      <time dateTime={e['@timestamp']} title={e['@timestamp']}>
                        <span className="text-muted">{formatDate(e['@timestamp'])}</span> {formatTimeOfDay(e['@timestamp'])}
                      </time>
                    </button>
                  </td>
                  {showTenant && <td className="px-2 py-1.5 whitespace-nowrap">{e.tenant}</td>}
                  <td className="px-2 py-1.5 whitespace-nowrap">{sourceLabels[e.source]}</td>
                  <td className="max-w-56 truncate px-2 py-1.5 font-medium" title={e.event_type}>
                    {e.event_type}
                  </td>
                  <td className="px-2 py-1.5">{e.action}</td>
                  <td className="px-2 py-1.5">{e.severity !== undefined && <Badge tone={severityTone(e.severity)}>{e.severity}</Badge>}</td>
                  <td className="px-2 py-1.5 font-mono text-[13px] whitespace-nowrap">
                    {src}
                    {dst && <span className="text-muted"> → </span>}
                    {dst}
                  </td>
                  <td className="max-w-48 truncate px-2 py-1.5" title={e.user}>
                    {e.user}
                  </td>
                  <td className="max-w-40 truncate py-1.5 pr-4 pl-2" title={e.host}>
                    {e.host}
                  </td>
                </tr>
                {expanded && (
                  <tr className="border-b border-line bg-sunken">
                    <td colSpan={columns} className="px-4 pt-1 pb-4">
                      <EventDetail event={e} onFilter={onFilter} />
                    </td>
                  </tr>
                )}
              </Fragment>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function EventDetail({ event: e, onFilter }: { event: Event; onFilter: Props['onFilter'] }) {
  const filterable = (filter: TextFilter, value: string | undefined): ReactNode =>
    value && (
      <span className="inline-flex items-center gap-1">
        <span className="break-all">{value}</span>
        <button
          type="button"
          onClick={() => onFilter(filter, value)}
          className="rounded px-1 text-xs text-accent hover:bg-accent-soft"
          title={`Search for ${value}`}
          aria-label={`Search for ${textFilters.find((f) => f.key === filter)?.label}: ${value}`}
        >
          filter
        </button>
      </span>
    )
  const fields: [string, ReactNode][] = [
    ['Time', `${formatDateTime(e['@timestamp'])} (${e['@timestamp']})`],
    ['Received', formatDateTime(e.received_at)],
    ['Tenant', e.tenant],
    ['Source', sourceLabels[e.source]],
    ['Vendor / product', [e.vendor, e.product].filter(Boolean).join(' / ')],
    ['Event type', filterable('event_type', e.event_type)],
    ['Subtype', e.event_subtype],
    ['Action', filterable('action', e.action)],
    ['Severity', e.severity],
    ['Source IP', filterable('src_ip', e.src_ip)],
    ['Source port', e.src_port],
    ['Destination', address(e.dst_ip, e.dst_port)],
    ['Protocol', e.protocol],
    ['User', filterable('user', e.user)],
    ['Host', filterable('host', e.host)],
    ['Process', e.process],
    ['URL', e.url],
    ['HTTP', [e.http_method, e.status_code].filter((v) => v !== undefined).join(' ')],
    ['Rule', [e.rule_name, e.rule_id && `(${e.rule_id})`].filter(Boolean).join(' ')],
    ['Cloud account', e.cloud?.account_id],
    ['Cloud region', e.cloud?.region],
    ['Cloud service', e.cloud?.service],
    ['ID', e.id],
  ]
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <div>
        <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
          {fields
            .filter(([, v]) => v !== undefined && v !== '' && v !== false)
            .map(([label, value]) => (
              <Fragment key={label}>
                <dt className="text-muted">{label}</dt>
                <dd className="min-w-0 wrap-break-word">{value}</dd>
              </Fragment>
            ))}
        </dl>
        {e._tags.length > 0 && (
          <div className="mt-3 flex flex-wrap items-center gap-1.5">
            <span className="text-sm text-muted">Tags</span>
            {e._tags.map((t) => (
              <button
                key={t}
                type="button"
                onClick={() => onFilter('tag', t)}
                className="rounded border border-line bg-surface px-1.5 py-px font-mono text-xs hover:border-accent"
                title={`Search for events tagged ${t}`}
              >
                {t}
              </button>
            ))}
          </div>
        )}
      </div>
      <div className="min-w-0">
        <p className="mb-1 text-sm text-muted">Original event</p>
        <pre className="max-h-80 overflow-auto rounded-md border border-line bg-surface p-3 font-mono text-xs leading-relaxed">
          {JSON.stringify(e.raw, null, 2)}
        </pre>
      </div>
    </div>
  )
}
