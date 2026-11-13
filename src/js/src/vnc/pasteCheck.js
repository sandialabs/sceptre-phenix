// The pre-check of a paste in paste-as-keys mode. noVNC sends cut text as
// Latin-1: a code point above U+00FF goes as "?", and U+0080–U+00FF as one
// byte. The minimega shim types only printable ASCII, Tab and Enter, and
// skips the other characters.
import { shallowRef } from 'vue';

/**
 * The estimated typing time of one character. Keep it near the paste pacing
 * of the minimega shim: 10 ms before each key event, two events for each
 * character.
 */
export const PASTE_MS_PER_CHAR = 20;

/** The estimated typing time of a character that needs Shift: four events. */
export const PASTE_MS_PER_SHIFTED_CHAR = 40;

/** Past this many characters, the panel says the bidirectional mode is faster. */
export const BIDIRECTIONAL_HINT_CHARS = 5000;

const TAB = 0x09;
const LINE_FEED = 0x0a;
const CARRIAGE_RETURN = 0x0d;

// the characters that need Shift on the en-us keymap of the minimega shim
const SHIFTED = /^[A-Z!"#$%&()*+:<>?@^_{|}~]$/;

const typedAsIs = (code) =>
  (code >= 0x20 && code <= 0x7e) ||
  code === TAB ||
  code === LINE_FEED ||
  code === CARRIAGE_RETURN;

/**
 * @typedef {object} PasteCheck
 * @property {string} text the text to paste
 * @property {number} typed the characters that are typed, "?" included
 * @property {string[]} asQuestion each character above U+00FF, once, in order
 * @property {number} asQuestionCount every character typed as "?"
 * @property {string[]} skipped each skipped character, once, in order
 * @property {number} skippedCount every skipped character
 * @property {number} seconds the estimated typing time
 * @property {boolean} suggestBidirectional more than BIDIRECTIONAL_HINT_CHARS
 * @property {boolean} clean every character is typed as it is
 */

/**
 * Checks what a paste in paste-as-keys mode types.
 *
 * @param {string} text
 * @returns {PasteCheck}
 */
export function checkPaste(text) {
  const asQuestion = new Set();
  const skipped = new Set();
  let typed = 0;
  let asQuestionCount = 0;
  let skippedCount = 0;
  let length = 0;
  let ms = 0;

  // by code point, as noVNC sends it
  for (const char of text) {
    length += 1;
    const code = char.codePointAt(0);
    if (code > 0xff) {
      asQuestion.add(char);
      asQuestionCount += 1;
      typed += 1;
      ms += PASTE_MS_PER_SHIFTED_CHAR;
    } else if (typedAsIs(code)) {
      typed += 1;
      ms += SHIFTED.test(char) ? PASTE_MS_PER_SHIFTED_CHAR : PASTE_MS_PER_CHAR;
    } else {
      skipped.add(char);
      skippedCount += 1;
    }
  }

  return {
    text,
    typed,
    asQuestion: [...asQuestion],
    asQuestionCount,
    skipped: [...skipped],
    skippedCount,
    seconds: Math.ceil(ms / 1000),
    suggestBidirectional: length > BIDIRECTIONAL_HINT_CHARS,
    clean: asQuestionCount === 0 && skippedCount === 0,
  };
}

/**
 * The estimated typing time as text, for example "about 1 s".
 *
 * @param {number} seconds
 */
export function typingTime(seconds) {
  return seconds < 60
    ? `about ${seconds} s`
    : `about ${Math.ceil(seconds / 60)} min`;
}

/**
 * How the panel names a character: as it is, or as U+XXXX when it is a
 * control or space character that shows nothing.
 *
 * @param {string} char one code point
 */
export function charLabel(char) {
  if (!/^[\p{C}\p{Z}]$/u.test(char)) return char;
  const hex = char.codePointAt(0).toString(16).toUpperCase().padStart(4, '0');
  return `U+${hex}`;
}

/**
 * The paste flow of the clipboard panel in paste-as-keys mode. A clean paste
 * goes to `send` at once. Any other paste waits in `pending` for "Type
 * anyway" or "Cancel".
 *
 * @param {(text: string) => void} send types the text, for example
 *   RFB.clipboardPasteFrom
 * @returns {{ pending: import('vue').ShallowRef<PasteCheck|null>,
 *   paste: (text: string) => boolean, typeAnyway: () => void,
 *   cancel: () => void }} paste returns true when it sent the text
 */
export function usePastePrecheck(send) {
  const pending = shallowRef(null);

  return {
    pending,
    paste(text) {
      const check = checkPaste(text);
      if (!check.clean) {
        pending.value = check;
        return false;
      }
      pending.value = null;
      send(text);
      return true;
    },
    typeAnyway() {
      const check = pending.value;
      pending.value = null;
      if (check) send(check.text);
    },
    cancel() {
      pending.value = null;
    },
  };
}
