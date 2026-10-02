// The last data each page loaded, so returning to a page shows it straight
// away while a fresh copy loads, rather than a blank table under a spinner.
// Cleared on logout so one user never sees another's data.
const cache = new Map();

// Requests whose results go into the cache, by cache key. They are shared, so
// a page opened while its data is still loading (from a preload, or from an
// earlier visit) waits on that request instead of starting another, and they
// outlive the page that started them.
const inflight = new Map();

// A request that has run this long is given up once no page is waiting on it,
// so a stuck server call does not linger in the background.
export const BACKGROUND_TIMEOUT_MS = 2 * 60 * 1000;

// Returns { data, at } (at: when it was loaded, in ms) or undefined.
export function cachedPage(key) {
  return cache.get(key);
}

export function cachePage(key, data) {
  cache.set(key, { data, at: Date.now() });
}

export function isLoadingPage(key) {
  return inflight.has(key);
}

// Starts fetch(signal) for key, or joins the request already running for it,
// and caches the result. Returns a handle with the result promise, when the
// request was sent, and release(), which a page calls when it stops waiting
// on the result.
export function fetchIntoCache(key, fetch) {
  let entry = inflight.get(key);

  // an abandoned request that timed out is not worth joining
  if (!entry || entry.controller.signal.aborted) {
    const controller = new AbortController();
    entry = { controller, waiting: 0, expired: false, startedAt: Date.now() };

    // the executor runs fetch now, turning a synchronous throw into a rejection
    entry.promise = new Promise((resolve) => resolve(fetch(controller.signal)))
      .then((data) => {
        if (!controller.signal.aborted) cachePage(key, data);
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
    release() {
      if (released) return;
      released = true;
      entry.waiting--;
      // nobody is left waiting on a request that has already run too long
      if (entry.waiting === 0 && entry.expired) entry.controller.abort();
    },
  };
}

// The data cached for key when it loaded less than maxAge ms ago and is not
// loading again; otherwise the result of fetch into the cache, joining the
// request already running for key.
export async function recentPage(key, fetch, maxAge) {
  const cached = cachedPage(key);
  if (cached && !isLoadingPage(key) && Date.now() - cached.at < maxAge) {
    return cached.data;
  }

  const request = fetchIntoCache(key, fetch);
  try {
    return await request.promise;
  } finally {
    request.release();
  }
}

export function clearPageCache() {
  for (const entry of inflight.values()) entry.controller.abort();
  inflight.clear();
  cache.clear();
}
