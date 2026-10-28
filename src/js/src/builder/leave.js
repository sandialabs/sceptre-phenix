// Leaving a draft, or reading it whole, with work that the server does not
// have. Before the user leaves, publishes or downloads, the Builder applies the
// Inspector's unapplied edits as one SAVED_UNAPPLIED edit (see history.js). The
// Drafts button does not wait for the queue (see createBackgroundSaves).

import { count, listOf } from './announce.js';
import { describeState } from './autosave.js';
import { onBuilderSessionEnd } from './session.js';

// How long leaving waits for a save under way before it asks.
export const LEAVE_SAVE_WAIT_MS = 2000;

/**
 * "Your changes to <title> in the Inspector cannot be <done> until you fix
 * <fields>."
 */
export function unappliedText({ title, fields }, done = 'saved') {
  return `Your changes to ${title} in the Inspector cannot be ${done} until you fix ${listOf(fields)}.`;
}

/**
 * What Publish or Download says while the Inspector holds edits that it cannot
 * apply.
 */
export function unappliedBlock(unapplied, done) {
  return `${unappliedText(unapplied, done)} Fix or cancel them first.`;
}

/**
 * Guards leaving a draft (see the header comment). options.ask resolves true to
 * leave. Its scope is 'close' for the Drafts button, and 'all' when the user
 * also leaves the background saves of drafts closed earlier.
 */
export function createLeaveGuard({
  store,
  editing,
  saveUnapplied,
  ask,
  sessionOver = () => false,
  background = null,
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
        timer = setTimeout(resolve, LEAVE_SAVE_WAIT_MS);
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

  // The Drafts button: the queue is not waited for, as it goes on sending in
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
   * Whether the user can leave the draft for another one (Upload). It saves the
   * unapplied edits and sends the queue, and asks when work is still unsaved.
   */
  function mayLeave(options) {
    return once(() => askToLeave(options));
  }

  return {
    unsavedWork,
    backgroundWork,
    mayLeave,

    /**
     * Whether the user can close the draft for the drafts page, while the queue
     * sends in the background. A queue that storage failed to keep would be
     * lost with the page, so then it waits, as mayLeave does.
     */
    mayClose(options) {
      return store.saveState.storageFailed
        ? mayLeave(options)
        : once(askToClose);
    },

    /**
     * Whether a link out of the Builder can be followed: mayLeave, with the
     * background saves too. After the session ends, it does not ask, because
     * the logout comes next.
     */
    mayFollowLink() {
      return sessionOver() || once(() => askToLeave({ all: true }));
    },

    /**
     * Closing or reloading the tab cannot wait. It applies the unapplied edits
     * and copies the queue at once (see keepForUnload in autosave.js). The
     * browser asks while the server or the copy cannot hold the edits.
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

/** Whether a draft's queue has changes queued or a save under way. */
export function queueBusy(queue) {
  return pendingOf(queue) > 0 || queue.state.status === 'saving';
}

/**
 * What a draft's card says about its background saves, and whether its queue
 * stops: after it sent everything, or when it needs the user (a conflict, no
 * access, a refused change).
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
 * What the live region says when a draft's background saves finish or meet a
 * new problem.
 */
export function backgroundSaveAnnouncement(kind, name) {
  switch (kind) {
    case 'saved':
      return `Saved your changes to ${name}.`;
    case 'retrying':
      return `Could not save your changes to ${name} yet. Saving retries automatically.`;
    case 'waiting':
      return `Your changes to ${name} are not saved yet: another tab has changes to it too.`;
    case 'stopped':
      return `Could not save your changes to ${name}. Open it to see why.`;
    default:
      return '';
  }
}

/**
 * The saves of drafts closed with the Drafts button, which continue in the
 * background. A draft that opens again takes over its changes (see release).
 */
export function createBackgroundSaves({
  onChange = () => {},
  announce = () => {},
  saved = () => {},
} = {}) {
  // By card key: the queue (null once stopped), the draft's name and its
  // card.
  const saves = new Map();

  function publish() {
    onChange(
      Object.fromEntries([...saves].map(([key, save]) => [key, save.card])),
    );
  }

  function stop(save) {
    const { queue } = save;

    save.queue = null;

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
     */
    take(queue, { id, name }) {
      const key = id;
      const save = { queue, name, card: null };

      saves.set(key, save);
      queue.observe(
        { onState: (state) => update(key, save, state) },
        { open: false },
      );
      update(key, save, { ...queue.state, pending: pendingOf(queue) });
    },

    /**
     * Ends the saves of a draft that is about to open again or be deleted,
     * after the current send settles. The draft's own queue takes over what is
     * left.
     */
    async release(id) {
      const key = id;
      const save = saves.get(key);

      if (!save) {
        return;
      }

      saves.delete(key);
      publish();

      const { queue } = save;

      save.queue = null;

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
     * Copies at once what the local store may not hold yet, as the page closes.
     * False when some may be lost.
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
