// State shared by the Inspector's JSON Forms renderers.
//
// The vanilla renderers leave the error and help text unconnected to the
// input and show help only while the field has focus. These renderers give
// each field ids for its input, help text, error and warnings, and wire them
// with aria-describedby, aria-invalid and aria-required. The help text is the
// schema's description of the field, shown as a tooltip on its label (see
// fieldTooltip.js).

import { computed, inject, ref, unref, watch } from 'vue';
import { getControlPath } from '@jsonforms/core';
import { useJsonForms } from '@jsonforms/vue';

import { drawnColor } from '@/builder/colors.js';

// Provided by BuilderInspector: announces a message in the Builder's live
// region. Renderers rendered anywhere else fall back to doing nothing.
export const INSPECTOR_ANNOUNCE = Symbol('inspector-announce');

export function useInspectorAnnounce() {
  return inject(INSPECTOR_ANNOUNCE, () => {});
}

// Provided by BuilderInspector: a count that goes up each time the form
// reloads its data from the document (Cancel, undo, Apply), which may leave a
// field's data as it was. Renderers rendered anywhere else get a count that
// never changes.
export const INSPECTOR_RESETS = Symbol('inspector-resets');

export function useInspectorResets() {
  return inject(INSPECTOR_RESETS, () => ref(0), true);
}

// Provided by BuilderInspector: problems a renderer keeps to itself, such as
// a key/value row with no name, which never reaches the working copy. Each
// renderer reports its own under its data path, as {path, message} entries
// that the error summary lists and links to (path is the data-path of the
// element to focus), and none once it goes. While any are reported, Apply
// is refused, and the edit is not left to vanish silently. Renderers
// rendered anywhere else report to nothing.
export const INSPECTOR_LOCAL_PROBLEMS = Symbol('inspector-local-problems');

export function useInspectorLocalProblems() {
  return inject(INSPECTOR_LOCAL_PROBLEMS, () => () => {}, true);
}

// Provided by BuilderInspector: warnings about the working copy, keyed by
// the JSON Forms data path of the field they concern (control.path, for
// example "spec.network.interfaces.0.vlan"), each a list of messages. A
// warning does not block Apply. Renderers rendered anywhere else get none.
export const INSPECTOR_FIELD_WARNINGS = Symbol('inspector-field-warnings');

export function useInspectorFieldWarnings() {
  return inject(INSPECTOR_FIELD_WARNINGS, () => ref({}), true);
}

/**
 * The warnings for one field, each message once. The list stays the same
 * array while its messages do: the provider recomputes whenever the working
 * copy, the diagram or the server's disk images change, and a new array
 * would re-render every field for nothing.
 *
 * @param {import('vue').ComputedRef<object>} control
 * @returns {import('vue').ComputedRef<string[]>}
 */
export function useFieldWarnings(control) {
  const warnings = useInspectorFieldWarnings();

  return computed((previous) => {
    const next = [...new Set(unref(warnings)?.[control.value.path] || [])];

    return previous?.length === next.length &&
      next.every((message, index) => message === previous[index])
      ? previous
      : next;
  });
}

// Provided by BuilderInspector: what a field shows while it is not set,
// given its data path and schema, as {value, note} or undefined (see
// fieldDefault in adapters/forms.js). Renderers rendered anywhere else show
// none.
export const INSPECTOR_DEFAULTS = Symbol('inspector-defaults');

/**
 * What a field shows while it has no value (unset, or empty text): the value
 * it comes to all the same, such as the Memory phenix gives a VM or the
 * network name a connection is drawn with. It is shown, not stored.
 *
 * @param {import('vue').ComputedRef<object>} control
 * @returns {import('vue').ComputedRef<{value: unknown, note: string}|undefined>}
 *   undefined while the field has a value, or has no default
 */
export function useFieldDefault(control) {
  const unset = useUnsetValue(control);

  return computed(() => {
    const { data } = control.value;

    return data !== undefined && data !== null && data !== ''
      ? undefined
      : unset.value;
  });
}

