import { beforeEach, describe, expect, test, vi } from 'vitest';
import { isProxy, reactive } from 'vue';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import {
  appliedOperations,
  createAutosave,
  describeState,
  initialState,
  replayHistory,
  RETRY_DELAYS,
  saveAnnouncement,
  staleSaveMessage,
  stampOf,
} from '@/builder/autosave.js';
import {
  createMemoryStore,
  draftKey,
  mergeUnloadCopy,
  readUnloadCopy,
  splitRecord,
  staleUnloadCopy,
  unloadCopyKey,
  writeUnloadCopy,
} from '@/builder/idb.js';

import { memoryStorage, sampleDocument } from './fixtures.js';

// The store the queue falls back to without IndexedDB. Like the database, it
// keeps each entry's snapshot apart from the draft record; `written` lists
// every snapshot write, by commit id.
function memoryStore() {
  const store = createMemoryStore();
  const put = store.put;

  store.written = [];
  store.put = (record, changes = {}) => {
    store.written.push(...(changes.write || []).map((entry) => entry.id));

    return put(record, changes);
  };

  return store;
}

// A save answers with the draft alone, as the server does: its snapshot id,
// but no history.
function fakeApi(overrides = {}) {
  let revision = 1;

  return {
    appendSnapshot: vi.fn(async () => {
      revision += 1;

      return {
        draft: { id: 'd1', owner: 'alice', snapshotId: `s${revision}` },
        history: null,
        cursor: revision - 1,
        etag: `"${revision}"`,
      };
    }),
    moveCursor: vi.fn(async () => ({
      draft: { id: 'd1', owner: 'alice' },
      etag: `"${revision}"`,
    })),
    createDraft: vi.fn(async () => ({
      draft: { id: 'd2', owner: 'alice' },
      etag: '"1"',
    })),
    ...overrides,
  };
}

function conflict() {
  return Object.assign(new Error('conflict'), {
    response: { status: 412, data: {} },
  });
}

let doc;

beforeEach(() => {
  doc = sampleDocument().doc;
});

async function attached(options = {}) {
  const store = options.store || memoryStore();
  const api = options.api || fakeApi();
  const queue = createAutosave({
    api,
    store,
    actor: 'alice',
    setTimeout: options.setTimeout || (() => 0),
    clearTimeout: () => {},
    isOnline: options.isOnline || (() => true),
    ...options.extra,
  });

  await queue.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });

  return { queue, api, store };
}

describe('one commit, one snapshot', () => {
  test('each distinct commit posts its own snapshot in order', async () => {
    const { queue, api } = await attached();

    await queue.commit({ id: 'c1', label: 'Added a device', snapshot: doc });
    await queue.commit({ id: 'c2', label: 'Added a switch', snapshot: doc });
    await queue.commit({ id: 'c3', label: 'Connected nodes', snapshot: doc });

    expect(api.appendSnapshot).toHaveBeenCalledTimes(3);
    expect(
      api.appendSnapshot.mock.calls.map((call) => call[2].summary),
    ).toEqual(['Added a device', 'Added a switch', 'Connected nodes']);
  });

  test('commits are never coalesced, even back to back', async () => {
    const { queue, api } = await attached();

    await Promise.all([
      queue.commit({ id: 'c1', label: 'one', snapshot: doc }),
      queue.commit({ id: 'c2', label: 'two', snapshot: doc }),
      queue.commit({ id: 'c3', label: 'three', snapshot: doc }),
    ]);

    expect(api.appendSnapshot).toHaveBeenCalledTimes(3);
  });

  test('each snapshot carries the ETag returned by the previous one', async () => {
    const { queue, api } = await attached();

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    await queue.commit({ id: 'c2', label: 'two', snapshot: doc });

    expect(api.appendSnapshot.mock.calls[0][3]).toBe('"1"');
    expect(api.appendSnapshot.mock.calls[1][3]).toBe('"2"');
  });

  test('a cursor move is queued behind pending snapshots', async () => {
    const order = [];
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        order.push('snapshot');

        return {
          draft: { snapshotId: 'server-c1' },
          history: null,
          cursor: 0,
          etag: '"2"',
        };
      }),
      moveCursor: vi.fn(async () => {
        order.push('cursor');

        return { draft: {}, etag: '"3"' };
      }),
    });
    const { queue } = await attached({ api });

    const commit = queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    const cursor = queue.moveCursor({ commitId: 'c1' });

    await Promise.all([commit, cursor]);

    expect(order).toEqual(['snapshot', 'cursor']);
    expect(api.moveCursor.mock.calls[0][2]).toEqual({
      snapshotId: 'server-c1',
    });
  });

  test('the local ordered log is persisted before the server call', async () => {
    const store = memoryStore();
    const key = draftKey('alice', 'alice', 'd1');
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        const record = await store.get(key);

        expect(record.queue).toHaveLength(1);
        expect(record.entries).toEqual([
          expect.objectContaining({ id: 'c1', snapshot: doc }),
        ]);

        return { draft: {}, etag: '"2"' };
      }),
    });
    const { queue } = await attached({ store, api });

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    // Once the queue drains, nothing is left on this device.
    expect(await store.all()).toEqual([]);
    expect(store.snapshots.size).toBe(0);
  });

  // An edit writes its own snapshot once, and the draft record keeps no
  // snapshot, however long the history is.
  test('each snapshot is stored once, apart from the draft record', async () => {
    const store = memoryStore();
    const { queue } = await attached({ store, isOnline: () => false });

    for (const id of ['c1', 'c2', 'c3']) {
      await queue.commit({ id, label: id, snapshot: { ...doc, name: id } });
    }
    await queue.moveCursor({ commitId: 'c2' });

    expect(store.written).toEqual(['c1', 'c2', 'c3']);

    const [record] = await store.all();
    expect(record.queue).toHaveLength(4);
    expect(record.entries.map((entry) => entry.id)).toEqual(['c1', 'c2', 'c3']);
    expect(record.entries.some((entry) => 'snapshot' in entry)).toBe(false);
    expect(
      (await store.get(record.key)).entries.map((entry) => entry.snapshot.name),
    ).toEqual(['c1', 'c2', 'c3']);
  });

  test('history is capped at the 50 snapshots the server keeps', async () => {
    const { queue } = await attached();

    for (let i = 0; i < 70; i += 1) {
      await queue.commit({ id: `c${i}`, label: `step ${i}`, snapshot: doc });
    }

    expect(queue.record.entries).toHaveLength(50);
    expect(queue.record.entries[0].id).toBe('c20');
  });
});

