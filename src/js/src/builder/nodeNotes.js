// The notes block of a device or a switch: the node's notes (nodeNotes in
// model.js), drawn below its box on the canvas (nodes/NodeNotes.vue), and
// the room the block takes there, which the layouts leave free below the
// node (layouts/common.js and standard.js) and image downloads take in
// (documentBounds in exporters.js).
//
// The block is part of the node but not of its box: the document's size of
// the node, which its handles sit on, leaves it out. Its height here is an
// estimate made from the measures builder.css draws the block with (the
// .builder-node-notes rules), at the default text size: change both
// together.

import { nodeNotes, sizeOf } from './model.js';

/** The most notes the block shows; a last line says how many more. */
export const NOTES_SHOWN = 5;

/** The most lines of one note the block shows; an ellipsis ends the rest. */
export const NOTE_LINES = 3;

// The space between a node's box and its block, in CSS pixels: below a
// device it clears the device's new-interface handle on the box's bottom
// edge (half the handle and its hit area), below a switch the focus ring.
export const NOTES_GAP = { device: 16, switch: 8 };

// The block's measures in CSS pixels: a line of its text, the space between
// two notes, its padding and borders across (6 + 6 padding, a 3 pixel accent
// on the left and a 1 pixel border on the right) and down (4 + 4 padding
// and two 1 pixel borders), the width of an average character of its text,
// a little wide so the estimate errs on the side of room, and the least
// width any character of its text that takes room is drawn with.
const LINE_HEIGHT = 15;
const NOTE_SPACING = 2;
const ACROSS = 16;
const DOWN = 10;
const CHARACTER_WIDTH = 6;
const NARROWEST_CHARACTER = 1;

// The characters a tab counts as: it reaches the next tab stop, at most
// eight spaces on, and a space is about half as wide as an average
// character.
const TAB_CHARACTERS = 4;

/**
 * The notes the block shows, and how many it leaves out.
 *
 * @param {string[]} notes
 * @returns {{shown: string[], more: number}}
 */
export function shownNotes(notes) {
  return {
    shown: notes.slice(0, NOTES_SHOWN),
    more: Math.max(0, notes.length - NOTES_SHOWN),
  };
}

// The lines one line of a note takes where `perLine` characters fit across,
// wrapped the way the block wraps it (white-space: pre-wrap and
// overflow-wrap: anywhere in builder.css): at spaces first, each word that
// does not fit after the space before it starting the next line, and a
// word longer than a whole line broken across as many lines as it fills.
// A line of words can wrap to more lines than its characters fill, so
// counting by words keeps the estimate from falling short.
function wrappedLines(line, perLine) {
  let lines = 1;
  let used = 0;

  line.split(' ').forEach((word, index) => {
    const length = Array.from(word).length;

    if (index > 0) {
      if (used + 1 + length <= perLine) {
        used += 1 + length;

        return;
      }

      // The space ends this line, and the word starts the next.
      lines += 1;
    }

    const filled = Math.max(1, Math.ceil(length / perLine));

    lines += filled - 1;
    used = length - (filled - 1) * perLine;
  });

  return lines;
}

// The lines a note takes in a block `width` pixels wide: each of its own
// lines wraps (see wrappedLines), and no note takes more than NOTE_LINES.
function noteLines(note, width) {
  const perLine = Math.max(1, Math.floor((width - ACROSS) / CHARACTER_WIDTH));
  const text = note.replace(/\t/g, ' '.repeat(TAB_CHARACTERS));
  let lines = 0;

  for (const line of text.split('\n')) {
    lines += wrappedLines(line, perLine);

    if (lines >= NOTE_LINES) {
      return NOTE_LINES;
    }
  }

  return lines;
}

/**
 * The most characters of one note the block below a node can show: its
 * NOTE_LINES lines across the node's width, each filled with the narrowest
 * characters and ended by a space or a line break. The node's description
 * says each note the block shows up to this many characters (see
 * nodeInfo.js), so it never says less of a note than the block shows.
 *
 * @param {object} node
 * @returns {number}
 */
export function noteCharacters(node) {
  const across = Math.max(
    1,
    Math.floor((sizeOf(node).width - ACROSS) / NARROWEST_CHARACTER),
  );

  return NOTE_LINES * (across + 1);
}

/**
 * The height of a node's notes block, with the space above it: 0 for a node
 * that has no notes.
 *
 * @param {object} node
 * @returns {number} CSS pixels
 */
export function notesHeight(node) {
  const notes = nodeNotes(node);

  if (!notes.length) {
    return 0;
  }

  const { width } = sizeOf(node);
  const { shown, more } = shownNotes(notes);
  // The "+N more" line is a row of one line.
  const rows = shown.length + (more ? 1 : 0);
  let lines = more ? 1 : 0;

  for (const note of shown) {
    lines += noteLines(note, width);
  }

  return (
    (NOTES_GAP[node.kind] ?? 0) +
    DOWN +
    lines * LINE_HEIGHT +
    (rows - 1) * NOTE_SPACING
  );
}

/**
 * The room a node takes on the canvas: its box, and the notes block below it
 * while notes are shown. Every layout places nodes by their footprint, so a
 * laid-out node leaves its notes room.
 *
 * @param {object} node
 * @param {{showNotes?: boolean}} [options] showNotes: whether the canvas
 *   shows notes (the showNodeNotes setting), true by default
 * @returns {{width: number, height: number}}
 */
export function nodeFootprint(node, { showNotes = true } = {}) {
  const size = sizeOf(node);

  return showNotes
    ? { width: size.width, height: size.height + notesHeight(node) }
    : size;
}

/**
 * The box around nodes' footprints (see nodeFootprint).
 *
 * @param {object[]} nodes
 * @param {{showNotes?: boolean}} [options]
 * @returns {{x: number, y: number, width: number, height: number}}
 */
export function footprintBounds(nodes, options) {
  if (!nodes?.length) {
    return { x: 0, y: 0, width: 0, height: 0 };
  }

  let left = Infinity;
  let top = Infinity;
  let right = -Infinity;
  let bottom = -Infinity;

  for (const node of nodes) {
    const { width, height } = nodeFootprint(node, options);

    left = Math.min(left, node.position.x);
    top = Math.min(top, node.position.y);
    right = Math.max(right, node.position.x + width);
    bottom = Math.max(bottom, node.position.y + height);
  }

  return { x: left, y: top, width: right - left, height: bottom - top };
}
