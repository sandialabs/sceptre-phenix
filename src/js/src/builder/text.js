// The letter case of the words the Builder writes into its sentences and
// names, and what the server's limits check of a text: its length, and the
// control characters in it.

/**
 * @param {string} text
 * @returns {string} the text with its first letter a capital: "drive 1"
 *   gives "Drive 1"
 */
export function capitalize(text) {
  const value = String(text ?? '');

  return value.charAt(0).toUpperCase() + value.slice(1);
}

/**
 * The text as it reads inside a sentence: "Drives" gives "drives". An
 * acronym keeps its case: "DNS servers" stays as it is.
 *
 * @param {string} text
 * @returns {string}
 */
export function lowerFirst(text) {
  return /^[A-Z][a-z]/.test(text)
    ? text[0].toLowerCase() + text.slice(1)
    : text;
}

/**
 * @param {string} text
 * @returns {number} the length of the text in UTF-8 bytes
 */
export function utf8Length(text) {
  return new TextEncoder().encode(text).length;
}

/**
 * @param {string} text
 * @returns {boolean} whether the text holds a control character, as the
 *   server finds one: a character below U+0020, or U+007F
 */
export function hasControlCharacters(text) {
  return [...text].some(
    (ch) => ch.charCodeAt(0) < 0x20 || ch.charCodeAt(0) === 0x7f,
  );
}

/**
 * @param {string} text
 * @returns {boolean} whether the text holds a control character other than
 *   the newline and the tab, which text of several lines needs: what the
 *   server refuses in a diagram note
 */
export function hasControlCharactersInLines(text) {
  return [...text].some(
    (ch) => ch !== '\n' && ch !== '\t' && hasControlCharacters(ch),
  );
}

// The code points of the white space that Go's strings.TrimSpace removes
// (unicode.IsSpace). String's own trim differs by two characters: it keeps
// U+0085 and removes U+FEFF.
const GO_SPACE = new Set([
  0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0, 0x1680, 0x2000, 0x2001,
  0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a,
  0x2028, 0x2029, 0x202f, 0x205f, 0x3000,
]);

/**
 * @param {string} text
 * @returns {boolean} whether the text is empty or only white space, as the
 *   server finds it blank
 */
export function isBlank(text) {
  return [...String(text ?? '')].every((ch) => GO_SPACE.has(ch.codePointAt(0)));
}

/**
 * @param {string} text
 * @returns {string} the text without the white space around it, as the
 *   server's strings.TrimSpace gives it: U+0085 around the text goes, and
 *   U+FEFF stays (see GO_SPACE)
 */
export function trimSpace(text) {
  const characters = [...String(text ?? '')];
  const space = (ch) => GO_SPACE.has(ch.codePointAt(0));
  let start = 0;
  let end = characters.length;

  while (start < end && space(characters[start])) {
    start += 1;
  }

  while (end > start && space(characters[end - 1])) {
    end -= 1;
  }

  return characters.slice(start, end).join('');
}
