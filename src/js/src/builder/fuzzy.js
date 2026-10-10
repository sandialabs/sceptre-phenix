// Matching for the command palette: how well a query matches a title, and
// which letters matched. The palette uses this to rank and highlight results.
//
// Each word of the query must occur in the text, case-insensitive. A word at
// the start of the text ranks first, then a word at the start of a word, then
// a word anywhere. When a word is missing, the letters of the query must
// occur in order ("grp" finds "Group selection"), ranked by how close
// together they are. A word can also start one of the item's aliases, which
// are words that stand for its name ("export" for Download). It then counts
// as a word of the title. Keywords and a node's fields (its addresses, image
// and others) match only by words, and rank below any match on the title. As
// a last resort, the words can be spread over the title, the aliases and the
// keywords.

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

// The value of a word that starts an alias: the same as a word found at the
// start of a word, as far into the text as that penalty goes.
const ALIAS_GRADE = 200;
const ALIAS_SCORE = 150;

// Every word as a substring: {score, grade, ranges}, or null. The grade is
// the score without its penalty for words further into the text. A word that
// is not in the text may start one of the aliases instead, which marks no
// range.
function byWords(text, words, aliases = []) {
  const lower = String(text || '').toLowerCase();
  const ranges = [];
  let score = 0;
  let grade = 0;

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
      if (!aliases.some((alias) => alias.startsWith(word))) {
        return null;
      }

      grade += ALIAS_GRADE;
      score += ALIAS_SCORE;
      continue;
    }

    at = boundary >= 0 ? boundary : at;

    const worth = at === 0 ? 300 : boundary >= 0 ? 200 : 100;

    grade += worth;
    score += worth - Math.min(at, 50);
    ranges.push([at, at + word.length]);
  }

  return { score, grade, ranges: merge(ranges) };
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
 * How well `query` matches an item: first its title (with its aliases), then
 * its fields (for a node: hostname, image, addresses...), then its title,
 * aliases and keywords together. An empty query matches everything with score
 * 0. Ranges are [start, end) of the matched text.
 *
 * @param {object} item
 * @param {string} item.title
 * @param {string[]} [item.aliases] lower-case single words that stand for
 *   the title, matched as words of it without saying so
 * @param {{label: string, value: string}[]} [item.fields] what else the item
 *   is found by, named so the palette can say which one matched
 * @param {string[]} [item.keywords] more words, matched without saying so
 * @param {string} query
 * @returns {{score: number, ranges: number[][], grade?: number,
 *   field?: object}|null} `field` ({label, value, ranges}) is set when a
 *   field matched and the title did not. `grade` is set when the title
 *   matched by words. It is the score without the penalty for words further
 *   in, so it is equal for titles that differ only in the position of their
 *   words.
 */
export function matchItem(item, query) {
  const words = wordsOf(query);

  if (!words.length) {
    return { score: 0, ranges: [] };
  }

  const aliases = item.aliases || [];
  const title = byWords(item.title, words, aliases);

  if (title) {
    return {
      score: WORDS + title.score,
      grade: WORDS + title.grade,
      ranges: title.ranges,
    };
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

  // Each word in the title, an alias or a keyword: "save png" finds
  // Download PNG by a keyword and a word of its title.
  const rest = [...aliases, ...(item.keywords || [])];

  return rest.length && byWords([item.title, ...rest].join(' '), words)
    ? { score: KEYWORD, ranges: [] }
    : null;
}

/**
 * Splits text into runs for display, marking the matched ones.
 *
 * @param {string} text
 * @param {number[][]} [ranges] from matchItem
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
