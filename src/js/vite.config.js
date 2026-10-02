import { fileURLToPath, URL } from 'node:url';

import { defineConfig, loadEnv } from 'vite';
import vue from '@vitejs/plugin-vue';
import vueDevTools from 'vite-plugin-vue-devtools';

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  process.env = {
    ...process.env,
    ...loadEnv(mode, process.cwd()),
    VITE_FAVICON: mode === 'development' ? '/favicon_dev.ico' : '/favicon.ico',
  };
  return {
    // normalize to a trailing slash: import.meta.env.BASE_URL is replaced
    // with this raw value (not Vite's slash-normalized resolved base), and
    // code concatenates paths onto it (e.g. `${BASE_URL}api/v1/`)
    base: (process.env.VITE_BASE_PATH || '/').replace(/\/?$/, '/'),
    assetsDir: 'assets',
    plugins: [vue(), vueDevTools()],
    resolve: {
      alias: [
        {
          find: '@',
          replacement: fileURLToPath(new URL('./src', import.meta.url)),
        },
        // buefy's "module" entry is a single pre-bundled file that rollup
        // cannot tree-shake; its per-component ESM build can be
        {
          find: /^buefy$/,
          replacement: fileURLToPath(
            new URL('./node_modules/buefy/dist/esm/index.js', import.meta.url),
          ),
        },
      ],
    },
    // applies to npm run dev
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
        // WebShark, which the server serves when it is installed
        '/webshark': {
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
    // vitest: the Playwright suite in e2e/ and the perf/ tooling have their own runners
    test: {
      exclude: ['e2e/**', 'perf/**', 'node_modules/**', 'dist/**'],
    },
  };
});