// The server writes who made the document and who saved it last, and when,
// into the copy it stores, and answers the save with them (see withStamp in
// model.js). The queue's own copy takes them, and no others.
describe('the stamp a save answers with', () => {
  const stamps = [
    {
      author: 'alice',
      createdAt: '2026-10-01T15:04:05Z',
      updatedBy: 'alice',
      updatedAt: '2026-10-01T16:00:00Z',
    },
    {
      author: 'alice',
      createdAt: '2026-10-01T15:04:05Z',
      updatedBy: 'alice',
      updatedAt: '2026-10-01T16:00:07Z',
    },
  ];

  function stampingApi() {
    let saves = 0;

    return fakeApi({
      appendSnapshot: vi.fn(async () => {
        saves += 1;

        return {
          draft: {
            id: 'd1',
            owner: 'alice',
            snapshotId: `s${saves}`,
            stamp: stamps[saves - 1],
          },
          history: null,
          cursor: saves,
          etag: `"${saves + 1}"`,
        };
      }),
    });
  }

  test('is read from a create or a save, and from nothing else', () => {
    expect(stampOf({ draft: { stamp: stamps[0] } })).toBe(stamps[0]);
    // A document that names no one answers with an empty stamp.
    expect(stampOf({ draft: { stamp: {} } })).toEqual({});
    expect(stampOf({ draft: { id: 'd1' } })).toBeNull();
    expect(stampOf({ draft: { stamp: 'alice' } })).toBeNull();
    expect(stampOf({ draft: { stamp: [stamps[0]] } })).toBeNull();
    expect(stampOf(null)).toBeNull();
  });

  test('goes into the entry the save stored, and is what the next page shows', async () => {
    const api = stampingApi();
    const seen = [];
    const { queue } = await attached({
      api,
      extra: { onDraft: (envelope, op) => seen.push([op.commitId, envelope]) },
    });
    const first = { ...doc, name: 'first' };

    await queue.commit({ id: 'c1', label: 'one', snapshot: first });

    const [entry] = queue.record.entries;

    // What was sent is what the editor held: the stamp is the server's.
    expect(api.appendSnapshot.mock.calls[0][2].document).toBe(first);
    expect(entry.snapshot).toMatchObject({ name: 'first', ...stamps[0] });
    expect(Object.keys(entry.snapshot).slice(4, 9)).toEqual([
      'description',
      'author',
      'createdAt',
      'updatedBy',
      'updatedAt',
    ]);
    // The document handed in is not changed under its owner.
    expect(first).not.toHaveProperty('updatedAt');
    expect(seen.map(([id, envelope]) => [id, stampOf(envelope)])).toEqual([
      ['c1', stamps[0]],
    ]);

    // The next edit is built from the stamped copy, and sends that stamp;
    // the server replaces it, and the entry takes the new one.
    await queue.commit({
      id: 'c2',
      label: 'two',
      snapshot: { ...entry.snapshot, name: 'second' },
    });

    expect(api.appendSnapshot.mock.calls[1][2].document.updatedAt).toBe(
      stamps[0].updatedAt,
    );
    expect(queue.record.entries.map((item) => item.snapshot.updatedAt)).toEqual(
      [stamps[0].updatedAt, stamps[1].updatedAt],
    );
  });

  test('a save without one leaves the entry as it was', async () => {
    const { queue } = await attached();

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(queue.record.entries[0].snapshot).toBe(doc);
  });

  // An entry still needed once its save is confirmed is one a queued undo
  // or redo moves to. The store wrote it before the save: it is written
  // again, so a reload shows who saved it.
  test('an entry the store keeps is stored again with its stamp', async () => {
    const store = memoryStore();
    const api = stampingApi();
    let online = false;
    const { queue } = await attached({ api, store, isOnline: () => online });

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    await queue.commit({ id: 'c2', label: 'two', snapshot: doc });
    await queue.moveCursor({ commitId: 'c1' });
    expect(store.written).toEqual(['c1', 'c2']);

    // The cursor move fails, so the queue keeps it, and c1 with it.
    api.moveCursor.mockRejectedValueOnce(new Error('offline'));
    online = true;
    await queue.flush();

    expect(queue.record.queue.map((op) => op.kind)).toEqual(['cursor']);
    // Not c2: nothing queued needs it once it is saved.
    expect(store.written).toEqual(['c1', 'c2', 'c1']);

    const kept = await queue.recover('alice', 'd1');

    expect(kept.entries.map((entry) => entry.id)).toEqual(['c1']);
    expect(kept.entries[0].snapshot).toMatchObject(stamps[0]);
  });

  test('a fork gives each entry the stamp the new draft stored it with', async () => {
    const forked = [
      { ...stamps[0], updatedBy: 'bob', updatedAt: '2026-10-02T10:00:00Z' },
      { ...stamps[0], updatedBy: 'bob', updatedAt: '2026-10-02T10:00:01Z' },
      { ...stamps[0], updatedBy: 'bob', updatedAt: '2026-10-02T10:00:02Z' },
    ];
    let saves = 0;
    const api = fakeApi({
      createDraft: vi.fn(async () => ({
        draft: { id: 'd2', owner: 'bob', snapshotId: 'f0', stamp: forked[0] },
        etag: '"f0"',
      })),
      appendSnapshot: vi.fn(async (owner, id) => {
        saves += 1;

        return {
          draft: {
            id,
            owner,
            snapshotId: `f${saves}`,
            stamp: forked[saves],
          },
          etag: `"f${saves}"`,
        };
      }),
    });
    const queue = createAutosave({ api, store: memoryStore(), actor: 'bob' });
    // The history as the editor holds it: each entry with the stamp of the
    // save that stored it in the draft it leaves.
    const held = { ...doc, ...stamps[0] };
    const history = [
      { id: 'base', label: 'initial', snapshot: held },
      { id: 'kept', label: 'Added device', snapshot: { ...held, name: 'b' } },
      { id: 'redo', label: 'Added switch', snapshot: { ...held, name: 'c' } },
    ];

    await queue.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });

    const created = await queue.forkLocalHistory({
      title: 'Copy',
      entries: history,
      index: 1,
    });

    // The first document carries the author the editor got from the server,
    // which the new draft keeps. No file name is sent: a fork has none.
    expect(api.createDraft.mock.calls[0][0].document).toMatchObject({
      author: 'alice',
      createdAt: '2026-10-01T15:04:05Z',
    });
    expect(api.createDraft.mock.calls[0][0]).not.toHaveProperty('sourceFile');
    expect([...created.stamps]).toEqual([
      ['base', forked[0]],
      ['kept', forked[1]],
      ['redo', forked[2]],
    ]);
    expect(
      queue.record.entries.map((entry) => [
        entry.snapshot.updatedBy,
        entry.snapshot.updatedAt,
      ]),
    ).toEqual(forked.map((stamp) => [stamp.updatedBy, stamp.updatedAt]));
    // The entries handed in are the caller's to stamp.
    expect(history[1].snapshot.updatedBy).toBe('alice');
  });
});

describe('recovery', () => {
  test('a full local log with pending work is recovered', async () => {
    const store = memoryStore();
    const first = await attached({ store, isOnline: () => false });

    await first.queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    const second = await attached({ store, isOnline: () => false });
    const recovered = await second.queue.recover('alice', 'd1');

    expect(recovered.entries).toHaveLength(1);
    expect(recovered.entries[0]).toMatchObject({ label: 'one', snapshot: doc });
    // Attaching again wrote nothing the store already held.
    expect(store.written).toEqual(['c1']);
  });

  // A session that never saw a save's answer (the tab closed, or another
  // draft was opened) finds the snapshot by its operation id.
  test('operations the server already holds are recognised by their id', () => {
    const queue = [
      { kind: 'snapshot', opId: 'c1', commitId: 'c1' },
      { kind: 'cursor', opId: 'cursor-c0-1', commitId: 'c0' },
      { kind: 'snapshot', opId: 'c2', commitId: 'c2' },
      { kind: 'snapshot', opId: 'c3', commitId: 'c3' },
    ];
    const history = [
      { id: 's1' },
      { id: 's2', opId: 'c1' },
      { id: 's3', opId: 'c2' },
    ];

    expect(appliedOperations(queue, history)).toEqual({
      count: 3,
      snapshotIds: new Map([
        ['c1', 's2'],
        ['c2', 's3'],
      ]),
    });
    expect(appliedOperations(queue, null).count).toBe(0);
  });

  // An edit undone before a later edit is on no branch the server keeps, so
  // the recovered history leaves it out.
  test('the recovered history follows the queue as the server does', () => {
    const base = { id: 'b', serverSnapshotId: 's1' };
    const entries = ['x', 'y', 'z'].map((id) => ({ id }));

    expect(
      replayHistory(
        [base],
        0,
        [
          { kind: 'snapshot', commitId: 'x' },
          { kind: 'cursor', snapshotId: 's1' },
          { kind: 'snapshot', commitId: 'y' },
          { kind: 'snapshot', commitId: 'z' },
          { kind: 'cursor', commitId: 'y' },
        ],
        entries,
      ),
    ).toEqual({ entries: [base, entries[1], entries[2]], index: 1 });

    // A refused snapshot a later edit replaces is skipped, as the queue
    // skips it.
    expect(
      replayHistory(
        [base],
        0,
        [
          { kind: 'snapshot', commitId: 'x', rejected: 'invalid' },
          { kind: 'snapshot', commitId: 'y' },
        ],
        entries,
      ),
    ).toEqual({ entries: [base, entries[1]], index: 1 });

    // An undo made offline, past the snapshot the draft opens at, goes back
    // to an entry this device holds (see the next test): it is current
    // again, with the opened snapshot to redo, as on the server.
    const older = { id: 'a', serverSnapshotId: 's0' };
    const opened = { id: 'b', serverSnapshotId: 's1' };

    expect(
      replayHistory(
        [base],
        0,
        [{ kind: 'cursor', snapshotId: 's0', commitId: 'a' }],
        [older, opened],
      ),
    ).toEqual({ entries: [older, base], index: 0 });
  });

  // The entry an undo goes back to may be the draft as it was opened, which
  // was never saved from this device: it is stored with the queue, first,
  // so a reload can show it.
  test('an undo to the draft as opened is stored with the queue', async () => {
    const store = memoryStore();
    const { queue } = await attached({ store, isOnline: () => false });

    await queue.commit({ id: 'c1', label: 'Added', snapshot: doc });
    await queue.moveCursor({
      snapshotId: 's1',
      commitId: 'opened',
      entry: {
        id: 'opened',
        label: 'Draft loaded',
        snapshot: doc,
        serverSnapshotId: 's1',
      },
    });

    const recovered = await queue.recover('alice', 'd1');

    expect(recovered.entries.map((entry) => entry.id)).toEqual([
      'opened',
      'c1',
    ]);
    expect(store.written).toEqual(['c1', 'opened']);
  });

  describe('lifecycle', () => {
    test('dispose prevents an in-flight completion from emitting or retrying', async () => {
      let resolveRequest;
      const setTimeout = vi.fn();
      const onState = vi.fn();
      const onDraft = vi.fn();
      const api = fakeApi({
        appendSnapshot: vi.fn(
          () =>
            new Promise((resolve) => {
              resolveRequest = resolve;
            }),
        ),
      });
      const { queue } = await attached({
        api,
        setTimeout,
        extra: { onState, onDraft },
      });
      const pending = queue.commit({
        id: 'c1',
        label: 'one',
        snapshot: doc,
      });

      await vi.waitFor(() => expect(api.appendSnapshot).toHaveBeenCalled());
      queue.dispose();
      const stateCalls = onState.mock.calls.length;
      resolveRequest({
        draft: { snapshotId: 's2' },
        history: null,
        cursor: 0,
        etag: '"2"',
      });
      await pending;

      expect(onState).toHaveBeenCalledTimes(stateCalls);
      expect(onDraft).not.toHaveBeenCalled();
      expect(setTimeout).not.toHaveBeenCalled();
    });

    // The server stored the snapshot, so the device must not keep it queued
    // against the old ETag, or reopening the draft reports a conflict.
    test('a save that settles after dispose is still recorded', async () => {
      let resolveRequest;
      const store = memoryStore();
      const api = fakeApi({
        appendSnapshot: vi.fn(
          () =>
            new Promise((resolve) => {
              resolveRequest = resolve;
            }),
        ),
      });
      const { queue } = await attached({ api, store });
      const pending = queue.commit({ id: 'c1', label: 'one', snapshot: doc });

      await vi.waitFor(() => expect(api.appendSnapshot).toHaveBeenCalled());
      expect(api.appendSnapshot.mock.calls[0][2].opId).toBe('c1');
      expect(await store.all()).toHaveLength(1);

      queue.dispose();
      resolveRequest({ draft: { snapshotId: 's2' }, etag: '"2"' });
      await pending;

      expect(await store.all()).toEqual([]);
      expect(api.appendSnapshot).toHaveBeenCalledOnce();
    });
  });

  test('records are scoped to owner and actor', async () => {
    const store = memoryStore();
    const offline = { isOnline: () => false };
    const mine = createAutosave({
      api: fakeApi(),
      store,
      actor: 'alice',
      ...offline,
    });
    const theirs = createAutosave({
      api: fakeApi(),
      store,
      actor: 'bob',
      ...offline,
    });

    await mine.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });
    await theirs.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });
    await mine.commit({ id: 'c1', label: 'one', snapshot: doc });
    await theirs.commit({ id: 'c2', label: 'two', snapshot: doc });

    expect(await mine.recoverAll()).toHaveLength(1);
    expect(await theirs.recoverAll()).toHaveLength(1);
    expect((await store.all()).map((record) => record.key)).toEqual([
      draftKey('alice', 'alice', 'd1'),
      draftKey('bob', 'alice', 'd1'),
    ]);
  });
});

