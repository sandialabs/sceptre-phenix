// One action on several listed items at once. The server has a route per
// item, so a bulk action is a loop of requests, a few at a time, that tries
// every item and then lists each failure with its reason in one summary.

import { count, describeNames } from './announce.js';

// How many requests a bulk action sends at the same time. This is fewer than
// the six connections that a browser opens to one host, so the page's own
// requests do not wait.
export const BULK_CONCURRENCY = 4;

// Why an item a stopped run never reached was not changed.
export const NOT_ATTEMPTED = 'Not attempted.';

/**
 * A failure of one item whose reason is already in words, for example a
 * draft that changed again while it was being deleted.
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
 * Runs `work` for each item, at most `limit` at a time, and never rejects. A
 * failure ends the run only when `stop` says so (see endsBulk in api.js).
 * Then the items not started are reported as skipped.
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
 * The result of a run: the items it changed, and the items it did not change,
 * each with the reason.
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
 * The heading of the summary of a run that left some items unchanged ("2 of 5
 * drafts could not be deleted.").
 */
export function bulkFailureHeading(failed, total, noun, plural, done) {
  const rest = total - failed;
  const head = `${failed} of ${total} ${total === 1 ? noun : plural} could not be ${done}.`;

  return rest > 0
    ? `${head} The other ${rest} ${rest === 1 ? 'was' : 'were'} ${done}.`
    : head;
}

/**
 * What the page says once a run has changed every item ("Deleted 5 drafts.").
 */
export function bulkDoneMessage(n, noun, plural, done) {
  return `${done.charAt(0).toUpperCase()}${done.slice(1)} ${count(n, noun, plural)}.`;
}

/** The text of the one confirmation for the deletion of several drafts. */
export function draftsDeleteMessage(names) {
  return `${describeNames(names)}. The Builder removes the drafts and their whole histories from the server. You cannot undo this.`;
}

/**
 * The text of the one confirmation for the deletion of several published
 * topologies.
 */
export function topologiesDeleteMessage(names) {
  return `${describeNames(names)}. The Builder deletes the topologies from phēnix. It does not change the drafts and experiments made from them.`;
}

/**
 * The text of the one confirmation for the deletion of several library icons.
 */
export function iconsDeleteMessage(names) {
  return `Delete ${describeNames(names)} from the server? Diagrams and templates that use them, by any of their names, will show their built-in icon instead.`;
}

/**
 * The text of the one confirmation for the deletion of several layer sets.
 * The built-in sets among the selected ones stay, and it says so.
 */
export function layerSetsDeleteMessage(names, builtins = []) {
  const kept = builtins.length
    ? ` ${describeNames(builtins)} ${builtins.length === 1 ? 'stays' : 'stay'} because you cannot delete a built-in layer set.`
    : '';

  return `Delete ${describeNames(names)} from the server? Diagrams keep their copies of the layers. You cannot undo this.${kept}`;
}
