// vi.mock factories standing in for src/utils/pageLoader.js; load with
// vi.hoisted. It imports only vitest, so it loads no module a test mocks.
import { vi } from 'vitest';

// a loader that loads nothing: the page shows the state it is given
const idleLoader = () => ({ start: () => {}, stop: () => {}, load: () => {} });

// the real module, with every page given an idle loader
export async function idle(original) {
  return { ...(await original()), createPageLoader: idleLoader };
}

// The last capturing or fetching loader's options and load(); a test may set
// load first, to answer as it likes, and loading for a load in flight.
export const loaderSpy = { options: null, load: null, loading: false };

// forgets the loader the last page made
export function resetLoader() {
  loaderSpy.options = null;
  loaderSpy.load = null;
  loaderSpy.loading = false;
}

// the real module, whose loaders record their options and load nothing:
// their load() is loaderSpy.load, a vi.fn unless the test set one
export async function capturing(original) {
  return {
    ...(await original()),
    createPageLoader: (options) => {
      loaderSpy.options = options;
      loaderSpy.load ??= vi.fn();
      return {
        ...idleLoader(),
        load: (...args) => loaderSpy.load(...args),
        get loading() {
          return loaderSpy.loading;
        },
      };
    },
  };
}

// The real module, its loaders recording their options and load() fetching as
// the real one does, without the cache or apply(): it resolves to the data.
export async function fetching(original) {
  return {
    ...(await original()),
    createPageLoader: (options) => {
      loaderSpy.options = options;
      loaderSpy.load = vi.fn(() => options.fetch(new AbortController().signal));
      return { ...idleLoader(), load: (...args) => loaderSpy.load(...args) };
    },
  };
}
