import { useId, useState } from 'react'
import { Link } from 'react-router'
import { sourceLabels, sources, type Source } from '../api/enums'
import { useTenants, useUpload } from '../api/queries'
import { Badge, Button, Card, ErrorNotice, Field, inputClass, Notice, PageHeader } from '../components/ui'
import { cx } from '../lib/classes'
import { defaultFilters, writeFilters } from '../lib/filters'
import { formatBytes, formatCount } from '../lib/format'
import { toNdjson, type Upload } from '../lib/ndjson'

// The ingest endpoint refuses a larger body.
const maxBody = 32 * 1024 * 1024

interface Chosen extends Upload {
  name: string
  size: number
}

export function UploadPage() {
  const id = useId()
  const tenants = useTenants()
  const upload = useUpload()
  const [tenant, setTenant] = useState('')
  const [source, setSource] = useState<Source | ''>('')
  const [file, setFile] = useState<Chosen | null>(null)
  const [reading, setReading] = useState<string | null>(null)
  const [dragging, setDragging] = useState(false)

  const choose = async (f: File | undefined) => {
    upload.reset()
    setFile(null)
    if (!f) return
    setReading(null)
    try {
      setFile({ ...toNdjson(await f.text()), name: f.name, size: f.size })
    } catch {
      setReading(`${f.name} couldn't be read as text.`)
    }
  }
  const tooLarge = file !== null && new Blob([file.body]).size > maxBody
  const result = upload.data
  const searchLink = writeFilters({ ...defaultFilters, range: { kind: 'preset', preset: '15m' }, tenant, sources: source ? [source] : [] })

  return (
    <>
      <PageHeader
        title="Upload events"
        description="Send a vendor export, such as an AWS, Microsoft 365 or Active Directory file, straight to the normalizer."
      />
      <div className="grid gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
        <Card title="File">
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault()
              if (file && !tooLarge) upload.mutate({ body: file.body, tenant, source })
            }}
          >
            {/* oxlint-disable-next-line jsx-a11y/no-noninteractive-element-interactions -- dropping is a shortcut; the input it labels is the accessible way in */}
            <label
              htmlFor={`${id}-file`}
              onDragOver={(e) => {
                e.preventDefault()
                setDragging(true)
              }}
              onDragLeave={() => setDragging(false)}
              onDrop={(e) => {
                e.preventDefault()
                setDragging(false)
                void choose(e.dataTransfer.files[0])
              }}
              className={cx(
                'flex cursor-pointer flex-col items-center justify-center gap-1 rounded-lg border-2 border-dashed px-4 py-8 text-center',
                dragging ? 'border-accent bg-accent-soft' : 'border-line hover:border-accent',
              )}
            >
              <span className="text-sm font-medium">{file ? file.name : 'Choose a file or drop it here'}</span>
              <span className="text-xs text-muted">
                {file
                  ? `${formatBytes(file.size)} · ${formatCount(file.records)} record${file.records === 1 ? '' : 's'}`
                  : 'NDJSON, one JSON object, or a JSON array of objects'}
              </span>
              <input
                id={`${id}-file`}
                type="file"
                accept=".json,.ndjson,.jsonl,.log,.txt,application/json,application/x-ndjson"
                className="sr-only"
                onChange={(e) => void choose(e.target.files?.[0])}
              />
            </label>
            {reading && <Notice tone="danger">{reading}</Notice>}
            {tooLarge && <Notice tone="danger">This file is over 32 MiB once converted. Split it and upload the parts.</Notice>}

            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Tenant" htmlFor={`${id}-tenant`} hint="For records that don't name one">
                <select id={`${id}-tenant`} className={inputClass} value={tenant} onChange={(e) => setTenant(e.target.value)}>
                  <option value="">None: records name their own</option>
                  {tenants.data?.items.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name} ({t.id})
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="Source" htmlFor={`${id}-source`} hint="For records that don't name one">
                <select
                  id={`${id}-source`}
                  className={inputClass}
                  value={source}
                  onChange={(e) => setSource(e.target.value as Source | '')}
                >
                  <option value="">None: records name their own</option>
                  {sources.map((s) => (
                    <option key={s} value={s}>
                      {sourceLabels[s]}
                    </option>
                  ))}
                </select>
              </Field>
            </div>
            <Button type="submit" variant="primary" disabled={!file || tooLarge || upload.isPending}>
              {upload.isPending ? 'Uploading…' : 'Upload'}
            </Button>
          </form>
        </Card>

        <Card title="Result">
          {upload.isError ? (
            <ErrorNotice error={upload.error} what="upload the file" />
          ) : !result ? (
            <p className="text-sm text-muted">
              Each record is normalized on its own. Records that can't be stored are listed here with the reason, and the rest are stored.
              Samples are in <code className="font-mono text-xs">samples/json</code>.
            </p>
          ) : (
            <div className="space-y-3">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge tone="ok">{formatCount(result.accepted)} stored</Badge>
                <Badge tone={result.rejected ? 'danger' : 'neutral'}>{formatCount(result.rejected)} rejected</Badge>
                {result.accepted > 0 && (
                  <Link to={{ pathname: '/search', search: searchLink.toString() }} className="text-accent hover:underline">
                    Search recent events
                  </Link>
                )}
              </div>
              {result.errors && result.errors.length > 0 && (
                <div className="-mx-4 overflow-x-auto">
                  <table className="w-full text-left text-sm">
                    <caption className="px-4 pb-2 text-left text-xs text-muted">
                      {result.errors.length < result.rejected ? `The first ${result.errors.length} rejections` : 'Rejections'}
                    </caption>
                    <thead className="border-b border-line text-xs text-muted">
                      <tr>
                        <th scope="col" className="py-2 pr-2 pl-4 font-medium">
                          Line
                        </th>
                        <th scope="col" className="px-2 py-2 font-medium">
                          Reason
                        </th>
                        <th scope="col" className="py-2 pr-4 pl-2 font-medium">
                          Detail
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {result.errors.map((e) => (
                        <tr key={e.index} className="border-b border-line last:border-0">
                          <td className="py-1.5 pr-2 pl-4 tabular-nums">{e.index + 1}</td>
                          <td className="px-2 py-1.5 font-mono text-xs">{e.code}</td>
                          <td className="py-1.5 pr-4 pl-2">{e.message}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          )}
        </Card>
      </div>
    </>
  )
}
