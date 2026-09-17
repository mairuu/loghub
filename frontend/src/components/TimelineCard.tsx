import { Bar, BarChart, CartesianGrid, Tooltip, XAxis, YAxis, type TooltipContentProps } from 'recharts'
import type { Schemas } from '../api/client'
import { useTimeline } from '../api/queries'
import type { Filters } from '../lib/filters'
import { formatCount, formatDate, formatDateTime, formatTimeOfDay } from '../lib/format'
import { cx } from '../lib/classes'
import { Card, ErrorNotice, Spinner } from './ui'

type Interval = Schemas['TimelineInterval']

const minute = 60_000
const intervalWidth: Record<Interval, number> = {
  '1m': minute,
  '5m': 5 * minute,
  '15m': 15 * minute,
  '30m': 30 * minute,
  '1h': 60 * minute,
  '3h': 180 * minute,
  '6h': 360 * minute,
  '12h': 720 * minute,
  '1d': 1440 * minute,
}

const intervalNames: Record<Interval, string> = {
  '1m': '1-minute',
  '5m': '5-minute',
  '15m': '15-minute',
  '30m': '30-minute',
  '1h': 'hourly',
  '3h': '3-hour',
  '6h': '6-hour',
  '12h': '12-hour',
  '1d': 'daily',
}

interface Row {
  start: number
  count: number
}

interface Props {
  filters: Filters
  /** Called with a bucket the user chose, clipped to the window, to search its events. */
  onPick: (from: Date, to: Date) => void
}

export function TimelineCard({ filters, onPick }: Props) {
  const timeline = useTimeline(filters)
  const data = timeline.data
  const rows: Row[] = data?.buckets.map((b) => ({ start: Date.parse(b.start), count: b.count })) ?? []
  const total = rows.reduce((sum, r) => sum + r.count, 0)
  const width = data ? intervalWidth[data.interval] : minute
  const spansDays = data ? Date.parse(data.to) - Date.parse(data.from) > 24 * 60 * minute : false

  const tick = (t: number) => {
    if (width >= 1440 * minute) return formatDate(new Date(t)).slice(5)
    const time = formatTimeOfDay(new Date(t), false)
    return spansDays ? `${formatDate(new Date(t)).slice(5)} ${time}` : time
  }
  const pick = (index: unknown) => {
    const row = rows[Number(index)]
    if (!row || !data) return
    const from = Math.max(row.start, Date.parse(data.from))
    const to = Math.min(row.start + width, Date.parse(data.to))
    onPick(new Date(from), new Date(to))
  }

  return (
    <Card
      title="Events over time"
      subtitle={data ? `${formatCount(total)} events · ${intervalNames[data.interval]} buckets · select a bar to search it` : 'Loading'}
      className="min-w-0"
    >
      {timeline.isError ? (
        <ErrorNotice error={timeline.error} what="count events over time" />
      ) : !data ? (
        <div className="grid h-64 place-items-center">
          <Spinner />
        </div>
      ) : (
        <div className={cx('h-64', timeline.isPlaceholderData && 'opacity-60')}>
          <p className="sr-only">
            {formatCount(total)} events from {formatDateTime(data.from)} to {formatDateTime(data.to)}, counted in{' '}
            {intervalNames[data.interval]} buckets.
          </p>
          <BarChart
            data={rows}
            responsive
            style={{ width: '100%', height: '100%', cursor: 'pointer' }}
            margin={{ top: 4, right: 4, bottom: 0, left: 0 }}
            // Anywhere in a bucket's column picks it, so short bars are easy to hit.
            onClick={(state) => state.activeTooltipIndex != null && pick(state.activeTooltipIndex)}
          >
            <CartesianGrid vertical={false} stroke="var(--line)" />
            <XAxis
              dataKey="start"
              tickFormatter={tick}
              minTickGap={28}
              tick={{ fill: 'var(--muted)', fontSize: 12 }}
              stroke="var(--line)"
              tickLine={false}
            />
            <YAxis
              allowDecimals={false}
              width={48}
              tickFormatter={formatCount}
              tick={{ fill: 'var(--muted)', fontSize: 12 }}
              stroke="var(--line)"
              tickLine={false}
              axisLine={false}
            />
            <Tooltip
              cursor={{ fill: 'var(--sunken)' }}
              content={(props: TooltipContentProps) => <BucketTooltip {...props} width={width} />}
              isAnimationActive={false}
            />
            <Bar dataKey="count" fill="var(--accent)" radius={[2, 2, 0, 0]} isAnimationActive={false} />
          </BarChart>
        </div>
      )}
    </Card>
  )
}

function BucketTooltip({ active, payload, width }: TooltipContentProps & { width: number }) {
  const row = payload?.[0]?.payload as Row | undefined
  if (!active || !row) return null
  const end = new Date(row.start + width)
  return (
    <div className="rounded-md border border-line bg-surface px-3 py-2 text-xs shadow-lg">
      <p className="text-muted">
        {formatDateTime(new Date(row.start), false)} – {width >= 1440 * minute ? formatDateTime(end, false) : formatTimeOfDay(end, false)}
      </p>
      <p className="mt-0.5 text-sm font-semibold">{formatCount(row.count)} events</p>
    </div>
  )
}
