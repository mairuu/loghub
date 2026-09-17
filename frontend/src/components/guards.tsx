import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router'
import { useSession } from '../api/session'
import { EmptyState, PageHeader } from './ui'

export function RequireSession({ children }: { children: ReactNode }) {
  const s = useSession()
  const location = useLocation()
  if (!s) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  return children
}

// The API refuses a viewer anyway; this only spares them a page that can't work.
export function RequireAdmin({ children }: { children: ReactNode }) {
  const s = useSession()
  if (s?.role !== 'admin') {
    return (
      <>
        <PageHeader title="Admins only" />
        <EmptyState>Only an admin can use this page.</EmptyState>
      </>
    )
  }
  return children
}