/**
 * What a field comes to with no value (see useFieldDefault), whether or not
 * it has one now: for a choice that stands for no value, which names it
 * (see InspectorEnumControl).
 *
 * @param {import('vue').ComputedRef<object>} control
 * @returns {import('vue').ComputedRef<{value: unknown, note: string}|undefined>}
 *   undefined for a field with no default
 */
export function useUnsetValue(control) {
  const defaults = inject(INSPECTOR_DEFAULTS, () => ref(() => undefined), true);

  return computed(() =>
    unref(defaults)?.(control.value.path, control.value.schema),
  );
}

// Provided by BuilderInspector: whether the working copy changed the value
// at a data path from the element's, or the entry of the map there with a
// given key (see fieldChanged in adapters/forms.js), for a mark on the field
// until the change is applied or cancelled. Renderers rendered anywhere else
// mark nothing.
export const INSPECTOR_CHANGED = Symbol('inspector-changed');

export function useFieldChanged(control) {
  const changed = inject(INSPECTOR_CHANGED, () => ref(() => false), true);

  return computed(() => Boolean(unref(changed)?.(control.value.path)));
}

/**
 * Whether the working copy changed an entry of a map field (keys and
 * values; see InspectorMapRenderer), given the entry's key, which may hold
 * dots: added, removed or given another value.
 *
 * @param {import('vue').ComputedRef<object>} control the map's control
 * @returns {(key: string) => boolean}
 */
export function useEntryChanged(control) {
  const changed = inject(INSPECTOR_CHANGED, () => ref(() => false), true);

  return (key) => Boolean(unref(changed)?.(control.value.path, key));
}

// Provided by BuilderInspector: how the canvas draws a Color field's color,
// for the field's chip and swatches: a network's (see drawnNetworkColor),
// or another element's (see drawnColor). Renderers rendered anywhere else
// draw it as chosen.
export const INSPECTOR_DRAWN_COLOR = Symbol('inspector-drawn-color');

export function useInspectorDrawnColor() {
  return inject(INSPECTOR_DRAWN_COLOR, () => ref(drawnColor), true);
}

/**
 * The text a field shows, which its input binds in place of its data: what
 * was typed in it, until its data changes or the form reloads it (Cancel,
 * undo, Apply; see useInspectorResets). Anything else that re-renders the
 * field, such as new warnings, the server's disk images arriving or a schema
 * update, patches the input's value with what it binds, and would put the
 * data back over text typed and not yet committed.
 *
 * @param {import('vue').ComputedRef<object>} control
 * @param {object} [options]
 * @param {(data: unknown) => unknown} [options.format] the text for the
 *   field's data
 * @param {import('vue').ComputedRef<{value: unknown}|undefined>} [options.fallback]
 *   what the field shows while it has no value (see useFieldDefault)
 * @returns {{text: import('vue').Ref<unknown>, onInput: (event: Event) => void, sync: () => void}}
 *   `onInput` takes the typed text; `sync` shows the data again, as the
 *   form reads it once committed ("7" for "07")
 */
export function useFieldText(
  control,
  { format = (data) => data, fallback } = {},
) {
  const resets = useInspectorResets();
  const text = ref('');

  function sync() {
    const shown = fallback?.value ? fallback.value.value : control.value.data;

    text.value = format(shown) ?? '';
  }

  // Data replaced from outside the field shows even when it is the data the
  // field had, and so does a default that changes (a network renamed).
  watch(
    [() => control.value.data, resets, () => fallback?.value?.value],
    sync,
    { immediate: true },
  );

  function onInput(event) {
    text.value = event.target.value;
  }

  return { text, onInput, sync };
}

function typesOf(schema) {
  return [schema?.type].flat().filter(Boolean);
}

/**
 * The number alternative of a field phenix takes as a number or as text
 * (Memory, VCPUs: `oneOf` an integer and a string, the text being a
 * template such as {{ .Memory }}). The Inspector edits such a field as a
 * number, with no picker for its kind (see InspectorInputControl).
 *
 * @param {object} schema the field's schema
 * @returns {object|undefined} the number alternative, or undefined for any
 *   other field
 */
