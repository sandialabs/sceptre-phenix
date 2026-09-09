<!--
  Network edge.

  Network identity is carried by three redundant cues: stroke color token, dash
  pattern and a visible text label, so the diagram stays readable without color
  perception.
-->
<template>
  <path
    :id="id"
    class="builder-edge"
    :class="[
      `builder-edge--network-${style.token}`,
      { 'is-selected': selected },
    ]"
    :d="path[0]"
    fill="none"
    :stroke-dasharray="style.dashArray"
    :data-network="style.label"
    :data-pattern="style.pattern"
    :aria-label="ariaLabel" />
  <text
    class="builder-edge__label"
    :x="path[1]"
    :y="path[2]"
    text-anchor="middle"
    dominant-baseline="middle">
    {{ label }}
  </text>
</template>

<script setup>
  import { computed } from 'vue';
  import { getSmoothStepPath } from '@vue-flow/core';

  const props = defineProps({
    id: { type: String, required: true },
    sourceX: { type: Number, required: true },
    sourceY: { type: Number, required: true },
    targetX: { type: Number, required: true },
    targetY: { type: Number, required: true },
    sourcePosition: { type: String, default: 'right' },
    targetPosition: { type: String, default: 'left' },
    data: { type: Object, default: () => ({}) },
    label: { type: String, default: '' },
    selected: { type: Boolean, default: false },
    ariaLabel: { type: String, default: '' },
  });

  const style = computed(
    () => props.data.style || { token: 0, pattern: 'solid', label: '' },
  );

  const path = computed(() =>
    getSmoothStepPath({
      sourceX: props.sourceX,
      sourceY: props.sourceY,
      sourcePosition: props.sourcePosition,
      targetX: props.targetX,
      targetY: props.targetY,
      targetPosition: props.targetPosition,
      borderRadius: 8,
    }),
  );
</script>
