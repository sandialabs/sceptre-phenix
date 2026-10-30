import { reactive } from 'vue';
import {
  cachedPage,
  fetchIntoCache,
  isInvalidated,
} from '@/utils/pageCache.js';
import { useErrorNotification } from '@/utils/errorNotif.js';
import { FreshnessMeta, resourceFor, staleReason } from '@/utils/freshness.js';

// Load state of the page on screen, shown by RefreshStatus in the header.
// refresh is null when the page has nothing to reload.
export const pageStatus = reactive({
  refresh: null,
  loading: false,
  updatedAt: null, // when the data on screen arrived (ms)
  // when the server read the data on screen (ms, on the browser's clock)
  observedAt: null,
  failed: false, // whether the latest load failed
  fresh: false, // whether a load has come back since the page opened
  // why the data on screen may be out of date (see freshness.js
  // staleReason); age and disconnection are judged as the header redraws,
  // by currentStaleReason
  staleReason: null,
  resource: null, // the page's cache key, for its freshness rules
});

// Why the data on screen may be out of date now, or null: the reason set by
// its load, or else one judged from its age and the live connection.
export function currentStaleReason(now = Date.now()) {
  if (pageStatus.staleReason) return pageStatus.staleReason;
  const observedAt = pageStatus.observedAt ?? pageStatus.updatedAt;
  if (observedAt === null) return null;
  return staleReason({ observedAt }, resourceFor(pageStatus.resource), now);
}

// records in pageStatus how fresh the data the page just applied is
const showFreshness = (meta, fallbackAt, invalidated = false) => {
  pageStatus.observedAt = meta?.observedAt ?? fallbackAt;
  pageStatus.staleReason = staleReason(
    { stale: !!meta?.stale, invalidated },
    undefined,
  );
};

const resetStatus = () => {
  pageStatus.loading = false;
  pageStatus.updatedAt = null;
  pageStatus.observedAt = null;
  pageStatus.failed = false;
  pageStatus.fresh = false;
  pageStatus.staleReason = null;
};

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

// Loads a page's data without a spinner: cached data shows at once, and the
// header shows progress and reloads it. fetch(signal, meta) resolves to the
// data; apply(data, { seq }) shows it (see liveRows.js); key is the cache key
// or a function giving it (null: no caching); refresh replaces header reload.
// On leaving, a cached page's request finishes into the cache; others cancel.
export function createPageLoader({ key = null, fetch, apply, refresh }) {
  let current = null; // the request this page is waiting on
  // a shared request still running when a forced load queued current behind it
  let earlier = null;
  let loads = 0; // numbers each load() call, so older data never replaces newer
  let applied = 0; // the number of the load whose data is on the page
  let skipped = false; // whether an earlier result was skipped since then
  let failing = false; // whether the latest load failed
  const cacheKey = () => (typeof key === 'function' ? key() : key);

  // stops waiting on the page's requests: a private one is canceled, a
  // shared one keeps going for the cache
  const detach = () => {
    const requests = [current, earlier];
    current = null;
    earlier = null;
    for (const request of requests) request?.release();
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
      resetStatus();

      const k = cacheKey();
      pageStatus.resource = k;
      const cached = k ? cachedPage(k) : undefined;
      if (cached) {
        const meta = cached.meta;
        apply(cached.data, { seq: meta?.seq });
        pageStatus.updatedAt = cached.at;
        showFreshness(meta, cached.at, isInvalidated(cached));
      }

      return loader.load();
    },

    // Resolves to true once fresh data is applied, false otherwise. force
    // waits for a request sent after a change the page was told of.
    async load({ force = false, poll = false } = {}) {
      const n = ++loads;
      const k = cacheKey();
      let request;
      if (k) {
        request = fetchIntoCache(k, fetch, { force });
      } else {
        const controller = new AbortController();
        const meta = new FreshnessMeta();
        request = {
          promise: new Promise((resolve) =>
            resolve(fetch(controller.signal, meta)),
          ),
          startedAt: Date.now(),
          meta,
          release: () => controller.abort(),
        };
      }

      // A request a forced load queued behind is still awaited: under a
      // stream of forced loads, every other one lands, so the page keeps up.
      const previous = current;
      if (k && previous?.key === k && previous.seq !== request.seq && force) {
        earlier?.release();
        earlier = previous;
      } else {
        previous?.release();
        if (earlier && earlier.key !== k) {
          earlier.release();
          earlier = null;
        }
      }
      current = request;
      if (owner === loader) pageStatus.loading = true;

      try {
        const data = await request.promise;
        if (current !== request && earlier !== request) return false;
        if (n < applied) return false;
        // data from before the change is skipped, but never twice in a row
        if (earlier === request && !skipped) {
          skipped = true;
          return false;
        }
        applied = n;
        skipped = false;
        failing = false;

        const meta = request.meta;
        apply(data, { seq: meta?.seq });

        if (owner === loader) {
          pageStatus.resource = k;
          pageStatus.updatedAt = Date.now();
          pageStatus.failed = false;
          pageStatus.fresh = true;
          showFreshness(meta, request.startedAt);
        }
        return true;
      } catch (err) {
        if (current !== request) return false;

        // a poll failing again after a failed load has nothing new to say
        if (!(poll && failing)) useErrorNotification(err);
        failing = true;
        if (owner === loader) pageStatus.failed = true;
        return false;
      } finally {
        if (earlier === request) {
          earlier = null;
          request.release();
        } else if (current === request) {
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
      pageStatus.resource = null;
      resetStatus();
    },
  };

  return loader;
}
