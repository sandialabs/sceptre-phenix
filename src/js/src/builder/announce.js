// Wording and pacing of Builder announcements.
//
// The editor speaks through one polite live region. These helpers make each
// message specific (item names, counts with the correct plural). They also
// stop one message from replacing another before a screen reader reads it.

import { connectionEndLabel, nodeLabel } from './model.js';

/** Counts with the correct plural: "1 node", "2 nodes". */
export function count(n, noun, plural = `${noun}s`) {
  return `${n} ${n === 1 ? noun : plural}`;
}

/**
 * The message for an import, a copy or combine of a topology, or a legacy
 * diagram conversion, with the warning count.
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
      ? `Converted the legacy diagram, with ${count(warnings.length, 'warning')}.`
      : 'Converted the legacy diagram.';
  }

  return warnings.length
    ? `Imported the diagram, with ${count(warnings.length, 'warning')}.`
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

/** Names up to three things in a sentence, else the first two and a count of the others. */
export function describeNames(names) {
  const list = (names || []).filter(Boolean);

  if (list.length <= 3) {
    return listOf(list);
  }

  return `${list[0]}, ${list[1]} and ${count(list.length - 2, 'other')}`;
}

/** The message for combining the open draft's included nodes into a new draft. */
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
 * Names what an edit removed, for example "Deleted web-01 and 2 connections".
 * It counts cascaded removals too.
 */
export function describeRemoval(before, after) {
  const keptNodes = new Set((after.nodes || []).map((node) => node.id));
  const keptEdges = new Set((after.edges || []).map((edge) => edge.id));
  const keptNetworks = new Set(
    (after.networks || []).map((network) => network.id),
  );
  const nodes = (before.nodes || []).filter((node) => !keptNodes.has(node.id));
  const edges = (before.edges || []).filter((edge) => !keptEdges.has(edge.id));
  const networks = (before.networks || []).filter(
    (network) => !keptNetworks.has(network.id),
  );

  if (nodes.length === 0 && edges.length === 1 && networks.length === 0) {
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

  if (networks.length > 0) {
    parts.push(
      ...(networks.length <= 3
        ? networks.map((network) => `network ${network.name}`)
        : [count(networks.length, 'network')]),
    );
  }

  return parts.length ? `Deleted ${listOf(parts)}` : 'Deleted nothing';
}

// How long typing in a filter must pause before the result count is announced
// (the command palette, the shortcuts sheet).
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
 * The part of a store announcement that no modal dialog said at or after its
 * seq (see noteDialogSaid in store.js), or '' when a dialog said all of it.
 */
export function unsaidPart(message, said = []) {
  const text = message.text.trim();
  const sentence = asSentence(text);
  const heard = said
    .filter((entry) => entry.seq >= message.seq)
    .map((entry) => asSentence(entry.text.trim()));

  if (heard.some((s) => s === sentence || s.startsWith(`${sentence} `))) {
    return '';
  }

  const lead = heard
    .filter((s) => text.startsWith(`${s} `))
    .reduce((longest, s) => (s.length > longest.length ? s : longest), '');

  return lead ? text.slice(lead.length).trim() : message.text;
}

/**
 * Paces a polite live region so no message replaces another before a reader
 * reads it. Messages wait while blocked(): a modal dialog hides the region.
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

  // The text an item says now, or empty when it is stale or already said.
  function current(item) {
    if (item.stale?.()) {
      return '';
    }

    return item.rest ? item.rest() : item.text;
  }

  function release() {
    timer = null;
    pending = pending.filter(current);

    if (pending.length === 0) {
      return;
    }

    if (blocked()) {
      timer = setTimer(release, hold);

      return;
    }

    const texts = pending.map(current);
    pending = [];
    show(texts.length === 1 ? texts[0] : texts.map(asSentence).join(' '));
    timer = setTimer(release, hold);
  }

  return {
    /**
     * A newer message in the same slot replaces a waiting one. A message is
     * dropped when stale() is true or rest() gives nothing left to say.
     */
    push(message, { slot = '', stale = null, rest = null } = {}) {
      if (!message) {
        return;
      }

      if (slot) {
        pending = pending.filter((item) => item.slot !== slot);
      }

      // A stale message gives its place to a message that still holds.
      pending = pending.filter(current);

      if (pending[pending.length - 1]?.text !== message) {
        pending = capped([...pending, { text: message, slot, stale, rest }]);
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
