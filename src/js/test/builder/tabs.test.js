// Two tabs of one browser with the same draft open: each keeps its own
// local queue, they hear each other, and while both hold changes the
// server does not have, neither sends until the user chooses which to save.

import { describe, expect, test, vi } from 'vitest';

vi.mock('@/utils/axios.js', () => ({ default: {} }));

import { createAutosave, describeState } from '@/builder/autosave.js';
import {
  createMemoryStore,
  draftKey,
  tabRecordKey,
  unloadCopyKey,
} from '@/builder/idb.js';
import {
  TAB_ID_KEY,
  applyChoice,
  choiceRows,
  claimTab,
  createTabCoordinator,
  forkClosedQueue,
  answerPresence,
  lockName,
  needsChoice,
  openChannel,
  presentTabs,
  tabsNotice,
} from '@/builder/tabs.js';

// Lets the channel's messages, and the states posted once a task ends,
// arrive.
async function settle() {
  for (let round = 0; round < 20; round += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

// The channels the tabs of one browser share, one per name: each message
// reaches every other member of the channel, a task later, as a copy. One
// opened without a name hears, and is heard on, every channel. A muted
// member's messages go nowhere, as a page's do once it has crashed.
function channelHub() {
  const members = new Set();

  return {
    open(name = '') {
      const listeners = new Set();
      const member = { listeners, muted: false, name };

      members.add(member);

      return {
        connected: true,
        mute() {
          member.muted = true;
        },
        post(message) {
          if (member.muted) {
            return;
          }

          const copy = structuredClone(message);

          members.forEach((other) => {
            if (
              other !== member &&
              (!name || !other.name || other.name === name)
            ) {
              setTimeout(() => other.listeners.forEach((fn) => fn(copy)), 0);
            }
          });
        },
        listen(fn) {
          listeners.add(fn);

          return () => listeners.delete(fn);
        },
        close() {
          members.delete(member);
        },
      };
    },
  };
}

// Web Locks as the browser keeps them: a lock is held until its callback's
// promise settles, or, here, until the test lets it go (a tab closing).
function fakeLocks() {
  const held = new Map();
  const waiting = new Map();

  function grant(name) {
    const next = (waiting.get(name) || []).shift();

    next?.();
  }

  return {
    async request(name, options, callback) {
      if (held.has(name)) {
        if (options.ifAvailable) {
          return callback(null);
        }

        await new Promise((resolve, reject) => {
          const queue = waiting.get(name) || [];

          waiting.set(name, queue);
          queue.push(resolve);
          options.signal?.addEventListener('abort', () => {
            if (queue.includes(resolve)) {
              queue.splice(queue.indexOf(resolve), 1);
              reject(new DOMException('Aborted', 'AbortError'));
            }
          });
        });
      }

      held.set(name, true);

      const result = await callback({ name });

      held.delete(name);
      grant(name);

      return result;
    },
    async query() {
      return { held: [...held.keys()].map((name) => ({ name })) };
    },
    // The page holding it has gone.
    release(name) {
      held.delete(name);
      grant(name);
    },
    held,
  };
}

// A save answers with the draft alone, as the server does.
function fakeApi() {
  let revision = 1;

  return {
    appendSnapshot: vi.fn(async (owner, draftId) => {
      revision += 1;

      return {
        draft: { id: draftId, owner, snapshotId: `s${revision}` },
        history: null,
        cursor: revision - 1,
        etag: `"${revision}"`,
      };
    }),
    moveCursor: vi.fn(async () => ({ draft: {}, etag: `"${revision}"` })),
    createDraft: vi.fn(async (request) => ({
      draft: { id: `fork-${request.title}`, owner: 'alice', snapshotId: 'f1' },
      etag: '"f1"',
    })),
  };
}

// A browser: one device store, a channel per draft, one set of locks, and
// whether it is online, or, by tab id, whether that tab's requests get
// through. Each tab has its own id and save queue.
function browser() {
  const env = {
    api: fakeApi(),
    device: createMemoryStore(),
    hub: channelHub(),
    locks: fakeLocks(),
    online: false,
    reaches: {},
    // The channel each tab's queue last opened, by tab id.
    channels: {},
  };

  env.tab = (id, { locks = env.locks } = {}) => {
    locks?.request(lockName(id), {}, () => new Promise(() => {}));

    const queue = createAutosave({
      api: env.api,
      store: env.device,
      actor: 'alice',
      isOnline: () => env.reaches[id] ?? env.online,
      setTimeout: () => 0,
      clearTimeout: () => {},
      tabs: {
        tab: async () => id,
        open: (draft) => {
          env.channels[id] = env.hub.open(
            draftKey(draft.actor, draft.owner, draft.draftId),
          );

          return createTabCoordinator({
            ...draft,
            channel: env.channels[id],
            locks,
            waitMs: 30,
          });
        },
      },
    });

    return queue;
  };

  // A tab closing: it leaves the draft, and its lock goes with the page.
  env.close = (queue, id) => {
    queue.dispose();
    env.locks.release(lockName(id));
  };

  // A tab that crashes, or that the browser discards, says nothing: only
  // its lock goes.
  env.crash = (queue, id) => {
    env.channels[id]?.mute();
    queue.dispose();
    env.locks.release(lockName(id));
  };

  return env;
}

const DRAFT = { owner: 'alice', draftId: 'd1', etag: '"1"' };
const BASE = draftKey('alice', 'alice', 'd1');
const edit = (id, name = id) => ({
  id,
  label: `Edit ${id}`,
  snapshot: { metadata: { name }, nodes: [], edges: [] },
});

async function twoTabsOffline() {
  const env = browser();
  const a = env.tab('A');
  const b = env.tab('B');

  await a.attach(DRAFT);
  await b.attach(DRAFT);
  await a.commit(edit('a1'));
  await a.commit(edit('a2'));
  await b.commit(edit('b1'));
  await settle();

  return { env, a, b };
}

describe('two tabs with one draft open', () => {
  test("each keeps its own queue, so neither replaces the other's changes", async () => {
    const { env } = await twoTabsOffline();

    const records = await env.device.all();

    expect(
      records
        .map((record) => ({
          key: record.key,
          tab: record.tab,
          queue: record.queue.map((op) => op.opId),
        }))
        .sort((x, y) => x.key.localeCompare(y.key)),
    ).toEqual([
      { key: tabRecordKey(BASE, 'A'), tab: 'A', queue: ['a1', 'a2'] },
      { key: tabRecordKey(BASE, 'B'), tab: 'B', queue: ['b1'] },
    ]);
  });

  test('each hears the other, and says what the other holds', async () => {
    const { a, b } = await twoTabsOffline();

    expect(a.state.tabs).toEqual([
      expect.objectContaining({
        tab: 'B',
        pending: 1,
        status: 'offline',
        open: true,
      }),
    ]);
    expect(b.state.tabs).toEqual([
      expect.objectContaining({ tab: 'A', pending: 2 }),
    ]);
    expect(tabsNotice(a.state)).toBe(
      'This draft is also open in another tab, which has 1 change not saved yet. Changes made in both tabs cannot both be kept.',
    );
    expect(tabsNotice({ tabs: [{ open: true, pending: 0 }] })).toBe(
      'This draft is also open in another tab, where all changes are saved. Changes made in both tabs cannot both be kept.',
    );
    // A draft closed in the other tab, whose changes are still being sent,
    // is not open there.
    expect(tabsNotice({ tabs: [{ open: false, pending: 0 }] })).toBe('');
    expect(tabsNotice({ tabs: [{ open: false, pending: 3 }] })).toBe(
      'Another tab has 3 changes to this draft not saved yet.',
    );
  });

  test('back online, neither sends: each waits for the user to choose', async () => {
    const { env, a, b } = await twoTabsOffline();

    env.online = true;
    await a.flush();
    await b.flush();
    await settle();

    expect(env.api.appendSnapshot).not.toHaveBeenCalled();
    for (const queue of [a, b]) {
      expect(queue.state).toMatchObject({ status: 'idle', heldForTabs: true });
      expect(needsChoice(queue.state)).toBe(true);
    }
    expect(describeState(a.state)).toBe(
      '2 changes not saved yet. Choose which changes to save.',
    );
    expect(tabsNotice(a.state)).toContain(
      'Your 2 changes here are not sent until you choose which to save.',
    );
  });

  test('an edit while another tab holds changes waits too, until the user chooses', async () => {
    const env = browser();
    const a = env.tab('A');
    const b = env.tab('B');

    await a.attach(DRAFT);
    await b.attach(DRAFT);
    await b.commit(edit('b1'));
    await settle();

    // Only the other tab is offline.
    env.reaches.A = true;
    await a.commit(edit('a1'));
    expect(a.state).toMatchObject({ status: 'idle', heldForTabs: true });
    expect(env.api.appendSnapshot).not.toHaveBeenCalled();

    // The user chose the other tab's changes: this tab's no longer send,
    // and the other tab's do once it is back online.
    a.chooseVersion('tab:B');
    await settle();
    expect(a.state).toMatchObject({ status: 'conflict', otherTab: true });

    env.reaches.B = true;
    await b.flush();
    expect(
      env.api.appendSnapshot.mock.calls.map((call) => call[2].opId),
    ).toEqual(['b1']);
  });

  test("another tab's changes being sent do not hold this tab's", async () => {
    const env = browser();
    let answer;

    env.online = true;
    env.api.appendSnapshot.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          answer = resolve;
        }),
    );
    const a = env.tab('A');
    const b = env.tab('B');

    await a.attach(DRAFT);
    await b.attach(DRAFT);
    const sending = b.commit(edit('b1'));

    await settle();
    expect(a.state.tabs[0]).toMatchObject({ pending: 1, status: 'saving' });

    await a.commit(edit('a1'));
    expect(a.state.heldForTabs).toBe(false);
    expect(
      env.api.appendSnapshot.mock.calls.map((call) => call[2].opId),
    ).toEqual(['b1', 'a1']);

    answer({ draft: { snapshotId: 's9' }, etag: '"9"' });
    await sending;
  });

  test("the choice sends the chosen tab's changes; the other tab keeps its own, as a conflict it saves as a new draft", async () => {
    const { env, a, b } = await twoTabsOffline();

    env.online = true;
    await a.flush();
    await b.flush();
    await settle();

    a.chooseVersion('tab:A');
    await settle();

    expect(
      env.api.appendSnapshot.mock.calls.map((call) => call[2].opId),
    ).toEqual(['a1', 'a2']);
    expect(a.state).toMatchObject({
      status: 'saved',
      pending: 0,
      heldForTabs: false,
    });
    expect(b.state).toMatchObject({
      status: 'conflict',
      otherTab: true,
      pending: 1,
    });
    expect(describeState(b.state)).toBe(
      "Not saved: you chose another tab's changes",
    );
    // Nothing of the other tab's was dropped.
    expect(
      (await env.device.get(tabRecordKey(BASE, 'B'))).queue.map(
        (op) => op.opId,
      ),
    ).toEqual(['b1']);

    await b.forkLocalHistory({ title: 'Lab (local copy)' });
    expect(env.api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Lab (local copy)',
        forkOf: 'alice/d1',
      }),
    );
    expect(b.state.status).toBe('saved');
  });

  test('a tab whose changes go to a new draft leaves none behind as a closed tab’s', async () => {
    const { env, a, b } = await twoTabsWaiting();
    const remove = env.device.remove;

    // The device store takes a while to delete the tab's record, as
    // IndexedDB may in another page.
    env.device.remove = async (key) => {
      await settle();

      return remove(key);
    };

    a.chooseVersion('tab:A');
    await settle();
    await b.forkLocalHistory({ title: 'Lab (local copy)' });
    await settle();

    expect(await env.device.all()).toEqual([]);
    expect(a.state).toMatchObject({ status: 'saved', tabs: [], versions: [] });
    expect(tabsNotice(a.state)).toBe('');
  });

  test("a tab that opens the draft with changes of its own waits for an open tab's, which it hears first", async () => {
    const { env, a, b } = await twoTabsOffline();

    // The second tab reloads; the first still holds its changes, offline.
    env.close(b, 'B');
    env.reaches.B = true;
    const reloaded = env.tab('B');

    await reloaded.attach(DRAFT);
    expect(reloaded.record.queue.map((op) => op.opId)).toEqual(['b1']);
    await reloaded.flush();

    expect(env.api.appendSnapshot).not.toHaveBeenCalled();
    expect(reloaded.state).toMatchObject({ heldForTabs: true });
    expect(needsChoice(reloaded.state)).toBe(true);

    // The first tab heard the reload go and come back: the queue is the
    // open tab's, once, not a closed tab's too.
    await settle();
    expect(a.state.versions).toEqual([
      expect.objectContaining({ id: 'tab:B', where: 'tab', changes: 1 }),
    ]);
  });

  test('a reload finds its own queue; another tab that opens the draft takes a closed tab’s', async () => {
    const env = browser();
    const a = env.tab('A');

    await a.attach(DRAFT);
    await a.commit(edit('a1'));

    // Reloaded, the tab keeps its id, and finds its queue.
    env.close(a, 'A');
    const reloaded = env.tab('A');

    await reloaded.attach(DRAFT);
    expect(reloaded.record.queue.map((op) => op.opId)).toEqual(['a1']);
    expect(reloaded.state.versions).toEqual([]);

    // Closed, its queue is the only version: the next tab takes it.
    env.close(reloaded, 'A');
    await settle();
    const c = env.tab('C');

    await c.attach(DRAFT);
    expect((await c.recover('alice', 'd1')).queue.map((op) => op.opId)).toEqual(
      ['a1'],
    );
    expect(
      (await env.device.all()).map((record) => [record.key, record.tab]),
    ).toEqual([[tabRecordKey(BASE, 'C'), 'C']]);
    expect(c.state.versions).toEqual([]);
  });

  test('a queue kept before tabs had ids is taken as a closed tab’s', async () => {
    const env = browser();

    await env.device.put(
      {
        key: BASE,
        actor: 'alice',
        owner: 'alice',
        draftId: 'd1',
        etag: '"1"',
        entries: [{ id: 'old', label: 'Edit old' }],
        queue: [{ opId: 'old', kind: 'snapshot', commitId: 'old' }],
      },
      { write: [edit('old')] },
    );
    const c = env.tab('C');

    await c.attach(DRAFT);
    expect(c.record.queue.map((op) => op.opId)).toEqual(['old']);
    expect(c.record.entries[0].snapshot).toEqual(edit('old').snapshot);
  });

  test("an open tab's queue is not taken, and a tab closing with changes leaves them to choose", async () => {
    const { env, a, b } = await twoTabsOffline();
    const c = env.tab('C');

    await c.attach(DRAFT);
    await settle();
    expect(c.record.queue).toEqual([]);
    expect(c.state.tabs.map((peer) => peer.tab).sort()).toEqual(['A', 'B']);

    env.close(b, 'B');
    await settle();
    expect(a.state.versions).toEqual([
      expect.objectContaining({ where: 'closed', tab: 'B', changes: 1 }),
    ]);
    expect(tabsNotice(c.state)).toContain(
      'A closed tab left 1 change to this draft that is not saved yet.',
    );
  });

  test('queues two closed tabs left are not taken: the user chooses', async () => {
    const { env, a, b } = await twoTabsOffline();

    env.close(a, 'A');
    env.close(b, 'B');
    await settle();
    const c = env.tab('C');

    await c.attach(DRAFT);

    expect(c.record.queue).toEqual([]);
    expect(c.state.versions.map((version) => version.tab).sort()).toEqual([
      'A',
      'B',
    ]);
    expect(needsChoice(c.state)).toBe(true);
  });

  // Both tabs wait for the user to choose, online.
  async function twoTabsWaiting() {
    const found = await twoTabsOffline();

    found.env.online = true;
    await found.a.flush();
    await found.b.flush();
    await settle();

    return found;
  }

  test('a tab that closes while the user chooses leaves its changes to choose: the other still waits', async () => {
    const { env, a, b } = await twoTabsWaiting();
    const states = [];

    a.observe({ onState: (state) => states.push(state) });
    env.close(b, 'B');
    await settle();

    expect(env.api.appendSnapshot).not.toHaveBeenCalled();
    expect(a.state).toMatchObject({ heldForTabs: true, pending: 2 });
    expect(a.state.versions).toEqual([
      expect.objectContaining({ where: 'closed', tab: 'B', changes: 1 }),
    ]);
    // Not for a moment was there nothing to choose between.
    expect(states.every((state) => state.versions.length > 0)).toBe(true);
    expect(needsChoice(a.state)).toBe(true);
  });

  test('a tab reloaded while the user chooses says what it holds as it opens: neither sends', async () => {
    const { env, a, b } = await twoTabsWaiting();

    env.close(b, 'B');
    const reloaded = env.tab('B');

    await reloaded.attach(DRAFT);
    await settle();

    expect(env.api.appendSnapshot).not.toHaveBeenCalled();
    expect(a.state.versions).toEqual([
      expect.objectContaining({ where: 'tab', tab: 'B', changes: 1 }),
    ]);
    expect(a.state.heldForTabs).toBe(true);

    // Its first word already says so, before it reads anything else.
    const heard = [];
    const listener = env.hub.open();

    listener.listen((message) => heard.push(message));
    env.close(reloaded, 'B');
    await settle();
    await env.tab('B').attach(DRAFT);
    await settle();

    expect(heard.find((message) => message.type === 'hello')).toMatchObject({
      from: 'B',
      state: { pending: 1 },
    });
    expect(env.api.appendSnapshot).not.toHaveBeenCalled();
  });

  test('a held send reads the queues closed tabs left again before it goes', async () => {
    const env = browser();

    env.online = true;
    env.locks.request(lockName('B'), {}, () => new Promise(() => {}));
    // Another tab, holding a change it cannot send.
    const other = createTabCoordinator({
      actor: 'alice',
      owner: 'alice',
      draftId: 'd1',
      tab: 'B',
      state: { pending: 1, status: 'offline' },
      channel: env.hub.open(),
      locks: env.locks,
    });
    const a = env.tab('A');

    await a.attach(DRAFT);
    await settle();
    await a.commit(edit('a1'));
    expect(a.state.heldForTabs).toBe(true);

    // A tab this one never heard from left a queue, and the other tab's
    // change is then saved.
    await env.device.put(
      {
        key: tabRecordKey(BASE, 'Z'),
        tab: 'Z',
        actor: 'alice',
        owner: 'alice',
        draftId: 'd1',
        etag: '"1"',
        entries: [{ id: 'z1', label: 'Edit z1' }],
        queue: [{ opId: 'z1', kind: 'snapshot', commitId: 'z1' }],
      },
      { write: [edit('z1')] },
    );
    other.publish({ pending: 0, status: 'saved' });
    await settle();

    expect(env.api.appendSnapshot).not.toHaveBeenCalled();
    expect(a.state).toMatchObject({ heldForTabs: true });
    expect(a.state.versions).toEqual([
      expect.objectContaining({ where: 'closed', tab: 'Z', changes: 1 }),
    ]);
    other.close();
  });

  test('a tab that goes without a word is gone once its lock is free: its changes are a closed tab’s', async () => {
    const { env, a, b } = await twoTabsOffline();

    env.crash(b, 'B');
    await settle();

    expect(a.state.tabs).toEqual([]);
    expect(a.state.versions).toEqual([
      expect.objectContaining({ where: 'closed', tab: 'B', changes: 1 }),
    ]);
    expect(tabsNotice(a.state)).not.toContain('also open in another tab');
    await expect(a.tabOpen('B')).resolves.toBe(false);
  });

  test('a choice of a tab that has gone is not carried out', async () => {
    const calls = [];
    const outcome = await applyChoice({
      choice: 'tab:B',
      rows: choiceRows({
        tab: 'A',
        pending: 1,
        versions: [{ id: 'tab:B', where: 'tab', tab: 'B', changes: 2 }],
      }),
      queue: {
        tabOpen: async () => false,
        chooseVersion: (id) => calls.push(['choose', id]),
        rescan: async () => calls.push(['rescan']),
      },
      keepMine: async () => calls.push(['keepMine']),
      keepClosed: async () => calls.push(['keepClosed']),
      reopen: async () => calls.push(['reopen']),
    });

    expect(outcome).toMatchObject({ gone: true, kept: [], failed: [] });
    expect(calls).toEqual([['rescan']]);
  });

  test('a duplicated tab takes an id of its own; a reloaded one keeps its id', async () => {
    const locks = fakeLocks();
    const items = new Map();
    const storage = {
      getItem: (key) => items.get(key) ?? null,
      setItem: (key, value) => items.set(key, value),
    };
    let next = 0;
    const random = () => `id${(next += 1)}`;

    const first = await claimTab({ storage, locks, random });

    expect(first).toBe('id1');
    expect(items.get(TAB_ID_KEY)).toBe('id1');

    // A duplicate copies sessionStorage, but the first tab holds the lock.
    const copy = new Map(items);
    const duplicate = await claimTab({
      storage: {
        getItem: (key) => copy.get(key) ?? null,
        setItem: (key, value) => copy.set(key, value),
      },
      locks,
      random,
      waitMs: 20,
    });

    expect(duplicate).toBe('id2');
    expect(copy.get(TAB_ID_KEY)).toBe('id2');

    // Reloaded, the page before lets go of the lock a moment later.
    setTimeout(() => locks.release(lockName('id1')), 5);
    await expect(claimTab({ storage, locks, random })).resolves.toBe('id1');

    // Without Web Locks, each page takes a new id.
    await expect(claimTab({ storage, random })).resolves.toBe('id3');
  });

  test('without Web Locks, a queue no open tab answers for is a closed tab’s', async () => {
    const hub = channelHub();
    const records = async () => [
      { key: 'k-open', tab: 'open', queue: [{ opId: 'x' }] },
      { key: 'k-closed', tab: 'closed', queue: [{ opId: 'y' }] },
    ];
    const other = createTabCoordinator({
      actor: 'alice',
      owner: 'alice',
      draftId: 'd1',
      tab: 'open',
      channel: hub.open(),
    });
    const coordinator = createTabCoordinator({
      actor: 'alice',
      owner: 'alice',
      draftId: 'd1',
      tab: 'me',
      channel: hub.open(),
      records,
      waitMs: 30,
    });

    const closed = await coordinator.scan();

    expect(closed.map((version) => version.key)).toEqual(['k-closed']);
    other.close();
    coordinator.close();
  });

  // Logout asks so which tabs are open (see session.js).
  test('without Web Locks, the open tabs answer who is open; this tab is not among them', async () => {
    const hub = channelHub();

    answerPresence(hub.open(), 'open');
    answerPresence(hub.open(), 'me');

    await expect(
      presentTabs({ channel: hub.open(), self: 'me', waitMs: 30 }),
    ).resolves.toEqual(new Set(['open']));
    // Where no channel works, none answers.
    await expect(
      presentTabs({ channel: openChannel('tabs') }),
    ).resolves.toEqual(new Set());
  });

  test('without a BroadcastChannel, messages go through localStorage events to the other tabs', () => {
    const items = new Map();
    const windows = [];
    // One origin's localStorage: a write is an event in every other window.
    const storage = (own) => ({
      setItem(key, value) {
        items.set(key, value);
        windows
          .filter((window) => window !== own)
          .forEach((window) => window.fire({ key, newValue: value }));
      },
      removeItem(key) {
        items.delete(key);
        windows
          .filter((window) => window !== own)
          .forEach((window) => window.fire({ key, newValue: null }));
      },
    });
    const makeWindow = () => {
      const listeners = new Set();
      const window = {
        addEventListener: (_, fn) => listeners.add(fn),
        removeEventListener: (_, fn) => listeners.delete(fn),
        fire: (event) => listeners.forEach((fn) => fn(event)),
      };

      windows.push(window);

      return window;
    };
    const first = makeWindow();
    const second = makeWindow();
    const sender = openChannel('alice::alice::d1', {
      storage: storage(first),
      target: first,
    });
    const receiver = openChannel('alice::alice::d1', {
      storage: storage(second),
      target: second,
    });
    const heard = { sender: [], receiver: [] };

    sender.listen((message) => heard.sender.push(message));
    receiver.listen((message) => heard.receiver.push(message));
    sender.post({ type: 'hello', from: 'A' });

    expect(heard).toEqual({
      sender: [],
      receiver: [{ type: 'hello', from: 'A' }],
    });
    // Nothing is left behind in localStorage.
    expect(items.size).toBe(0);
    expect(openChannel('x').connected).toBe(false);
  });
});

