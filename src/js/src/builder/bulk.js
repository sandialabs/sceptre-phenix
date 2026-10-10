// One action on several listed items at once. The actions are: delete, share
// or download the selected drafts, delete or download the selected published
// diagrams, and delete the selected icons or add them to the server.
//
// The server has a route for each item, not for a batch. Thus a bulk action
// is a loop of requests, a few at a time. It never stops at the first
// failure. It tries every item and then lists each failure with its reason
// in one summary. All functions here are pure, so tests run them without a
// browser. The store sends the requests (see deleteDrafts,
// deletePublishedMany and shareDrafts in store.js).

import { count, describeNames } from './announce.js';

// How many requests a bulk action sends at the same time. This is fewer than
// the six connections that a browser opens to one host, so the page's own
// requests do not wait.
export const BULK_CONCURRENCY = 4;

// Why an item a stopped run never reached was not changed.
export const NOT_ATTEMPTED = 'Not attempted.';

/**
 * A failure of one item whose reason is already in words, for example a
 * draft that would be shared with too many people. No request was sent for
 * it.
 */
export class BulkError extends Error {
  /**
   * @param {string} reason a sentence
   */
  constructor(reason) {
    super(reason);
    this.name = 'BulkError';
    this.reason = reason;
  }
}

/**
 * Runs `work` for each item, at most `limit` at a time, and reports every
 * outcome. A failure does not end the run unless `stop` says so (the session
 * ended, or the server cannot be reached: see endsBulk in api.js). Then the
 * items in progress finish, and the items not started are reported as
 * skipped.
 *
 * @param {object[]} items
 * @param {(item: object) => Promise<*>} work
 * @param {object} [options] limit: how many at once. onProgress(done,
 *   total): called as each item ends. stop(error): whether a failure ends
 *   the run.
 * @returns {Promise<Array<{item: object, ok: boolean, value?: *,
 *   error?: *, skipped?: boolean}>>} in the order of `items`. It never
 *   rejects.
 */
export async function runBulk(
  items,
  work,
  { limit = BULK_CONCURRENCY, onProgress, stop } = {},
) {
  const list = [...(items || [])];
  const results = new Array(list.length);
  let next = 0;
  let done = 0;
  let stopped = false;

  // A callback that throws must not end the run.
  const quietly = (call, fallback) => {
    try {
      return call();
    } catch {
      return fallback;
    }
  };

  async function worker() {
    while (!stopped && next < list.length) {
      const index = next;
      const item = list[index];

      next += 1;

      try {
        results[index] = { item, ok: true, value: await work(item) };
      } catch (error) {
        results[index] = { item, ok: false, error };
        stopped = stopped || quietly(() => Boolean(stop?.(error)), false);
      }

      done += 1;
      quietly(() => onProgress?.(done, list.length));
    }
  }

  await Promise.all(
    Array.from({ length: Math.min(Math.max(limit, 1), list.length) }, worker),
  );

  return list.map(
    (item, index) => results[index] || { item, ok: false, skipped: true },
  );
}

/**
 * The result of a run: the items it changed, and the items it did not
 * change, each with the reason.
 *
 * @param {Array<{item: object, ok: boolean, error?: *, skipped?: boolean}>}
 *   results as runBulk gives them
 * @param {(error: *, item: object) => string} reasonOf a failure in words
 * @returns {{done: object[], failures: Array<{item: object, reason: string}>}}
 */
export function bulkOutcome(results, reasonOf) {
  return {
    done: results.filter((result) => result.ok).map((result) => result.item),
    failures: results
      .filter((result) => !result.ok)
      .map((result) => ({
        item: result.item,
        reason: result.skipped
          ? NOT_ATTEMPTED
          : result.error instanceof BulkError
            ? result.error.reason
            : reasonOf(result.error, result.item),
      })),
  };
}

