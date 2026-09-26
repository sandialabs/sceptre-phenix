<!--
  Keys and values for the Inspector: a node's advanced settings, labels and
  annotations, objects whose keys are free (see MAP_KEYWORD in schema.js).
  JSON Forms has no renderer for them, and drew an empty group.

  Each entry is a row of two fields, its name and its value, named after the
  entry ("Label 2 name"), and a Remove button; "Add label" follows the rows
  and may suggest names. Add and remove are announced, and focus goes to the
  new row's name or stays in the list rather than falling to the page. A
  row reaches the working copy when one of its fields commits (on change),
  as other fields do. One with no name, or the name of a row above it, does
  not until that is fixed, and says so under its name; the Inspector lists
  it with its errors and refuses Apply meanwhile (see
  useInspectorLocalProblems). A value is text, or,
  for annotations, read the way a topology's YAML reads it: true, false,
  null, numbers and JSON as such. A row whose entry the working copy added
  or changed is marked, as other fields are, until the change is applied or
  cancelled (see useEntryChanged).

  A locked map (see useInspectorLocked) has no buttons and read-only fields;
  in a read-only draft its fields and buttons are disabled.
-->
<template>
  <fieldset
    v-if="control.visible"
    ref="root"
    class="array-list inspector-map"
    :data-path="control.path"
    :aria-describedby="describedBy">
    <legend class="array-list-legend">
      <span
        ref="legendLabel"
        class="array-list-label"
        :class="{ 'label--described': Boolean(control.description) }"
        @mouseenter="tooltip?.show(legendLabel)"
        @mouseleave="tooltip?.scheduleHide()"
        >{{ label }}</span
      >
    </legend>
    <p v-if="control.description" :id="`${baseId}-description`" hidden>
      {{ control.description }}
    </p>

    <div
      v-for="(row, index) in rows"
      :key="row.id"
      class="inspector-map__row"
      :class="{ 'control--changed': changedRows[index] }"
      :data-path="`${control.path}.${index}`"
      data-testid="inspector-map-row">
      <div
        class="control inspector-map__field"
        :class="{ 'control--invalid': Boolean(problems[index]) }">
        <label :for="`${baseId}-${row.id}-key`" class="label"
          ><span class="builder-visually-hidden">{{
            `${rowName(index)} `
          }}</span
          >Name</label
        >
        <input
          :id="`${baseId}-${row.id}-key`"
          type="text"
          class="input"
          autocomplete="off"
          :list="suggested.length && !locked ? `${baseId}-keys` : undefined"
          :value="row.key"
          v-bind="fieldAttrs"
          :aria-invalid="problems[index] ? 'true' : undefined"
          :aria-describedby="
            [
              problems[index] && `${baseId}-${row.id}-problem`,
              changedRows[index] && `${baseId}-changed`,
            ]
              .filter(Boolean)
              .join(' ') || undefined
          "
          @input="row.key = $event.target.value"
          @change="commit" />
        <p
          v-if="problems[index]"
          :id="`${baseId}-${row.id}-problem`"
          class="error">
          <builder-icon name="close" :size="12" />
          {{ problems[index] }}
        </p>
      </div>
      <div class="control inspector-map__field">
        <label :for="`${baseId}-${row.id}-value`" class="label"
          ><span class="builder-visually-hidden">{{
            `${rowName(index)} `
          }}</span
          >Value</label
        >
        <input
          :id="`${baseId}-${row.id}-value`"
          type="text"
          class="input"
          autocomplete="off"
          :value="row.value"
          v-bind="fieldAttrs"
          :aria-describedby="
            changedRows[index] ? `${baseId}-changed` : undefined
          "
          @input="row.value = $event.target.value"
          @change="commit" />
      </div>
      <button
        v-if="!locked"
        type="button"
        class="inspector-map__remove"
        :aria-label="`Remove ${itemName(index)}`"
        :title="`Remove ${itemName(index)}`"
        :disabled="!control.enabled"
        @click="remove(index)">
        <builder-icon name="trash" :size="14" />
      </button>
    </div>

    <p v-if="rows.length === 0" class="array-list-no-data">No {{ plural }}.</p>
    <p v-if="changedRows.some(Boolean)" :id="`${baseId}-changed`" hidden>
      Changed, not applied yet.
    </p>
    <p v-if="errorText" :id="`${baseId}-error`" class="error">
      <builder-icon name="close" :size="12" />
      {{ errorText }}
    </p>
    <p
      v-if="warnings.length"
      :id="`${baseId}-warning`"
      class="warning"
      data-testid="inspector-field-warning">
      <builder-icon name="warning" :size="12" />
      <span><strong>Warning:</strong> {{ warnings.join(' ') }}</span>
    </p>

    <datalist v-if="suggested.length && !locked" :id="`${baseId}-keys`">
      <option v-for="key in suggested" :key="key" :value="key" />
    </datalist>

    <button
      v-if="!locked"
      ref="addButton"
      type="button"
      class="array-list-add builder-button"
      :disabled="!control.enabled"
      @click="add"
      @focus="tooltip?.onFocusIn($event, legendLabel)"
      @blur="tooltip?.hide()">
      <builder-icon name="plus" :size="14" />
      Add {{ noun }}
    </button>

    <inspector-tooltip
      v-if="control.description"
      ref="tooltip"
      :text="control.description" />
  </fieldset>
