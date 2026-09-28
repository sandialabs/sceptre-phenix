// What Builder Flow keeps in this browser, and what logout clears of it.
//
// The Builder keeps drafts that are not saved yet in IndexedDB, and in
// localStorage under phenix.builder.* the viewer's preferences (theme, pane
// widths, minimap size, shortcuts, settings), the recent commands, which
// name drafts, their owners and nodes by id, and the unload copies of the
// drafts whose edits IndexedDB had not stored when a page was left (see
// idb.js); once loaded, its modules also hold the drafts listed for the
// user and the open diagram in memory. What belongs to the user may not outlive the
// session on a shared workstation, so logout clears it, whether or not the
// Builder is open, and so does the next sign-in of another user when the
// browser was closed without logging out. The preferences say nothing about
// the user or their work, and stay for this browser.
//
// This module is loaded with the app, so it stays small: the Builder's
// modules register what they hold in memory when they are first loaded, and
// a Builder that never opened has nothing in memory to clear.
//
// Clearing also deletes edits the server never received, so logout first
// asks this module for them (see utils/logout.js): the open draft's, which
// the Builder view registers, those of drafts closed for the drafts whose
// queues still send them (see createBackgroundSaves in leave.js), and
// those queued in IndexedDB, or in an unload copy, for any other draft,
// which are there whether or not the Builder is open.
//
// Signing in again without leaving the Builder (see signin.js) clears
// nothing: the queues send what the server refused meanwhile (see
// resumeBuilderSaves).

import { clearBuilderDatabase, createDraftStore } from './idb.js';

const BUILDER_STORAGE_PREFIX = 'phenix.builder.';

/**
 * The user the Builder data in this browser belongs to, written at sign-in.
 * It is not a preference, so logout clears it with the rest.
 */
export const BUILDER_USER_KEY = 'phenix.builder.user';

/**
 * The phenix.builder.* keys logout leaves in place: preferences of this
 * browser, which hold no names, ids or content. They are listed here rather
 * than registered by the modules that own them, so a Builder that never
 * opened keeps them too. Any other key under the prefix is cleared, so a
 * new one stays private until it is added here as a preference.
 */
export const BUILDER_PREFERENCE_KEYS = Object.freeze([
  'phenix.builder.minimap', // panes.js, the minimap's size
  'phenix.builder.panes', // panes.js, the side columns' widths
  'phenix.builder.settings', // settings.js, the Settings dialog's choices
  'phenix.builder.shortcuts', // keymap.js, custom keys, single-key switch
  'phenix.builder.theme', // theme.js
]);

const resets = new Set();

/**
 * Registers what a Builder module does when the session ends: forget what
 * it holds in memory for the user. Storage is already cleared when it runs.
 *
 * @param {() => void} reset
 * @returns {() => void} unregisters it
 */
export function onBuilderSessionEnd(reset) {
  resets.add(reset);

  return () => resets.delete(reset);
}

// Reading a storage throws where site data is blocked.
function storageOf(name) {
  try {
    return globalThis[name] || null;
  } catch {
    return null;
  }
}

/**
 * Removes every phenix.builder.* key but the preferences from a storage.
 *
 * @param {Storage|null} storage
 */
function removeBuilderKeys(storage) {
  try {
    const keys = [];

    for (let index = 0; index < (storage?.length || 0); index += 1) {
      const key = storage.key(index);

      if (
        key?.startsWith(BUILDER_STORAGE_PREFIX) &&
        !BUILDER_PREFERENCE_KEYS.includes(key)
      ) {
        keys.push(key);
      }
    }

    keys.forEach((key) => storage.removeItem(key));
  } catch {
    // Blocked storage holds nothing of ours.
  }
}

/**
 * Ends the Builder session: removes its keys but the preferences from
 * localStorage and sessionStorage, resets what its modules hold in memory
 * for the user, and deletes every local draft record in IndexedDB. The
 * first two happen before it returns, so a navigation that follows cannot
 * show the previous user's data.
 *
 * @param {object} [options] localStorage, sessionStorage and clearDatabase,
 *   for tests
 * @returns {Promise<boolean>} whether the local draft records are gone
 */
