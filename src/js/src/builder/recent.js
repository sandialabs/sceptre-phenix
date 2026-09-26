// The commands last run from the command palette, most recent first. The
// palette lists them when it opens and ranks them first among equal matches.
// A command that asked for choices is kept with them, by id, so "Add device
// › Router" repeats in one keystroke.
//
// Kept per browser under phenix.builder.recentCommands. Storage that is
// blocked or full is not an error: the list then lasts as long as the page.

import { onBuilderSessionEnd } from './session.js';

export const RECENT_STORAGE_KEY = 'phenix.builder.recentCommands';
export const RECENT_LIMIT = 5;

// The list as last written, which stands in for storage once storage has
// refused a write (it would still hand back the older list).
let memory = [];
let unwritable = false;

// Reading localStorage throws where site data is blocked.
function browserStorage() {
  try {
    return globalThis.window?.localStorage || null;
  } catch {
    return null;
  }
}

function clean(list) {
  return (Array.isArray(list) ? list : [])
    .filter((entry) => entry && typeof entry.id === 'string' && entry.id)
    .map((entry) => ({
      id: entry.id,
      choices: (Array.isArray(entry.choices) ? entry.choices : []).filter(
        (choice) => typeof choice === 'string',
      ),
    }))
    .slice(0, RECENT_LIMIT);
}

function same(a, b) {
  return a.id === b.id && a.choices.join('\n') === b.choices.join('\n');
}

/**
 * The recent commands, most recent first.
 *
 * @param {Storage|null} [storage] localStorage by default
 * @returns {{id: string, choices: string[]}[]}
 */
export function readRecent(storage = browserStorage()) {
  if (!storage || unwritable) {
    return memory;
  }

  try {
    return clean(JSON.parse(storage.getItem(RECENT_STORAGE_KEY) || '[]'));
  } catch {
    return memory;
  }
}

/**
 * Puts a command at the head of the recent list, with the ids of the
 * choices it ran with.
 *
 * @param {string} id command id
 * @param {string[]} [choices] choice ids, in order
 * @param {Storage|null} [storage] localStorage by default
 * @returns {{id: string, choices: string[]}[]} the new list
 */
export function rememberCommand(id, choices = [], storage = browserStorage()) {
  const entry = { id, choices: [...choices] };
  const list = [
    entry,
    ...readRecent(storage).filter((other) => !same(other, entry)),
  ].slice(0, RECENT_LIMIT);

  memory = list;

  try {
    storage?.setItem(RECENT_STORAGE_KEY, JSON.stringify(list));
  } catch {
    // Blocked or full: the list still lasts as long as the page.
    unwritable = true;
  }

  return list;
}

/**
 * Empties the recent list.
 *
 * @param {Storage|null} [storage] localStorage by default
 */
export function clearRecent(storage = browserStorage()) {
  memory = [];
  unwritable = false;

  try {
    storage?.removeItem(RECENT_STORAGE_KEY);
  } catch {
    // Nothing to clear that the page can reach.
  }
}

// Logout forgets the list, which is the user's and not a preference: the
// choices name drafts, their owners and nodes by id (see session.js).
onBuilderSessionEnd(() => clearRecent());
