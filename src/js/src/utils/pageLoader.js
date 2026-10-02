import { reactive } from 'vue';
import { cachedPage, fetchIntoCache } from '@/utils/pageCache.js';
import { useErrorNotification } from '@/utils/errorNotif.js';

// Load state of the page on screen, shown by RefreshStatus in the header.
// refresh is null when the page has nothing to reload.
export const pageStatus = reactive({
  refresh: null,
  loading: false,
  updatedAt: null,
  failed: false, // whether the latest load failed
  fresh: false, // whether a load has come back since the page opened
});

// Placeholder for a table whose first load has not finished: it says so
// when that load failed rather than claiming it is still loading.
export function loadingText(what) {
  return pageStatus.failed ? `Could not load ${what}` : `Loading ${what}…`;
}

// Whether a table should still say it is loading: its first load has not
// finished, or it is empty (perhaps from a stale cached copy) and no load
// has come back since the page opened. An empty table says there is nothing
// only once this visit's own load has come back empty.
export function stillLoading(loaded, rows) {
  return !loaded || (rows.length === 0 && !pageStatus.fresh);
}

// the loader that owns pageStatus; others must not touch it
let owner = null;

// Loads a page's data without hiding the page behind a spinner: cached data
// is shown at once, progress shows in the header, and the header's refresh
// button reloads it.
//
//   fetch(signal) resolves to the data; signal aborts the request.
//   apply(data, { requestedAt }) puts the data on the page; requestedAt is
//     when the data was requested, so updates the page received since then
//     can be kept (see liveRows.js).
//   key is the cache key; pass a function to decide per load, returning
//     null to skip caching (e.g. for non-default filters), or omit it for
//     live pages whose cached copy would mislead.
//   refresh, if given, replaces load() as the header button's action, for
//     pages that also reload secondary data on request.
//
// A cached page's request is shared (see fetchIntoCache): leaving the page
// lets it finish in the background and fill the cache for the next visit.
// Other requests are canceled when the page is left.
export function createPageLoader({ key = null, fetch, apply, refresh }) {
  let current = null; // the request this page is waiting on
  const cacheKey = () => (typeof key === 'function' ? key() : key);

  // stops waiting on the current request: a private one is canceled, a
  // shared one keeps going for the cache
  const detach = () => {
    const request = current;
    current = null;
    request?.release();
  };

  const loader = {
    // whether a load is in flight, so polling can skip a tick
    get loading() {
      return current !== null;
    },

    // shows cached data (if any) and loads fresh data
    start() {
      owner = loader;
      pageStatus.refresh = refresh ?? (() => loader.load());
      pageStatus.loading = false;
      pageStatus.updatedAt = null;
      pageStatus.failed = false;
      pageStatus.fresh = false;

      const k = cacheKey();
      const cached = k ? cachedPage(k) : undefined;
      if (cached) {
        apply(cached.data, { requestedAt: cached.at });
        pageStatus.updatedAt = cached.at;
      }

      return loader.load();
    },

    // resolves to true once fresh data is applied, false otherwise
    async load() {
      detach();

      const k = cacheKey();
      let request;
      if (k) {
        request = fetchIntoCache(k, fetch);
      } else {
        const controller = new AbortController();
        request = {
          promise: new Promise((resolve) => resolve(fetch(controller.signal))),
          startedAt: Date.now(),
          release: () => controller.abort(),
        };
      }

      current = request;
      if (owner === loader) pageStatus.loading = true;

      try {
        const data = await request.promise;
        if (current !== request) return false;

        apply(data, { requestedAt: request.startedAt });

        if (owner === loader) {
          pageStatus.updatedAt = Date.now();
          pageStatus.failed = false;
          pageStatus.fresh = true;
        }
        return true;
      } catch (err) {
        if (current !== request) return false;

        useErrorNotification(err);
        if (owner === loader) pageStatus.failed = true;
        return false;
      } finally {
        if (current === request) {
          detach();
          if (owner === loader) pageStatus.loading = false;
        }
      }
    },

    // stops waiting on the page's request and clears the header
    stop() {
      detach();
      if (owner !== loader) return;

      owner = null;
      pageStatus.refresh = null;
      pageStatus.loading = false;
      pageStatus.updatedAt = null;
      pageStatus.failed = false;
      pageStatus.fresh = false;
    },
  };

  return loader;
}
