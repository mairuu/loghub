import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { eventQuery, writeFilters, type Filters } from '../lib/filters'
import { api, unwrap, type Schemas } from './client'
import type { Source, TopField } from './enums'

// New events and alerts show within the minute the acceptance criteria allow.
const countsRefresh = 30_000
const liveRefresh = 15_000
const searchPage = 50

// A preset's window moves with the clock, so queries are keyed by the
// filters as the URL holds them, and the window is worked out per request.
const filtersKey = (f: Filters) => writeFilters(f).toString()

export function useTenants() {
  return useQuery({
    queryKey: ['tenants'],
    queryFn: ({ signal }) => unwrap(api.GET('/api/v1/tenants', { signal })),
    staleTime: 5 * 60_000,
  })
}

export function useTop(field: TopField, filters: Filters, limit = 10) {
  return useQuery({
    queryKey: ['top', field, limit, filtersKey(filters)],
    queryFn: ({ signal }) => unwrap(api.GET('/api/v1/events/top', { params: { query: { ...eventQuery(filters), field, limit } }, signal })),
    placeholderData: keepPreviousData,
    refetchInterval: countsRefresh,
  })
}

export function useTimeline(filters: Filters, enabled = true) {
  return useQuery({
    enabled,
    queryKey: ['timeline', filtersKey(filters)],
    queryFn: ({ signal }) => unwrap(api.GET('/api/v1/events/timeline', { params: { query: eventQuery(filters) }, signal })),
    placeholderData: keepPreviousData,
    refetchInterval: countsRefresh,
  })
}

export function useEvents(filters: Filters, enabled = true) {
  return useInfiniteQuery({
    enabled,
    queryKey: ['events', filtersKey(filters)],
    initialPageParam: '',
    queryFn: ({ pageParam: cursor, signal }) => {
      const { from, to, ...rest } = eventQuery(filters)
      // A cursor carries its window, and a preset's would have moved on.
      const query = cursor ? { ...rest, cursor, limit: searchPage } : { ...rest, from, to, limit: searchPage }
      return unwrap(api.GET('/api/v1/events', { params: { query }, signal }))
    },
    getNextPageParam: (page) => page.next_cursor,
    // Refreshing would reload every page, so only the first is kept live.
    refetchInterval: (query) => ((query.state.data?.pages.length ?? 0) <= 1 ? liveRefresh : false),
  })
}

export function useAlerts(tenant: string, limit = 50, enabled = true) {
  return useQuery({
    enabled,
    queryKey: ['alerts', tenant, limit],
    queryFn: ({ signal }) => unwrap(api.GET('/api/v1/alerts', { params: { query: { tenant: tenant || undefined, limit } }, signal })),
    refetchInterval: liveRefresh,
  })
}

export function useAlertRules(tenant: string, enabled = true) {
  return useQuery({
    enabled,
    queryKey: ['alert-rules', tenant],
    queryFn: ({ signal }) => unwrap(api.GET('/api/v1/alert-rules', { params: { query: { tenant: tenant || undefined } }, signal })),
  })
}

export function useCreateRule() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (rule: Schemas['NewAlertRule']) => unwrap(api.POST('/api/v1/alert-rules', { body: rule })),
    onSuccess: () => client.invalidateQueries({ queryKey: ['alert-rules'] }),
  })
}

export interface UploadRequest {
  body: string
  tenant: string
  source: Source | ''
}

export function useUpload() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ body, tenant, source }: UploadRequest) =>
      unwrap(
        api.POST('/api/v1/ingest/file', {
          params: { query: { tenant: tenant || undefined, source: source || undefined } },
          // openapi-fetch would send a string body as a JSON string.
          body,
          bodySerializer: (b) => b,
          headers: { 'Content-Type': 'application/x-ndjson' },
        }),
      ),
    // New events change every count and search.
    onSuccess: () => client.invalidateQueries({ predicate: (q) => ['events', 'top', 'timeline'].includes(String(q.queryKey[0])) }),
  })
}