describe('failure states', () => {
  test('a conflict stops the queue and keeps local work', async () => {
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        throw conflict();
      }),
    });
    const { queue } = await attached({ api });

    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.status).toBe('conflict');
    expect(queue.record.queue).toHaveLength(1);
    expect(queue.record.entries).toHaveLength(1);

    // A blocked queue must not keep hammering the server.
    await queue.commit({ id: 'c2', label: 'two', snapshot: doc });

    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
  });

  test('a conflict can be resolved by forking local history into a new draft', async () => {
    const api = fakeApi({
      appendSnapshot: vi
        .fn()
        .mockRejectedValueOnce(conflict())
        .mockResolvedValue({ draft: { id: 'd2' }, etag: '"3"' }),
    });
    const { queue } = await attached({ api });

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    await queue.commit({ id: 'c2', label: 'two', snapshot: doc });

    const created = await queue.forkLocalHistory({ title: 'Recovered' });

    // The new draft forks the conflicting one, so the server gives it that
    // draft's source, and it may update what that draft published.
    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Recovered',
        document: doc,
        forkOf: 'alice/d1',
      }),
    );
    expect(created.draft.id).toBe('d2');
    expect(queue.record.draftId).toBe('d2');
    expect(queue.record.queue).toHaveLength(0);
    expect(queue.record.entries).toHaveLength(2);
  });

  // The fork saves the history on screen, in order, with its cursor on the
  // entry shown, and every entry learns its snapshot in the new draft, so
  // undo there never names the old draft's snapshots. The new draft
  // is the actor's: the server refuses one for anybody else.
  test('a fork of a shared draft is the actor’s, with the history on screen', async () => {
    const store = memoryStore();
    let revision = 0;
    const api = fakeApi({
      createDraft: vi.fn(async () => ({
        draft: { id: 'd2', owner: 'alice', snapshotId: 'f0' },
        etag: '"f0"',
      })),
      appendSnapshot: vi.fn(async (owner, id) => {
        if (id === 'd1') {
          throw conflict();
        }

        revision += 1;

        return {
          draft: { id, owner, snapshotId: `f${revision}` },
          etag: `"f${revision}"`,
        };
      }),
      moveCursor: vi.fn(async (owner, id, target) => ({
        draft: { id, owner, snapshotId: target.snapshotId, digest: 'sha' },
        etag: '"moved"',
      })),
    });
    const queue = createAutosave({ api, store, actor: 'alice' });

    await queue.attach({ owner: 'bob', draftId: 'd1', etag: '"1"' });
    await queue.commit({ id: 'undone', label: 'undone', snapshot: doc });

    const history = [
      { id: 'base', label: 'initial', snapshot: doc, serverSnapshotId: 'b0' },
      { id: 'kept', label: 'Added device', snapshot: doc },
      { id: 'redo', label: 'Added switch', snapshot: doc },
    ];
    const created = await queue.forkLocalHistory({
      title: 'Copy',
      entries: history,
      index: 1,
    });

    expect(api.createDraft.mock.calls[0][0]).not.toHaveProperty('owner');
    expect(api.createDraft.mock.calls[0][0].forkOf).toBe('bob/d1');
    expect(api.createDraft.mock.calls[0][0].summary).toBeUndefined();
    expect(
      api.appendSnapshot.mock.calls
        .filter((call) => call[1] === 'd2')
        .map((call) => [call[0], call[2].summary, call[3]]),
    ).toEqual([
      ['alice', 'Added device', '"f0"'],
      ['alice', 'Added switch', '"f1"'],
    ]);
    expect(api.moveCursor).toHaveBeenCalledWith(
      'alice',
      'd2',
      { snapshotId: 'f1' },
      '"f2"',
    );
    expect(created).toMatchObject({
      etag: '"moved"',
      draft: { id: 'd2', owner: 'alice', digest: 'sha' },
    });
    expect([...created.snapshotIds]).toEqual([
      ['base', 'f0'],
      ['kept', 'f1'],
      ['redo', 'f2'],
    ]);
    expect(queue.record).toMatchObject({ owner: 'alice', cursor: 1 });
    // The conflicting draft's queue lives on in the fork only.
    expect(await store.all()).toEqual([]);
  });

  // A draft the server no longer has, or no longer lets this user read,
  // has no source to give: the history is saved as a draft of its own.
  test('a fork of a draft that is gone still saves the history', async () => {
    const missing = Object.assign(new Error('not found'), {
      response: { status: 404, data: {} },
    });
    const api = fakeApi({
      createDraft: vi
        .fn()
        .mockRejectedValueOnce(missing)
        .mockResolvedValue({
          draft: { id: 'd2', owner: 'alice' },
          etag: '"1"',
        }),
    });
    const { queue } = await attached({ api });

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    const created = await queue.forkLocalHistory({ title: 'Recovered' });

    expect(api.createDraft).toHaveBeenCalledTimes(2);
    expect(api.createDraft.mock.calls[0][0].forkOf).toBe('alice/d1');
    expect(api.createDraft.mock.calls[1][0]).not.toHaveProperty('forkOf');
    expect(api.createDraft.mock.calls[1][0].title).toBe('Recovered');
    expect(created.draft.id).toBe('d2');

    // Any other refusal is the answer.
    api.createDraft.mockRejectedValueOnce(conflict());
    await expect(queue.forkLocalHistory({ title: 'Again' })).rejects.toThrow(
      'conflict',
    );
    expect(api.createDraft).toHaveBeenCalledTimes(3);
  });

  test('discarding local work is an explicit choice', async () => {
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        throw conflict();
      }),
    });
    const store = memoryStore();
    const { queue } = await attached({ api, store });

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    expect(await store.all()).toHaveLength(1);
    await queue.discardLocal();

    expect(queue.record.entries).toHaveLength(0);
    expect(queue.record.queue).toHaveLength(0);
    expect(await store.all()).toEqual([]);
    expect(store.snapshots.size).toBe(0);
  });

  // A publish holds the queue, so no save lands between it and the ETag it
  // answers with.
  test('a held queue keeps edits and sends them once released', async () => {
    const api = fakeApi();
    const { queue } = await attached({ api });
    const release = queue.hold();

    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state).toMatchObject({ status: 'idle', pending: 1 });
    expect(api.appendSnapshot).not.toHaveBeenCalled();
    expect(queue.sent).toBe(0);

    await queue.setETag('"published"');
    expect(await release()).toMatchObject({ status: 'saved', pending: 0 });
    expect(api.appendSnapshot.mock.calls[0][3]).toBe('"published"');
    expect(queue.sent).toBe(1);
    // Releasing twice does nothing more.
    release();
    expect(api.appendSnapshot).toHaveBeenCalledOnce();
  });

  // An expired session is not a refusal of this user: Retry saving stays,
  // and the queue is kept.
  test('an ended session keeps the queue and offers Retry saving', async () => {
    const timers = [];
    const api = fakeApi();
    api.appendSnapshot = vi.fn(api.appendSnapshot).mockRejectedValueOnce(
      Object.assign(new Error('unauthorized'), {
        response: { status: 401, data: 'invalid token' },
      }),
    );
    const { queue } = await attached({
      api,
      setTimeout: (fn) => timers.push(fn),
    });

    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state).toMatchObject({
      status: 'error',
      retryable: true,
      signInNeeded: true,
      pending: 1,
    });
    expect(describeState(state)).toBe(
      'Could not save: your session has ended. Use Download to keep a copy of the diagram, then sign in again.',
    );
    // The same credentials are refused every time: no timer retry.
    expect(timers).toHaveLength(0);

    expect(await queue.retry()).toMatchObject({
      status: 'saved',
      signInNeeded: false,
      pending: 0,
    });
  });

  // Where the Builder can ask for the password again (see signin.js), the
  // queue waits for it, keeping every edit, and signing in resumes it.
  test('an ended session waits for the user to sign in again, and then sends everything queued', async () => {
    let signedIn = false;
    const timers = [];
    const api = fakeApi();
    const send = api.appendSnapshot;
    api.appendSnapshot = vi.fn((...args) =>
      signedIn
        ? send(...args)
        : Promise.reject(
            Object.assign(new Error('unauthorized'), {
              response: { status: 401, data: 'user token error' },
            }),
          ),
    );
    const { queue, store } = await attached({
      api,
      setTimeout: (fn) => timers.push(fn),
      extra: { signInHere: () => true },
    });

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    const state = await queue.commit({ id: 'c2', label: 'two', snapshot: doc });

    expect(state).toMatchObject({
      status: 'error',
      signInNeeded: true,
      pending: 2,
    });
    expect(describeState(state)).toBe(
      'Not saved: your session has ended. Sign in again to save your changes.',
    );
    expect(timers).toHaveLength(0);
    expect((await store.all())[0].queue).toHaveLength(2);

    signedIn = true;
    await expect(queue.retry()).resolves.toMatchObject({
      status: 'saved',
      pending: 0,
    });
    expect(
      api.appendSnapshot.mock.calls.slice(-2).map((call) => call[2].summary),
    ).toEqual(['one', 'two']);
  });

  test('offline work is retried and never lost', async () => {
    let online = false;
    const api = fakeApi();
    const timers = [];
    const { queue } = await attached({
      api,
      isOnline: () => online,
      setTimeout: (fn) => {
        timers.push(fn);

        return timers.length;
      },
    });

    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.status).toBe('offline');
    expect(api.appendSnapshot).not.toHaveBeenCalled();
    expect(queue.record.queue).toHaveLength(1);

    online = true;
    await queue.flush();

    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
  });

  test('a server error schedules a retry with a backoff', async () => {
    const delays = [];
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        throw Object.assign(new Error('boom'), {
          response: { status: 500, data: {} },
        });
      }),
    });
    const { queue } = await attached({
      api,
      setTimeout: (_, ms) => {
        delays.push(ms);

        return delays.length;
      },
    });

    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.status).toBe('error');
    expect(state.retryable).toBe(true);
    expect(delays[0]).toBe(RETRY_DELAYS[0]);
  });

  // The server's answer once etcd is out of space (builderWebError).
  test('a server out of space says so and keeps retrying', async () => {
    const reason =
      'etcd is out of space: phenix cannot save changes until an administrator frees space (compact and defragment etcd, then clear its NOSPACE alarm)';
    const delays = [];
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        throw Object.assign(new Error('insufficient storage'), {
          response: {
            status: 507,
            data: {
              message: reason,
              cause: `draft "d1": updating record builder.drafts/d1 in Etcd: ${reason}: etcdserver: mvcc: database space exceeded`,
            },
          },
        });
      }),
    });
    const { queue } = await attached({
      api,
      setTimeout: (_, ms) => {
        delays.push(ms);

        return delays.length;
      },
    });

    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.status).toBe('error');
    expect(state.retryable).toBe(true);
    expect(delays[0]).toBe(RETRY_DELAYS[0]);
    expect(describeState(state)).toBe(
      'Could not save your changes. Etcd is out of space: phenix cannot save changes until an administrator frees space (compact and defragment etcd, then clear its NOSPACE alarm). Saving retries automatically.',
    );
  });

  test('an explicit retry waits for the send already under way', async () => {
    let fail;
    const api = fakeApi();
    api.appendSnapshot = vi
      .fn(api.appendSnapshot)
      .mockImplementationOnce(async () => {
        throw Object.assign(new Error('boom'), {
          response: { status: 500, data: {} },
        });
      })
      .mockImplementationOnce(
        () =>
          new Promise((_, reject) => {
            fail = () =>
              reject(
                Object.assign(new Error('boom'), {
                  response: { status: 500, data: {} },
                }),
              );
          }),
      );
    const timers = [];
    const { queue } = await attached({
      api,
      setTimeout: (fn) => timers.push(fn),
    });
    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    // The automatic retry is sending when the user presses Retry saving.
    timers.shift()();
    await vi.waitFor(() => expect(fail).toBeTypeOf('function'));
    const retried = queue.retry();
    fail();

    const state = await retried;
    expect(api.appendSnapshot).toHaveBeenCalledTimes(3);
    expect(state.status).toBe('saved');
  });

  // Save now (and publish) flush while an edit is being sent: the state
  // they get is how saving ended, not "Saving 3 changes".
  test('a flush during a send waits for it, then sends what is left', async () => {
    const failing = () =>
      Object.assign(new Error('boom'), {
        response: { status: 500, data: {} },
      });
    let fail;
    const api = fakeApi();
    api.appendSnapshot = vi.fn(api.appendSnapshot).mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          fail = () => reject(failing());
        }),
    );
    const { queue } = await attached({ api });

    const edits = [queue.commit({ id: 'c1', label: 'one', snapshot: doc })];
    await vi.waitFor(() => expect(fail).toBeTypeOf('function'));
    edits.push(
      queue.commit({ id: 'c2', label: 'two', snapshot: doc }),
      queue.commit({ id: 'c3', label: 'three', snapshot: doc }),
    );
    let ended = false;
    const saved = queue.flush().then((state) => {
      ended = true;

      return state;
    });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(ended).toBe(false);
    // A retry the timer starts leaves the send under way alone.
    expect((await queue.flush({ background: true })).status).toBe('saving');

    // The send fails; the flushes waiting for it try once more, together.
    fail();

    expect(await saved).toMatchObject({ status: 'saved', pending: 0 });
    await Promise.all(edits);
    expect(
      api.appendSnapshot.mock.calls.map((call) => call[2].summary),
    ).toEqual(['one', 'one', 'two', 'three']);

    // When that attempt fails too, every flush waiting says so, after one
    // request rather than one each.
    api.appendSnapshot.mockClear();
    api.appendSnapshot.mockImplementation(async () => {
      throw failing();
    });
    const again = [
      queue.commit({ id: 'c4', label: 'four', snapshot: doc }),
      queue.commit({ id: 'c5', label: 'five', snapshot: doc }),
      queue.flush(),
    ];

    for (const state of await Promise.all(again)) {
      expect(state.status).toBe('error');
    }
    expect(api.appendSnapshot).toHaveBeenCalledTimes(2);
  });

  test('a forbidden draft reports a visible state', async () => {
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        throw Object.assign(new Error('nope'), {
          response: { status: 403, data: {} },
        });
      }),
    });
    const { queue } = await attached({ api });
    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.status).toBe('forbidden');
    expect(describeState(state)).toMatch(/cannot save|not allowed/i);
  });

  test('coming back online drains the queue', async () => {
    const listeners = {};
    const target = {
      addEventListener: (name, fn) => {
        listeners[name] = fn;
      },
      removeEventListener: () => {},
    };
    let online = false;
    const api = fakeApi();
    const { queue } = await attached({ api, isOnline: () => online });

    queue.listen(target);
    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    online = true;
    await listeners.online();

    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
  });
});