export function numberBranch(schema) {
  const branches = schema?.oneOf;

  if (!Array.isArray(branches) || branches.length !== 2) {
    return undefined;
  }

  const number = branches.find(
    (branch) =>
      typesOf(branch).some((type) => ['integer', 'number'].includes(type)) &&
      branch.enum === undefined &&
      branch.const === undefined,
  );
  const text = branches.find((branch) => typesOf(branch).includes('string'));

  return number && text && number !== text ? number : undefined;
}

// A number as text, and a whole number: digits alone.
const NUMBER_TEXT = /^\s*[-+]?(\d+(\.\d*)?|\.\d+)([eE][-+]?\d+)?\s*$/;
const WHOLE_TEXT = /^\s*[-+]?\d+\s*$/;

/**
 * The number that text typed in a number field stands for: undefined for
 * text that is no number, and NaN for one a whole-number field cannot
 * take, such as 1.5 or 1e3, which parseInt read as 1.
 *
 * @param {string} text
 * @param {boolean} whole the field takes whole numbers only (integer)
 * @returns {number|undefined}
 */
export function readNumberText(text, whole) {
  const typed = String(text ?? '');

  if (!NUMBER_TEXT.test(typed)) {
    return undefined;
  }

  return whole && !WHOLE_TEXT.test(typed) ? NaN : Number(typed);
}

// The alternatives of a field, with those of nested oneOf and anyOf
// flattened and null left out.
function alternatives(schema) {
  const branches = schema?.oneOf || schema?.anyOf;

  if (!Array.isArray(branches)) {
    return [schema];
  }

  return branches
    .flatMap(alternatives)
    .filter(
      (branch) => !(typesOf(branch).length === 1 && branch.type === 'null'),
    );
}

// The first description of a schema or, failing that, of its alternatives.
function describedIn(schema) {
  if (schema?.description) {
    return schema.description;
  }

  for (const branch of [...(schema?.oneOf || []), ...(schema?.anyOf || [])]) {
    const found = describedIn(branch);

    if (found) {
      return found;
    }
  }

  return undefined;
}

/**
 * Whether a field holds one text value or a list of them (an interface's
 * DNS servers: `anyOf` one address or a list, or null), which the Inspector
 * edits as one line of values separated by commas (see
 * InspectorInputControl). JSON Forms has no renderer for such an anyOf,
 * and showed "No applicable renderer found." in its place.
 *
 * @param {object} schema the field's schema
 * @returns {boolean}
 */
export function isTextOrList(schema) {
  if (!schema?.oneOf && !schema?.anyOf) {
    return false;
  }

  const branches = alternatives(schema);
  const text = branches.filter((branch) => typesOf(branch).includes('string'));
  const lists = branches.filter(
    (branch) =>
      typesOf(branch).includes('array') &&
      typesOf(branch.items).includes('string'),
  );

  return (
    text.length === 1 &&
    lists.length === 1 &&
    branches.length === 2 &&
    !text[0].enum &&
    !lists[0].items.enum
  );
}

// Provided by BuilderInspector: the item a list's Add button appends, given
// the list's data path and the form's data, for a list whose new items need
// more than their schema's defaults (a new interface is named the way a
// connection drawn on the canvas names one). Undefined leaves the schema's
// default item, as it is for renderers rendered anywhere else.
export const INSPECTOR_NEW_ITEM = Symbol('inspector-new-item');

export function useInspectorNewItem() {
  return inject(INSPECTOR_NEW_ITEM, () => () => undefined, true);
}

// Provided by BuilderInspector: values a free-text field suggests as the
// user types, by kind ({disks: [...]}: the server's disk images, or null
// while they are unknown). Renderers rendered anywhere else suggest nothing.
export const INSPECTOR_SUGGESTIONS = Symbol('inspector-suggestions');

export function useInspectorSuggestions() {
  return inject(INSPECTOR_SUGGESTIONS, () => ref({}), true);
}

