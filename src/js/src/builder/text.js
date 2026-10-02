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
