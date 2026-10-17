// The page's Web Storage. Reading window.localStorage or sessionStorage
// throws where the browser blocks site data, which reads here as no storage.
// A preference kept in localStorage follows the changes other tabs make to
// it (followStorageKey).

/**
 * @param {'localStorage'|'sessionStorage'} [name]
 * @returns {Storage|null} the storage, or null where there is none or it is
 *   out of reach
 */
export function pageStorage(name = 'localStorage') {
  try {
    return globalThis[name] || null;
  } catch {
    return null;
  }
}

/**
 * Calls `reload` when another tab changes `key` in localStorage, or clears
 * it.
 *
 * @param {string} key
 * @param {() => void} reload
 * @param {Window} [target]
 * @returns {() => void} stops following
 */
export function followStorageKey(key, reload, target = globalThis.window) {
  const onStorage = (event) => {
    if (event.key === key || event.key === null) {
      reload();
    }
  };

  target?.addEventListener?.('storage', onStorage);

  return () => target?.removeEventListener?.('storage', onStorage);
}
