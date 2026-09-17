import { NavLink, Outlet, useLocation } from 'react-router'
import { useTenants } from '../api/queries'
import { session, useSession } from '../api/session'
import { useUnseenAlerts } from '../api/unseenAlerts'
import { cx } from '../lib/classes'
import { Button } from './ui'

// The dashboard and search read the same filters, so moving between them
// keeps the query string.
const filterPages = ['/', '/search']

export function Layout() {
  const s = useSession()
  const location = useLocation()
  const tenants = useTenants()
  const { unseen } = useUnseenAlerts()
  const isAdmin = s?.role === 'admin'
  const carried = filterPages.includes(location.pathname) ? location.search : ''
  const tenantName = tenants.data?.items.find((t) => t.id === s?.tenant)?.name ?? s?.tenant

  const links = [
    { to: { pathname: '/', search: carried }, label: 'Dashboard', end: true },
    { to: { pathname: '/search', search: carried }, label: 'Search' },
    { to: { pathname: '/alerts' }, label: 'Alerts', dot: unseen },
    ...(isAdmin ? [{ to: { pathname: '/upload' }, label: 'Upload' }] : []),
  ]

  return (
    <div className="min-h-screen">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:rounded focus:bg-surface focus:px-3 focus:py-2"
      >
        Skip to content
      </a>
      <header className="sticky top-0 z-10 border-b border-line bg-surface/90 backdrop-blur">
        <div className="mx-auto flex max-w-[1400px] flex-wrap items-center gap-x-6 gap-y-2 px-4 py-2.5">
          <NavLink to="/" className="flex items-center gap-2 font-semibold tracking-tight">
            <img src="/favicon.svg" alt="" className="size-6" />
            loghub
          </NavLink>
          <nav aria-label="Main" className="flex flex-wrap gap-1">
            {links.map((l) => (
              <NavLink
                key={l.label}
                to={l.to}
                end={l.end}
                aria-label={l.dot ? `${l.label}, new alerts` : undefined}
                className={({ isActive }) =>
                  cx(
                    'relative rounded-md px-3 py-1.5 text-sm font-medium',
                    isActive ? 'bg-accent-soft text-accent' : 'text-muted hover:bg-sunken hover:text-fg',
                  )
                }
              >
                {l.label}
                {l.dot && <span aria-hidden className="absolute top-1 right-1 size-2 rounded-full bg-danger" />}
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3 text-sm">
            <span className="text-muted">
              {isAdmin ? (
                <>
                  <span className="font-medium text-fg">Admin</span> · all tenants
                </>
              ) : (
                <>
                  <span className="font-medium text-fg">Viewer</span> · {tenantName}
                </>
              )}
            </span>
            <a href="/api/docs" className="text-muted hover:text-fg hover:underline" target="_blank" rel="noreferrer">
              API docs
            </a>
            <Button variant="ghost" onClick={() => session.end('signed_out')}>
              Sign out
            </Button>
          </div>
        </div>
      </header>
      <main id="main" className="mx-auto max-w-[1400px] space-y-4 px-4 py-5">
        <Outlet />
      </main>
    </div>
  )
}
