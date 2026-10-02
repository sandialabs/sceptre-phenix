// Every page is a lazy-loaded chunk, so without this the first visit to a page
// blocks navigation on fetching its chunk(s) from the server; on a slow or
// high-latency link that reads as the UI hanging for seconds after a click.
// Warming the chunks once the app is idle makes the first click instant.

// Lazy route components are the functions (not objects) in each record's
// `components`; calling one starts the dynamic import.
export function lazyRouteLoaders(routes) {
  const loaders = [];
  for (const route of routes) {
    for (const component of Object.values(route.components ?? {})) {
      if (typeof component === 'function') {
        loaders.push(component);
      }
    }
  }
  return loaders;
}

// One chunk at a time so prefetching never competes with requests the user
// triggers; a failed prefetch is harmless, navigation retries the import.
export async function prefetchSequentially(loaders) {
  for (const load of loaders) {
    try {
      await load();
    } catch {
      // ignored, see above
    }
  }
}

// Runs start in the background once the browser is idle, or after timeout
// ms at the latest, unless the user asked to save data.
export function whenIdle(start, timeout) {
  if (navigator.connection?.saveData) {
    return;
  }

  if (typeof requestIdleCallback === 'function') {
    requestIdleCallback(start, { timeout });
    return;
  }

  // no idle callback (Safari): wait for the page itself to finish loading so
  // the background work never delays it
  const afterLoad = () => setTimeout(start, 1000);
  if (document.readyState === 'complete') {
    afterLoad();
  } else {
    window.addEventListener('load', afterLoad, { once: true });
  }
}

export function schedulePrefetch(loaders) {
  whenIdle(() => prefetchSequentially(loaders), 2000);
}
