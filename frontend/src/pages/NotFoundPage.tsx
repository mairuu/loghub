import { Link } from 'react-router'
import { EmptyState, PageHeader } from '../components/ui'

export function NotFoundPage() {
  return (
    <>
      <PageHeader title="Page not found" />
      <EmptyState>
        Nothing is at this address.{' '}
        <Link to="/" className="text-accent hover:underline">
          Go to the dashboard
        </Link>
      </EmptyState>
    </>
  )
}
