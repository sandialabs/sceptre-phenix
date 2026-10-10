// Leaving a draft, or reading it whole, with work that the server does not
// have.
//
// Before the user leaves (Back to drafts, another page, Upload that replaces
// the diagram, a reload or a closed tab) or reads the whole diagram (Publish,
// Download), the Builder saves what the user typed in the Inspector and did
// not apply. The view's saveUnapplied applies it as one edit named
// SAVED_UNAPPLIED (see history.js), which the save queue then sends like any
// other edit. Edits that fail their checks cannot be applied. Leaving then
// asks, and names their fields. Publish and Download tell what blocks them.
// A reload or a closed tab also cannot wait for the write to the local
// store. Thus the part of the queue that the store may not hold yet is
// copied at once (see keepForUnload in autosave.js).
//
// Back to drafts does not wait for the save queue. The queue continues to
// send in the background, and the draft's card on the landing shows its
// progress (see createBackgroundSaves). Leaving the Builder, or the tab,
// still waits for those saves, and asks when the server does not have some.

import { count, listOf } from './announce.js';
import { describeState } from './autosave.js';
import { onBuilderSessionEnd, registerQueue } from './session.js';

// How long leaving waits for a save under way before it asks.
export const LEAVE_SAVE_WAIT_MS = 2000;

/**
 * What stops the Inspector's unapplied edits from being saved, published or
 * downloaded.
 *
 * @param {{title: string, fields: string[]}} unapplied the element the
 *   edits were made on, and the fields that need fixing
 * @param {string} [done] what the edits cannot be: 'saved', 'published'
 * @returns {string} "Your changes to Device alpha in the Inspector cannot be
 *   saved until Memory is fixed."
 */
export function unappliedText({ title, fields }, done = 'saved') {
  return `Your changes to ${title} in the Inspector cannot be ${done} until ${listOf(fields)} ${fields.length === 1 ? 'is' : 'are'} fixed.`;
}

/**
 * What Publish or Download says while the Inspector holds edits that it
 * cannot apply, in place of leaving them out.
 *
 * @param {{title: string, fields: string[]}} unapplied
 * @param {string} done 'published' or 'downloaded'
 * @returns {string}
 */
export function unappliedBlock(unapplied, done) {
  return `${unappliedText(unapplied, done)} Fix or cancel them first.`;
}

/**
 * Guards leaving a draft (see the header comment).
 *
 * @param {object} options
 * @param {object} options.store the Builder store: saveState, readOnly,
 *   historyVersion, saveNow, and autosave, the save queue, if any
 * @param {() => boolean} options.editing whether a draft is open
 * @param {() => ({title: string, fields: string[]}|null)} options.saveUnapplied
 *   applies the unapplied edits at once. Returns what cannot be applied.
 * @param {(unapplied: object|null, scope?: string) => Promise<boolean>}
 *   options.ask asks whether to leave. Resolves true to leave. The scope
 *   is 'close' for Back to drafts, and 'all' when the user also leaves the
 *   background saves of drafts closed earlier.
 * @param {() => boolean} [options.sessionOver] whether the session has
 *   ended or its token has expired
 * @param {object} [options.background] createBackgroundSaves()
 * @param {number} [options.waitMs]
 * @returns {{unsavedWork: Function, backgroundWork: Function,
 *   mayLeave: Function, mayClose: Function, mayFollowLink: Function,
 *   beforeUnload: Function}}
 */
