// Autosave: an ordered, per-commit persistence queue.
//
// Every semantic edit (a "commit") is recorded locally first and then sent to
// the server as its own snapshot, in the order it was made. Commits are never
// debounced or coalesced: the server history is meant to be the same history
// the user can step through locally, so collapsing two edits into one snapshot
// would silently lose an undo step.
//
// Concurrency is handled with ETags only. A conflict stops the queue and
// hands the store the server copy it read (see onConflict), which the store
// merges with the unsaved changes (see merge.js): the queue then holds one
// snapshot of the merged document, sent with the ETag of that server copy
// (see rebase). The user may also reload the server copy or save their local
// history as a new draft. There is no code path that overwrites a draft whose
// ETag we no longer hold.
//
// Some changes to a draft leave its content as it was: a change to who it is
// shared with, or someone else's publish. The queue keeps the head the server
// last confirmed (serverHead: the current snapshot, the cursor and how many
// snapshots it keeps), and on a conflict reads the draft again. When the
// draft as read already holds the operation sent, an earlier delivery of it
// was stored but its answer never came, so the read is taken as its answer
// (see alreadyStored). When the head is the same, the content is what this
// device last saw, so the queue takes the new ETag and sends again;
// otherwise the conflict stands, naming who saved last and carrying the
// draft as read. The read also finds a draft
// that is no longer shared with the user, or that they may now only view
// (accessLost).
//
// There is no presence, no live collaboration, no heartbeat and no polling:
// another editor's changes reach this queue only through a conflict. The
// only requests it makes are the draft read after a conflict, the draft
// snapshot append and the draft history cursor move; the last two are
// ordered, awaited and carry If-Match. "Cursor" here means the position in
// the draft's own undo history, not a collaborator's caret.
//
// A save stores the document with the creator, creation time, last editor
// and last edit time the server writes into its metadata, and answers with
// them (the stamp). The queue copies the stamp into its own copy of the
// snapshot it sent, so that copy is the document the server stores (see
// withStamp in model.js). It never makes these values up.
//
// Other tabs of this browser are another matter (see tabs.js). Each tab
// keeps its own local record of a draft, and while another tab, or a queue
// a closed tab left, holds changes to the draft the server does not have,
// this queue does not send its own: the user chooses which to save.

import { count } from './announce.js';
import { classifyError, errorMessage, sentence } from './api.js';
import { DEFAULT_HISTORY_LIMIT } from './history.js';
import { draftKey, tabRecordKey } from './idb.js';
import { metadataOf, STAMP_KEYS, withStamp } from './model.js';

export const RETRY_DELAYS = [1000, 2000, 5000, 15000, 30000];

// How many times in a row one operation is sent again after a conflict
// that left the content as it was.
export const MAX_REBASES = 2;

// Why a draft someone shared can no longer be saved (see accessLost in
// initialState): 'role' is a draft still shared for editing whose user's
// role may no longer change configs.
const ACCESS_LOST = {
  'view-only': 'You can no longer edit this draft.',
  role: 'You can no longer edit this draft: your role cannot change configs.',
  gone: 'This draft is no longer shared with you, or it was deleted.',
};

function accessLost(why, error) {
  return Object.assign(new Error(ACCESS_LOST[why]), {
    accessLost: why,
    response: error?.response,
  });
}

/**
 * Save-state text for a failure that retrying cannot fix: the reason from
 * the server, then what the user can do about it.
 *
 * @param {'invalid'|'missing'|'too-large'|'pruned'} kind
 * @param {string} reason errorMessage() for the failure
 * @returns {string}
 */
function blockedMessage(kind, reason) {
  const why = sentence(reason);

  switch (kind) {
    case 'invalid':
      return `Could not save your last change. ${why} Fix the diagram and it saves again.`;
    case 'missing':
      return `Could not save. ${why} Use Download to keep a copy of the diagram.`;
    case 'pruned':
      return 'Could not save your last undo or redo: the server no longer keeps that step. Your next edit saves the diagram as it is shown.';
    default:
      return `Could not save. ${why} Remove part of the diagram, or use Download to keep a copy.`;
  }
}

/**
 * @returns {object} initial queue state
 */
export function initialState() {
  return {
    status: 'idle',
    pending: 0,
    message: '',
    lastSavedAt: null,
    etag: null,
    online: true,
    // True while this device cannot store the queue (storage blocked, full
    // or unavailable), so nothing may claim that work is kept here.
    storageFailed: false,
    // False while an error is one that sending again cannot fix (a refused
    // or oversized snapshot, a deleted draft), so Retry saving is not
    // offered for it.
    retryable: true,
    // True while an error is the server refusing the session (401): the
    // queue waits for the user to sign in again (see signin.js).
    signInNeeded: false,
    // During a conflict: who saved the draft last, when the server said.
    lastModifiedBy: '',
    // Why a draft someone shared can no longer be saved: 'view-only' (the
    // user may now only view it), 'role' (their role may no longer change
    // configs) or 'gone' (no longer shared with them, or deleted); ''
    // otherwise. The queue is then blocked, as when forbidden.
    accessLost: '',
    // When the last change was queued.
    changedAt: null,
    // The other tabs (see tabs.js): this tab's id; the other tabs with the
    // draft open, each with its state; the other versions of the draft's
    // unsaved changes, in other tabs or left by closed tabs; whether the
    // queue waits for the user to choose which to save; and whether a
    // conflict is the user's choice of another version.
    tab: '',
    tabs: [],
    versions: [],
    heldForTabs: false,
    otherTab: false,
  };
}

/**
 * The id of the snapshot a draft envelope's cursor points at. A save answers
 * with the draft alone, which names it; a read also carries the history.
 *
 * @param {object} envelope readEnvelope() result
 * @returns {string|undefined}
 */
export function snapshotIdOf(envelope) {
  return (
    envelope?.history?.[envelope.cursor]?.id || envelope?.draft?.snapshotId
  );
}

/**
 * The stamp a create or save answered with: the creator, creation time, last
 * editor and last edit time of the document it stored. Empty for a document
 * that names none of them.
 *
 * @param {object} envelope readEnvelope() result
 * @returns {object|null} null when the envelope carries none, as that of a
 *   read or a cursor move
 */
export function stampOf(envelope) {
  const stamp = envelope?.draft?.stamp;

  return stamp && typeof stamp === 'object' && !Array.isArray(stamp)
    ? stamp
    : null;
}

/**
 * The head of the draft an envelope describes: its current snapshot, its
 * cursor and how many snapshots it keeps. Two envelopes with the same head
 * hold the same content, whatever else changed (who it is shared with, a
 * publication).
 *
 * @param {object} envelope readEnvelope() result, or {draft}
 * @returns {{snapshotId: string, cursor: number, snapshots: number}|null}
 *   null when the envelope does not say
 */
export function headOf(envelope) {
  const draft = envelope?.draft;
  const snapshotId = snapshotIdOf(envelope);
  const cursor = Number.isInteger(draft?.cursor)
    ? draft.cursor
    : envelope?.cursor;
  const snapshots = draft?.snapshots;

  if (
    !snapshotId ||
    !Number.isInteger(cursor) ||
    !Number.isInteger(snapshots)
  ) {
    return null;
  }

  return { snapshotId, cursor, snapshots };
}