// A draft on a server that checks If-Match, as the real one does: a share
// change or a publish bumps its revision and leaves its content (its head)
// as it was; an edit moves its head too.
function sharedDraft({ owner = 'alice' } = {}) {
  const server = {
    revision: 1,
    snapshots: 1,
    lastModifiedBy: owner,
    access: 'edit',
    readOnly: false,
    gone: false,
  };
  const draft = () => ({
    id: 'd1',
    owner,
    snapshotId: `s${server.snapshots}`,
    cursor: server.snapshots - 1,
    snapshots: server.snapshots,
    lastModifiedBy: server.lastModifiedBy,
    access: server.access,
    readOnly: server.readOnly,
  });
  const envelope = () => ({
    draft: draft(),
    history: null,
    cursor: server.snapshots - 1,
    etag: `"${server.revision}"`,
  });
  const refused = (status) =>
    Object.assign(new Error(`status ${status}`), {
      response: { status, data: {} },
    });
  const api = fakeApi({
    // The ETag is the fourth argument, after owner, id and payload.
    appendSnapshot: vi.fn(async (...args) => {
      const etag = args[3];
      if (server.gone) {
        throw refused(404);
      }

      if (server.readOnly) {
        throw refused(403);
      }

      if (etag !== `"${server.revision}"`) {
        throw conflict();
      }

      server.revision += 1;
      server.snapshots += 1;

      return envelope();
    }),
    getDraft: vi.fn(async () => {
      if (server.gone) {
        throw refused(404);
      }

      return envelope();
    }),
  });

  return {
    server,
    api,
    head: () => ({
      snapshotId: `s${server.snapshots}`,
      cursor: server.snapshots - 1,
      snapshots: server.snapshots,
    }),
    // Bumps the revision alone, as a share change does.
    touch() {
      server.revision += 1;
    },
    edit(by) {
      server.revision += 1;
      server.snapshots += 1;
      server.lastModifiedBy = by;
    },
  };
}

