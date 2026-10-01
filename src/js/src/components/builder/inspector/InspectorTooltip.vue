<!--
  The description tooltip of an Inspector field or list (see fieldTooltip.js).
  The field calls show(anchor, event), scheduleHide(), hide() and
  onFocusIn(event, anchor) on it; the tooltip's state is its own, so it
  changes without re-rendering the field.
-->
<template>
  <builder-fixed-tooltip :tooltip="tooltip" testid="inspector-tooltip" />
</template>

<script setup>
  import BuilderFixedTooltip from '../BuilderFixedTooltip.vue';
  import { useFieldTooltip } from './fieldTooltip.js';

  const props = defineProps({ text: { type: String, default: '' } });

  const tooltip = useFieldTooltip(() => props.text);
  const { show, scheduleHide, hide, onFocusIn } = tooltip;

  defineExpose({ show, scheduleHide, hide, onFocusIn });
</script>
