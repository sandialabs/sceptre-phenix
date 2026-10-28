// The Node Editor's field search (see dialogs/NodeEditorDialog.vue): which
// fields, sections, lists and list entries of the form a query matches.
// The functions are pure: the dialog reads the parts from its elements.

import { specFieldPath } from './formOutline.js';

/** The words of a query, in lowercase, each path in the dotted form. */
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
 * The path that a query matches a part of the form by: its node spec path or
 * a Builder data path ("iconKey"). An outline key that is not a data path has
 * none.
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
 * Whether each word of a query is in a part's label or its YAML path,
 * ignoring case. A number in a path is a whole list index, so "interfaces.1"
 * does not match entry 10.
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
 * What a query shows of the form: a match shows with all it holds, its
 * holders show their headers, and the rest hides. `count` is the number of
 * results that show.
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

/** The text that says how many fields a search shows. */
export function searchResultText(count) {
  if (count === 0) {
    return 'No fields match.';
  }

  return count === 1 ? '1 field matches.' : `${count} fields match.`;
}
