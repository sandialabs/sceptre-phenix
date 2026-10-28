// What the drafts page and the editor say about a draft: its order in a
// list, its delete confirmation and its conflict text. Everything here is
// pure, so the tests need no browser.

/**
 * What deleting a draft does, for its confirmation.
 *
 * @returns {string}
 */
export function deleteMessage() {
  return 'The Builder removes the draft and its whole history from the server. You cannot undo this.';
}

/**
 * What the conflict panel says of who saved the newer version of a draft:
 * the named person, the user in another tab, or someone else.
 */
export function conflictMessage(by, me) {
  if (by && by === me) {
    return 'You saved a newer version of this draft in another tab or window. The changes here cannot replace it.';
  }

  return `${by || 'Someone else'} saved a newer version of this draft. Your changes cannot replace it.`;
}

/**
 * Drafts ordered by last change, the latest first. Times are compared as
 * times, because RFC 3339 text drops trailing zeros ("…:43Z" < "…:43.1Z").
 */
export function newestFirst(drafts) {
  const time = (draft) => Date.parse(draft?.updated) || 0;

  return [...(drafts || [])].sort((a, b) => time(b) - time(a));
}
