import { fileURLToPath, URL } from 'node:url';

import { defineConfig, loadEnv } from 'vite';
import vue from '@vitejs/plugin-vue';
import vueDevTools from 'vite-plugin-vue-devtools';

import builderAssets from './plugins/builder-assets.js';

// Vitest leaves out the Playwright suite in e2e/, which has its own runner.
const TEST_EXCLUDE = ['e2e/**', 'node_modules/**', 'dist/**'];

// Suites of pure functions that mock nothing and leave no state behind. They
// share workers and run in any order (`--project contract --sequence.shuffle`).
const CONTRACT_TESTS = [
  'test/builder/addressing.test.js',
  'test/builder/announce.test.js',
  'test/builder/catalog.test.js',
  'test/builder/click-slop.test.js',
  'test/builder/combobox.test.js',
  'test/builder/command-search.test.js',
  'test/builder/configs.test.js',
  'test/builder/decode.test.js',
  'test/builder/diagram-text.test.js',
  'test/builder/dialog-message.test.js',
  'test/builder/digest.test.js',
  'test/builder/drawings.test.js',
  'test/builder/field-search.test.js',
  'test/builder/form-outline.test.js',
  'test/builder/form-validator.test.js',
  'test/builder/format.test.js',
  'test/builder/forms.test.js',
  'test/builder/fuzzy.test.js',
  'test/builder/group-notes.test.js',
  'test/builder/history.test.js',
  'test/builder/icons.test.js',
  'test/builder/issues.test.js',
  'test/builder/keymap-properties.test.js',
  'test/builder/large-diagram.test.js',
  'test/builder/layer-look.test.js',
  'test/builder/layer-matchers.test.js',
  'test/builder/layer-sets.test.js',
  'test/builder/layout-tiers.test.js',
  'test/builder/layout.test.js',
  'test/builder/list-selection.test.js',
  'test/builder/merge.test.js',
  'test/builder/model.test.js',
  'test/builder/node-info.test.js',
  'test/builder/palette-sections.test.js',
  'test/builder/panes.test.js',
  'test/builder/routes.test.js',
  'test/builder/rulers.test.js',
  'test/builder/scenario-summary.test.js',
  'test/builder/schema-examples.test.js',
  'test/builder/schema.test.js',
  'test/builder/selection.test.js',
  'test/builder/separate.test.js',
  'test/builder/stable.test.js',
  'test/builder/text.test.js',
  'test/builder/theme.test.js',
  'test/builder/validate.test.js',
  'test/builder/vueflow.test.js',
  'test/plugins/builder-assets.test.js',
];

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
    // The contract tests share workers, and every other file gets its own.
    // A contract maxWorkers would make Vitest run the projects one by one.
    test: {
      exclude: TEST_EXCLUDE,
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
