// What Builder keeps in this browser, and what logout clears of it.
//
// The Builder keeps these in the browser:
// - In IndexedDB: the drafts that are not saved yet.
// - In localStorage under phenix.builder.*: the viewer's preferences
//   (theme, pane widths, minimap size, shortcuts, settings), the recent
//   commands (which name drafts, their owners and nodes by id), and the
//   unload copies of the drafts whose edits IndexedDB had not stored when a
//   page was left (see idb.js).
// - In memory, after its modules load: the drafts listed for the user and
//   the open diagram.
// What belongs to the user must not stay after the session on a shared
// workstation. So logout clears it, whether or not the Builder is open. The
// next sign-in of another user also clears it, when the browser was closed
// without logout. The preferences say nothing about the user or their
// work, and stay for this browser.
//
// The app loads this module, so the module stays small. The Builder's
// modules register what they hold in memory when they first load. A
// Builder that never opened has nothing in memory to clear.
//
// Clearing also deletes edits the server never received. So logout first
// asks this module for these edits (see utils/logout.js):
// - the open draft's edits, which the Builder view registers
// - the edits of drafts closed for the drafts page whose queues still send
//   them (see createBackgroundSaves in leave.js)
// - the edits queued in IndexedDB, or in an unload copy, for any other
//   draft, which are there whether or not the Builder is open
//
// A new sign-in without leaving the Builder (see signin.js) clears nothing.
// The queues send what the server refused in the meantime (see
// resumeBuilderSaves).

import { clearBuilderDatabase, createDraftStore } from './idb.js';
import { pageStorage } from './storage.js';

const BUILDER_STORAGE_PREFIX = 'phenix.builder.';

/**
 * The user the Builder data in this browser belongs to, written at sign-in.
 * It is not a preference, so logout clears it with the rest.
 */
export const BUILDER_USER_KEY = 'phenix.builder.user';

/**
 * The phenix.builder.* keys logout keeps: preferences of this browser,
 * which hold no names, ids or content. This list names them, and the
 * modules that own them do not register them, so a Builder that never
 * opened keeps them too. Logout clears any other key under the prefix. So
 * a new key stays private until it is added here as a preference.
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
 * Ends the Builder session:
 * 1. Removes its keys, except the preferences, from localStorage and
 *    sessionStorage.
 * 2. Resets what its modules hold in memory for the user.
 * 3. Deletes every local draft record in IndexedDB.
 * Steps 1 and 2 finish before it returns, so a navigation that follows
 * cannot show the previous user's data.
 *
 * @param {object} [options] localStorage, sessionStorage and clearDatabase,
 *   for tests
 * @returns {Promise<boolean>} whether the local draft records are gone
 */
export function endBuilderSession({
  localStorage = pageStorage('localStorage'),
  sessionStorage = pageStorage('sessionStorage'),
  clearDatabase = clearBuilderDatabase,
} = {}) {
  removeBuilderKeys(localStorage);
  removeBuilderKeys(sessionStorage);

  for (const reset of resets) {
    try {
      reset();
    } catch (error) {
      console.error('Could not reset Builder on logout.', error);
    }
  }

  return clearDatabase();
}

/**
 * Starts the Builder session of the user signing in. First it clears what
 * another user left in this browser when they closed it without logout, as
 * logout would have (see endBuilderSession). The preferences stay. It also
 * clears data that names no user. The local drafts database waits for the
 * clearing (see idb.js). So the Builder cannot read the previous user's
 * drafts, however soon it loads.
 *
 * @param {string} username the user signing in
 * @param {object} [options] localStorage, sessionStorage and clearDatabase,
 *   for tests
 * @returns {Promise<boolean>|null} the clearing, or null when the data was
 *   this user's already
 */
