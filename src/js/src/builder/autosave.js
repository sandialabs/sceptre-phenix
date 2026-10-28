// Autosave: an ordered, per-commit persistence queue. Each edit is stored
// locally, then sent as its own snapshot. Never coalesce edits: the server
// history must be the undo history. Only ETags control concurrency, and no
// path overwrites a draft whose ETag the queue does not hold. A conflict is
// handled in sendChecked and merged by the store (see merge.js, rebase).

import { count } from './announce.js';
import { classifyError, errorMessage, sentence, unanswered } from './api.js';
import { DEFAULT_HISTORY_LIMIT } from './history.js';
import { draftKey, tabRecordKey } from './idb.js';
import { metadataOf, STAMP_KEYS, withStamp } from './model.js';
import { isPlainObject } from './values.js';

export const RETRY_DELAYS = [1000, 2000, 5000, 15000, 30000];

// How many times in a row one operation is sent again after a conflict
// that left the content as it was.
const MAX_REBASES = 2;

/** Save-state text for a failure that a retry cannot fix: the reason, then what to do. */
function blockedMessage(kind, reason) {
  const why = sentence(reason);

  switch (kind) {
    case 'invalid':
      return `Could not save your last change. ${why} When you fix the diagram, the Builder saves it again.`;
    case 'missing':
      return 'Could not save: someone deleted this draft from the server. Select Download to keep a copy of the diagram.';
    case 'pruned':
      return 'Could not save your last undo or redo: the server no longer keeps that step. Your next edit saves the diagram as it is shown.';
    default:
      return `Could not save. ${why} Remove part of the diagram, or select Download to keep a copy.`;
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
    etag: null,
    online: true,
    // True while this device cannot store the queue (storage blocked, full
    // or unavailable), so nothing may claim that work is kept here.
    storageFailed: false,
    // False while the error is one that sending again cannot fix (a refused
    // or oversized snapshot, a deleted draft). Then Retry saving is not
    // offered.
    retryable: true,
    // During a conflict: who saved the draft last, when the server said.
    lastModifiedBy: '',
    // When the last change was queued.
    changedAt: null,
    // The other tabs (see tabs.js): this tab's id, the tabs with the draft open,
    // the versions of unsaved changes, whether the user must choose one, and
    // whether a conflict is that choice.
    tab: '',
    tabs: [],
    versions: [],
    heldForTabs: false,
    otherTab: false,
  };
}

/** The id of the cursor's snapshot in an envelope. A save names it, and a read has the history. */
export function snapshotIdOf(envelope) {
  return (
    envelope?.history?.[envelope.cursor]?.id || envelope?.draft?.snapshotId
  );
}

/**
 * The stamp (creator, editor and times) in a create or save response, or
 * null for an envelope with none, such as a read or a cursor move.
 */
export function stampOf(envelope) {
  const stamp = envelope?.draft?.stamp;

  return isPlainObject(stamp) ? stamp : null;
}

/**
 * The head of a draft: its current snapshot, cursor and snapshot count, or
 * null. The same head means the same content, whatever else changed.
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
 * The draft read after a 412, as the response to an operation the server
 * already stored from an earlier delivery whose answer was lost. Else null.
 * A snapshot matches by its op id, a cursor move by its target and count.
 */
