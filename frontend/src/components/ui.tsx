import { useId, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { ApiError } from '../api/client'
import { cx, type Tone } from '../lib/classes'

type ButtonVariant = 'primary' | 'secondary' | 'ghost'

const buttonVariants: Record<ButtonVariant, string> = {
  primary: 'bg-accent text-on-accent hover:opacity-90',
  secondary: 'border border-line bg-surface hover:bg-sunken',
  ghost: 'text-muted hover:bg-sunken hover:text-fg',
}

export function Button({
  variant = 'secondary',
  className,
  type = 'button',
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant }) {
  return (
    <button
      type={type}
      className={cx(
        'inline-flex items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium whitespace-nowrap transition',
        'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50',
        buttonVariants[variant],
        className,
      )}
      {...props}
    />
  )
}

export const inputClass =
  'w-full rounded-md border border-line bg-surface px-2.5 py-1.5 text-sm placeholder:text-muted/70 focus:border-accent focus:outline-2 focus:outline-accent/30'

export function Card({
  title,
  subtitle,
  actions,
  className,
  children,
}: {
  title?: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
  className?: string
  children: ReactNode
}) {
  const id = useId()
  return (
    <section className={cx('rounded-lg border border-line bg-surface', className)} aria-labelledby={title ? id : undefined}>
      {(title || actions) && (
        <header className="flex flex-wrap items-start justify-between gap-2 border-b border-line px-4 py-3">
          <div>
            {title && (
              <h2 id={id} className="text-sm font-semibold">
                {title}
              </h2>
            )}
            {subtitle && <p className="mt-0.5 text-xs text-muted">{subtitle}</p>}
          </div>
          {actions}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  )
}

const tones: Record<Tone, string> = {
  danger: 'border-danger/30 bg-danger-soft text-danger',
  warn: 'border-warn/30 bg-warn-soft text-warn',
  ok: 'border-ok/30 bg-ok-soft text-ok',
  info: 'border-accent/30 bg-accent-soft text-fg',
}

export function Notice({ tone = 'info', children }: { tone?: Tone; children: ReactNode }) {
  return (
    <div role={tone === 'danger' ? 'alert' : 'status'} className={cx('rounded-md border px-3 py-2 text-sm', tones[tone])}>
      {children}
    </div>
  )
}

/** Why a request failed, with the request ID to quote from the server log. */
export function ErrorNotice({ error, what }: { error: unknown; what: string }) {
  let detail = 'Cannot reach loghub. Check your connection and try again.'
  let requestId: string | undefined
  if (error instanceof ApiError) {
    detail = error.message
    requestId = error.requestId
  }
  return (
    <Notice tone="danger">
      <p>
        <span className="font-medium">Couldn't {what}.</span> {detail}
      </p>
      {requestId && <p className="mt-1 text-xs opacity-80">Request ID {requestId}</p>}
    </Notice>
  )
}

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return (
    <output className="inline-flex items-center gap-2 text-sm text-muted">
      <span aria-hidden className="size-4 animate-spin rounded-full border-2 border-line border-t-accent" />
      {label}…
    </output>
  )
}

/** What shows while the first page's code loads. */
export function PageLoading() {
  return (
    <div className="grid min-h-screen place-items-center">
      <Spinner />
    </div>
  )
}

export function Badge({ tone, children, title }: { tone?: Tone | 'neutral'; children: ReactNode; title?: string }) {
  const toneClass = tone && tone !== 'neutral' ? tones[tone] : 'border-line bg-sunken text-muted'
  return (
    <span
      title={title}
      className={cx('inline-flex items-center rounded border px-1.5 py-px text-xs font-medium whitespace-nowrap', toneClass)}
    >
      {children}
    </span>
  )
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <p className="py-6 text-center text-sm text-muted">{children}</p>
}

export function Field({
  label,
  htmlFor,
  hint,
  error,
  children,
}: {
  label: string
  htmlFor: string
  hint?: ReactNode
  error?: string
  children: ReactNode
}) {
  return (
    <div className="space-y-1">
      <label htmlFor={htmlFor} className="block text-xs font-medium text-muted">
        {label}
      </label>
      {children}
      {error ? (
        <p id={`${htmlFor}-error`} className="text-xs text-danger">
          {error}
        </p>
      ) : (
        hint && <p className="text-xs text-muted">{hint}</p>
      )}
    </div>
  )
}

export function PageHeader({ title, description, actions }: { title: string; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 text-sm text-muted">{description}</p>}
      </div>
      {actions}
    </div>
  )
}
