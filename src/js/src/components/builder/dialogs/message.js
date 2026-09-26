// Messages for the Builder dialogs' status and error regions.
//
// A dialog renders each region, empty, from the start, and each message as a
// child keyed by `key`. Setting a message always changes the key, so the
// region gets a new node and announces the text again even when it is the
// same, as when a user submits the same mistake twice.

import { nextTick, reactive } from 'vue';

/**
 * A message for one live region. `field` names the form control the message
 * is about, so the dialog can mark that control invalid and point it at the
 * message.
 *
 * @returns {{text: string, key: number, field: string,
 *   set: (text: string, field?: string) => void, clear: () => void}}
 */
export function useMessage() {
  return reactive({
    text: '',
    key: 0,
    field: '',
    set(text, field = '') {
      this.text = text || '';
      this.field = this.text ? field : '';
      this.key += 1;
    },
    clear() {
      this.text = '';
      this.field = '';
    },
  });
}

/**
 * The error message of a dialog form, tied to the control it is about: that
 * control is marked invalid, described by the message, and takes focus.
 *
 * @param {string} errorId id of the element that shows the message
 * @param {Object<string, string>} fieldIds control id for each field name
 */
export function useFieldError(errorId, fieldIds = {}) {
  const error = useMessage();

  return {
    error,

    // aria-invalid for a field's control.
    invalid(field) {
      return error.field === field ? 'true' : undefined;
    },

    // A control's hints, followed by the message while it is about it.
    describedBy(field, hints = '') {
      return (
        [hints, error.field === field ? errorId : '']
          .filter(Boolean)
          .join(' ') || undefined
      );
    },

    // Shows `message`; when it is about a field, focus moves to its control.
    async fail(message, field = '') {
      error.set(message, field);

      if (fieldIds[field]) {
        await nextTick();
        document.getElementById(fieldIds[field])?.focus();
      }
    },
  };
}

/**
 * The first line of a document parser's error, with its "(2:1)" position
 * spelled out. The code frame the parser adds after it (source lines and a
 * "-----^" pointer) is read aloud as punctuation, so it is dropped.
 *
 * @param {string} message
 * @returns {string}
 */
export function parseErrorText(message) {
  const [first] = String(message || '').split('\n');

  return first.trim().replace(/\((\d+):(\d+)\)$/, '(line $1, column $2)');
}
