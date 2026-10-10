// Wording and pacing of Builder announcements.
//
// The editor speaks through one polite live region. These helpers make each
// message specific (item names, counts with the correct plural). They also
// stop one message from replacing another before a screen reader reads it.

import { connectionEndLabel, nodeLabel } from './model.js';

/**
 * @param {number} n
 * @param {string} noun singular
 * @param {string} [plural]
 * @returns {string} "1 node", "2 nodes"
 */
export function count(n, noun, plural = `${noun}s`) {
  return `${n} ${n === 1 ? noun : plural}`;
}

/**
 * Returns the message that an import from a topology or experiment announces
 * after its draft exists. It also returns the message for the conversion of a
 * legacy Builder diagram.
 *
 * @param {string[]} [warnings] the warnings from the import
 * @param {object} [options] mode: 'import', 'copy' (a topology imported as a
 *   copy) or 'combine' (a topology imported with its included topologies
 *   combined). Copy and combine name the topology read (source) and the new
 *   topology (name). legacy: true when the import converted a legacy Builder
 *   diagram. Only the 'import' mode uses it.
 * @returns {string} for example "Imported diagram.", "The legacy diagram was
 *   converted with 2 warnings." or "Imported a copy of topology site as
 *   site-copy, with 2 warnings."
 */
export function describeImport(
  warnings = [],
  { mode = 'import', legacy = false, source = '', name = '' } = {},
) {
  if (mode === 'copy' || mode === 'combine') {
    const made =
      mode === 'copy'
        ? `Imported a copy of topology ${source} as ${name}`
        : `Combined topology ${source} and its included topologies as ${name}`;

    return warnings.length
      ? `${made}, with ${count(warnings.length, 'warning')}.`
      : `${made}.`;
  }

  if (legacy) {
    return warnings.length
      ? `The legacy diagram was converted with ${count(warnings.length, 'warning')}.`
      : 'Converted the legacy diagram.';
  }

  return warnings.length
    ? `The diagram was imported with ${count(warnings.length, 'warning')}.`
    : 'Imported diagram.';
}

/**
 * @param {string[]} items
 * @returns {string} "a", "a and b", "a, b and c"
 */
