// Minimal IndexedDB helper for the builder's offline autosave queue.
//
// Deliberately tiny (no external dependency) and fully injectable so unit
// tests can pass a fake factory. Reads degrade to an empty result when
// IndexedDB is unavailable (private windows, SSR, older embedded browsers);
// a write that cannot happen throws, so the queue never claims to have kept
// work it did not keep.
//
// A draft's record keeps only what its queue needs to replay: the queue, the
// ETag it was based on, and the metadata of the history entries the queue
// refers to. Each entry's document snapshot is a record of its own, keyed by
// the draft record and the commit id, and is written once: an edit stores
// one snapshot and a small record, never the whole history again.

const DB_NAME = 'phenix-builder';
// Version 1 kept every entry's snapshot inside the draft record; version 2
// moves them to ENTRY_STORE (see splitRecord).
const DB_VERSION = 2;
const STORE_NAME = 'drafts';
const ENTRY_STORE = 'entries';

const STORES = [STORE_NAME, ENTRY_STORE];

// Bumped by clearBuilderDatabase(). A store created before then refuses to
// write, so a queue still settling a save when the user signs out cannot
// store the draft again.
let generation = 0;

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

// Settles when a write transaction has committed, or rejects when it failed.
function completion(tx) {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
    tx.onabort = () =>
      reject(tx.error || new Error('The local draft was not stored.'));
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

// Records hold documents taken from the editor's reactive state, and a Vue
// proxy cannot be structured-cloned (DataCloneError). Records are plain
// JSON, so a JSON copy stores exactly the data and no proxies.
function plain(value) {
  return JSON.parse(JSON.stringify(value));
}

// The draft record a put stores: its entries' metadata, without snapshots.
function storedRecord(record) {
  return plain({ ...record, entries: (record.entries || []).map(entryMeta) });
}

// The entry record a put stores for one entry's snapshot.
function storedEntry(key, entry) {
  return plain({ draft: key, id: entry.id, snapshot: entry.snapshot });
}

/**
 * Splits a draft record as version 1 stored it, with every history entry's
 * snapshot inline, into the version 2 draft record and its entry records.
 * Only what the queue can still replay is kept: a record with nothing
 * queued, and every entry no queued operation refers to, are dropped.
 *
 * @param {object} record version 1 draft record
 * @returns {{record: object, entries: object[]}|null} null when the record
 *   is to be deleted
 */
export function splitRecord(record) {
  const queue = Array.isArray(record?.queue) ? record.queue : [];

  if (!record?.key || queue.length === 0) {
    return null;
  }

  const needed = new Set(queue.map((op) => op?.commitId).filter(Boolean));
  const kept = (Array.isArray(record.entries) ? record.entries : []).filter(
    (entry) => entry && needed.has(entry.id) && entry.snapshot !== undefined,
  );

  return {
    record: { ...record, entries: kept.map(entryMeta) },
    entries: kept.map((entry) => ({
      draft: record.key,
      id: entry.id,
      snapshot: entry.snapshot,
    })),
  };
}

// Joins a draft record with its entry records, in the record's order. An
// entry whose snapshot is missing is left out.
function joinEntries(record, stored) {
  const snapshots = new Map(stored.map((entry) => [entry.id, entry.snapshot]));

  return {
    ...record,
    entries: (record.entries || [])
      .filter((entry) => snapshots.has(entry.id))
      .map((entry) => ({ ...entry, snapshot: snapshots.get(entry.id) })),
  };
}

// Moves version 1 records to the version 2 layout, inside the upgrade
// transaction. A record that cannot be split is deleted rather than left
// behind in the old layout.
function migrate(tx) {
  const entries = tx.objectStore(ENTRY_STORE);

  tx.objectStore(STORE_NAME).openCursor().onsuccess = (event) => {
    const cursor = event.target.result;

    if (!cursor) {
      return;
    }

    try {
      const split = splitRecord(cursor.value);

      if (split) {
        split.entries.forEach((entry) => entries.put(entry));
        cursor.update(split.record);
      } else {
        cursor.delete();
      }
    } catch {
      cursor.delete();
    }

    cursor.continue();
  };
}

/**
 * Opens (and upgrades) the builder database.
 *
 * @param {IDBFactory} [factory]
 * @param {Function} [onClose] called when the connection is closed for
 *   another tab's upgrade, or a deletion of the database
 * @returns {Promise<IDBDatabase|null>} null when unavailable
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

    request.onupgradeneeded = (event) => {
      const db = request.result;

      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: 'key' });
      }

      if (!db.objectStoreNames.contains(ENTRY_STORE)) {
        db.createObjectStore(ENTRY_STORE, {
          keyPath: ['draft', 'id'],
        }).createIndex('draft', 'draft');
      }

      if (event.oldVersion === 1) {
        migrate(request.transaction);
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
 * Creates the store facade used by the autosave queue.
 *
 * @param {object} [options] factory
 * @returns {{put: Function, get: Function, remove: Function, all: Function}}
 */
export function createDraftStore(options = {}) {
  const born = generation;
  let dbPromise = null;

  const db = () => {
    if (!dbPromise) {
      dbPromise = openBuilderDb(options.factory, () => {
        dbPromise = null;
      });
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
     * Stores a draft record with its entries' metadata and, in the same
     * transaction, the entry snapshots it adds and drops.
     *
     * @param {object} record
     * @param {object} [changes] write: entries whose snapshots to store;
     *   drop: commit ids of stored snapshots the record no longer needs
     * @throws {Error} when the record could not be stored
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
     *   their snapshots
     */
    async get(key) {
      return reading(async (tx) => {
        const [record, entries] = await Promise.all([
          promisify(tx.objectStore(STORE_NAME).get(key)),
          promisify(tx.objectStore(ENTRY_STORE).index('draft').getAll(key)),
        ]);

        return record ? joinEntries(record, entries) : undefined;
      });
    },

    /**
     * Deletes a draft record and its entry snapshots.
     *
     * @param {string} key
     * @throws {Error} when they could not be deleted
     */
    async remove(key) {
      const tx = await writing();
      const done = completion(tx);
      const entries = tx.objectStore(ENTRY_STORE);

      tx.objectStore(STORE_NAME).delete(key);
      entries.index('draft').getAllKeys(key).onsuccess = (event) => {
        event.target.result.forEach((entry) => entries.delete(entry));
      };
      await done;
    },

    /** @returns {Promise<object[]>} every draft record, without snapshots */
    async all() {
      const result = await reading((tx) =>
        promisify(tx.objectStore(STORE_NAME).getAll()),
      );

      return result || [];
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
 * Deletes every local Builder record on this device, for every user: the
 * draft queues and their history snapshots. Sign out calls it. Stores
 * created before the call refuse to write afterwards, so a queue still
 * settling a save cannot store its draft again. It never rejects.
 *
 * @param {object} [options] factory
 * @returns {Promise<boolean>} whether the records are gone
 */
export async function clearBuilderDatabase(options = {}) {
  generation += 1;

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

/**
 * The database's stand-in in unit tests, with the same surface. Like the
 * database, it keeps each entry's snapshot apart from the draft record. It
 * stores what the database's put stores, as IndexedDB does: a structured
 * clone, which each read clones again. Nothing it holds shares an object with
 * its caller, and a record IndexedDB could not clone fails here too.
 *
 * @returns {{put: Function, get: Function, remove: Function, all: Function,
 *   snapshots: Map}}
 */
export function createMemoryStore() {
  const records = new Map();
  // Draft key to a map of commit id to entry record.
  const snapshots = new Map();

  return {
    snapshots,
    async put(record, { write = [], drop = [] } = {}) {
      const stored = structuredClone(storedRecord(record));
      const written = write.map((entry) =>
        structuredClone(storedEntry(record.key, entry)),
      );
      const entries = snapshots.get(record.key) || new Map();

      drop.forEach((id) => entries.delete(id));
      written.forEach((entry) => entries.set(entry.id, entry));
      snapshots.set(record.key, entries);
      records.set(record.key, stored);

      return record.key;
    },
    async get(key) {
      if (!records.has(key)) {
        return undefined;
      }

      return joinEntries(
        structuredClone(records.get(key)),
        structuredClone([...(snapshots.get(key)?.values() || [])]),
      );
    },
    async remove(key) {
      records.delete(key);
      snapshots.delete(key);
    },
    async all() {
      return structuredClone([...records.values()]);
    },
  };
}
