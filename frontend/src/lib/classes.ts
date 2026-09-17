export function cx(...classes: (string | false | null | undefined)[]): string {
  return classes.filter(Boolean).join(' ')
}

export type Tone = 'danger' | 'warn' | 'ok' | 'info'

export function severityTone(severity: number | undefined): Tone | 'neutral' {
  if (severity === undefined) return 'neutral'
  if (severity >= 7) return 'danger'
  if (severity >= 4) return 'warn'
  return 'neutral'
}
