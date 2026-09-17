import { useId, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'
import { useEvents } from '../api/queries'
import { useSession } from '../api/session'
import { ActiveFilters } from '../components/ActiveFilters'
import { EventTable, type Narrowing } from '../components/EventTable'
import { ForeignTenant } from '../components/ForeignTenant'
import { FilterBar } from '../components/FilterBar'
import { Button, Card, EmptyState, ErrorNotice, Field, inputClass, PageHeader, Spinner } from '../components/ui'
import { readFilters, refinementCount, textFilters, writeFilters, type Filters, type TextFilters } from '../lib/filters'
import { formatCount } from '../lib/format'
import { isForeignTenant } from '../lib/tenancy'
import { describeRange } from '../lib/timeRange'

export function SearchPage() {
  const [params, setParams] = useSearchParams()
  const filters = useMemo(() => readFilters(params), [params])
  const s = useSession()
  const foreign = isForeignTenant(s, filters.tenant)
  const events = useEvents(filters, !foreign)
  const items = events.data?.pages.flatMap((p) => p.items) ?? []

  const setFilters = (f: Filters) => setParams(writeFilters(f))
  const narrow = (filter: Narrowing, value: string) =>
    setFilters(
      filter === 'tag'
        ? { ...filters, tags: filters.tags.includes(value) ? filters.tags : [...filters.tags, value] }
        : { ...filters, text: { ...filters.text, [filter]: value } },
    )

  return (
    <>
      <PageHeader title="Search" description={`${describeRange(filters.range)} · newest first`} />
      <FilterBar
        filters={filters}
        onChange={setFilters}
        onRefresh={() => events.refetch()}
        refreshing={events.isRefetching && !events.isFetchingNextPage}
        updatedAt={events.dataUpdatedAt}
      />
      {/* Keyed by the URL, so the form shows filters set from elsewhere. */}
      <RefineForm key={params.toString()} filters={filters} onChange={setFilters} />
      <ActiveFilters filters={filters} onChange={setFilters} />
      {foreign ? (
        <ForeignTenant tenant={filters.tenant} onShowOwn={() => setFilters({ ...filters, tenant: '' })} />
      ) : (
        <Card
          title="Events"
          subtitle={
            events.data
              ? `${formatCount(items.length)}${events.hasNextPage ? '+' : ''} shown${events.hasNextPage ? ' · load more below' : ''} · select a row for details`
              : undefined
          }
        >
          {events.isPending ? (
            <div className="grid h-40 place-items-center">
              <Spinner label="Searching" />
            </div>
          ) : !events.data ? (
            <ErrorNotice error={events.error} what="search" />
          ) : (
            <>
              {/* A page that fails after others loaded leaves them shown. */}
              {events.isError && (
                <div className="mb-3">
                  <ErrorNotice error={events.error} what={events.isFetchNextPageError ? 'load more events' : 'refresh the results'} />
                </div>
              )}
              {items.length === 0 ? (
                <EmptyState>No events match. Try a longer time range or fewer filters.</EmptyState>
              ) : (
                <EventTable events={items} showTenant={s?.role === 'admin' && !filters.tenant} onFilter={narrow} />
              )}
              {events.hasNextPage && (
                <div className="mt-4 flex justify-center">
                  <Button onClick={() => events.fetchNextPage()} disabled={events.isFetchingNextPage}>
                    {events.isFetchingNextPage ? 'Loading…' : 'Load more'}
                  </Button>
                </div>
              )}
            </>
          )}
        </Card>
      )}
    </>
  )
}

interface Draft {
  text: TextFilters
  severityMin: string
  severityMax: string
  tags: string
}

const severityValue = (s: string): number | undefined | null => {
  if (s.trim() === '') return undefined
  const n = Number(s)
  return Number.isInteger(n) && n >= 0 && n <= 10 ? n : null
}

function RefineForm({ filters, onChange }: { filters: Filters; onChange: (f: Filters) => void }) {
  const id = useId()
  const [draft, setDraft] = useState<Draft>({
    text: filters.text,
    severityMin: filters.severityMin?.toString() ?? '',
    severityMax: filters.severityMax?.toString() ?? '',
    tags: filters.tags.join(', '),
  })
  const beyondText = refinementCount(filters) - (filters.text.q ? 1 : 0)
  const [more, setMore] = useState(beyondText > 0)
  const setText = (key: keyof TextFilters, value: string) => setDraft({ ...draft, text: { ...draft.text, [key]: value } })

  const min = severityValue(draft.severityMin)
  const max = severityValue(draft.severityMax)
  const severityError =
    min === null || max === null
      ? 'Severity is a whole number from 0 to 10.'
      : min !== undefined && max !== undefined && min > max
        ? 'The minimum is above the maximum.'
        : undefined

  return (
    <search>
      <form
        className="space-y-3 rounded-lg border border-line bg-surface p-3"
        onSubmit={(e) => {
          e.preventDefault()
          if (severityError || min === null || max === null) return
          onChange({
            ...filters,
            text: draft.text,
            severityMin: min,
            severityMax: max,
            tags: [
              ...new Set(
                draft.tags
                  .split(',')
                  .map((t) => t.trim())
                  .filter(Boolean),
              ),
            ],
          })
        }}
      >
        <div className="flex flex-wrap items-center gap-2">
          <label htmlFor={`${id}-q`} className="sr-only">
            Text in the original event
          </label>
          <input
            id={`${id}-q`}
            type="search"
            className={`${inputClass} min-w-60 flex-1`}
            placeholder="Search the original events for text, such as an address, a name or an ID"
            value={draft.text.q ?? ''}
            onChange={(e) => setText('q', e.target.value)}
          />
          <Button type="submit" variant="primary">
            Search
          </Button>
          <Button variant="ghost" aria-expanded={more} aria-controls={`${id}-more`} onClick={() => setMore(!more)}>
            {more ? 'Fewer filters' : `More filters${beyondText ? ` (${beyondText})` : ''}`}
          </Button>
        </div>
        {more && (
          <div id={`${id}-more`} className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {textFilters
              .filter((f) => f.key !== 'q')
              .map((f) => (
                <Field key={f.key} label={f.label} htmlFor={`${id}-${f.key}`}>
                  <input
                    id={`${id}-${f.key}`}
                    className={inputClass}
                    value={draft.text[f.key] ?? ''}
                    onChange={(e) => setText(f.key, e.target.value)}
                    spellCheck={false}
                  />
                </Field>
              ))}
            <Field label="Tags, all required" htmlFor={`${id}-tags`} hint="Separate with commas, such as auth_failure">
              <input
                id={`${id}-tags`}
                className={inputClass}
                value={draft.tags}
                onChange={(e) => setDraft({ ...draft, tags: e.target.value })}
                spellCheck={false}
              />
            </Field>
            <Field label="Severity from 0 to 10" htmlFor={`${id}-sev-min`} error={severityError}>
              <div className="flex items-center gap-2">
                <input
                  id={`${id}-sev-min`}
                  aria-label="Minimum severity"
                  type="number"
                  min={0}
                  max={10}
                  className={inputClass}
                  value={draft.severityMin}
                  onChange={(e) => setDraft({ ...draft, severityMin: e.target.value })}
                  aria-invalid={Boolean(severityError)}
                  aria-describedby={severityError ? `${id}-sev-min-error` : undefined}
                />
                <span className="text-muted">to</span>
                <input
                  aria-label="Maximum severity"
                  type="number"
                  min={0}
                  max={10}
                  className={inputClass}
                  value={draft.severityMax}
                  onChange={(e) => setDraft({ ...draft, severityMax: e.target.value })}
                  aria-invalid={Boolean(severityError)}
                />
              </div>
            </Field>
          </div>
        )}
      </form>
    </search>
  )
}
