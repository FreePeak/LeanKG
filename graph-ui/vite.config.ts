import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  // FR-E43 — relative base so the built SPA works when rust_embed serves it
  // under /3d/ and also standalone in dev/preview.
  base: './',
  server: {
    port: 5174,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
});