async function attachedTo(shared, { actor = 'alice', store } = {}) {
  const onDraft = vi.fn();
  const queue = createAutosave({
    api: shared.api,
    store: store || memoryStore(),
    actor,
    onDraft,
    setTimeout: () => 0,
    clearTimeout: () => {},
    isOnline: () => true,
  });

  await queue.attach({
    owner: 'alice',
    draftId: 'd1',
    etag: '"1"',
    serverHead: shared.head(),
  });

  return { queue, onDraft };
}

describe('changes that leave the content as it was', () => {
  test('a conflict over the same content takes the new ETag and sends again', async () => {
    const shared = sharedDraft();
    const { queue, onDraft } = await attachedTo(shared);

    shared.touch();
    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.status).toBe('saved');
    expect(shared.api.appendSnapshot).toHaveBeenCalledTimes(2);
    expect(shared.api.appendSnapshot.mock.calls[1][3]).toBe('"2"');
    expect(queue.record.etag).toBe('"3"');
    // The head moves with every save the server confirms.
    expect(queue.record.serverHead).toEqual(shared.head());
    // The draft read again reaches the store, which keeps what it says.
    expect(onDraft).toHaveBeenCalledWith(
      expect.objectContaining({ etag: '"2"' }),
      null,
    );

    // Another change that leaves the content: sent again once more.
    shared.touch();
    await queue.commit({ id: 'c2', label: 'two', snapshot: doc });
    expect(queue.state.status).toBe('saved');
  });

  test('a conflict over another save stands, and names who saved', async () => {
    const shared = sharedDraft();
    const { queue } = await attachedTo(shared);

    shared.edit('carol');
    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state).toMatchObject({
      status: 'conflict',
      lastModifiedBy: 'carol',
    });
    expect(shared.api.appendSnapshot).toHaveBeenCalledTimes(1);
    expect(queue.record.queue).toHaveLength(1);
    // Nothing else is sent until the user chooses.
    await queue.commit({ id: 'c2', label: 'two', snapshot: doc });
    expect(shared.api.appendSnapshot).toHaveBeenCalledTimes(1);
  });

  test('sending again stops after two tries in a row', async () => {
    const shared = sharedDraft();
    const { queue } = await attachedTo(shared);

    shared.api.appendSnapshot.mockImplementation(async () => {
      throw conflict();
    });
    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.status).toBe('conflict');
    expect(shared.api.appendSnapshot).toHaveBeenCalledTimes(3);
    expect(shared.api.getDraft).toHaveBeenCalledTimes(2);
  });

  test('a queue with no confirmed head never sends again', async () => {
    const shared = sharedDraft();
    const queue = createAutosave({
      api: shared.api,
      store: memoryStore(),
      actor: 'alice',
      isOnline: () => true,
    });

    await queue.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });
    shared.touch();

    expect(
      (await queue.commit({ id: 'c1', label: 'one', snapshot: doc })).status,
    ).toBe('conflict');
    expect(shared.api.appendSnapshot).toHaveBeenCalledTimes(1);
  });

  test('the head a pending queue was based on is stored with it', async () => {
    const shared = sharedDraft();
    const store = memoryStore();
    const { queue } = await attachedTo(shared, { store });
    const first = shared.head();
    const release = queue.hold();

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    const stored = await store.get(draftKey('alice', 'alice', 'd1'));
    expect(stored.serverHead).toEqual(first);

    // Reopened on this device, the pending queue keeps the head it was
    // based on, not the one the draft opened with.
    const reopened = createAutosave({ api: shared.api, store, actor: 'alice' });
    await reopened.attach({
      owner: 'alice',
      draftId: 'd1',
      etag: '"9"',
      serverHead: { snapshotId: 's9', cursor: 8, snapshots: 9 },
    });
    expect(reopened.record.serverHead).toEqual(first);

    await release();
    expect(queue.record.serverHead).toEqual(shared.head());
    expect(queue.record.serverHead).not.toEqual(first);
  });

  test('the owner’s own share change is adopted only over the same content', async () => {
    const shared = sharedDraft();
    const { queue } = await attachedTo(shared);

    shared.touch();
    expect(
      await queue.adoptIfSameHead({ ...shared.head(), id: 'd1' }, '"2"'),
    ).toBe(true);
    expect(queue.record.etag).toBe('"2"');
    expect(queue.state.etag).toBe('"2"');

    // Someone else saved meanwhile: their ETag is not taken, so the next
    // save meets the conflict rather than writing over them.
    shared.edit('carol');
    const { snapshotId, cursor, snapshots } = shared.head();
    expect(
      await queue.adoptIfSameHead({ snapshotId, cursor, snapshots }, '"3"'),
    ).toBe(false);
    expect(queue.record.etag).toBe('"2"');

    // Nor while the queue is blocked.
    queue.conflict();
    expect(
      await queue.adoptIfSameHead(
        { snapshotId: 's1', cursor: 0, snapshots: 1 },
        '"4"',
      ),
    ).toBe(false);
  });
});

