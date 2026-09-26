// Autosave: an ordered, per-commit persistence queue.
//
// Every semantic edit (a "commit") is recorded locally first and then sent to
// the server as its own snapshot, in the order it was made. Commits are never
// debounced or coalesced: the server history is meant to be the same history
// the user can step through locally, so collapsing two edits into one snapshot
// would silently lose an undo step.
//
// Concurrency is handled with ETags only. A conflict stops the queue and is
// surfaced to the user, who may either reload the server copy or save their
// local history as a new draft. There is no code path that overwrites a draft
// whose ETag we no longer hold.
//
// This queue is single-editor by design. There is no presence, no live
// collaboration, no heartbeat and no polling: the only requests it makes are
// the draft snapshot append and the draft history cursor move, both of which
// are ordered, awaited and carry If-Match. "Cursor" here means the position in
// the draft's own undo history, not a collaborator's caret.

import { classifyError, errorMessage, sentence } from './api.js';
import { DEFAULT_HISTORY_LIMIT } from './history.js';
import { draftKey } from './idb.js';

export const RETRY_DELAYS = [1000, 2000, 5000, 15000, 30000];

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
      return `Could not save. ${why} Use Export to keep a copy of the diagram.`;
    case 'pruned':
      return 'Could not save your last undo or redo: the server no longer keeps that step. Your next edit saves the diagram as it is shown.';
    default:
      return `Could not save. ${why} Remove part of the diagram, or use Export to keep a copy.`;
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
  };
}

