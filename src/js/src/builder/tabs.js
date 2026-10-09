// Tab coordination: the tabs of this browser with the same draft open.
//
// Each tab keeps its own local queue of a draft (see tabRecordKey in
// idb.js), so two tabs editing one draft never replace each other's
// changes. A tab's id is kept in sessionStorage, so a reload finds the queue
// the page left. A tab holds a Web Lock named after its id while it is
// open: a duplicated tab, which copies sessionStorage, finds the lock taken
// and takes an id of its own, and a queue whose tab holds no lock was left
// by a tab that has closed. The other tabs ask for that lock too, which
// they get once the page has gone, however it went: closed, crashed or
// discarded. Without Web Locks, each page takes a new id, and a queue no
// open tab answers for is taken as a closed tab's: the tabs answer over a
// channel of their own who is open (see presentTabs).
//
// The tabs with one draft open, for one user, tell each other over a
// BroadcastChannel (where there is none, localStorage events) whether it is
// open in their editor, how many of their changes the server does not have
// yet and when they made the last one. The editor warns while another tab
// has the draft open.
//
// Only one version of a draft's unsaved changes can be saved to it. While
// another tab, or a queue a closed tab left, holds changes the server does
// not have, a queue with changes of its own does not send them: the user
// chooses which version to save (see BuilderTabsDialog.vue). Every tab
// hears the choice. The versions not chosen are saved as new drafts, as a
// conflict's Save my history as a new draft does (see applyChoice); a role
// that cannot make drafts is offered Download first.

import { count } from './announce.js';
import { createAutosave, replayHistory } from './autosave.js';
import { draftKey, tabRecordKey } from './idb.js';
import { newId } from './ids.js';
import { setDocumentInfo } from './model.js';
import { pageStorage } from './storage.js';

/** The sessionStorage key of this tab's id. */
export const TAB_ID_KEY = 'phenix.builder.tab';

// The localStorage key the channel's messages go through where there is no
// BroadcastChannel.
const CHANNEL_PREFIX = 'phenix.builder.channel.';

const LOCK_PREFIX = 'phenix-builder-tab:';

// How long a page waits for the lock of the id sessionStorage kept. A
// reloaded page's previous page lets go of it at once; the tab a duplicated
// tab copied it from never does.
export const CLAIM_WAIT_MS = 1500;

// How long, without Web Locks, a tab waits for the others to answer before
// it takes a queue no tab answered for as a closed tab's.
export const ANSWER_WAIT_MS = 500;

// The channel every tab with an id answers on, without Web Locks, when
// another asks which tabs are open (see presentTabs).
const PRESENCE_CHANNEL = 'tabs';

/**
 * @param {string} tab a tab's id
 * @returns {string} the name of the Web Lock the tab holds while open
 */
export function lockName(tab) {
  return `${LOCK_PREFIX}${tab}`;
}

function readId(storage) {
  try {
    return storage?.getItem(TAB_ID_KEY) || '';
  } catch {
    return '';
  }
}

function writeId(storage, id) {
  try {
    storage?.setItem(TAB_ID_KEY, id);
  } catch {
    // Blocked storage keeps no id: the next page takes another.
  }
}

// Takes the lock named after `tab` and holds it while the page is open.
// Resolves whether it was taken within `waitMs`; with none, only if it was
// free.
function holdLock(locks, tab, waitMs) {
  return new Promise((resolve) => {
    const controller =
      waitMs > 0 && typeof AbortController === 'function'
        ? new AbortController()
        : null;
    const timer = controller
      ? setTimeout(() => controller.abort(), waitMs)
      : null;
    const options = controller
      ? { signal: controller.signal }
      : { ifAvailable: true };

    Promise.resolve()
      .then(() =>
        locks.request(lockName(tab), options, (lock) => {
          clearTimeout(timer);
          resolve(Boolean(lock));

          // Held until the page goes.
          return lock ? new Promise(() => {}) : undefined;
        }),
      )
      .catch(() => {
        clearTimeout(timer);
        resolve(false);
      });
  });
}

