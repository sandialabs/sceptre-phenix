// The Share dialog's username field: an editable combobox with list
// autocomplete (WAI-ARIA APG), when the role may list users. This module
// holds its logic, so it is tested without a browser: which users it
// suggests, and what each key does.

import { matchItem } from './fuzzy.js';

// The most suggestions shown at once.
export const MAX_OPTIONS = 8;

/**
 * The users to suggest for what was typed, best match first: by username,
 * then by name. Users in `exclude` (the owner and anyone listed already)
 * are left out.
 *
 * @param {{username: string, name?: string}[]} users
 * @param {string} query
 * @param {object} [options] exclude: usernames; limit
 * @returns {{username: string, name: string}[]}
 */
export function userOptions(
  users,
  query,
  { exclude = [], limit = MAX_OPTIONS } = {},
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
