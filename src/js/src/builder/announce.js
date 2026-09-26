// Wording and pacing of Builder Beta announcements.
//
// The editor speaks through one polite live region. These helpers keep what it
// says specific (item names, counts with the right plural) and keep one
// message from replacing another before a screen reader has read it.

import { nodeLabel } from './model.js';

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
 * What an Import from a topology or experiment says once its draft exists.
 *
 * @param {string[]} [warnings] the warnings the import gave
 * @returns {string} "Imported diagram.", or "The diagram was imported with 2
 *   warnings."
 */
export function describeImport(warnings = []) {
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
 * Names what an edit removed from the document, for example "Deleted web-01
 * and 2 connections". Nodes are named when there are few of them; cascaded
 * removals (group members, the connections of a removed node) are counted.
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
    const ends = [edges[0].sourceNodeId, edges[0].targetNodeId]
      .map((id) => nodeLabel(byId.get(id)))
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

// How long a message stays in the live region before the next one replaces
// it. A message replaced a few milliseconds after it appeared is often never
// spoken: the screen reader has not read the region yet. Once it has, a
// polite message is queued by the screen reader itself, so a short hold is
// enough.
const ANNOUNCE_HOLD_MS = 750;

// Messages waiting for the hold to end. Older ones are dropped beyond this,
// so a burst of edits never builds a backlog of stale messages. A message in
// a slot is dropped last: it is the only report of its state.
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
 * Paces messages for a polite live region. A message is shown at once when
 * the region is free, and then held for `hold` ms. Messages that arrive
 * meanwhile wait, and when the hold ends they are shown together, in order,
 * as one message: none replaces another before it could be read, and none
 * waits longer than one hold unless the region is blocked (below). A
 * message repeated while it waits is queued once; a repeat of the message
 * on screen is shown again after the hold, so a repeated result is still
 * announced.
 *
 * A message may name a slot, where only the latest message is true (the
 * save state): a newer message in the same slot replaces one still waiting.
 * It may also carry stale(), checked when it would be shown; a message that
 * no longer holds is dropped rather than spoken late.
 *
 * While `blocked()` is true, messages wait. A modal dialog hides everything
 * outside it from assistive technology, the live region included, so a
 * message shown behind it would never be read; it is shown once the dialog
 * has closed instead.
 *
 * @param {object} options show(text) renders the text as a new element;
 *   blocked() says whether the region cannot be read now; hold, setTimer
 *   and clearTimer are injectable for tests
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
     * @param {object} [options] slot: messages that supersede each other;
     *   stale(): true once the message no longer holds
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
