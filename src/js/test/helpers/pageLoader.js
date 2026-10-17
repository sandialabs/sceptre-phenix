// vi.mock factories standing in for src/utils/pageLoader.js; load with
// vi.hoisted. It imports only vitest, so it loads no module a test mocks.
import { vi } from 'vitest';

// a loader that loads nothing: the page shows the state it is given
const idleLoader = () => ({ start: () => {}, stop: () => {}, load: () => {} });

// the real module, with every page given an idle loader
export async function idle(original) {
  return { ...(await original()), createPageLoader: idleLoader };
}

let loading = true;

// The real module, whose loaders load as the real ones do except while
// withoutLoading runs.
export async function switchable(original) {
  const real = await original();
  return {
    ...real,
    createPageLoader: (options) =>
      loading ? real.createPageLoader(options) : idleLoader(),
  };
}

// Runs render with idle loaders: a page rendered by it shows the state it is
// given rather than loading its own.
export async function withoutLoading(render) {
  loading = false;
  try {
    return await render();
  } finally {
    loading = true;
  }
}

// The last page loader's options and load(), for the capturing and fetching
// loaders; a test may set load before the page is made, to answer as it likes.
export const loaderSpy = { options: null, load: null };

// forgets the loader the last page made
export function resetLoader() {
  loaderSpy.options = null;
  loaderSpy.load = null;
}

// the real module, whose loaders record their options and load nothing:
// their load() is loaderSpy.load, a vi.fn unless the test set one
export async function capturing(original) {
  return {
    ...(await original()),
    createPageLoader: (options) => {
      loaderSpy.options = options;
      loaderSpy.load ??= vi.fn();
      return { ...idleLoader(), load: (...args) => loaderSpy.load(...args) };
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
