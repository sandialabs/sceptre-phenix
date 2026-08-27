import js from '@eslint/js';
import pluginVue from 'eslint-plugin-vue';
import skipFormatting from '@vue/eslint-config-prettier/skip-formatting';
import globals from 'globals';

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
    files: ['vite.config.js', 'vitest.config.js'],
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
  // Turns off eslint-plugin-vue's formatting rules that overlap with
  // Prettier, so the two tools don't disagree. Must stay last.
  skipFormatting,
];