/**
 * @param {object|null} a headOf() result
 * @param {object|null} b
 * @returns {boolean} whether both are known and the same
 */
export function sameHead(a, b) {
  return (
    Boolean(a && b) &&
    a.snapshotId === b.snapshotId &&
    a.cursor === b.cursor &&
    a.snapshots === b.snapshots
  );
}

/**
 * The draft as read again after the server refused an operation with 412,
 * as the answer to that operation, when the server already holds it: an
 * earlier delivery of the same operation was stored, but its answer never
 * came (the connection dropped once the server had stored it, or the
 * request reached the server twice). A snapshot is held when the draft's
 * newest snapshot is its current one and carries the operation's id (see
 * send); a cursor move when the draft's current snapshot is the one it
 * moves to and the draft keeps as many snapshots as when the queue last
 * heard from the server. The draft is then what the operation left, so the
 * queue goes on from it instead of meeting a conflict with its own save. A
 * snapshot's answer carries the stamp the stored document holds, as a
 * save's does (see stampOf).
 *
 * @param {object} op the operation the server refused
 * @param {object} envelope readEnvelope() result of the draft read again
 * @param {object|null} confirmed headOf() the draft the queue last heard
 *   of from the server (its serverHead)
 * @param {string} [target] the snapshot id a cursor move goes to
 * @returns {object|null} the envelope to go on from, or null when the
 *   server does not hold the operation
 */
export function alreadyStored(op, envelope, confirmed, target = '') {
  const head = headOf(envelope);

  if (!envelope?.etag || !head) {
    return null;
  }

  if (op?.kind === 'cursor') {
    const moved =
      Boolean(target) &&
      head.snapshotId === target &&
      head.snapshots === confirmed?.snapshots;

    return moved ? envelope : null;
  }

  const newest = Array.isArray(envelope.history)
    ? envelope.history.at(-1)
    : null;

  if (
    op?.kind !== 'snapshot' ||
    !op.opId ||
    newest?.opId !== op.opId ||
    newest.id !== head.snapshotId
  ) {
    return null;
  }

  if (!envelope.document) {
    return envelope;
  }

  const metadata = metadataOf(envelope.document);
  const stamp = {};

  STAMP_KEYS.forEach((key) => {
    if (typeof metadata[key] === 'string') {
      stamp[key] = metadata[key];
    }
  });

  return { ...envelope, draft: { ...envelope.draft, stamp } };
}

/**
 * How many of a recovered queue's operations the server already holds. A
 * session that ended while a save was under way never saw the answer, so
 * the operation is still queued. A stored snapshot carries the id of the
 * operation that sent it (see send), and operations are sent in order, so
 * everything queued up to the last snapshot the server holds was sent.
 *
 * @param {object[]} queue recovered operations
 * @param {object[]|null} history the server's snapshot history
 * @returns {{count: number, snapshotIds: Map<string, string>}} how many
 *   leading operations were sent, and the snapshot id the server gave each
 *   snapshot among them, by commit id
 */
export function appliedOperations(queue, history) {
  const stored = new Map(
    (history || [])
      .filter((snapshot) => snapshot?.opId)
      .map((snapshot) => [snapshot.opId, snapshot.id]),
  );
  const held = (op) => op.kind === 'snapshot' && stored.has(op.opId);
  const count = queue.reduce(
    (sent, op, index) => (held(op) ? index + 1 : sent),
    0,
  );

  return {
    count,
    snapshotIds: new Map(
      queue
        .slice(0, count)
        .filter(held)
        .map((op) => [op.commitId, stored.get(op.opId)]),
    ),
  };
}

/**
 * The undo history a recovered queue leaves, applying its operations in
 * order to the history the draft opened with, as the server does: a
 * snapshot drops the redo branch after the current entry and becomes
 * current, and a cursor move makes its entry current. So an edit undone
 * before a later edit is left out, as it is on the server. A move to an
 * entry this device holds that is not in that history yet, such as an undo
 * past the snapshot the draft opened at, puts the entry in it, before the
 * current entry when it is older and after it otherwise. A move to a
 * snapshot this device does not hold leaves the current entry as it is,
 * and a refused snapshot that a later operation replaces is skipped, as
 * the queue skips it.
 *
 * @param {object[]} base history entries the queue starts from
 * @param {number} index the current entry of base
 * @param {object[]} queue operations
 * @param {object[]} entries the local entries the operations refer to
 * @returns {{entries: object[], index: number}}
 */
export function replayHistory(base, index, queue, entries) {
  const byId = new Map(entries.map((entry) => [entry.id, entry]));
  let stack = [...base];
  let current = index;

  queue.forEach((op, position) => {
    if (op.kind === 'snapshot') {
      const entry = byId.get(op.commitId);

      if (entry && !(op.rejected && position < queue.length - 1)) {
        stack = [...stack.slice(0, current + 1), entry];
        current = stack.length - 1;
      }

      return;
    }

    const matches = (entry) =>
      (op.commitId && entry.id === op.commitId) ||
      (op.snapshotId && entry.serverSnapshotId === op.snapshotId);
    const target = stack.findIndex(matches);

    if (target >= 0) {
      current = target;

      return;
    }

    const entry = entries.find(matches);

    if (!entry) {
      return;
    }

    // Commit order: an entry's place among the local entries, by id or by
    // the snapshot it was saved as.
    const rank = (item) =>
      entries.findIndex(
        (other) =>
          other.id === item.id ||
          (item.serverSnapshotId &&
            other.serverSnapshotId === item.serverSnapshotId),
      );
    const newer = rank(stack[current]);

    if (newer >= 0 && rank(entry) > newer) {
      stack = [...stack.slice(0, current + 1), entry];
      current += 1;
    } else {
      stack = [...stack.slice(0, current), entry, ...stack.slice(current)];
    }
  });

  return { entries: stack, index: current };
}

/**
 * Human readable, screen-reader friendly description of the queue state.
 *
 * @param {object} state
 * @returns {string}
 */
export function describeState(state) {
  switch (state.status) {
    case 'saving':
      return state.pending > 1
        ? `Saving ${state.pending} changes`
        : 'Saving changes';
    case 'saved':
      return 'All changes saved';
    case 'offline':
      if (state.pending === 0) {
        return 'Offline: no unsaved changes';
      }

      return state.storageFailed
        ? `Offline: ${count(state.pending, 'change')} not stored anywhere yet. Keep this tab open; saving retries automatically.`
        : `Offline: ${count(state.pending, 'change')} kept on this device. Saving retries automatically.`;
    case 'conflict':
      return state.otherTab
        ? "Not saved: you chose another tab's changes"
        : 'This draft changed on the server';
    case 'forbidden':
      return 'You cannot save changes to this draft';
    case 'error':
      return state.message || 'Changes could not be saved';
    default:
      if (state.heldForTabs && state.pending > 0) {
        return `${count(state.pending, 'change')} not saved yet. Choose which changes to save.`;
      }

      return state.pending > 0
        ? count(state.pending, 'unsaved change')
        : 'Ready';
  }
}

// Problems the save state announces. A conflict is left out: the conflict
// panel is an alert of its own, and resolving it is announced by the action
// the user chose.
const PROBLEMS = ['offline', 'error', 'forbidden'];

