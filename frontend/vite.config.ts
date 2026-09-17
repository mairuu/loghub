/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    // `make dev-up` or `make dev-api` serves the API here. In production
    // Caddy serves both from one origin, as this does.
    proxy: { '/api': 'http://127.0.0.1:8080' },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    // Not UTC, so a time shown in the wrong zone fails a test. Bangkok has no
    // daylight saving, so the offset is always +07:00.
    env: { TZ: 'Asia/Bangkok' },
  },
})
