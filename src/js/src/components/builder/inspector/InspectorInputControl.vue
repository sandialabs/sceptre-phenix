<!--
  Text, multi-line text, number, integer and checkbox fields for the
  Inspector, in place of the vanilla JSON Forms controls. See control.js for
  the ARIA wiring.

  A locked field (see useInspectorLocked: the node hostname, which Apply
  copies from the device Hostname, and every field of a device from an
  included topology) is a read-only input rather than a disabled one, so Tab
  still reaches it and its value and description are read. A locked checkbox,
  which has no read-only state, is aria-readonly and ignores clicks. In a
  read-only draft every field is disabled.

  A checkbox has no Bulma "input" class: that class draws a box without the
  checkbox's own look, so a checked box looked unchecked. An unset field
  shows its default (see useFieldDefault), the value phenix uses (Snapshot is
  on unless turned off, a VM has 512 megabytes of memory), marked "Default";
  a click on a checkbox stores the opposite of what it shows, and a text or
  number field emptied shows its default again. A text or number field
  showing its default has its text selected as it takes focus, so what is
  typed replaces the default rather than adding to it.

  A field phenix takes as a number or as text (Memory, VCPUs; see
  numberBranch) is a number field. Text it holds already is kept: a number
  written as text ("2048") shows as the number, and any other text (a
  template such as {{ .Memory }}) in a text field, until a number replaces
  it. A field of one text value or a list (DNS servers; see isTextOrList)
  is one line of values separated by commas: one value is stored as text,
  several as a list, and a list stays a list.

  Text a number field cannot read (letters, which Firefox lets through) is
  not committed: the field says it must be a number, and Apply waits for
  it, rather than the value being cleared. So is a number a whole-number
  field cannot take (1.5, 1e3), which was cut to 1: the field says it must
  be a whole number.

  A text or number field shows the text typed in it until it commits (see
  useFieldText), so nothing that re-renders it puts its data back over that
  text.
-->
<template>
  <inspector-field
    v-if="control.visible"
    :control="field"
    :ids="ids"
    :error-text="unreadable || errorText"
    :warnings="warnings"
    :fallback="fallback"
    :changed="changed">
    <textarea
      v-if="kind === 'multi'"
      v-bind="attrs"
      class="text-area"
      :value="text"
      @mousedown="onPress"
      @focus="selectDefault"
      @mouseup="keepSelected"
      @input="onInput"
      @change="commit" />
    <input
      v-else-if="kind === 'boolean'"
      v-bind="attrs"
      type="checkbox"
      :checked="
        Boolean(control.data ?? fallback?.value ?? control.schema?.default)
      "
      @change="onChange" />
    <input
      v-else-if="kind === 'integer' || kind === 'number'"
      v-bind="attrs"
      :type="storedText ? 'text' : 'number'"
      class="input"
      :step="kind === 'integer' ? 1 : 'any'"
      :min="numberSchema.minimum"
      :max="numberSchema.maximum"
      :value="text"
      @mousedown="onPress"
      @focus="selectDefault"
      @mouseup="keepSelected"
      @input="onInput"
      @change="commit" />
    <input
      v-else
      v-bind="attrs"
      type="text"
      class="input"
      :value="text"
      @mousedown="onPress"
      @focus="selectDefault"
      @mouseup="keepSelected"
      @input="onInput"
      @change="commit" />
  </inspector-field>
</template>