export function startBuilderSession(username, options = {}) {
  const { localStorage = pageStorage('localStorage') } = options;
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
 * Registers the draft open in the Builder view. Then logout can save the
 * Inspector's unapplied edits, as leaving does (see leave.js), and count
 * and send what the server does not have yet.
 *
 * @param {object} draft store: the Builder store. editing: () => whether a
 *   draft is open. saveUnapplied: () => the Inspector edits it cannot
 *   apply, or null. describe: (unapplied) => a sentence naming them
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

// The save queues of drafts closed for the drafts page, which still send
// (see registerQueue).
const backgroundQueues = new Set();

/**
 * Registers the save queue of a draft closed for the drafts page, which
 * continues to send (see createBackgroundSaves in leave.js). So logout
 * counts and sends its changes with that queue, not with a queue of its
 * own.
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

// The queues of drafts closed for the drafts page, with their records.
function closedQueues() {
  return [...backgroundQueues].filter((queue) => queue.record);
}

// The records in IndexedDB that hold unsent changes of `username`, except
// the records whose queues the Builder holds (`skip`, their keys).
async function queuedRecords(draftStore, username, skip) {
  const records = await draftStore.all().catch(() => []);

  return records.filter(
    (record) =>
      record.actor === username &&
      !skip.includes(record.key) &&
      (record.queue?.length || 0) > 0,
  );
}

// The keys of the records whose queues the Builder holds.
function heldKeys(open, closed) {
  return [open.key, ...closed.map((queue) => queue.record.key)].filter(Boolean);
}

// Sends each record's queue, in order, with a queue of its own bound to the
// record. A conflict or a refusal keeps it queued, as it would in the
// Builder. The modules that send load only now. Offline, they may not
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
// after the user chooses which changes to save. These are the tabs that
// hold a Web Lock. Where there are no Web Locks, they are the tabs that
// answer over a BroadcastChannel (see others in tabs.js). tabs.js loads
// only now, as the modules that send do.
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

// Settles when `work` settles, or after `ms`. It never rejects.
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
 * queued in this browser (the open draft's and every other draft's), and
 * the Inspector edits that cannot be applied. The Inspector edits that can
 * be applied are saved first, as leaving saves them. With `send`, the
 * queued changes are sent first, with a wait of at most `wait`. Changes
 * that another open tab holds are not sent here: that tab sends them.
 *
 * @param {object} options username, send, wait. draftStore, sendQueued and
 *   openTabs: for tests
 * @returns {Promise<{changes: number, unapplied: string, drafts: object[]}>}
 *   unapplied: a sentence naming the Inspector edits, or ''. drafts: the
 *   drafts Download can save, as {key, name} (see draftExport)
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
 * Sends again what the server refused while the session was over, after
 * the user signs in again without leaving the Builder (see signin.js). It
 * sends the open draft's changes, and those of drafts closed for the drafts
 * page. Nothing is cleared.
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
 * The diagram that a queued record gives: the diagram that the record's
 * last change in this browser made current.
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

// The drafts that hold changes of `username` that the server does not
// have, each with its diagram. The open draft's diagram is as it is shown.
// Every other draft's diagram is as its queued changes make it. None of
// them includes the Inspector's edits that cannot be applied.
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

// Download's file name for each draft. A name that two drafts would share
// gets a number, so each file is different and none is overwritten.
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
 * The file Download saves for one of the drafts unsentBuilderWork names: its
 * diagram as this browser has it now, as Download saves JSON, with a copy of
 * every custom icon it uses (see downloadedDiagram).
 *
 * @param {object} options username. key and name: as unsentBuilderWork
 *   names the draft. draftStore and iconLibrary: for tests
 * @returns {Promise<{name: string, text: string}|null>} null when this
 *   browser no longer holds the draft
 */
export async function draftExport({
  username,
  key,
  name,
  draftStore = createDraftStore(),
  iconLibrary = null,
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

  if (!doc) {
    return null;
  }

  const file = diagramFile(await downloadedDiagram(doc, { iconLibrary }));

  return { name, text: file.text };
}

/**
 * A diagram as Download saves it as JSON or YAML: a copy that carries every
 * custom icon its nodes and templates name. Each icon comes from the
 * diagram's own copies, else from the server's icon library. The library
 * is read first, unless it was read already (see embedIcons in icons.js
 * and downloadDocument in exporters.js). An icon that neither has is left
 * out, and the nodes that name it show their built-in icon. The app loads
 * this module, so the modules that do this load only for a diagram that
 * names a custom icon.
 *
 * @param {object} doc Builder document
 * @param {{iconLibrary?: object}} [options] the icon library (see
 *   iconLibrary.js), for tests
 * @returns {Promise<object>}
 */
export async function downloadedDiagram(doc, { iconLibrary = null } = {}) {
  const { embedIcons, iconRefs } = await import('./icons.js');

  if (iconRefs(doc).size === 0) {
    return embedIcons(doc).doc;
  }

  const library = iconLibrary || (await import('./iconLibrary.js')).iconLibrary;

  await library.ensure().catch(() => {});

  return embedIcons(doc, library).doc;
}

/**
 * A diagram as Download saves it as JSON (see exporters.js, which this module
 * does not load): its file name and text. The diagram is written as it is
 * given. downloadedDiagram makes the copy that carries its custom icons.
 *
 * @param {object} doc Builder document
 * @returns {{name: string, text: string}}
 */
export function diagramFile(doc) {
  const base = String(doc?.metadata?.name || 'topology')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  return {
    name: `${base || 'topology'}.json`,
    text: `${JSON.stringify(doc, null, 2)}\n`,
  };
}