/**
 * Finds this tab's id (see the header): the one sessionStorage kept, unless
 * another open tab holds its lock, or a new one.
 *
 * @param {object} [options] storage (sessionStorage), locks (Web Locks),
 *   random, waitMs
 * @returns {Promise<string>}
 */
export async function claimTab({
  storage = null,
  locks = null,
  random = newId,
  waitMs = CLAIM_WAIT_MS,
} = {}) {
  if (!locks?.request) {
    return random();
  }

  const kept = readId(storage);

  if (kept && (await holdLock(locks, kept, waitMs))) {
    return kept;
  }

  const id = random();

  writeId(storage, id);
  await holdLock(locks, id, 0);

  return id;
}

/**
 * Opens the channel the tabs with a draft open talk over: a
 * BroadcastChannel, or where there is none, localStorage events, which
 * reach the other tabs of the same origin. Where neither works, messages
 * go nowhere (connected is false).
 *
 * @param {string} name
 * @param {object} [options] Channel (BroadcastChannel), storage
 *   (localStorage) and target (window), for tests
 * @returns {{connected: boolean, post: Function, listen: Function,
 *   close: Function}}
 */
export function openChannel(name, { Channel, storage, target } = {}) {
  if (typeof Channel === 'function') {
    const channel = new Channel(`phenix-builder:${name}`);

    return {
      connected: true,
      post(message) {
        try {
          channel.postMessage(message);
        } catch {
          // A closed channel sends nothing.
        }
      },
      listen(fn) {
        const heard = (event) => fn(event.data);

        channel.addEventListener('message', heard);

        return () => channel.removeEventListener('message', heard);
      },
      close() {
        channel.close();
      },
    };
  }

  if (storage && target?.addEventListener) {
    const key = `${CHANNEL_PREFIX}${name}`;
    const listeners = new Set();
    // A storage event reaches every other tab, not the one that wrote.
    const heard = (event) => {
      if (event.key !== key || !event.newValue) {
        return;
      }

      let message;

      try {
        message = JSON.parse(event.newValue).message;
      } catch {
        return;
      }

      listeners.forEach((fn) => fn(message));
    };

    target.addEventListener('storage', heard);

    return {
      connected: true,
      post(message) {
        try {
          // The nonce makes each write a change, so each is heard.
          storage.setItem(key, JSON.stringify({ message, nonce: newId() }));
          storage.removeItem(key);
        } catch {
          // Blocked storage sends nothing.
        }
      },
      listen(fn) {
        listeners.add(fn);

        return () => listeners.delete(fn);
      },
      close() {
        target.removeEventListener('storage', heard);
        listeners.clear();
      },
    };
  }

  return { connected: false, post() {}, listen: () => () => {}, close() {} };
}

// A queue holds changes it is not sending: offline, failed or waiting for
// the user's choice. One sending them, or that can no longer send them to
// the draft (a conflict, or no access), does not stand in another's way.
function stuck(state) {
  return (
    state.pending > 0 &&
    !['saving', 'conflict', 'forbidden'].includes(state.status)
  );
}

function stateOf(value = {}) {
  return {
    pending: value.pending || 0,
    changedAt: value.changedAt || null,
    status: value.status || 'idle',
    open: value.open !== false,
  };
}

/**
 * What a queue knows of the other tabs with its draft open, and of the
 * queues closed tabs left of it (see the header).
 *
 * @param {object} options actor, owner, draftId; tab: this tab's id;
 *   state: this tab's queue state as it opens the draft (its changes the
 *   server does not have), which its first message says; channel
 *   (openChannel); locks (Web Locks), if any; records: () => the user's
 *   local records of the draft but this tab's; onChange: called when the
 *   other tabs, the closed tabs' queues or the user's choice change;
 *   target: the window, for pagehide and pageshow; waitMs; now, setTimeout
 *   and clearTimeout, for tests
 * @returns {object} coordinator
 */
