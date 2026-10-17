// The Node Editor's field search (see dialogs/NodeEditorDialog.vue): which
// fields, sections, lists and list entries of the form a query matches.
// The functions are pure: the dialog reads the parts from its elements.

import { specFieldPath } from './formOutline.js';

/**
 * The words of a query, in lowercase, each path in the dotted form.
 *
 * @param {string} query
 * @returns {string[]} none for a query of white space
 */
export function searchWords(query) {
  return String(query ?? '')
    .toLowerCase()
    .split(/\s+/)
    .map((word) =>
      word
        .replace(/\[(\d+)\]/g, '.$1')
        .replace(/^spec\./, '')
        .replace(/^\.+|\.+$/g, ''),
    )
    .filter(Boolean);
}

/**
 * The path a query matches a part of the form by: its path in the node
 * spec, or the data path of a field of the Builder's own (such as
 * "iconKey"). An outline entry that is not a data path ("#checks",
 * "spec#more-settings") has none.
 *
 * @param {string} dataPath the JSON Forms data path, or an outline key
 * @returns {string}
 */
export function searchPath(dataPath) {
  const path = String(dataPath ?? '');

  if (path.includes('#')) {
    return '';
  }

  return path.startsWith('spec.') ? specFieldPath(path) : path;
}

const INDEX = /^\d+$/;

// Whether a word is in a path, with each number in it a whole list index:
// "interfaces.1" is not in "network.interfaces.10".
function inPath(word, path) {
  const parts = word.split('.');

  if (!parts.some((part) => INDEX.test(part))) {
    return path.includes(word);
  }

  const start = INDEX.test(parts[0]) ? '(?:^|\\.)' : '';
  const end = INDEX.test(parts.at(-1)) ? '(?=\\.|$)' : '';
  const body = parts
    .map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    .join('\\.');

  return new RegExp(start + body + end).test(path);
}

// Whether the parts of a dotted word are in the segments of a path, in
// that order: "interfaces.vlan" in "network.interfaces.0.vlan".
function inOrder(word, path) {
  const parts = word.split('.').filter(Boolean);
  let at = 0;

  for (const segment of path.split('.')) {
    const part = parts[at];

    if (
      at < parts.length &&
      (INDEX.test(part) ? segment === part : segment.includes(part))
    ) {
      at += 1;
    }
  }

  return parts.length > 1 && at === parts.length;
}

/**
 * Whether a part of the form matches the words of a query: each word is in
 * its label or in its path (the one a topology's YAML has, see
 * specFieldPath), ignoring case. A word can be a path in either form,
 * "interfaces[0].vlan" or "interfaces.0.vlan", or its parts in order:
 * "interfaces.vlan" matches the VLAN of every interface. A number in a path
 * is a whole list index, so "interfaces.1" does not match entry 10.
 *
 * @param {string[]} words see searchWords
 * @param {{label?: string, path?: string}} part its label, and its path
 *   (see searchPath)
 * @returns {boolean}
 */
export function partMatches(words, { label = '', path = '' }) {
  const name = String(label).toLowerCase();
  const where = String(path).toLowerCase();

  return (
    words.length > 0 &&
    words.every(
      (word) =>
        name.includes(word) ||
        inPath(word, where) ||
        (word.includes('.') && inOrder(word, where)),
    )
  );
}

/**
 * What a query shows of the form. A part that matches shows with all it
 * holds, and the parts that hold it show (their headers) too. The rest
 * hides.
 *
 * @param {{label?: string, path?: string, parent: number, field?: boolean}[]} parts
 *   the parts of the form in document order, each with the index of the
 *   part that holds it, or -1. A field is a part that counts as a result
 * @param {string} query
 * @returns {{states: ('match'|'inside'|'holds'|'hidden')[], count: number}}
 *   the state of each part, and how many results show: the fields, and a
 *   part that matches and holds no field
 */
export function searchFields(parts, query) {
  const words = searchWords(query);
  const states = parts.map(() => 'hidden');

  parts.forEach((part, index) => {
    const parent = states[part.parent];

    if (parent === 'match' || parent === 'inside') {
      states[index] = 'inside';
    } else if (partMatches(words, part)) {
      states[index] = 'match';
    }
  });

  const holdsField = parts.map(() => false);

  parts.forEach((part, index) => {
    for (let at = part.parent; at >= 0; at = parts[at].parent) {
      holdsField[at] ||= Boolean(part.field);

      if (states[index] === 'match' && states[at] === 'hidden') {
        states[at] = 'holds';
      }
    }
  });

  const count = parts.filter((part, index) =>
    part.field
      ? states[index] === 'match' || states[index] === 'inside'
      : states[index] === 'match' && !holdsField[index],
  ).length;

  return { states, count };
}

/**
 * The text that says how many fields a search shows.
 *
 * @param {number} count
 * @returns {string}
 */
export function searchResultText(count) {
  if (count === 0) {
    return 'No fields match.';
  }

  return count === 1 ? '1 field matches.' : `${count} fields match.`;
}