export function createLeaveGuard({
  store,
  editing,
  saveUnapplied,
  ask,
  sessionOver = () => false,
  background = null,
  waitMs = LEAVE_SAVE_WAIT_MS,
}) {
  // Edits the server does not have yet: queued, or being sent.
  function unsavedWork() {
    return Boolean(
      editing() &&
        !store.readOnly &&
        (store.saveState.pending > 0 || store.saveState.status === 'saving'),
    );
  }

  // Edits of drafts closed before, still being sent.
  function backgroundWork() {
    return (background?.pending() || 0) > 0;
  }

  // Applies the unapplied edits. Tells whether that made an edit.
  function save() {
    const before = store.historyVersion;
    const unapplied = saveUnapplied() || null;

    return { unapplied, applied: store.historyVersion !== before };
  }

  // A save that fails shows this in the save state. Leaving still asks.
  async function waitForSave(all) {
    let timer;

    await Promise.race([
      Promise.all([
        store.saveNow().catch(() => null),
        all ? background?.flush() : null,
      ]),
      new Promise((resolve) => {
        timer = setTimeout(resolve, waitMs);
      }),
    ]);
    clearTimeout(timer);
  }

  async function askToLeave({ saving, all = false } = {}) {
    const { unapplied, applied } = save();
    const unsent = () => unsavedWork() || (all && backgroundWork());

    if (applied || unsent()) {
      saving?.();
      await waitForSave(all);
    }

    if (!unapplied && !unsent()) {
      return true;
    }

    return all ? ask(unapplied, 'all') : ask(unapplied);
  }

  // Back to drafts: the queue is not waited for, as it goes on sending in
  // the background. Only edits the Inspector cannot apply ask.
  async function askToClose() {
    const { unapplied } = save();

    return unapplied ? ask(unapplied, 'close') : true;
  }

  let check = null;

  // Asked twice at once, it saves and asks once.
  function once(run) {
    check ||= run().finally(() => {
      check = null;
    });

    return check;
  }

  /**
   * Whether the user can leave the draft for another one (Upload). First,
   * the unapplied edits are saved and the unsaved work is sent. If the
   * server still does not have some of it, or edits cannot be applied, the
   * user is asked.
   *
   * @param {object} [options] saving: called once it waits for a save
   * @returns {Promise<boolean>}
   */
  function mayLeave(options) {
    return once(() => askToLeave(options));
  }

  return {
    unsavedWork,
    backgroundWork,
    mayLeave,

    /**
     * Whether the user can close the draft for the drafts page. The
     * unapplied edits are saved, and the queue continues to send in the
     * background (see createBackgroundSaves). Edits that cannot be applied
     * cause a question. A queue that this device cannot keep (storage
     * failed) would be lost with the page, so this waits for it, as
     * mayLeave does.
     *
     * @param {object} [options] saving: called once it waits for a save
     * @returns {Promise<boolean>}
     */
    mayClose(options) {
      return store.saveState.storageFailed
        ? mayLeave(options)
        : once(askToClose);
    },

    /**
     * Whether a link out of the Builder can be followed. This is as
     * mayLeave, and also includes the saves of drafts closed earlier. But it
     * does not ask after the session is over. Instead the logout, or its
     * warning, which saves and counts the same work, comes next (see
     * utils/logout.js).
     *
     * @returns {boolean|Promise<boolean>}
     */
    mayFollowLink() {
      return sessionOver() || once(() => askToLeave({ all: true }));
    },

    /**
     * Closing or reloading the tab cannot wait. Thus:
     *   - the unapplied edits are applied at once, into the history and the
     *     local queue.
     *   - the part of the queue that the local store may not hold yet is
     *     copied at once (see keepForUnload in autosave.js).
     *   - the browser asks in its own words while the server cannot have the
     *     edits yet. It also asks when the copy does not fit, so the user can
     *     stay until the local store holds the edits. Leaving then can lose
     *     them.
     *
     * @param {Event} event beforeunload
     * @returns {boolean} whether the browser asks
     */
    beforeUnload(event) {
      const { unapplied, applied } = save();
      const kept = store.autosave?.keepForUnload?.() ?? true;
      const keptBehind = background?.keepForUnload() ?? true;

      if (
        !unapplied &&
        !applied &&
        !unsavedWork() &&
        !backgroundWork() &&
        kept &&
        keptBehind
      ) {
        return false;
      }

      event.preventDefault();
      // Some browsers ask only when returnValue is set.
      event.returnValue = '';

      return true;
    },
  };
}