export function endBuilderSession({
  localStorage = storageOf('localStorage'),
  sessionStorage = storageOf('sessionStorage'),
  clearDatabase = clearBuilderDatabase,
} = {}) {
  removeBuilderKeys(localStorage);
  removeBuilderKeys(sessionStorage);

  for (const reset of resets) {
    try {
      reset();
    } catch (error) {
      console.error('Could not reset Builder Flow on logout.', error);
    }
  }

  return clearDatabase();
}

/**
 * Starts the Builder session of the user signing in. What another user left
 * in this browser, by closing it without logging out, is cleared first, as
 * logout would have (see endBuilderSession): the preferences stay. Data no
 * user is named for is cleared too. The local drafts database waits for the
 * clearing (see idb.js), so the Builder cannot read the previous user's
 * drafts, however soon it loads.
 *
 * @param {string} username the user signing in
 * @param {object} [options] localStorage, sessionStorage and clearDatabase,
 *   for tests
 * @returns {Promise<boolean>|null} the clearing, or null when the data was
 *   this user's already
 */
export function startBuilderSession(username, options = {}) {
  const { localStorage = storageOf('localStorage') } = options;
  let previous = null;

  try {
    previous = localStorage?.getItem(BUILDER_USER_KEY) ?? null;
  } catch {
    // Blocked storage names no one, and holds nothing of ours.
  }

  const cleared =
    previous === username
      ? null
      : endBuilderSession({ ...options, localStorage });

  try {
    localStorage?.setItem(BUILDER_USER_KEY, username);
  } catch {
    // The next sign-in clears everything again.
  }

  return cleared;
}

// How long a logout waits for the changes it sends before it warns.
export const LOGOUT_SEND_WAIT_MS = 3000;

// The draft open in the Builder, while there is one (see registerOpenDraft).
let openDraft = null;

/**
 * Registers the draft open in the Builder view, so logout can save what the
 * Inspector holds unapplied, as leaving does (see leave.js), and count and
 * send what the server does not have yet.
 *
 * @param {object} draft store: the Builder store; editing: () => whether a
 *   draft is open; saveUnapplied: () => the Inspector edits it cannot apply,
 *   or null; describe: (unapplied) => a sentence naming them
 * @returns {() => void} unregisters it
 */
export function registerOpenDraft(draft) {
  openDraft = draft;

  return () => {
    if (openDraft === draft) {
      openDraft = null;
    }
  };
}

// The open draft, when it can be changed.
function editedDraft() {
  return openDraft?.editing() && !openDraft.store.readOnly ? openDraft : null;
}

// The save queues of drafts closed for the drafts, still sending (see
// registerQueue).
const backgroundQueues = new Set();

/**
 * Registers the save queue of a draft closed for the drafts, which goes on
 * sending (see createBackgroundSaves in leave.js), so logout counts and
 * sends its changes with it rather than by a queue of its own.
 *
 * @param {object} queue
 * @returns {() => void} unregisters it
 */
export function registerQueue(queue) {
  backgroundQueues.add(queue);

  return () => backgroundQueues.delete(queue);
}

// The open draft's queue: its record's key, and how many changes it holds.
function openQueue(draft) {
  const record = draft?.store.autosave?.record;

  return { key: record?.key, pending: record?.queue?.length || 0 };
}

// The queues of drafts closed for the drafts, with their records.
function closedQueues() {
  return [...backgroundQueues].filter((queue) => queue.record);
}

// The records in IndexedDB holding changes of `username`'s not sent yet,
// but those the Builder holds the queues of (`skip`, their keys).
async function queuedRecords(draftStore, username, skip) {
  const records = await draftStore.all().catch(() => []);

  return records.filter(
    (record) =>
      record.actor === username &&
      !skip.includes(record.key) &&
      (record.queue?.length || 0) > 0,
  );
}

// The keys of the records the Builder holds the queues of.
function heldKeys(open, closed) {
  return [open.key, ...closed.map((queue) => queue.record.key)].filter(Boolean);
}

