import { fileURLToPath, URL } from 'node:url';

import { defineConfig, loadEnv } from 'vite';
import vue from '@vitejs/plugin-vue';
import vueDevTools from 'vite-plugin-vue-devtools';

import builderAssets from './plugins/builder-assets.js';

// Vitest leaves out the Playwright suite in e2e/, which has its own runner.
const TEST_EXCLUDE = ['e2e/**', 'node_modules/**', 'dist/**'];

// Suites of pure functions that mock nothing and leave no state behind. They
// share workers and run in any order (`--project contract --sequence.shuffle`).
const CONTRACT_TESTS = ['test/**/*.contract.test.js'];

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
    plugins: [
      vue(),
      vueDevTools(),
      // Compresses the files only Builder loads, for the server
      // (src/go/web/builder_assets.go), and lists them.
      builderAssets({
        view: fileURLToPath(
          new URL('./src/views/Builder.vue', import.meta.url),
        ),
      }),
    ],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
    // applies to npm run dev
    server: {
      proxy: {
        '/api/v1': {
          target: 'http://localhost:3000',
          changeOrigin: true,
          ws: true,
        },
        '/version': {
          target: 'http://localhost:3000',
          changeOrigin: true,
          ws: true,
        },
        '/features': {
          target: 'http://localhost:3000',
          changeOrigin: true,
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
    // The contract tests share workers, and every other file gets its own.
    // A contract maxWorkers would make Vitest run the projects one by one.
    test: {
      exclude: TEST_EXCLUDE,
      // A test that mounts the Inspector takes up to 5.4 s on a CI runner with
      // coverage, which is past the 5 s default.
      testTimeout: 20_000,
      // Measured only with --coverage (npm run test:coverage), as CI does.
      coverage: {
        provider: 'v8',
        include: ['src/**'],
        reporter: ['text-summary', 'json-summary', 'html'],
      },
      projects: [
        {
          extends: true,
          test: { name: 'contract', include: CONTRACT_TESTS, isolate: false },
        },
        {
          extends: true,
          test: {
            name: 'unit',
            exclude: [...TEST_EXCLUDE, ...CONTRACT_TESTS],
            setupFiles: ['test/unit-setup.js'],
          },
        },
      ],
    },
  };
});
