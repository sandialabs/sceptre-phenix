<!--
  The description tooltip of an Inspector field or list (see fieldTooltip.js).
  The field calls show(anchor, event), scheduleHide(), hide() and
  onFocusIn(event, anchor) on it. The tooltip keeps its own state, so it
  changes without a re-render of the field.
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
