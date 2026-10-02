<!--
  The tooltip useFixedTooltip shows and places (see fixedTooltip.js). The
  component showing it passes what useFixedTooltip returned as `tooltip`,
  which the tooltip hands its element back to.
-->
<template>
  <div
    v-if="tooltip.tip.value"
    :ref="tooltip.setTipEl"
    class="builder-tooltip builder-tooltip--fixed"
    :data-testid="testid"
    aria-hidden="true"
    :style="{
      top: `${tooltip.tip.value.top}px`,
      left: `${tooltip.tip.value.left}px`,
    }">
    {{ tooltip.tip.value.text }}
  </div>
</template>

<script setup>
  defineProps({
    tooltip: { type: Object, required: true },
    testid: { type: String, required: true },
  });
</script>
