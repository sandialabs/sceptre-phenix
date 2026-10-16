// The last data each page loaded, so returning to a page shows it straight
// away while a fresh copy loads, rather than a blank table under a spinner.
// Cleared on logout so one user never sees another's data.
import { FreshnessMeta, resourceFor } from '@/utils/freshness.js';

const cache = new Map();

// Requests whose results go into the cache, by cache key. They are shared, so
// a page opened while its data is still loading (from a preload, or from an
// earlier visit) waits on that request instead of starting another, and they
// outlive the page that started them.
const inflight = new Map();

// A request that has run this long is given up once no page is waiting on it,
// so a stuck server call does not linger in the background.
export const BACKGROUND_TIMEOUT_MS = 2 * 60 * 1000;

// Returns { data, at, meta, invalidatedSeq } or undefined: at is when it was
// loaded (ms), meta how fresh it is (a FreshnessMeta), and invalidatedSeq,
// when set, the seq of a websocket message that changed it since.
export function cachedPage(key) {
  return cache.get(key);
}

// Caches data for key, with the freshness meta of the load that got it; data
// without one is taken as read now. A change announced after the load's seq
// keeps the entry invalidated.
export function cachePage(key, data, meta) {
  const fresh =
    meta ?? Object.assign(new FreshnessMeta(), { observedAt: Date.now() });
  const previous = cache.get(key);
  const invalidatedSeq =
    previous?.invalidatedSeq !== undefined &&
    fresh.seq !== undefined &&
    previous.invalidatedSeq > fresh.seq
      ? previous.invalidatedSeq
      : undefined;

  cache.set(key, { data, at: Date.now(), meta: fresh, invalidatedSeq });
}

// Whether a websocket message has changed the cached data since it loaded.
export function isInvalidated(entry) {
  return entry?.invalidatedSeq !== undefined;
}

export function isLoadingPage(key) {
  return inflight.has(key);
}

// Starts fetch(signal, meta) for key, or joins the request already running
// for it, and caches the result. fetch passes meta (a FreshnessMeta) with its
// requests, which fill it in. Returns a handle with the result promise, when
// the request was sent, its meta, and release(), which a page calls when it
// stops waiting on the result. force starts a new request even when one is
// running, for data that changed since it was sent: the older one's result is
// then not cached.
export function fetchIntoCache(key, fetch, { force = false } = {}) {
  let entry = inflight.get(key);

  if (entry && force) {
    entry.superseded = true;
    inflight.delete(key);
    if (entry.waiting === 0) entry.controller.abort();
    entry = undefined;
  }

  // an abandoned request that timed out is not worth joining
  if (!entry || entry.controller.signal.aborted) {
    const controller = new AbortController();
    const meta = new FreshnessMeta();
    entry = {
      controller,
      meta,
      waiting: 0,
      expired: false,
      superseded: false,
      startedAt: Date.now(),
    };

    // the executor runs fetch now, turning a synchronous throw into a rejection
    entry.promise = new Promise((resolve) =>
      resolve(fetch(controller.signal, meta)),
    )
      .then((data) => {
        if (!controller.signal.aborted && !entry.superseded)
          cachePage(key, data, meta);
        return data;
      })
      .finally(() => {
        clearTimeout(entry.timer);
        if (inflight.get(key) === entry) inflight.delete(key);
      });

    entry.timer = setTimeout(() => {
      entry.expired = true;
      if (entry.waiting === 0) controller.abort();
    }, BACKGROUND_TIMEOUT_MS);

    inflight.set(key, entry);
  }

  entry.waiting++;

  let released = false;
  return {
    promise: entry.promise,
    // when the shared request was sent, which may be before this call
    startedAt: entry.startedAt,
    meta: entry.meta,
    release() {
      if (released) return;
      released = true;
      entry.waiting--;
      // nobody is left waiting on a request that has run too long or been
      // replaced
      if (entry.waiting === 0 && (entry.expired || entry.superseded))
        entry.controller.abort();
    },
  };
}

// The data cached for key when it loaded less than maxAge ms ago (by default
// the key's reuse window in freshness.js), is not loading again, and has not
// changed since; otherwise the result of fetch into the cache, joining the
// request already running for key.
export async function recentPage(key, fetch, maxAge) {
  const reuse = maxAge ?? resourceFor(key)?.reuseMs ?? 0;
  const cached = cachedPage(key);
  if (
    cached &&
    !isLoadingPage(key) &&
    !isInvalidated(cached) &&
    Date.now() - cached.at < reuse
  ) {
    return cached.data;
  }

  const request = fetchIntoCache(key, fetch);
  try {
    return await request.promise;
  } finally {
    request.release();
  }
}

// Marks the cached pages a websocket message changed (see invalidatedBy in
// freshness.js) as out of date, so the next visit or reuse loads them again
// rather than trusting them. A message no newer than an entry's load changes
// nothing; one without a seq (an older server) always counts.
export function invalidatePages(msg) {
  for (const [key, entry] of cache) {
    const rules = resourceFor(key)?.invalidatedBy ?? [];
    if (!rules.some((matches) => matches(msg, key))) continue;

    const seq = msg.seq ?? Infinity;
    if (entry.meta?.seq !== undefined && seq <= entry.meta.seq) continue;

    entry.invalidatedSeq = Math.max(entry.invalidatedSeq ?? -Infinity, seq);
  }
}

export function clearPageCache() {
  for (const entry of inflight.values()) entry.controller.abort();
  inflight.clear();
  cache.clear();
}
