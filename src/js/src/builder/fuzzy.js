// Matching for the command palette: how well a query matches a title, and
// which letters matched. Title words rank first, then letters in order, then
// keywords and fields, then words spread over all of them.

// What starts a word, besides the start of the text.
const BOUNDARY = /[\s\-_.:/,›·()@#=]/;

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

const SURROGATE = /[\ud800-\udfff]/;

// Text in lower case, with where the letter behind each of its UTF-16 units
// starts and ends in the text (null when the units match one to one).
function lowerCase(text) {
  const value = String(text || '');
  const lower = value.toLowerCase();

  if (lower.length === value.length && !SURROGATE.test(value)) {
    return { lower, starts: null, ends: null };
  }

  const starts = [];
  const ends = [];
  let joined = '';

  for (let at = 0; at < value.length; ) {
    const letter = String.fromCodePoint(value.codePointAt(at));
    const low = letter.toLowerCase();

    for (let unit = 0; unit < low.length; unit++) {
      starts.push(at);
      ends.push(at + letter.length);
    }
    joined += low;
    at += letter.length;
  }

  return {
    lower: joined.length === lower.length ? lower : joined,
    starts,
    ends,
  };
}

// Ranges of lowerCase(text).lower as ranges of whole letters of the text.
function originalRanges({ starts, ends }, ranges) {
  return starts
    ? merge(ranges.map(([start, end]) => [starts[start], ends[end - 1]]))
    : ranges;
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
  const lowered = lowerCase(text);
  const found = wordsIn(lowered.lower, words, aliases);

  return found && { ...found, ranges: originalRanges(lowered, found.ranges) };
}

// byWords for text that is already lower case.
function wordsIn(lower, words, aliases = []) {
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
  const lowered = lowerCase(text);
  const { lower } = lowered;
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

    ranges.push([at, at + letter.length]);
    next = at + letter.length;
  }

  return {
    score: Math.max(1, 100 - gaps * 5 - first),
    ranges: originalRanges(lowered, merge(ranges)),
  };
}

// The title, aliases and keywords of each item, in lower case, for the last
// resort of matchItem. Items do not change, so each is joined once.
const keywordTexts = new WeakMap();

function keywordText(item) {
  if (!item.aliases?.length && !item.keywords?.length) {
    return '';
  }

  let text = keywordTexts.get(item);

  if (text === undefined) {
    text = [item.title, ...(item.aliases || []), ...(item.keywords || [])]
      .join(' ')
      .toLowerCase();
    keywordTexts.set(item, text);
  }

  return text;
}

// The title, with its aliases, by words.
function titleMatch(item, words) {
  const title = byWords(item.title, words, item.aliases || []);

  return (
    title && {
      score: WORDS + title.score,
      grade: WORDS + title.grade,
      ranges: title.ranges,
    }
  );
}

// The text of the last resort of matchItem: keywordText, and with
// `fields`, the title and the values of the fields too.
function spreadText(item, fields) {
  const text = keywordText(item);

  if (!fields || !item.fields?.length) {
    return text;
  }

  return [text || item.title, ...item.fields.map((field) => field.value)]
    .map((part) => String(part ?? ''))
    .join(' ')
    .toLowerCase();
}

// The title letter by letter.
function lettersMatch(item, words) {
  const letters = byLetters(item.title, words.join(''));

  return letters && { score: LETTERS + letters.score, ranges: letters.ranges };
}

/**
 * How well `query` matches an item's title and aliases. matchItem with
 * `name: false` matches the rest, so a caller can list name matches first.
 */
export function matchName(item, query) {
  const words = wordsOf(query);

  return words.length
    ? titleMatch(item, words) || lettersMatch(item, words)
    : { score: 0, ranges: [] };
}

/**
 * How well `query` matches an item: title and aliases, then fields, then the
 * words spread over all of them. Ranges are [start, end). `grade` is the
 * title score without the penalty for word position.
 */
export function matchItem(item, query, { name = true, spread = false } = {}) {
  const words = wordsOf(query);

  if (!words.length) {
    return { score: 0, ranges: [] };
  }

  const title = name && titleMatch(item, words);

  if (title) {
    return title;
  }

  let best = null;

  for (const field of item.fields || []) {
    const rank = field.rank ?? 0;

    // A field of a lower rank goes first (see fieldOrder).
    if (best && rank > best.rank) {
      continue;
    }

    const found = byWords(field.value, words);

    if (found && (!best || rank < best.rank || found.score > best.score)) {
      best = { ...found, field, rank };
    }
  }

  if (best) {
    return {
      score: Math.min(FIELD + Math.round(best.score / 10), WORDS - 1),
      ranges: [],
      field: { ...best.field, ranges: best.ranges },
    };
  }

  const letters = name && lettersMatch(item, words);

  if (letters) {
    return letters;
  }

  // Each word in the title, an alias or a keyword: "save png" finds
  // Download PNG by a keyword and a word of its title.
  const keywords = spreadText(item, spread);

  return keywords && wordsIn(keywords, words)
    ? { score: KEYWORD, ranges: [] }
    : null;
}

/**
 * The order of two field matches: the lower `rank` first. Their scores order
 * any other pair.
 */
export function fieldOrder(a, b) {
  return a.field && b.field ? (a.field.rank ?? 0) - (b.field.rank ?? 0) : 0;
}

/** Splits text into runs for display, marking the matched ones. */
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