export function createTabCoordinator({
  actor,
  owner,
  draftId,
  tab,
  state = {},
  channel = openChannel(draftKey(actor, owner, draftId)),
  locks = null,
  records = async () => [],
  onChange = () => {},
  target = null,
  waitMs = ANSWER_WAIT_MS,
  now = () => Date.now(),
  setTimeout: setTimer = (fn, ms) => setTimeout(fn, ms),
  clearTimeout: clearTimer = (handle) => clearTimeout(handle),
}) {
  const self = `tab:${tab}`;
  // The other tabs, by id, with their states, and those that left the
  // draft: a tab closing says so before it lets go of its lock.
  const peers = new Map();
  const gone = new Set();
  // The queues closed tabs left, with changes, as versions.
  let closed = [];
  // The version the user chose, `tab:<id>` or `record:<key>`, until there
  // is nothing left to choose between.
  let chosen = '';
  let mine = stateOf(state);
  let posted = '';
  let posting = null;
  let ended = false;
  const startedAt = now();
  // Liveness checks waiting for a tab to answer, by its id.
  const waiting = new Map();
  // The requests for the other tabs' locks, by id (see watch).
  const watches = new Map();

  function post(message) {
    channel.post({ ...message, from: tab });
  }

  function sayHere() {
    posted = JSON.stringify(mine);
    post({ type: 'here', state: mine });
  }

  // Tells the other tabs this tab's state once the current task ends, so a
  // state that lasts no longer than it is never heard.
  function schedule() {
    if (posting !== null || ended) {
      return;
    }

    posting = setTimer(() => {
      posting = null;

      if (!ended && JSON.stringify(mine) !== posted) {
        sayHere();
      }
    }, 0);
  }

  function versions() {
    return [
      ...[...peers]
        .filter(([, peer]) => stuck(peer))
        .map(([id, peer]) => ({
          id: `tab:${id}`,
          where: 'tab',
          tab: id,
          changes: peer.pending,
          changedAt: peer.changedAt,
        })),
      ...closed.map((version) => ({ ...version })),
    ];
  }

  function changed() {
    if (versions().length === 0) {
      chosen = '';
    }

    onChange();
  }

  function answered(id) {
    (waiting.get(id) || []).forEach((settle) => settle(true));
    waiting.delete(id);
  }

  // A tab heard from is open: its queue is no closed tab's (a reload says
  // bye, then hello again).
  function heard(id) {
    gone.delete(id);
    answered(id);

    if (closed.some((version) => version.tab === id)) {
      closed = closed.filter((version) => version.tab !== id);
    }
  }

  // Resolves once the tab with this id has been heard from, or after `ms`:
  // whether it was.
  function hear(id, ms) {
    if (peers.has(id) || ms <= 0) {
      return Promise.resolve(peers.has(id));
    }

    return new Promise((resolve) => {
      const timer = setTimer(() => resolve(peers.has(id)), ms);

      waiting.set(id, [
        ...(waiting.get(id) || []),
        (value) => {
          clearTimer(timer);
          resolve(value);
        },
      ]);
    });
  }

  // The tab with this id has gone. Its changes are a closed tab's now:
  // until the records are read again, what it last said of them stands for
  // them, so there is never a moment with nothing to choose between.
  function left(id) {
    const peer = peers.get(id);

    unwatch(id);
    peers.delete(id);
    gone.add(id);

    if (!peer) {
      return;
    }

    if (peer.pending > 0 && !closed.some((version) => version.tab === id)) {
      const key = tabRecordKey(draftKey(actor, owner, draftId), id);

      closed = [
        ...closed,
        {
          id: `record:${key}`,
          where: 'closed',
          key,
          tab: id,
          changes: peer.pending,
          changedAt: peer.changedAt,
        },
      ];
    }

    changed();
    scan();
  }

  // Asks for the lock of a tab just heard from, which is free only once
  // that page has gone, however it went: one that crashes or is discarded
  // says no bye. A tab that holds no lock is left to its bye.
  async function watch(id) {
    if (
      !locks?.request ||
      !locks.query ||
      watches.has(id) ||
      typeof AbortController !== 'function'
    ) {
      return;
    }

    const controller = new AbortController();

    watches.set(id, controller);

    try {
      const { held = [] } = await locks.query();

      if (
        controller.signal.aborted ||
        !held.some((lock) => lock.name === lockName(id))
      ) {
        throw new Error('no lock');
      }

      await locks.request(lockName(id), { signal: controller.signal }, () => {
        // Let go at once: a reloaded page takes it next.
        if (watches.get(id) === controller && !ended) {
          left(id);
        }
      });
    } catch {
      // Given up, or no lock to wait for.
    } finally {
      if (watches.get(id) === controller) {
        watches.delete(id);
      }
    }
  }

  function unwatch(id) {
    watches.get(id)?.abort();
    watches.delete(id);
  }

  const stopListening = channel.listen((message) => {
    if (ended || !message || !message.from || message.from === tab) {
      return;
    }

    switch (message.type) {
      case 'hello':
        peers.set(message.from, stateOf(message.state));
        sayHere();
        heard(message.from);
        watch(message.from);
        changed();
        break;
      case 'here':
        peers.set(message.from, stateOf(message.state));
        heard(message.from);
        watch(message.from);
        changed();
        break;
      case 'bye':
        left(message.from);
        break;
      case 'chosen':
        chosen = message.keep || '';
        changed();
        break;
      case 'scan':
        scan();
        break;
      default:
    }
  });

  // A page put in the back-forward cache is gone until it comes back.
  const hide = () => post({ type: 'bye' });
  const show = (event) => {
    if (event?.persisted) {
      post({ type: 'hello', state: mine });
    }
  };

  target?.addEventListener?.('pagehide', hide);
  target?.addEventListener?.('pageshow', show);

  post({ type: 'hello', state: mine });

  /**
   * Whether the tab with this id is open. Its lock says so, where there
   * are Web Locks, even for a tab this tab has heard from: one that went
   * without a word is not.
   *
   * @param {string} id
   * @returns {Promise<boolean>}
   */
  async function live(id) {
    if (!id) {
      return false;
    }

    if (id === tab) {
      return true;
    }

    if (gone.has(id)) {
      return false;
    }

    if (locks?.query) {
      try {
        const { held = [] } = await locks.query();

        return held.some((lock) => lock.name === lockName(id));
      } catch {
        // Asked as below.
      }
    }

    if (peers.has(id)) {
      return true;
    }

    // An open tab answers the hello this tab said as it opened the draft.
    return channel.connected ? hear(id, waitMs - (now() - startedAt)) : false;
  }

  /**
   * Reads the queues closed tabs left of the draft again.
   *
   * @returns {Promise<object[]>} them, as versions
   */
  async function scan() {
    const found = await records().catch(() => []);
    const next = [];

    for (const record of found) {
      if (!record?.queue?.length) {
        continue;
      }

      // An open tab's changes are its own; what it says of them comes in
      // answer to this tab's hello, before this tab sends anything.
      if (await live(record.tab)) {
        if (channel.connected) {
          await hear(record.tab, waitMs - (now() - startedAt));
        }

        continue;
      }

      next.push({
        id: `record:${record.key}`,
        where: 'closed',
        key: record.key,
        tab: record.tab || '',
        changes: record.queue.length,
        changedAt: record.changedAt || record.updatedAt || null,
      });
    }

    if (!ended) {
      closed = next;
      changed();
    }

    return next;
  }

  return {
    /** @returns {object[]} the other tabs with the draft open */
    peers() {
      return [...peers].map(([id, peer]) => ({ tab: id, ...peer }));
    },

    /**
     * @returns {object[]} the other versions of the draft's unsaved
     *   changes: other tabs' (where 'tab') and closed tabs' ('closed'),
     *   each with its id, number of changes and when the last was made
     */
    versions,

    /**
     * Whether a queue with `pending` changes waits for the user to choose
     * which version to save.
     *
     * @param {number} pending
     * @returns {boolean}
     */
    blocked(pending) {
      return pending > 0 && chosen !== self && versions().length > 0;
    },

    /** @returns {boolean} whether the user chose another version */
    lost() {
      return Boolean(chosen) && chosen !== self;
    },

    /**
     * Tells the other tabs this queue's state (see schedule).
     *
     * @param {object} state queue state
     */
    publish(state) {
      mine = stateOf({ ...state, open: mine.open });
      schedule();
    },

    /**
     * @param {boolean} open whether the draft is open in this tab's editor
     */
    setOpen(open) {
      mine = { ...mine, open };
      schedule();
    },

    /**
     * Tells every tab the version the user chose.
     *
     * @param {string} id
     */
    choose(id) {
      chosen = id;
      post({ type: 'chosen', keep: id });
      changed();
    },

    scan,
    live,

    /** Has the other tabs read the closed tabs' queues again. */
    recordsChanged() {
      post({ type: 'scan' });
    },

    /** Leaves the other tabs: this tab no longer has the draft. */
    close() {
      if (ended) {
        return;
      }

      ended = true;
      clearTimer(posting);
      post({ type: 'bye' });
      stopListening();
      channel.close();
      target?.removeEventListener?.('pagehide', hide);
      target?.removeEventListener?.('pageshow', show);
      waiting.forEach((settles) => settles.forEach((settle) => settle(false)));
      waiting.clear();
      [...watches.keys()].forEach(unwatch);
    },
  };
}

