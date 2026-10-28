<!--
  Key and value rows for MAP_KEYWORD objects (see schema.js), which JSON Forms
  cannot render. A row with no name or a duplicate name stays out of the working
  copy.
-->
<template>
  <fieldset
    v-if="control.visible"
    ref="root"
    class="array-list inspector-map"
    :class="{ 'array-list--collapsed': collapsed }"
    :data-path="control.path"
    :data-outline="control.path"
    :data-outline-label="label"
    :data-outline-count="rows.length"
    :data-outline-errors="errors ? 'true' : undefined"
    :data-collapse-path="control.path"
    :aria-describedby="describedBy">
    <legend class="array-list-legend">
      <inspector-disclosure
        :collapsed="collapsed"
        :errors="errors"
        @toggle="toggle"
        @focus="tooltip?.onFocusIn($event, legendLabel)"
        @blur="tooltip?.hide()"
        ><span
          ref="legendLabel"
          class="array-list-label"
          :class="{ 'label--described': Boolean(control.description) }"
          @mouseenter="tooltip?.show(legendLabel, $event)"
          @mouseleave="tooltip?.scheduleHide()"
          >{{ label }}</span
        ></inspector-disclosure
      >
    </legend>

    <div
      v-for="(row, index) in rows"
      v-show="!collapsed"
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
          :list="suggested.length && !locked ? `${listId}-keys` : undefined"
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
      <div
        class="control inspector-map__field"
        :data-entry-path="
          row.key.trim() ? `${control.path}.${row.key.trim()}` : undefined
        ">
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
          :list="valueList(row)"
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

    <p v-if="rows.length === 0" v-show="!collapsed" class="array-list-no-data">
      No {{ plural }}.
    </p>
    <p v-if="changedRows.some(Boolean)" :id="`${baseId}-changed`" hidden>
      Changed, not applied yet.
    </p>
    <inspector-field-messages
      :ids="ids"
      :error-text="errorText"
      :warnings="warnings"
      :description="control.description" />

    <datalist v-if="suggested.length && !locked" :id="`${listId}-keys`">
      <option v-for="key in suggested" :key="key" :value="key" />
    </datalist>
    <template v-if="!locked">
      <datalist
        v-for="(values, kind) in suggestedValues"
        :id="`${listId}-values-${kind}`"
        :key="kind">
        <option v-for="value in values" :key="value" :value="value" />
      </datalist>
    </template>

    <button
      v-if="!locked"
      v-show="!collapsed"
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
      :text="control.description"
      :path="specFieldPath(control.path)" />
  </fieldset>
</template>

<script>
  let rowIds = 0;
</script>

<script setup>
  import { computed, nextTick, onBeforeUnmount, ref, unref, watch } from 'vue';
  import { rendererProps, useJsonFormsControl } from '@jsonforms/vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import { specFieldPath } from '@/builder/formOutline.js';
  import { capitalize } from '@/builder/text.js';
  import {
    MAP_KEYS_KEYWORD,
    MAP_KEYWORD,
    MAP_VALUES_KEYWORD,
  } from '@/builder/schema.js';
  import { isPlainObject } from '@/builder/values.js';
  import {
    readValue,
    useEntryChanged,
    useInspectorAnnounce,
    useInspectorLocalProblems,
    useInspectorLocked,
    useInspectorResets,
    useInspectorSuggestions,
    useListControl,
    valueText,
  } from './control.js';
  import { useCollapsible } from './collapse.js';
  import InspectorDisclosure from './InspectorDisclosure.vue';
  import InspectorFieldMessages from './InspectorFieldMessages.vue';
  import InspectorTooltip from './InspectorTooltip.vue';

  const props = defineProps(rendererProps());
  const { control, handleChange } = useJsonFormsControl(props);
  const announce = useInspectorAnnounce();
  const reportProblems = useInspectorLocalProblems();
  const locked = useInspectorLocked(control);
  const resets = useInspectorResets();
  const entryChanged = useEntryChanged(control);
  const {
    label,
    noun,
    plural,
    baseId,
    ids,
    errorText,
    warnings,
    describedBy,
    itemName,
  } = useListControl(control, 'Entries');

  const { collapsed, errors, toggle } = useCollapsible(
    () => control.value.path,
  );

  const root = ref();
  const addButton = ref();
  const legendLabel = ref();
  const tooltip = ref();

  const typed = computed(() => control.value.schema?.[MAP_KEYWORD] === 'value');
  const suggested = computed(
    () => control.value.schema?.[MAP_KEYS_KEYWORD] || [],
  );

  // The ids of the suggestion lists: the list attribute takes a plain id,
  // and the base id is a path such as #/properties/labels.
  const listId = computed(() => baseId.value.replace(/[^\w-]/g, '-'));

  // The values the Inspector suggests for the value of an entry, by kind,
  // for the kinds the map's keys name (see MAP_VALUES_KEYWORD).
  const suggestions = useInspectorSuggestions();
  const valueKinds = computed(
    () => control.value.schema?.[MAP_VALUES_KEYWORD] || {},
  );
  const suggestedValues = computed(() =>
    Object.fromEntries(
      [...new Set(Object.values(valueKinds.value))]
        .map((kind) => [kind, unref(suggestions)?.[kind] || []])
        .filter(([, values]) => values.length > 0),
    ),
  );

  function valueList(row) {
    const kind = valueKinds.value[row.key.trim()];

    return kind && suggestedValues.value[kind] && !locked.value
      ? `${listId.value}-values-${kind}`
      : undefined;
  }

  const fieldAttrs = computed(() => ({
    disabled: !control.value.enabled && !locked.value,
    readonly: locked.value || undefined,
  }));

  function rowName(index) {
    return capitalize(itemName(index));
  }

  // The rows of a map. A row shown already for an entry keeps its id, so
  // its fields, and focus in them, stay.
  function toRows(data, shown = []) {
    const unused = [...shown];

    return Object.entries(isPlainObject(data) ? data : {}).map(
      ([key, value]) => {
        const text = typed.value ? valueText(value) : String(value ?? '');
        const index = unused.findIndex(
          (row) => row.key.trim() === key && row.value === text,
        );
        const id = index === -1 ? (rowIds += 1) : unused.splice(index, 1)[0].id;

        return { id, key, value: text };
      },
    );
  }

  const rows = ref(toRows(control.value.data));

  // Why each row is not in the working copy, or ''.
  const problems = computed(() =>
    rows.value.map((row, index) => {
      const key = row.key.trim();

      if (!key) {
        return row.value.trim() ? 'Enter a name' : '';
      }

      const first = rows.value.findIndex((other) => other.key.trim() === key);

      return first < index ? `${rowName(first)} already uses this name` : '';
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
      JSON.stringify(isPlainObject(data) ? data : {}) ===
      JSON.stringify(rowsData())
    );
  }

  // Data replaced from outside the map (Cancel, undo, Apply) shows as it
  // is. The rows' own commits leave the rows, and any row not committed,
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
