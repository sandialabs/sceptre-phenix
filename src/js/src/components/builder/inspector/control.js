// State shared by the Inspector's JSON Forms renderers. It wires each field's
// help, error and warnings to its input with ARIA, which the vanilla renderers
// do not do.

import { computed, inject, ref, toValue, unref, watch } from 'vue';
import { getControlPath } from '@jsonforms/core';
import { useJsonForms } from '@jsonforms/vue';

import { drawnColor } from '@/builder/colors.js';
import { itemNoun } from '@/builder/form-validator.js';
import { lowerFirst } from '@/builder/text.js';

// Provided by BuilderInspector: announces a message in the Builder's live
// region. Renderers rendered anywhere else do nothing.
export const INSPECTOR_ANNOUNCE = Symbol('inspector-announce');

export function useInspectorAnnounce() {
  return inject(INSPECTOR_ANNOUNCE, () => {});
}

// Provided by BuilderInspector: the level of the heading of the node's
// fields (see InspectorGroupRenderer), one below the heading that names the
// Inspector's section. Renderers rendered anywhere else use 3.
export const INSPECTOR_HEADING_LEVEL = Symbol('inspector-heading-level');

export function useInspectorHeadingLevel() {
  return inject(INSPECTOR_HEADING_LEVEL, 3);
}

// Provided by BuilderInspector: a count that increases each time the form
// reloads its data from the document (Cancel, undo, Apply), which may leave a
// field's data as it was. Renderers rendered anywhere else get a count that
// never changes.
export const INSPECTOR_RESETS = Symbol('inspector-resets');

export function useInspectorResets() {
  return inject(INSPECTOR_RESETS, () => ref(0), true);
}

// Provided by BuilderInspector: problems that a renderer keeps to itself, as
// {path, message} under its data path. The Inspector refuses Apply while any
// exist, so an edit does not vanish silently.
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
 * The warnings for one field, each message once. The array stays the same while
 * its messages do, so fields do not re-render for nothing.
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

// Provided by BuilderInspector: a note about a field, given its data path,
// or '' for none. It says something that applies outside the diagram, such
// as a setting of the viewer that overrides the field's value on the canvas.
// It is no warning. Renderers rendered anywhere else get none.
export const INSPECTOR_FIELD_NOTES = Symbol('inspector-field-notes');

/** The note about one field (see INSPECTOR_FIELD_NOTES). */
function useFieldNote(control) {
  const notes = inject(INSPECTOR_FIELD_NOTES, () => ref(() => ''), true);

  return computed(() => unref(notes)?.(toValue(control)?.path) || '');
}

// Provided by BuilderInspector: what a field shows while it is not set,
// given its data path and schema, as {value, note} or undefined (see
// fieldDefault in adapters/forms.js). Renderers rendered anywhere else show
// none.
export const INSPECTOR_DEFAULTS = Symbol('inspector-defaults');

/**
 * What a field with no value shows, but does not store: the value it comes to
 * all the same. Undefined while it has a value or no default.
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
 * What a field comes to with no value, whether or not it has one now (see
 * useFieldDefault).
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
 * Whether the working copy changed an entry of a map field, given the entry's
 * key, which may hold dots.
 */
export function useEntryChanged(control) {
  const changed = inject(INSPECTOR_CHANGED, () => ref(() => false), true);

  return (key) => Boolean(unref(changed)?.(control.value.path, key));
}

// Provided by BuilderInspector: how the canvas draws a color field's color,
// given the color and the field's data path, for the field's chip and
// swatches: a network's (see drawnNetworkColor), or any other (see
// drawnColor). Renderers rendered anywhere else draw it as chosen.
export const INSPECTOR_DRAWN_COLOR = Symbol('inspector-drawn-color');

export function useInspectorDrawnColor() {
  return inject(INSPECTOR_DRAWN_COLOR, () => ref(drawnColor), true);
}

// Provided by BuilderInspector: the custom icons of the form's icon field, as
// entry(name) and diagram(). A form for something other than the open diagram
// can supply its own.
export const INSPECTOR_ICONS = Symbol('inspector-icons');

const NO_ICONS = Object.freeze({
  entry: () => undefined,
  diagram: () => [],
});

export function useInspectorIcons() {
  return inject(INSPECTOR_ICONS, NO_ICONS);
}

// Provided by BuilderInspector: the server's icon library, for the Custom
// icons dialog (see createIconLibrary in iconLibrary.js): its reactive
// state, load(), lookup(name), upload({name, data}), rename(name, newName)
// and failure(error), which says why one of them failed. A dialog opened
// anywhere else has no library to reach.
export const INSPECTOR_ICON_LIBRARY = Symbol('inspector-icon-library');