// Sends each record's queue, in order, by a queue of its own bound to the
// record; a conflict or a refusal leaves it queued, as it would in the
// Builder. The modules that send are loaded only now: offline they may not
// load, and nothing is sent.
async function sendRecords(records, username, draftStore) {
  const [{ createAutosave }, { builderApi }] = await Promise.all([
    import('./autosave.js'),
    import('./api.js'),
  ]);

  await Promise.all(
    records.map(async (record) => {
      const queue = createAutosave({
        api: builderApi,
        store: draftStore,
        actor: username,
      });

      try {
        await queue.attach({
          owner: record.owner,
          draftId: record.draftId,
          key: record.key,
        });
        await queue.flush();
      } finally {
        queue.dispose();
      }
    }),
  );
}

// The ids of this browser's other open tabs, which send their own queues
// once the user chooses which changes to save: those holding a Web Lock,
// or where there are no Web Locks, those that answer over a
// BroadcastChannel (see others in tabs.js). Loaded only now, as the
// modules that send are.
async function otherOpenTabs() {
  const { builderTabs } = await import('./tabs.js');

  return builderTabs.others();
}

// Sends the queued records no other open tab holds (see sendRecords).
async function sendOwnRecords(records, username, draftStore, send, openTabs) {
  const others = await openTabs();
  const own = records.filter(
    (record) => !record.tab || !others.has(record.tab),
  );

  if (own.length > 0) {
    await send(own, username, draftStore);
  }
}

// Settles once `work` does, or after `ms`; never rejects.
function within(work, ms) {
  let timer;

  return Promise.race([
    Promise.resolve(work).catch(() => null),
    new Promise((resolve) => {
      timer = setTimeout(resolve, ms);
    }),
  ]).finally(() => clearTimeout(timer));
}

/**
 * What the server does not have of `username`'s Builder work: the changes
 * queued in this browser, the open draft's and every other draft's, and
 * the Inspector edits that cannot be applied. The Inspector edits that can
 * be are saved first, as leaving saves them. With `send`, the queued
 * changes are sent first, waiting for them at most `wait`, but those
 * another open tab holds: that tab sends them.
 *
 * @param {object} options username, send, wait; draftStore, sendQueued and
 *   openTabs, for tests
 * @returns {Promise<{changes: number, unapplied: string, drafts: object[]}>}
 *   unapplied: a sentence naming the Inspector edits, or ''; drafts: those
 *   Export can save, as {key, name} (see draftExport)
 */
export async function unsentBuilderWork({
  username,
  send = false,
  wait = LOGOUT_SEND_WAIT_MS,
  draftStore = createDraftStore(),
  sendQueued = sendRecords,
  openTabs = otherOpenTabs,
} = {}) {
  const draft = editedDraft();
  const blocked = draft?.saveUnapplied() || null;

  if (send) {
    const open = openQueue(draft);
    const closed = closedQueues();
    const records = await queuedRecords(
      draftStore,
      username,
      heldKeys(open, closed),
    );

    await within(
      Promise.all([
        open.pending > 0 ? draft.store.saveNow() : null,
        ...closed.map((queue) =>
          queue.record.queue.length > 0 ? queue.flush() : null,
        ),
        records.length > 0
          ? sendOwnRecords(records, username, draftStore, sendQueued, openTabs)
          : null,
      ]),
      wait,
    );
  }

  const open = openQueue(draft);
  const closed = closedQueues();
  const records = await queuedRecords(
    draftStore,
    username,
    heldKeys(open, closed),
  );

  return {
    changes: records.reduce(
      (sum, record) => sum + record.queue.length,
      closed.reduce(
        (sum, queue) => sum + queue.record.queue.length,
        open.pending,
      ),
    ),
    unapplied: blocked ? draft.describe(blocked) : '',
    drafts: fileNames(await unsentDrafts(draftStore, username, draft)),
  };
}

/**
 * Sends again, once the user has signed in again without leaving the
 * Builder (see signin.js), what the server refused while the session was
 * over: the open draft's changes, and those of drafts closed for the
 * drafts. Nothing is cleared.
 *
 * @returns {{changes: number, sent: Promise<void>}} how many changes wait
 *   to be sent, and the sends
 */