</template>

<script>
  let rowIds = 0;

  function isRecord(value) {
    return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
  }
</script>

<script setup>
  import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import { itemNoun } from '@/builder/form-validator.js';
  import { MAP_KEYS_KEYWORD, MAP_KEYWORD } from '@/builder/schema.js';
  import {
    readValue,
    useEntryChanged,
    useFieldWarnings,
    useInspectorAnnounce,
    useInspectorLocalProblems,
    useInspectorLocked,
    useInspectorResets,
    valueText,
  } from './control.js';
  import InspectorTooltip from './InspectorTooltip.vue';

  const props = defineProps(rendererProps());
  const { control, handleChange } = useJsonFormsControl(props);
  const announce = useInspectorAnnounce();
  const reportProblems = useInspectorLocalProblems();
  const locked = useInspectorLocked(control);
  const resets = useInspectorResets();
  const warnings = useFieldWarnings(control);
  const entryChanged = useEntryChanged(control);

  const root = ref();
  const addButton = ref();
  const legendLabel = ref();
  const tooltip = ref();

  const label = computed(() => control.value.label || 'Entries');
  const noun = computed(() => itemNoun(label.value));
  const plural = computed(() =>
    /^[A-Z][a-z]/.test(label.value)
      ? label.value[0].toLowerCase() + label.value.slice(1)
      : label.value,
  );
  const baseId = computed(
    () => control.value.id || `field-${control.value.path}`,
  );
  const typed = computed(() => control.value.schema?.[MAP_KEYWORD] === 'value');
  const suggested = computed(
    () => control.value.schema?.[MAP_KEYS_KEYWORD] || [],
  );

  const errorText = computed(() =>
    [...new Set(String(control.value.errors || '').split('\n'))]
      .filter(Boolean)
      .join(' '),
  );
  const describedBy = computed(
    () =>
      [
        errorText.value && `${baseId.value}-error`,
        warnings.value.length && `${baseId.value}-warning`,
        control.value.description && `${baseId.value}-description`,
      ]
        .filter(Boolean)
        .join(' ') || undefined,
  );

  const fieldAttrs = computed(() => ({
    disabled: !control.value.enabled && !locked.value,
    readonly: locked.value || undefined,
  }));

  function itemName(index) {
    return `${noun.value} ${index + 1}`;
  }

  function rowName(index) {
    const name = itemName(index);

    return `${name[0].toUpperCase()}${name.slice(1)}`;
  }

  // The rows of a map. A row shown already for an entry keeps its id, so
  // its fields, and focus in them, stay.
  function toRows(data, shown = []) {
    const unused = [...shown];

    return Object.entries(isRecord(data) ? data : {}).map(([key, value]) => {
      const text = typed.value ? valueText(value) : String(value ?? '');
      const index = unused.findIndex(
        (row) => row.key.trim() === key && row.value === text,
      );
      const id = index === -1 ? (rowIds += 1) : unused.splice(index, 1)[0].id;

      return { id, key, value: text };
    });
  }

  const rows = ref(toRows(control.value.data));

  // Why each row is not in the working copy, or ''.
  const problems = computed(() =>
    rows.value.map((row, index) => {
      const key = row.key.trim();

      if (!key) {
        return row.value.trim() ? 'Name is required' : '';
      }

      const first = rows.value.findIndex((other) => other.key.trim() === key);

      return first < index ? `Name is used by ${itemName(first)} already` : '';
    }),
  );

  // The rows whose entries the working copy added or changed. A row with a
  // problem is not in it.
  const changedRows = computed(() =>
    rows.value.map((row, index) => {
      const key = row.key.trim();

      return Boolean(key) && !problems.value[index] && entryChanged(key);
    }),
  );

  // Rows with a problem are not in the working copy, so the Inspector hears
  // of them: it lists them, each linked to its row, and refuses Apply.
  watch(
    [problems, () => control.value.path],
    ([found, path], previous) => {
      if (previous && previous[1] !== path) {
        reportProblems(previous[1], []);
      }

      reportProblems(
        path,
        found
          .map((message, index) =>
            message
              ? {
                  path: `${path}.${index}`,
                  message: `${rowName(index)}: ${message}`,
                }
              : null,
          )
          .filter(Boolean),
      );
    },
    { immediate: true },
  );

  onBeforeUnmount(() => reportProblems(control.value.path, []));

  // The map the rows make: each row with a name no row above it has.
  function rowsData() {
    return Object.fromEntries(
      rows.value
        .filter((row, index) => row.key.trim() && !problems.value[index])
        .map((row) => [
          row.key.trim(),
          typed.value ? readValue(row.value) : row.value,
        ]),
    );
  }

  function sameAsRows(data) {
    return (
      JSON.stringify(isRecord(data) ? data : {}) === JSON.stringify(rowsData())
    );
  }

  // Data replaced from outside the map (Cancel, undo, Apply) shows as it
  // is; the rows' own commits leave the rows, and any row not committed,
  // as they are.
  watch(resets, () => {
    rows.value = toRows(control.value.data, rows.value);
  });
  watch(
    () => control.value.data,
    (data) => {
      if (!sameAsRows(data)) {
        rows.value = toRows(data, rows.value);
      }
    },
  );

  function commit() {
    if (locked.value || sameAsRows(control.value.data)) {
      return;
    }

    const next = rowsData();

    handleChange(
      control.value.path,
      Object.keys(next).length ? next : undefined,
    );
  }

  function rowElements() {
    return root.value?.querySelectorAll('.inspector-map__row') || [];
  }

  async function add() {
    rows.value.push({ id: (rowIds += 1), key: '', value: '' });
    announce(`Added ${itemName(rows.value.length - 1)}.`);
    await nextTick();

    const elements = rowElements();

    elements[elements.length - 1]?.querySelector('input')?.focus();
  }

  async function remove(index) {
    const name = itemName(index);

    rows.value.splice(index, 1);
    commit();
    announce(`Removed ${name}.`);
    await nextTick();

    const elements = rowElements();
    const next = elements[Math.min(index, elements.length - 1)];

    (next?.querySelector('.inspector-map__remove') || addButton.value)?.focus();
  }
</script>

<style scoped>
  .inspector-map__row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
    align-items: start;
    gap: 0.35rem;
  }

  .inspector-map__field {
    min-width: 0;
  }

  /* Level with the fields, below their labels. */
  .inspector-map__remove {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 24px;
    min-height: 24px;
    margin-top: 1.25rem;
    padding: 0.15rem;
  }
</style>
