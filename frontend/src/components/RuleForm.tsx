import { useId, useState, type FormEvent, type InputHTMLAttributes } from 'react'
import { ApiError, type Schemas } from '../api/client'
import { fieldLabels, groupBys, sourceLabels, sources, type GroupBy, type Source } from '../api/enums'
import { useCreateRule, useTenants } from '../api/queries'
import { Button, ErrorNotice, Field, inputClass } from './ui'

type Rule = Schemas['AlertRule']

interface Draft {
  tenant: string
  name: string
  source: Source | ''
  event_type: string
  action: string
  severity_min: string
  tags: string
  group_by: GroupBy
  threshold: string
  window_minutes: string
  cooldown_minutes: string
  webhook_url: string
}

const blank: Draft = {
  tenant: '',
  name: '',
  source: '',
  event_type: '',
  action: '',
  severity_min: '',
  tags: '',
  group_by: 'src_ip',
  threshold: '5',
  window_minutes: '5',
  cooldown_minutes: '',
  webhook_url: '',
}

// samples/alert_rule.json, the assignment's worked example.
const failedLogins: Partial<Draft> = {
  name: 'Repeated failed logins from one address',
  source: '',
  event_type: '',
  action: '',
  severity_min: '',
  tags: 'auth_failure',
  group_by: 'src_ip',
  threshold: '5',
  window_minutes: '5',
  cooldown_minutes: '',
}

/** The rule a draft describes. Numbers the browser can't read are sent as they are, for the API to explain. */
function toRule(d: Draft, tenant: string): Schemas['NewAlertRule'] {
  const int = (s: string) => (s.trim() === '' ? undefined : Number(s))
  const text = (s: string) => s.trim() || undefined
  const tags = d.tags
    .split(',')
    .map((t) => t.trim())
    .filter(Boolean)
  return {
    tenant,
    name: d.name.trim(),
    source: d.source || undefined,
    event_type: text(d.event_type),
    action: text(d.action),
    severity_min: int(d.severity_min),
    tags: tags.length ? tags : undefined,
    group_by: d.group_by,
    threshold: int(d.threshold) as number,
    window_minutes: int(d.window_minutes) as number,
    cooldown_minutes: int(d.cooldown_minutes),
    webhook_url: text(d.webhook_url),
  }
}

interface Props {
  defaultTenant: string
  onCreated: (rule: Rule) => void
  onCancel: () => void
}

