import { holdOffMs, whenServing } from '@/utils/freshness.js';

// Every page is a lazy-loaded chunk, so without this the first visit to a page
// blocks navigation on fetching its chunk(s) from the server; on a slow or
// high-latency link that reads as the UI hanging for seconds after a click.
// Warming the chunks once the app is idle makes the first click instant.

// Lazy route components are the functions (not objects) in each record's
// `components`; calling one starts the dynamic import. A route with
// `meta.prefetch: false` loads only when a link to it shows that the user
// will open it (see prefetchRoute).
export function lazyRouteLoaders(routes) {
  const loaders = [];
  for (const route of routes) {
    if (route.meta?.prefetch === false) continue;
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
    // paused while the server is shutting down
    if (holdOffMs() > 0) await whenServing();
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

// The loaders of routes with `meta.prefetch: false` that an intent started.
const started = new WeakSet();

// Starts loading the chunks of the route at `to` one time when the route
// skips the idle prefetch (`meta.prefetch: false`). It calls the route's own
// loader, so the navigation then uses the same chunk. It does nothing when the
// user asked to save data or the role may not open the route (`meta.allowed`).
export function prefetchRoute(router, to) {
  if (navigator.connection?.saveData) return;
  let route;
  try {
    route = router.resolve(to);
  } catch {
    return;
  }
  if (route.meta.allowed && !route.meta.allowed(route)) return;
  for (const record of route.matched) {
    if (record.meta?.prefetch !== false) continue;
    for (const load of Object.values(record.components ?? {})) {
      if (typeof load !== 'function' || started.has(load)) continue;
      started.add(load);
      // ignored: the navigation loads the chunk again
      load().catch(() => {});
    }
  }
}

// the signs that the user is about to follow a link
const INTENTS = ['pointerenter', 'focus', 'pointerdown', 'touchstart'];
const intentTargets = new WeakMap();

// `v-prefetch-route="{ name: 'builder' }"` on a link: calls prefetchRoute
// with the link's route when a pointer enters the link, the link gets focus,
// or the user presses it. Put it on a link that shows only for a role that
// may open the route.
export const vPrefetchRoute = {
  mounted(el, binding) {
    const target = { to: binding.value };
    target.listener = () => prefetchRoute(binding.instance.$router, target.to);
    intentTargets.set(el, target);
    for (const type of INTENTS) {
      el.addEventListener(type, target.listener, { passive: true });
    }
  },
  updated(el, binding) {
    const target = intentTargets.get(el);
    if (target) target.to = binding.value;
  },
  beforeUnmount(el) {
    const target = intentTargets.get(el);
    if (!target) return;
    for (const type of INTENTS) {
      el.removeEventListener(type, target.listener);
    }
    intentTargets.delete(el);
  },
};
