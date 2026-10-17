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
// every request not yet settled, including ones a forced load replaced
const running = new Set();

// A request that has run this long is given up once no page is waiting on it,
// so a stuck server call does not linger in the background.
export const BACKGROUND_TIMEOUT_MS = 2 * 60 * 1000;

// Returns { data, at, meta, invalidatedSeq, superseded } or undefined: when it
// loaded (ms), how fresh (a FreshnessMeta), the seq of a websocket message that
// changed it since, and whether a reload was forced while it loaded.
export function cachedPage(key) {
  return cache.get(key);
}

// Caches data for key, with the freshness meta of the load that got it; data
// without one is taken as read now. A change announced after the load's seq
// keeps the entry invalidated, as does superseded.
export function cachePage(key, data, meta, { superseded = false } = {}) {
  const fresh =
    meta ?? Object.assign(new FreshnessMeta(), { observedAt: Date.now() });
  const previous = cache.get(key);
  const invalidatedSeq =
    previous?.invalidatedSeq !== undefined &&
    fresh.seq !== undefined &&
    previous.invalidatedSeq > fresh.seq
      ? previous.invalidatedSeq
      : undefined;

  cache.set(key, {
    data,
    at: Date.now(),
    meta: fresh,
    invalidatedSeq,
    superseded,
  });
}

// Whether a websocket message has changed the cached data since it loaded.
export function isInvalidated(entry) {
  return entry?.invalidatedSeq !== undefined || !!entry?.superseded;
}

export function isLoadingPage(key) {
  return inflight.has(key);
}

// Starts fetch(signal, meta) for key, or joins the request already loading it,
// and caches the result; meta is a FreshnessMeta its requests fill in. Returns
// a handle with the result promise, start time, meta and release(), called when
// a page stops waiting. force never joins a running request: it queues one that
// starts once that settles, and the older result is cached as out of date.
export function fetchIntoCache(key, fetch, { force = false } = {}) {
  let entry = inflight.get(key);

  // an abandoned request that timed out is not worth joining
  if (!entry || entry.controller.signal.aborted) {
    entry = startRequest(key, fetch);
  } else if (force && entry.started) {
    entry.superseded = true;
    entry = startRequest(key, fetch, entry.promise);
  }

  entry.waiting++;

  let released = false;
  return {
    promise: entry.promise,
    key,
    seq: entry.seq,
    // when the request was sent, which may be before this call or, for one
    // still queued, later
    get startedAt() {
      return entry.startedAt;
    },
    meta: entry.meta,
    release() {
      if (released) return;
      released = true;
      entry.waiting--;
      // nobody is left waiting on a request that has already run too long
      if (entry.waiting === 0 && entry.expired) entry.controller.abort();
    },
  };
}

let requestSeq = 0;

// Runs fetch for key now, or once the request after settles, as the request
// the inflight map shares.
function startRequest(key, fetch, after) {
  const controller = new AbortController();
  const meta = new FreshnessMeta();
  const entry = {
    controller,
    meta,
    seq: ++requestSeq,
    waiting: 0,
    expired: false,
    superseded: false,
    started: false,
    startedAt: undefined,
  };

  const run = () => {
    if (controller.signal.aborted)
      throw controller.signal.reason ?? new DOMException('', 'AbortError');
    entry.started = true;
    entry.startedAt = Date.now();
    entry.timer = setTimeout(() => {
      entry.expired = true;
      if (entry.waiting === 0) controller.abort();
    }, BACKGROUND_TIMEOUT_MS);
    return fetch(controller.signal, meta);
  };

  // the executor runs fetch now, turning a synchronous throw into a rejection
  entry.promise = (
    after ? after.then(run, run) : new Promise((resolve) => resolve(run()))
  )
    .then((data) => {
      if (!controller.signal.aborted)
        cachePage(key, data, meta, { superseded: entry.superseded });
      return data;
    })
    .finally(() => {
      clearTimeout(entry.timer);
      running.delete(entry);
      if (inflight.get(key) === entry) inflight.delete(key);
    });

  running.add(entry);
  inflight.set(key, entry);
  return entry;
}

// The data cached for key when it loaded within the key's reuse window in
// freshness.js, is not loading again, and has not changed since; otherwise
// the result of fetch into the cache, joining the request already running
// for key.
export async function recentPage(key, fetch) {
  const reuse = resourceFor(key)?.reuseMs ?? 0;
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
  for (const entry of running) entry.controller.abort();
  running.clear();
  inflight.clear();
  cache.clear();
}