/**
 * The heading of the summary of a run that left some items unchanged.
 *
 * @param {number} failed how many were not changed
 * @param {number} total how many the run was for
 * @param {string} noun singular: "draft"
 * @param {string} plural "drafts"
 * @param {string} done what the run does, as done: "deleted", "shared"
 * @returns {string} "2 of 5 drafts could not be deleted. The other 3 were
 *   deleted."
 */
export function bulkFailureHeading(failed, total, noun, plural, done) {
  const rest = total - failed;
  const head = `${failed} of ${total} ${total === 1 ? noun : plural} could not be ${done}.`;

  return rest > 0
    ? `${head} The other ${rest} ${rest === 1 ? 'was' : 'were'} ${done}.`
    : head;
}

/**
 * What the page says once a run has changed every item.
 *
 * @param {number} n how many
 * @param {string} noun singular
 * @param {string} plural
 * @param {string} done what the run does, as done: "deleted"
 * @returns {string} "Deleted 5 drafts.", "Deleted 1 draft."
 */
export function bulkDoneMessage(n, noun, plural, done) {
  return `${done.charAt(0).toUpperCase()}${done.slice(1)} ${count(n, noun, plural)}.`;
}

/**
 * The text of the one confirmation for the deletion of several drafts. For
 * other users' drafts, it names the owners. For the user's own drafts, it
 * tells how many are shared, because the people they are shared with lose
 * them too.
 *
 * @param {string[]} names the drafts, as their cards name them
 * @param {object} [options] shared: how many of them are shared. owners:
 *   the owners of other users' drafts.
 * @returns {string}
 */
export function draftsDeleteMessage(names, { shared = 0, owners = [] } = {}) {
  const whose = owners.length
    ? ` They belong to ${describeNames([...new Set(owners)])}.`
    : '';
  const lost = shared
    ? ` ${shared} of them ${shared === 1 ? 'is' : 'are'} shared: the people with access lose it too.`
    : '';

  return `${describeNames(names)}.${whose} The drafts and their whole histories are removed from the server.${lost} This cannot be undone.`;
}

/**
 * What deleting several published topologies does, for the one
 * confirmation the batch gets.
 *
 * @param {string[]} names the topologies
 * @returns {string}
 */
export function topologiesDeleteMessage(names) {
  return `${describeNames(names)}. The topologies are deleted from phēnix. Drafts and experiments made from them are not changed.`;
}

/**
 * What deleting several icons of the server's icon library does, for the
 * one confirmation the batch gets.
 *
 * @param {string[]} names the icons
 * @returns {string}
 */
export function iconsDeleteMessage(names) {
  return `Delete ${describeNames(names)} from the server? Diagrams and templates that use them, by any of their names, will show their built-in icon instead.`;
}

/**
 * The share list of a draft after `people` are added to it at `access`.
 * Everyone already on the list stays, with their access, except the people
 * added. They get the chosen access, whatever access they had. A share whose
 * account was removed (stale) is left out, as the Share dialog does when it
 * saves. Such a share gives no access, and the server refuses a list that
 * holds one.
 *
 * @param {{user: string, access: string, stale?: boolean}[]} current the
 *   list as read
 * @param {string[]} people the users to add
 * @param {string} access 'view' or 'edit'
 * @returns {{shares: {user: string, access: string}[], changed: boolean}}
 *   the whole new list, and whether it differs from the list read
 */
export function mergeShareList(current, people, access) {
  const level = access === 'edit' ? 'edit' : 'view';
  const before = (current || []).filter((entry) => entry?.user);
  const shares = before
    .filter((entry) => !entry.stale)
    .map((entry) => ({
      user: entry.user,
      access: entry.access === 'edit' ? 'edit' : 'view',
    }));

  for (const user of people || []) {
    const listed = shares.find((entry) => entry.user === user);

    if (listed) {
      listed.access = level;
    } else {
      shares.push({ user, access: level });
    }
  }

  return {
    shares,
    changed:
      shares.length !== before.length ||
      shares.some(
        (entry, index) =>
          entry.user !== before[index].user ||
          entry.access !== before[index].access,
      ),
  };
}