const NO_LIBRARY_TEXT = 'The icon library is not available here.';
const noLibrary = () => Promise.reject(new Error(NO_LIBRARY_TEXT));
const NO_LIBRARY = Object.freeze({
  state: Object.freeze({
    status: 'failed',
    error: NO_LIBRARY_TEXT,
    icons: [],
    index: new Map(),
    maxIcons: 0,
  }),
  load: noLibrary,
  ensure: noLibrary,
  lookup: () => null,
  upload: noLibrary,
  rename: noLibrary,
  failure: () => NO_LIBRARY_TEXT,
});

export function useInspectorIconLibrary() {
  return inject(INSPECTOR_ICON_LIBRARY, NO_LIBRARY);
}

// Provided by BuilderInspector: the layer control of the device or switch the
// form shows, as {state, choose, useMatched, editLayers}. Elsewhere there is no
// layer control.
export const INSPECTOR_LAYER = Symbol('inspector-layer');

export function useInspectorLayer() {
  return inject(INSPECTOR_LAYER, null);
}

// Provided by BuilderInspector: the toggle beside a node's Description field,
// as {path, shown, locked, set}, or null. Elsewhere there is no toggle.
export const INSPECTOR_DESCRIPTION = Symbol('inspector-description');

/** The description toggle of the field at the control's path, or null. */
export function useDescriptionToggle(control) {
  const toggle = inject(INSPECTOR_DESCRIPTION, null);

  return computed(() => {
    const value = unref(toggle);

    return value && value.path === toValue(control)?.path ? value : null;
  });
}

/**
 * The text a field shows, which its input binds in place of its data. It keeps
 * typed text until the data changes or the form reloads, so a re-render does
 * not overwrite it.
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
 * The number alternative of a field phenix takes as a number or as template
 * text (Memory, VCPUs), or undefined for any other field.
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
 * The number that typed text stands for: undefined for no number, NaN for a
 * fraction in a whole-number field (parseInt read 1.5 as 1).
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
 * Whether a field holds one text value or a list (DNS servers), edited as one
 * comma-separated line. JSON Forms has no renderer for such an anyOf.
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

// Provided by BuilderInspector: a random MAC address that no interface of
// the diagram or of the form's data uses (see randomMac in addressing.js),
// or '' when it finds none. An interface's MAC address field then has a
// Generate button (see InspectorInputControl). Renderers rendered anywhere
// else have none.
export const INSPECTOR_RANDOM_MAC = Symbol('inspector-random-mac');

export function useInspectorRandomMac() {
  return inject(INSPECTOR_RANDOM_MAC, null);
}

// The data path of an interface's MAC address field.
export const INTERFACE_MAC_PATH = /(^|\.)network\.interfaces\.\d+\.mac$/;

// Provided by BuilderInspector: the item a list's Add button appends, given
// the list's data path and the form's data, for a list whose new items need
// more than their schema's defaults (a new interface is named the way a
// connection drawn on the canvas names one). Undefined leaves the schema's
// default item, as it is for renderers rendered anywhere else.
export const INSPECTOR_NEW_ITEM = Symbol('inspector-new-item');

export function useInspectorNewItem() {
  return inject(INSPECTOR_NEW_ITEM, () => () => undefined, true);
}

// Provided by BuilderInspector: the item that a list's "Insert … after" button
// puts after an item. Undefined leaves the list without the button.
export const INSPECTOR_INSERT_ITEM = Symbol('inspector-insert-item');

export function useInspectorInsertItem() {
  return inject(INSPECTOR_INSERT_ITEM, () => () => undefined, true);
}

// Provided by BuilderInspector: values a free-text field suggests as the
// user types, by kind: disks, the server's disk images, or null while they
// are unknown, rulesets, the names of the device's firewall rulesets, and
// interfaces, the names of its interfaces. Renderers rendered anywhere else
// suggest nothing.
export const INSPECTOR_SUGGESTIONS = Symbol('inspector-suggestions');

export function useInspectorSuggestions() {
  return inject(INSPECTOR_SUGGESTIONS, () => ref({}), true);
}

/**
 * The values that contain the typed text, ignoring case, those that start with
 * it first, each group in alphabetical order.
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
// included topology, or a read-only draft. JSON Forms is then read only, but
// its fields are locked rather than disabled (see useInspectorLocked).
// Renderers rendered anywhere else get false.
export const INSPECTOR_LOCKED = Symbol('inspector-locked');

/**
 * Whether a control that cannot change is locked: shown read-only rather than
 * disabled, so Tab still reaches it and it shows at full contrast.
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
 * The value of typed annotation text, read as a topology's YAML reads it: JSON
 * values as such, and anything else as text.
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
 * The text of an annotation's value. Text that readValue would read as another
 * value is quoted.
 */
