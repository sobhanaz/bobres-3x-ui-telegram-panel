import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// The Mini App is served by core at /app/ and embedded into its binary from
// dist/app (see embed.go). `npm run dev` proxies the API to a running core
// (BOBRES_DEV_API, default http://localhost:8088); open it from Telegram with
// a tunnel, or paste init data into the URL hash (#tgWebAppData=...).
export default defineConfig({
  base: '/app/',
  plugins: [vue()],
  build: {
    outDir: 'dist/app',
    emptyOutDir: true,
    sourcemap: false,
    // No inline scripts: the app runs under a CSP that allows only itself
    // and Telegram's script.
    modulePreload: { polyfill: false },
  },
  server: {
    proxy: { '/api': process.env.BOBRES_DEV_API ?? 'http://localhost:8088' },
  },
  test: {
    environment: 'jsdom',
  },
})
