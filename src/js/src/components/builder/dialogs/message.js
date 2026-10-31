// Messages for the Builder dialogs' status and error regions. Each set changes
// `key`, so the region re-renders and announces the same text again.

import { nextTick, onScopeDispose, reactive, watch } from 'vue';

import { MAX_DOCUMENT_BYTES, tooLargeText } from '@/builder/decode.js';
import { sameButStamp } from '@/builder/model.js';

/** A message for one live region. `field` names the control it is about. */
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
 * A modal dialog's status region for store messages. The store records each
 * one (noteDialogSaid), so the page does not say it again after the dialog.
 */
export function useDialogStatus(store) {
  const status = useMessage();
  const { set } = status;

  status.set = (text, field = '') => {
    set.call(status, text, field);
    store.noteDialogSaid(status.text);
  };

  return status;
}

/** A form error that marks its control invalid, describes it and focuses it. */
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

    // Shows `message`. When it is about a field, focus moves to its control.
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
 * What the server listed when it refused a publish. It clears on open, on
 * close and on a diagram change, so it never shows against another diagram.
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
 * Reads the file a file field chose: its text and name, or why it was not
 * read. Null when no file is chosen or a newer choice replaced it.
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
 * The first line of a parser error, with its "(2:1)" position spelled out. The
 * code frame is dropped, because a screen reader reads it as punctuation.
 */
export function parseErrorText(message) {
  const [first] = String(message || '').split('\n');

  return first.trim().replace(/\((\d+):(\d+)\)$/, '(line $1, column $2)');
}

// The Upload source that converts a diagram of the legacy Builder.
export const LEGACY_SOURCE = 'Legacy Builder diagram or Topology';

/** The Upload message for XML text, a legacy diagram, or '' for other text. */
export function legacyDiagramHint(text) {
  return String(text || '')
    .trimStart()
    .startsWith('<')
    ? `This is possibly a legacy Builder diagram (XML). Select "${LEGACY_SOURCE}" to convert it.`
    : '';
}

/** A file name without its last extension: "plant.xml" is "plant". */
export function fileBaseName(name) {
  return String(name || '').replace(/\.[^.]*$/, '');
}
