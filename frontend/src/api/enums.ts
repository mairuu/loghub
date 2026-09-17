import type { components } from './schema'

type Schemas = components['schemas']
export type Source = Schemas['Source']
export type TopField = Schemas['TopField']
export type GroupBy = Schemas['AlertGroupBy']
export type Role = Schemas['Role']

export const sources = ['firewall', 'network', 'crowdstrike', 'aws', 'm365', 'ad', 'api'] as const satisfies readonly Source[]

export const sourceLabels: Record<Source, string> = {
  firewall: 'Firewall',
  network: 'Network',
  crowdstrike: 'CrowdStrike',
  aws: 'AWS',
  m365: 'Microsoft 365',
  ad: 'Active Directory',
  api: 'API',
}

export const groupBys = ['src_ip', 'dst_ip', 'user', 'host'] as const satisfies readonly GroupBy[]

export const fieldLabels: Record<TopField, string> = {
  src_ip: 'Source IP',
  dst_ip: 'Destination IP',
  user: 'User',
  host: 'Host',
  event_type: 'Event type',
}

/** Field names as they read mid-sentence. */
export const fieldNouns: Record<TopField, string> = {
  src_ip: 'source IP',
  dst_ip: 'destination IP',
  user: 'user',
  host: 'host',
  event_type: 'event type',
}

// The spec's enums have no runtime form, so the lists above are written out.
// These fail to compile if the spec gains a value a list lacks.
type Complete<T, L extends readonly T[]> = [Exclude<T, L[number]>] extends [never] ? true : false
type Assert<T extends true> = T
export type SourcesComplete = Assert<Complete<Source, typeof sources>>
export type GroupBysComplete = Assert<Complete<GroupBy, typeof groupBys>>

export function isSource(s: string): s is Source {
  return (sources as readonly string[]).includes(s)
}
