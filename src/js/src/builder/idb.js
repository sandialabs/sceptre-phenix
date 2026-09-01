// Minimal IndexedDB helper for the builder's offline autosave queue.
//
// Deliberately tiny (no external dependency) and fully injectable so unit
// tests can pass a fake factory. Every method degrades to a no-op result when
// IndexedDB is unavailable (private windows, SSR, older embedded browsers).

export const DB_NAME = 'phenix-builder';
export const DB_VERSION = 1;
export const STORE_NAME = 'drafts';

/**
 * Key that scopes an entry to both the acting user and the draft owner, so a
 * shared draft opened by two different users never shares queue state.
 *
 * @param {string} actor signed-in username
 * @param {string} owner draft owner
 * @param {string} id draft id
 * @returns {string}
 */
export function draftKey(actor, owner, id) {
  return `${actor || 'anonymous'}::${owner || 'unknown'}::${id || 'new'}`;
}

function promisify(request) {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

/**
 * Opens (and upgrades) the builder database.
 *
 * @param {IDBFactory} [factory]
 * @returns {Promise<IDBDatabase|null>} null when unavailable
 */
export function openBuilderDb(factory) {
  const idb =
    factory ||
    (typeof indexedDB !== 'undefined' ? indexedDB : undefined) ||
    (typeof globalThis !== 'undefined' ? globalThis.indexedDB : undefined);

  if (!idb) {
    return Promise.resolve(null);
  }

  return new Promise((resolve) => {
    let request;

    try {
      request = idb.open(DB_NAME, DB_VERSION);
    } catch {
      resolve(null);
      return;
    }

    request.onupgradeneeded = () => {
      const db = request.result;

      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: 'key' });
      }
    };

    request.onsuccess = () => resolve(request.result);
    request.onerror = () => resolve(null);
    request.onblocked = () => resolve(null);
  });
}

/**
 * Creates the store facade used by the autosave queue.
 *
 * @param {object} [options] factory
 * @returns {{put: Function, get: Function, remove: Function, all: Function}}
 */
export function createDraftStore(options = {}) {
  let dbPromise = null;

  const db = () => {
    if (!dbPromise) {
      dbPromise = openBuilderDb(options.factory);
    }
    return dbPromise;
  };

  const withStore = async (mode, action) => {
    const database = await db();

    if (!database) {
      return null;
    }

    try {
      const tx = database.transaction(STORE_NAME, mode);
      return await action(tx.objectStore(STORE_NAME));
    } catch {
      return null;
    }
  };

  return {
    async put(entry) {
      return withStore('readwrite', (store) =>
        promisify(store.put({ ...entry })),
      );
    },

    async get(key) {
      return withStore('readonly', (store) => promisify(store.get(key)));
    },

    async remove(key) {
      return withStore('readwrite', (store) => promisify(store.delete(key)));
    },

    async all() {
      const result = await withStore('readonly', (store) =>
        promisify(store.getAll()),
      );

      return result || [];
    },
  };
}

/**
 * In-memory fallback with the same surface, used when IndexedDB is missing and
 * in unit tests.
 *
 * @returns {{put: Function, get: Function, remove: Function, all: Function}}
 */
export function createMemoryStore() {
  const map = new Map();

  return {
    async put(entry) {
      map.set(entry.key, { ...entry });
      return entry.key;
    },
    async get(key) {
      return map.has(key) ? { ...map.get(key) } : undefined;
    },
    async remove(key) {
      map.delete(key);
    },
    async all() {
      return [...map.values()];
    },
  };
}