describe('a draft someone shared', () => {
  test('a view-only share blocks the queue and says why', async () => {
    const shared = sharedDraft();
    const { queue } = await attachedTo(shared, { actor: 'bob' });

    shared.server.readOnly = true;
    shared.server.access = 'view';
    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state).toMatchObject({
      status: 'forbidden',
      accessLost: 'view-only',
    });
    expect(queue.record.queue).toHaveLength(1);

    // Still shared for editing, but the role may not change configs.
    shared.server.access = 'edit';
    await queue.retry();
    expect(queue.state.accessLost).toBe('role');
  });

  test('a share taken away, or a draft deleted, is gone', async () => {
    const shared = sharedDraft();
    const { queue } = await attachedTo(shared, { actor: 'bob' });

    shared.server.gone = true;
    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state).toMatchObject({ status: 'forbidden', accessLost: 'gone' });
    expect(describeState(state)).toMatch(/cannot save/i);

    // A conflict that finds the draft gone says so too.
    shared.server.gone = false;
    shared.touch();
    shared.api.getDraft.mockRejectedValueOnce(
      Object.assign(new Error('gone'), { response: { status: 404 } }),
    );
    expect((await queue.retry()).accessLost).toBe('gone');
  });

  test('the owner’s deleted draft is reported as before', async () => {
    const shared = sharedDraft();
    const { queue } = await attachedTo(shared);

    shared.server.gone = true;
    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state).toMatchObject({ status: 'error', accessLost: '' });
    expect(state.retryable).toBe(false);
    expect(shared.api.getDraft).not.toHaveBeenCalled();
  });
});

describe('refused snapshots', () => {
  function refusing(isBad) {
    return fakeApi({
      appendSnapshot: vi.fn(async (...args) => {
        const body = args[2];
        if (isBad(body.document)) {
          throw Object.assign(new Error('refused'), {
            response: {
              status: 422,
              data: {
                message:
                  'unable to save builder draft e11aa62f-3289-4051-a661-bc2fa63429ba',
                cause:
                  'builder: invalid request: document: nodes[1].device.hostname: duplicate hostname "node-2" (also nodes[0])',
              },
            },
          });
        }

        return {
          draft: { id: 'd1', owner: 'alice', snapshotId: 's9' },
          etag: '"9"',
        };
      }),
    });
  }

  test('a refused snapshot names the reason and is not retried on a timer', async () => {
    const timers = [];
    const api = refusing(() => true);
    const { queue } = await attached({
      api,
      setTimeout: (fn) => timers.push(fn),
    });

    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.status).toBe('error');
    expect(describeState(state)).toBe(
      'Could not save your last change. Duplicate hostname "node-2". Fix the diagram and it saves again.',
    );
    expect(describeState(state)).not.toMatch(/e11aa62f/);
    // Sending it again cannot help, so Retry saving is not offered.
    expect(state.retryable).toBe(false);
    expect(timers).toHaveLength(0);

    // Nothing new to send: saving again does not ask the server again.
    await queue.flush();
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
  });

  test('the next edit replaces a refused snapshot instead of queuing behind it', async () => {
    const bad = { ...doc, name: 'bad' };
    const api = refusing((document) => document.name === 'bad');
    const { queue } = await attached({ api });

    await queue.commit({ id: 'c1', label: 'bad', snapshot: bad });
    const state = await queue.commit({ id: 'c2', label: 'fix', snapshot: doc });

    expect(state.status).toBe('saved');
    expect(state.pending).toBe(0);
    expect(api.appendSnapshot).toHaveBeenCalledTimes(2);
    expect(api.appendSnapshot.mock.calls[1][2].summary).toBe('fix');
  });

  test('an oversized snapshot is not retried, and the next edit replaces it', async () => {
    const timers = [];
    const store = memoryStore();
    const big = { ...doc, name: 'big' };
    const api = fakeApi({
      appendSnapshot: vi.fn(async (...args) => {
        if (args[2].document.name === 'big') {
          throw Object.assign(new Error('too large'), {
            response: {
              status: 413,
              data: { message: 'This diagram is too large to save.' },
            },
          });
        }

        return {
          draft: { id: 'd1', owner: 'alice', snapshotId: 's9' },
          etag: '"9"',
        };
      }),
    });
    const { queue } = await attached({
      api,
      store,
      setTimeout: (fn) => timers.push(fn),
    });

    let state = await queue.commit({ id: 'c1', label: 'big', snapshot: big });

    expect(state.status).toBe('error');
    expect(state.retryable).toBe(false);
    expect(timers).toHaveLength(0);
    expect(describeState(state)).toMatch(/Remove part of the diagram/);

    // After a reload the queue still says why it is blocked, without asking
    // the server again.
    const reloaded = await attached({ api, store });
    state = await reloaded.queue.flush();

    expect(describeState(state)).toMatch(
      /too large.*Remove part of the diagram/,
    );
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);

    state = await queue.commit({ id: 'c2', label: 'smaller', snapshot: doc });

    expect(state.status).toBe('saved');
    expect(state.pending).toBe(0);
    expect(api.appendSnapshot).toHaveBeenCalledTimes(2);
    expect(api.appendSnapshot.mock.calls[1][2].summary).toBe('smaller');
  });

  // The server drops its oldest snapshots past its byte limit, so it may no
  // longer hold the one an undo goes back to.
  test('an undo to a snapshot the server no longer keeps does not block later edits', async () => {
    const api = fakeApi({
      moveCursor: vi.fn(async () => {
        throw Object.assign(new Error('not found'), {
          response: {
            status: 404,
            data: { message: 'builder: not found: snapshot s1' },
          },
        });
      }),
    });
    const { queue } = await attached({ api });

    let state = await queue.moveCursor({ snapshotId: 's1', commitId: 'c0' });

    expect(state.status).toBe('error');
    expect(state.retryable).toBe(false);
    expect(describeState(state)).toMatch(
      /the server no longer keeps that step\. Your next edit saves the diagram as it is shown\.$/,
    );

    state = await queue.commit({ id: 'c1', label: 'Added', snapshot: doc });

    expect(state.status).toBe('saved');
    expect(state.pending).toBe(0);
    expect(api.moveCursor).toHaveBeenCalledTimes(1);
    expect(api.appendSnapshot).toHaveBeenCalledTimes(1);
  });
});

describe('local storage', () => {
  // Version 1 of the database kept every entry's snapshot in the draft
  // record. Upgrading keeps only what the queue can replay, with each
  // snapshot a record of its own, and drops records with nothing
  // queued.
  test('version 1 records are split, keeping what the queue needs', () => {
    const v1 = {
      key: 'alice::alice::d1',
      etag: '"1"',
      entries: [
        { id: 'old', label: 'old', snapshot: { name: 'old' } },
        {
          id: 'c1',
          label: 'one',
          snapshot: { name: 'one' },
          serverSnapshotId: 's2',
        },
        { id: 'c2', label: 'two', snapshot: { name: 'two' } },
      ],
      queue: [
        { kind: 'cursor', commitId: 'c1' },
        { kind: 'snapshot', commitId: 'c2' },
      ],
    };

    expect(splitRecord(v1)).toEqual({
      record: {
        ...v1,
        entries: [
          { id: 'c1', label: 'one', serverSnapshotId: 's2' },
          { id: 'c2', label: 'two' },
        ],
      },
      entries: [
        { draft: v1.key, id: 'c1', snapshot: { name: 'one' } },
        { draft: v1.key, id: 'c2', snapshot: { name: 'two' } },
      ],
    });
    expect(splitRecord({ ...v1, queue: [] })).toBeNull();
    expect(splitRecord({ key: 'k' })).toBeNull();
  });

  // The queue changes its live record in place, and its snapshots come from
  // the editor's reactive state. The store keeps structured clones of plain
  // data, as IndexedDB does, so what it holds is what was stored, whatever
  // the caller does next.
  test('the memory store keeps what was stored, as IndexedDB does', async () => {
    const store = createMemoryStore();
    const { name } = doc;
    const record = {
      key: draftKey('alice', 'alice', 'd1'),
      etag: '"1"',
      queue: [{ opId: 'c1', kind: 'snapshot', commitId: 'c1' }],
      entries: [{ id: 'c1', label: 'one', snapshot: reactive(doc) }],
    };

    await store.put(record, { write: record.entries });
    record.queue.shift();
    record.entries[0].snapshot.name = 'changed';

    const stored = await store.get(record.key);

    expect(stored.queue).toHaveLength(1);
    expect(stored.entries[0].snapshot.name).toBe(name);
    expect(isProxy(stored.entries[0].snapshot)).toBe(false);

    // Each read is a copy of its own, and the draft record holds no
    // snapshot.
    stored.queue.length = 0;
    expect((await store.get(record.key)).queue).toHaveLength(1);
    expect((await store.all())[0].entries).toEqual([
      { id: 'c1', label: 'one' },
    ]);
  });

  test('a device that cannot store the queue never claims to keep it', async () => {
    const store = memoryStore();
    store.put = async () => {
      throw new Error('QuotaExceededError');
    };
    const { queue } = await attached({ store, isOnline: () => false });

    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.storageFailed).toBe(true);
    expect(describeState(state)).toMatch(/not stored anywhere yet/);
    expect(describeState(state)).not.toMatch(/kept on this device/);
  });

  test('a retry the user did not start keeps the problem text steady', async () => {
    let online = false;
    const timers = [];
    const seen = [];
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        throw new Error('network');
      }),
    });
    const queue = createAutosave({
      api,
      store: memoryStore(),
      actor: 'alice',
      onState: (state) => seen.push(state.status),
      setTimeout: (fn) => timers.push(fn),
      clearTimeout: () => {},
      isOnline: () => online,
    });
    await queue.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });
    online = true;
    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    seen.length = 0;

    timers.shift()();
    await vi.waitFor(() => expect(api.appendSnapshot).toHaveBeenCalledTimes(2));
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(seen).not.toContain('saving');
    expect(seen.at(-1)).toBe('offline');
  });
});

