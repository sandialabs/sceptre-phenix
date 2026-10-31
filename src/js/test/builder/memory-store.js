// The draft database's stand-in for unit tests (createDraftStore in idb.js).
import {
  joinEntries,
  movedRecord,
  readUnloadCopy,
  removeUnloadCopy,
  storedEntry,
  storedRecord,
  withUnloadCopies,
  withUnloadCopy,
  writeUnloadCopy,
} from '@/builder/idb.js';

// Keeps structured clones, as IndexedDB does. storage keeps the unload copies.
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
