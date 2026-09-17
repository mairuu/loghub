// Times are shown in the browser's zone, as ISO-like text that reads the same
// in every locale.

const pad = (n: number, width = 2) => String(n).padStart(width, '0')

function toDate(t: string | Date): Date {
  return typeof t === 'string' ? new Date(t) : t
}

export function formatDate(t: string | Date): string {
  const d = toDate(t)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

export function formatTimeOfDay(t: string | Date, seconds = true): string {
  const d = toDate(t)
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  return seconds ? `${hm}:${pad(d.getSeconds())}` : hm
}

export function formatDateTime(t: string | Date, seconds = true): string {
  return `${formatDate(t)} ${formatTimeOfDay(t, seconds)}`
}

/** The browser's offset from UTC, such as UTC+07:00. */
export function zoneLabel(at: Date = new Date()): string {
  const offset = -at.getTimezoneOffset()
  const sign = offset < 0 ? '-' : '+'
  const abs = Math.abs(offset)
  return `UTC${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`
}

const counts = new Intl.NumberFormat('en-US')

export function formatCount(n: number): string {
  return counts.format(n)
}

/** How long ago t was, roughly, such as "3 min ago". */
export function formatAgo(t: string | Date, now: Date = new Date()): string {
  const seconds = Math.round((now.getTime() - toDate(t).getTime()) / 1000)
  if (seconds < 45) return 'just now'
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours} h ago`
  return `${Math.round(hours / 24)} d ago`
}

/** A date-time as a datetime-local input shows it, in the browser's zone. */
export function toLocalInput(d: Date): string {
  return `${formatDate(d)}T${formatTimeOfDay(d, false)}`
}

/** A datetime-local input's value as a Date, or null if it isn't one. */
export function fromLocalInput(value: string): Date | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?$/.test(value)) return null
  // Without an offset, this is read as local time.
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? null : d
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(1)} MiB`
}
