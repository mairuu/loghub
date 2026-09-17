import { useMutation } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { Navigate, useLocation } from 'react-router'
import { ApiError, api, unwrap } from '../api/client'
import { session, useSession, type EndReason } from '../api/session'
import { Button, ErrorNotice, Field, inputClass, Notice } from '../components/ui'

const endings: Record<EndReason, string | null> = {
  signed_out: null,
  expired: 'Your session expired. Sign in again to continue.',
  refused: 'Your session is no longer valid. Sign in again to continue.',
}

export function LoginPage() {
  const id = useId()
  const current = useSession()
  const location = useLocation()
  // Where the user was sent from, to go back to.
  const from = (location.state as { from?: string } | null)?.from ?? '/'
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [ended] = useState(() => session.endedBecause())

  const login = useMutation({
    mutationFn: () => unwrap(api.POST('/api/v1/auth/login', { body: { email: email.trim(), password } })),
    // Starting the session renders the redirect below.
    onSuccess: (s) => session.start(s),
  })

  if (current) return <Navigate to={from} replace />

  const wrongPassword = login.error instanceof ApiError && login.error.code === 'invalid_credentials'

  return (
    <main className="grid min-h-screen place-items-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center justify-center gap-2 text-2xl font-semibold tracking-tight">
          <img src="/favicon.svg" alt="" className="size-8" />
          loghub
        </div>
        <form
          className="space-y-4 rounded-lg border border-line bg-surface p-6 shadow-sm"
          onSubmit={(e) => {
            e.preventDefault()
            login.mutate()
          }}
        >
          <h1 className="text-lg font-semibold">Sign in</h1>
          {ended && endings[ended] && !login.isError && <Notice tone="warn">{endings[ended]}</Notice>}
          {wrongPassword ? (
            <Notice tone="danger">The email or password is incorrect.</Notice>
          ) : (
            login.isError && <ErrorNotice error={login.error} what="sign in" />
          )}
          <Field label="Email" htmlFor={`${id}-email`}>
            <input
              id={`${id}-email`}
              type="email"
              autoComplete="username"
              required
              className={inputClass}
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              aria-invalid={wrongPassword || undefined}
            />
          </Field>
          <Field label="Password" htmlFor={`${id}-password`}>
            <input
              id={`${id}-password`}
              type="password"
              autoComplete="current-password"
              required
              className={inputClass}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              aria-invalid={wrongPassword || undefined}
            />
          </Field>
          <Button type="submit" variant="primary" className="w-full" disabled={login.isPending}>
            {login.isPending ? 'Signing in…' : 'Sign in'}
          </Button>
        </form>
        <p className="mt-4 text-center text-xs text-muted">
          The appliance's accounts and passwords are in its <code className="font-mono">.env</code>; see the setup guide.
        </p>
      </div>
    </main>
  )
}