/**
 * Answers, for the page's life, the tabs that ask over `channel` which
 * tabs are open (see presentTabs).
 *
 * @param {object} channel openChannel()
 * @param {string} tab this tab's id
 */
export function answerPresence(channel, tab) {
  channel.listen((message) => {
    if (message?.type === 'who' && message.from !== tab) {
      channel.post({ type: 'here', from: tab });
    }
  });
}

/**
 * The ids of the other open tabs that answer over `channel` within
 * `waitMs` (see answerPresence): which tabs are open, where there are no
 * Web Locks to say. The channel is closed afterwards. Where no channel
 * works, none answers.
 *
 * @param {object} options channel (openChannel), self (this tab's id, if
 *   it has one), waitMs; setTimeout, for tests
 * @returns {Promise<Set<string>>}
 */
export function presentTabs({
  channel,
  self = '',
  waitMs = ANSWER_WAIT_MS,
  setTimeout: setTimer = (fn, ms) => setTimeout(fn, ms),
}) {
  const found = new Set();

  if (!channel.connected) {
    channel.close();

    return Promise.resolve(found);
  }

  const stop = channel.listen((message) => {
    if (message?.type === 'here' && message.from && message.from !== self) {
      found.add(message.from);
    }
  });

  channel.post({ type: 'who', from: self });

  return new Promise((resolve) => {
    setTimer(() => {
      stop();
      channel.close();
      resolve(found);
    }, waitMs);
  });
}