/**
 * What a field suggests for the text typed in it: the values that contain
 * it, ignoring case, those that start with it first, each group in
 * alphabetical order. Empty text suggests every value.
 *
 * @param {string[]} values
 * @param {string} text
 * @param {number} [limit] the most to suggest
 * @returns {string[]}
 */
export function suggestionsFor(values, text, limit = 50) {
  const wanted = String(text ?? '')
    .trim()
    .toLowerCase();
  const starting = [];
  const containing = [];

  for (const value of [...(values || [])].sort((a, b) => a.localeCompare(b))) {
    const lower = String(value).toLowerCase();

    if (lower.startsWith(wanted)) {
      starting.push(value);
    } else if (lower.includes(wanted)) {
      containing.push(value);
    }
  }

  return [...starting, ...containing].slice(0, limit);
}

// Provided by BuilderInspector: true while the form shows a device from an
// included topology in a draft that can be edited. JSON Forms is then read
// only, but its fields are locked rather than disabled (see
// useInspectorLocked). Renderers rendered anywhere else get false.
export const INSPECTOR_LOCKED = Symbol('inspector-locked');

/**
 * Whether a control that cannot change is locked: shown read only rather
 * than disabled, so Tab still reaches it and its value is read at full
 * contrast. That is every field of an included device, a field the Inspector
 * locks (the uischema `readonly` option, see inspectorLock), and a field its
 * schema marks readOnly. In a read-only draft every field is disabled.
 *
 * @param {import('vue').ComputedRef<object>} control
 * @returns {import('vue').ComputedRef<boolean>}
 */
export function useInspectorLocked(control) {
  const lockedForm = inject(INSPECTOR_LOCKED, false);
  const jsonforms = useJsonForms(true);

  return computed(() => {
    if (unref(lockedForm)) {
      return true;
    }

    return (
      !jsonforms?.readonly &&
      (control.value.schema?.readOnly === true ||
        control.value.uischema?.options?.readonly === true)
    );
  });
}

// Text that reads as a value other than itself: JSON's literals, numbers,
// quoted text, lists and objects.
const NOT_TEXT =
  /^(true|false|null|-?\d+(\.\d+)?([eE][+-]?\d+)?|"[\s\S]*"|\[[\s\S]*\]|\{[\s\S]*\})$/;

/**
 * The value that text typed for an annotation stands for, read the way a
 * topology's YAML reads it: true, false, null, a number, quoted text, a
 * list or an object in JSON as such, and anything else as the text.
 *
 * @param {string} text
 * @returns {unknown}
 */
export function readValue(text) {
  const trimmed = String(text).trim();

  if (NOT_TEXT.test(trimmed)) {
    try {
      return JSON.parse(trimmed);
    } catch {
      // Not JSON after all: text.
    }
  }

  return text;
}

/**
 * The text an annotation's value shows as, which readValue reads back as
 * the same value: text that would read as another value is quoted.
 *
 * @param {unknown} value
 * @returns {string}
 */
export function valueText(value) {
  if (typeof value === 'string') {
    return readValue(value) === value ? value : JSON.stringify(value);
  }

  return JSON.stringify(value ?? null);
}

/**
 * Labels the control that edits a whole value (scope "#"), which JSON Forms
 * generates for primitive list items and oneOf branches and leaves
 * unlabelled.
 *
 * @param {object} uischema generated UI schema, possibly a one-element layout
 * @param {string} label
 * @returns {object}
 */
export function labelWholeValue(uischema, label) {
  if (uischema?.type === 'Control' && uischema.scope === '#') {
    return { ...uischema, label };
  }

  if (Array.isArray(uischema?.elements) && uischema.elements.length === 1) {
    return {
      ...uischema,
      elements: [labelWholeValue(uischema.elements[0], label)],
    };
  }

  return uischema;
}

const COMBINATORS = ['oneOf', 'anyOf', 'allOf'];

/**
 * The messages of the errors about a field that JSON Forms gives it none
 * of: a field whose schema is a oneOf of alternatives edited as one value
 * (Memory as a number, DNS servers as a line of text). JSON Forms shows such
 * a field only errors of its whole schema, and its errors come from its
 * alternatives.
 *
 * @param {import('vue').ComputedRef<object>} control
 * @returns {import('vue').ComputedRef<string[]>}
 */
