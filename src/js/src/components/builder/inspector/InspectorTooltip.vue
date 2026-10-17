<!--
  The description tooltip of an Inspector field or list (see fieldTooltip.js).
  The field calls show(anchor, event), scheduleHide(), hide() and
  onFocusIn(event, anchor) on it. The tooltip keeps its own state, so it
  changes without a re-render of the field.

  A field of the phenix node spec has its path in the spec first, in bold
  on a line of its own ("network.interfaces.0.address", see specFieldPath),
  then its description. The path is for the eye: a screen reader gets the
  description alone, as the field's accessible description.
-->
<template>
  <builder-fixed-tooltip
    v-slot="{ content }"
    :tooltip="tooltip"
    testid="inspector-tooltip">
    <strong
      v-if="content.path"
      class="builder-tooltip__path"
      data-testid="inspector-tooltip-path"
      >{{ content.path }}</strong
    >
    <span data-testid="inspector-tooltip-text">{{ content.text }}</span>
  </builder-fixed-tooltip>
</template>

<script setup>
  import BuilderFixedTooltip from '../BuilderFixedTooltip.vue';
  import { useFieldTooltip } from './fieldTooltip.js';

  const props = defineProps({
    text: { type: String, default: '' },
    // The field's path in the phenix node spec, or '' for none.
    path: { type: String, default: '' },
  });

  const tooltip = useFieldTooltip(() =>
    props.text ? { path: props.path, text: props.text } : null,
  );
  const { show, scheduleHide, hide, onFocusIn } = tooltip;

  defineExpose({ show, scheduleHide, hide, onFocusIn });
</script>
