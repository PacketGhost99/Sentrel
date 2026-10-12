import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

// In development, `npm run dev` proxies API calls to a running server.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src') } },
  server: {
    proxy: {
      '/api': { target: process.env.HEARTBEAT_URL || 'http://localhost:8080', changeOrigin: true },
      '/download': { target: process.env.HEARTBEAT_URL || 'http://localhost:8080', changeOrigin: true },
      '/install': { target: process.env.HEARTBEAT_URL || 'http://localhost:8080', changeOrigin: true },
    },
  },
  test: { environment: 'node' },
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 1200, sourcemap: false },
})
