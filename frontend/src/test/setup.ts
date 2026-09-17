import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'
import { session } from '../api/session'
import { fakeFetch, mockApi } from './api'

globalThis.fetch = fakeFetch

// Recharts measures its container; jsdom has no layout, so nothing is drawn.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

afterEach(() => {
  cleanup()
  session.end('signed_out')
  sessionStorage.clear()
  localStorage.clear()
  mockApi({})
})