describe('the choice', () => {
  const state = {
    tab: 'A',
    pending: 2,
    changedAt: '2026-09-28T10:42:05.000Z',
    heldForTabs: true,
    versions: [
      {
        id: 'tab:B',
        where: 'tab',
        tab: 'B',
        changes: 1,
        changedAt: '2026-09-28T10:40:11.000Z',
      },
      {
        id: 'record:k1',
        where: 'closed',
        key: 'k1',
        changes: 3,
        changedAt: '2026-09-27T17:03:00.000Z',
      },
      {
        id: 'record:k2',
        where: 'closed',
        key: 'k2',
        changes: 1,
        changedAt: null,
      },
    ],
  };

  test('lists each version with its changes and the time of the last', () => {
    expect(
      choiceRows(state, (value) => value.slice(11, 19)).map((row) => [
        row.id,
        row.text,
      ]),
    ).toEqual([
      ['tab:A', 'This tab: 2 changes, last at 10:42:05'],
      ['tab:B', 'Another tab: 1 change, last at 10:40:11'],
      ['record:k1', 'Closed tab 1: 3 changes, last at 17:03:00'],
      ['record:k2', 'Closed tab 2: 1 change'],
    ]);
    expect(choiceRows({ ...state, pending: 0 })[0].text).toBe(
      'This tab: no unsaved changes',
    );
  });

  test('is needed while a send waits, or closed tabs left several versions', () => {
    expect(needsChoice(state)).toBe(true);
    expect(needsChoice({ ...state, heldForTabs: false })).toBe(false);
    expect(needsChoice({ ...state, heldForTabs: false, pending: 0 })).toBe(
      true,
    );
    expect(needsChoice({ ...state, status: 'conflict' })).toBe(false);
    expect(needsChoice({ ...state, versions: [] })).toBe(false);
  });

  function carry(choice, { keepMine = async () => true } = {}) {
    const calls = [];
    const queue = {
      tabOpen: async () => true,
      chooseVersion: (id) => calls.push(['choose', id]),
      rescan: async () => calls.push(['rescan']),
    };

    return applyChoice({
      choice,
      rows: choiceRows(state),
      queue,
      keepMine: async () => {
        calls.push(['keepMine']);

        return keepMine();
      },
      keepClosed: async (version) => {
        calls.push(['keepClosed', version.key]);

        if (version.key === 'k2') {
          throw new Error('offline');
        }
      },
      reopen: async () => calls.push(['reopen']),
    }).then((outcome) => ({ calls, outcome }));
  }

  test("this tab's: the closed tabs' queues are kept as new drafts", async () => {
    const { calls, outcome } = await carry('tab:A');

    expect(calls).toEqual([
      ['choose', 'tab:A'],
      ['keepClosed', 'k1'],
      ['keepClosed', 'k2'],
      ['rescan'],
    ]);
    expect(outcome.kept.map((version) => version.key)).toEqual(['k1']);
    expect(outcome.failed.map((version) => version.key)).toEqual(['k2']);
  });

  test("another tab's: this tab's changes are kept as a new draft", async () => {
    const { calls } = await carry('tab:B');

    expect(calls.map(([name]) => name)).toEqual([
      'choose',
      'keepClosed',
      'keepClosed',
      'rescan',
      'keepMine',
    ]);
  });

  test("a closed tab's: the others are kept, then the draft opens again with it", async () => {
    const { calls } = await carry('record:k1');

    expect(calls).toEqual([
      ['choose', 'record:k1'],
      ['keepClosed', 'k2'],
      ['rescan'],
      ['keepMine'],
      ['reopen'],
    ]);

    // This tab's could not be kept: the draft stays as it is.
    const failed = await carry('record:k1', { keepMine: async () => false });

    expect(failed.calls.map(([name]) => name)).not.toContain('reopen');
  });

  test("a closed tab's queue not chosen is saved as a new draft, and leaves this browser", async () => {
    const api = fakeApi();
    const device = createMemoryStore();
    const key = tabRecordKey(BASE, 'gone');

    await device.put(
      {
        key,
        tab: 'gone',
        actor: 'alice',
        owner: 'alice',
        draftId: 'd1',
        etag: '"1"',
        entries: [
          { id: 'g1', label: 'Edit g1' },
          { id: 'g2', label: 'Edit g2' },
        ],
        queue: [
          { opId: 'g1', kind: 'snapshot', commitId: 'g1', label: 'Edit g1' },
          { opId: 'g2', kind: 'snapshot', commitId: 'g2', label: 'Edit g2' },
        ],
      },
      { write: [edit('g1', 'Lab'), edit('g2', 'Lab')] },
    );

    await forkClosedQueue({
      api,
      store: device,
      actor: 'alice',
      key,
      title: (name) => `${name} (local copy)`,
    });

    expect(api.createDraft).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Lab (local copy)',
        document: expect.objectContaining({
          metadata: expect.objectContaining({ name: 'Lab (local copy)' }),
        }),
        forkOf: 'alice/d1',
      }),
    );
    expect(api.appendSnapshot).toHaveBeenCalledWith(
      'alice',
      'fork-Lab (local copy)',
      expect.objectContaining({
        document: expect.objectContaining({
          metadata: expect.objectContaining({ name: 'Lab (local copy)' }),
        }),
      }),
      '"f1"',
    );
    expect(await device.all()).toEqual([]);

    // Undo and redo alone hold no diagram of their own: nothing to keep.
    await device.put({
      key,
      actor: 'alice',
      owner: 'alice',
      draftId: 'd1',
      entries: [],
      queue: [{ opId: 'c', kind: 'cursor', snapshotId: 's1' }],
    });
    await expect(
      forkClosedQueue({
        api,
        store: device,
        actor: 'alice',
        key,
        title: String,
      }),
    ).resolves.toBeNull();
    expect(await device.all()).toEqual([]);
  });
});

