import { fileURLToPath, URL } from 'node:url';

import { defineConfig, loadEnv } from 'vite';
import vue from '@vitejs/plugin-vue';
import vueDevTools from 'vite-plugin-vue-devtools';

import builderAssets from './plugins/builder-assets.js';
import builderPrefetch from './plugins/builder-prefetch.js';

// Vitest leaves out the Playwright suite in e2e/ and the perf/ tooling,
// which have their own runners.
const TEST_EXCLUDE = ['e2e/**', 'perf/**', 'node_modules/**', 'dist/**'];

// Suites of pure functions that mock nothing and leave no state behind. They
// share workers and run in any order (`--project contract --sequence.shuffle`).
const CONTRACT_TESTS = ['test/**/*.contract.test.js'];

// noVNC 1.7.0 uses top-level await, so the build targets its own browser
// minimums instead of the Vite default (es2020, Safari 14).
const BROWSER_TARGET = ['es2022', 'chrome89', 'firefox89', 'safari15'];

const BUILDER_VIEW = fileURLToPath(
  new URL('./src/views/Builder.vue', import.meta.url),
);

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
      // Compresses the files only Builder loads, and the ELK files it shares
      // with State of Health, for the server (src/go/web/builder_assets.go).
      builderAssets({
        view: BUILDER_VIEW,
        shared: /[\\/]node_modules[\\/]elkjs[\\/]/,
      }),
      // The list in index.html of the files that the UI prefetches for
      // Builder: its chunks and styles, and the files of the modules below.
      builderPrefetch({
        view: BUILDER_VIEW,
        // What src/builder/layouts/elk.js and src/builder/grouping.js import.
        modules: [
          fileURLToPath(
            new URL('./node_modules/elkjs/lib/elk-api.js', import.meta.url),
          ),
          `${fileURLToPath(new URL('./node_modules/elkjs/lib/elk-worker.min.js', import.meta.url))}?url`,
          `${fileURLToPath(new URL('./src/builder/groupingWorker.js', import.meta.url))}?worker`,
        ],
      }),
    ],
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
    build: { target: BROWSER_TARGET },
    optimizeDeps: { esbuildOptions: { target: BROWSER_TARGET } },
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
        // health endpoints, served from the server root
        '/healthz': {
          target: 'http://localhost:3000',
          changeOrigin: true,
          logLevel: 'debug',
        },
        '/readyz': {
          target: 'http://localhost:3000',
          changeOrigin: true,
          logLevel: 'debug',
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
    // The contract tests share workers, and every other file gets its own.
    // A contract maxWorkers would make Vitest run the projects one by one.
    // tests run in happy-dom unless a file starts `// @vitest-environment node`
    test: {
      exclude: TEST_EXCLUDE,
      environment: 'happy-dom',
      environmentOptions: { happyDOM: { url: 'http://phenix.test/' } },
      // A test that mounts the Inspector takes up to 5.4 s on a CI runner with
      // coverage, which is past the 5 s default.
      testTimeout: 20_000,
      // Measured only with --coverage (npm run test:coverage), as CI does.
      // Only code counts, not images, styles or data.
      coverage: {
        provider: 'v8',
        include: ['src/**/*.{js,vue,ts}'],
        reporter: ['text-summary', 'json-summary', 'html'],
      },
      projects: [
        {
          extends: true,
          test: {
            name: 'contract',
            include: CONTRACT_TESTS,
            isolate: false,
            environment: 'node',
          },
        },
        {
          extends: true,
          test: {
            name: 'unit',
            exclude: [...TEST_EXCLUDE, ...CONTRACT_TESTS],
            setupFiles: ['test/setup.js', 'test/unit-setup.js'],
          },
        },
      ],
    },
  };
});
