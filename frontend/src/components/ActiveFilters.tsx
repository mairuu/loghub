import { textFilters, type Filters } from '../lib/filters'

interface Chip {
  label: string
  remove: (f: Filters) => Filters
}

function chipsOf(f: Filters): Chip[] {
  const chips: Chip[] = []
  for (const { key, label } of textFilters) {
    const value = f.text[key]
    if (value) chips.push({ label: `${label}: ${value}`, remove: (g) => ({ ...g, text: { ...g.text, [key]: undefined } }) })
  }
  if (f.severityMin !== undefined) chips.push({ label: `Severity ≥ ${f.severityMin}`, remove: (g) => ({ ...g, severityMin: undefined }) })
  if (f.severityMax !== undefined) chips.push({ label: `Severity ≤ ${f.severityMax}`, remove: (g) => ({ ...g, severityMax: undefined }) })
  for (const tag of f.tags) {
    chips.push({ label: `Tag: ${tag}`, remove: (g) => ({ ...g, tags: g.tags.filter((t) => t !== tag) }) })
  }
  return chips
}

/** The filters beyond time, tenant and source, each removable. */
export function ActiveFilters({ filters, onChange }: { filters: Filters; onChange: (f: Filters) => void }) {
  const chips = chipsOf(filters)
  if (chips.length === 0) return null
  return (
    <div className="flex flex-wrap items-center gap-1.5" aria-label="Active filters">
      <span className="text-xs text-muted">Filtered by</span>
      {chips.map((chip) => (
        <span
          key={chip.label}
          className="inline-flex max-w-full items-center gap-1 rounded-full border border-accent/40 bg-accent-soft py-0.5 pr-1 pl-2.5 text-xs"
        >
          <span className="truncate">{chip.label}</span>
          <button
            type="button"
            onClick={() => onChange(chip.remove(filters))}
            aria-label={`Remove ${chip.label}`}
            className="rounded-full px-1 text-muted hover:bg-surface hover:text-fg"
          >
            ×
          </button>
        </span>
      ))}
      <button
        type="button"
        className="px-1 text-xs text-accent hover:underline"
        onClick={() => onChange({ ...filters, text: {}, severityMin: undefined, severityMax: undefined, tags: [] })}
      >
        Clear all
      </button>
    </div>
  )
}