export function resumeBuilderSaves() {
  const draft = openDraft?.editing() ? openDraft : null;
  const refused = (state) => Boolean(state?.signInNeeded);
  const open = refused(draft?.store.saveState) ? openQueue(draft).pending : 0;
  const closed = closedQueues().filter((queue) => refused(queue.state));

  return {
    changes: closed.reduce(
      (sum, queue) => sum + queue.record.queue.length,
      open,
    ),
    sent: Promise.all([
      open > 0 ? draft.store.retrySave() : null,
      ...closed.map((queue) => queue.retry()),
    ]).then(() => {}),
  };
}

/**
 * The diagram a queued record leaves: the one its last change this browser
 * holds made current.
 *
 * @param {object} record a draft record, its entries with their snapshots
 * @returns {object|null}
 */
export function queuedDiagram(record) {
  const entries = record.entries || [];
  let diagram = null;

  for (const op of record.queue || []) {
    const entry = entries.find(
      (item) =>
        (op.commitId && item.id === op.commitId) ||
        (op.snapshotId && item.serverSnapshotId === op.snapshotId),
    );

    diagram = entry?.snapshot || diagram;
  }

  return diagram;
}

// The drafts holding `username`'s changes the server does not have, with
// the diagram each leaves: the open draft's as it is shown, and every other
// draft's as its queued changes leave it. The Inspector's edits that cannot
// be applied are in none of them.
async function unsentDrafts(draftStore, username, draft) {
  const open = openQueue(draft);
  const closed = closedQueues();
  const drafts =
    open.pending > 0 ? [{ key: open.key, doc: draft.store.doc }] : [];

  for (const queue of closed) {
    const doc = queue.record.queue.length > 0 && queuedDiagram(queue.record);

    if (doc) {
      drafts.push({ key: queue.record.key, doc });
    }
  }

  for (const record of await queuedRecords(
    draftStore,
    username,
    heldKeys(open, closed),
  )) {
    const doc = queuedDiagram((await draftStore.get(record.key)) || {});

    if (doc) {
      drafts.push({ key: record.key, doc });
    }
  }

  return drafts;
}

// Export's file name for each draft; one that two drafts would share gets a
// number, so each can be told apart and kept.
function fileNames(drafts) {
  const taken = new Set();

  return drafts.map(({ key, doc }) => {
    const { name } = diagramFile(doc);
    let unique = name;

    for (let count = 2; taken.has(unique); count += 1) {
      unique = name.replace(/\.json$/, `-${count}.json`);
    }

    taken.add(unique);

    return { key, name: unique };
  });
}

/**
 * The file Export saves for one of the drafts unsentBuilderWork names: its
 * diagram as this browser has it now, as Export saves JSON.
 *
 * @param {object} options username; key and name, as unsentBuilderWork
 *   names the draft; draftStore, for tests
 * @returns {Promise<{name: string, text: string}|null>} null when this
 *   browser no longer holds the draft
 */
export async function draftExport({
  username,
  key,
  name,
  draftStore = createDraftStore(),
}) {
  const draft = editedDraft();
  const closed = closedQueues().find((queue) => queue.record.key === key);
  let doc;

  if (draft && key === openQueue(draft).key) {
    doc = draft.store.doc;
  } else if (closed) {
    doc = queuedDiagram(closed.record);
  } else {
    const record = await draftStore.get(key).catch(() => null);

    doc = record?.actor === username ? queuedDiagram(record) : null;
  }

  return doc ? { name, text: diagramFile(doc).text } : null;
}

/**
 * A diagram as Export saves it as JSON (see exporters.js, which this module
 * does not load): its file name and text.
 *
 * @param {object} doc Builder document
 * @returns {{name: string, text: string}}
 */
export function diagramFile(doc) {
  const base = String(doc?.name || 'topology')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  return {
    name: `${base || 'topology'}.json`,
    text: `${JSON.stringify(doc, null, 2)}\n`,
  };
}
