import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  // base '/' ensures assets load from /assets/... when the daemon serves web/dist.
  // BrowserRouter works because the daemon falls back to index.html for all non-/api paths.
  base: '/',
  // Read shared root .env / .env.dev; only VITE_* are exposed to the browser.
  envDir: '..',
  plugins: [
    tailwindcss(),
    react(),
  ],
  build: {
    // Route-splitting (React.lazy in App.jsx) keeps the entry chunk small;
    // bump the warning ceiling so legitimately-grouped vendor code is quiet.
    chunkSizeWarningLimit: 700,
    rollupOptions: {
      output: {
        // Group the stable React/router runtime into one long-cached vendor
        // chunk, separate from per-route app code that changes frequently.
        manualChunks(id) {
          if (/node_modules\/(react|react-dom|react-router|react-router-dom|scheduler)\//.test(id)) {
            return 'react-vendor'
          }
        },
      },
    },
  },
  // Dev: the frontend uses relative API paths; proxy them to a locally-run
  // `gitstated` daemon (default port 7473) so `npm run dev` hits real data with
  // no cross-origin/CORS concern. Start it with `gitstate serve` (or `cargo run
  // -p gitstate-cli -- serve`) alongside `npm run dev`.
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:7473',
      '/health': 'http://localhost:7473',
    },
  },
})
