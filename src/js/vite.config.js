import { fileURLToPath, URL } from 'node:url';

import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import vueDevTools from 'vite-plugin-vue-devtools';
import { visualizer } from 'rollup-plugin-visualizer';

// https://vite.dev/config/
export default defineConfig({
  base: '/', //TODO
  assetsDir: 'assets',
  plugins: [vue(), vueDevTools()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    rollupOptions: {
      plugins: [
        visualizer({ open: true, filename: './analysis/bundle-analysis.html' }),
      ],
    },
  },
  server: {
    proxy: {
      '/api/v1': {
        target: 'http://localhost:3000',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true,
      },
      '/version': {
        target: 'http://localhost:3000',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true,
      },
      '/features': {
        target: 'http://localhost:3000',
        changeOrigin: true,
        logLevel: 'debug',
        ws: true,
      },
    },
  },
  css: {
    preprocessorOptions: {
      scss: {
        api: 'modern',
        // These are all caused by using Bulma 0.9.4. Buefy-next doesn't seem to support Bulma v1 yet
        silenceDeprecations: ['import', 'color-functions', 'global-builtin'],
      },
    },
  },
});