<script setup>
  import { computed, onBeforeUnmount, ref, watch } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import InspectorField from './InspectorField.vue';
  import {
    isTextOrList,
    numberBranch,
    readNumberText,
    useFieldDefault,
    useFieldText,
    useInspectorControl,
    useInspectorLocalProblems,
    useInspectorResets,
  } from './control.js';
  import { fieldLabel } from '@/builder/form-validator.js';

  const props = defineProps(rendererProps());
  const input = useJsonFormsControl(props);

  // The schema the field's number follows: its own, or the number
  // alternative of a number or text.
  const numberSchema = computed(
    () =>
      numberBranch(input.control.value.schema) ?? input.control.value.schema,
  );

  const kind = computed(() => {
    const schema = input.control.value.schema;

    if (isTextOrList(schema)) {
      return 'list';
    }

    const types = [numberSchema.value?.type].flat();

    if (types.includes('boolean')) {
      return 'boolean';
    }

    if (types.includes('integer') || types.includes('number')) {
      return types.includes('integer') ? 'integer' : 'number';
    }

    return input.control.value.uischema?.options?.multi ? 'multi' : 'text';
  });

  const numeric = computed(() => ['integer', 'number'].includes(kind.value));
  // A number or text alternative takes its errors from its alternatives.
  const alternatives = computed(
    () =>
      kind.value === 'list' ||
      numberSchema.value !== input.control.value.schema,
  );

  // Text a number or text field holds that is no number: a template, shown
  // as it is in a text field.
  const storedText = computed(
    () =>
      numeric.value &&
      typeof input.control.value.data === 'string' &&
      readNumberText(input.control.value.data, false) === undefined,
  );

  // One text value, or several as a list; a list stays a list.
  function readList(text) {
    const values = String(text)
      .split(/[\s,]+/)
      .filter(Boolean);

    if (values.length === 0) {
      return undefined;
    }

    return values.length === 1 && !Array.isArray(input.control.value.data)
      ? values[0]
      : values;
  }

  // An emptied field removes its value rather than storing "" or NaN, as the
  // vanilla controls do. The default an unset field shows is no value
  // either: a change event from the field as it shows it (Enter in it after
  // it was emptied, or leaving it) leaves it unset.
  function adapt(target) {
    const text = target.value;

    if (
      fallback.value &&
      kind.value !== 'boolean' &&
      text === String(format(fallback.value.value))
    ) {
      return undefined;
    }

    switch (kind.value) {
      case 'boolean':
        return target.checked;
      case 'list':
        return readList(text);
      case 'integer':
      case 'number': {
        if (text.trim() === '') {
          return undefined;
        }

        // Other text stays text only in the field of a template.
        const number = readNumberText(text, kind.value === 'integer');

        return number === undefined ? text : number;
      }
      default:
        return text || undefined;
    }
  }

  const fallback = useFieldDefault(input.control);

  // The text for the field's data: a list's values separated by commas.
  function format(data) {
    return kind.value === 'list' && Array.isArray(data)
      ? data.join(', ')
      : data;
  }

  const {
    control,
    field,
    ids,
    errorText,
    warnings,
    inputAttrs,
    locked,
    changed,
    onChange,
  } = useInspectorControl(input, adapt, {
    ownErrors: alternatives.value,
    fallback,
  });

  const { text, onInput, sync } = useFieldText(control, { format, fallback });

  const report = useInspectorLocalProblems();
  const resets = useInspectorResets();
  // The message for text a number field cannot read, while it holds some.
  // The error summary leads with the list item the field is in, if any.
  const unreadable = ref('');

  function setUnreadable(message) {
    if (message !== unreadable.value) {
      const { path, rootSchema } = control.value;
      const { context } = fieldLabel(rootSchema, path);

      unreadable.value = message;
      report(
        path,
        message
          ? [{ path, message: context ? `${context}: ${message}` : message }]
          : [],
      );
    }
  }

  // A reload of the form's data (Cancel, undo) takes such text away.
  watch(resets, () => setUnreadable(''));
  onBeforeUnmount(() => setUnreadable(''));

  // Why text typed in a number field is not committed, or '': it is no
  // number, other than in the text field of a template, or it is a number a
  // whole-number field cannot take, in a template's field too.
  function unreadableText(target) {
    if (!numeric.value) {
      return '';
    }

    const whole = kind.value === 'integer';
    const number = readNumberText(target.value, whole);
    const noNumber =
      target.validity?.badInput ||
      (target.value.trim() !== '' && number === undefined);

    return Number.isNaN(number) || (noNumber && !storedText.value)
      ? `${control.value.label || 'Value'} must be ${whole ? 'a whole number' : 'a number'}`
      : '';
  }

  // Committed, the field shows its value as the form reads it, also when
  // the data did not change: "07" typed over 7 shows 7 again, and an emptied
  // field its default.
  function commit(event) {
    const message = unreadableText(event.target);

    setUnreadable(message);

    if (message) {
      return;
    }

    onChange(event);
    sync();
  }

  // A field showing its default selects it as it takes focus, and again as
  // a click that gave it focus ends, which would put the caret in it.
  let clicked = false;

  function selectsDefault() {
    return Boolean(fallback.value) && !locked.value;
  }

  function onPress(event) {
    clicked = document.activeElement !== event.target;
  }

  function selectDefault(event) {
    if (selectsDefault()) {
      event.target.select();
    }
  }

  function keepSelected(event) {
    if (clicked && selectsDefault()) {
      event.preventDefault();
      event.target.select();
    }

    clicked = false;
  }

  const attrs = computed(() => {
    const attributes = unreadable.value
      ? {
          ...inputAttrs.value,
          'aria-invalid': 'true',
          'aria-describedby': [
            ...new Set([
              ids.value.error,
              ...String(inputAttrs.value['aria-describedby'] || '').split(' '),
            ]),
          ]
            .filter(Boolean)
            .join(' '),
        }
      : inputAttrs.value;

    return locked.value && kind.value === 'boolean'
      ? {
          ...attributes,
          readonly: undefined,
          'aria-readonly': 'true',
          onClick: (event) => event.preventDefault(),
        }
      : attributes;
  });
</script>
