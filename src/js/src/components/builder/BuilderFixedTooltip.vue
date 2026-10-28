<!-- The tooltip that useFixedTooltip shows and places (fixedTooltip.js). Pass
     the return value of useFixedTooltip as tooltip. -->
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
    <slot :content="tooltip.tip.value.text">{{ tooltip.tip.value.text }}</slot>
  </div>
</template>

<script setup>
  defineProps({
    tooltip: { type: Object, required: true },
    testid: { type: String, required: true },
  });
</script>
