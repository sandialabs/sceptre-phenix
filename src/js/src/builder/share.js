// Sharing a draft with named users, as the Share dialog edits it.
//
// The server keeps each draft's share list: who, and whether they can view
// or edit. The dialog edits a copy of it as rows, {user, access, stale,
// removed}, and saves the whole list at once. A row the user removed stays
// on screen, marked, until the list is saved, so nothing moves under the
// pointer or focus. A share whose account was deleted or made again under
// the same name (stale) no longer gives access, and starts marked removed.
//
// Everything here is pure, so it is tested without a browser.

import { count, describeNames, listOf } from './announce.js';
import { MAX_SHARES, MAX_USER_BYTES } from './limits.js';
import { utf8Length } from './text.js';

export const ACCESS_LABELS = { view: 'Can view', edit: 'Can edit' };

/**
 * @param {string} access 'view' or 'edit'
 * @returns {string} "Can view" or "Can edit"
 */
export function accessLabel(access) {
  return ACCESS_LABELS[access] || ACCESS_LABELS.view;
}

/**
 * Whether the server could take `name` as a username: not empty, not too
 * long, printable (letters, marks, numbers, punctuation, symbols and plain
 * spaces, as Go's unicode.IsPrint), and with no slash.
 *
 * @param {string} name
 * @returns {boolean}
 */
export function validUsername(name) {
  return (
    typeof name === 'string' &&
    name !== '' &&
    utf8Length(name) <= MAX_USER_BYTES &&
    /^[\p{L}\p{M}\p{N}\p{P}\p{S} ]+$/u.test(name) &&
    !name.includes('/')
  );
}

/**
 * Names the people a draft is shared with, for its card and the Share
 * button's tooltip.
 *
 * @param {Array<{user: string}|string>} shares
 * @returns {string} "bob and carol", "bob, carol and 3 others", or ''
 */
export function describeShares(shares) {
  return describeNames(
    (shares || []).map((entry) =>
      typeof entry === 'string' ? entry : entry?.user,
    ),
  );
}

/**
 * What deleting a draft does, for its confirmation: the people it is
 * shared with lose it too.
 *
 * @param {Array<{user: string}>} [shares]
 * @returns {string}
 */
export function deleteMessage(shares) {
  const who = describeShares(shares);
  const lost = who ? ` ${who} will lose access too.` : '';

  return `The draft and its whole history are removed from the server.${lost} This cannot be undone.`;
}

/**
 * What another user's listed draft lets the user do, for its card. Edit
 * access that the role does not allow (readOnly) says so.
 *
 * @param {{access?: string, readOnly?: boolean}} draft
 * @returns {string} "Can edit", "Can view", or
 *   "Can edit (your role allows viewing only)"
 */
export function listedAccess(draft) {
  const label = accessLabel(draft?.access);

  return draft?.access === 'edit' && draft.readOnly
    ? `${label} (your role allows viewing only)`
    : label;
}

/**
 * What the conflict panel says of who saved the newer version of a draft:
 * the person the server named, the user (in another tab or window), or,
 * when no one is named, someone else.
 *
 * @param {string} by who saved it (lastModifiedBy), or '' when unknown
 * @param {string} me the signed-in user
 * @returns {string}
 */
export function conflictMessage(by, me) {
  if (by && by === me) {
    return 'You saved a newer version of this draft in another tab or window, so the changes here cannot be written over it.';
  }

  return `${by || 'Someone else'} saved a newer version of this draft, so your changes cannot be written over it.`;
}

/**
 * Drafts ordered by when they last changed, the latest first. Times are
 * compared as times: the server's RFC 3339 text drops trailing zeros of a
 * fraction, so "…:43Z" is earlier than "…:43.1Z".
 *
 * @param {{updated?: string}[]} drafts
 * @returns {object[]} a new array
 */
export function newestFirst(drafts) {
  const time = (draft) => Date.parse(draft?.updated) || 0;

  return [...(drafts || [])].sort((a, b) => time(b) - time(a));
}

/**
 * What the page says once a share list is saved.
 *
 * @param {string} name the draft's name
 * @param {{user: string, access: string}[]} shares the list saved
 * @returns {string}
 */
export function savedMessage(name, shares) {
  const people = (shares || []).filter((entry) => entry?.user);

  if (people.length === 0) {
    return `Only you can open ${name} now.`;
  }

  const whom =
    people.length <= 3
      ? listOf(
          people.map(
            (entry) =>
              `${entry.user} (${accessLabel(entry.access).toLowerCase()})`,
          ),
        )
      : count(people.length, 'person', 'people');

  return `${name} is now shared with ${whom}.`;
}

