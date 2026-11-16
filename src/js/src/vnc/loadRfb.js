// noVNC loads only through this module, so it is a separate lazy chunk that
// only a VNC console or a live tile downloads.

let loading = null;

/**
 * Loads noVNC once and resolves to its RFB class. The package exports only
 * `core/rfb.js`. A failed load (for example, a lost network) is tried again
 * on the next call.
 *
 * @returns {Promise<typeof import('@novnc/novnc').default>}
 */
export function loadRfb() {
  loading ??= import('@novnc/novnc').then(
    (module) => module.default,
    (err) => {
      loading = null;
      throw err;
    },
  );
  return loading;
}
