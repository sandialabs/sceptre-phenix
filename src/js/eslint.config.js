import js from '@eslint/js';
import pluginVue from 'eslint-plugin-vue';
import skipFormatting from '@vue/eslint-config-prettier/skip-formatting';
import globals from 'globals';
import { readFileSync } from 'node:fs';

// a toast.open(...) call, as in this.$buefy.toast.open, buefy?.toast.open or toast.open
const TOAST_OPEN =
  "CallExpression[callee.property.name='open']:matches([callee.object.property.name='toast'], [callee.object.name='toast'])";

// Toast durations come from utils/toast.js, and errors are never toasts:
// they open as notifications that stay until dismissed (utils/errorNotif.js).
const TOAST_RULES = [
  {
    selector: `${TOAST_OPEN} > ObjectExpression Property:matches([key.name='duration'], [key.value='duration']) > :matches(Literal[value=type(number)], BinaryExpression, UnaryExpression)`,
    message:
      'Do not give a toast a number duration. Use the default, or LONG_TOAST_DURATION from utils/toast.js.',
  },
  {
    selector: `${TOAST_OPEN} > ObjectExpression :matches(Literal[value=/is-danger/], TemplateElement[value.raw=/is-danger/])`,
    message:
      'Do not show an error as an is-danger toast. Use showError from utils/errorNotif.js.',
  },
];

// The first word of each code in the Builder's issue registry, such as
// "device" in device.ntp-server.invalid.
const CODE_SUBJECTS = [
  ...new Set(
    Object.keys(
      JSON.parse(
        readFileSync(
          new URL('./src/builder/schema/codes.json', import.meta.url),
          'utf8',
        ),
      ),
    ).map((code) => code.split('.')[0]),
  ),
].join('|');

export default [
  {
    name: 'app/files-to-lint',
    files: ['**/*.{js,mjs,jsx,vue}'],
  },
  {
    name: 'app/files-to-ignore',
    ignores: [
      '**/dist/**',
      '**/node_modules/**',
      '**/coverage/**',
      'e2e/test-results/**',
      'e2e/playwright-report/**',
      'perf/lighthouse-results/**',
      'perf/.lighthouseci/**',
    ],
  },
  js.configs.recommended,
  // `essential` (rather than `recommended`/`strongly-recommended`) catches
  // real Vue bugs (missing v-for keys, side effects in computed properties,
  // etc.) without imposing style/ordering rules (attribute order, v-slot
  // style, etc.) across an existing codebase that predates this lint setup.
  ...pluginVue.configs['flat/essential'],
  {
    languageOptions: {
      globals: {
        ...globals.browser,
      },
    },
    rules: {
      // `_` is used throughout this codebase as the conventional name for
      // an intentionally-unused callback parameter (e.g. `.then((_) => ...)`
      // when only the side effect matters, not the resolved value).
      'no-unused-vars': [
        'error',
        {
          args: 'all',
          argsIgnorePattern: '^_$',
          varsIgnorePattern: '^_$',
          caughtErrorsIgnorePattern: '^_$',
        },
      ],
      // Empty catch blocks are used a few places to intentionally swallow
      // errors from optional/best-effort requests (e.g. probing whether an
      // endpoint/feature is available). Empty blocks elsewhere still error.
      'no-empty': ['error', { allowEmptyCatch: true }],
      // The browser console is for problems; diagnostics that only help
      // while developing go through debug() in src/utils/debug.js, which
      // production builds drop.
      'no-console': ['error', { allow: ['warn', 'error'] }],
      // Dead code: fail on unused Vue members, refs, emits and components,
      // and on useless or unreachable JavaScript. Remove the code.
      'vue/no-unused-properties': [
        'error',
        {
          groups: ['props', 'data', 'computed', 'methods', 'setup'],
          deepData: true,
        },
      ],
      'vue/no-unused-emit-declarations': 'error',
      'vue/no-unused-refs': 'error',
      'vue/no-unused-components': 'error',
      'vue/no-unused-vars': 'error',
      'no-unused-private-class-members': 'error',
      'no-useless-assignment': 'error',
      'no-unreachable-loop': 'error',
      'no-constant-binary-expression': 'error',
      'no-useless-return': 'error',
      'no-useless-constructor': 'error',
      'no-useless-rename': 'error',
      'no-unused-expressions': 'error',
      'no-unused-labels': 'error',
    },
  },
  {
    // .ts files are not checked: no TypeScript parser is installed.
    files: ['src/**/*.{js,vue}'],
    rules: {
      'no-restricted-syntax': ['error', ...TOAST_RULES],
    },
  },
  {
    // Codes are written in quotes so validate.contract.test.js can check them.
    // This block replaces the one above, so it repeats TOAST_RULES.
    files: ['src/builder/validate.js', 'src/builder/decode.js'],
    rules: {
      'no-restricted-syntax': [
        'error',
        ...TOAST_RULES,
        {
          selector: `TemplateLiteral > TemplateElement:first-child[value.raw=/^(${CODE_SUBJECTS})\\.[a-z0-9.-]*$/]`,
          message:
            'Do not build an issue code in a template string. Write the full code from schema/codes.json in quotes, because the test that checks codes against the registry reads only quoted codes.',
        },
      ],
    },
  },
  {
    // Router views are conventionally named after their route (single word,
    // e.g. Settings.vue, Console.vue), which vue/multi-word-component-names
    // otherwise flags; App.vue is exempted by the rule by default.
    files: ['src/views/**/*.vue'],
    rules: {
      'vue/multi-word-component-names': 'off',
    },
  },
  {
    files: ['vite.config.js'],
    languageOptions: {
      globals: {
        ...globals.node,
      },
    },
  },
  {
    // Playwright e2e specs/helpers run under Node with CommonJS require().
    files: ['e2e/**/*.js'],
    languageOptions: {
      sourceType: 'commonjs',
      globals: {
        ...globals.node,
      },
    },
  },
  {
    // size-limit and Lighthouse CI configs are CommonJS run under Node.
    files: ['perf/**/*.cjs'],
    languageOptions: {
      sourceType: 'commonjs',
      globals: {
        ...globals.node,
      },
    },
  },
  {
    // perf/summary.mjs is an ES module run under Node in CI.
    files: ['perf/**/*.mjs'],
    languageOptions: {
      sourceType: 'module',
      globals: {
        ...globals.node,
      },
    },
  },
  // Turns off eslint-plugin-vue's formatting rules that overlap with
  // Prettier, so the two tools don't disagree. Must stay last.
  skipFormatting,
];