function alreadyStored(op, envelope, confirmed, target = '') {
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
 * How many leading operations of a recovered queue the server holds. A
 * stored snapshot carries its op id and ops go in order, so all ops up to
 * the last stored snapshot were sent.
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
 * The undo history a recovered queue leaves, replayed as the server does: a
 * snapshot drops the redo branch, and a cursor move makes its entry current,
 * adding a local entry the history lacks. A refused, replaced snapshot is skipped.
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

/** Human readable, screen-reader friendly description of the queue state. */
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
        ? `Offline: ${count(state.pending, 'change')} not stored anywhere yet. Keep this tab open. Saving retries automatically.`
        : `Offline: ${count(state.pending, 'change')} kept on this device. Saving retries automatically.`;
    case 'conflict':
      return state.otherTab
        ? "Not saved: you chose another tab's changes"
        : 'This draft changed on the server';
    case 'forbidden':
      return 'You cannot save changes to this draft';
    case 'error':
      return state.message || 'Could not save the changes';
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
 * What to announce when the queue state changes, or ''. Only a new problem
 * and the recovery from one are spoken, so the live region stays quiet on
 * each edit and retry.
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

/** Whether a waiting save-state message is out of date. A retry (idle, saving) keeps it. */
export function staleSaveMessage(announced, current) {
  return announced !== current && !['idle', 'saving'].includes(current);
}

// Why a queue stopped sending: the user chose another version of the
// draft's unsaved changes (see tabs.js).
const OTHER_TAB_CHOSEN = 'You chose to save the changes made in another tab.';

/** The server copy a conflict carries for the store to merge, or null without document or ETag. */
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
 * Creates the autosave queue. onConflict gets the server copy (see
 * serverCopyOf). tabs is builderTabs in tabs.js.
 */
export function createAutosave(options = {}) {
  const {
    api,
    store,
    actor = '',
    now = () => new Date().toISOString(),
    tabs = null,
  } = options;
  // Replaced when the draft is closed and the queue goes on sending (see
  // observe).
  let onState = options.onState || (() => {});
  let onDraft = options.onDraft || (() => {});
  let onConflict = options.onConflict || (() => {});

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
  // operations that its last finished write stored. Leaving the page copies
  // the rest at once (see keepForUnload). copiedOps lists the operations that
  // such a copy holds, while the copy can still be kept, until the store
  // holds them.
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

  // A conflict that is the user's choice of the changes of another tab says
  // so (otherTab) until the queue leaves it.
  const emit = (patch = {}) => {
    const conflicted = (patch.status || state.status) === 'conflict';

    state = {
      ...state,
      ...patch,
      pending: record ? record.queue.length : 0,
      changedAt: record?.changedAt || null,
      otherTab: conflicted && Boolean(patch.otherTab ?? state.otherTab),
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
  function ownKey(draftId) {
    return tabRecordKey(draftKey(actor, draftId), tab);
  }

  // Tells the other tabs with the draft open about this queue, and receives
  // their messages (see tabs.js): one coordinator per draft. `held` is the
  // record that this tab holds of the draft, and its first message gives the
  // record's changes. A tab that gave none, as it reloads, would let another
  // tab send its own changes.
  function connect(draftId, held) {
    const draft = draftId;

    if (!tabs || coordinator?.draft === draft) {
      return;
    }

    coordinator?.close();
    coordinator = tabs.open({
      actor,
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
            item.draftId === draftId &&
            item.key !== ownKey(draftId),
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

    // The user chose another version. This version cannot be sent to the
    // draft now, and waits to be saved as a new draft.
    if (
      coordinator.lost() &&
      record.queue.length > 0 &&
      !['conflict', 'forbidden'].includes(state.status)
    ) {
      cancelRetry();
      waitingForTabs = false;
      emit({
        status: 'conflict',
        message: OTHER_TAB_CHOSEN,
        lastModifiedBy: '',
        otherTab: true,
      });

      return;
    }

    // Nothing seems to block a held send. The queues that closed tabs left
    // are read first, because a tab that just closed may have left its
    // changes.
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
   * Takes the queue a closed tab left for the draft when it is the only
   * version of unsaved changes, as for a tab reloaded without its id (see
   * tabs.js). With more versions, the user chooses.
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

  // Local persistence never rejects into a caller. A storage failure goes in
  // the state, and the send goes on. The store keeps only what replay needs,
  // and an empty queue removes its record. A save that settles after dispose
  // is still recorded, so a reopened draft does not replay it on a stale ETag.
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

  // Records what a finished write stored (see held). An unload copy is
  // removed after the store holds each operation that it copied and that is
  // still queued. The copy does not need to keep operations sent since. The
  // store keeps the operations queued after the copy was made.
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
   * Copies, as the page closes, the ops queued since the store's last
   * finished write (see writeUnloadCopy in idb.js), because a close can stop an
   * IndexedDB write. Returns false when some queued work may not be kept.
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

  // The server never stores a snapshot it refused as invalid or too large, or
  // a cursor move to a pruned snapshot. When a later op is queued, drop the
  // refused op and the moves to it, so it does not block the queue.
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
   * A background retry keeps the problem text until it settles, so the status
   * does not flicker.
   */
  async function flush({ background = false } = {}) {
    if (disposed || !record || !record.draftId) {
      return state;
    }

    // A save is in progress. A foreground flush waits, then flushes once more,
    // so it returns how saving ended and sends what was queued meanwhile. Waiting
    // flushes share that one follow-up. A background retry returns at once.
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

      // Only a new operation can unblock a refused snapshot. If the queue
      // sent it again, the server would refuse it again.
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
        etag: record.etag,
      });
    } catch (error) {
      if (disposed) {
        return state;
      }

      const kind = classifyError(error);
      const message = errorMessage(kind, error);

      if (kind === 'offline') {
        scheduleRetry();

        return emit({ status: 'offline', online: false, message });
      }

      if (kind === 'conflict' || kind === 'forbidden') {
        cancelRetry();
        const server = kind === 'conflict' ? error?.server || null : null;

        const blocked = emit({
          status: kind,
          message,
          lastModifiedBy: error?.lastModifiedBy || '',
        });

        // The store merges the server copy with what is queued here.
        if (server) {
          onConflict(server);
        }

        return blocked;
      }

      // The session ended (401), and the Builder logs out (see watchSession
      // in api.js). The server refuses every retry, so none is scheduled.
      if (kind === 'unauthenticated') {
        cancelRetry();

        return emit({ status: 'error', retryable: false, message });
      }

      // Retrying cannot change these answers, so the queue waits for the
      // user instead of asking again on a timer.
      if (kind === 'invalid' || kind === 'missing' || kind === 'too-large') {
        cancelRetry();

        // The next edit replaces a snapshot refused as invalid or too large
        // (see dropRejected). After a reload, the kind tells why. The same
        // applies to an undo or redo to a snapshot that the server does not
        // keep now.
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

  // Reads the draft again after a conflict (see the header). A draft that
  // was deleted meanwhile throws the read's 404. A read that got no answer
  // throws its own error, so the queue sends again later and the read after
  // that refusal finds out if the server holds the operation. A read that
  // fails for another reason does not change `error`.
  async function readAgain(error) {
    try {
      return await api.getDraft(record.draftId);
    } catch (readError) {
      throw classifyError(readError) === 'missing' || unanswered(readError)
        ? readError
        : error;
    }
  }

  /**
   * Sends op. On a 412 it reads the draft: a read that holds op is its response
   * (alreadyStored), the same head takes the new ETag and sends again (at most
   * MAX_REBASES times), and else the conflict is thrown.
   */
  async function sendChecked(op) {
    for (let rebases = 0; ; rebases += 1) {
      try {
        return await send(op);
      } catch (error) {
        if (!(classifyError(error) === 'conflict' && rebases < MAX_REBASES)) {
          throw error;
        }

        const fresh = await readAgain(error);

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

      return api.moveCursor(record.draftId, { snapshotId }, record.etag);
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
    if (record.entries.length <= DEFAULT_HISTORY_LIMIT) {
      return;
    }

    const keep = new Set(record.queue.map((op) => op.commitId));

    while (
      record.entries.length > DEFAULT_HISTORY_LIMIT &&
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
     * Binds the queue to a draft, replacing any local record. key names another
     * page's record, such as a closed tab's (see tabs.js).
     */
    async attach(draft) {
      tab = tabs ? await tabs.tab() : '';

      const key = draft.key || ownKey(draft.draftId);
      // Any log that this device already holds for the draft is kept unless
      // the caller gives a replacement. Attaching must never cause local work
      // to disappear.
      const explicit = draft.entries !== undefined || draft.queue !== undefined;

      if (!draft.key) {
        const own =
          explicit || !store
            ? draft
            : await store.get(key).catch(() => undefined);

        connect(draft.draftId, own);

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
      // A record merged with an unload copy can reference snapshots that the
      // store does not have. All of them are written again, and the copy is
      // removed after that.
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

      await persist();

      return emit({
        status: record.queue.length > 0 ? 'idle' : 'saved',
        etag: record.etag,
        message: '',
        lastModifiedBy: '',
      });
    },

    /** Reads this tab's stored record of a draft after an interruption. Call it once attached. */
    async recover(draftId) {
      if (!store) {
        return null;
      }

      const found = await store.get(ownKey(draftId));

      return found || null;
    },

    /** Records one commit: stored locally first, then queued as its own snapshot. */
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
     * Queues a move of the draft's history cursor (undo/redo), so a reload and a
     * publish use the document the user sees. The target entry is kept with the
     * queue, so a reload shows it even when this device never saved it.
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

    /** Removes a queued cursor move that is not sent. False when it was sent or is sending. */
    async withdrawCursor(snapshotId) {
      const index = record
        ? record.queue.findLastIndex(
            (op) => op.kind === 'cursor' && op.snapshotId === snapshotId,
          )
        : -1;

      if (flushing || index < 0) {
        return false;
      }

      record.queue = record.queue.filter((_, at) => at !== index);
      await persist();

      if (
        record.queue.length === 0 &&
        !['conflict', 'forbidden'].includes(state.status)
      ) {
        cancelRetry();
        emit({ status: 'saved', message: '' });
      } else {
        emit();
      }

      return true;
    },

    /** Blocks replay when local work started from a different ETag. server is the copy to merge. */
    conflict(
      message = 'This draft changed on the server since your local edits.',
      { server = null } = {},
    ) {
      cancelRetry();

      const blocked = emit({
        status: 'conflict',
        message,
        lastModifiedBy: server?.lastModifiedBy || '',
        otherTab: false,
      });

      if (server) {
        onConflict(server);
      }

      return blocked;
    },

    /**
     * Replaces the queue with one snapshot of the merged document, from the
     * server copy's ETag and head, and ends the conflict.
     */
    async rebase({ etag, head, entries }) {
      const last = entries?.at(-1);

      if (!record || disposed || !etag || !last) {
        return state;
      }

      cancelRetry();
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
     * Adopts the ETag of a change with no content change, such as a publish, only
     * when the head is the one the server last confirmed. Else another user's save
     * would be overwritten. Call it while nothing is sent (see hold and idle).
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
     * Adopts the ETag and head of a change made from here that moved the head
     * but not the content, such as a snapshot delete, only when it was sent with
     * the queue's ETag. Call it while nothing is sent (see hold and idle).
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
      await persist();

      return emit({ status: 'saved', message: '' });
    },

    /**
     * Saves the local history as a new draft that forks the conflicting one, the
     * only resolution that keeps local work. Its cursor is on the entry on
     * screen. When the forked draft is gone, it is saved as a separate draft.
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
          forkOf: record.draftId,
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
      const draftId = created.draft?.id;
      const snapshotIds = new Map([[first.id, snapshotIdOf(created)]]);
      const stamps = new Map([[first.id, stampOf(created)]]);

      try {
        for (const entry of rest) {
          latest = await api.appendSnapshot(
            draftId,
            { document: entry.snapshot, summary: entry.label },
            etag,
          );

          etag = latest.etag || etag;
          snapshotIds.set(entry.id, snapshotIdOf(latest));
          stamps.set(entry.id, stampOf(latest));
        }

        // Appending leaves the cursor on the last entry. The one on screen
        // may be earlier, with a redo branch after it.
        if (index < last) {
          latest = await api.moveCursor(
            draftId,
            { snapshotId: snapshotIds.get(entries[index].id) },
            etag,
          );

          etag = latest.etag || etag;
        }
      } catch (error) {
        // A part of the history is not a copy of it. The next try makes a
        // new draft, so this one is deleted if the server lets it.
        if (draftId) {
          await api.deleteDraft(draftId, etag).catch(() => {});
        }

        throw error;
      }

      const previous = record.key;

      record = {
        key: ownKey(draftId),
        tab,
        actor,
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

      // The queue of the conflicting draft continues in the new draft. If its
      // record stays, reopening that draft offers the same choice again. It is
      // removed before this tab leaves the other tabs with that draft open.
      // They read the records again when a tab leaves (see left in tabs.js),
      // and would list a remaining record as the changes of a closed tab.
      try {
        await store?.remove(previous);
      } catch {}

      connect(draftId);
      emit({ status: 'saved', etag, message: '' });

      return {
        ...created,
        etag,
        draft: { ...created.draft, ...latest.draft, id: draftId },
        snapshotIds,
        stamps,
      };
    },

    /** Retries a blocked queue, after any send in progress, so the state is this retry's. */
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
        emit({ status: 'idle', message: '' });
      }

      return flush();
    },

    flush,

    /**
     * Holds the queue: edits queue but nothing is sent until every hold ends. A
     * publish holds it, so no save comes between it and its ETag.
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
     * Sends states and saves to other callbacks, and tells other tabs whether the
     * draft is open here. A closed draft saves on in the background (leave.js).
     */
    observe(callbacks = {}, { open = true } = {}) {
      onState = callbacks.onState || (() => {});
      onDraft = callbacks.onDraft || (() => {});
      onConflict = callbacks.onConflict || (() => {});
      coordinator?.setOpen(open);
    },

    /** Tells every tab which version of unsaved changes the user chose (applyChoice, tabs.js). */
    chooseVersion(id) {
      coordinator?.choose(id);
    },

    /** Whether another tab with this id has the draft open (see live in tabs.js). */
    async tabOpen(id) {
      return coordinator ? coordinator.live(id) : false;
    },

    /** Reads again the queues closed tabs left, and has the other tabs read them. */
    async rescan() {
      await coordinator?.scan();
      coordinator?.recordsChanged();
    },

    /**
     * Listens for reconnects, so an offline queue empties at once, and for the
     * page closing (see keepForUnload).
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
