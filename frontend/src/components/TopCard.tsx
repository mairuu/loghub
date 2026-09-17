import { fieldNouns, type TopField } from '../api/enums'
import { useTop } from '../api/queries'
import type { Filters } from '../lib/filters'
import { formatCount } from '../lib/format'
import { cx } from '../lib/classes'
import { Card, EmptyState, ErrorNotice } from './ui'

interface Props {
  field: TopField
  title: string
  filters: Filters
  /** Called with a value the user chose, to search for its events. */
  onPick: (value: string) => void
}

const monospace: TopField[] = ['src_ip', 'dst_ip']

/** The most frequent values of one field, as bars. */
export function TopCard({ field, title, filters, onPick }: Props) {
  const top = useTop(field, filters)
  const items = top.data?.items ?? []
  const max = Math.max(1, ...items.map((i) => i.count))
  const label = fieldNouns[field]

  return (
    <Card title={title} subtitle={`Top ${items.length || 10} by events · select one to search`}>
      {top.isError ? (
        <ErrorNotice error={top.error} what={`count by ${label}`} />
      ) : top.isPending ? (
        <ol aria-hidden className="space-y-2">
          {Array.from({ length: 5 }, (_, i) => (
            <li key={i} className="h-7 animate-pulse rounded bg-sunken" style={{ width: `${90 - i * 12}%` }} />
          ))}
        </ol>
      ) : items.length === 0 ? (
        <EmptyState>No {label}s in this range.</EmptyState>
      ) : (
        <ol className={cx('space-y-1', top.isPlaceholderData && 'opacity-60')}>
          {items.map((item) => (
            <li key={item.value}>
              <button
                type="button"
                onClick={() => onPick(item.value)}
                title={`Search events where ${label} is ${item.value}`}
                className="group relative flex w-full items-center justify-between gap-3 overflow-hidden rounded px-2 py-1 text-left text-sm hover:bg-sunken focus-visible:outline-2 focus-visible:outline-accent"
              >
                <span
                  aria-hidden
                  className="absolute inset-y-0 left-0 rounded bg-accent-soft transition-[width] group-hover:bg-accent/20"
                  style={{ width: `${(item.count / max) * 100}%` }}
                />
                <span className={cx('relative truncate', monospace.includes(field) && 'font-mono text-[13px]')}>{item.value}</span>
                <span className="relative shrink-0 font-medium tabular-nums">{formatCount(item.count)}</span>
              </button>
            </li>
          ))}
        </ol>
      )}
    </Card>
  )
}
