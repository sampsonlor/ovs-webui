import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  plugins: [svelte()],
  build: {
    outDir: '../internal/web/assets/spa',
    emptyOutDir: true,
    sourcemap: false,
    target: 'es2022',
  },
});
