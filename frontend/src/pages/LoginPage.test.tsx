import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { session } from '../api/session'
import { adminSession, apiError, json, mockApi, requests, tenants } from '../test/api'
import { renderApp } from '../test/render'

describe('signing in', () => {
  it('is where a visitor without a session is sent, and returns them', async () => {
    mockApi({
      'POST /api/v1/auth/login': () => json(adminSession),
      'GET /api/v1/tenants': () => json(tenants),
      'GET /api/v1/alerts': () => json({ items: [] }),
      'GET /api/v1/alert-rules': () => json({ items: [] }),
    })
    const { router } = renderApp('/alerts?tenant=demoB')
    await screen.findByRole('heading', { name: 'Sign in' })

    await userEvent.type(screen.getByLabelText('Email'), ' admin@loghub.local ')
    await userEvent.type(screen.getByLabelText('Password'), 'secret')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    await screen.findByRole('heading', { name: 'Alerts' })
    expect(router.state.location.pathname + router.state.location.search).toBe('/alerts?tenant=demoB')
    expect(await requests[0].json()).toEqual({ email: 'admin@loghub.local', password: 'secret' })
    expect(session.get()).toEqual(adminSession)
  })

  it('says when the password is wrong', async () => {
    mockApi({ 'POST /api/v1/auth/login': () => apiError(401, 'invalid_credentials', 'email or password is incorrect') })
    renderApp('/login')
    await userEvent.type(screen.getByLabelText('Email'), 'admin@loghub.local')
    await userEvent.type(screen.getByLabelText('Password'), 'wrong')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The email or password is incorrect.')
    expect(session.get()).toBeNull()
  })

  it('explains an ended session', async () => {
    mockApi({
      'GET /api/v1/tenants': () => apiError(401, 'invalid_token', 'the token is invalid or has expired; sign in again'),
      'GET /api/v1/alerts': () => apiError(401, 'invalid_token', 'the token is invalid or has expired; sign in again'),
      'GET /api/v1/alert-rules': () => apiError(401, 'invalid_token', 'the token is invalid or has expired; sign in again'),
    })
    session.start(adminSession)
    renderApp('/alerts')
    await screen.findByRole('heading', { name: 'Sign in' })
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Your session is no longer valid.'))
  })

  it('shows why the server failed', async () => {
    mockApi({ 'POST /api/v1/auth/login': () => apiError(503, 'database_unavailable', 'cannot reach the database') })
    renderApp('/login')
    await userEvent.type(screen.getByLabelText('Email'), 'admin@loghub.local')
    await userEvent.type(screen.getByLabelText('Password'), 'x')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't sign in. cannot reach the database")
    expect(alert).toHaveTextContent('Request ID req-1')
  })
})
