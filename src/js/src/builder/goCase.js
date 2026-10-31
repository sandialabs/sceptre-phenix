// Letter case and white space as the server's Go code sees them, from Go's
// own tables (schema/go-case.json, written by case_test.go in types/builder).

import table from './schema/go-case.json';

// The white space strings.TrimSpace trims. String's trim keeps U+0085 and
// trims U+FEFF. Each is one UTF-16 code unit.
const GO_SPACE = new Set([
  0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0, 0x1680, 0x2000, 0x2001,
  0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a,
  0x2028, 0x2029, 0x202f, 0x205f, 0x3000,
]);

// The ASCII white space Go's code trims around an address or a proto
// (trimASCIISpace in types/builder/topology.go).
const ASCII_SPACE = new Set([0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20]);

// The text without the code units of `space` around it, in one pass.
function trimCodes(text, space) {
  let start = 0;
  let end = text.length;

  while (start < end && space.has(text.charCodeAt(start))) {
    start += 1;
  }

  while (end > start && space.has(text.charCodeAt(end - 1))) {
    end -= 1;
  }

  return text.slice(start, end);
}

const REPLACEMENT = 0xfffd;

/** Text without the white space around it, as strings.TrimSpace gives it. */
export function goTrimSpace(text) {
  return trimCodes(text, GO_SPACE);
}

/**
 * Text without the ASCII white space around it (trimASCIISpace in
 * types/builder/topology.go). String's trim and strings.TrimSpace differ.
 */
export function trimASCIISpace(text) {
  return trimCodes(text, ASCII_SPACE);
}

// The character the runs map code to. A run [first, last, delta, stride]
// adds delta to first, first + stride, ... up to last.
function mapped(runs, code) {
  let low = 0;
  let high = runs.length;

  while (low < high) {
    const middle = (low + high) >>> 1;
    const [first, last, delta, stride] = runs[middle];

    if (last < code) {
      low = middle + 1;
    } else if (first > code) {
      high = middle;
    } else {
      return (code - first) % stride === 0 ? code + delta : code;
    }
  }

  return code;
}

function isASCII(text) {
  for (let index = 0; index < text.length; index += 1) {
    if (text.charCodeAt(index) > 0x7f) {
      return false;
    }
  }

  return true;
}

// The characters of text as Go decodes them from JSON: a lone surrogate is
// U+FFFD.
function codePoints(text) {
  return Array.from(text, (character) => {
    const code = character.codePointAt(0);

    return code >= 0xd800 && code <= 0xdfff ? REPLACEMENT : code;
  });
}

/**
 * @param {string} text
 * @returns {string} the text as strings.ToLower gives it: each character
 *   on its own, so "İ" gives "i" and a final "Σ" gives "σ"
 */
export function goToLower(text) {
  if (isASCII(text)) {
    return text.toLowerCase();
  }

  return codePoints(text)
    .map((code) => String.fromCodePoint(mapped(table.lower, code)))
    .join('');
}

/**
 * The key that values differing only in case and outer space share (foldKey in
 * types/builder/spec.go).
 */
export function foldKey(value) {
  return goToLower(goTrimSpace(String(value ?? '')));
}

/** Whether strings.EqualFold takes the texts as one. */
export function goEqualFold(a, b) {
  if (isASCII(a) && isASCII(b)) {
    return a.length === b.length && a.toLowerCase() === b.toLowerCase();
  }

  const first = codePoints(a);
  const second = codePoints(b);

  return (
    first.length === second.length &&
    first.every(
      (code, index) =>
        mapped(table.fold, code) === mapped(table.fold, second[index]),
    )
  );
}
