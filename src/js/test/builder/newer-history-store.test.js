// The newer snapshots that the next change removes, in the store: what
// newerHistory says after an undo or a restore, and what Return to latest
// and Save newer history as a new draft do (store.js, newerHistory.js).

import { beforeEach, describe, expect, test, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

// A server that keeps one draft's snapshots and cursor as the real one
// does: a save after the cursor removes the snapshots after it.
const server = vi.hoisted(() => ({
  snapshots: [],
  cursor: 0,
  documents: new Map(),
  etag: 1,
  saves: 0,
  user: 'alice',
}));

const api = vi.hoisted(() => {
  const draft = () => {
    const current = server.snapshots[server.cursor];

    return {
      id: 'd1',
      createdBy: 'alice',
      title: 'Pump station',
      snapshotId: current.id,
      cursor: server.cursor,
      snapshots: server.snapshots.length,
      updated: current.createdAt,
      lastModifiedBy: server.user,
    };
  };
  const listed = () =>
    server.snapshots.map((snapshot, index) => ({
      ...snapshot,
      current: index === server.cursor,
    }));
  const answer = (extra = {}) => {
    server.etag += 1;

    return {
      draft: draft(),
      document: null,
      history: null,
      cursor: server.cursor,
      etag: `"${server.etag}"`,
      ...extra,
    };
  };

  return {
    listed,
    getDraft: vi.fn(async () => ({
      ...answer(),
      document: server.documents.get(server.snapshots[server.cursor].id),
      history: listed(),
    })),
    appendSnapshot: vi.fn(async (_, payload) => {
      server.saves += 1;

      const snapshot = {
        id: `s${server.snapshots.length + 1}-${server.saves}`,
        createdBy: server.user,
        createdAt: `2026-10-26T10:${String(server.saves).padStart(2, '0')}:00Z`,
        summary: payload.summary,
      };

      server.snapshots = [
        ...server.snapshots.slice(0, server.cursor + 1),
        snapshot,
      ];
      server.cursor = server.snapshots.length - 1;
      server.documents.set(snapshot.id, payload.document);

      return answer({
        draft: {
          ...draft(),
          stamp: { updatedBy: server.user, updatedAt: snapshot.createdAt },
        },
      });
    }),
    moveCursor: vi.fn(async (_, { snapshotId }) => {
      server.cursor = server.snapshots.findIndex(
        (snapshot) => snapshot.id === snapshotId,
      );

      return answer();
    }),
    listSnapshots: vi.fn(async () => listed()),
    getSnapshot: vi.fn(async (_, snapshotId) => ({
      document: server.documents.get(snapshotId),
    })),
    listDrafts: vi.fn(async () => ({
      all: [{ id: 'd1', title: 'Pump station' }],
      published: [],
      damaged: [],
    })),
    createDraft: vi.fn(async (request) => ({
      draft: { id: 'd9', createdBy: 'alice', title: request.title },
      document: request.document,
      history: null,
      cursor: 0,
      etag: '"1"',
    })),
  };
});

vi.mock('@/builder/api.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, builderApi: { ...actual.builderApi, ...api } };
});

vi.mock('@/builder/idb.js', async (importOriginal) => {
  const actual = await importOriginal();

  return { ...actual, createDraftStore: () => createMemoryStore() };
});

import { useBuilderStore } from '@/builder/store.js';

import { sampleDocument } from './fixtures.js';
import { createMemoryStore } from './memory-store.js';

let store;

// Opens the draft, which the server holds with one snapshot by bob.
async function openDraft() {
  const { doc } = sampleDocument();

  server.snapshots = [
    {
      id: 's1',
      createdBy: 'bob',
      createdAt: '2026-10-26T09:00:00Z',
      summary: '',
    },
  ];
  server.cursor = 0;
  server.documents = new Map([
    ['s1', { ...doc, metadata: { ...doc.metadata, name: 'Pump station' } }],
  ]);
  server.saves = 0;
  server.user = 'alice';

  await store.loadDraft('d1');
}

// Adds `n` devices, each one change saved as one snapshot.
async function addDevices(n) {
  for (let i = 0; i < n; i += 1) {
    store.addNode({ kind: 'device', hostname: `added-${i}` });
  }

  await store.saveNow();
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  store = useBuilderStore();
});

