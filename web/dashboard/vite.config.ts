import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// The dashboard is served by core at /admin/ and embedded into its binary
// from dist/app (see embed.go). `npm run dev` proxies the API to a running
// core (BOBRES_DEV_API, default http://localhost:8088).
export default defineConfig({
  base: '/admin/',
  plugins: [vue()],
  build: {
    outDir: 'dist/app',
    emptyOutDir: true,
    sourcemap: false,
    // No inline scripts: the dashboard runs under a self-only CSP.
    modulePreload: { polyfill: false },
    chunkSizeWarningLimit: 900,
  },
  server: {
    proxy: { '/api': process.env.BOBRES_DEV_API ?? 'http://localhost:8088' },
  },
  test: {
    environment: 'jsdom',
  },
})