/**
 * What the dialog says once it adds a person, before anything is saved.
 *
 * @param {string} user
 * @param {string} access
 * @returns {string} "bob added, can edit. Save to apply."
 */
export function addedMessage(user, access) {
  return `${user} added, ${accessLabel(access).toLowerCase()}. Save to apply.`;
}

/**
 * How many users the user field's list matches.
 *
 * @param {number} n
 * @returns {string} "No users match", "1 user matches", "3 users match"
 */
export function matchesMessage(n) {
  if (n === 0) {
    return 'No users match.';
  }

  return `${count(n, 'user')} ${n === 1 ? 'matches' : 'match'}.`;
}

/**
 * The note a row carries while it differs from the list as read: see
 * rowChange. A stale row says so instead.
 *
 * @param {{user: string, access: string}[]} base
 * @param {object} row
 * @returns {string}
 */
export function rowNote(base, row) {
  if (row.stale) {
    return 'Account removed';
  }

  switch (rowChange(base, row)) {
    case 'added':
      return 'New';
    case 'changed':
      return 'Changed';
    case 'removed':
      return 'Removed when you save';
    default:
      return row.removed ? 'Not added' : '';
  }
}

/**
 * Why the server refused one person of a share list (a 422 reason code),
 * in words.
 *
 * @param {string} code
 * @param {string} [user]
 * @returns {string}
 */
export function reasonMessage(code, user = '') {
  switch (code) {
    case 'owner':
      return 'You own this draft.';
    case 'duplicate':
      return `${user} is listed twice.`;
    case 'invalid-access':
      return 'Choose Can view or Can edit.';
    case 'invalid-user':
      return `${user || 'That'} is not a valid username.`;
    case 'too-many':
      return `A draft can be shared with at most ${MAX_SHARES} people.`;
    case 'unknown-user':
      return `No user named ${user}. People must have signed in to phēnix at least once.`;
    case 'account-removed':
      return `${user}'s account was removed. Remove them and save, then add them again if they have a new account.`;
    default:
      return user
        ? `${user} could not be added.`
        : 'Some people could not be added.';
  }
}

/**
 * The dialog's rows for a share list the server sent. A stale share starts
 * marked removed.
 *
 * @param {{user: string, access: string, stale?: boolean}[]} shares
 * @returns {object[]} rows
 */
export function rowsOf(shares) {
  return (shares || []).map((entry) => ({
    user: entry.user,
    access: entry.access === 'edit' ? 'edit' : 'view',
    stale: entry.stale === true,
    removed: entry.stale === true,
  }));
}

/**
 * The share list rows save: every row not marked removed.
 *
 * @param {object[]} rows
 * @returns {{user: string, access: string}[]}
 */
export function sharesOf(rows) {
  return (rows || [])
    .filter((row) => !row.removed)
    .map(({ user, access }) => ({ user, access }));
}

/**
 * How many people the rows change against the list they were made from:
 * each added, removed or given other access counts once.
 *
 * @param {{user: string, access: string}[]} base the list as read
 * @param {object[]} rows the dialog's rows
 * @returns {number}
 */
export function changeCount(base, rows) {
  const before = new Map((base || []).map((entry) => [entry.user, entry]));
  const after = new Map(sharesOf(rows).map((entry) => [entry.user, entry]));
  let changes = 0;

  for (const [user, entry] of after) {
    if (!before.has(user) || before.get(user).access !== entry.access) {
      changes += 1;
    }
  }

  for (const user of before.keys()) {
    if (!after.has(user)) {
      changes += 1;
    }
  }

  return changes;
}

/**
 * Whether a row differs from the list it was made from, and how: 'added',
 * 'removed', 'changed', or '' for none.
 *
 * @param {{user: string, access: string}[]} base
 * @param {object} row
 * @returns {string}
 */
export function rowChange(base, row) {
  const before = (base || []).find((entry) => entry.user === row.user);

  if (!before) {
    return row.removed ? '' : 'added';
  }

  if (row.removed) {
    return 'removed';
  }

  return before.access === row.access ? '' : 'changed';
}