/**
 * What to announce when the queue state changes, if anything. Only
 * transitions a user must know about are spoken: a new problem, and the
 * recovery from one. The transient "Saving" and a problem that has not
 * changed (the offline queue retrying on its own) stay silent, so the live
 * region does not repeat itself on every edit or retry.
 *
 * @param {object|null} last state last announced (status, message, storageFailed)
 * @param {object} next new queue state
 * @returns {string} message, or '' to stay silent
 */
export function saveAnnouncement(last, next) {
  if (next.status === 'saved') {
    return last && PROBLEMS.includes(last.status) ? 'All changes saved.' : '';
  }

  if (!PROBLEMS.includes(next.status)) {
    return '';
  }

  const same =
    last &&
    last.status === next.status &&
    last.message === next.message &&
    Boolean(last.storageFailed) === Boolean(next.storageFailed);

  return same ? '' : describeState(next);
}

/**
 * Whether a save-state message waiting to be spoken no longer holds. A
 * retry under way (idle or saving) does not make it stale: its outcome is
 * announced when known and replaces the waiting message. Any other status,
 * such as a conflict raised meanwhile, does.
 *
 * @param {string} announced status the message describes
 * @param {string} current status now
 * @returns {boolean}
 */
export function staleSaveMessage(announced, current) {
  return announced !== current && !['idle', 'saving'].includes(current);
}

// Why a queue no longer sends: the user chose another version of the
// draft's unsaved changes (see tabs.js).
const OTHER_TAB_CHOSEN = 'You chose to save the changes made in another tab.';

/**
 * The server copy a conflict carries: the draft as the read after the
 * conflict found it (see sendChecked), which the store merges with the
 * unsaved changes.
 *
 * @param {object} envelope readEnvelope() result of a draft read
 * @returns {{document: object, etag: string, head: object|null,
 *   lastModifiedBy: string, draft: object, history: object[]|null}|null}
 *   null when the read holds no document or no ETag
 */
export function serverCopyOf(envelope) {
  if (!envelope?.etag || !envelope.document) {
    return null;
  }

  return {
    document: envelope.document,
    etag: envelope.etag,
    head: headOf(envelope),
    lastModifiedBy: envelope.draft?.lastModifiedBy || '',
    draft: envelope.draft || null,
    history: Array.isArray(envelope.history) ? envelope.history : null,
  };
}

/**
 * Creates the autosave queue.
 *
 * @param {object} options api, store, actor, onState, onDraft, now, setTimeout,
 *   clearTimeout, isOnline, addOnlineListener, historyLimit; onConflict:
 *   called with the server copy (see serverCopyOf) when a save meets a
 *   draft someone else saved since; tabs: the other tabs of this browser
 *   (builderTabs in tabs.js), which a queue without one ignores;
 *   signInHere: () => whether the Builder can ask for the password again
 *   once the session has ended (see signin.js)
 * @returns {object} queue
 */