export function RuleForm({ defaultTenant, onCreated, onCancel }: Props) {
  const id = useId()
  const tenants = useTenants()
  const create = useCreateRule()
  const [draft, setDraft] = useState<Draft>({ ...blank, tenant: defaultTenant })
  const tenant = draft.tenant || tenants.data?.items[0]?.id || ''
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft({ ...draft, [key]: value })

  // The API reports every unusable field at once, by name.
  const fieldErrors = new Map<string, string>()
  if (create.error instanceof ApiError) {
    for (const e of create.error.fieldErrors) fieldErrors.set(e.field, e.message)
  }
  const errorOf = (field: keyof Draft) => fieldErrors.get(field)
  const described = (field: keyof Draft) => (errorOf(field) ? { 'aria-invalid': true, 'aria-describedby': `${id}-${field}-error` } : {})

  const submit = (e: FormEvent) => {
    e.preventDefault()
    create.mutate(toRule(draft, tenant), { onSuccess: onCreated })
  }

  const input = (field: keyof Draft, props: InputHTMLAttributes<HTMLInputElement> = {}) => (
    <input
      id={`${id}-${field}`}
      className={inputClass}
      value={draft[field]}
      onChange={(e) => set(field, e.target.value as never)}
      {...described(field)}
      {...props}
    />
  )

  return (
    <form onSubmit={submit} className="space-y-4 rounded-lg border border-accent/40 bg-sunken p-4" aria-labelledby={`${id}-title`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 id={`${id}-title`} className="font-semibold">
          New alert rule
        </h3>
        <Button variant="ghost" onClick={() => setDraft({ ...draft, ...failedLogins })}>
          Fill in the failed-login example
        </Button>
      </div>
      {create.isError && !fieldErrors.size && <ErrorNotice error={create.error} what="add the rule" />}
      {fieldErrors.size > 0 && <p className="text-sm text-danger">Some fields need attention.</p>}

      <fieldset className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <legend className="mb-2 text-xs font-semibold tracking-wide text-muted uppercase">Rule</legend>
        <Field label="Name" htmlFor={`${id}-name`} error={errorOf('name')}>
          {input('name', { required: true, maxLength: 200 })}
        </Field>
        <Field label="Tenant" htmlFor={`${id}-tenant`} error={errorOf('tenant')}>
          <select
            id={`${id}-tenant`}
            className={inputClass}
            value={tenant}
            onChange={(e) => set('tenant', e.target.value)}
            required
            {...described('tenant')}
          >
            {tenants.data?.items.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} ({t.id})
              </option>
            ))}
          </select>
        </Field>
      </fieldset>

      <fieldset className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <legend className="mb-2 text-xs font-semibold tracking-wide text-muted uppercase">Count events that match all of</legend>
        <Field
          label="Tags"
          htmlFor={`${id}-tags`}
          hint="Separate with commas. auth_failure marks a failed login from any source."
          error={errorOf('tags')}
        >
          {input('tags', { spellCheck: false })}
        </Field>
        <Field label="Source" htmlFor={`${id}-source`} error={errorOf('source')}>
          <select
            id={`${id}-source`}
            className={inputClass}
            value={draft.source}
            onChange={(e) => set('source', e.target.value as Source | '')}
            {...described('source')}
          >
            <option value="">Any source</option>
            {sources.map((s) => (
              <option key={s} value={s}>
                {sourceLabels[s]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Event type" htmlFor={`${id}-event_type`} hint="Matched exactly" error={errorOf('event_type')}>
          {input('event_type', { spellCheck: false })}
        </Field>
        <Field label="Action" htmlFor={`${id}-action`} hint="Such as login or deny" error={errorOf('action')}>
          {input('action', { spellCheck: false })}
        </Field>
        <Field label="Minimum severity" htmlFor={`${id}-severity_min`} hint="0 to 10" error={errorOf('severity_min')}>
          {input('severity_min', { type: 'number', min: 0, max: 10 })}
        </Field>
      </fieldset>

      <fieldset className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <legend className="mb-2 text-xs font-semibold tracking-wide text-muted uppercase">Raise an alert when</legend>
        <Field label="Events from one" htmlFor={`${id}-group_by`} error={errorOf('group_by')}>
          <select
            id={`${id}-group_by`}
            className={inputClass}
            value={draft.group_by}
            onChange={(e) => set('group_by', e.target.value as GroupBy)}
            {...described('group_by')}
          >
            {groupBys.map((g) => (
              <option key={g} value={g}>
                {fieldLabels[g]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Reach" htmlFor={`${id}-threshold`} hint="events or more" error={errorOf('threshold')}>
          {input('threshold', { type: 'number', min: 1, required: true })}
        </Field>
        <Field label="Within" htmlFor={`${id}-window_minutes`} hint="minutes, up to 1440" error={errorOf('window_minutes')}>
          {input('window_minutes', { type: 'number', min: 1, max: 1440, required: true })}
        </Field>
        <Field
          label="Then stay quiet for"
          htmlFor={`${id}-cooldown_minutes`}
          hint="minutes; blank for the window"
          error={errorOf('cooldown_minutes')}
        >
          {input('cooldown_minutes', { type: 'number', min: 0, max: 10080 })}
        </Field>
      </fieldset>

      <Field
        label="Webhook URL (optional)"
        htmlFor={`${id}-webhook_url`}
        hint="Each alert is also POSTed here as JSON. Only admins can see it once saved."
        error={errorOf('webhook_url')}
      >
        {input('webhook_url', { type: 'url', placeholder: 'https://hooks.example.com/loghub', spellCheck: false })}
      </Field>

      <div className="flex gap-2">
        <Button type="submit" variant="primary" disabled={create.isPending || !tenant}>
          {create.isPending ? 'Adding…' : 'Add rule'}
        </Button>
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  )
}
