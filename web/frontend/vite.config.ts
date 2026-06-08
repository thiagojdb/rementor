import { defineConfig } from 'vite'
import solid from 'vite-plugin-solid'

export default defineConfig({
  plugins: [solid()],
  build: {
    outDir: '../../cmd/server/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/rpc': 'http://127.0.0.1:9300',
      '/healthz': 'http://127.0.0.1:9300',
    },
  },
})
