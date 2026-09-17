import type { RouteObject } from 'react-router'
import { RequireAdmin, RequireSession } from './components/guards'
import { Layout } from './components/Layout'
import { PageLoading } from './components/ui'
import { LoginPage } from './pages/LoginPage'
import { NotFoundPage } from './pages/NotFoundPage'

// Pages load when first visited, so signing in doesn't wait for the chart
// library.
export const routes: RouteObject[] = [
  {
    HydrateFallback: PageLoading,
    children: [
      { path: '/login', element: <LoginPage /> },
      {
        element: (
          <RequireSession>
            <Layout />
          </RequireSession>
        ),
        children: [
          { index: true, lazy: async () => ({ Component: (await import('./pages/DashboardPage')).DashboardPage }) },
          { path: 'search', lazy: async () => ({ Component: (await import('./pages/SearchPage')).SearchPage }) },
          { path: 'alerts', lazy: async () => ({ Component: (await import('./pages/AlertsPage')).AlertsPage }) },
          {
            path: 'upload',
            lazy: async () => {
              const { UploadPage } = await import('./pages/UploadPage')
              return {
                element: (
                  <RequireAdmin>
                    <UploadPage />
                  </RequireAdmin>
                ),
              }
            },
          },
          { path: '*', element: <NotFoundPage /> },
        ],
      },
    ],
  },
]
