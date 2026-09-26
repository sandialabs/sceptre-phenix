// Matching for the command palette: how well a query matches a title, and
// which letters matched, so the palette can rank results and highlight them.
//
// Each word of the query must appear in the text, ignoring case. A word at
// the start of the text ranks first, then one at the start of a word, then
// one anywhere. When a word is missing, the query's letters must appear in
// order ("grp" finds "Group selection"), ranked by how close together they
// are. Keywords and a node's fields (its addresses, image and so on) match
// only by words, and rank below any match on the title.

// What starts a word, besides the start of the text.
const BOUNDARY = /[\s\-_.:/,›·()@#]/;

// Score bands, so every title match outranks every field match, and every
// field match a title matched letter by letter.
const WORDS = 1000;
const FIELD = 600;
const LETTERS = 500;
const KEYWORD = 50;

function merge(ranges) {
  const out = [];

  for (const range of [...ranges].sort((a, b) => a[0] - b[0])) {
    const last = out[out.length - 1];

    if (last && range[0] <= last[1]) {
      last[1] = Math.max(last[1], range[1]);
    } else {
      out.push([range[0], range[1]]);
    }
  }

  return out;
}

function wordsOf(query) {
  return String(query || '')
    .toLowerCase()
    .trim()
    .split(/\s+/)
    .filter(Boolean);
}

// Every word as a substring: {score, ranges}, or null.
function byWords(text, words) {
  const lower = String(text || '').toLowerCase();
  const ranges = [];
  let score = 0;

  for (const word of words) {
    let at = lower.indexOf(word);
    let boundary = -1;

    for (let from = at; from >= 0; from = lower.indexOf(word, from + 1)) {
      if (from === 0 || BOUNDARY.test(lower[from - 1])) {
        boundary = from;
        break;
      }
    }

    if (at < 0) {
      return null;
    }

    at = boundary >= 0 ? boundary : at;
    score += (at === 0 ? 300 : boundary >= 0 ? 200 : 100) - Math.min(at, 50);
    ranges.push([at, at + word.length]);
  }

  return { score, ranges: merge(ranges) };
}

// The letters in order: {score, ranges}, or null.
function byLetters(text, letters) {
  const lower = String(text || '').toLowerCase();
  const ranges = [];
  let first = -1;
  let gaps = 0;
  let next = 0;

  for (const letter of letters) {
    const at = lower.indexOf(letter, next);

    if (at < 0) {
      return null;
    }

    if (first < 0) {
      first = at;
    } else {
      gaps += at - next;
    }

    ranges.push([at, at + 1]);
    next = at + 1;
  }

  return { score: Math.max(1, 100 - gaps * 5 - first), ranges: merge(ranges) };
}

/**
 * How well `query` matches `text`.
 *
 * @param {string} text
 * @param {string} query
 * @param {object} [options]
 * @param {boolean} [options.letters] also match the letters in order when a
 *   word is missing (default true)
 * @returns {{score: number, ranges: number[][]}|null} null when it does not
 *   match; ranges are [start, end) of the matched text. An empty query
 *   matches everything with score 0.
 */
export function matchText(text, query, { letters = true } = {}) {
  const words = wordsOf(query);

  if (!words.length) {
    return { score: 0, ranges: [] };
  }

  const found = byWords(text, words);

  if (found) {
    return { score: WORDS + found.score, ranges: found.ranges };
  }

  const loose = letters ? byLetters(text, words.join('')) : null;

  return loose ? { score: LETTERS + loose.score, ranges: loose.ranges } : null;
}

/**
 * How well `query` matches an item: its title first, then its fields (for a
 * node: hostname, image, addresses...), then its keywords.
 *
 * @param {object} item
 * @param {string} item.title
 * @param {{label: string, value: string}[]} [item.fields] what else the item
 *   is found by, named so the palette can say which one matched
 * @param {string[]} [item.keywords] more words, matched without saying so
 * @param {string} query
 * @returns {{score: number, ranges: number[][], field?: object}|null} with
 *   `field` ({label, value, ranges}) when a field matched rather than the
 *   title
 */
export function matchItem(item, query) {
  const words = wordsOf(query);

  if (!words.length) {
    return { score: 0, ranges: [] };
  }

  const title = byWords(item.title, words);

  if (title) {
    return { score: WORDS + title.score, ranges: title.ranges };
  }

  let best = null;

  for (const field of item.fields || []) {
    const found = byWords(field.value, words);

    if (found && (!best || found.score > best.score)) {
      best = { ...found, field };
    }
  }

  if (best) {
    return {
      score: FIELD + Math.round(best.score / 10),
      ranges: [],
      field: { ...best.field, ranges: best.ranges },
    };
  }

  const letters = byLetters(item.title, words.join(''));

  if (letters) {
    return { score: LETTERS + letters.score, ranges: letters.ranges };
  }

  return (item.keywords || []).length && byWords(item.keywords.join(' '), words)
    ? { score: KEYWORD, ranges: [] }
    : null;
}

/**
 * Splits text into runs for display, marking the matched ones.
 *
 * @param {string} text
 * @param {number[][]} [ranges] from matchText or matchItem
 * @returns {{text: string, match: boolean}[]}
 */
export function highlightParts(text, ranges = []) {
  const value = String(text ?? '');
  const parts = [];
  let last = 0;

  for (const [start, end] of ranges) {
    if (start > last) {
      parts.push({ text: value.slice(last, start), match: false });
    }

    parts.push({ text: value.slice(start, end), match: true });
    last = end;
  }

  if (last < value.length) {
    parts.push({ text: value.slice(last), match: false });
  }

  return parts;
}
