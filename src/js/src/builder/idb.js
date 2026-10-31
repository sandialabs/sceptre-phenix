// IndexedDB store for the Builder's offline autosave queue. Without IndexedDB,
// reads are empty and writes throw, so the queue never claims to keep work that
// it did not keep. Leaving the page can stop a database write, so the page also
// keeps an unload copy in localStorage (see mergeUnloadCopy).

import { pageStorage } from './storage.js';
import { deepCopy } from './values.js';

const DB_NAME = 'phenix-builder';
const DB_VERSION = 1;
const STORE_NAME = 'drafts';
const ENTRY_STORE = 'entries';

const STORES = [STORE_NAME, ENTRY_STORE];

// Bumped by clearBuilderDatabase(). A store created before then refuses to
// write, so a queue still settling a save when the user signs out cannot
// store the draft again.
let generation = 0;

// The last clearBuilderDatabase(). A store opens the database after it
// settles. Thus a Builder opened just after a sign-in does not read the
// records that it clears, and does not write records that it would clear.
let clearing = Promise.resolve(true);

/**
 * A queue key per user and draft, so two users of one browser never share a
 * queue.
 */
export function draftKey(actor, id) {
  return `${actor || 'anonymous'}::${id || 'new'}`;
}

/**
 * The key of one tab's record of a draft (see tabs.js). Without a tab, the
 * draft key.
 */
export function tabRecordKey(key, tab) {
  return tab ? `${key}#${tab}` : key;
}

function promisify(request) {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

// Settles when a write transaction has committed, or rejects when it failed.
function completion(tx) {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
    tx.onabort = () =>
      reject(
        tx.error || new Error('The browser did not store the local draft.'),
      );
  });
}

function factoryOf(factory) {
  return (
    factory ||
    (typeof indexedDB !== 'undefined' ? indexedDB : undefined) ||
    (typeof globalThis !== 'undefined' ? globalThis.indexedDB : undefined)
  );
}

// A history entry as the draft record keeps it: everything but its snapshot.
function entryMeta(entry) {
  const { snapshot: _, ...meta } = entry;

  return meta;
}

// The draft record a put stores: its entries' metadata, without snapshots.
// It is a JSON copy, because a Vue proxy cannot be structured-cloned.
export function storedRecord(record) {
  return deepCopy({
    ...record,
    entries: (record.entries || []).map(entryMeta),
  });
}

// The entry record a put stores for one entry's snapshot, as a JSON copy.
export function storedEntry(key, entry) {
  return deepCopy({ draft: key, id: entry.id, snapshot: entry.snapshot });
}

// What the localStorage key of an unload copy starts with.
const UNLOAD_COPY_PREFIX = 'phenix.builder.unload.';

/**
 * @param {string} key draft record key (see draftKey)
 * @returns {string} the localStorage key of its unload copy
 */
export function unloadCopyKey(key) {
  return `${UNLOAD_COPY_PREFIX}${key}`;
}

/**
 * Writes the unload copy of a draft record with the snapshots of `write`. It
 * never throws. A copy too large for localStorage is not written, and the older
 * copy is removed, so it cannot replace newer work.
 */
export function writeUnloadCopy(storage, record, write = []) {
  const copied = new Set(write.map((entry) => entry.id));
  const key = unloadCopyKey(record.key);

  try {
    storage.setItem(
      key,
      JSON.stringify({
        ...record,
        entries: (record.entries || []).map((entry) =>
          copied.has(entry.id) ? entry : entryMeta(entry),
        ),
      }),
    );

    return true;
  } catch {
    removeUnloadCopy(storage, record.key);

    return false;
  }
}

/**
 * @param {Storage|null} storage
 * @param {string} key draft record key
 * @returns {object|null} its unload copy, when there is a readable one
 */
export function readUnloadCopy(storage, key) {
  try {
    const copy = JSON.parse(storage?.getItem(unloadCopyKey(key)) ?? 'null');

    return copy?.key === key &&
      Array.isArray(copy.queue) &&
      Array.isArray(copy.entries)
      ? copy
      : null;
  } catch {
    return null;
  }
}

/**
 * @param {Storage|null} storage
 * @param {string} key draft record key
 */
export function removeUnloadCopy(storage, key) {
  try {
    storage?.removeItem(unloadCopyKey(key));
  } catch {
    // Blocked storage holds no copy.
  }
}

// Every readable unload copy in a storage.
function unloadCopies(storage) {
  const keys = [];

  try {
    for (let index = 0; index < (storage?.length || 0); index += 1) {
      const name = storage.key(index);

      if (name?.startsWith(UNLOAD_COPY_PREFIX)) {
        keys.push(name.slice(UNLOAD_COPY_PREFIX.length));
      }
    }
  } catch {
    // Blocked storage holds no copy.
  }

  return keys.map((key) => readUnloadCopy(storage, key)).filter(Boolean);
}

// When a draft record or unload copy was written, in milliseconds, or NaN.
function writtenAt(value) {
  const at = value?.updatedAt;

  return typeof at === 'number' ? at : Date.parse(at);
}

