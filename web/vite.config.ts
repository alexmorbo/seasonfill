import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import path from 'node:path';

const BACKEND = process.env.VITE_BACKEND_URL ?? 'http://localhost:8080';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, 'src') } },
  server: {
    port: 5173, strictPort: true,
    proxy: {
      '/api':     { target: BACKEND, changeOrigin: true },
      '/webhook': { target: BACKEND, changeOrigin: true },
    },
  },
  build: { outDir: 'dist', sourcemap: true, target: 'es2022' },
  test: {
    environment: 'jsdom', globals: true,
    setupFiles: ['./src/test-setup.ts'], css: false,
    clearMocks: true,
    coverage: { provider: 'v8', reporter: ['text', 'html'] },
  },
});
