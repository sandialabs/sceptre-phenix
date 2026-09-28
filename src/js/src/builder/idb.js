// Minimal IndexedDB helper for the builder's offline autosave queue.
//
// Deliberately tiny (no external dependency) and fully injectable so unit
// tests can pass a fake factory. Reads degrade to an empty result when
// IndexedDB is unavailable (private windows, SSR, older embedded browsers);
// a write that cannot happen throws, so the queue never claims to have kept
// work it did not keep.
//
// Each tab keeps a record of its own for a draft (see tabRecordKey and
// tabs.js), so two tabs editing one draft never write each other's queue.
// A draft's record keeps only what its queue needs to replay: the queue, the
// ETag it was based on, and the metadata of the history entries the queue
// refers to. Each entry's document snapshot is a record of its own, keyed by
// the draft record and the commit id, and is written once: an edit stores
// one snapshot and a small record, never the whole history again.
//
// A write to IndexedDB finishes after the call that makes it, and leaving
// the page (a reload, a closed tab) can cut it off, losing such edits as the
// one applied as the page is left (see leave.js). So the page keeps, as it
// is left, an unload copy in localStorage, whose writes finish at once: the
// draft record, with the snapshots of the entries the database may not
// hold (see keepForUnload in autosave.js). Reads merge it into the database's
// record (see mergeUnloadCopy), unless the database was written after it,
// and the queue removes it once the database holds what it copied. It is
// keyed by the draft record's key, so by user, draft and tab, under
// phenix.builder., which logout and another user's sign-in clear (see
// session.js).

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

// The last clearBuilderDatabase(). A store opens the database once it has
// settled, so a Builder opened just after a sign-in neither reads the
// records it clears nor writes records it would clear.
let clearing = Promise.resolve(true);

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

/**
 * Key of one tab's record of a draft (see tabs.js). A record kept before
 * tabs had ids, or by a queue with no tab, has the draft's key alone.
 *
 * @param {string} key draftKey()
 * @param {string} [tab] the tab's id
 * @returns {string}
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

// What the localStorage key of an unload copy starts with.
export const UNLOAD_COPY_PREFIX = 'phenix.builder.unload.';

/**
 * @param {string} key draft record key (see draftKey)
 * @returns {string} the localStorage key of its unload copy
 */
export function unloadCopyKey(key) {
  return `${UNLOAD_COPY_PREFIX}${key}`;
}

// The page's localStorage; reading it throws where site data is blocked.
function pageStorage() {
  try {
    return typeof window === 'undefined' ? null : window.localStorage || null;
  } catch {
    return null;
  }
}

/**
 * Writes the unload copy of a draft record: the record, and the snapshots
 * of the entries in `write`. It never throws. A copy that does not fit in
 * localStorage (its quota is a few megabytes) is not written, and an older
 * copy of the draft is removed, so it cannot stand in for newer work.
 *
 * @param {Storage|null} storage
 * @param {object} record draft record, its entries with their snapshots
 * @param {object[]} [write] the entries whose snapshots to copy
 * @returns {boolean} whether the copy was written
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
 * Whether a draft's unload copy is older than the record the database holds
 * for it. The copy carries the time of the last write its page asked for;
 * a record written after that, by another tab or by the same page once the
 * user stayed, stands for newer work. A draft record is the last writer's,
 * as the database's own writes are, so such a copy is set aside.
 *
 * @param {object|undefined} record the database's record, if any
 * @param {object|null} copy the unload copy, if any
 * @returns {boolean} whether the record was written after the copy
 */
export function staleUnloadCopy(record, copy) {
  return Boolean(record && copy) && writtenAt(record) > writtenAt(copy);
}

