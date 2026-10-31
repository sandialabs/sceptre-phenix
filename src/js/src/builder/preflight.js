// Preflight checks as the Checks dialog offers them: which checks there are
// and what each looks at, the choice of checks the browser remembers, and
// how a report reads (see POST /builder/drafts/{draft}/preflight).
// The server runs the checks on the draft's saved document. Nothing here
// reads the cluster.

import { pageStorage } from './storage.js';

/**
 * The checks, in the order the dialog lists them and the server reports
 * them: the id the server takes, the label of its checkbox, and a line that
 * says what it looks at.
 */
export const PREFLIGHT_CHECKS = Object.freeze([
  Object.freeze({
    id: 'capacity',
    label: 'Host capacity',
    hint: "Compares the devices' CPUs and memory with what the cluster's hosts have free.",
  }),
  Object.freeze({
    id: 'network',
    label: 'Networks',
    hint: "Compares VLANs with the experiment's range and running experiments, and bridges with the hosts'.",
  }),
  Object.freeze({
    id: 'disks',
    label: 'Disk images',
    hint: "Compares each drive image with the server's disk images and the device's kind.",
  }),
  Object.freeze({
    id: 'apps',
    label: 'Scenario apps',
    hint: "Compares each app the diagram's scenarios run with the apps the server has.",
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

/** The checks of a choice, each once and in the dialog's order. */
export function orderedChecks(choice) {
  const chosen = new Set(Array.isArray(choice) ? choice : []);

  return PREFLIGHT_CHECKS.map((check) => check.id).filter((id) =>
    chosen.has(id),
  );
}

// The checks that the browser remembers as last chosen, or none. No check is
// ticked until the user ticks one.
export function loadPreflightChecks(storage = pageStorage('localStorage')) {
  try {
    const saved = JSON.parse(storage?.getItem(PREFLIGHT_STORAGE_KEY) || '{}');

    return orderedChecks(saved?.checks);
  } catch {
    return [];
  }
}

// Remembers the chosen checks for the next time the dialog opens in this
// browser.
export function savePreflightChecks(
  checks,
  storage = pageStorage('localStorage'),
) {
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

// The summary the dialog shows when the checks are done: "Preflight: 2 passed,
// 1 failed, 1 unavailable". It leaves out a status that no check has.
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
