// The letter case of the words the Builder writes into its sentences and
// names, and the length of a text as the server's limits count it.

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