/**
 * Merges a draft's unload copy into the record the database holds for it.
 * A copy older than the record is left out (see staleUnloadCopy). Otherwise
 * the copy stands for the page's last write, which leaving the page may have
 * cut off, so its queue, ETag, head and cursor replace the record's: an
 * operation both hold is kept once, and one only the database holds was
 * sent since. Each entry the copy's queue needs is kept once, with the
 * copy's snapshot or else the database's; one neither holds is left out, as
 * a read of the database leaves it out.
 *
 * @param {object|undefined} record the database's record, if any
 * @param {object|null} copy the unload copy, if any
 * @param {object} [options] snapshots: false to merge the entries'
 *   metadata alone, as all() lists records
 * @returns {object|undefined} the record; with a copy merged into it,
 *   fromUnload is true
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
function withUnloadCopy(record, copy, storage, options) {
  if (staleUnloadCopy(record, copy)) {
    removeUnloadCopy(storage, copy.key);
  }

  return mergeUnloadCopy(record, copy, options);
}

// Every record, with the unload copies merged in: a copy of a draft the
// database holds no record for is a record of its own.
function withUnloadCopies(records, storage) {
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

// A record, with its unload copy merged in (see mergeUnloadCopy), as it is
// moved to another key; undefined when there is neither.
function movedRecord(record, copy, key, patch) {
  const merged = mergeUnloadCopy(record, copy);

  if (!merged) {
    return undefined;
  }

  const { fromUnload: _, ...moved } = merged;

  return { ...moved, ...patch, key };
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
 * @param {object} [options] factory; storage: the Storage that keeps the
 *   unload copies, the page's localStorage by default
 * @returns {{put: Function, get: Function, remove: Function, all: Function,
 *   rekey: Function, keep: Function, release: Function}}
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

    /**
     * Deletes a draft record, its entry snapshots and its unload copy.
     *
     * @param {string} key
     * @throws {Error} when they could not be deleted
     */
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
     * Moves a draft record, with its entry snapshots and its unload copy
     * merged in, to another key, in one transaction: a tab takes the queue
     * a closed tab left (see tabs.js).
     *
     * @param {string} from
     * @param {string} to
     * @param {object} [patch] what the moved record changes (its tab)
     * @returns {Promise<object|undefined>} the record as moved, if any
     * @throws {Error} when it could not be moved
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

      // Requests settle in the order they were made, so both are read. Two
      // moves of one record, by this page or another, run one after the
      // other. The unload copy is taken as the record is read, not once
      // the move is complete, which the other page may not wait for: the
      // second move finds neither.
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
     * Writes a draft record's unload copy at once, as the page is left (see
     * writeUnloadCopy). A store made before the drafts on this device were
     * cleared writes none.
     *
     * @param {object} record
     * @param {object} [changes] write: the entries whose snapshots to copy
     * @returns {boolean} whether the copy was written
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
 * Deletes every local Builder record on this device, for every user: the
 * draft queues and their history snapshots. Sign out calls it. Stores
 * created before the call refuse to write afterwards, so a queue still
 * settling a save cannot store its draft again. It never rejects.
 *
 * @param {object} [options] factory
 * @returns {Promise<boolean>} whether the records are gone
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

/**
 * The database's stand-in in unit tests, with the same surface. Like the
 * database, it keeps each entry's snapshot apart from the draft record. It
 * stores what the database's put stores, as IndexedDB does: a structured
 * clone, which each read clones again. Nothing it holds shares an object with
 * its caller, and a record IndexedDB could not clone fails here too.
 *
 * @param {object} [options] storage: the Storage that keeps the unload
 *   copies; without one, none is kept
 * @returns {{put: Function, get: Function, remove: Function, all: Function,
 *   rekey: Function, keep: Function, release: Function, snapshots: Map}}
 */
export function createMemoryStore({ storage = null } = {}) {
  const records = new Map();
  // Draft key to a map of commit id to entry record.
  const snapshots = new Map();

  const memory = {
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
      const found = records.has(key)
        ? joinEntries(
            structuredClone(records.get(key)),
            structuredClone([...(snapshots.get(key)?.values() || [])]),
          )
        : undefined;

      return withUnloadCopy(found, readUnloadCopy(storage, key), storage);
    },
    async remove(key) {
      removeUnloadCopy(storage, key);
      records.delete(key);
      snapshots.delete(key);
    },
    async all() {
      return withUnloadCopies(structuredClone([...records.values()]), storage);
    },
    async rekey(from, to, patch = {}) {
      const moved = movedRecord(
        records.has(from)
          ? joinEntries(
              structuredClone(records.get(from)),
              structuredClone([...(snapshots.get(from)?.values() || [])]),
            )
          : undefined,
        readUnloadCopy(storage, from),
        to,
        patch,
      );

      records.delete(from);
      snapshots.delete(from);
      removeUnloadCopy(storage, from);

      if (moved) {
        await memory.put(moved, { write: moved.entries });
      }

      return moved;
    },
    keep(record, { write = [] } = {}) {
      return writeUnloadCopy(storage, record, write);
    },
    release(key) {
      removeUnloadCopy(storage, key);
    },
  };

  return memory;
}
