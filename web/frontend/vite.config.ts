import { fileURLToPath, URL } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const controlPlaneOrigin = 'http://127.0.0.1:9300'

function controlPlaneCSRFToken() {
  return {
    name: 'rementor-control-plane-csrf',
    apply: 'serve' as const,
    transformIndexHtml: {
      order: 'pre' as const,
      async handler(html: string) {
        try {
          const response = await fetch(controlPlaneOrigin)
          const document = await response.text()
          const token = document.match(/<meta name="rementor-csrf" content="([^"]*)"/i)?.[1] || ''
          return html.replace('%REMENTOR_CSRF_TOKEN%', token)
        } catch {
          return html.replace('%REMENTOR_CSRF_TOKEN%', '')
        }
      },
    },
  }
}

export default defineConfig({
  plugins: [react(), controlPlaneCSRFToken()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: '../../cmd/server/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/rpc': controlPlaneOrigin,
      '/healthz': controlPlaneOrigin,
    },
  },
})