describe('newerHistory', () => {
  test('is null while the draft shows its newest snapshot', async () => {
    await openDraft();
    expect(store.newerHistory).toBeNull();

    await addDevices(2);
    expect(store.newerHistory).toBeNull();
    expect(store.serverHistory.map((entry) => entry.id)).toEqual(
      api.listed().map((entry) => entry.id),
    );
  });

  test('counts the snapshots after an undo, with who saved the newest and when', async () => {
    await openDraft();
    await addDevices(3);

    store.undo();
    // Not before the server moved its cursor.
    expect(store.newerHistory).toBeNull();
    await store.saveNow();

    expect(store.newerHistory).toEqual({
      count: 1,
      newest: expect.objectContaining({
        id: server.snapshots.at(-1).id,
        createdBy: 'alice',
        createdAt: server.snapshots.at(-1).createdAt,
      }),
      saved: '',
    });

    store.undo();
    store.undo();
    await store.saveNow();

    expect(store.newerHistory.count).toBe(3);
    expect(api.listSnapshots).not.toHaveBeenCalled();
  });

  test('a change after the undo removes them, and the notice goes', async () => {
    await openDraft();
    await addDevices(2);
    store.undo();
    await store.saveNow();
    expect(store.newerHistory.count).toBe(1);

    store.addNode({ kind: 'device', hostname: 'later' });
    // The change is not saved yet: it is the newest already.
    expect(store.newerHistory).toBeNull();

    await store.saveNow();
    expect(store.newerHistory).toBeNull();
    expect(server.snapshots).toHaveLength(3);
    expect(store.serverHistory.map((entry) => entry.id)).toEqual(
      server.snapshots.map((entry) => entry.id),
    );
  });

  test('counts the snapshots after a restore, from the listed history', async () => {
    await openDraft();
    await addDevices(3);
    await store.fetchHistory();

    await store.restoreSnapshot('s1');

    expect(store.newerHistory).toMatchObject({
      count: 3,
      newest: { id: server.snapshots.at(-1).id, createdBy: 'alice' },
    });
  });

  test('is null for a read-only diagram', async () => {
    await openDraft();
    await addDevices(1);
    store.undo();
    await store.saveNow();
    expect(store.newerHistory).not.toBeNull();

    store.readOnly = true;
    expect(store.newerHistory).toBeNull();
  });
});

describe('readNewerHistory', () => {
  test('reads the list when it is not the server’s, and names the newest', async () => {
    await openDraft();
    await addDevices(2);
    store.undo();
    await store.saveNow();
    // A list the editor cannot trust: as if it was read long ago.
    store.serverHistory = [{ id: 's1' }];

    expect(store.newerHistory).toEqual({ count: 1, newest: null, saved: '' });

    const newest = await store.readNewerHistory();

    expect(api.listSnapshots).toHaveBeenCalledExactlyOnceWith('d1');
    expect(newest.id).toBe(server.snapshots.at(-1).id);
    expect(store.newerHistory.newest.id).toBe(newest.id);
  });

  test('drops a list that answers after the draft changed', async () => {
    await openDraft();
    await addDevices(2);
    store.undo();
    await store.saveNow();
    store.serverHistory = [{ id: 's1' }];

    let answer;
    api.listSnapshots.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          answer = resolve;
        }),
    );

    const reading = store.readNewerHistory();

    store.undo();
    await store.saveNow();
    answer([{ id: 'old' }]);
    await reading;

    expect(store.serverHistory).not.toEqual([{ id: 'old' }]);
  });

  test('keeps a failure quiet', async () => {
    await openDraft();
    await addDevices(1);
    store.undo();
    await store.saveNow();
    store.serverHistory = [];
    api.listSnapshots.mockRejectedValueOnce(new Error('offline'));

    await expect(store.readNewerHistory()).resolves.toBeNull();
    expect(store.error).toBe('');
    expect(store.newerHistory.count).toBe(1);
  });
});

