// The IndexedDB draft store, over a small IndexedDB of its own: two pages
// of one browser share the database and localStorage.

import { describe, expect, test } from 'vitest';

import {
  createDraftStore,
  draftKey,
  tabRecordKey,
  unloadCopyKey,
} from '@/builder/idb.js';

// IndexedDB as a browser keeps it for every page of an origin: one write
// transaction runs at a time, and its requests settle in order. The next
// one starts as one commits, before the page that made it hears it is
// complete, as it may when the two pages are not the same. With `failNext`,
// the next write transaction aborts, and nothing it wrote is kept.
function fakeIndexedDB() {
  const stores = { drafts: new Map(), entries: new Map() };
  const waiting = [];
  let running = null;
  const fake = { failNext: false, stores };

  const copy = (value) =>
    value === undefined ? undefined : structuredClone(value);
  const entryKey = (key) => JSON.stringify(key);

  function request(tx, action) {
    const made = { result: undefined, onsuccess: null, onerror: null };

    tx.requests.push({ made, action });

    return made;
  }

  function objectStore(tx, name) {
    const items = stores[name];
    const keyOf = (value) =>
      name === 'drafts' ? value.key : entryKey([value.draft, value.id]);
    const find = (key) => (name === 'drafts' ? key : entryKey(key));

    return {
      get: (key) => request(tx, () => copy(items.get(find(key)))),
      put: (value) =>
        request(tx, () => {
          items.set(keyOf(value), copy(value));
        }),
      delete: (key) =>
        request(tx, () => {
          items.delete(find(key));
        }),
      getAll: () => request(tx, () => [...items.values()].map(copy)),
      index: () => ({
        getAll: (draft) =>
          request(tx, () =>
            [...items.values()]
              .filter((value) => value.draft === draft)
              .map(copy),
          ),
        getAllKeys: (draft) =>
          request(tx, () =>
            [...items.values()]
              .filter((value) => value.draft === draft)
              .map((value) => [value.draft, value.id]),
          ),
      }),
    };
  }

  function start() {
    if (running || waiting.length === 0) {
      return;
    }

    running = waiting.shift();
    setTimeout(() => run(running), 0);
  }

  function run(tx) {
    const before = {
      drafts: new Map(stores.drafts),
      entries: new Map(stores.entries),
    };
    const fails = tx.mode === 'readwrite' && fake.failNext;

    if (fails) {
      fake.failNext = false;
    }

    // Requests made as one settles join the end.
    while (tx.requests.length > 0) {
      const { made, action } = tx.requests.shift();

      made.result = action();
      made.onsuccess?.({ target: made });
    }

    if (fails) {
      stores.drafts = before.drafts;
      stores.entries = before.entries;
    }

    running = null;
    start();
    setTimeout(() => (fails ? tx.onabort?.() : tx.oncomplete?.()), 0);
  }

  const db = {
    objectStoreNames: { contains: () => true },
    close() {},
    transaction(_, mode) {
      const tx = { mode, requests: [] };

      tx.objectStore = (name) => objectStore(tx, name);
      waiting.push(tx);
      start();

      return tx;
    },
  };

  fake.open = () => {
    const opening = { result: db };

    setTimeout(() => opening.onsuccess?.(), 0);

    return opening;
  };

  return fake;
}

// One origin's localStorage.
function localStorageFake() {
  const items = new Map();

  return {
    items,
    get length() {
      return items.size;
    },
    key: (index) => [...items.keys()][index] ?? null,
    getItem: (key) => items.get(key) ?? null,
    setItem: (key, value) => items.set(key, value),
    removeItem: (key) => items.delete(key),
  };
}

const BASE = draftKey('alice', 'alice', 'd1');
const edit = (id) => ({
  id,
  label: `Edit ${id}`,
  snapshot: { name: id, nodes: [], edges: [] },
});

// The queue a closed tab left, with its last change in an unload copy.
async function closedTabQueue(factory, storage) {
  const store = createDraftStore({ factory, storage });
  const from = tabRecordKey(BASE, 'gone');
  const record = {
    key: from,
    tab: 'gone',
    actor: 'alice',
    owner: 'alice',
    draftId: 'd1',
    entries: [{ id: 'e1', label: 'Edit e1' }],
    queue: [{ opId: 'e1', kind: 'snapshot', commitId: 'e1' }],
  };

  await store.put(record, { write: [edit('e1')] });
  store.keep(
    {
      ...record,
      entries: [{ id: 'e1', label: 'Edit e1' }, edit('e2')],
      queue: [
        ...record.queue,
        { opId: 'e2', kind: 'snapshot', commitId: 'e2' },
      ],
    },
    { write: [edit('e2')] },
  );

  return from;
}

describe('taking the queue a closed tab left', () => {
  test('two pages taking it at once: one takes it, with its unload copy; the other finds nothing', async () => {
    const factory = fakeIndexedDB();
    const storage = localStorageFake();
    const from = await closedTabQueue(factory, storage);
    const pages = ['A', 'B'].map((tab) => ({
      tab,
      store: createDraftStore({ factory, storage }),
    }));

    const moved = await Promise.all(
      pages.map(({ tab, store }) =>
        store.rekey(from, tabRecordKey(BASE, tab), { tab }),
      ),
    );

    expect(moved.filter(Boolean)).toHaveLength(1);
    expect(moved[0]?.queue.map((op) => op.opId)).toEqual(['e1', 'e2']);
    expect([...factory.stores.drafts.keys()]).toEqual([
      tabRecordKey(BASE, 'A'),
    ]);
    expect(storage.items.has(unloadCopyKey(from))).toBe(false);
  });

  test('a move that fails keeps the record and puts its unload copy back', async () => {
    const factory = fakeIndexedDB();
    const storage = localStorageFake();
    const from = await closedTabQueue(factory, storage);
    const copy = storage.getItem(unloadCopyKey(from));
    const store = createDraftStore({ factory, storage });

    factory.failNext = true;
    await expect(
      store.rekey(from, tabRecordKey(BASE, 'A'), { tab: 'A' }),
    ).rejects.toThrow();

    expect(JSON.parse(storage.getItem(unloadCopyKey(from)))).toEqual(
      JSON.parse(copy),
    );
    expect([...factory.stores.drafts.keys()]).toEqual([from]);
    expect((await store.get(from)).queue.map((op) => op.opId)).toEqual([
      'e1',
      'e2',
    ]);
  });
});
