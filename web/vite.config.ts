/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// During development, run the Go server on :8080 and `npm run dev` here.
const backend = process.env.SIMPLEREMOTE_DEV_BACKEND ?? 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    proxy: {
      '/api': backend,
      '/auth': backend,
      '/d': backend,
      '/ws': { target: backend.replace(/^http/, 'ws'), ws: true },
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
  },
});
