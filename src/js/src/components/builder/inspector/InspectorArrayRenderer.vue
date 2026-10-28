<!--
  List fields for the Inspector, with named Add, Move, Insert and Remove buttons
  and collapsible items. A locked list has no buttons.
-->
<template>
  <fieldset
    v-if="control.visible"
    ref="root"
    class="array-list"
    :class="{ 'array-list--collapsed': collapsed }"
    :data-path="control.path"
    :data-outline="control.path"
    :data-outline-label="label"
    :data-outline-count="count"
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
      v-for="(item, index) in control.data || []"
      v-show="!collapsed"
      :key="`${control.path}-${index}`"
      class="array-list-item-wrapper">
      <fieldset
        class="array-list-item"
        :class="{ 'array-list-item--collapsed': itemCollapsed(index) }"
        :data-outline="itemPath(index)"
        :data-outline-label="itemTitle(index)"
        :data-outline-errors="itemErrors(index) ? 'true' : undefined"
        :data-collapse-path="itemPath(index)">
        <legend class="array-list-item-label">
          <inspector-disclosure
            :collapsed="itemCollapsed(index)"
            :errors="itemErrors(index)"
            @toggle="toggleItem(index)"
            >{{ itemTitle(index) }}</inspector-disclosure
          >
        </legend>
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
        <div v-show="!itemCollapsed(index)" class="array-list-item-content">
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

    <p v-if="count === 0" v-show="!collapsed" class="array-list-no-data">
      No {{ plural }}.
    </p>
    <inspector-field-messages
      :ids="ids"
      :error-text="errorText"
      :warnings="warnings"
      :description="control.description" />

    <button
      v-if="!locked"
      v-show="!collapsed"
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
      :text="control.description"
      :path="specFieldPath(control.path)" />
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
  import { entryTitle, specFieldPath } from '@/builder/formOutline.js';
  import { capitalize } from '@/builder/text.js';
  import { useCollapsible, useInspectorCollapse } from './collapse.js';
  import {
    labelWholeValue,
    useInspectorAnnounce,
    useInspectorInsertItem,
    useInspectorLocked,
    useInspectorNewItem,
    useListControl,
  } from './control.js';
  import InspectorDisclosure from './InspectorDisclosure.vue';
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

  const count = computed(() => (control.value.data || []).length);

  // The list and each of its items collapse (see collapse.js).
  const collapse = useInspectorCollapse();
  const { collapsed, errors, toggle } = useCollapsible(
    () => control.value.path,
  );

  function itemPath(index) {
    return composePaths(control.value.path, `${index}`);
  }

  function itemCollapsed(index) {
    return collapse.isCollapsed(itemPath(index));
  }

  function itemErrors(index) {
    return collapse.hasErrors(itemPath(index));
  }

  function toggleItem(index) {
    collapse.setCollapsed(itemPath(index), !itemCollapsed(index));
  }

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
  // control, so this names the control after the item.
  function childUiSchema(index) {
    if (!primitiveItems.value) {
      return itemUiSchema.value;
    }

    return labelWholeValue(itemUiSchema.value, capitalize(itemName(index)));
  }

  function itemTitle(index) {
    return entryTitle(capitalize(itemName(index)), control.value.data?.[index]);
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

  // Fields that a new item's focus goes to before its first field: an
  // interface's Address, the field a new interface needs (see
  // INTERFACE_FIELDS in schema.js).
  const FIRST_FIELDS = ['address'];

  function add() {
    collapse.setCollapsed(itemPath(count.value), false);
    addItem(
      control.value.path,
      newItem(control.value.path, jsonforms?.core?.data) ??
        createDefaultValue(control.value.schema, control.value.rootSchema),
    )();
    announce(`Added ${itemName(count.value - 1)}.`);

    const path = `${control.value.path}.${count.value - 1}`;

    focusItem(count.value - 1, [
      ...FIRST_FIELDS.map(
        (key) =>
          `.control[data-path="${path}.${key}"] :is(input, select, textarea)`,
      ),
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

    collapse.inserted(control.value.path, index + 1);
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
    collapse.removed(control.value.path, index);
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
    collapse.moved(control.value.path, index, to);
    announce(
      `Moved ${noun.value} ${index + 1} ${step < 0 ? 'up' : 'down'} to position ${to + 1}.`,
    );
    focusItem(to, [
      step < 0 ? '.array-list-item-move-up' : '.array-list-item-move-down',
      step < 0 ? '.array-list-item-move-down' : '.array-list-item-move-up',
    ]);
  }
</script>
