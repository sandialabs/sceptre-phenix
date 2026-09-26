<!--
  The description tooltip of an Inspector field or list (see fieldTooltip.js).
  The field calls show(anchor), scheduleHide(), hide() and
  onFocusIn(event, anchor) on it; the tooltip's state is its own, so it
  changes without re-rendering the field.
-->
<template>
  <div
    v-if="tip"
    ref="tipEl"
    class="builder-tooltip builder-tooltip--fixed"
    data-testid="inspector-tooltip"
    aria-hidden="true"
    :style="{ top: `${tip.top}px`, left: `${tip.left}px` }">
    {{ tip.text }}
  </div>
</template>

<script setup>
  import { useFieldTooltip } from './fieldTooltip.js';

  const props = defineProps({ text: { type: String, default: '' } });

  const { tip, tipEl, show, scheduleHide, hide, onFocusIn } = useFieldTooltip(
    () => props.text,
  );

  defineExpose({ show, scheduleHide, hide, onFocusIn });
</script>
