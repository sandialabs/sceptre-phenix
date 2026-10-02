// Stand-ins for src/utils/pageLoader.js, each a vi.mock factory. vi.mock runs
// before a test file's imports, so a file loads this module with vi.hoisted:
//
//   const loader = await vi.hoisted(() => import('./helpers/pageLoader.js'));
//   vi.mock('@/utils/pageLoader.js', loader.idle);
//
// It imports nothing, so loading it that early loads no module a test mocks.

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