export function createAutosave(options = {}) {
  const {
    api,
    store,
    actor = '',
    now = () => new Date().toISOString(),
    historyLimit = DEFAULT_HISTORY_LIMIT,
    tabs = null,
    signInHere = () => false,
  } = options;
  // Replaced when the draft is closed and the queue goes on sending (see
  // observe).
  let onState = options.onState || (() => {});
  let onDraft = options.onDraft || (() => {});
  let onConflict = options.onConflict || (() => {});
  // The server copy the conflict in force carries (see serverCopyOf), or
  // null.
  let serverCopy = null;

  const timer = {
    set: options.setTimeout || ((fn, ms) => setTimeout(fn, ms)),
    clear: options.clearTimeout || ((handle) => clearTimeout(handle)),
  };

  const isOnline =
    options.isOnline ||
    (() =>
      typeof navigator === 'undefined' ? true : navigator.onLine !== false);

  let state = { ...initialState(), online: isOnline() };
  let record = null;
  let flushing = false;
  // The flush under way, so an explicit retry can wait for it to settle.
  let inflight = null;
  // The flush that runs once the one under way settles, shared by every
  // foreground flush asked for meanwhile.
  let followUp = null;
  let retries = 0;
  let retryHandle = null;
  let detachOnline = null;
  let disposed = false;
  // The ids of the entry snapshots the store holds for the record, so each
  // is written once, and whether it holds the record at all.
  let stored = new Set();
  let hasRecord = false;
  // The ids of the entries whose snapshot took a stamp since the store
  // wrote it (see restamp), which the next write stores again.
  let restamped = new Set();
  // What the store is known to hold of the record: the entries and the
  // operations its last finished write stored. Leaving the page copies the
  // rest at once (see keepForUnload); copiedOps lists the operations such a
  // copy holds, while it may be kept still, until the store holds them.
  let held = { entries: new Set(), ops: new Set() };
  let copiedOps = null;
  // While above zero, nothing is sent (see hold).
  let holds = 0;
  // Operations the server has confirmed, so a caller can tell whether any
  // was sent while it waited on a request of its own.
  let sent = 0;
  // This tab's id, the other tabs with the draft open (see tabs.js), and
  // whether a send waits for the user to choose which changes to save.
  let tab = '';
  let coordinator = null;
  let waitingForTabs = false;

  // A conflict that is the user's choice of another tab's changes says so
  // (otherTab) until the queue leaves it; an error that is the session
  // ending (signInNeeded) until another status, or another error, replaces
  // it.
  const emit = (patch = {}) => {
    const conflicted = (patch.status || state.status) === 'conflict';

    state = {
      ...state,
      ...patch,
      pending: record ? record.queue.length : 0,
      changedAt: record?.changedAt || null,
      otherTab: conflicted && Boolean(patch.otherTab ?? state.otherTab),
      signInNeeded: patch.status
        ? patch.status === 'error' && Boolean(patch.signInNeeded)
        : state.signInNeeded,
      tab,
      tabs: coordinator ? coordinator.peers() : [],
      versions: coordinator ? coordinator.versions() : [],
      heldForTabs: waitingForTabs,
    };
    coordinator?.publish(state);
    if (!disposed) {
      onState(state);
    }

    return state;
  };

  // The key of this tab's record of a draft.
  function ownKey(owner, draftId) {
    return tabRecordKey(draftKey(actor, owner, draftId), tab);
  }

  // Tells the other tabs with the draft open about this queue, and hears
  // theirs (see tabs.js): one coordinator per draft. `held` is the record
  // this tab holds of it, whose changes its first message says: a tab that
  // said none, as it reloads, would let another send its own.
  function connect(owner, draftId, held) {
    const draft = `${owner}/${draftId}`;

    if (!tabs || coordinator?.draft === draft) {
      return;
    }

    coordinator?.close();
    coordinator = tabs.open({
      actor,
      owner,
      draftId,
      tab,
      state: {
        pending: held?.queue?.length || 0,
        changedAt: held?.changedAt || held?.updatedAt || null,
      },
      // This user's records of the draft, but this tab's.
      records: async () =>
        ((await store?.all?.()) || []).filter(
          (item) =>
            item.actor === actor &&
            item.owner === owner &&
            item.draftId === draftId &&
            item.key !== ownKey(owner, draftId),
        ),
      onChange: tabsChanged,
    });
    coordinator.draft = draft;
  }

  // Reading the queues closed tabs left before a held send goes (see
  // tabsChanged).
  let rechecking = null;

  // The other tabs changed: their states, the queues closed tabs left, or
  // the user's choice of which changes to save.
  function tabsChanged() {
    if (disposed || !record || !coordinator) {
      return;
    }

    // Another version was chosen: this one can no longer be sent to the
    // draft, and waits to be saved as a new draft.
    if (
      coordinator.lost() &&
      record.queue.length > 0 &&
      !['conflict', 'forbidden'].includes(state.status)
    ) {
      cancelRetry();
      waitingForTabs = false;
      serverCopy = null;
      emit({
        status: 'conflict',
        message: OTHER_TAB_CHOSEN,
        lastModifiedBy: '',
        otherTab: true,
      });

      return;
    }

    // Nothing seems to stand in the way of a held send: the queues closed
    // tabs left are read first, as a tab that has just gone may have left
    // its changes.
    if (
      waitingForTabs &&
      !rechecking &&
      !coordinator.blocked(record.queue.length)
    ) {
      const checking = coordinator;

      rechecking = checking
        .scan()
        .catch(() => {})
        .then(() => {
          rechecking = null;

          if (
            !disposed &&
            record &&
            coordinator === checking &&
            waitingForTabs &&
            !checking.blocked(record.queue.length)
          ) {
            waitingForTabs = false;
            emit();
            flush();
          }
        });
    }

    emit();
  }

  /**
   * Takes the queue a closed tab left of the draft, when it is the only
   * version of the draft's unsaved changes: this tab's record holds none.
   * A tab reloaded without its id finds its queue so (see tabs.js). With
   * more, none is taken: the user chooses which to save.
   *
   * @param {string} key this tab's record key
   */
  async function takeClosedQueue(key) {
    const own = await store.get(key).catch(() => undefined);
    const closed = await coordinator.scan();

    if (own?.queue?.length || closed.length !== 1 || !store.rekey) {
      return;
    }

    // Two tabs taking it at once: the database runs one move after the
    // other, and the second finds nothing to move.
    try {
      if (await store.rekey(closed[0].key, key, { tab })) {
        await coordinator.scan();
        coordinator.recordsChanged();
      }
    } catch {
      // It stays the closed tab's, and the user chooses.
    }
  }

  // Local persistence must never reject into a caller that cannot handle it:
  // the queue lives in memory too, so a storage failure is recorded in the
  // state (the offline message then stops claiming work is kept on this
  // device) and the send continues rather than raising an unhandled
  // rejection.
  //
  // The store keeps only what the queue needs to replay: the entries its
  // operations refer to, each written once. A drained queue needs nothing,
  // so its record is removed. A save that settles after the queue was
  // disposed is still recorded, so a reopened draft does not find that
  // operation queued against an ETag the server has moved past.
  const persist = async () => {
    if (!record || !store) {
      return;
    }

    const { key } = record;

    record.updatedAt = now();

    try {
      if (record.queue.length === 0) {
        // An unload copy of a drained queue keeps nothing.
        release();
        restamped = new Set();

        if (hasRecord) {
          hasRecord = false;
          stored = new Set();
          await store.remove(record.key);
        }

        confirm(key, [], []);
      } else {
        const needed = new Set(record.queue.map((op) => op.commitId));
        const entries = record.entries.filter((entry) => needed.has(entry.id));
        const write = entries.filter(
          (entry) => !stored.has(entry.id) || restamped.has(entry.id),
        );
        const drop = [...stored].filter((id) => !needed.has(id));
        const ops = record.queue.map((op) => op.opId);

        hasRecord = true;
        stored = new Set(entries.map((entry) => entry.id));
        restamped = new Set();
        await store.put({ ...record, entries }, { write, drop });
        confirm(
          key,
          entries.map((entry) => entry.id),
          ops,
        );
      }

      if (state.storageFailed) {
        emit({ storageFailed: false });
      }
    } catch {
      // What the store holds is unknown now: the next write writes it all.
      stored = new Set();
      restamped = new Set();
      hasRecord = true;

      if (!state.storageFailed) {
        emit({ storageFailed: true });
      }
    }
  };

  // Copies the stamp a save answered with into the entry it stored, so the
  // entry holds the document the server does. An undo back to the entry
  // (a cursor move kept with the queue) then shows who saved it, and when.
  function restamp(entry, stamp) {
    if (!entry || !stamp) {
      return;
    }

    const snapshot = withStamp(entry.snapshot, stamp);

    if (snapshot !== entry.snapshot) {
      entry.snapshot = snapshot;
      restamped.add(entry.id);
    }
  }

  // Removes the record's unload copy.
  function release() {
    store?.release?.(record.key);
    copiedOps = null;
  }

  // Records what a finished write stored (see held); an unload copy is
  // removed once the store holds each operation it copied that is queued
  // still. Those sent since need keeping no more, and those queued since
  // the copy was taken are the store's to keep.
  function confirm(key, entries, ops) {
    if (record?.key !== key) {
      return;
    }

    held = { entries: new Set(entries), ops: new Set(ops) };

    if (
      copiedOps &&
      record.queue.every(
        (op) => !copiedOps.has(op.opId) || held.ops.has(op.opId),
      )
    ) {
      release();
    }
  }

  /**
   * Keeps what the store may not hold yet as the page is left: a write to
   * IndexedDB finishes after the call that makes it, and a reload or a
   * closed tab can cut it off. The operations queued since the store's last
   * finished write, such as the edit leaving applies (see leave.js), are
   * copied at once, with the snapshots it may lack (see writeUnloadCopy in
   * idb.js); the next read of the draft merges the copy back, unless the
   * store was written after it (see staleUnloadCopy). A copy that does not
   * fit is not written: the caller keeps the browser's question, so the
   * user can stay until the store holds the edits. Leaving anyway can lose
   * those the store had not stored yet.
   *
   * @returns {boolean} false when some queued work may not be kept
   */
  function keepForUnload() {
    if (
      !record ||
      !store?.keep ||
      disposed ||
      record.queue.every((op) => held.ops.has(op.opId))
    ) {
      return true;
    }

    const needed = new Set(record.queue.map((op) => op.commitId));
    const entries = record.entries.filter((entry) => needed.has(entry.id));

    const kept = store.keep(
      { ...record, entries },
      { write: entries.filter((entry) => !held.entries.has(entry.id)) },
    );

    // A copy that does not fit removes the older one (see writeUnloadCopy).
    copiedOps = kept ? new Set(record.queue.map((op) => op.opId)) : null;

    return kept;
  }

  const cancelRetry = () => {
    if (retryHandle !== null) {
      timer.clear(retryHandle);
      retryHandle = null;
    }
  };

  const scheduleRetry = () => {
    if (disposed) {
      return;
    }

    cancelRetry();

    const delay = RETRY_DELAYS[Math.min(retries, RETRY_DELAYS.length - 1)];

    retries += 1;
    retryHandle = timer.set(() => {
      retryHandle = null;
      flush({ background: true });
    }, delay);
  };

  // A snapshot the server refused as invalid or too large can never be
  // stored, nor can a cursor move to a snapshot the server no longer keeps
  // ('pruned'). Once any later operation is queued (the user fixed the
  // diagram, or undid the edit), it is dropped so it no longer blocks the
  // queue, together with cursor moves that point at it. The local history
  // keeps the entry.
  const dropRejected = () => {
    const op = record.queue[0];

    if (!op?.rejected || record.queue.length < 2) {
      return false;
    }

    record.queue = record.queue
      .slice(1)
      .filter(
        (later) => later.kind !== 'cursor' || later.commitId !== op.commitId,
      );

    return true;
  };

  /**
   * Sends queued operations in order until the queue drains or is blocked.
   *
   * @param {object} [options] background: a retry the user did not start;
   *   the state keeps its problem text until the attempt settles, so the
   *   status does not flicker between "Saving" and the problem on every retry
   * @returns {Promise<object>} state, once this flush has ended
   */
  async function flush({ background = false } = {}) {
    if (disposed || !record || !record.owner || !record.draftId) {
      return state;
    }

    // A save is under way. A foreground flush (an edit, Save now, publish)
    // waits for it and then flushes again, so it returns how saving ended
    // rather than "Saving", with whatever was queued meanwhile sent too.
    // The flushes asked for meanwhile share that one follow-up, so edits
    // made during a failing save do not each send again. A background
    // retry leaves the save under way to finish.
    if (flushing) {
      if (background) {
        return state;
      }

      followUp ||= inflight.then(() => {
        followUp = null;

        return flush();
      });

      return followUp;
    }

    if (['conflict', 'forbidden'].includes(state.status)) {
      return state;
    }

    // Held (a publish is under way): the queue keeps what was made meanwhile
    // and sends it once released.
    if (holds > 0) {
      return record.queue.length > 0 ? emit({ status: 'idle' }) : state;
    }

    // Claimed before the first await, so no second flush starts meanwhile
    // and sends the same operation again.
    flushing = true;

    let settled;
    inflight = new Promise((resolve) => {
      settled = resolve;
    });

    try {
      if (dropRejected()) {
        await persist();
      }

      if (record.queue.length === 0) {
        return emit({ status: 'saved', message: '' });
      }

      // Only a new operation can unblock a refused snapshot; resending it
      // would be refused again.
      if (record.queue[0].rejected) {
        const reasons = {
          'too-large': 'The diagram is too large.',
          pruned: '',
          invalid: 'The server refused it as invalid.',
        };
        const kind =
          record.queue[0].rejected in reasons
            ? record.queue[0].rejected
            : 'invalid';

        return state.status === 'error'
          ? state
          : emit({
              status: 'error',
              retryable: false,
              message: blockedMessage(kind, reasons[kind]),
            });
      }

      if (!isOnline()) {
        return emit({ status: 'offline', online: false });
      }

      // Another tab, or a queue a closed tab left, holds changes to this
      // draft too: nothing is sent until the user chooses (see tabs.js).
      if (coordinator?.blocked(record.queue.length)) {
        waitingForTabs = true;
        cancelRetry();

        return emit({ status: 'idle', online: true, message: '' });
      }

      waitingForTabs = false;

      if (!background) {
        emit({ status: 'saving', online: true, message: '' });
      }

      while (record.queue.length > 0) {
        if (disposed) {
          return state;
        }

        if (dropRejected()) {
          await persist();
          continue;
        }

        const op = record.queue[0];
        const envelope = await sendChecked(op);

        sent += 1;
        record.queue.shift();
        record.etag = envelope.etag || record.etag;
        record.serverHead = headOf(envelope);

        if (op.kind === 'snapshot') {
          const entry = record.entries.find((item) => item.id === op.commitId);
          const snapshotId = snapshotIdOf(envelope);

          if (entry && snapshotId) {
            entry.serverSnapshotId = snapshotId;
          }

          restamp(entry, stampOf(envelope));
        }

        await persist();
        if (!disposed) {
          onDraft(envelope, op);
        }

        // Each change sent counts: the draft's card, and the other tabs.
        emit();
      }

      retries = 0;
      cancelRetry();

      return emit({
        status: 'saved',
        lastSavedAt: now(),
        etag: record.etag,
        accessLost: '',
      });
    } catch (error) {
      if (disposed) {
        return state;
      }

      // A draft someone shared that the user may no longer change: nothing
      // queued can be saved to it.
      if (error?.accessLost) {
        cancelRetry();

        return emit({
          status: 'forbidden',
          accessLost: error.accessLost,
          message: ACCESS_LOST[error.accessLost],
        });
      }

      const kind = classifyError(error);
      const message = errorMessage(kind, error);

      if (kind === 'offline') {
        scheduleRetry();

        return emit({ status: 'offline', online: false, message });
      }

      if (kind === 'conflict' || kind === 'forbidden') {
        cancelRetry();
        serverCopy = kind === 'conflict' ? error?.server || null : null;

        const blocked = emit({
          status: kind,
          message,
          lastModifiedBy: error?.lastModifiedBy || '',
        });

        // The store merges the server copy with what is queued here.
        if (serverCopy) {
          onConflict(serverCopy);
        }

        return blocked;
      }

      // The session ended (401). Sending again succeeds once the user is
      // signed in again, so the queue stays retryable, but not on a timer:
      // the same credentials are refused every time. Where the Builder can
      // ask for the password again, it resumes the queue once it has it.
      if (kind === 'unauthenticated') {
        cancelRetry();

        return emit({
          status: 'error',
          retryable: true,
          signInNeeded: true,
          message: signInHere()
            ? 'Not saved: your session has ended. Sign in again to save your changes.'
            : 'Could not save: your session has ended. Use Download to keep a copy of the diagram, then sign in again.',
        });
      }

      // Retrying cannot change these answers, so the queue waits for the
      // user instead of asking again on a timer.
      if (kind === 'invalid' || kind === 'missing' || kind === 'too-large') {
        cancelRetry();

        // A snapshot refused as invalid or too large is replaced by the
        // next edit (see dropRejected); the kind says why after a reload.
        // So is an undo or redo to a snapshot the server no longer keeps.
        const head = record.queue[0];
        const pruned = kind === 'missing' && head?.kind === 'cursor';

        if ((kind !== 'missing' && head?.kind === 'snapshot') || pruned) {
          head.rejected = pruned ? 'pruned' : kind;
          await persist();
        }

        return emit({
          status: 'error',
          retryable: false,
          message: blockedMessage(pruned ? 'pruned' : kind, message),
        });
      }

      scheduleRetry();

      return emit({
        status: 'error',
        retryable: true,
        message: `Could not save your changes. ${sentence(message)} Saving retries automatically.`,
      });
    } finally {
      flushing = false;
      inflight = null;
      settled();
    }
  }

  // Reads the draft again after a conflict or a refusal (see the header).
  // A draft someone shared that is gone or may now only be viewed is thrown
  // as access lost; a read that fails otherwise leaves `error` as it was.
  async function readAgain(error) {
    const shared = record.owner !== actor;
    let fresh;

    try {
      fresh = await api.getDraft(record.owner, record.draftId);
    } catch (readError) {
      if (shared && classifyError(readError) === 'missing') {
        throw accessLost('gone', error);
      }

      throw error;
    }

    // Still shared for editing, but the role may not change configs.
    if (shared && fresh?.draft?.readOnly) {
      throw accessLost(
        fresh.draft.access === 'edit' ? 'role' : 'view-only',
        error,
      );
    }

    return fresh;
  }

  /**
   * Sends `op`. A conflict reads the draft again. When the draft as read
   * already holds the operation, an earlier delivery of it was stored and
   * its answer never came: the read is its answer (see alreadyStored). When
   * its head is the one the server last confirmed, the content is as this
   * device last saw it: the queue takes the new ETag and sends again, at
   * most MAX_REBASES times in a row. Otherwise the conflict is thrown,
   * naming who saved last. A refusal (403 or 404) of a draft someone shared
   * reads it again too, to tell whether the user lost access.
   *
   * @param {object} op queued operation
   * @returns {Promise<object>} the server's envelope
   */
  async function sendChecked(op) {
    for (let rebases = 0; ; rebases += 1) {
      try {
        return await send(op);
      } catch (error) {
        const kind = classifyError(error);
        const refused =
          record.owner !== actor && ['forbidden', 'missing'].includes(kind);

        if (!(kind === 'conflict' && rebases < MAX_REBASES) && !refused) {
          throw error;
        }

        const fresh = await readAgain(error);

        if (kind !== 'conflict') {
          throw error;
        }

        const delivered = alreadyStored(
          op,
          fresh,
          record.serverHead,
          targetOf(op),
        );

        if (delivered) {
          return delivered;
        }

        if (!fresh?.etag || !sameHead(headOf(fresh), record.serverHead)) {
          throw Object.assign(new Error(error?.message || 'conflict'), {
            response: error?.response,
            lastModifiedBy: fresh?.draft?.lastModifiedBy || '',
            // The draft as read, for the store to merge (see onConflict).
            server: serverCopyOf(fresh),
          });
        }

        record.etag = fresh.etag;
        await persist();

        if (!disposed) {
          onDraft(fresh, null);
        }
      }
    }
  }

  // The server snapshot a cursor move goes to: the one it names, or the
  // one its entry was saved as.
  function targetOf(op) {
    if (op.kind !== 'cursor') {
      return '';
    }

    const entry = record.entries.find((item) => item.id === op.commitId);

    return op.snapshotId || entry?.serverSnapshotId || '';
  }

  async function send(op) {
    if (op.kind === 'cursor') {
      const snapshotId = targetOf(op);

      if (!snapshotId) {
        throw Object.assign(new Error('missing server snapshot id'), {
          response: { status: 500 },
        });
      }

      return api.moveCursor(
        record.owner,
        record.draftId,
        { snapshotId },
        record.etag,
      );
    }

    const entry = record.entries.find((item) => item.id === op.commitId);

    if (!entry) {
      throw Object.assign(new Error('missing local snapshot'), {
        response: { status: 500 },
      });
    }

    // The operation id is recorded with the snapshot, so a session that
    // never saw the response can tell the snapshot was stored.
    return api.appendSnapshot(
      record.owner,
      record.draftId,
      {
        document: entry.snapshot,
        summary: op.label || entry.label,
        opId: op.opId,
      },
      record.etag,
    );
  }

  function trimEntries() {
    if (record.entries.length <= historyLimit) {
      return;
    }

    const keep = new Set(record.queue.map((op) => op.commitId));

    while (
      record.entries.length > historyLimit &&
      !keep.has(record.entries[0].id)
    ) {
      record.entries.shift();
    }
  }

  return {
    /** @returns {object} current state */
    get state() {
      return state;
    },

    /** @returns {object|null} local record */
    get record() {
      return record;
    },

    /**
     * Binds the queue to a draft, replacing any local record.
     *
     * @param {object} draft owner, draftId, etag, entries, cursor, and
     *   serverHead: headOf() the envelope etag came with, when known; key:
     *   the record to bind to, when not this tab's (a record another page
     *   left, which logout sends)
     */
    async attach(draft) {
      tab = tabs ? await tabs.tab() : '';

      const key = draft.key || ownKey(draft.owner, draft.draftId);
      // Any log this device already holds for the draft is kept unless the
      // caller passes a replacement: attaching must never be the reason local
      // work disappears.
      const explicit = draft.entries !== undefined || draft.queue !== undefined;

      if (!draft.key) {
        const own =
          explicit || !store
            ? draft
            : await store.get(key).catch(() => undefined);

        connect(draft.owner, draft.draftId, own);

        if (!explicit && coordinator && store) {
          await takeClosedQueue(key);
        }
      }

      // Read either way: what the store holds is not written again.
      const existing = store ? (await store.get(key)) || null : null;
      const hasPending = !explicit && (existing?.queue || []).length > 0;

      record = {
        key,
        tab: draft.key ? existing?.tab || '' : tab,
        actor,
        owner: draft.owner,
        draftId: draft.draftId,
        etag: hasPending ? existing.etag : draft.etag || null,
        // The head that ETag stands for, so a pending queue keeps its own.
        serverHead: hasPending
          ? existing.serverHead || null
          : draft.serverHead || null,
        cursor: draft.cursor ?? existing?.cursor ?? 0,
        entries: draft.entries ?? existing?.entries ?? [],
        queue: draft.queue ?? existing?.queue ?? [],
        changedAt: existing?.changedAt || existing?.updatedAt || null,
        updatedAt: now(),
      };
      // A record merged with an unload copy may reference snapshots the
      // store lacks: every one is written again, and the copy is removed
      // once they are.
      const merged = Boolean(existing?.fromUnload);

      copiedOps = merged
        ? new Set((existing.queue || []).map((op) => op.opId))
        : null;
      stored = new Set(
        merged ? [] : (existing?.entries || []).map((entry) => entry.id),
      );
      restamped = new Set();
      held = {
        entries: new Set(stored),
        ops: new Set(
          merged ? [] : (existing?.queue || []).map((op) => op.opId),
        ),
      };
      hasRecord = Boolean(existing);
      serverCopy = null;

      await persist();

      return emit({
        status: record.queue.length > 0 ? 'idle' : 'saved',
        etag: record.etag,
        message: '',
        lastModifiedBy: '',
        accessLost: '',
      });
    },

    /**
     * Reads this tab's locally stored record for a draft, so an interrupted
     * session can restore its full ordered history and pending queue. Call
     * it once attached: attaching finds the tab's id.
     *
     * @param {string} owner
     * @param {string} draftId
     * @returns {Promise<object|null>}
     */
    async recover(owner, draftId) {
      if (!store) {
        return null;
      }

      const found = await store.get(ownKey(owner, draftId));

      return found || null;
    },

    /** @returns {Promise<object[]>} every local record for this actor */
    async recoverAll() {
      if (!store) {
        return [];
      }

      const all = await store.all();

      return all.filter((entry) => entry.actor === actor);
    },

    /**
     * Records one semantic commit: stored locally first, then queued for the
     * server as its own snapshot.
     *
     * @param {{id: string, label: string, snapshot: object}} entry
     * @returns {Promise<object>} state
     */
    async commit(entry) {
      if (!record) {
        return state;
      }

      record.entries = [
        ...record.entries.filter((item) => item.id !== entry.id),
        {
          id: entry.id,
          label: entry.label,
          snapshot: entry.snapshot,
          serverSnapshotId: entry.serverSnapshotId,
        },
      ];
      record.cursor = record.entries.length - 1;
      record.queue = [
        ...record.queue,
        {
          opId: entry.id,
          kind: 'snapshot',
          commitId: entry.id,
          label: entry.label,
        },
      ];
      record.changedAt = now();

      trimEntries();
      await persist();

      // A blocked queue keeps its state: the user must resolve the conflict or
      // the permission problem before anything else is sent. The pending
      // count still grows, since the conflict panel reports it.
      emit(
        ['conflict', 'forbidden'].includes(state.status)
          ? {}
          : { status: 'saving' },
      );

      return flush();
    },

    /**
     * Queues a move of the draft's *history* cursor (undo/redo) behind the
     * commits already queued. This is not a collaboration or caret signal:
     * it tells the server which snapshot the draft currently points at, so a
     * reload and a publish use the same document the user sees. The entry
     * it moves to is kept with the queue, so a reload shows it even when it
     * was never saved from this device, as the draft it opened was not.
     *
     * @param {{snapshotId?: string, commitId?: string, entry?: object}} target
     *   server snapshot, and the history entry it is
     * @returns {Promise<object>} state
     */
    async moveCursor(target) {
      if (!record) {
        return state;
      }

      const entry = target.entry;

      // Older than any edit saved from here, so it goes first.
      if (entry && !record.entries.some((item) => item.id === entry.id)) {
        record.entries = [
          {
            id: entry.id,
            label: entry.label,
            snapshot: entry.snapshot,
            serverSnapshotId: entry.serverSnapshotId,
          },
          ...record.entries,
        ];
      }

      record.queue = [
        ...record.queue,
        {
          opId: `cursor-${target.snapshotId || target.commitId}-${record.queue.length}`,
          kind: 'cursor',
          snapshotId: target.snapshotId,
          commitId: target.commitId,
        },
      ];
      record.changedAt = now();

      await persist();

      return flush();
    },

    /**
     * Blocks replay when locally queued work was based on another ETag.
     *
     * @param {string} [message]
     * @param {object} [options] server: the draft as the server holds it
     *   now (see serverCopyOf), which the store then merges with the queue
     *   (see onConflict)
     * @returns {object} state
     */
    conflict(
      message = 'This draft changed on the server since your local edits.',
      { server = null } = {},
    ) {
      cancelRetry();
      serverCopy = server;

      const blocked = emit({
        status: 'conflict',
        message,
        lastModifiedBy: server?.lastModifiedBy || '',
        otherTab: false,
      });

      if (serverCopy) {
        onConflict(serverCopy);
      }

      return blocked;
    },

    /** @returns {object|null} the server copy the conflict carries */
    get serverCopy() {
      return serverCopy;
    },

    /**
     * Puts the merge of the queue's changes with the server copy of a
     * conflict in place of the queue: the record goes on from that copy's
     * ETag and head, its entries are the history given (the server copy,
     * then the merged document), and its queue is one snapshot of the last
     * entry, sent on the next flush. The conflict ends.
     *
     * @param {object} merged etag and head of the server copy; entries: the
     *   history entries ({id, label, snapshot, serverSnapshotId}), the last
     *   one the merged document
     * @returns {Promise<object>} state
     */
    async rebase({ etag, head, entries }) {
      const last = entries?.at(-1);

      if (!record || disposed || !etag || !last) {
        return state;
      }

      cancelRetry();
      serverCopy = null;
      record.etag = etag;
      record.serverHead = head || null;
      record.entries = entries.map((entry) => ({
        id: entry.id,
        label: entry.label,
        snapshot: entry.snapshot,
        serverSnapshotId: entry.serverSnapshotId,
      }));
      record.cursor = record.entries.length - 1;
      record.queue = [
        {
          opId: last.id,
          kind: 'snapshot',
          commitId: last.id,
          label: last.label,
        },
      ];
      record.changedAt = now();
      retries = 0;

      await persist();

      return emit({
        status: 'idle',
        etag,
        message: '',
        lastModifiedBy: '',
        otherTab: false,
      });
    },

    /**
     * Adopts a server ETag (after a reload or an out-of-band write).
     *
     * @param {string} etag
     */
    async setETag(etag) {
      if (record) {
        record.etag = etag;
        await persist();
      }

      return emit({ etag });
    },

    /**
     * Adopts the ETag of a change that left the content as it was, such as
     * the owner's own change of who the draft is shared with: only when the
     * draft's head is the one the server last confirmed to this queue, and
     * the queue is not blocked. The ETag of a draft someone else saved
     * meanwhile would let this queue write over their save, so it is not
     * taken: the next save then meets the conflict, and reads the draft
     * again (see sendChecked). Call it with nothing being sent (see hold
     * and idle).
     *
     * @param {object} draft the draft the change answered with
     * @param {string} etag its ETag
     * @returns {Promise<boolean>} whether the ETag was taken
     */
    async adoptIfSameHead(draft, etag) {
      if (
        !record ||
        !etag ||
        ['conflict', 'forbidden'].includes(state.status) ||
        !sameHead(headOf({ draft }), record.serverHead)
      ) {
        return false;
      }

      record.etag = etag;
      await persist();
      emit({ etag });

      return true;
    },

    /**
     * Adopts the ETag and head of a change made from here that left the
     * content as it was but not the head, such as deleting an older
     * snapshot: only when the change was sent with the ETag this queue
     * holds, so the draft it answered with follows from the one the server
     * last confirmed here, and the queue is not blocked. Call it with
     * nothing being sent (see hold and idle).
     *
     * @param {string} sentEtag the ETag the change was sent with
     * @param {object} draft the draft the change answered with
     * @param {string} etag its ETag
     * @returns {Promise<boolean>} whether the ETag was taken
     */
    async adoptAfter(sentEtag, draft, etag) {
      if (
        !record ||
        !etag ||
        record.etag !== sentEtag ||
        ['conflict', 'forbidden'].includes(state.status)
      ) {
        return false;
      }

      record.etag = etag;
      record.serverHead = headOf({ draft });
      await persist();
      emit({ etag });

      return true;
    },

    /**
     * Discards local state after the user chose to reload the server copy.
     * Local commits are dropped only on this explicit choice.
     */
    async discardLocal() {
      if (!record) {
        return state;
      }

      record.queue = [];
      record.entries = [];
      serverCopy = null;
      await persist();

      return emit({ status: 'saved', message: '', accessLost: '' });
    },

    /**
     * Saves the local history as a brand new draft. This is the only
     * conflict resolution that keeps local work: the conflicting draft is left
     * untouched, so no other editor's changes are overwritten.
     *
     * The history saved is the one the user steps through (the editor's
     * undo history, when given), in order, and the new draft's cursor is put
     * on the entry on screen, so undo, redo and publish there act on what the
     * user sees. The new draft belongs to whoever saves it: the server
     * refuses to create one for anybody else, such as the owner of a shared
     * draft.
     *
     * The new draft forks the conflicting one (forkOf), so the server gives
     * it that draft's source and last publication: publishing it can update
     * what that draft published or was opened from. Should that draft be gone, or no
     * longer readable, the history is still saved, as a draft of its own.
     *
     * @param {object} [options] title; entries and index: the history to
     *   save and its current entry (the queue's own entries by default)
     * @returns {Promise<object>} new draft envelope, with snapshotIds: the
     *   new draft's snapshot id of each entry, by entry id; and stamps: the
     *   stamp the server answered each entry's save with, by entry id
     */
    async forkLocalHistory(options = {}) {
      const entries = options.entries || record?.entries || [];

      if (!record || entries.length === 0) {
        throw new Error('There is no local history to save.');
      }

      const last = entries.length - 1;
      const index = Math.min(Math.max(options.index ?? last, 0), last);
      const [first, ...rest] = entries;
      const request = {
        title:
          options.title ||
          first.snapshot?.metadata?.name ||
          'Recovered diagram',
        document: first.snapshot,
        // An editor history starts at the document as opened, which names
        // no edit.
        summary: first.label === 'initial' ? undefined : first.label,
      };
      const created = await api
        .createDraft({
          ...request,
          forkOf: `${record.owner}/${record.draftId}`,
        })
        .catch((error) => {
          if (classifyError(error) !== 'missing') {
            throw error;
          }

          return api.createDraft(request);
        });

      let etag = created.etag;
      // The draft as the last request left it.
      let latest = created;
      const owner = created.draft?.owner || actor;
      const draftId = created.draft?.id;
      const snapshotIds = new Map([[first.id, snapshotIdOf(created)]]);
      const stamps = new Map([[first.id, stampOf(created)]]);

      for (const entry of rest) {
        latest = await api.appendSnapshot(
          owner,
          draftId,
          { document: entry.snapshot, summary: entry.label },
          etag,
        );

        etag = latest.etag || etag;
        snapshotIds.set(entry.id, snapshotIdOf(latest));
        stamps.set(entry.id, stampOf(latest));
      }

      // Appending leaves the cursor on the last entry. The one on screen may
      // be earlier, with a redo branch after it.
      if (index < last) {
        latest = await api.moveCursor(
          owner,
          draftId,
          { snapshotId: snapshotIds.get(entries[index].id) },
          etag,
        );

        etag = latest.etag || etag;
      }

      const previous = record.key;

      record = {
        key: ownKey(owner, draftId),
        tab,
        actor,
        owner,
        draftId,
        etag,
        serverHead: headOf(latest),
        cursor: index,
        entries: entries.map((entry) => ({
          id: entry.id,
          label: entry.label,
          // As the new draft stores it.
          snapshot: stamps.get(entry.id)
            ? withStamp(entry.snapshot, stamps.get(entry.id))
            : entry.snapshot,
          serverSnapshotId: snapshotIds.get(entry.id),
        })),
        queue: [],
        updatedAt: now(),
      };
      stored = new Set();
      restamped = new Set();
      hasRecord = false;
      held = { entries: new Set(), ops: new Set() };
      copiedOps = null;
      serverCopy = null;

      // The conflicting draft's queue lives on in the new draft. Should its
      // record stay behind, reopening that draft offers the same choice again.
      // It is removed before this tab leaves the other tabs with that draft
      // open: they read the records again when a tab leaves (see left in
      // tabs.js), and would list one still there as a closed tab's changes.
      try {
        await store?.remove(previous);
      } catch {}

      connect(owner, draftId);
      emit({ status: 'saved', etag, message: '', accessLost: '' });

      return {
        ...created,
        etag,
        draft: { ...created.draft, ...latest.draft, id: draftId, owner },
        snapshotIds,
        stamps,
      };
    },

    /**
     * Retries a blocked queue (after the user fixed permissions, or manually).
     * A send already under way is waited for first, so the state returned
     * is the outcome of this retry, not of the attempt before it.
     *
     * @returns {Promise<object>} state
     */
    async retry() {
      if (inflight) {
        await inflight;
      }

      retries = 0;

      // An explicit retry sends a refused snapshot again: whatever the
      // server objected to may have changed since.
      if (record?.queue[0]?.rejected) {
        delete record.queue[0].rejected;
      }

      if (['conflict', 'forbidden', 'error'].includes(state.status)) {
        serverCopy = null;
        emit({ status: 'idle', message: '', accessLost: '' });
      }

      return flush();
    },

    flush,

    /**
     * Holds the queue: edits are still recorded and queued, but nothing is
     * sent until every hold is released. A publish holds it, so no save
     * lands between the publish and the ETag it answers with.
     *
     * @returns {Function} release, which sends whatever was queued meanwhile
     */
    hold() {
      let released = false;

      holds += 1;

      return () => {
        if (released) {
          return state;
        }

        released = true;
        holds -= 1;

        return holds === 0 ? flush() : state;
      };
    },

    /** @returns {Promise<void>} once no send is under way */
    async idle() {
      while (inflight) {
        await inflight;
      }
    },

    /** @returns {number} operations the server has confirmed so far */
    get sent() {
      return sent;
    },

    keepForUnload,

    /**
     * Sends the queue's states and saves to other callbacks, and tells the
     * other tabs whether the draft is still open here: a draft closed for
     * the drafts goes on sending in the background (see
     * createBackgroundSaves in leave.js).
     *
     * @param {object} callbacks onState, onDraft, onConflict
     * @param {object} [options] open: whether the draft is open in the editor
     */
    observe(callbacks = {}, { open = true } = {}) {
      onState = callbacks.onState || (() => {});
      onDraft = callbacks.onDraft || (() => {});
      onConflict = callbacks.onConflict || (() => {});
      coordinator?.setOpen(open);
    },

    /**
     * Tells every tab which version of the draft's unsaved changes the user
     * chose to save (see applyChoice in tabs.js); this queue sends its own
     * only if they are the ones chosen.
     *
     * @param {string} id a version's id: `tab:<id>` or `record:<key>`
     */
    chooseVersion(id) {
      coordinator?.choose(id);
    },

    /**
     * Whether another tab with this id has the draft open (see live in
     * tabs.js).
     *
     * @param {string} id
     * @returns {Promise<boolean>}
     */
    async tabOpen(id) {
      return coordinator ? coordinator.live(id) : false;
    },

    /**
     * Reads the queues closed tabs left again, and has the other tabs read
     * them, once some were saved as new drafts or taken.
     *
     * @returns {Promise<void>}
     */
    async rescan() {
      await coordinator?.scan();
      coordinator?.recordsChanged();
    },

    /**
     * Starts listening for connectivity changes so a queue parked offline
     * drains as soon as the browser reconnects, and for the page being
     * left, which keeps what the store may not hold yet (see
     * keepForUnload).
     *
     * @param {object} [target] window-like event target
     */
    listen(target = typeof window === 'undefined' ? null : window) {
      if (!target || detachOnline) {
        return () => {};
      }

      const online = () => {
        if (disposed) {
          return;
        }

        emit({ online: true });
        retries = 0;
        flush();
      };
      const offline = () => {
        if (!disposed) {
          emit({ status: 'offline', online: false });
        }
      };

      target.addEventListener('online', online);
      target.addEventListener('offline', offline);
      target.addEventListener('pagehide', keepForUnload);

      detachOnline = () => {
        target.removeEventListener('online', online);
        target.removeEventListener('offline', offline);
        target.removeEventListener('pagehide', keepForUnload);
        detachOnline = null;
      };

      return detachOnline;
    },

    /** Stops listeners and pending retries, and leaves the other tabs. */
    dispose() {
      disposed = true;
      cancelRetry();
      coordinator?.close();
      coordinator = null;

      if (detachOnline) {
        detachOnline();
      }
    },
  };
}
