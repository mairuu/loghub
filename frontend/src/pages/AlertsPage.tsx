import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import type { Schemas } from '../api/client'
import { fieldLabels, fieldNouns, sourceLabels } from '../api/enums'
import { useAlertRules, useAlerts } from '../api/queries'
import { useSession } from '../api/session'
import { useUnseenAlerts } from '../api/unseenAlerts'
import { TenantPicker } from '../components/FilterBar'
import { ForeignTenant } from '../components/ForeignTenant'
import { RuleForm } from '../components/RuleForm'
import { Badge, Button, Card, EmptyState, ErrorNotice, Notice, PageHeader, Spinner } from '../components/ui'
import { alertSearch } from '../lib/alertSearch'
import { formatAgo, formatCount, formatDateTime, formatTimeOfDay } from '../lib/format'
import { isForeignTenant } from '../lib/tenancy'

type Rule = Schemas['AlertRule']

const alertPage = 100

export function AlertsPage() {
  const s = useSession()
  const isAdmin = s?.role === 'admin'
  const [params, setParams] = useSearchParams()
  const tenant = params.get('tenant') ?? ''
  const foreign = isForeignTenant(s, tenant)
  const alerts = useAlerts(tenant, alertPage, !foreign)
  const rules = useAlertRules(tenant, !foreign)
  const { markSeen } = useUnseenAlerts()
  const [creating, setCreating] = useState(false)
  const [created, setCreated] = useState<Rule | null>(null)

  const newest = alerts.data?.items[0]?.created_at
  useEffect(() => {
    if (newest) markSeen(newest)
  }, [newest, markSeen])

  const rulesById = new Map(rules.data?.items.map((r) => [r.id, r]))
  const items = alerts.data?.items ?? []

  return (
    <>
      <PageHeader
        title="Alerts"
        description="Rules are evaluated every minute. An alert appears up to about two and a half minutes after the event that completes it."
        actions={<TenantPicker value={tenant} onChange={(t) => setParams(t ? { tenant: t } : {})} />}
      />
      {foreign && <ForeignTenant tenant={tenant} onShowOwn={() => setParams({})} />}
      {!foreign && (
        <>
          <Card
            title="Raised alerts"
            subtitle={`Newest first${items.length === alertPage ? `, the latest ${alertPage}` : ''} · refreshed every 15 seconds`}
          >
            {alerts.isError ? (
              <ErrorNotice error={alerts.error} what="load alerts" />
            ) : alerts.isPending ? (
              <Spinner />
            ) : items.length === 0 ? (
              <EmptyState>No alerts have been raised{tenant ? ` for ${tenant}` : ''}.</EmptyState>
            ) : (
              <div className="-mx-4 overflow-x-auto">
                <table className="w-full min-w-190 text-left text-sm">
                  <thead className="border-b border-line text-xs text-muted">
                    <tr>
                      <th scope="col" className="py-2 pr-2 pl-4 font-medium">
                        Raised
                      </th>
                      <th scope="col" className="px-2 py-2 font-medium">
                        Rule
                      </th>
                      <th scope="col" className="px-2 py-2 font-medium">
                        Tenant
                      </th>
                      <th scope="col" className="px-2 py-2 font-medium">
                        Group
                      </th>
                      <th scope="col" className="px-2 py-2 text-right font-medium">
                        Events
                      </th>
                      <th scope="col" className="px-2 py-2 font-medium">
                        Window
                      </th>
                      <th scope="col" className="py-2 pr-4 pl-2 font-medium">
                        <span className="sr-only">Actions</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {items.map((a) => (
                      <tr key={a.id} className="border-b border-line last:border-0 hover:bg-sunken">
                        <td className="py-2 pr-2 pl-4 whitespace-nowrap">
                          <time dateTime={a.created_at} title={formatDateTime(a.created_at)}>
                            {formatAgo(a.created_at)}
                          </time>
                        </td>
                        <td className="px-2 py-2 font-medium">{a.rule_name}</td>
                        <td className="px-2 py-2">{a.tenant}</td>
                        <td className="px-2 py-2">
                          <span className="text-muted">{fieldLabels[a.group_by]}</span>{' '}
                          <span className="font-mono text-[13px]">{a.group_key}</span>
                        </td>
                        <td className="px-2 py-2 text-right">
                          <Badge tone="danger">{formatCount(a.count)}</Badge>
                        </td>
                        <td className="px-2 py-2 whitespace-nowrap text-muted">
                          {formatDateTime(a.window_start, false)}–{formatTimeOfDay(a.window_end, false)}
                        </td>
                        <td className="py-2 pr-4 pl-2 text-right whitespace-nowrap">
                          <Link
                            to={{ pathname: '/search', search: alertSearch(a, rulesById.get(a.rule_id)) }}
                            className="text-accent hover:underline"
                          >
                            View events
                          </Link>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Card>

          <Card
            title="Rules"
            subtitle={isAdmin ? 'Rules can be added, not changed or removed.' : 'Only an admin can add rules.'}
            actions={
              isAdmin &&
              !creating && (
                <Button
                  variant="primary"
                  onClick={() => {
                    setCreating(true)
                    setCreated(null)
                  }}
                >
                  New rule
                </Button>
              )
            }
          >
            <div className="space-y-4">
              {created && (
                <Notice tone="ok">
                  Rule “{created.name}” was added for {created.tenant}. It is evaluated from the next minute.
                </Notice>
              )}
              {creating && (
                <RuleForm
                  defaultTenant={tenant}
                  onCancel={() => setCreating(false)}
                  onCreated={(rule) => {
                    setCreating(false)
                    setCreated(rule)
                  }}
                />
              )}
              <RuleList rules={rules} />
            </div>
          </Card>
        </>
      )}
    </>
  )
}

function RuleList({ rules }: { rules: ReturnType<typeof useAlertRules> }) {
  if (rules.isError) return <ErrorNotice error={rules.error} what="load rules" />
  if (rules.isPending) return <Spinner />
  const items = rules.data.items
  if (items.length === 0) return <EmptyState>No rules yet.</EmptyState>
  return (
    <div className="-mx-4 overflow-x-auto">
      <table className="w-full min-w-190 text-left text-sm">
        <thead className="border-b border-line text-xs text-muted">
          <tr>
            <th scope="col" className="py-2 pr-2 pl-4 font-medium">
              Name
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              Tenant
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              Counts events that match
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              Raises an alert at
            </th>
            <th scope="col" className="py-2 pr-4 pl-2 font-medium">
              Webhook
            </th>
          </tr>
        </thead>
        <tbody>
          {items.map((r) => (
            <tr key={r.id} className="border-b border-line align-top last:border-0">
              <td className="py-2 pr-2 pl-4">
                <p className="font-medium">{r.name}</p>
                <p className="text-xs text-muted">Added {formatDateTime(r.created_at, false)}</p>
              </td>
              <td className="px-2 py-2">{r.tenant}</td>
              <td className="px-2 py-2">
                <RuleFilter rule={r} />
              </td>
              <td className="px-2 py-2">
                {formatCount(r.threshold)} from one {fieldNouns[r.group_by]} within {r.window_minutes} min
                <p className="text-xs text-muted">then quiet for {r.cooldown_minutes} min</p>
              </td>
              <td className="py-2 pr-4 pl-2 text-muted">{webhookHost(r.webhook_url)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function RuleFilter({ rule: r }: { rule: Rule }) {
  const parts = [
    r.source && `source ${sourceLabels[r.source]}`,
    r.event_type && `event type ${r.event_type}`,
    r.action && `action ${r.action}`,
    r.severity_min !== undefined && `severity ≥ ${r.severity_min}`,
  ].filter(Boolean)
  if (parts.length === 0 && r.tags.length === 0) return <span className="text-muted">every event</span>
  return (
    <span className="flex flex-wrap items-center gap-1">
      {parts.map((p) => (
        <Badge key={String(p)}>{p}</Badge>
      ))}
      {r.tags.map((t) => (
        <Badge key={t} tone="info">
          tag {t}
        </Badge>
      ))}
    </span>
  )
}

// A webhook URL often carries a secret, so only its host is shown. Viewers
// aren't sent the URL at all.
function webhookHost(url: string | undefined): string {
  if (!url) return '—'
  try {
    return new URL(url).host
  } catch {
    return 'set'
  }
}
