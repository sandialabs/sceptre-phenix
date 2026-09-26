<!--
  Network edge.

  Network identity is carried by three redundant cues: stroke color (the
  network's token, or the color the user gave it; see colors.js), dash
  pattern and a visible text label, so the diagram stays readable without
  color perception. The label is drawn in Vue Flow's label layer, above the nodes,
  so a node never hides it, and lets the pointer through to what is under it:
  the line, or a node it overlaps. Vue Flow's wrapper <g> is the focusable,
  named element (see adapters/vueflow.js); the label only repeats the network
  name.

  A color the user gave the connection itself replaces the network's; its
  dash pattern and label still name the network.
-->
<template>
  <!-- A wide, invisible stroke under the line, so a pointer does not have to
       hit the line itself (WCAG 2.5.8). -->
  <path
    class="builder-edge__hit"
    :d="path[0]"
    fill="none"
    stroke="transparent"
    stroke-width="24"
    pointer-events="stroke" />
  <!-- The keyboard focus band (see builder.css). The pointer passes through
       it to the hit area, which keeps its width while the edge has focus. -->
  <path
    class="builder-edge__focus"
    :d="path[0]"
    fill="none"
    pointer-events="none" />
  <!-- A casing under a line whose chosen color would fade into the canvas
       in a theme (see colors.js); the theme's CSS draws it. -->
  <path
    v-if="casing"
    class="builder-edge__casing"
    :class="{
      'is-low-light': casing.light,
      'is-low-dark': casing.dark,
      'is-selected': selected,
    }"
    :d="path[0]"
    fill="none"
    :stroke-dasharray="style.dashArray"
    pointer-events="none" />
  <path
    :id="id"
    class="builder-edge"
    :class="[
      `builder-edge--network-${style.token}`,
      { 'builder-edge--custom': color, 'is-selected': selected },
    ]"
    :style="color ? { '--bx-edge-color': color } : undefined"
    :d="path[0]"
    fill="none"
    :stroke-dasharray="style.dashArray"
    :data-network="style.label"
    :data-pattern="style.pattern" />
  <EdgeLabelRenderer>
    <div
      v-if="label"
      class="builder-edge__label nodrag nopan"
      :class="{ 'is-selected': selected }"
      :style="{
        transform: `translate(-50%, -50%) translate(${path[1]}px, ${path[2]}px)`,
      }"
      :data-edge-id="id"
      aria-hidden="true">
      {{ label }}
    </div>
  </EdgeLabelRenderer>
</template>

<script setup>
  import { computed } from 'vue';
  import { EdgeLabelRenderer, getSmoothStepPath } from '@vue-flow/core';

  import {
    customNetworkColor,
    drawnColor,
    needsCasing,
  } from '@/builder/colors.js';

  const props = defineProps({
    id: { type: String, required: true },
    sourceX: { type: Number, required: true },
    sourceY: { type: Number, required: true },
    targetX: { type: Number, required: true },
    targetY: { type: Number, required: true },
    sourcePosition: { type: String, default: 'right' },
    targetPosition: { type: String, default: 'left' },
    sourceNode: { type: Object, default: null },
    targetNode: { type: Object, default: null },
    sourceHandleId: { type: String, default: null },
    targetHandleId: { type: String, default: null },
    data: { type: Object, default: () => ({}) },
    label: { type: String, default: '' },
    selected: { type: Boolean, default: false },
  });

  const style = computed(
    () => props.data.style || { token: 0, pattern: 'solid', label: '' },
  );

  // The color the user gave the connection, else the one they gave its
  // network, drawn in place of the network's token.
  const color = computed(
    () =>
      drawnColor(props.data.edge?.color) ||
      customNetworkColor(props.data.network?.color),
  );
  const casing = computed(() => {
    const low = color.value ? needsCasing(color.value) : null;

    return low && (low.light || low.dark) ? low : null;
  });

  // Vue Flow ends a line at the outer edge of its handle. Handles sit centred
  // on the node border and image exports leave them out, so the line runs on
  // to the handle's centre: on screen the extra length is under the handle,
  // and an exported line still meets its node.
  function atHandleCentre(x, y, position, node, handleId) {
    // A node with no handles of one type has null, not [], for it.
    const bounds = node?.handleBounds || {};
    const handle = [...(bounds.source || []), ...(bounds.target || [])].find(
      (candidate) => candidate.id === handleId,
    );
    const inward = {
      left: [1, 0],
      right: [-1, 0],
      top: [0, 1],
      bottom: [0, -1],
    }[position];

    if (!handle || !inward) {
      return [x, y];
    }

    return [
      x + (inward[0] * handle.width) / 2,
      y + (inward[1] * handle.height) / 2,
    ];
  }

  const path = computed(() => {
    const [sourceX, sourceY] = atHandleCentre(
      props.sourceX,
      props.sourceY,
      props.sourcePosition,
      props.sourceNode,
      props.sourceHandleId,
    );
    const [targetX, targetY] = atHandleCentre(
      props.targetX,
      props.targetY,
      props.targetPosition,
      props.targetNode,
      props.targetHandleId,
    );

    return getSmoothStepPath({
      sourceX,
      sourceY,
      sourcePosition: props.sourcePosition,
      targetX,
      targetY,
      targetPosition: props.targetPosition,
      borderRadius: 8,
    });
  });
</script>