function useOwnErrors(control) {
  const jsonforms = useJsonForms(true);

  return computed(() => {
    const core = jsonforms?.core;
    const translate = jsonforms?.i18n?.translateError;

    // An alternative of the wrong type says least: a number below Memory's
    // minimum is not text either.
    return [...(core?.errors || []), ...(core?.additionalErrors || [])]
      .filter(
        (error) =>
          !COMBINATORS.includes(error.keyword) &&
          getControlPath(error) === control.value.path,
      )
      .sort((a, b) => (a.keyword === 'type') - (b.keyword === 'type'))
      .map((error) =>
        translate
          ? translate(error, jsonforms.i18n.translate, control.value.uischema)
          : error.message,
      );
  });
}

/**
 * Wraps a JSON Forms control binding (`useJsonFormsControl` and friends)
 * with ids and ARIA attributes for its input.
 *
 * @param {{control: import('vue').ComputedRef<object>, handleChange: Function}} input
 * @param {(target: HTMLElement) => unknown} [adapt] reads the new value from
 *   the element that fired `change`
 * @param {object} [options]
 * @param {boolean} [options.ownErrors] the field's errors come from the
 *   alternatives of its oneOf, which JSON Forms does not give it
 * @param {import('vue').ComputedRef<object|undefined>} [options.fallback]
 *   what the field shows while it has no value (see useFieldDefault), which
 *   the input is described by
 */
export function useInspectorControl(
  input,
  adapt = (target) => target.value,
  { ownErrors = false, fallback } = {},
) {
  const { control } = input;

  const ids = computed(() => {
    // JSON Forms assigns ids when the control mounts; server rendering has
    // none, so fall back to the data path.
    const base = control.value.id || `field-${control.value.path || 'root'}`;

    return {
      input: `${base}-input`,
      description: `${base}-description`,
      error: `${base}-error`,
      warning: `${base}-warning`,
      fallback: `${base}-default`,
      changed: `${base}-changed`,
    };
  });

  const branchErrors = ownErrors ? useOwnErrors(control) : null;

  // A field edited as one value that is described only in its alternatives
  // (DNS: the description is on the anyOf's first alternative) is described
  // by that. Render InspectorField with `field` for it.
  const field = computed(() => {
    const own = control.value;
    const description =
      own.description || (ownErrors ? describedIn(own.schema) : undefined);

    return description === own.description ? own : { ...own, description };
  });

  // One message per field: the Inspector gives JSON Forms only the most
  // relevant error of each field (see relevantErrors), and JSON Forms joins
  // one message per error with newlines, repeating a message when several
  // oneOf branches fail the same way.
  const errorText = computed(
    () =>
      (branchErrors
        ? branchErrors.value
        : String(control.value.errors || '').split('\n')
      ).filter(Boolean)[0] || '',
  );

  // Warnings describe the field but do not make it invalid.
  const warnings = useFieldWarnings(control);

  const locked = useInspectorLocked(control);

  const changed = useFieldChanged(control);

  const inputAttrs = computed(() => {
    const describedBy = [
      errorText.value && ids.value.error,
      warnings.value.length && ids.value.warning,
      field.value.description && ids.value.description,
      fallback?.value && ids.value.fallback,
      changed.value && ids.value.changed,
    ].filter(Boolean);

    return {
      id: ids.value.input,
      disabled: !control.value.enabled && !locked.value,
      readonly: locked.value || undefined,
      'aria-invalid': errorText.value ? 'true' : undefined,
      'aria-required': control.value.required ? 'true' : undefined,
      'aria-describedby': describedBy.join(' ') || undefined,
    };
  });

  function onChange(event) {
    if (!locked.value) {
      input.handleChange(control.value.path, adapt(event.target));
    }
  }

  return {
    ...input,
    field,
    ids,
    errorText,
    warnings,
    inputAttrs,
    locked,
    changed,
    onChange,
  };
}
