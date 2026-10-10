<!--
  Select for `enum` and oneOf-of-`const` fields.

  Options carry their label as text: the vanilla renderer's <option label>
  with no text content has no accessible name in Chromium. An empty-string
  choice, which phenix reads as "use the default", is named "Default" and
  stands in for the vanilla renderer's separate unnamed empty option. It
  names the value the field comes to, "Default (kvm)", where there is one
  (see useFieldDefault), and while it is chosen the field says "Default",
  as a text or number field showing its default does. A field whose schema
  names that choice (UNSET_KEYWORD in schema.js) uses its word in place of
  "Default" and "Not set": a line style left to the canvas is "Auto
  (Dashed)".

  A value no choice matches, as an uploaded topology can hold, is a choice
  of its own, named as not one of the choices, and a warning says so: the
  select would otherwise show nothing, and read as having no value.

  A locked field (see useInspectorLocked), which a select cannot show, is a
  read-only text input holding the chosen option's name.
-->
<template>
  <inspector-field
    v-if="control.visible"
    :control="control"
    :ids="ids"
    :error-text="errorText"
    :warnings="warnings"
    :fallback="fallback"
    :changed="changed">
    <input
      v-if="locked"
      v-bind="inputAttrs"
      type="text"
      class="input"
      :value="selectedLabel" />
    <select
      v-else
      v-bind="inputAttrs"
      class="select"
      :value="selected"
      @change="onSelect">
      <option
        v-for="choice in choices"
        :key="choice.key"
        :value="choice.key"
        class="option">
        {{ choice.label }}
      </option>
    </select>
  </inspector-field>
</template>

<script setup>
  import { computed } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import InspectorField from './InspectorField.vue';
  import {
    useFieldDefault,
    useInspectorControl,
    useUnsetValue,
  } from './control.js';
  import { UNSET_KEYWORD } from '@/builder/schema.js';

  const props = defineProps(rendererProps());
  const input = useJsonFormsControl(props);
  const fallback = useFieldDefault(input.control);
  const unset = useUnsetValue(input.control);

  const choices = computed(() => {
    const schema = input.control.value.schema || {};
    const values = Array.isArray(schema.oneOf)
      ? schema.oneOf.map((branch) => ({
          value: branch.const,
          label: branch.title ?? String(branch.const),
        }))
      : (schema.enum ?? (schema.const === undefined ? [] : [schema.const])).map(
          (value) => ({ value, label: String(value) }),
        );
    // The choice that stands for no value, named after the value the
    // field then comes to, as its choice names it, in the schema's word
    // for it when it has one.
    const word = schema[UNSET_KEYWORD];
    const fallbackValue = unset.value?.value;
    const empty =
      fallbackValue === undefined
        ? word
        : `${word ?? 'Default'} (${
            values.find((choice) => choice.value === fallbackValue)?.label ??
            String(fallbackValue)
          })`;
    const named = values.map((choice) => ({
      ...choice,
      key: String(choice.value),
      label: choice.value === '' ? (empty ?? 'Default') : choice.label,
    }));
    const all = named.some((choice) => choice.value === '')
      ? named
      : [{ value: undefined, key: '', label: empty ?? 'Not set' }, ...named];
    const data = input.control.value.data;

    return data === undefined ||
      data === null ||
      all.some((choice) => choice.key === String(data))
      ? all
      : [
          ...all,
          {
            value: data,
            key: String(data),
            label: `${String(data)} (not one of the choices)`,
            outside: true,
          },
        ];
  });

  const outside = computed(() =>
    choices.value.find((choice) => choice.outside),
  );

  const {
    control,
    ids,
    errorText,
    warnings,
    inputAttrs,
    locked,
    changed,
    onChange,
  } = useInspectorControl(
    input,
    (target) => choices.value[target.selectedIndex]?.value,
    {
      fallback,
      moreWarnings: computed(() =>
        outside.value
          ? [`"${outside.value.key}" is not one of the choices.`]
          : [],
      ),
    },
  );

  const selected = computed(() =>
    control.value.data === undefined || control.value.data === null
      ? ''
      : String(control.value.data),
  );

  const selectedLabel = computed(
    () =>
      choices.value.find((choice) => choice.key === selected.value)?.label ??
      selected.value,
  );

  // A synthetic change event on a select with no selection must not clear
  // the value. No value should leave the select with no selection now.
  function onSelect(event) {
    if (event.target.selectedIndex >= 0) {
      onChange(event);
    }
  }
</script>