// The page's own Web Locks, BroadcastChannel and storages. Unit tests run
// outside a page and coordinate nothing unless they pass fakes.
function page() {
  if (typeof window === 'undefined') {
    return {};
  }

  return {
    locks: window.navigator?.locks?.request ? window.navigator.locks : null,
    Channel: window.BroadcastChannel,
    localStorage: pageStorage('localStorage'),
    sessionStorage: pageStorage('sessionStorage'),
    target: window,
  };
}

let claimed = null;

// The presence channel over which, without Web Locks, other tabs ask
// which are open.
function presenceChannel() {
  const { Channel, localStorage, target } = page();

  return openChannel(PRESENCE_CHANNEL, {
    Channel,
    storage: localStorage,
    target,
  });
}

/**
 * The other tabs of this browser, for the Builder's save queues (see
 * createAutosave's tabs option).
 */
export const builderTabs = {
  /**
   * This tab's id. Without Web Locks, the tab answers from then on which
   * tabs are open (see presentTabs).
   *
   * @returns {Promise<string>}
   */
  tab() {
    const { sessionStorage, locks } = page();

    if (!claimed) {
      claimed = claimTab({ storage: sessionStorage, locks });

      if (!locks) {
        claimed.then((id) => answerPresence(presenceChannel(), id));
      }
    }

    return claimed;
  },

  /**
   * @returns {Promise<Set<string>>} the ids of this browser's other open
   *   tabs: those that hold a lock (see claimTab), or where there are no
   *   Web Locks, those that answer over the presence channel
   */
  async others() {
    const { locks } = page();
    const self = claimed ? await claimed : '';

    if (!locks?.query) {
      return presentTabs({ channel: presenceChannel(), self });
    }

    const { held = [] } = await locks.query();

    return new Set(
      held
        .map((lock) => lock.name || '')
        .filter((name) => name.startsWith(LOCK_PREFIX))
        .map((name) => name.slice(LOCK_PREFIX.length))
        .filter((id) => id !== self),
    );
  },

  /**
   * @param {object} draft actor, owner, draftId, tab, state, records,
   *   onChange
   * @returns {object} coordinator (see createTabCoordinator)
   */
  open(draft) {
    const { locks, Channel, localStorage, target } = page();

    return createTabCoordinator({
      ...draft,
      locks,
      target,
      channel: openChannel(draftKey(draft.actor, draft.owner, draft.draftId), {
        Channel,
        storage: localStorage,
        target,
      }),
    });
  },
};

