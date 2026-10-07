import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// `npm run dev` proxies the API to a local xdocker server on :8790.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../backend/internal/web/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 900,
  },
  server: {
    proxy: {
      '/api': { target: 'http://localhost:8790', ws: true },
    },
  },
});
