// Messages for the Builder dialogs' status and error regions, what the
// server listed when it refused a publish, and the file a dialog's file
// field chooses.
//
// A dialog renders each region, empty, from the start, and each message as a
// child keyed by `key`. Setting a message always changes the key, so the
// region gets a new node and announces the text again even when it is the
// same, as when a user submits the same mistake twice.

import { nextTick, onScopeDispose, reactive, watch } from 'vue';

import { MAX_DOCUMENT_BYTES, tooLargeText } from '@/builder/decode.js';
import { sameButStamp } from '@/builder/model.js';

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
 * What the server listed when it refused a publish (`publishIssues`, see
 * publish in store.js), for the Publish dialog, which calls this in its
 * setup. The list is cleared as the dialog opens, as it closes (its setup's
 * scope ends), and once the diagram changes after the refusal, so it never
 * shows against a diagram other than the one refused. A change of who saved
 * the diagram and when (see sameButStamp) leaves the diagram as it was.
 *
 * @param {{doc: object, publishIssues: object[]}} store the Builder store
 */
export function useRefusalIssues(store) {
  store.publishIssues = [];

  watch(
    () => store.doc,
    (next, previous) => {
      if (store.publishIssues.length > 0 && !sameButStamp(next, previous)) {
        store.publishIssues = [];
      }
    },
  );

  onScopeDispose(() => {
    store.publishIssues = [];
  });
}

// The read under way in each file field.
const reads = new WeakMap();

/**
 * Reads the file a file field chose, for its change event. A file chosen
 * while another was read replaces it: the older read gives null.
 *
 * @param {Event} event
 * @param {string} noun what the file holds, for the message when it is too
 *   large: "file", "config", "scenario"
 * @returns {Promise<{text?: string, name?: string, error?: string}|null>}
 *   the file's text and its name, or why it was not read; null when no file
 *   is chosen, or when another replaced it
 */
export async function readChosenFile(event, noun) {
  const field = event.target;
  const file = field.files?.[0];
  const read = {};

  reads.set(field, read);

  if (!file) {
    return null;
  }

  if (file.size > MAX_DOCUMENT_BYTES) {
    return { error: tooLargeText(noun) };
  }

  const text = await file.text();

  return reads.get(field) === read ? { text, name: file.name } : null;
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

// The Upload source that converts a diagram of the legacy Builder.
export const LEGACY_SOURCE = 'Legacy Builder diagram or Topology';

/**
 * What Upload says of text that is XML when it is given as a Builder
 * document: no Builder document is XML, and a diagram of the legacy Builder
 * is, which another source of the dialog converts.
 *
 * @param {string} text the text of the file, or the pasted text
 * @returns {string} the message, or '' for text that is not XML
 */
export function legacyDiagramHint(text) {
  return String(text || '')
    .trimStart()
    .startsWith('<')
    ? `This looks like a legacy Builder diagram (XML). Choose "${LEGACY_SOURCE}" to convert it.`
    : '';
}

/**
 * A file's name without its last extension, which names the diagram
 * converted from it: "plant.xml" is "plant".
 *
 * @param {string} name
 * @returns {string}
 */
export function fileBaseName(name) {
  return String(name || '').replace(/\.[^.]*$/, '');
}
