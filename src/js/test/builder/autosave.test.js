import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import {
  createAutosave,
  describeState,
  initialState,
  RETRY_DELAYS,
} from '@/builder/autosave.js';

import { sampleDocument } from './fixtures.js';

function memoryStore() {
  const data = new Map();

  return {
    data,
    async put(record) {
      data.set(record.key, JSON.parse(JSON.stringify(record)));
    },
    async get(key) {
      const found = data.get(key);

      return found ? JSON.parse(JSON.stringify(found)) : null;
    },
    async all() {
      return [...data.values()];
    },
    async remove(key) {
      data.delete(key);
    },
  };
}

function fakeApi(overrides = {}) {
  let revision = 1;

  return {
    appendSnapshot: vi.fn(async () => {
      revision += 1;

      return {
        draft: { id: 'd1', owner: 'alice', snapshotId: `s${revision}` },
        history: [{ id: `s${revision}` }],
        cursor: 0,
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

function offline() {
  return new Error('network down');
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
          history: [{ id: 'server-c1' }],
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
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        const [record] = await store.all();

        expect(record.queue).toHaveLength(1);
        expect(record.entries).toHaveLength(1);

        return { draft: {}, etag: '"2"' };
      }),
    });
    const { queue } = await attached({ store, api });

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    const [record] = await store.all();

    expect(record.queue).toHaveLength(0);
    expect(record.entries).toHaveLength(1);
  });

  test('history is capped at the 100 snapshots the server keeps', async () => {
    const { queue } = await attached({ extra: { historyLimit: 100 } });

    for (let i = 0; i < 120; i += 1) {
      await queue.commit({ id: `c${i}`, label: `step ${i}`, snapshot: doc });
    }

    expect(queue.record.entries).toHaveLength(100);
    expect(queue.record.entries[0].id).toBe('c20');
  });
});

describe('recovery', () => {
  test('a full local log with pending work is recovered', async () => {
    const store = memoryStore();
    const first = await attached({ store, api: fakeApi() });

    await first.queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    const second = await attached({ store });
    const recovered = await second.queue.recover('alice', 'd1');

    expect(recovered.entries).toHaveLength(1);
    expect(recovered.entries[0].label).toBe('one');
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
        history: [{ id: 's2' }],
        cursor: 0,
        etag: '"2"',
      });
      await pending;

      expect(onState).toHaveBeenCalledTimes(stateCalls);
      expect(onDraft).not.toHaveBeenCalled();
      expect(setTimeout).not.toHaveBeenCalled();
    });
  });

  test('records are scoped to owner and actor', async () => {
    const store = memoryStore();
    const mine = createAutosave({ api: fakeApi(), store, actor: 'alice' });
    const theirs = createAutosave({ api: fakeApi(), store, actor: 'bob' });

    await mine.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });
    await theirs.attach({ owner: 'alice', draftId: 'd1', etag: '"1"' });

    expect(await mine.recoverAll()).toHaveLength(1);
    expect(await theirs.recoverAll()).toHaveLength(1);
    expect(store.data.size).toBe(2);
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

  test('discarding local work is an explicit choice', async () => {
    const api = fakeApi({
      appendSnapshot: vi.fn(async () => {
        throw conflict();
      }),
    });
    const { queue } = await attached({ api });

    await queue.commit({ id: 'c1', label: 'one', snapshot: doc });
    await queue.discardLocal();

    expect(queue.record.entries).toHaveLength(0);
    expect(queue.record.queue).toHaveLength(0);
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
      setTimeout: (fn, ms) => {
        delays.push(ms);

        return delays.length;
      },
    });

    const state = await queue.commit({ id: 'c1', label: 'one', snapshot: doc });

    expect(state.status).toBe('error');
    expect(delays[0]).toBe(RETRY_DELAYS[0]);
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

describe('state descriptions', () => {
  test('each state has accessible text', () => {
    expect(describeState(initialState())).toBeTruthy();
    expect(describeState({ status: 'saving', pending: 2 })).toMatch(/saving/i);
    expect(describeState({ status: 'conflict' })).toMatch(/conflict|changed/i);
    expect(describeState({ status: 'offline' })).toMatch(/offline|device/i);
  });
});
