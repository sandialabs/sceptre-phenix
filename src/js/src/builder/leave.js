// Leaving a draft, or reading it whole, with work the server does not have.
//
// Leaving (Back to drafts, another page, Upload replacing the diagram, a
// reload or closing the tab) and reading the whole diagram (Publish,
// Export) first save what the user typed in the Inspector and did not
// apply: the view's saveUnapplied applies it as one edit named
// SAVED_UNAPPLIED (see history.js), which the save queue then sends like
// any other. Edits that fail their checks cannot be applied: leaving then
// asks, naming their fields, and Publish and Export say what blocks them.
// A reload or a closed tab cannot wait for the local store's write either:
// what it may not hold yet is copied at once (see keepForUnload in
// autosave.js).

import { listOf } from './announce.js';

// How long leaving waits for a save under way before it asks.
export const LEAVE_SAVE_WAIT_MS = 2000;

/**
 * What keeps the Inspector's unapplied edits from being saved, published or
 * exported.
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
 * What Publish or Export says while the Inspector holds edits it cannot
 * apply, in place of leaving them out.
 *
 * @param {{title: string, fields: string[]}} unapplied
 * @param {string} done 'published' or 'exported'
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
 *   applies the unapplied edits at once; returns what cannot be applied
 * @param {(unapplied: object|null) => Promise<boolean>} options.ask asks
 *   whether to leave; resolves true to leave
 * @param {() => boolean} [options.sessionOver] whether the session has
 *   ended or its token has expired
 * @param {number} [options.waitMs]
 * @returns {{unsavedWork: Function, mayLeave: Function,
 *   mayFollowLink: Function, beforeUnload: Function}}
 */
export function createLeaveGuard({
  store,
  editing,
  saveUnapplied,
  ask,
  sessionOver = () => false,
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

  // Applies the unapplied edits; says whether that made an edit.
  function save() {
    const before = store.historyVersion;
    const unapplied = saveUnapplied() || null;

    return { unapplied, applied: store.historyVersion !== before };
  }

  // A save that fails says so in the save state; leaving still asks.
  async function waitForSave() {
    let timer;

    await Promise.race([
      store.saveNow().catch(() => null),
      new Promise((resolve) => {
        timer = setTimeout(resolve, waitMs);
      }),
    ]);
    clearTimeout(timer);
  }

  async function askToLeave({ saving } = {}) {
    const { unapplied, applied } = save();

    if (applied || unsavedWork()) {
      saving?.();
      await waitForSave();
    }

    if (!unapplied && !unsavedWork()) {
      return true;
    }

    return ask(unapplied);
  }

  let check = null;

  /**
   * Whether the draft may be left: for the drafts, another page, or a new
   * draft (Upload). The unapplied edits are saved and what is not saved
   * yet is sent first; if the server still lacks some of it, or edits
   * cannot be applied, the user is asked. Asked twice at once, it asks
   * once.
   *
   * @param {object} [options] saving: called once it waits for a save
   * @returns {Promise<boolean>}
   */
  function mayLeave(options) {
    check ||= askToLeave(options).finally(() => {
      check = null;
    });

    return check;
  }

  return {
    unsavedWork,
    mayLeave,

    /**
     * Whether a link out of the Builder may be followed: as mayLeave, but
     * without asking once the session is over. The logout, or its warning,
     * which saves and counts the same work, comes instead (see
     * utils/logout.js).
     *
     * @returns {boolean|Promise<boolean>}
     */
    mayFollowLink() {
      return sessionOver() || mayLeave();
    },

    /**
     * Closing or reloading the tab cannot wait: the unapplied edits are
     * applied at once, into the history and the local queue; what of the
     * queue the local store may not hold yet is copied at once (see
     * keepForUnload in autosave.js); and the browser asks in its own words
     * while the server cannot have the edits yet. It asks too when the copy
     * does not fit, so the user can stay until the local store holds the
     * edits: leaving then can lose them.
     *
     * @param {Event} event beforeunload
     * @returns {boolean} whether the browser asks
     */
    beforeUnload(event) {
      const { unapplied, applied } = save();
      const kept = store.autosave?.keepForUnload?.() ?? true;

      if (!unapplied && !applied && !unsavedWork() && kept) {
        return false;
      }

      event.preventDefault();
      // Some browsers ask only when returnValue is set.
      event.returnValue = '';

      return true;
    },
  };
}
