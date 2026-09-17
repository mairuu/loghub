import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { routes } from '../routes'

/** Renders the whole UI at path, as the browser would after navigating there. */
export function renderApp(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchInterval: false } } })
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { ...view, router, client }
}