/**
 * Puts the user's edits (from `base` to `rows`) onto the list the server
 * holds now, after someone else changed it meanwhile (a 412): people added
 * are added, or given the access chosen when the other change added them
 * too; people removed are removed; access changed is changed. A person the
 * other change removed stays removed even when the user changed their
 * access, since the user did not ask to add them. Rows keep the server's
 * order, with the people the user added after them.
 *
 * @param {{user: string, access: string, stale?: boolean}[]} server the
 *   list as read again
 * @param {{user: string, access: string}[]} base the list the user edited
 * @param {object[]} rows the user's rows
 * @returns {object[]} rows over the server's list
 */
export function mergeShares(server, base, rows) {
  const merged = rowsOf(server);
  const find = (user) => merged.find((row) => row.user === user);
  const before = new Map((base || []).map((entry) => [entry.user, entry]));

  for (const row of rows || []) {
    const current = find(row.user);
    const previous = before.get(row.user);

    if (!previous) {
      if (row.removed) {
        continue;
      }

      if (current) {
        current.access = row.access;
        current.removed = current.stale;
      } else {
        merged.push({
          user: row.user,
          access: row.access,
          stale: false,
          removed: false,
        });
      }

      continue;
    }

    if (!current) {
      continue;
    }

    if (row.removed) {
      current.removed = true;
    } else if (row.access !== previous.access) {
      current.access = row.access;
    }
  }

  // People the user removed who are not among their rows any more.
  for (const user of before.keys()) {
    const current = find(user);

    if (current && !(rows || []).some((row) => row.user === user)) {
      current.removed = true;
    }
  }

  return merged;
}

/**
 * Checks a person before the dialog adds them.
 *
 * @param {string} name as typed
 * @param {object[]} rows the dialog's rows
 * @param {string} owner the draft's owner
 * @param {string[]|null} knownUsers the usernames the draft may be shared
 *   with, or null when they are unknown
 * @param {number} [maxShares]
 * @returns {{user: string, error: string, row?: object}} error is '' when
 *   the person can be added; row is the row already there for them
 */
export function validateAdd(
  name,
  rows,
  owner,
  knownUsers,
  maxShares = MAX_SHARES,
) {
  const user = String(name ?? '').trim();
  const row = (rows || []).find((entry) => entry.user === user);

  if (!user) {
    return { user, error: 'Enter a username.' };
  }

  if (user === owner) {
    return { user, error: 'You own this draft.' };
  }

  if (row?.stale) {
    return { user, error: reasonMessage('account-removed', user), row };
  }

  if (row && !row.removed) {
    return { user, error: `${user} already has access.`, row };
  }

  if (!validUsername(user)) {
    return { user, error: reasonMessage('invalid-user', user) };
  }

  if (Array.isArray(knownUsers) && !knownUsers.includes(user)) {
    return { user, error: `No user named ${user}.` };
  }

  if (!row && sharesOf(rows).length >= maxShares) {
    return {
      user,
      error: `A draft can be shared with at most ${maxShares} people.`,
    };
  }

  return { user, error: '', row };
}

/**
 * Checks a person before the dialog that shares several drafts at once adds
 * them to the people it will share the drafts with.
 *
 * @param {string} name as typed
 * @param {string[]} people the people listed so far
 * @param {string} owner the drafts' owner
 * @param {string[]|null} knownUsers the usernames the drafts may be shared
 *   with, or null when they are unknown
 * @param {number} [maxShares]
 * @returns {{user: string, error: string}} error is '' when the person can
 *   be added
 */
export function validateBulkAdd(
  name,
  people,
  owner,
  knownUsers,
  maxShares = MAX_SHARES,
) {
  const user = String(name ?? '').trim();
  let error = '';

  if (!user) {
    error = 'Enter a username.';
  } else if (user === owner) {
    error = 'You own these drafts.';
  } else if ((people || []).includes(user)) {
    error = `${user} is already in the list.`;
  } else if (!validUsername(user)) {
    error = reasonMessage('invalid-user', user);
  } else if (Array.isArray(knownUsers) && !knownUsers.includes(user)) {
    error = `No user named ${user}.`;
  } else if ((people || []).length >= maxShares) {
    error = `A draft can be shared with at most ${maxShares} people.`;
  }

  return { user, error };
}

/**
 * The address that opens a draft in Builder, for Copy link.
 *
 * @param {object} router vue-router
 * @param {string} owner
 * @param {string} id
 * @param {string} [origin] the page's origin
 * @returns {string}
 */
export function shareLink(
  router,
  owner,
  id,
  origin = globalThis.location?.origin || '',
) {
  const { href } = router.resolve({
    name: 'builder',
    query: { draft: `${owner}/${id}` },
  });

  return `${origin}${href}`;
}