describe('leaving the page', () => {
  // A store whose writes finish only when the test says: a reload cuts off
  // the ones left unfinished.
  function slowStore(storage) {
    const store = memoryStore();
    const put = store.put;
    const waiting = [];

    store.storage = storage;
    Object.assign(store, {
      keep: createMemoryStore({ storage }).keep,
      release: createMemoryStore({ storage }).release,
    });
    store.put = (record, changes) => {
      const cloned = structuredClone({ ...record, entries: record.entries });
      const written = structuredClone(changes);

      return new Promise((resolve) => {
        waiting.push(() => resolve(put(cloned, written)));
      });
    };
    // Finishes the writes asked for, the first `count` of them or every
    // one, and lets their callers go on.
    store.finish = async (count = Infinity) => {
      for (let left = count; waiting.length > 0 && left > 0; left -= 1) {
        waiting.shift()();
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
    };

    return store;
  }

  // The store the next page opens: the database as the last page left it,
  // and the same localStorage.
  async function reopened(before, storage) {
    const store = createMemoryStore({ storage });

    for (const found of await before.all()) {
      const record = await before.get(found.key);

      if (!record.fromUnload) {
        await store.put(record, { write: record.entries });
      }
    }

    return store;
  }

  const key = draftKey('alice', 'alice', 'd1');
  const unloadKey = unloadCopyKey(key);

  function storedCopy(storage) {
    return JSON.parse(storage.getItem(unloadKey));
  }

  async function offline(store) {
    const queue = createAutosave({
      api: fakeApi(),
      store,
      actor: 'alice',
      isOnline: () => false,
    });

    await queue.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });

    return queue;
  }

  test('an edit the database had not stored when the page was left is kept, and the next page recovers it', async () => {
    const storage = memoryStorage();
    const store = createMemoryStore({ storage });
    const first = await offline(store);
    const put = store.put;

    // The database never finishes this write: the reload cut it off.
    store.put = () => new Promise(() => {});
    first.commit({ id: 'c1', label: 'Saved unapplied changes', snapshot: doc });

    expect(first.keepForUnload()).toBe(true);
    expect(storedCopy(storage)).toMatchObject({
      key,
      etag: '"1"',
      queue: [{ opId: 'c1', kind: 'snapshot', commitId: 'c1' }],
      entries: [{ id: 'c1', label: 'Saved unapplied changes', snapshot: doc }],
    });
    expect(await store.all()).toMatchObject([{ key, queue: [{ opId: 'c1' }] }]);

    store.put = put;
    const next = await offline(await reopened(store, storage));

    expect(next.record).toMatchObject({
      etag: '"1"',
      queue: [{ opId: 'c1' }],
      entries: [{ id: 'c1', snapshot: doc }],
    });
    // Once the database holds it, the copy goes.
    expect(storage.entries.has(unloadKey)).toBe(false);
    expect(await next.recover('alice', 'd1')).toMatchObject({
      queue: [{ opId: 'c1' }],
      entries: [{ id: 'c1', snapshot: doc }],
    });
  });

  test('only what the database may not hold is copied, and the copy merges into its record once', async () => {
    const storage = memoryStorage();
    const store = slowStore(storage);
    const first = await offline(store);
    const second = { ...doc, name: 'second' };

    first.commit({ id: 'c1', label: 'one', snapshot: doc });
    await store.finish();
    first.commit({ id: 'c2', label: 'two', snapshot: second });

    expect(first.keepForUnload()).toBe(true);
    // The queue, but only the snapshot the database may lack.
    expect(storedCopy(storage).queue.map((op) => op.opId)).toEqual([
      'c1',
      'c2',
    ]);
    expect(storedCopy(storage).entries).toEqual([
      { id: 'c1', label: 'one' },
      { id: 'c2', label: 'two', snapshot: second },
    ]);

    const next = await reopened(store, storage);
    const merged = await next.get(key);

    expect(merged.fromUnload).toBe(true);
    expect(merged.queue.map((op) => op.opId)).toEqual(['c1', 'c2']);
    expect(merged.entries).toEqual([
      { id: 'c1', label: 'one', snapshot: doc },
      { id: 'c2', label: 'two', snapshot: second },
    ]);
    expect(await next.all()).toMatchObject([
      { key, queue: [{ opId: 'c1' }, { opId: 'c2' }] },
    ]);
  });

  test('nothing is copied while the database holds every queued edit', async () => {
    const storage = memoryStorage();
    const queue = await offline(createMemoryStore({ storage }));

    expect(queue.keepForUnload()).toBe(true);
    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(queue.keepForUnload()).toBe(true);
    expect(storage.entries.size).toBe(0);
  });

  test('a copy made while the user stays goes once the database holds the edit', async () => {
    const storage = memoryStorage();
    const store = slowStore(storage);
    const queue = await offline(store);

    queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    queue.keepForUnload();
    expect(storage.entries.has(unloadKey)).toBe(true);

    await store.finish();

    expect(storage.entries.has(unloadKey)).toBe(false);
    expect((await store.get(key)).queue).toHaveLength(1);
  });

  test('a copy made while the user stays goes once the database holds what it copied, while newer edits are still being written', async () => {
    const storage = memoryStorage();
    const store = slowStore(storage);
    const queue = await offline(store);

    queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    queue.keepForUnload();
    queue.commit({ id: 'c2', label: 'two', snapshot: { ...doc, name: 'two' } });

    await store.finish(1);
    expect(storage.entries.has(unloadKey)).toBe(false);

    // Leaving again copies what the database lacks now.
    expect(queue.keepForUnload()).toBe(true);
    expect(storedCopy(storage).entries).toEqual([
      { id: 'c1', label: 'one' },
      { id: 'c2', label: 'two', snapshot: { ...doc, name: 'two' } },
    ]);

    await store.finish();
    expect(storage.entries.has(unloadKey)).toBe(false);
    expect((await store.get(key)).queue.map((op) => op.opId)).toEqual([
      'c1',
      'c2',
    ]);
  });

  test('a copy from one tab gives way to a later write to the database from another tab on the same draft', async () => {
    const storage = memoryStorage();
    // The database and localStorage both tabs share.
    const shared = createMemoryStore({ storage });
    let clock = Date.parse('2026-01-01T00:00:00.000Z');
    const now = () => new Date((clock += 1000)).toISOString();
    let cut = false;
    // Tab A's writes finish until its reload cuts them off.
    const tabA = {
      ...shared,
      put: (...args) => (cut ? new Promise(() => {}) : shared.put(...args)),
    };
    const tab = (store) =>
      createAutosave({
        api: fakeApi(),
        store,
        actor: 'alice',
        isOnline: () => false,
        now,
      });
    const a = tab(tabA);
    const b = tab(shared);
    const added = { ...doc, name: 'added' };

    await a.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });
    await b.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });

    cut = true;
    a.commit({ id: 'a1', label: 'Saved unapplied changes', snapshot: doc });
    expect(a.keepForUnload()).toBe(true);
    expect(storedCopy(storage).queue.map((op) => op.opId)).toEqual(['a1']);

    // Tab B, later: its edit reaches the database, which then holds it all.
    await b.commit({ id: 'b1', label: 'Added device', snapshot: added });
    expect(b.keepForUnload()).toBe(true);

    expect(await shared.all()).toMatchObject([
      { key, queue: [{ opId: 'b1' }] },
    ]);
    expect(storage.entries.has(unloadKey)).toBe(false);

    const next = await offline(shared);

    expect(next.record).toMatchObject({
      queue: [{ opId: 'b1' }],
      entries: [{ id: 'b1', snapshot: added }],
    });
  });

  test('a copy goes once the edit is sent', async () => {
    const storage = memoryStorage();
    const store = slowStore(storage);
    const api = fakeApi();
    let online = false;
    const queue = createAutosave({
      api,
      store,
      actor: 'alice',
      isOnline: () => online,
    });
    const attaching = queue.attach({
      owner: 'alice',
      draftId: 'd1',
      etag: '"1"',
    });

    await store.finish();
    await attaching;
    queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    queue.keepForUnload();
    online = true;
    const sending = queue.flush();

    await vi.waitFor(async () => {
      await store.finish();
      expect(api.appendSnapshot).toHaveBeenCalledOnce();
    });
    await store.finish();
    await sending;

    expect(storage.entries.has(unloadKey)).toBe(false);
    expect(await store.all()).toEqual([]);
  });

  test('a copy that does not fit is not kept, and says so', async () => {
    // A storage that refuses every write, as a browser does past its quota.
    const storage = memoryStorage();
    storage.setItem = () => {
      throw new DOMException('full', 'QuotaExceededError');
    };

    const store = slowStore(storage);
    const queue = await offline(store);

    queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(queue.keepForUnload()).toBe(false);
    expect(storage.entries.size).toBe(0);
  });

  test('a copy that no longer fits removes the older one, which would stand for less', () => {
    const storage = memoryStorage();
    const record = {
      key,
      queue: [{ opId: 'c1', kind: 'snapshot', commitId: 'c1' }],
      entries: [{ id: 'c1', label: 'one', snapshot: doc }],
    };

    expect(writeUnloadCopy(storage, record, record.entries)).toBe(true);
    storage.setItem = () => {
      throw new DOMException('full', 'QuotaExceededError');
    };

    expect(writeUnloadCopy(storage, record, record.entries)).toBe(false);
    expect(readUnloadCopy(storage, key)).toBeNull();
    expect(writeUnloadCopy(null, record)).toBe(false);
  });

  test('the page being hidden keeps the copy; a disposed queue keeps none', async () => {
    const storage = memoryStorage();
    const store = slowStore(storage);
    const queue = await offline(store);
    const page = new EventTarget();

    queue.listen(page);
    queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    page.dispatchEvent(new Event('pagehide'));
    expect(storage.entries.has(unloadKey)).toBe(true);

    storage.entries.clear();
    queue.dispose();
    page.dispatchEvent(new Event('pagehide'));
    expect(queue.keepForUnload()).toBe(true);
    expect(storage.entries.size).toBe(0);
  });

  test('a copy merges into the database record: its queue replaces it, and each entry is kept once', () => {
    const stored = {
      key,
      etag: '"1"',
      queue: [
        { opId: 'c0', kind: 'snapshot', commitId: 'c0' },
        { opId: 'c1', kind: 'snapshot', commitId: 'c1' },
      ],
      entries: [
        { id: 'c0', label: 'zero', snapshot: { name: 'zero' } },
        { id: 'c1', label: 'one', snapshot: { name: 'one' } },
      ],
    };
    const copy = {
      key,
      etag: '"2"',
      queue: [
        { opId: 'c1', kind: 'snapshot', commitId: 'c1' },
        { opId: 'c2', kind: 'snapshot', commitId: 'c2' },
        { opId: 'c3', kind: 'snapshot', commitId: 'c3' },
      ],
      entries: [
        { id: 'c1', label: 'one' },
        { id: 'c2', label: 'two', snapshot: { name: 'two' } },
        { id: 'c3', label: 'three' },
      ],
    };

    // c0 was sent since the database stored it; the database never stored
    // c3's snapshot, which is left out, as a read of the database leaves it.
    expect(mergeUnloadCopy(stored, copy)).toEqual({
      key,
      etag: '"2"',
      queue: copy.queue,
      entries: [
        { id: 'c1', label: 'one', snapshot: { name: 'one' } },
        { id: 'c2', label: 'two', snapshot: { name: 'two' } },
      ],
      fromUnload: true,
    });
    expect(mergeUnloadCopy(stored, null)).toBe(stored);

    // A record written after the copy was taken is the newer one.
    const at = (second) => `2026-01-01T00:00:0${second}.000Z`;
    const later = { ...stored, updatedAt: at(2) };

    expect(staleUnloadCopy(later, { ...copy, updatedAt: at(1) })).toBe(true);
    expect(mergeUnloadCopy(later, { ...copy, updatedAt: at(1) })).toBe(later);
    // A copy taken as the record's write was asked for, or after, is not;
    // nor is one with no time to compare.
    for (const updatedAt of [at(2), at(3), undefined]) {
      expect(staleUnloadCopy(later, { ...copy, updatedAt })).toBe(false);
      expect(mergeUnloadCopy(later, { ...copy, updatedAt }).fromUnload).toBe(
        true,
      );
    }
    expect(staleUnloadCopy(undefined, { ...copy, updatedAt: at(1) })).toBe(
      false,
    );
    expect(mergeUnloadCopy(undefined, copy).entries).toEqual([
      { id: 'c2', label: 'two', snapshot: { name: 'two' } },
    ]);
  });

  test('a copy older than the database record is set aside and removed, whether the draft is read or listed', async () => {
    const storage = memoryStorage();
    const store = createMemoryStore({ storage });
    const at = (second) => `2026-01-01T00:00:0${second}.000Z`;
    const copy = {
      key,
      updatedAt: at(1),
      queue: [{ opId: 'c1', kind: 'snapshot', commitId: 'c1' }],
      entries: [{ id: 'c1', label: 'one', snapshot: doc }],
    };
    // The user stayed and edited on, and the tab was killed after the
    // database held the newer record but before the copy was removed.
    const record = {
      key,
      updatedAt: at(2),
      queue: [...copy.queue, { opId: 'c2', kind: 'snapshot', commitId: 'c2' }],
      entries: [...copy.entries, { id: 'c2', label: 'two', snapshot: doc }],
    };

    await store.put(record, { write: record.entries });

    for (const read of [
      () => store.get(key),
      async () => (await store.all())[0],
    ]) {
      expect(store.keep(copy, { write: copy.entries })).toBe(true);

      const found = await read();

      expect(found.fromUnload).toBeUndefined();
      expect(found.queue.map((op) => op.opId)).toEqual(['c1', 'c2']);
      expect(storage.entries.has(unloadKey)).toBe(false);
    }
  });

  test('a copy that is not one is ignored, and removing a draft removes its copy', async () => {
    const storage = memoryStorage();
    const store = createMemoryStore({ storage });

    storage.setItem(unloadKey, '{not json');
    expect(await store.get(key)).toBeUndefined();
    storage.setItem(unloadKey, JSON.stringify({ key: 'other', queue: [] }));
    expect(await store.all()).toEqual([]);

    const record = {
      key,
      queue: [{ opId: 'c1', kind: 'snapshot', commitId: 'c1' }],
      entries: [{ id: 'c1', label: 'one', snapshot: doc }],
    };

    expect(store.keep(record, { write: record.entries })).toBe(true);
    await store.remove(key);
    expect(storage.entries.size).toBe(0);
  });
});

