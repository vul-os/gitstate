import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  // A10: read shared root .env / .env.dev; only VITE_* are exposed to the browser
  envDir: '..',
  plugins: [
    tailwindcss(),
    react(),
  ],
})
