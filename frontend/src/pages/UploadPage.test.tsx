import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { session } from '../api/session'
import { adminSession, json, mockApi, requests, tenants, viewerSession } from '../test/api'
import { renderApp } from '../test/render'

describe('upload', () => {
  it('sends a pretty-printed file as NDJSON, with the defaults chosen', async () => {
    mockApi({
      'GET /api/v1/tenants': () => json(tenants),
      'GET /api/v1/alerts': () => json({ items: [] }),
      'POST /api/v1/ingest/file': () =>
        json({ accepted: 1, rejected: 1, errors: [{ index: 1, code: 'unknown_tenant', message: 'tenant "nope" does not exist' }] }),
    })
    session.start(adminSession)
    renderApp('/upload')

    const file = new File([JSON.stringify([{ source: 'aws', event_type: 'CreateUser' }, { tenant: 'nope' }], null, 2)], 'trail.json', {
      type: 'application/json',
    })
    await userEvent.upload(await screen.findByLabelText(/Choose a file/), file)
    expect(await screen.findByText(/2 records/)).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByRole('option', { name: 'Demo B (demoB)' })).toHaveLength(1))
    await userEvent.selectOptions(screen.getByLabelText('Tenant'), 'demoB')
    await userEvent.selectOptions(screen.getByLabelText('Source'), 'aws')
    await userEvent.click(screen.getByRole('button', { name: 'Upload' }))

    expect(await screen.findByText('1 stored')).toBeInTheDocument()
    expect(screen.getByText('1 rejected')).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'tenant "nope" does not exist' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Search recent events' })).toHaveAttribute('href', '/search?range=15m&tenant=demoB&source=aws')

    const sent = requests.find((r) => r.method === 'POST')!
    expect(sent.headers.get('Content-Type')).toBe('application/x-ndjson')
    expect(new URL(sent.url).search).toBe('?tenant=demoB&source=aws')
    expect(await sent.text()).toBe('{"source":"aws","event_type":"CreateUser"}\n{"tenant":"nope"}')
  })

  it('is only for admins', async () => {
    mockApi({ 'GET /api/v1/tenants': () => json(tenants), 'GET /api/v1/alerts': () => json({ items: [] }) })
    session.start(viewerSession)
    renderApp('/upload')
    expect(await screen.findByText('Only an admin can use this page.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Upload' })).toBeNull()
  })
})
