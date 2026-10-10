<!--
  List fields (Drives, Interfaces, Commands, ...) for the Inspector, in place
  of the vanilla JSON Forms array renderer, whose buttons were named "+", "↑",
  "↓" and "🗙" and whose add button sat inside the legend.

  Each item is its own group ("Drive 2: ubuntu.qc2") with named buttons
  ("Move drive 2 up", "Remove drive 2"). "Add drive" follows the items. Add,
  remove and move are announced, and focus goes to the new item's first field
  or stays on the list instead of falling to the page. A locked list (see
  useInspectorLocked) has no buttons: its items cannot change. A new item is
  the one BuilderInspector provides for the list, if any (a new interface's
  name and kind), or else its schema's default. A list that takes an item
  between two others (a line's points, see useInspectorInsertItem) has an
  "Insert … after" button on each item but its last ("Insert point after
  point 2"), which puts the item BuilderInspector provides there.

  The list's description is a tooltip on its legend, shown on hover and
  while Add has keyboard focus, and the group's accessible description, as
  for a field (see InspectorField.vue). Its warnings follow its items, as a
  field's follow its input.
-->
<template>
  <fieldset
    v-if="control.visible"
    ref="root"
    class="array-list"
    :data-path="control.path"
    :aria-describedby="describedBy">
    <legend class="array-list-legend">
      <span
        ref="legendLabel"
        class="array-list-label"
        :class="{ 'label--described': Boolean(control.description) }"
        @mouseenter="tooltip?.show(legendLabel, $event)"
        @mouseleave="tooltip?.scheduleHide()"
        >{{ label }}</span
      >
    </legend>

    <div
      v-for="(item, index) in control.data || []"
      :key="`${control.path}-${index}`"
      class="array-list-item-wrapper">
      <fieldset class="array-list-item">
        <legend class="array-list-item-label">{{ itemTitle(index) }}</legend>
        <div v-if="!locked" class="array-list-item-toolbar">
          <button
            type="button"
            class="array-list-item-move-up"
            :aria-label="`Move ${itemName(index)} up`"
            :title="`Move ${itemName(index)} up`"
            :disabled="!control.enabled || index === 0"
            @click="move(index, -1)">
            <builder-icon name="arrow-up" :size="14" />
          </button>
          <button
            type="button"
            class="array-list-item-move-down"
            :aria-label="`Move ${itemName(index)} down`"
            :title="`Move ${itemName(index)} down`"
            :disabled="!control.enabled || index === count - 1"
            @click="move(index, 1)">
            <builder-icon name="arrow-down" :size="14" />
          </button>
          <button
            v-if="insertable(index)"
            type="button"
            class="array-list-item-insert"
            :aria-label="`Insert ${noun} after ${itemName(index)}`"
            :title="`Insert ${noun} after ${itemName(index)}`"
            :disabled="!control.enabled || atMaximum"
            @click="insert(index)">
            <builder-icon name="plus" :size="14" />
          </button>
          <button
            type="button"
            class="array-list-item-delete"
            :aria-label="`Remove ${itemName(index)}`"
            :title="`Remove ${itemName(index)}`"
            :disabled="!control.enabled || atMinimum"
            @click="remove(index)">
            <builder-icon name="trash" :size="14" />
          </button>
        </div>
        <div class="array-list-item-content">
          <dispatch-renderer
            :schema="control.schema"
            :uischema="childUiSchema(index)"
            :path="composePaths(control.path, `${index}`)"
            :enabled="control.enabled"
            :renderers="control.renderers"
            :cells="control.cells" />
        </div>
      </fieldset>
    </div>

    <p v-if="count === 0" class="array-list-no-data">No {{ plural }}.</p>
    <inspector-field-messages
      :ids="ids"
      :error-text="errorText"
      :warnings="warnings"
      :description="control.description" />

    <button
      v-if="!locked"
      ref="addButton"
      type="button"
      class="array-list-add builder-button"
      :disabled="!control.enabled || atMaximum"
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

<script setup>
  import { computed, inject, nextTick, ref } from 'vue';
  import {
    Actions,
    composePaths,
    createDefaultValue,
    findUISchema,
    Resolve,
  } from '@jsonforms/core';
  import {
    DispatchRenderer,
    rendererProps,
    useJsonForms,
    useJsonFormsArrayControl,
  } from '@jsonforms/vue';

  import BuilderIcon from '../BuilderIcon.vue';
  import { capitalize } from '@/builder/text.js';
  import {
    isMultilineList,
    labelWholeValue,
    useInspectorAnnounce,
    useInspectorInsertItem,
    useInspectorLocked,
    useInspectorNewItem,
    useListControl,
  } from './control.js';
  import InspectorFieldMessages from './InspectorFieldMessages.vue';
  import InspectorTooltip from './InspectorTooltip.vue';

  const props = defineProps(rendererProps());
  const { control, addItem, removeItems, moveUp, moveDown } =
    useJsonFormsArrayControl(props);
  const announce = useInspectorAnnounce();
  const locked = useInspectorLocked(control);
  const newItem = useInspectorNewItem();
  const insertItem = useInspectorInsertItem();
  const jsonforms = useJsonForms(true);
  // JSON Forms' own dispatch, which the list's Add and Remove use as well.
  const dispatch = inject('dispatch', null);
  const {
    label,
    noun,
    plural,
    ids,
    errorText,
    warnings,
    describedBy,
    itemName,
  } = useListControl(control, 'Items');
  const legendLabel = ref();
  const tooltip = ref();

  const root = ref();
  const addButton = ref();

  // Fields whose value best names an item in its group's legend.
  const SUMMARY_KEYS = [
    'name',
    'hostname',
    'image',
    'destination',
    'network',
    'src',
    'description',
  ];

  const count = computed(() => (control.value.data || []).length);

  const arraySchema = computed(() =>
    Resolve.schema(
      props.schema,
      control.value.uischema.scope,
      control.value.rootSchema,
    ),
  );
  const atMinimum = computed(
    () =>
      arraySchema.value?.minItems !== undefined &&
      count.value <= arraySchema.value.minItems,
  );
  const atMaximum = computed(
    () =>
      arraySchema.value?.maxItems !== undefined &&
      count.value >= arraySchema.value.maxItems,
  );

  const itemUiSchema = computed(() =>
    findUISchema(
      control.value.uischemas,
      control.value.schema,
      control.value.uischema.scope,
      control.value.path,
      undefined,
      control.value.uischema,
      control.value.rootSchema,
    ),
  );

  const primitiveItems = computed(() => {
    const schema = control.value.schema || {};

    return (
      !schema.oneOf &&
      !schema.anyOf &&
      ![schema.type].flat().some((type) => ['object', 'array'].includes(type))
    );
  });

  // A list of primitives (Commands) edits each item with one unlabelled
  // control, so this names the control after the item. An item of a list of
  // text of several lines (Notes, see isMultilineList) is a text area.
  function childUiSchema(index) {
    if (!primitiveItems.value) {
      return itemUiSchema.value;
    }

    return labelWholeValue(
      itemUiSchema.value,
      capitalize(itemName(index)),
      isMultilineList(control.value.path) ? { multi: true } : undefined,
    );
  }

  function itemTitle(index) {
    const item = control.value.data?.[index];
    const title = itemName(index);
    const summary =
      item && typeof item === 'object'
        ? SUMMARY_KEYS.map((key) => item[key]).find(
            (value) => typeof value === 'string' && value.trim(),
          )
        : item;
    const text = summary === undefined || summary === null ? '' : `${summary}`;

    return `${capitalize(title)}${
      text ? `: ${text.length > 40 ? `${text.slice(0, 39)}…` : text}` : ''
    }`;
  }

  // The item wrappers of this list only, not those of lists nested in it.
  function itemElement(index) {
    return root.value?.querySelectorAll(':scope > .array-list-item-wrapper')[
      index
    ];
  }

  // Focuses the first enabled element matched by one of `selectors` inside
  // item `index`, or the Add button.
  async function focusItem(index, selectors) {
    await nextTick();
    const item = itemElement(index);

    for (const selector of selectors) {
      const target = item?.querySelector(
        `${selector}:not(:disabled):not([type="hidden"])`,
      );

      if (target) {
        target.focus();

        return;
      }
    }

    addButton.value?.focus();
  }

  function add() {
    addItem(
      control.value.path,
      newItem(control.value.path, jsonforms?.core?.data) ??
        createDefaultValue(control.value.schema, control.value.rootSchema),
    )();
    announce(`Added ${itemName(count.value - 1)}.`);
    focusItem(count.value - 1, [
      '.array-list-item-content :is(input, select, textarea)',
      '.array-list-item-delete',
    ]);
  }

  // The item the list puts after item `index`, for a list that takes one
  // between two others (see useInspectorInsertItem).
  function insertedAfter(index) {
    return insertItem(control.value.path, index, jsonforms?.core?.data);
  }

  function insertable(index) {
    return index < count.value - 1 && insertedAfter(index) !== undefined;
  }

  // Puts the new item after item `index`, as one change of the list.
  function insert(index) {
    const item = insertedAfter(index);

    if (item === undefined || !dispatch) {
      return;
    }

    dispatch(
      Actions.update(control.value.path, (list) => [
        ...(list || []).slice(0, index + 1),
        item,
        ...(list || []).slice(index + 1),
      ]),
    );
    announce(`Added ${itemName(index + 1)}.`);
    focusItem(index + 1, [
      '.array-list-item-content :is(input, select, textarea)',
      '.array-list-item-delete',
    ]);
  }

  function remove(index) {
    const name = itemName(index);

    removeItems(control.value.path, [index])();
    announce(`Removed ${name}.`);

    if (count.value === 0) {
      nextTick(() => addButton.value?.focus());
    } else {
      focusItem(Math.min(index, count.value - 1), ['.array-list-item-delete']);
    }
  }

  function move(index, step) {
    const to = index + step;

    (step < 0 ? moveUp : moveDown)(control.value.path, index)();
    announce(
      `Moved ${noun.value} ${index + 1} ${step < 0 ? 'up' : 'down'} to position ${to + 1}.`,
    );
    focusItem(to, [
      step < 0 ? '.array-list-item-move-up' : '.array-list-item-move-down',
      step < 0 ? '.array-list-item-move-down' : '.array-list-item-move-up',
    ]);
  }
</script>