describe('returnToLatest', () => {
  test('goes forward in the undo history when it holds the newest snapshot', async () => {
    await openDraft();
    await addDevices(3);
    const newest = store.doc;

    store.undo();
    store.undo();
    await store.saveNow();
    api.moveCursor.mockClear();

    await expect(store.returnToLatest()).resolves.toBe(true);
    await store.saveNow();

    expect(store.doc).toStrictEqual(newest);
    expect(store.canUndo).toBe(true);
    expect(store.canRedo).toBe(false);
    expect(api.moveCursor).toHaveBeenCalledExactlyOnceWith(
      'd1',
      { snapshotId: server.snapshots.at(-1).id },
      expect.any(String),
    );
    expect(store.announcement).toBe('Returned to the latest snapshot.');
    expect(store.newerHistory).toBeNull();
  });

  test('restores the newest snapshot when the undo history does not hold it', async () => {
    await openDraft();
    await addDevices(2);
    await store.fetchHistory();
    await store.restoreSnapshot('s1');
    expect(store.newerHistory.count).toBe(2);

    await expect(store.returnToLatest()).resolves.toBe(true);

    expect(api.getSnapshot).toHaveBeenLastCalledWith(
      'd1',
      server.snapshots.at(-1).id,
    );
    expect(server.cursor).toBe(2);
    expect(store.newerHistory).toBeNull();
    expect(store.announcement).toBe('Returned to the latest snapshot');
  });

  test('reads the list first when it does not know the newest snapshot', async () => {
    await openDraft();
    await addDevices(1);
    store.undo();
    await store.saveNow();
    store.serverHistory = [];

    await expect(store.returnToLatest()).resolves.toBe(true);
    await store.saveNow();

    expect(api.listSnapshots).toHaveBeenCalledOnce();
    expect(store.newerHistory).toBeNull();
  });

  test('says why when the newest snapshot cannot be read', async () => {
    await openDraft();
    await addDevices(1);
    store.undo();
    await store.saveNow();
    store.serverHistory = [];
    api.listSnapshots.mockRejectedValueOnce(new Error('offline'));

    await expect(store.returnToLatest()).resolves.toBe(false);
    expect(store.error).toMatch(/^Could not read the draft history/);
  });
});

describe('saveNewerAsDraft', () => {
  test('makes a new draft of the newest snapshot, and stays on this one', async () => {
    await openDraft();
    await addDevices(2);
    store.undo();
    store.undo();
    await store.saveNow();
    const newest = server.snapshots.at(-1);

    const created = await store.saveNewerAsDraft();

    expect(created).toMatchObject({ id: 'd9' });
    expect(api.getSnapshot).toHaveBeenCalledWith('d1', newest.id);

    const [request] = api.createDraft.mock.calls[0];

    expect(request.title).toBe('Pump station (copy)');
    expect(request.sourceToken).toBe('');
    expect(request.document.metadata.name).toBe('Pump station (copy)');
    const copied = JSON.stringify(request.document.nodes);

    expect(copied).toContain('"added-0"');
    expect(copied).toContain('"added-1"');
    expect(store.draftId).toBe('d1');
    expect(store.newerHistory).toMatchObject({
      count: 2,
      saved: 'Pump station (copy)',
    });
    expect(store.announcement).toBe(
      'Saved the latest snapshot as draft Pump station (copy).',
    );
  });

  test('a newer snapshot saved later is not named saved', async () => {
    await openDraft();
    await addDevices(1);
    store.undo();
    await store.saveNow();
    await store.saveNewerAsDraft();

    // Someone saves on, and this editor reads the list again.
    store.newerSaved = { ...store.newerSaved, snapshotId: 'other' };

    expect(store.newerHistory.saved).toBe('');
  });

  test('says why the draft was not made', async () => {
    await openDraft();
    await addDevices(1);
    store.undo();
    await store.saveNow();
    api.createDraft.mockRejectedValueOnce(
      Object.assign(new Error('refused'), {
        response: { status: 500, data: { message: 'disk full' } },
      }),
    );

    await expect(store.saveNewerAsDraft()).resolves.toBeNull();
    expect(store.error).toMatch(
      /^Could not save the newer history as a new draft\./,
    );
    expect(store.newerSaved).toBeNull();
  });
});
