import type { Schemas } from '../api/client'
import { defaultFilters, writeFilters, type Filters } from './filters'

/** The search for the events an alert counted: its rule's filter, its group and its window. */
export function alertSearch(alert: Schemas['Alert'], rule: Schemas['AlertRule'] | undefined): string {
  const f: Filters = {
    ...defaultFilters,
    range: { kind: 'custom', from: new Date(alert.window_start), to: new Date(alert.window_end) },
    tenant: alert.tenant,
    sources: rule?.source ? [rule.source] : [],
    text: { event_type: rule?.event_type, action: rule?.action },
    severityMin: rule?.severity_min,
    tags: rule?.tags ?? [],
  }
  // Search can't filter on a destination address.
  if (alert.group_by !== 'dst_ip') f.text[alert.group_by] = alert.group_key
  return writeFilters(f).toString()
}