/**
 * Whether the database record was written after the unload copy. The record
 * then holds newer work (another tab, or this page after the user stayed).
 */
export function staleUnloadCopy(record, copy) {
  return Boolean(record && copy) && writtenAt(record) > writtenAt(copy);
}

/**
 * Merges a draft's unload copy into the database record, unless the copy is
 * stale. The copy's queue, ETag, head and cursor win, and fromUnload is set.
 * With snapshots false, only the entries' metadata merges.
 */
export function mergeUnloadCopy(record, copy, { snapshots = true } = {}) {
  if (!copy || staleUnloadCopy(record, copy)) {
    return record;
  }

  const held = new Map(
    (record?.entries || []).map((entry) => [entry.id, entry.snapshot]),
  );
  const entries = copy.entries.flatMap((entry) => {
    if (!snapshots) {
      return [entryMeta(entry)];
    }

    const snapshot =
      entry.snapshot !== undefined ? entry.snapshot : held.get(entry.id);

    return snapshot === undefined ? [] : [{ ...entry, snapshot }];
  });

  return { ...record, ...copy, entries, fromUnload: true };
}

// A draft's record with its unload copy merged in (see mergeUnloadCopy). A
// copy older than the record is removed: it would never be merged again.
export function withUnloadCopy(record, copy, storage, options) {
  if (staleUnloadCopy(record, copy)) {
    removeUnloadCopy(storage, copy.key);
  }

  return mergeUnloadCopy(record, copy, options);
}

// Every record, with the unload copies merged in: a copy of a draft the
// database holds no record for is a record of its own.
export function withUnloadCopies(records, storage) {
  const copies = new Map(unloadCopies(storage).map((copy) => [copy.key, copy]));
  const merged = records.map((record) => {
    const copy = copies.get(record.key);

    copies.delete(record.key);

    return withUnloadCopy(record, copy, storage, { snapshots: false });
  });

  return [
    ...merged,
    ...[...copies.values()].map((copy) =>
      mergeUnloadCopy(undefined, copy, { snapshots: false }),
    ),
  ];
}

// A record, with its unload copy merged in (see mergeUnloadCopy), as it
// moves to another key. undefined when there is neither.
export function movedRecord(record, copy, key, patch) {
  const merged = mergeUnloadCopy(record, copy);

  if (!merged) {
    return undefined;
  }

  const { fromUnload: _, ...moved } = merged;

  return { ...moved, ...patch, key };
}

// Joins a draft record with its entry records, in the record's order. An
// entry whose snapshot is missing is left out.
export function joinEntries(record, stored) {
  const snapshots = new Map(stored.map((entry) => [entry.id, entry.snapshot]));

  return {
    ...record,
    entries: (record.entries || [])
      .filter((entry) => snapshots.has(entry.id))
      .map((entry) => ({ ...entry, snapshot: snapshots.get(entry.id) })),
  };
}

/**
 * Opens the Builder database, or null. onClose runs when another tab's upgrade
 * or a delete closes it.
 */
function openBuilderDb(factory, onClose) {
  const idb = factoryOf(factory);

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

      if (!db.objectStoreNames.contains(ENTRY_STORE)) {
        db.createObjectStore(ENTRY_STORE, {
          keyPath: ['draft', 'id'],
        }).createIndex('draft', 'draft');
      }
    };

    request.onsuccess = () => {
      const db = request.result;

      // Another tab upgrading the database, or a sign out deleting it,
      // waits until this connection closes.
      db.onversionchange = () => {
        db.close();
        onClose?.();
      };

      resolve(db);
    };
    request.onerror = () => resolve(null);
    request.onblocked = () => resolve(null);
  });
}

/**
 * Creates the store that the autosave queue uses. options.storage keeps the
 * unload copies.
 */