// The changes that a queue holds. Its state shows them after the local store
// has the last one. Its record shows them at once, as an edit is queued.
function pendingOf(queue) {
  return queue.record ? queue.record.queue.length : queue.state.pending;
}

/**
 * Whether a draft's queue has anything left to send: changes queued, or a
 * save under way.
 *
 * @param {object} queue
 * @returns {boolean}
 */
export function queueBusy(queue) {
  return pendingOf(queue) > 0 || queue.state.status === 'saving';
}

/**
 * What a draft's card says about the saves that continue after it closed,
 * and whether its queue stops. A queue stops when it sent everything, or
 * when it can send nothing more without the user (a conflict, no access, a
 * refused change).
 *
 * @param {object} state queue state
 * @returns {{kind: string, text: string, stop: boolean}} kind: 'saving',
 *   'saved', 'retrying' (offline or failed, sent again automatically),
 *   'waiting' (for the choice of which tab's changes to save), 'signin'
 *   (the session ended, sent again after the user signs in again) or
 *   'stopped'
 */
export function backgroundSaveCard(state) {
  const pending = state.pending || 0;

  if (state.heldForTabs && pending > 0) {
    return {
      kind: 'waiting',
      text: 'Not saved yet: another tab has changes to this draft too. Open it to choose which to save.',
      stop: false,
    };
  }

  if (state.status === 'error' && state.signInNeeded) {
    return {
      kind: 'signin',
      text: `Not saved yet: sign in again to save ${count(pending, 'change')}.`,
      stop: false,
    };
  }

  switch (state.status) {
    case 'offline':
      return { kind: 'retrying', text: describeState(state), stop: false };
    case 'error':
      return state.retryable
        ? { kind: 'retrying', text: describeState(state), stop: false }
        : {
            kind: 'stopped',
            text: `Not saved: ${count(pending, 'change')} kept on this device. Open the draft to see why.`,
            stop: true,
          };
    case 'conflict':
      return {
        kind: 'stopped',
        text: state.otherTab
          ? "Not saved: you chose another tab's changes. Open it to keep yours as a new draft."
          : 'Not saved: this draft changed on the server. Open it to keep your changes.',
        stop: true,
      };
    case 'forbidden':
      return {
        kind: 'stopped',
        text: 'Not saved: you can no longer save changes to this draft. Open it to keep a copy.',
        stop: true,
      };
    default:
      return pending > 0 || state.status === 'saving'
        ? {
            kind: 'saving',
            text:
              pending > 0 ? `Saving ${count(pending, 'change')}…` : 'Saving…',
            stop: false,
          }
        : { kind: 'saved', text: 'All changes saved.', stop: true };
  }
}

/**
 * What the live region says when the background saves of a draft change
 * state: after they finish, or on a new problem.
 *
 * @param {string} kind backgroundSaveCard()'s
 * @param {string} name the draft's name
 * @returns {string}
 */
export function backgroundSaveAnnouncement(kind, name) {
  switch (kind) {
    case 'saved':
      return `Saved your changes to ${name}.`;
    case 'retrying':
      return `Could not save your changes to ${name} yet. Saving retries automatically.`;
    case 'waiting':
      return `Your changes to ${name} are not saved yet: another tab has changes to it too.`;
    case 'signin':
      return `Your changes to ${name} are not saved yet: sign in again to save them.`;
    case 'stopped':
      return `Could not save your changes to ${name}. Open it to see why.`;
    default:
      return '';
  }
}

