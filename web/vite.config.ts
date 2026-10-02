import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Dev: Vite on :5173 proxies API calls to the Go server on :8080 (make dev-server).
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/ws': { target: 'ws://127.0.0.1:8080', ws: true },
    },
  },
  build: { outDir: 'dist', emptyOutDir: true },
})