describe('taking a closed tab’s queue', () => {
  test('moves its record, snapshots and unload copy, once', async () => {
    const items = new Map();
    const storage = {
      get length() {
        return items.size;
      },
      key: (index) => [...items.keys()][index] ?? null,
      getItem: (key) => items.get(key) ?? null,
      setItem: (key, value) => items.set(key, value),
      removeItem: (key) => items.delete(key),
    };
    const device = createMemoryStore({ storage });
    const from = tabRecordKey(BASE, 'gone');
    const to = tabRecordKey(BASE, 'me');
    const record = {
      key: from,
      tab: 'gone',
      actor: 'alice',
      owner: 'alice',
      draftId: 'd1',
      updatedAt: 1,
      entries: [{ id: 'e1', label: 'Edit e1' }],
      queue: [{ opId: 'e1', kind: 'snapshot', commitId: 'e1' }],
    };

    await device.put(record, { write: [edit('e1')] });
    // The page kept its last edit in an unload copy as it closed.
    device.keep(
      {
        ...record,
        updatedAt: 2,
        entries: [{ id: 'e1', label: 'Edit e1' }, edit('e2')],
        queue: [
          ...record.queue,
          { opId: 'e2', kind: 'snapshot', commitId: 'e2' },
        ],
      },
      { write: [edit('e2')] },
    );

    const [moved, again] = await Promise.all([
      device.rekey(from, to, { tab: 'me' }),
      device.rekey(from, to, { tab: 'me' }),
    ]);

    expect(moved).toMatchObject({ key: to, tab: 'me' });
    expect(again).toBeUndefined();
    const taken = await device.get(to);

    expect(taken.queue.map((op) => op.opId)).toEqual(['e1', 'e2']);
    expect(taken.entries.map((entry) => entry.snapshot.metadata.name)).toEqual([
      'e1',
      'e2',
    ]);
    expect(await device.get(from)).toBeUndefined();
    expect(items.has(unloadCopyKey(from))).toBe(false);
  });
});