/**
 * The saves of drafts closed for the drafts page (Back to drafts), which
 * continue in the background. The draft's save queue continues to send, and
 * its card shows the progress (see backgroundSaveCard). A queue stops when it
 * sent everything, or when it can send nothing more without the user. When
 * the draft opens again, it takes over its changes (see release), because
 * the queue keeps them on this device. Logout also counts and sends them
 * (see registerQueue in session.js), and ends them.
 *
 * @param {object} [options] onChange: (cards) => void, with each card's
 *   kind, text and pending changes by `${owner}/${id}` (see cardKey in
 *   BuilderDrafts.vue).
 *   announce: (message) => void. saved: () => void, after a queue sent
 *   everything.
 * @returns {object}
 */
export function createBackgroundSaves({
  onChange = () => {},
  announce = () => {},
  saved = () => {},
} = {}) {
  // By card key: the queue (null once stopped), the draft's name, its
  // card, and how to unregister the queue from logout.
  const saves = new Map();

  function publish() {
    onChange(
      Object.fromEntries([...saves].map(([key, save]) => [key, save.card])),
    );
  }

  function stop(save) {
    const { queue } = save;

    save.queue = null;
    save.unregister?.();

    if (queue) {
      queue.idle().then(() => queue.dispose());
    }
  }

  function update(key, save, state) {
    if (saves.get(key) !== save || !save.queue) {
      return;
    }

    const card = backgroundSaveCard(state);
    const before = save.card?.kind;

    save.card = {
      kind: card.kind,
      text: card.text,
      pending: card.stop ? 0 : state.pending || 0,
    };

    if (before && before !== card.kind) {
      const message = backgroundSaveAnnouncement(card.kind, save.name);

      if (message) {
        announce(message);
      }
    }

    if (card.stop) {
      stop(save);

      if (card.kind === 'saved') {
        saved();
      }
    }

    publish();
  }

  const endSession = onBuilderSessionEnd(() => dispose());

  function dispose() {
    saves.forEach((save) => {
      save.unregister?.();
      save.queue?.dispose();
      save.queue = null;
    });
    saves.clear();
    publish();
  }

  // The queues still sending.
  const live = () =>
    [...saves.values()].map((save) => save.queue).filter(Boolean);

  return {
    /**
     * Takes the save queue of a draft that closes. The queue continues to send.
     *
     * @param {object} queue the draft's save queue
     * @param {object} draft owner, id, and name: as its card names it
     */
    take(queue, { owner, id, name }) {
      const key = `${owner}/${id}`;
      const save = {
        queue,
        name,
        card: null,
        unregister: registerQueue(queue),
      };

      saves.set(key, save);
      queue.observe(
        { onState: (state) => update(key, save, state) },
        { open: false },
      );
      update(key, save, { ...queue.state, pending: pendingOf(queue) });
    },

    /**
     * Ends the saves of a draft that is about to open again, or about to be
     * deleted, after the send in progress settles. The remaining work is on
     * this device, and the draft's own queue takes it over.
     *
     * @param {string} owner
     * @param {string} id
     * @returns {Promise<void>}
     */
    async release(owner, id) {
      const key = `${owner}/${id}`;
      const save = saves.get(key);

      if (!save) {
        return;
      }

      saves.delete(key);
      publish();

      const { queue } = save;

      save.queue = null;
      save.unregister?.();

      // Nothing more is sent. The send in progress settles and is recorded.
      if (queue) {
        queue.dispose();
        await queue.idle();
      }
    },

    /** @returns {number} the changes the queues still sending hold */
    pending() {
      return live().reduce((sum, queue) => sum + pendingOf(queue), 0);
    },

    /** @returns {Promise<void>} once the queues still sending have tried */
    async flush() {
      await Promise.all(live().map((queue) => queue.flush()));
    },

    /**
     * As the page closes, the work that the local store may not hold yet is
     * copied at once (see keepForUnload in autosave.js).
     *
     * @returns {boolean} false when some queued work may not be kept
     */
    keepForUnload() {
      return live()
        .map((queue) => queue.keepForUnload())
        .every(Boolean);
    },

    /** Ends every queue: what they hold stays on this device. */
    dispose() {
      endSession();
      dispose();
    },
  };
}
