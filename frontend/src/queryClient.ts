import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './api/client'
import { session } from './api/session'

export function newQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // A refusal or a timeout won't go away by asking again at once.
        retry: (failures, error) => failures < 2 && !(error instanceof ApiError && (error.status < 500 || error.code === 'search_timeout')),
      },
    },
  })
}

/** Forgets one user's data before the next can see it. */
export function clearOnSignOut(client: QueryClient): () => void {
  return session.subscribe(() => {
    if (!session.snapshot()) client.clear()
  })
}
