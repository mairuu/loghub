import { useIsFetching, useQueryClient, type Query } from '@tanstack/react-query'
import { useMemo } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { fieldLabels, type TopField } from '../api/enums'
import { useAlerts, useTimeline } from '../api/queries'
import { useSession } from '../api/session'
import { ActiveFilters } from '../components/ActiveFilters'
import { FilterBar } from '../components/FilterBar'
import { ForeignTenant } from '../components/ForeignTenant'
import { TimelineCard } from '../components/TimelineCard'
import { TopCard } from '../components/TopCard'
import { Badge, Card, EmptyState, ErrorNotice, PageHeader } from '../components/ui'
import { readFilters, writeFilters, type Filters, type TextFilter } from '../lib/filters'
import { formatAgo, formatCount, formatDateTime } from '../lib/format'
import { isForeignTenant } from '../lib/tenancy'
import { describeRange } from '../lib/timeRange'

const tops: { field: TopField; title: string; filter: TextFilter }[] = [
  { field: 'src_ip', title: 'Top source IPs', filter: 'src_ip' },
  { field: 'user', title: 'Top users', filter: 'user' },
  { field: 'event_type', title: 'Top event types', filter: 'event_type' },
  { field: 'host', title: 'Top hosts', filter: 'host' },
]

const isCount = (q: Query) => q.queryKey[0] === 'top' || q.queryKey[0] === 'timeline'

export function DashboardPage() {
  const [params, setParams] = useSearchParams()
  const filters = useMemo(() => readFilters(params), [params])
  const navigate = useNavigate()
  const client = useQueryClient()
  const foreign = isForeignTenant(useSession(), filters.tenant)
  const timeline = useTimeline(filters, !foreign)
  const refreshing = useIsFetching({ predicate: isCount }) > 0

  const setFilters = (f: Filters) => setParams(writeFilters(f))
  const search = (f: Filters) => navigate({ pathname: '/search', search: writeFilters(f).toString() })
  const total = timeline.data?.buckets.reduce((sum, b) => sum + b.count, 0)

  return (
    <>
      <PageHeader
        title="Dashboard"
        description={`${describeRange(filters.range)}${total !== undefined ? ` · ${formatCount(total)} events` : ''}`}
      />
      <FilterBar
        filters={filters}
        onChange={setFilters}
        onRefresh={() => client.invalidateQueries({ predicate: isCount })}
        refreshing={refreshing}
        updatedAt={timeline.dataUpdatedAt}
      />
      <ActiveFilters filters={filters} onChange={setFilters} />
      {foreign ? (
        <ForeignTenant tenant={filters.tenant} onShowOwn={() => setFilters({ ...filters, tenant: '' })} />
      ) : (
        <>
          <div className="grid gap-4 xl:grid-cols-3">
            <div className="min-w-0 xl:col-span-2">
              <TimelineCard filters={filters} onPick={(from, to) => search({ ...filters, range: { kind: 'custom', from, to } })} />
            </div>
            <RecentAlerts tenant={filters.tenant} />
          </div>
          <div className="grid gap-4 md:grid-cols-2 2xl:grid-cols-4">
            {tops.map((t) => (
              <TopCard
                key={t.field}
                field={t.field}
                title={t.title}
                filters={filters}
                onPick={(value) => search({ ...filters, text: { ...filters.text, [t.filter]: value } })}
              />
            ))}
          </div>
        </>
      )}
    </>
  )
}

function RecentAlerts({ tenant }: { tenant: string }) {
  const alerts = useAlerts(tenant, 6)
  const items = alerts.data?.items ?? []
  return (
    <Card
      title="Recent alerts"
      subtitle="Newest first, whatever the time range"
      actions={
        <Link to="/alerts" className="text-sm text-accent hover:underline">
          All alerts
        </Link>
      }
    >
      {alerts.isError ? (
        <ErrorNotice error={alerts.error} what="load alerts" />
      ) : alerts.isPending ? (
        <EmptyState>Loading…</EmptyState>
      ) : items.length === 0 ? (
        <EmptyState>No alerts have been raised.</EmptyState>
      ) : (
        <ul className="divide-y divide-line">
          {items.map((a) => (
            <li key={a.id} className="py-2 first:pt-0 last:pb-0">
              <div className="flex items-baseline justify-between gap-2">
                <p className="truncate text-sm font-medium" title={a.rule_name}>
                  {a.rule_name}
                </p>
                <time dateTime={a.created_at} title={formatDateTime(a.created_at)} className="shrink-0 text-xs text-muted">
                  {formatAgo(a.created_at)}
                </time>
              </div>
              <p className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-muted">
                <Badge tone="danger">{formatCount(a.count)} events</Badge>
                <span>
                  {fieldLabels[a.group_by]} <span className="font-mono text-fg">{a.group_key}</span>
                </span>
                <span>· {a.tenant}</span>
              </p>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}
