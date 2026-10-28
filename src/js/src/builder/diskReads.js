// When a view reads the server's disk images (see fetchDisks in store.js).

import { onBeforeUnmount, onMounted, watch } from 'vue';

// Each read asks minimega about every image file. So a view reads them
// again only when they are this old.
const DISKS_STALE_MS = 30000;

/**
 * Reads the server's disk images when the calling component mounts, and
 * again when the page shows after they are a while old. A hidden component
 * (`shown`) reads nothing.
 */
export function useDiskReads(store, shown = () => true) {
  let readAt = 0;

  function readIfOld() {
    if (shown() && Date.now() - readAt >= DISKS_STALE_MS) {
      readAt = Date.now();
      store.fetchDisks();
    }
  }

  function onVisibility() {
    if (document.visibilityState === 'visible') {
      readIfOld();
    }
  }

  watch(shown, readIfOld);

  onMounted(() => {
    readIfOld();
    document.addEventListener('visibilitychange', onVisibility);
  });

  onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', onVisibility);
  });
}
