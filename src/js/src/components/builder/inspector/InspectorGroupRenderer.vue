<!--
  A group of Inspector fields, with the vanilla renderer's classes. A labelled
  group collapses, and its hidden fields stay rendered to keep their state.
-->
<template>
  <fieldset
    v-if="layout.visible"
    class="group"
    :class="{
      'group--heading': heading,
      'group--collapsed': collapsible && collapsed,
    }"
    v-bind="outlineAttrs">
    <legend v-if="heading" class="group-label">
      <component :is="`h${headingLevel}`" class="group-heading">{{
        layout.label
      }}</component>
    </legend>
    <legend v-else-if="collapsible" class="group-label">
      <inspector-disclosure
        :collapsed="collapsed"
        :errors="errors"
        @toggle="toggle"
        >{{ layout.label }}</inspector-disclosure
      >
    </legend>
    <legend v-else-if="layout.label" class="group-label">
      {{ layout.label }}
    </legend>
    <div
      v-for="(element, index) in layout.uischema.elements"
      v-show="!(collapsible && collapsed)"
      :key="`${layout.path}-${index}`"
      class="group-item">
      <dispatch-renderer
        :schema="layout.schema"
        :uischema="element"
        :path="layout.path"
        :enabled="layout.enabled"
        :renderers="layout.renderers"
        :cells="layout.cells" />
    </div>
  </fieldset>
</template>

<script setup>
  import { computed } from 'vue';
  import {
    DispatchRenderer,
    rendererProps,
    useJsonFormsLayout,
  } from '@jsonforms/vue';

  import { useCollapsible } from './collapse.js';
  import { useInspectorHeadingLevel } from './control.js';
  import InspectorDisclosure from './InspectorDisclosure.vue';

  // JSON Forms' object renderer passes `visible`, which is no attribute of
  // a fieldset.
  defineOptions({ inheritAttrs: false });

  const props = defineProps(rendererProps());
  const { layout } = useJsonFormsLayout(props);
  const headingLevel = useInspectorHeadingLevel();

  const heading = computed(() =>
    Boolean(layout.value.uischema.options?.heading),
  );
  // The data path of the object the group shows, such as "spec.general".
  const path = computed(() => layout.value.path || '');
  const collapsible = computed(
    () => !heading.value && Boolean(layout.value.label) && Boolean(path.value),
  );
  const { collapsed, errors, toggle } = useCollapsible(path);

  const outlineAttrs = computed(() =>
    collapsible.value
      ? {
          'data-outline': path.value,
          'data-outline-label': layout.value.label,
          'data-outline-errors': errors.value ? 'true' : undefined,
          'data-collapse-path': path.value,
        }
      : {},
  );
</script>
