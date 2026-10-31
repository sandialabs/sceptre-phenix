<!--
  Text, number and checkbox fields. A locked field is read-only, so Tab reaches
  it. A checkbox has no Bulma "input" class, which hides its check. Integers use
  a text input: Firefox's number input read " 3 " as no number.
-->
<template>
  <inspector-field
    v-if="control.visible"
    :control="field"
    :ids="ids"
    :error-text="unreadable || errorText"
    :warnings="warnings"
    :fallback="fallback"
    :note="note"
    :reset="resettable"
    :changed="changed"
    @reset="reset">
    <template v-if="kind === 'multi'">
      <textarea
        v-bind="attrs"
        v-fit-text
        class="text-area"
        rows="2"
        :value="text"
        @mousedown="onPress"
        @focus="selectDefault"
        @mouseup="keepSelected"
        @input="onInput"
        @change="commit" />
      <inspector-description-toggle
        v-if="descriptionToggle"
        :toggle="descriptionToggle" />
    </template>
    <input
      v-else-if="kind === 'boolean'"
      v-bind="attrs"
      type="checkbox"
      :checked="
        Boolean(control.data ?? fallback?.value ?? control.schema?.default)
      "
      @change="onChange" />
    <input
      v-else-if="kind === 'integer'"
      v-bind="{ ...attrs, ...spin }"
      type="text"
      class="input"
      :value="text"
      @mousedown="onPress"
      @focus="selectDefault"
      @mouseup="keepSelected"
      @keydown="step"
      @input="onInput"
      @change="commit" />
    <input
      v-else-if="kind === 'number'"
      v-bind="attrs"
      :type="storedText ? 'text' : 'number'"
      class="input"
      step="any"
      :min="numberSchema.minimum"
      :max="numberSchema.maximum"
      :value="text"
      @mousedown="onPress"
      @focus="selectDefault"
      @mouseup="keepSelected"
      @input="onInput"
      @change="commit" />
    <template v-else>
      <input
        v-bind="attrs"
        type="text"
        class="input"
        :value="text"
        @mousedown="onPress"
        @focus="selectDefault"
        @mouseup="keepSelected"
        @input="onInput"
        @change="commit" />
      <button
        v-if="generatesMac"
        type="button"
        class="builder-button control__generate"
        data-testid="inspector-generate-mac"
        aria-label="Generate a random MAC address"
        title="Generate a random MAC address"
        @click="generateMac">
        <builder-icon name="refresh" :size="14" />
      </button>
      <inspector-description-toggle
        v-if="descriptionToggle"
        :toggle="descriptionToggle" />
    </template>
  </inspector-field>
</template>

<script setup>
  import { computed, onBeforeUnmount, ref, watch } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import InspectorDescriptionToggle from './InspectorDescriptionToggle.vue';
  import InspectorField from './InspectorField.vue';
  import { vFitText } from './fitText.js';
  import {
    INTERFACE_MAC_PATH,
    isTextOrList,
    useDescriptionToggle,
    numberBranch,
    readNumberText,
    useFieldDefault,
    useFieldText,
    useInspectorControl,
    useInspectorAnnounce,
    useInspectorLocalProblems,
    useInspectorRandomMac,
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

  // One text value, or several as a list. A list stays a list.
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
    // A number field's text counts without the spaces around it.
    const text = numeric.value ? target.value.trim() : target.value;

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
        if (text === '') {
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
    note,
    resettable,
    resetDefault,
    onChange,
  } = useInspectorControl(input, adapt, {
    ownErrors: alternatives.value,
    fallback,
  });

  // A node's Description field has the toggle that hides it on the canvas.
  const descriptionToggle = useDescriptionToggle(control);

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

  // A whole-number field holding a number reads as a spin button: its
  // bounds and the number it shows. A template's field holds text.
  const spin = computed(() => {
    if (storedText.value) {
      return {};
    }

    const shown = readNumberText(text.value, true);

    return {
      role: 'spinbutton',
      inputmode: 'numeric',
      'aria-valuemin': numberSchema.value?.minimum,
      'aria-valuemax': numberSchema.value?.maximum,
      'aria-valuenow': Number.isInteger(shown) ? shown : undefined,
    };
  });

  // ArrowUp and ArrowDown step a whole number by one, within the field's
  // bounds, and an empty field from 0, as a number input's arrow keys do.
  // Like theirs, each step is typed and committed at once: the input and
  // change events tell the Inspector the field was edited. Text that is no
  // whole number, and a template, are left to the keys' usual work.
  function step(event) {
    const up = event.key === 'ArrowUp';

    if (
      (!up && event.key !== 'ArrowDown') ||
      event.altKey ||
      event.ctrlKey ||
      event.metaKey ||
      locked.value ||
      storedText.value
    ) {
      return;
    }

    const field = event.target;
    const typed = field.value.trim();
    const current = typed === '' ? 0 : readNumberText(typed, true);

    if (!Number.isInteger(current)) {
      return;
    }

    const { minimum = -Infinity, maximum = Infinity } =
      numberSchema.value || {};
    const next = Math.min(Math.max(current + (up ? 1 : -1), minimum), maximum);

    event.preventDefault();
    field.value = String(next);
    field.dispatchEvent(new Event('input', { bubbles: true }));
    field.dispatchEvent(new Event('change', { bubbles: true }));
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

  // Default, as a button: text a number field could not read goes too.
  function reset() {
    setUnreadable('');
    resetDefault();
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

  // The Generate button of an interface's MAC address field.
  const randomMac = useInspectorRandomMac();
  const announce = useInspectorAnnounce();
  const generatesMac = computed(
    () =>
      Boolean(randomMac) &&
      !locked.value &&
      control.value.enabled &&
      INTERFACE_MAC_PATH.test(control.value.path),
  );

  function generateMac() {
    const mac = randomMac();

    if (!mac) {
      announce('The Builder found no free MAC address. Select Generate again.');

      return;
    }

    setUnreadable('');
    input.handleChange(control.value.path, mac);
    text.value = mac;
    announce(`Generated MAC address ${mac}.`);
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
