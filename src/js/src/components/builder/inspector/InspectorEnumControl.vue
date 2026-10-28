<!--
  Select for `enum` and oneOf-of-`const` fields. Options carry their label as
  text: an <option label> with no text has no name in Chromium.
-->
<template>
  <inspector-field
    v-if="control.visible"
    :control="control"
    :ids="ids"
    :error-text="errorText"
    :warnings="warnings"
    :fallback="fallback"
    :note="note"
    :reset="resettable"
    :changed="changed"
    @reset="resetDefault">
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
    note,
    resettable,
    resetDefault,
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
