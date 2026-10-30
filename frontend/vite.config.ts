import { defineConfig } from 'vite'

// The editor calls the backend at /api; in development that is proxied to
// `flowforge serve` (docs/api-v1.md).
export default defineConfig({
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8090',
    },
  },
})