describe('state descriptions', () => {
  test('each state has accessible text', () => {
    expect(describeState(initialState())).toBeTruthy();
    expect(describeState({ status: 'saving', pending: 2 })).toMatch(/saving/i);
    expect(describeState({ status: 'conflict' })).toMatch(/conflict|changed/i);
    expect(describeState({ status: 'offline' })).toMatch(/offline|device/i);
    expect(describeState({ status: 'offline', pending: 1 })).toMatch(
      /^Offline: 1 change kept on this device/,
    );
  });

  test('only a new problem, or the end of one, is announced', () => {
    const offline = { status: 'offline', pending: 1, message: 'x' };

    expect(saveAnnouncement(null, { status: 'saving', pending: 1 })).toBe('');
    expect(saveAnnouncement({ status: 'saved' }, { status: 'saved' })).toBe('');
    expect(saveAnnouncement({ status: 'saved' }, offline)).toMatch(
      /^Offline: 1 change/,
    );
    // The same problem again (a retry that failed the same way) is silent.
    expect(saveAnnouncement(offline, { ...offline, pending: 2 })).toBe('');
    expect(saveAnnouncement(offline, { status: 'saved' })).toBe(
      'All changes saved.',
    );
    // The conflict panel announces itself.
    expect(saveAnnouncement({ status: 'saved' }, { status: 'conflict' })).toBe(
      '',
    );
  });

  test('a waiting save message is stale once another state is settled', () => {
    expect(staleSaveMessage('offline', 'offline')).toBe(false);
    // A retry under way has no outcome yet.
    expect(staleSaveMessage('offline', 'saving')).toBe(false);
    expect(staleSaveMessage('offline', 'idle')).toBe(false);
    expect(staleSaveMessage('offline', 'conflict')).toBe(true);
    expect(staleSaveMessage('saved', 'error')).toBe(true);
  });
});
