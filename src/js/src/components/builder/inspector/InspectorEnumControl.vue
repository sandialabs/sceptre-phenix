<!--
  Select for `enum` and oneOf-of-`const` fields.

  Options carry their label as text: the vanilla renderer's <option label>
  with no text content has no accessible name in Chromium. An empty-string
  choice, which phenix reads as "use the default", is named "Default" and
  stands in for the vanilla renderer's separate unnamed empty option.

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
  import { useInspectorControl } from './control.js';

  const props = defineProps(rendererProps());
  const input = useJsonFormsControl(props);

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
    const named = values.map((choice) => ({
      ...choice,
      key: String(choice.value),
      label: choice.value === '' ? 'Default' : choice.label,
    }));

    return named.some((choice) => choice.value === '')
      ? named
      : [{ value: undefined, key: '', label: 'Not set' }, ...named];
  });

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

  // A value no option matches leaves the select with no selection; a change
  // event then (a synthetic one) must not clear that value.
  function onSelect(event) {
    if (event.target.selectedIndex >= 0) {
      onChange(event);
    }
  }
</script>
