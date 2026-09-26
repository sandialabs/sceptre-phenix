// What Builder Flow keeps in this browser, and what logout clears of it.
//
// The Builder keeps drafts that are not saved yet in IndexedDB, and in
// localStorage under phenix.builder.* the viewer's preferences (theme, pane
// widths, shortcuts, settings) and the recent commands, which name drafts,
// their owners and nodes by id; once loaded, its modules also hold the
// drafts listed for the user and the open diagram in memory. What belongs
// to the user may not outlive the session on a shared workstation, so
// logout clears it, whether or not the Builder is open. The preferences say
// nothing about the user or their work, and stay for this browser.
//
// This module is loaded with the app, so it stays small: the Builder's
// modules register what they hold in memory when they are first loaded, and
// a Builder that never opened has nothing in memory to clear.

import { clearBuilderDatabase } from './idb.js';

const BUILDER_STORAGE_PREFIX = 'phenix.builder.';

/**
 * The phenix.builder.* keys logout leaves in place: preferences of this
 * browser, which hold no names, ids or content. They are listed here rather
 * than registered by the modules that own them, so a Builder that never
 * opened keeps them too. Any other key under the prefix is cleared, so a
 * new one stays private until it is added here as a preference.
 */
export const BUILDER_PREFERENCE_KEYS = Object.freeze([
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