/**
 * What the editor says of the other tabs with the draft open and the
 * queues closed tabs left of it, or '' when there are none.
 *
 * @param {object} state queue state
 * @returns {string}
 */
export function tabsNotice(state) {
  const open = (state?.tabs || []).filter((peer) => peer.open);
  const closedChanges = (state?.versions || [])
    .filter((version) => version.where === 'closed')
    .reduce((sum, version) => sum + version.changes, 0);
  const parts = [];

  // Tabs where the draft was closed while its changes were being saved.
  const saving = (state?.tabs || []).filter(
    (peer) => !peer.open && peer.pending > 0,
  );

  if (open.length > 0) {
    const pending = open.reduce((sum, peer) => sum + peer.pending, 0);
    const where =
      open.length === 1 ? 'another tab' : `${open.length} other tabs`;
    const what =
      pending === 0
        ? `, where all changes are saved`
        : `, which ${open.length === 1 ? 'has' : 'have'} ${count(pending, 'change')} not saved yet`;

    parts.push(
      `This draft is also open in ${where}${what}.`,
      open.length === 1
        ? 'Changes made in both tabs cannot both be kept.'
        : 'Changes made in more than one tab cannot all be kept.',
    );
  }

  if (saving.length > 0) {
    const pending = saving.reduce((sum, peer) => sum + peer.pending, 0);

    parts.push(
      `${saving.length === 1 ? 'Another tab has' : `${saving.length} other tabs have`} ${count(pending, 'change')} to this draft not saved yet.`,
    );
  }

  if (closedChanges > 0) {
    parts.push(
      `A closed tab left ${count(closedChanges, 'change')} to this draft that ${closedChanges === 1 ? 'is' : 'are'} not saved yet.`,
    );
  }

  if (state?.heldForTabs && state.pending > 0) {
    parts.push(
      `Your ${count(state.pending, 'change')} here ${state.pending === 1 ? 'is' : 'are'} not sent until you choose which to save.`,
    );
  }

  return parts.join(' ');
}

/**
 * Whether the user has to choose which version of the draft's unsaved
 * changes to save: a send waits for it, or closed tabs left more than one
 * queue and this tab has no changes of its own.
 *
 * @param {object} state queue state
 * @returns {boolean}
 */
export function needsChoice(state) {
  const versions = state?.versions || [];

  if (state?.status === 'conflict' || versions.length === 0) {
    return false;
  }

  return (
    Boolean(state.heldForTabs && state.pending > 0) ||
    (state.pending === 0 &&
      versions.filter((version) => version.where === 'closed').length > 1)
  );
}

/**
 * The versions the choice lists: this tab's first, then the other tabs',
 * then the closed tabs'. Each is named by where it is, numbered when there
 * are several of a kind.
 *
 * @param {object} state queue state
 * @param {Function} [time] formats a change's time
 * @returns {{id: string, where: string, name: string, changes: number,
 *   changedAt: string|null, text: string}[]}
 */
