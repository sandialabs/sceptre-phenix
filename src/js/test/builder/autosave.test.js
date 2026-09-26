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
} from '@/builder/autosave.js';
import { createMemoryStore, draftKey, splitRecord } from '@/builder/idb.js';

import { sampleDocument } from './fixtures.js';

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

    // Once the queue drains, nothing is left on this device (R86).
    expect(await store.all()).toEqual([]);
    expect(store.snapshots.size).toBe(0);
  });

  // An edit writes its own snapshot once, and the draft record keeps no
  // snapshot, however long the history is (R88).
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
  // draft was opened) finds the snapshot by its operation id (R84).
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
  // the recovered history leaves it out (R83).
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
  // so a reload can show it (R83).
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
    // against the old ETag, or reopening the draft reports a conflict (R84).
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

    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Recovered', document: doc }),
    );
    expect(created.draft.id).toBe('d2');
    expect(queue.record.draftId).toBe('d2');
    expect(queue.record.queue).toHaveLength(0);
    expect(queue.record.entries).toHaveLength(2);
  });

  // The fork saves the history on screen, in order, with its cursor on the
  // entry shown, and every entry learns its snapshot in the new draft, so
  // undo there never names the old draft's snapshots (R39). The new draft
  // is the actor's: the server refuses one for anybody else (R38).
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
  // answers with (R40).
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
  // and the queue is kept (R41).
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
      pending: 1,
    });
    expect(describeState(state)).toBe(
      'Could not save: your session has ended. Use Export to keep a copy of the diagram, then sign in again.',
    );
    // The same credentials are refused every time: no timer retry.
    expect(timers).toHaveLength(0);

    expect(await queue.retry()).toMatchObject({ status: 'saved', pending: 0 });
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

  test('an oversized snapshot is not retried, and the next edit replaces it (R19)', async () => {
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
  // longer hold the one an undo goes back to (R19).
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
  // snapshot a record of its own (R88), and drops records with nothing
  // queued (R86).
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
  // the caller does next (R89).
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
