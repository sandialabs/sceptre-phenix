// Preflight checks as the Checks dialog offers them: which checks there are
// and what each looks at, the choice of checks the browser remembers, and
// how a report reads (see POST /builder/drafts/{owner}/{draft}/preflight).
// The server runs the checks on the draft's saved document. Nothing here
// reads the cluster.

/**
 * The checks, in the order the dialog lists them and the server reports
 * them: the id the server takes, the label of its checkbox, and a line that
 * says what it looks at.
 */
export const PREFLIGHT_CHECKS = Object.freeze([
  Object.freeze({
    id: 'capacity',
    label: 'Host capacity',
    hint: "The devices' CPUs and memory against what the cluster's hosts have free.",
  }),
  Object.freeze({
    id: 'network',
    label: 'Networks',
    hint: "VLANs against the experiment's range and running experiments, and bridges against the hosts'.",
  }),
  Object.freeze({
    id: 'disks',
    label: 'Disk images',
    hint: "Each drive image against the server's disk images and the device's kind.",
  }),
  Object.freeze({
    id: 'apps',
    label: 'Scenario apps',
    hint: "Each app the diagram's scenarios run against the apps the server has.",
  }),
]);

// The words of each status, which the result says as well as its icon.
const STATUS_TEXT = {
  passed: 'Passed',
  failed: 'Failed',
  unavailable: 'Unavailable',
};

/** Where the browser keeps the checks last chosen (see loadPreflightChecks). */
export const PREFLIGHT_STORAGE_KEY = 'phenix.builder.preflight';

/**
 * The checks of a choice, each once and in the dialog's order. Anything
 * that is not a check is left out.
 *
 * @param {unknown} choice ids of checks
 * @returns {string[]}
 */
export function orderedChecks(choice) {
  const chosen = new Set(Array.isArray(choice) ? choice : []);

  return PREFLIGHT_CHECKS.map((check) => check.id).filter((id) =>
    chosen.has(id),
  );
}

/**
 * The checks that the browser remembers as last chosen, or none. No check
 * is ticked until the user ticks one. Storage that cannot be read remembers
 * nothing.
 *
 * @param {Storage|null} [storage] localStorage
 * @returns {string[]}
 */
export function loadPreflightChecks(storage = globalThis.localStorage) {
  try {
    const saved = JSON.parse(storage?.getItem(PREFLIGHT_STORAGE_KEY) || '{}');

    return orderedChecks(saved?.checks);
  } catch {
    return [];
  }
}

/**
 * Remembers the checks chosen, for the next time the dialog opens in this
 * browser. Storage that cannot be written leaves the choice unremembered.
 *
 * @param {string[]} checks
 * @param {Storage|null} [storage] localStorage
 */
export function savePreflightChecks(checks, storage = globalThis.localStorage) {
  try {
    storage?.setItem(
      PREFLIGHT_STORAGE_KEY,
      JSON.stringify({ checks: orderedChecks(checks) }),
    );
  } catch {
    // Not remembered: the choice still holds until the page is left.
  }
}

/**
 * @param {string} id a check's id
 * @returns {string} its label, or the id for a check the dialog does not know
 */
export function checkLabel(id) {
  return PREFLIGHT_CHECKS.find((check) => check.id === id)?.label || id;
}

/**
 * @param {string} status passed, failed or unavailable
 * @returns {string} Passed, Failed or Unavailable
 */
export function statusText(status) {
  return STATUS_TEXT[status] || STATUS_TEXT.unavailable;
}

/**
 * The summary of a report, which the dialog shows and announces when the
 * checks are done: "Preflight: 2 passed, 1 failed, 1 unavailable". A status
 * that no check has is left out.
 *
 * @param {{checks: {status: string}[]}} report
 * @returns {string}
 */
export function preflightOutcome(report) {
  const checks = Array.isArray(report?.checks) ? report.checks : [];
  const parts = ['passed', 'failed', 'unavailable'].flatMap((status) => {
    const n = checks.filter((check) => check.status === status).length;

    return n ? [`${n} ${status}`] : [];
  });

  return parts.length
    ? `Preflight: ${parts.join(', ')}`
    : 'Preflight: no check was made';
}