function changes(count) {
  return `${count} change${count === 1 ? '' : 's'}`;
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
        ? `Offline: ${changes(state.pending)} not stored anywhere yet. Keep this tab open; saving retries automatically.`
        : `Offline: ${changes(state.pending)} kept on this device. Saving retries automatically.`;
    case 'conflict':
      return 'This draft changed on the server';
    case 'forbidden':
      return 'You cannot save changes to this draft';
    case 'error':
      return state.message || 'Changes could not be saved';
    default:
      return state.pending > 0
        ? `${state.pending} unsaved change${state.pending === 1 ? '' : 's'}`
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

/**
 * Creates the autosave queue.
 *
 * @param {object} options api, store, actor, onState, onDraft, now, setTimeout,
 *   clearTimeout, isOnline, addOnlineListener, historyLimit
 * @returns {object} queue
 */
export function createAutosave(options = {}) {
  const {
    api,
    store,
    actor = '',
    onState = () => {},
    onDraft = () => {},
    now = () => new Date().toISOString(),
    historyLimit = DEFAULT_HISTORY_LIMIT,
  } = options;

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
  // While above zero, nothing is sent (see hold).
  let holds = 0;
  // Operations the server has confirmed, so a caller can tell whether any
  // was sent while it waited on a request of its own.
  let sent = 0;

  const emit = (patch = {}) => {
    state = { ...state, ...patch, pending: record ? record.queue.length : 0 };
    if (!disposed) {
      onState(state);
    }

    return state;
  };

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

    record.updatedAt = now();

    try {
      if (record.queue.length === 0) {
        if (hasRecord) {
          hasRecord = false;
          stored = new Set();
          await store.remove(record.key);
        }
      } else {
        const needed = new Set(record.queue.map((op) => op.commitId));
        const entries = record.entries.filter((entry) => needed.has(entry.id));
        const write = entries.filter((entry) => !stored.has(entry.id));
        const drop = [...stored].filter((id) => !needed.has(id));

        hasRecord = true;
        stored = new Set(entries.map((entry) => entry.id));
        await store.put({ ...record, entries }, { write, drop });
      }

      if (state.storageFailed) {
        emit({ storageFailed: false });
      }
    } catch {
      // What the store holds is unknown now: the next write writes it all.
      stored = new Set();
      hasRecord = true;

      if (!state.storageFailed) {
        emit({ storageFailed: true });
      }
    }
  };

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
        const envelope = await send(op);

        sent += 1;
        record.queue.shift();
        record.etag = envelope.etag || record.etag;

        if (op.kind === 'snapshot') {
          const entry = record.entries.find((item) => item.id === op.commitId);
          const snapshotId = snapshotIdOf(envelope);

          if (entry && snapshotId) {
            entry.serverSnapshotId = snapshotId;
          }
        }

        await persist();
        if (!disposed) {
          onDraft(envelope, op);
        }
      }

      retries = 0;
      cancelRetry();

      return emit({ status: 'saved', lastSavedAt: now(), etag: record.etag });
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

        return emit({ status: kind, message });
      }

      // The session ended (401). Sending again succeeds once the user is
      // signed in again, so the queue stays retryable, but not on a timer:
      // the same credentials are refused every time.
      if (kind === 'unauthenticated') {
        cancelRetry();

        return emit({
          status: 'error',
          retryable: true,
          message:
            'Could not save: your session has ended. Use Export to keep a copy of the diagram, then sign in again.',
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

  async function send(op) {
    if (op.kind === 'cursor') {
      const snapshotId =
        op.snapshotId ||
        record.entries.find((item) => item.id === op.commitId)
          ?.serverSnapshotId;

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
     * @param {object} draft owner, draftId, etag, entries, cursor
     */
    async attach(draft) {
      const key = draftKey(actor, draft.owner, draft.draftId);
      // Any log this device already holds for the draft is kept unless the
      // caller passes a replacement: attaching must never be the reason local
      // work disappears.
      const explicit = draft.entries !== undefined || draft.queue !== undefined;
      // Read either way: what the store holds is not written again.
      const existing = await this.recover(draft.owner, draft.draftId);
      const hasPending = !explicit && (existing?.queue || []).length > 0;

      record = {
        key,
        actor,
        owner: draft.owner,
        draftId: draft.draftId,
        etag: hasPending ? existing.etag : draft.etag || null,
        cursor: draft.cursor ?? existing?.cursor ?? 0,
        entries: draft.entries ?? existing?.entries ?? [],
        queue: draft.queue ?? existing?.queue ?? [],
        updatedAt: now(),
      };
      stored = new Set((existing?.entries || []).map((entry) => entry.id));
      hasRecord = Boolean(existing);

      await persist();

      return emit({
        status: record.queue.length > 0 ? 'idle' : 'saved',
        etag: record.etag,
        message: '',
      });
    },

    /**
     * Reads any locally stored record for a draft, so an interrupted session
     * can restore its full ordered history and pending queue.
     *
     * @param {string} owner
     * @param {string} draftId
     * @returns {Promise<object|null>}
     */
    async recover(owner, draftId) {
      if (!store) {
        return null;
      }

      const found = await store.get(draftKey(actor, owner, draftId));

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

      await persist();

      return flush();
    },

    /** Blocks replay when locally queued work was based on another ETag. */
    conflict(
      message = 'This draft changed on the server since your local edits.',
    ) {
      cancelRetry();

      return emit({ status: 'conflict', message });
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
     * @param {object} [options] title; entries and index: the history to
     *   save and its current entry (the queue's own entries by default)
     * @returns {Promise<object>} new draft envelope, with snapshotIds: the
     *   new draft's snapshot id of each entry, by entry id
     */
    async forkLocalHistory(options = {}) {
      const entries = options.entries || record?.entries || [];

      if (!record || entries.length === 0) {
        throw new Error('There is no local history to save.');
      }

      const last = entries.length - 1;
      const index = Math.min(Math.max(options.index ?? last, 0), last);
      const [first, ...rest] = entries;
      const created = await api.createDraft({
        title: options.title || first.snapshot?.name || 'Recovered diagram',
        document: first.snapshot,
        // An editor history starts at the document as opened, which names
        // no edit.
        summary: first.label === 'initial' ? undefined : first.label,
      });

      let etag = created.etag;
      // The draft as the last request left it.
      let latest = created;
      const owner = created.draft?.owner || actor;
      const draftId = created.draft?.id;
      const snapshotIds = new Map([[first.id, snapshotIdOf(created)]]);

      for (const entry of rest) {
        latest = await api.appendSnapshot(
          owner,
          draftId,
          { document: entry.snapshot, summary: entry.label },
          etag,
        );

        etag = latest.etag || etag;
        snapshotIds.set(entry.id, snapshotIdOf(latest));
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
        key: draftKey(actor, owner, draftId),
        actor,
        owner,
        draftId,
        etag,
        cursor: index,
        entries: entries.map((entry) => ({
          id: entry.id,
          label: entry.label,
          snapshot: entry.snapshot,
          serverSnapshotId: snapshotIds.get(entry.id),
        })),
        queue: [],
        updatedAt: now(),
      };
      stored = new Set();
      hasRecord = false;

      // The conflicting draft's queue lives on in the new draft. Should its
      // record stay behind, reopening that draft offers the same choice again.
      try {
        await store?.remove(previous);
      } catch {}

      emit({ status: 'saved', etag, message: '' });

      return {
        ...created,
        etag,
        draft: { ...created.draft, ...latest.draft, id: draftId, owner },
        snapshotIds,
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
        emit({ status: 'idle', message: '' });
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

    /**
     * Starts listening for connectivity changes so a queue parked offline
     * drains as soon as the browser reconnects.
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

      detachOnline = () => {
        target.removeEventListener('online', online);
        target.removeEventListener('offline', offline);
        detachOnline = null;
      };

      return detachOnline;
    },

    /** Stops listeners and pending retries. */
    dispose() {
      disposed = true;
      cancelRetry();

      if (detachOnline) {
        detachOnline();
      }
    },
  };
}
