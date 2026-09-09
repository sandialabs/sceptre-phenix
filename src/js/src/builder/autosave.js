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

import { classifyError, errorMessage } from './api.js';
import { DEFAULT_HISTORY_LIMIT } from './history.js';
import { draftKey } from './idb.js';

export const RETRY_DELAYS = [1000, 2000, 5000, 15000, 30000];

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
  };
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
      return `Offline: ${state.pending} change${state.pending === 1 ? '' : 's'} kept on this device`;
    case 'conflict':
      return 'This draft changed on the server';
    case 'forbidden':
      return 'You cannot save changes to this draft';
    case 'error':
      return state.message || 'Changes could not be saved';
    default:
      return state.pending > 0 ? `${state.pending} unsaved changes` : 'Ready';
  }
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
  let retries = 0;
  let retryHandle = null;
  let detachOnline = null;
  let disposed = false;

  const emit = (patch = {}) => {
    state = { ...state, ...patch, pending: record ? record.queue.length : 0 };
    if (!disposed) {
      onState(state);
    }

    return state;
  };

  // Local persistence must never reject into a caller that cannot handle it:
  // the queue lives in memory too, so a storage failure is reported and the
  // send continues rather than raising an unhandled rejection.
  const persist = async () => {
    if (disposed || !record || !store) {
      return;
    }

    record.updatedAt = now();

    try {
      await store.put(record);
    } catch {
      emit({
        message:
          'Your work could not be stored on this device. It is still being sent to the server.',
      });
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
      flush();
    }, delay);
  };

  /**
   * Sends queued operations in order until the queue drains or is blocked.
   *
   * @returns {Promise<object>} state
   */
  async function flush() {
    if (disposed || !record || flushing || !record.owner || !record.draftId) {
      return state;
    }

    if (['conflict', 'forbidden'].includes(state.status)) {
      return state;
    }

    if (record.queue.length === 0) {
      return emit({ status: 'saved', message: '' });
    }

    if (!isOnline()) {
      return emit({ status: 'offline', online: false });
    }

    flushing = true;
    emit({ status: 'saving', online: true, message: '' });

    try {
      while (record.queue.length > 0) {
        if (disposed) {
          return state;
        }

        const op = record.queue[0];
        const envelope = await send(op);

        record.queue.shift();
        record.etag = envelope.etag || record.etag;

        if (op.kind === 'snapshot') {
          const entry = record.entries.find((item) => item.id === op.commitId);
          const snapshotId =
            envelope.history?.[envelope.cursor]?.id ||
            envelope.draft?.snapshotId ||
            envelope.draft?.history?.[envelope.draft?.cursor]?.id;

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

      scheduleRetry();

      return emit({ status: 'error', message });
    } finally {
      flushing = false;
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

    return api.appendSnapshot(
      record.owner,
      record.draftId,
      { document: entry.snapshot, summary: op.label || entry.label },
      record.etag,
    );
  }

  function trimEntries() {
    if (record.entries.length <= historyLimit) {
      return;
    }

    const keep = new Set(
      record.queue
        .filter((op) => op.kind === 'snapshot')
        .map((op) => op.commitId),
    );

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
      const existing = explicit
        ? null
        : await this.recover(draft.owner, draft.draftId);
      const hasPending = (existing?.queue || []).length > 0;

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
      // the permission problem before anything else is sent.
      if (!['conflict', 'forbidden'].includes(state.status)) {
        emit({ status: 'saving' });
      }

      return flush();
    },

    /**
     * Queues a move of the draft's *history* cursor (undo/redo) behind the
     * commits already queued. This is not a collaboration or caret signal:
     * it tells the server which snapshot the draft currently points at, so a
     * reload and a publish use the same document the user sees.
     *
     * @param {{snapshotId?: string, commitId?: string}} target server snapshot
     * @returns {Promise<object>} state
     */
    async moveCursor(target) {
      if (!record) {
        return state;
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
     * Saves the full local history as a brand new draft. This is the only
     * conflict resolution that keeps local work: the conflicting draft is left
     * untouched, so no other editor's changes are overwritten.
     *
     * @param {object} [options] title, owner
     * @returns {Promise<object>} new draft envelope
     */
    async forkLocalHistory(options = {}) {
      if (!record || record.entries.length === 0) {
        throw new Error('There is no local history to save.');
      }

      const [first, ...rest] = record.entries;
      const created = await api.createDraft({
        owner: options.owner || record.owner,
        title: options.title || first.snapshot?.name || 'Recovered diagram',
        document: first.snapshot,
        summary: first.label,
      });

      let etag = created.etag;
      const owner = created.draft?.owner || options.owner || record.owner;
      const draftId = created.draft?.id;

      for (const entry of rest) {
        const appended = await api.appendSnapshot(
          owner,
          draftId,
          { document: entry.snapshot, summary: entry.label },
          etag,
        );

        etag = appended.etag || etag;
      }

      record = {
        key: draftKey(actor, owner, draftId),
        actor,
        owner,
        draftId,
        etag,
        cursor: record.entries.length - 1,
        entries: record.entries,
        queue: [],
        updatedAt: now(),
      };

      await persist();
      emit({ status: 'saved', etag, message: '' });

      return {
        ...created,
        etag,
        draft: { ...created.draft, id: draftId, owner },
      };
    },

    /**
     * Retries a blocked queue (after the user fixed permissions, or manually).
     */
    retry() {
      retries = 0;

      if (['conflict', 'forbidden', 'error'].includes(state.status)) {
        emit({ status: 'idle', message: '' });
      }

      return flush();
    },

    flush,

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
