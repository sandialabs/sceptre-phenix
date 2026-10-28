// What Builder keeps in this browser, and what logout clears of it. Logout, or
// the next sign-in of another user, clears the user's data so that it does not
// stay on a shared workstation. Preferences stay. The app loads this module,
// so it stays small.

import { clearBuilderDatabase } from './idb.js';
import { pageStorage } from './storage.js';

const BUILDER_STORAGE_PREFIX = 'phenix.builder.';

/**
 * The user the Builder data in this browser belongs to, written at sign-in.
 * It is not a preference, so logout clears it with the rest.
 */
export const BUILDER_USER_KEY = 'phenix.builder.user';

// The phenix.builder.* keys logout keeps: preferences that hold no names, ids
// or content. Logout clears any other key, so a new key stays private until it
// is added here.
export const BUILDER_PREFERENCE_KEYS = Object.freeze([
  'phenix.builder.minimap', // panes.js, the minimap's size
  'phenix.builder.panes', // panes.js, the side columns' widths
  'phenix.builder.settings', // settings.js, the Settings dialog's choices
  'phenix.builder.shortcuts', // keymap.js, custom keys, single-key switch
  'phenix.builder.theme', // theme.js
]);

const resets = new Set();

// Registers how a Builder module forgets what it holds in memory for the user
// when the session ends. Storage is already cleared then.
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

// Ends the Builder session: clears its keys except the preferences, resets
// module memory, then deletes the IndexedDB drafts. The first two finish before
// it returns, so a following navigation cannot show the previous user's data.
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

// Starts the session of the user signing in. It first clears what another
// user left without logout, as endBuilderSession does. The drafts database
// waits for it, so the Builder cannot read the previous user's drafts. It
// returns null when the data was this user's already.
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