export function choiceRows(state, time = (value) => value || '') {
  const others = state?.versions || [];
  const numbered = (where, one, many) => {
    const kind = others.filter((version) => version.where === where);

    return kind.map((version, index) => ({
      ...version,
      name: kind.length === 1 ? one : `${many} ${index + 1}`,
    }));
  };
  const rows = [
    {
      id: `tab:${state?.tab || ''}`,
      where: 'this',
      name: 'This tab',
      changes: state?.pending || 0,
      changedAt: state?.pending ? state.changedAt : null,
    },
    ...numbered('tab', 'Another tab', 'Other tab'),
    ...numbered('closed', 'A closed tab', 'Closed tab'),
  ];

  return rows.map((row) => {
    const when = row.changes > 0 && row.changedAt ? time(row.changedAt) : '';

    return {
      ...row,
      text:
        row.changes === 0
          ? `${row.name}: no unsaved changes`
          : `${row.name}: ${count(row.changes, 'change')}${when ? `, last at ${when}` : ''}`,
    };
  });
}

/**
 * Saves the queue a closed tab left of a draft as a new draft of the
 * user's, as a conflict's Save my history as a new draft does, and removes
 * it from this browser. Its history is the diagrams its operations made,
 * in order (see replayHistory), each named `title`.
 *
 * @param {object} options api, store, actor; key: the queue's record;
 *   title: (name) => the new draft's title, from the diagram's name
 * @returns {Promise<object|null>} the new draft, or null when the queue
 *   holds no diagram of its own (undo and redo to snapshots the server
 *   keeps), which is removed
 */
export async function forkClosedQueue({ api, store, actor, key, title }) {
  const found = await store.get(key);

  if (!found) {
    return null;
  }

  const { entries, index } = replayHistory(
    [],
    -1,
    found.queue || [],
    found.entries || [],
  );

  if (entries.length === 0) {
    await store.remove(key);

    return null;
  }

  const name = title(entries[index]?.snapshot?.metadata?.name);
  const queue = createAutosave({ api, store, actor });

  try {
    await queue.attach({ owner: found.owner, draftId: found.draftId, key });

    return await queue.forkLocalHistory({
      title: name,
      entries: entries.map((entry) => ({
        ...entry,
        snapshot: setDocumentInfo(entry.snapshot, { name }),
      })),
      index,
    });
  } finally {
    queue.dispose();
  }
}

/**
 * Carries out the user's choice of which version of a draft's unsaved
 * changes to save (see the header). Every tab hears it: a tab whose
 * changes were not chosen keeps them as a new draft. Each queue a closed
 * tab left that was not chosen is kept so (keepClosed), and so are this
 * tab's own changes when not chosen (keepMine), which leaves the draft. A
 * closed tab's queue that was chosen is then taken, as the draft opens
 * again (reopen). Another tab's that has gone since it was listed is not
 * carried out: its changes are a closed tab's now, and the list changes.
 *
 * @param {object} options
 * @param {string} options.choice the chosen version's id
 * @param {object[]} options.rows choiceRows()
 * @param {object} options.queue this tab's save queue
 * @param {() => Promise<boolean>} options.keepMine
 * @param {(version: object) => Promise<*>} options.keepClosed
 * @param {() => Promise<*>} options.reopen
 * @returns {Promise<{kept: object[], failed: object[], gone?: boolean}>}
 *   the closed tabs' queues kept, and those that could not be; gone: the
 *   chosen tab had gone, and nothing was done
 */
export async function applyChoice({
  choice,
  rows,
  queue,
  keepMine,
  keepClosed,
  reopen,
}) {
  const mine = rows.find((row) => row.where === 'this');
  const picked = rows.find((row) => row.id === choice);
  const kept = [];
  const failed = [];

  if (picked?.where === 'tab' && !(await queue.tabOpen(picked.tab))) {
    await queue.rescan();

    return { kept, failed, gone: true };
  }

  // This tab's queue no longer sends, once told.
  queue.chooseVersion(choice);

  for (const version of rows) {
    if (version.where !== 'closed' || version.id === choice) {
      continue;
    }

    try {
      await keepClosed(version);
      kept.push(version);
    } catch {
      failed.push(version);
    }
  }

  await queue.rescan();

  if (mine && mine.id !== choice && mine.changes > 0 && !(await keepMine())) {
    return { kept, failed };
  }

  if (rows.some((row) => row.id === choice && row.where === 'closed')) {
    await reopen();
  }

  return { kept, failed };
}
