// A stale chunk: a tab opened before a server update asks for a file of the
// old build, which the server no longer has. The page then reloads once to
// get the new build. A second reload within RELOAD_GUARD_MS is not done, so
// the page cannot reload in a loop: the user is told to reload instead.
import { openNotification } from '@/utils/notify.js';

export const RELOAD_GUARD_KEY = 'phenix.staleChunkReloadAt';
export const RELOAD_GUARD_MS = 60_000;

export const UPDATED_MESSAGE =
  'phēnix was updated on the server. Reload the page to get the new version.';

// the error texts of a failed import() or preload in Chromium, Firefox,
// Safari and Vite
const CHUNK_ERRORS = [
  /failed to fetch dynamically imported module/i,
  /error loading dynamically imported module/i,
  /importing a module script failed/i,
  /unable to preload css/i,
  /loading (css )?chunk \S+ failed/i,
];

// Whether err is a failed load of a build file (a chunk), not an error in
// the code that the file holds.
export function isChunkLoadError(err) {
  if (err == null) return false;
  if (err.name === 'ChunkLoadError') return true;
  const text = String(err.message ?? err);
  return CHUNK_ERRORS.some((re) => re.test(text));
}

// what this page did about a stale chunk: null, 'reloading' or 'blocked'
let handled = null;

function readGuard(storage) {
  try {
    return Number(storage.getItem(RELOAD_GUARD_KEY)) || 0;
  } catch {
    return 0;
  }
}

function writeGuard(storage, now) {
  try {
    storage.setItem(RELOAD_GUARD_KEY, String(now));
    return true;
  } catch {
    return false;
  }
}

function sessionStore() {
  try {
    return globalThis.sessionStorage;
  } catch {
    return undefined;
  }
}

// Tells the user to reload the page, once for each page load.
export function showUpdatedNotice(notify = openNotification) {
  if (handled) return;
  handled = 'blocked';
  notify({
    type: 'is-warning',
    hasIcon: true,
    position: 'is-top',
    indefinite: true,
    ariaCloseLabel: 'Dismiss notice',
    message: `<span role="alert">${UPDATED_MESSAGE}</span>`,
  });
}

// Reloads the page at href (the current page when href is empty), unless the
// guard blocks it; then it tells the user to reload. Returns 'reloading' or
// 'blocked'. A page that already reloaded or told the user does nothing more.
export function reloadForStaleChunk(
  href,
  {
    storage = sessionStore(),
    location = globalThis.location,
    now = Date.now(),
    notify = openNotification,
  } = {},
) {
  if (handled) return handled;
  // without storage, the guard cannot stop a loop
  if (
    !storage ||
    Math.abs(now - readGuard(storage)) < RELOAD_GUARD_MS ||
    !writeGuard(storage, now)
  ) {
    showUpdatedNotice(notify);
    return handled;
  }
  handled = 'reloading';
  if (href) location.assign(href);
  else location.reload();
  return handled;
}

// the loads under way of pages that tell the user about a stale chunk
// themselves
let pageLoads = 0;

// Runs load(), an import() whose page shows its own notice when the file is
// gone, so that handlePreloadError does not show a second one meanwhile.
export function loadInPage(load) {
  pageLoads += 1;
  let loading;
  try {
    loading = Promise.resolve(load());
  } catch (err) {
    loading = Promise.reject(err);
  }
  return loading.finally(() => {
    pageLoads -= 1;
  });
}

// Handles a build file that Vite could not load. A navigation (href) reloads
// to its page. Elsewhere a reload could lose the user's work, so the user is
// told to reload instead, unless a page that loads its own file tells them.
export function handlePreloadError(err, href, options = {}) {
  if (!isChunkLoadError(err)) return null;
  if (href) return reloadForStaleChunk(href, options);
  if (pageLoads > 0) return null;
  showUpdatedNotice(options.notify);
  return handled;
}