export function listOf(items) {
  if (items.length <= 1) {
    return items[0] || '';
  }

  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`;
}

/**
 * Names several things in a sentence, as a confirmation or a card does. It
 * names up to three. For more, it names the first two and counts the others.
 *
 * @param {string[]} names
 * @returns {string} "a and b", "a, b and c", "a, b and 3 others", or ''
 */
export function describeNames(names) {
  const list = (names || []).filter(Boolean);

  if (list.length <= 3) {
    return listOf(list);
  }

  return `${list[0]}, ${list[1]} and ${count(list.length - 2, 'other')}`;
}

/**
 * Returns the message that combining the included nodes of the open draft
 * announces after the new draft exists.
 *
 * @param {number} included how many included nodes became the new draft's own
 * @param {string} name the new draft's name
 * @param {string} from the name of the draft that was open
 * @param {string[]} [kept] the included topologies that the new draft still
 *   includes, because their nodes were never in the diagram
 * @returns {string} "Combined 3 included nodes into new draft
 *   site-combined. Draft site is unchanged.", and, with kept includes, " It
 *   still includes site-b, whose nodes are not in the diagram."
 */
export function describeCombined(included, name, from, kept = []) {
  const still = kept.length
    ? ` It still includes ${listOf(kept)}, whose nodes are not in the diagram.`
    : '';

  return (
    `Combined ${count(included, 'included node')} into new draft ${name}. ` +
    `Draft ${from} is unchanged.${still}`
  );
}

/**
 * Names what an edit removed from the document, for example "Deleted web-01
 * and 2 connections". It names nodes when there are few of them. It counts
 * cascaded removals (group members, the connections of a removed node).
 *
 * @param {object} before document before the edit
 * @param {object} after document after the edit
 * @returns {string}
 */
export function describeRemoval(before, after) {
  const keptNodes = new Set((after.nodes || []).map((node) => node.id));
  const keptEdges = new Set((after.edges || []).map((edge) => edge.id));
  const nodes = (before.nodes || []).filter((node) => !keptNodes.has(node.id));
  const edges = (before.edges || []).filter((edge) => !keptEdges.has(edge.id));

  if (nodes.length === 0 && edges.length === 1) {
    const byId = new Map((before.nodes || []).map((node) => [node.id, node]));
    const [edge] = edges;
    const ends = [
      [edge.sourceNodeId, edge.sourceHandleId],
      [edge.targetNodeId, edge.targetHandleId],
    ]
      .map(([id, handle]) => connectionEndLabel(byId.get(id), handle))
      .filter(Boolean);

    return ends.length === 2
      ? `Deleted the connection between ${ends[0]} and ${ends[1]}`
      : 'Deleted 1 connection';
  }

  const parts = [];

  if (nodes.length > 0) {
    parts.push(
      ...(nodes.length <= 3
        ? nodes.map((node) => nodeLabel(node))
        : [count(nodes.length, 'node')]),
    );
  }

  if (edges.length > 0) {
    parts.push(count(edges.length, 'connection'));
  }

  return parts.length ? `Deleted ${listOf(parts)}` : 'Deleted nothing';
}

// How long typing in a filter must pause before the result count is announced
// (the command palette, the shortcuts sheet, the Share dialog's user field).
// The count is then spoken once, not for every letter.
export const COUNT_DELAY_MS = 400;

// How long a message stays in the live region before the next one replaces
// it. A screen reader often does not speak a message that another replaces a
// few milliseconds after it appears, because it has not read the region yet.
// After it reads the region, the screen reader queues a polite message
// itself, so a short hold is sufficient.
const ANNOUNCE_HOLD_MS = 750;

// The maximum number of messages that wait for the hold to end. Older
// messages above this limit are dropped, so a burst of edits never makes a
// backlog of stale messages. A message in a slot is dropped last because it is
// the only report of its state.
const MAX_PENDING = 3;

function capped(pending) {
  const kept = [...pending];

  while (kept.length > MAX_PENDING) {
    const oldest = kept.findIndex((item) => !item.slot);
    kept.splice(Math.max(oldest, 0), 1);
  }

  return kept;
}

// Joined messages are read as sentences: "Added switch. Connected nodes."
function asSentence(text) {
  return /[.!?…:]$/.test(text) ? text : `${text}.`;
}

/**
 * Paces messages for a polite live region. When the region is free, a
 * message shows at once and stays for `hold` ms. Messages that arrive during
 * the hold wait. When the hold ends, they show together, in order, as one
 * message. Thus no message replaces another before a reader can read it, and
 * no message waits longer than one hold unless the region is blocked (see
 * below). A message repeated while it waits is queued once. A repeat of the
 * message on screen shows again after the hold, so a repeated result is still
 * announced.
 *
 * A message can name a slot, where only the latest message is true (the save
 * state). A newer message in the same slot replaces one that still waits. A
 * message can also carry stale(), which is checked before the message shows.
 * A stale message is dropped, not spoken late.
 *
 * While `blocked()` is true, messages wait. A modal dialog hides all content
 * outside it from assistive technology, the live region included. A message
 * shown behind the dialog is never read, so it shows after the dialog closes.
 *
 * @param {object} options show(text) renders the text as a new element.
 *   blocked() tells whether the region cannot be read now. hold, setTimer and
 *   clearTimer are injectable for tests.
 * @returns {{push: Function, dispose: Function}}
 */
export function createAnnouncer({
  show,
  blocked = () => false,
  hold = ANNOUNCE_HOLD_MS,
  setTimer = (fn, ms) => setTimeout(fn, ms),
  clearTimer = (handle) => clearTimeout(handle),
}) {
  let pending = [];
  let timer = null;

  function release() {
    timer = null;
    pending = pending.filter((item) => !item.stale?.());

    if (pending.length === 0) {
      return;
    }

    if (blocked()) {
      timer = setTimer(release, hold);

      return;
    }

    const texts = pending.map((item) => item.text);
    pending = [];
    show(texts.length === 1 ? texts[0] : texts.map(asSentence).join(' '));
    timer = setTimer(release, hold);
  }

  return {
    /**
     * @param {string} message
     * @param {object} [options] slot: the name shared by messages that
     *   replace each other. stale(): true when the message is out of date.
     */
    push(message, { slot = '', stale = null } = {}) {
      if (!message) {
        return;
      }

      if (slot) {
        pending = pending.filter((item) => item.slot !== slot);
      }

      if (pending[pending.length - 1]?.text !== message) {
        pending = capped([...pending, { text: message, slot, stale }]);
      }

      if (timer === null) {
        release();
      }
    },

    dispose() {
      if (timer !== null) {
        clearTimer(timer);
      }

      timer = null;
      pending = [];
    },
  };
}
