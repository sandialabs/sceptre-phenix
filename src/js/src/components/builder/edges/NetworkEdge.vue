<!-- Network edge: color, dash pattern and label each show the network, so the
     diagram reads without color. Vue Flow's wrapper <g> is the focusable element.
     It inherits no attributes, because Vue Flow passes extra ones. -->
<template>
  <!-- A wide, invisible stroke under the line, so a pointer does not have to
       hit the line itself (WCAG 2.5.8). -->
  <path
    class="builder-edge__hit"
    :d="line.d"
    fill="none"
    stroke="transparent"
    stroke-width="24"
    pointer-events="stroke" />
  <!-- The keyboard focus band (see builder.css). The pointer passes through
       it to the hit area, which keeps its width while the edge has focus. -->
  <path
    class="builder-edge__focus"
    :d="line.d"
    fill="none"
    pointer-events="none" />
  <!-- A casing under a line whose chosen color would fade into the canvas
       in a theme (see colors.js). The theme's CSS draws it. -->
  <path
    v-if="casing"
    class="builder-edge__casing"
    :class="{
      'is-low-light': casing.light,
      'is-low-dark': casing.dark,
      'is-selected': selected,
    }"
    :d="line.d"
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
    :d="line.d"
    fill="none"
    :stroke-dasharray="style.dashArray"
    :data-network="style.label"
    :data-pattern="style.pattern"
    :data-routed="line.routed ? 'true' : undefined" />
  <!-- Moved to Vue Flow's label layer. The canvas finds the layer once
       (see BuilderCanvas.vue), not each edge, as Vue Flow's
       EdgeLabelRenderer does. The foreignObject gives the label HTML's
       namespace. -->
  <svg v-if="(label || issue) && labelLayer">
    <foreignObject height="0" width="0">
      <Teleport :to="labelLayer">
        <div
          v-if="label"
          class="builder-edge__label nodrag nopan"
          :class="{ 'is-selected': selected }"
          :style="atMiddle"
          :data-edge-id="id"
          aria-hidden="true">
          <span
            v-if="issue"
            class="builder-edge__issue"
            :data-level="issue.level"
            data-testid="edge-issue">
            <builder-icon :name="issueIcon" :size="12" />
          </span>
          {{ label }}
        </div>
        <div
          v-else
          class="builder-edge__issue builder-edge__issue--alone nodrag nopan"
          :style="atMiddle"
          :data-level="issue.level"
          :data-edge-id="id"
          data-testid="edge-issue"
          aria-hidden="true">
          <builder-icon :name="issueIcon" :size="12" />
        </div>
        <span
          v-if="issue"
          :id="edgeIssueId(id)"
          class="builder-edge__issue-text"
          hidden
          >{{ issue.text }}</span
        >
      </Teleport>
    </foreignObject>
  </svg>
</template>

<script setup>
  import { computed, inject } from 'vue';
  import { getSmoothStepPath } from '@vue-flow/core';

  import BuilderIcon from '../BuilderIcon.vue';

  import {
    customNetworkColor,
    drawnColor,
    needsCasing,
  } from '@/builder/colors.js';
  import { edgeIssueId } from '@/builder/adapters/vueflow.js';
  import { fitRoute, routePath } from '@/builder/routes.js';

  defineOptions({ inheritAttrs: false });

  // How far a route's end may be from its handle and still be followed: a
  // node dragged further, not yet dropped, has left it.
  const ROUTE_TOLERANCE = 4;
  // Room between the bends of lines into one switch, and the least a line
  // runs straight out of its handle and into the other before it bends
  // (Vue Flow's own).
  const LANE_GAP = 10;
  const STUB = 20;

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

  // Vue Flow's layer for edge labels, once the canvas has it.
  const labelLayer = inject('builderEdgeLabels', null);

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
  // What the diagram checks found about the connection, or null.
  const issue = computed(() => props.data.issue || null);
  const issueIcon = computed(() =>
    issue.value?.level === 'error' ? 'close' : 'warning',
  );

  const casing = computed(() => {
    const low = color.value ? needsCasing(color.value) : null;

    return low && (low.light || low.dark) ? low : null;
  });

  // Vue Flow ends a line at the outer edge of its handle. Handles sit centred
  // on the node border and downloaded images leave them out, so the line runs
  // on to the handle's centre: on screen the extra length is under the
  // handle, and a line in a downloaded image still meets its node.
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

  // The column a line bends in: its own, among the lines that share its end
  // at a switch, spread about the middle, closer together when there is
  // little room. Undefined for the middle.
  function bendX(sourceX, targetX) {
    const lane = props.data.lane;
    const room = targetX - sourceX - 2 * STUB;

    if (!lane || lane.count < 2 || room <= 0) {
      return undefined;
    }

    const gap = Math.min(LANE_GAP, room / (lane.count - 1));

    return (sourceX + targetX) / 2 + (lane.index - (lane.count - 1) / 2) * gap;
  }

  // The line's path, where its label goes, and whether it follows a route.
  const line = computed(() => {
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

    const route = fitRoute(
      props.data.edge?.route,
      { x: sourceX, y: sourceY },
      { x: targetX, y: targetY },
      ROUTE_TOLERANCE,
    );

    if (route) {
      const drawn = routePath(route, 8);

      return { d: drawn.path, x: drawn.labelX, y: drawn.labelY, routed: true };
    }

    const [d, x, y] = getSmoothStepPath({
      sourceX,
      sourceY,
      sourcePosition: props.sourcePosition,
      targetX,
      targetY,
      targetPosition: props.targetPosition,
      borderRadius: 8,
      centerX: bendX(sourceX, targetX),
    });

    return { d, x, y, routed: false };
  });

  // Where the label, or the mark alone, is drawn: centred on the middle of
  // the line.
  const atMiddle = computed(() => ({
    transform: `translate(-50%, -50%) translate(${line.value.x}px, ${line.value.y}px)`,
  }));
</script>
