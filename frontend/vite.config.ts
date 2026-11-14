import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { loadEnv } from 'vite'
import { defineConfig } from 'vitest/config'

// The browser always talks to the same origin. In development Vite proxies
// /api to the Go backend, so the refresh cookie stays first-party and the
// frontend never needs a hardcoded backend origin.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const backend = env.VITE_DEV_PROXY_TARGET || 'http://localhost:8080'
  const pluginOrigin = env.VITE_PLUGIN_IFRAME_ORIGIN || 'http://127.0.0.2:8091'
  const csp = `frame-src ${pluginOrigin}`

  return {
    plugins: [vue(), tailwindcss()],
    resolve: {
      alias: {
        '@': path.resolve(import.meta.dirname, './src'),
      },
    },
    server: {
      headers: {
        'Content-Security-Policy': csp,
      },
      proxy: {
        '/api': {
          target: backend,
          changeOrigin: false,
        },
      },
    },
    preview: {
      headers: {
        'Content-Security-Policy': csp,
      },
    },
    test: {
      environment: 'happy-dom',
      include: ['src/**/*.test.ts'],
      restoreMocks: true,
    },
  }
})