// Knip finds unused files, exports and dependencies (npm run knip). Its
// plugins read the entry points from vite.config.js and its Vitest projects.
export default {
  workspaces: {
    '.': {
      // The SCSS files import Bulma by a bare path, which Knip reads as a
      // relative file.
      ignoreDependencies: ['bulma'],
    },
    // The Playwright suite has its own package.json and dependencies. The
    // entries are given here, so Knip runs without the e2e packages.
    e2e: {
      entry: ['playwright.config.js', 'tests/**/*.spec.js'],
      playwright: false,
    },
  },
};
