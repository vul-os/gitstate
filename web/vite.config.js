import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  // base '/' ensures assets load from /assets/... when served by the Go binary via go:embed.
  // BrowserRouter works because the Go server falls back to index.html for all non-asset paths.
  base: '/',
  // A10: read shared root .env / .env.dev; only VITE_* are exposed to the browser
  envDir: '..',
  plugins: [
    tailwindcss(),
    react(),
  ],
})
