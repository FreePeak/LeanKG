import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { fileURLToPath } from 'node:url';

// The Go side embeds dist/ and serves it from "/", so asset URLs stay absolute
// from the root. Routing is hash-based, so the server needs no SPA fallback.
const DASHBOARD_TARGET = process.env.DASHBOARD_TARGET ?? 'http://127.0.0.1:9701';

export default defineConfig({
  base: '/',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    host: '127.0.0.1',
    port: Number(process.env.PORT) || 5174,
    proxy: {
      '/api': { target: DASHBOARD_TARGET, changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsDir: 'assets',
  },
});