export function valueText(value) {
  if (typeof value === 'string') {
    return readValue(value) === value ? value : JSON.stringify(value);
  }

  return JSON.stringify(value ?? null);
}

/**
 * Labels the whole-value control (scope "#") that JSON Forms generates for list
 * items and oneOf branches and leaves unlabelled.
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
 * The errors of a field edited as one value whose schema is a oneOf (Memory,
 * DNS servers). JSON Forms gives such a field none, so they come from its
 * alternatives.
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
 * Wraps a JSON Forms control binding with ids and ARIA attributes for its
 * input. `adapt` reads the new value from the element that fired `change`.
 */
export function useInspectorControl(
  input,
  adapt = (target) => target.value,
  { ownErrors = false, fallback, moreWarnings } = {},
) {
  const { control } = input;

  const ids = computed(() => {
    // JSON Forms assigns ids when the control mounts. Server rendering has
    // none, so this uses the data path.
    const base = control.value.id || `field-${control.value.path || 'root'}`;

    return {
      input: `${base}-input`,
      description: `${base}-description`,
      error: `${base}-error`,
      warning: `${base}-warning`,
      fallback: `${base}-default`,
      changed: `${base}-changed`,
      note: `${base}-note`,
      reset: `${base}-reset`,
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
  const fieldWarnings = useFieldWarnings(control);
  const warnings = moreWarnings
    ? computed(() => [...fieldWarnings.value, ...moreWarnings.value])
    : fieldWarnings;

  const locked = useInspectorLocked(control);

  const changed = useFieldChanged(control);

  // InspectorField shows the note under the input, as `note`.
  const note = useFieldNote(control);
  const reset = fallback
    ? useDefaultReset(input, { fallback, locked })
    : { resettable: computed(() => undefined), resetDefault: () => {} };

  const inputAttrs = computed(() => {
    const describedBy = [
      errorText.value && ids.value.error,
      warnings.value.length && ids.value.warning,
      note.value && ids.value.note,
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
    note,
    resettable: reset.resettable,
    resetDefault: reset.resetDefault,
    onChange,
  };
}

// The text of a default value in the name of the button that resets to it.
function defaultText(value) {
  if (typeof value === 'boolean') {
    return value ? 'on' : 'off';
  }

  return Array.isArray(value) ? value.join(', ') : String(value);
}

/**
 * The "Default" reset button of a field that has another value: it clears the
 * field, or sets the default when the field cannot be empty.
 */
export function useDefaultReset(input, { fallback, locked }) {
  const unset = useUnsetValue(input.control);

  const resettable = computed(() => {
    const { data, enabled, label } = input.control.value;
    const shown = unset.value;

    if (
      !shown ||
      fallback.value ||
      locked.value ||
      enabled === false ||
      JSON.stringify(data) === JSON.stringify(shown.value)
    ) {
      return undefined;
    }

    return {
      name: `Reset ${label || 'Value'} to the default, ${defaultText(shown.value)}`,
      description: `${shown.note}.`,
    };
  });

  function resetDefault() {
    if (!resettable.value) {
      return;
    }

    const { path, required } = input.control.value;

    input.handleChange(path, required ? unset.value.value : undefined);
  }

  return { resettable, resetDefault };
}

/**
 * The names, ids and messages of a list or a map, whose group they describe as
 * a field's describe its input.
 */
export function useListControl(control, fallbackLabel) {
  const label = computed(() => control.value.label || fallbackLabel);
  const noun = computed(() => itemNoun(label.value));
  const plural = computed(() => lowerFirst(label.value));
  const baseId = computed(
    () => control.value.id || `field-${control.value.path}`,
  );
  const ids = computed(() => ({
    error: `${baseId.value}-error`,
    warning: `${baseId.value}-warning`,
    description: `${baseId.value}-description`,
  }));

  // One message, as a field shows.
  const errorText = computed(
    () =>
      String(control.value.errors || '')
        .split('\n')
        .filter(Boolean)[0] || '',
  );
  const warnings = useFieldWarnings(control);

  const describedBy = computed(
    () =>
      [
        errorText.value && ids.value.error,
        warnings.value.length && ids.value.warning,
        control.value.description && ids.value.description,
      ]
        .filter(Boolean)
        .join(' ') || undefined,
  );

  function itemName(index) {
    return `${noun.value} ${index + 1}`;
  }

  return {
    label,
    noun,
    plural,
    baseId,
    ids,
    errorText,
    warnings,
    describedBy,
    itemName,
  };
}