export function createDraftStore(options = {}) {
  const born = generation;
  let dbPromise = null;
  const storage = () =>
    options.storage === undefined ? pageStorage() : options.storage;

  const db = () => {
    if (!dbPromise) {
      dbPromise = clearing.then(() =>
        openBuilderDb(options.factory, () => {
          dbPromise = null;
        }),
      );
    }
    return dbPromise;
  };

  // Opens a write transaction over both stores. Unlike the reads, a write
  // that cannot happen is an error: callers tell the user whether work is
  // kept on this device.
  const writing = async () => {
    if (born !== generation) {
      throw new Error('The drafts on this device were cleared.');
    }

    const database = await db();

    if (!database) {
      throw new Error('This browser offers no local storage for drafts.');
    }

    return database.transaction(STORES, 'readwrite');
  };

  const reading = async (action) => {
    const database = await db();

    if (!database) {
      return null;
    }

    try {
      return await action(database.transaction(STORES, 'readonly'));
    } catch {
      return null;
    }
  };

  return {
    /**
     * Stores a draft record, and in the same transaction the entry snapshots in
     * `write` and the drop of the snapshots in `drop`.
     */
    async put(record, { write = [], drop = [] } = {}) {
      const stored = storedRecord(record);
      const snapshots = write.map((entry) => storedEntry(record.key, entry));
      const tx = await writing();
      const done = completion(tx);
      const entries = tx.objectStore(ENTRY_STORE);

      drop.forEach((id) => entries.delete([record.key, id]));
      snapshots.forEach((entry) => entries.put(entry));
      tx.objectStore(STORE_NAME).put(stored);
      await done;

      return record.key;
    },

    /**
     * @param {string} key
     * @returns {Promise<object|undefined>} the record, its entries with
     *   their snapshots, and its unload copy merged in (see mergeUnloadCopy)
     */
    async get(key) {
      const found = await reading(async (tx) => {
        const [record, entries] = await Promise.all([
          promisify(tx.objectStore(STORE_NAME).get(key)),
          promisify(tx.objectStore(ENTRY_STORE).index('draft').getAll(key)),
        ]);

        return record ? joinEntries(record, entries) : undefined;
      });

      const kept = storage();

      return withUnloadCopy(found, readUnloadCopy(kept, key), kept);
    },

    /** Deletes a draft record, its entry snapshots and its unload copy. */
    async remove(key) {
      removeUnloadCopy(storage(), key);

      const tx = await writing();
      const done = completion(tx);
      const entries = tx.objectStore(ENTRY_STORE);

      tx.objectStore(STORE_NAME).delete(key);
      entries.index('draft').getAllKeys(key).onsuccess = (event) => {
        event.target.result.forEach((entry) => entries.delete(entry));
      };
      await done;
    },

    /**
     * @returns {Promise<object[]>} every draft record, without snapshots,
     *   with the unload copies merged in
     */
    async all() {
      const result = await reading((tx) =>
        promisify(tx.objectStore(STORE_NAME).getAll()),
      );

      return withUnloadCopies(result || [], storage());
    },

    /**
     * Moves a draft record, with its snapshots and unload copy, to another key
     * in one transaction. A tab takes a closed tab's queue this way (see
     * tabs.js).
     */
    async rekey(from, to, patch = {}) {
      const kept = storage();
      const tx = await writing();
      const done = completion(tx);
      const drafts = tx.objectStore(STORE_NAME);
      const entries = tx.objectStore(ENTRY_STORE);
      const found = drafts.get(from);
      const snapshots = entries.index('draft').getAll(from);
      let moved;
      // The unload copy taken, put back if the move fails.
      let copy = null;

      // Requests settle in the order they were made, so both reads are done
      // here. Two moves of one record, by this page or another, run one after
      // the other. The unload copy is taken as the record is read, not when
      // the move is complete, because the other page may not wait for that.
      // The second move finds neither.
      snapshots.onsuccess = () => {
        copy = readUnloadCopy(kept, from);
        removeUnloadCopy(kept, from);
        moved = movedRecord(
          found.result
            ? joinEntries(found.result, snapshots.result)
            : undefined,
          copy,
          to,
          patch,
        );

        if (!moved) {
          return;
        }

        snapshots.result.forEach((entry) => entries.delete([from, entry.id]));
        drafts.delete(from);
        moved.entries.forEach((entry) => entries.put(storedEntry(to, entry)));
        drafts.put(storedRecord(moved));
      };

      try {
        await done;
      } catch (error) {
        if (copy) {
          writeUnloadCopy(kept, copy, copy.entries);
        }

        throw error;
      }

      return moved;
    },

    /**
     * Writes the unload copy at once as the page closes. A store made before a
     * clear writes none.
     */
    keep(record, { write = [] } = {}) {
      return born === generation && writeUnloadCopy(storage(), record, write);
    },

    /**
     * Removes a draft record's unload copy, once the database holds it.
     *
     * @param {string} key
     */
    release(key) {
      removeUnloadCopy(storage(), key);
    },
  };
}

// Deletes the database outright. It resolves false when another tab keeps
// it open: the deletion then completes once that tab closes it.
function deleteDatabase(idb) {
  return new Promise((resolve) => {
    let request;

    try {
      request = idb.deleteDatabase(DB_NAME);
    } catch {
      resolve(false);
      return;
    }

    request.onsuccess = () => resolve(true);
    request.onerror = () => resolve(false);
    request.onblocked = () => resolve(false);
  });
}

/**
 * Deletes every local Builder record on this device, for every user. Stores
 * created before the call refuse to write afterwards, so a settling save cannot
 * store its draft again. It never rejects.
 */
export function clearBuilderDatabase(options = {}) {
  generation += 1;
  clearing = clearRecords(options);

  return clearing;
}

async function clearRecords(options) {
  const idb = factoryOf(options.factory);

  if (!idb) {
    return true;
  }

  const database = await openBuilderDb(idb);

  if (!database) {
    return deleteDatabase(idb);
  }

  try {
    const tx = database.transaction(STORES, 'readwrite');
    const done = completion(tx);

    STORES.forEach((name) => tx.objectStore(name).clear());
    await done;

    return true;
  } catch {
    return deleteDatabase(idb);
  } finally {
    database.close();
  }
}
