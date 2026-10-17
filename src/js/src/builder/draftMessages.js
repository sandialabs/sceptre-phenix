// What the drafts page and the editor say about a draft: its order in a
// list, its delete confirmation and its conflict text. Everything here is
// pure, so the tests need no browser.

/**
 * What deleting a draft does, for its confirmation.
 *
 * @returns {string}
 */
export function deleteMessage() {
  return 'The draft and its whole history are removed from the server. You cannot undo this.';
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
    return 'You saved a newer version of this draft in another tab or window, so the changes here cannot replace it.';
  }

  return `${by || 'Someone else'} saved a newer version of this draft, so your changes cannot replace it.`;
}

/**
 * Drafts ordered by when they last changed, the latest first. Times are
 * compared as times, not as text. The server's RFC 3339 text drops trailing
 * zeros of a fraction, so "…:43Z" is earlier than "…:43.1Z".
 *
 * @param {{updated?: string}[]} drafts
 * @returns {object[]} a new array
 */
export function newestFirst(drafts) {
  const time = (draft) => Date.parse(draft?.updated) || 0;

  return [...(drafts || [])].sort((a, b) => time(b) - time(a));
}
