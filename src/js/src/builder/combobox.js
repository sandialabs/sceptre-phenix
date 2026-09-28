// The Share dialog's user field: an editable combobox with list
// autocomplete (WAI-ARIA APG) over the users the draft's owner may share it
// with. This module holds its logic, so it is tested without a browser:
// which users it lists, how they are named, and what each key does.

import { matchItem } from './fuzzy.js';

/**
 * How the list names a user: "Name (username)", or the username alone when
 * the user has no name.
 *
 * @param {{username: string, name?: string}} user
 * @returns {string}
 */
export function userLabel(user) {
  return user?.name ? `${user.name} (${user.username})` : user?.username || '';
}

/**
 * The user the field's text names: by their username, or by the label the
 * list gives them (see userLabel), as choosing them from it leaves.
 *
 * @param {{username: string, name?: string}[]} users
 * @param {string} text
 * @returns {{username: string, name: string}|null}
 */
export function findUser(users, text) {
  const wanted = String(text ?? '').trim();

  return (
    (users || []).find(
      (user) => user?.username === wanted || userLabel(user) === wanted,
    ) || null
  );
}

/**
 * The users to list for what was typed, best match first: by username, then
 * by name; with nothing typed, all of them, by username. Users in `exclude`
 * (the owner and anyone listed already) are left out.
 *
 * @param {{username: string, name?: string}[]} users
 * @param {string} query
 * @param {object} [options] exclude: usernames; limit
 * @returns {{username: string, name: string}[]}
 */
export function userOptions(
  users,
  query,
  { exclude = [], limit = Infinity } = {},
) {
  const skip = new Set(exclude);
  const text = String(query ?? '').trim();
  const scored = [];

  for (const user of users || []) {
    if (!user?.username || skip.has(user.username)) {
      continue;
    }

    const found = matchItem(
      {
        title: user.username,
        fields: user.name ? [{ label: 'Name', value: user.name }] : [],
      },
      text,
    );

    if (found) {
      scored.push({ user, score: found.score });
    }
  }

  return scored
    .sort(
      (a, b) =>
        b.score - a.score || a.user.username.localeCompare(b.user.username),
    )
    .slice(0, limit)
    .map(({ user }) => ({ username: user.username, name: user.name || '' }));
}

/**
 * What a key does in the field.
 *
 * @param {{key: string, altKey?: boolean, ctrlKey?: boolean, metaKey?: boolean}} event
 * @param {{expanded: boolean, active: number, count: number}} state the list
 *   shown, the suggestion in view (-1 for none), and how many there are
 * @returns {{action: 'open'|'move'|'choose'|'close'|'add', index?: number,
 *   prevent: boolean}|null} null leaves the key to the browser; prevent says
 *   to cancel its default
 */
export function comboboxKey(event, { expanded, active, count }) {
  if (event.ctrlKey || event.metaKey) {
    return null;
  }

  switch (event.key) {
    case 'ArrowDown':
      if (event.altKey) {
        return { action: 'open', prevent: true };
      }

      if (count === 0) {
        return null;
      }

      return {
        action: 'move',
        index: expanded && active >= 0 ? (active + 1) % count : 0,
        prevent: true,
      };
    case 'ArrowUp':
      if (event.altKey || count === 0) {
        return null;
      }

      return {
        action: 'move',
        index:
          expanded && active >= 0 ? (active - 1 + count) % count : count - 1,
        prevent: true,
      };
    case 'Enter':
      return expanded && active >= 0
        ? { action: 'choose', index: active, prevent: true }
        : { action: 'add', prevent: true };
    case 'Escape':
      // A closed list leaves Escape to the dialog, which it closes.
      return expanded ? { action: 'close', prevent: true } : null;
    case 'Tab':
      return { action: 'close', prevent: false };
    default:
      return null;
  }
}
