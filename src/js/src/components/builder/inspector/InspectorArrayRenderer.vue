<!--
  List fields (Drives, Interfaces, Commands, ...) for the Inspector, in place
  of the vanilla JSON Forms array renderer, whose buttons were named "+", "↑",
  "↓" and "🗙" and whose add button sat inside the legend.

  Each item is its own group ("Drive 2: ubuntu.qc2") with named buttons
  ("Move drive 2 up", "Remove drive 2"); "Add drive" follows the items. Add,
  remove and move are announced, and focus goes to the new item's first field
  or stays on the list instead of falling to the page. A locked list (see
  useInspectorLocked) has no buttons: its items cannot change. A new item is
  the one BuilderInspector provides for the list, if any (a new interface's
  name and kind), else its schema's default.

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
        @mouseenter="tooltip?.show(legendLabel)"
        @mouseleave="tooltip?.scheduleHide()"
        >{{ label }}</span
      >
    </legend>
    <p v-if="control.description" :id="`${baseId}-description`" hidden>
      {{ control.description }}
    </p>

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
  import { computed, nextTick, ref } from 'vue';
  import {
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
  import { itemNoun } from '@/builder/form-validator.js';
  import {
    labelWholeValue,
    useFieldWarnings,
    useInspectorAnnounce,
    useInspectorLocked,
    useInspectorNewItem,
  } from './control.js';
  import InspectorTooltip from './InspectorTooltip.vue';

  const props = defineProps(rendererProps());
  const { control, addItem, removeItems, moveUp, moveDown } =
    useJsonFormsArrayControl(props);
  const announce = useInspectorAnnounce();
  const locked = useInspectorLocked(control);
  const newItem = useInspectorNewItem();
  const jsonforms = useJsonForms(true);
  const warnings = useFieldWarnings(control);
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

  const label = computed(() => control.value.label || 'Items');
  const noun = computed(() => itemNoun(label.value));
  const plural = computed(() =>
    /^[A-Z][a-z]/.test(label.value)
      ? label.value[0].toLowerCase() + label.value.slice(1)
      : label.value,
  );
  const count = computed(() => (control.value.data || []).length);
  const baseId = computed(
    () => control.value.id || `field-${control.value.path}`,
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
  // control; name it after the item.
  function childUiSchema(index) {
    const name = itemName(index);

    return primitiveItems.value
      ? labelWholeValue(
          itemUiSchema.value,
          `${name[0].toUpperCase()}${name.slice(1)}`,
        )
      : itemUiSchema.value;
  }

  function itemName(index) {
    return `${noun.value} ${index + 1}`;
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

    return `${title[0].toUpperCase()}${title.slice(1)}${
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
