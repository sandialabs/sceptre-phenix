import { fileURLToPath, URL } from 'node:url';

import { defineConfig, loadEnv } from 'vite';
import vue from '@vitejs/plugin-vue';
import vueDevTools from 'vite-plugin-vue-devtools';

import builderAssets from './plugins/builder-assets.js';

// Vitest leaves out the Playwright suite in e2e/, which has its own runner.
const TEST_EXCLUDE = ['e2e/**', 'node_modules/**', 'dist/**'];

// The contract tests: suites of pure functions (the document model and its
// schema, validation, layout math, codecs, the build's compression plugin)
// that mock no module, stub no global, and leave no storage, timer or module
// state behind. They run in shared workers, without a fresh one per file,
// and in any order (`npx vitest run --project contract --sequence.shuffle`
// checks that). `npm run test:contract` runs them alone. A suite that mocks,
// stubs or keeps state belongs in the unit project.
const CONTRACT_TESTS = [
  'test/builder/announce.test.js',
  'test/builder/catalog.test.js',
  'test/builder/command-search.test.js',
  'test/builder/configs.test.js',
  'test/builder/decode.test.js',
  'test/builder/dialog-message.test.js',
  'test/builder/digest.test.js',
  'test/builder/drawings.test.js',
  'test/builder/form-validator.test.js',
  'test/builder/format.test.js',
  'test/builder/forms.test.js',
  'test/builder/fuzzy.test.js',
  'test/builder/group-notes.test.js',
  'test/builder/history.test.js',
  'test/builder/icons.test.js',
  'test/builder/inspector-notes.test.js',
  'test/builder/issues.test.js',
  'test/builder/large-diagram.test.js',
  'test/builder/layout.test.js',
  'test/builder/list-selection.test.js',
  'test/builder/model.test.js',
  'test/builder/node-info.test.js',
  'test/builder/outline.test.js',
  'test/builder/panes.test.js',
  'test/builder/rename.test.js',
  'test/builder/routes.test.js',
  'test/builder/schema-examples.test.js',
  'test/builder/schema.test.js',
  'test/builder/selection.test.js',
  'test/builder/stable.test.js',
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
    // vitest: `npm test` runs both projects. The contract tests share
    // workers; every other test file starts in a fresh worker of its own, so
    // its module mocks, global stubs and state never reach another file.
    // The contract project sets no maxWorkers of its own: Vitest requires a
    // project with its own limit to have its own sequence.groupOrder, and it
    // runs the groups one after another, so the contract tests would run
    // after the unit tests instead of beside them. A limit of 4 workers made
    // `npm test` 0.45 s slower, and a limit of 1, 1.6 s slower.
    test: {
      exclude: TEST_EXCLUDE,
      projects: [
        {
          extends: true,
          test: { name: 'contract', include: CONTRACT_TESTS, isolate: false },
        },
        {
          extends: true,
          test: { name: 'unit', exclude: [...TEST_EXCLUDE, ...CONTRACT_TESTS] },
        },
      ],
    },
  };
});
