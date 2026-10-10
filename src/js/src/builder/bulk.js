// One action on several listed items at once: deleting, sharing or
// downloading the selected drafts, deleting or downloading the selected
// published diagrams, deleting the selected icons or adding them to the
// server.
//
// The server has a route for each item, not for a batch, so a bulk action
// is a loop of requests, a few at a time. It never stops at the first
// failure: every item is tried, and what failed is listed afterwards with
// why, in one summary. Everything here is pure, so it is tested without a
// browser; the requests themselves are the store's (see deleteDrafts,
// deletePublishedMany and shareDrafts in store.js).

import { count, describeNames } from './announce.js';

// How many requests a bulk action has under way at once: fewer than the
// six connections a browser opens to one host, so the page's own requests
// are not held up.
export const BULK_CONCURRENCY = 4;

// Why an item a stopped run never reached was not changed.
export const NOT_ATTEMPTED = 'Not attempted.';

/**
 * A failure of one item whose reason is already in words, such as a draft
 * that would be shared with too many people, which no request was sent for.
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
 * outcome. A failure does not end the run, unless `stop` says it should
 * (the session ended, the server cannot be reached: endsBulk in api.js):
 * the items under way finish, and those not started are reported as
 * skipped.
 *
 * @param {object[]} items
 * @param {(item: object) => Promise<*>} work
 * @param {object} [options] limit: how many at once; onProgress(done,
 *   total): called as each item ends; stop(error): whether a failure ends
 *   the run
 * @returns {Promise<Array<{item: object, ok: boolean, value?: *,
 *   error?: *, skipped?: boolean}>>} in the order of `items`; it never
 *   rejects
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
 * What a run comes to: the items it changed, and those it did not, each
 * with why.
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
 * What deleting several drafts does, for the one confirmation the batch
 * gets. Other users' drafts say whose they are (owners); the user's own say
 * how many are shared, as the people they are shared with lose them too.
 *
 * @param {string[]} names the drafts, as their cards name them
 * @param {object} [options] shared: how many of them are shared; owners:
 *   the owners of other users' drafts
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
 * The share list of a draft once `people` are added to it at `access`:
 * everyone it is shared with stays, with their access, but for the people
 * added, who get the access chosen, whatever they had. A share whose
 * account was removed (stale) is left out, as the Share dialog leaves it
 * out when it saves: it gives no access, and the server refuses a list that
 * holds one.
 *
 * @param {{user: string, access: string, stale?: boolean}[]} current the
 *   list as read
 * @param {string[]} people the users to add
 * @param {string} access 'view' or 'edit'
 * @returns {{shares: {user: string, access: string}[], changed: boolean}}
 *   the whole new list, and whether it differs from the one read
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
