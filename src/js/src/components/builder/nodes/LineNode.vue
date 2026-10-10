<!--
  Line node: a polyline drawn on the canvas, joined to no node or network,
  with no phenix semantics and never published. The node's box is the box
  of its points (see placedLine in model.js), which are relative to it.

  The line is drawn as a connection is: in the color chosen for it (the
  text color without one), cased where that color would fade into the
  canvas (see colors.js), in its line style's dash pattern (see
  NETWORK_PATTERNS in adapters/vueflow.js), with its label at its middle
  segment and an arrowhead at either end it has one. A wide, invisible
  stroke under it takes the pointer (WCAG 2.5.8), and only that stroke:
  the rest of the box lets the pointer through to what is under it (the
  node's style sets pointer-events, see toFlowNodes). The line lies under
  the devices and switches, also while it is selected (see nodeZIndex in
  adapters/vueflow.js).

  Vue Flow's wrapper is the focusable, named element (see DeviceNode.vue);
  its name says the arrowheads, which only the canvas shows otherwise. A
  focused line gets a band under it, as a focused connection does.

  While the line is selected and the draft can be changed, each point has a
  handle, drawn in Vue Flow's edge label layer: over the nodes, and outside
  the node's wrapper, a button that must hold nothing focusable. Dragging a
  handle moves its point, as one edit when it is dropped; double-clicking
  the line adds a bend there. A handle a click focused moves its point with
  the arrow keys (by a grid step, or a pixel with Shift) and removes it with
  Delete or Backspace, never leaving fewer than two points. The handles are
  for the pointer: the Inspector's Points list edits every point from the
  keyboard, so they stay out of the Tab order and are hidden from
  assistive technology.
-->
<template>
  <div
    ref="root"
    class="builder-line"
    :class="{ 'is-selected': selected, 'is-editing': editing }"
    :style="{ '--bx-line-color': color || 'var(--bx-text)' }"
    :data-node-id="id"
    data-node-kind="line"
    data-testid="builder-node">
    <svg
      class="builder-line__drawing"
      :width="box.width"
      :height="box.height"
      overflow="visible"
      aria-hidden="true">
      <defs v-if="line.startArrow || line.endArrow">
        <marker
          :id="arrowId"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="12"
          markerHeight="12"
          markerUnits="userSpaceOnUse"
          orient="auto-start-reverse">
          <path class="builder-line__arrow" d="M0,0L10,5L0,10z" />
        </marker>
      </defs>
      <polyline
        class="builder-line__hit"
        :points="pointsText"
        fill="none"
        stroke="transparent"
        stroke-width="24"
        pointer-events="stroke"
        @dblclick="addBend" />
      <polyline
        class="builder-line__focus"
        :points="pointsText"
        fill="none"
        pointer-events="none" />
      <polyline
        v-if="casing"
        class="builder-edge__casing"
        :class="{
          'is-low-light': casing.light,
          'is-low-dark': casing.dark,
          'is-selected': selected,
        }"
        :points="pointsText"
        fill="none"
        :stroke-dasharray="data.dashArray"
        pointer-events="none" />
      <polyline
        class="builder-edge builder-line__stroke"
        :class="{ 'builder-edge--custom': color, 'is-selected': selected }"
        :style="color ? { '--bx-edge-color': color } : undefined"
        :points="pointsText"
        fill="none"
        :stroke-dasharray="data.dashArray"
        :marker-start="line.startArrow ? `url(#${arrowId})` : undefined"
        :marker-end="line.endArrow ? `url(#${arrowId})` : undefined"
        :data-start-arrow="line.startArrow ? 'true' : undefined"
        :data-end-arrow="line.endArrow ? 'true' : undefined"
        pointer-events="none" />
    </svg>
    <span
      v-if="label"
      class="builder-edge__label builder-line__label"
      :class="{ 'is-selected': selected }"
      :style="{
        transform: `translate(-50%, -50%) translate(${labelAt.x}px, ${labelAt.y}px)`,
      }">
      {{ label }}
    </span>
    <node-issue-mark v-if="data.issue" :node-id="id" :issue="data.issue" />
    <!-- In Vue Flow's label layer, over the nodes and outside the node's
         wrapper, which is a button and so holds nothing focusable. -->
    <Teleport v-if="editing && labelLayer" :to="labelLayer">
      <span
        v-for="(point, index) in points"
        :key="index"
        class="builder-line__point nodrag nopan"
        :class="{ 'is-end': index === 0 || index === points.length - 1 }"
        :style="{
          transform: `translate(-50%, -50%) translate(${origin.x + point.x}px, ${origin.y + point.y}px)`,
        }"
        :title="capitalize(linePointName(data.node, index))"
        :data-line="id"
        :data-point="index"
        :data-testid="`line-point-${index}`"
        tabindex="-1"
        aria-hidden="true"
        @pointerdown="startDrag(index, $event)"
        @pointermove="moveDrag"
        @pointerup="endDrag"
        @pointercancel="cancelDrag"
        @click.stop
        @dblclick.stop
        @keydown="onPointKey(index, $event)"></span>
    </Teleport>
  </div>
</template>

<script setup>
  import { computed, inject, nextTick, ref } from 'vue';

  import NodeIssueMark from './NodeIssueMark.vue';
  import { useCanvasEditing } from './canvasEditing.js';

  import { drawnColor, needsCasing } from '@/builder/colors.js';
  import {
    linePointKey,
    linePointName,
    nearestSegment,
    sizeOf,
    snappedLinePoint,
  } from '@/builder/model.js';
  import { capitalize } from '@/builder/text.js';

  // Vue Flow passes its node state as attributes as well; none belong on
  // the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  // The canvas's store, Vue Flow's nodes and its zoom; none outside the
  // canvas, where the line is only drawn.
  const canvas = useCanvasEditing();
  const root = ref(null);
  // Vue Flow's layer for edge labels, once the canvas has it (see
  // BuilderCanvas.vue), where the handles of the points go.
  const labelLayer = inject('builderEdgeLabels', null);
  // Where the line is on the canvas, as Vue Flow draws it: it follows the
  // node while it is dragged, before the document has its new position.
  const origin = computed(() => {
    const at =
      canvas?.flowNode(props.id)?.computedPosition || props.data.node.position;

    return { x: at.x, y: at.y };
  });

  const line = computed(() => props.data.node.line || {});
  // The points while one is dragged, then the document's.
  const draft = ref(null);
  const points = computed(() => draft.value || line.value.points || []);
  const editable = computed(() => Boolean(canvas) && !canvas.store.readOnly);
  const editing = computed(() => props.selected && editable.value);
  const box = computed(() => sizeOf(props.data.node));
  const pointsText = computed(() =>
    points.value.map((point) => `${point.x},${point.y}`).join(' '),
  );
  const arrowId = computed(() => `builder-line-arrow-${props.id}`);
  const label = computed(() => line.value.label || '');

  // A color that is not #rrggbb, which only an edited file can hold, is
  // drawn as none.
  const color = computed(() =>
    /^#[0-9a-f]{6}$/i.test(line.value.color || '')
      ? drawnColor(line.value.color)
      : '',
  );
  const casing = computed(() => {
    const low = color.value ? needsCasing(color.value) : null;

    return low && (low.light || low.dark) ? low : null;
  });

  // The middle of the middle segment, or of the first of the two middle
  // ones.
  const labelAt = computed(() => {
    const list = points.value;
    const index = Math.max(0, Math.floor((list.length - 2) / 2));
    const from = list[index] || { x: 0, y: 0 };
    const to = list[index + 1] || from;

    return { x: (from.x + to.x) / 2, y: (from.y + to.y) / 2 };
  });

  // A point relative to the line on the grid, while the diagram snaps to
  // it.
  function snapped(point) {
    return snappedLinePoint(props.data.node, point, canvas?.store.doc.grid);
  }

  // --- dragging a point ------------------------------------------------------

  let drag = null;

  function startDrag(index, event) {
    if (!editing.value || event.button !== 0) {
      return;
    }

    event.stopPropagation();
    event.preventDefault();
    event.currentTarget.setPointerCapture?.(event.pointerId);
    event.currentTarget.focus({ preventScroll: true });

    drag = {
      index,
      pointerId: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      from: points.value.map((point) => ({ ...point })),
    };
    draft.value = drag.from;
  }

  function moveDrag(event) {
    if (!drag || event.pointerId !== drag.pointerId) {
      return;
    }

    const zoom = canvas.zoom() || 1;
    const start = drag.from[drag.index];
    const moved = snapped({
      x: start.x + (event.clientX - drag.x) / zoom,
      y: start.y + (event.clientY - drag.y) / zoom,
    });

    draft.value = drag.from.map((point, at) =>
      at === drag.index ? moved : point,
    );
  }

  function endDrag(event) {
    if (!drag || event.pointerId !== drag.pointerId) {
      return;
    }

    const { index, from } = drag;
    const moved = draft.value;

    drag = null;

    if (moved[index].x !== from[index].x || moved[index].y !== from[index].y) {
      canvas.store.setLinePoints(
        props.id,
        moved,
        `Moved ${linePointName(props.data.node, index)}`,
      );
    }

    draft.value = null;
  }

  function cancelDrag() {
    drag = null;
    draft.value = null;
  }

  // --- bends -------------------------------------------------------------------

  // A double-click on the line adds a bend where it was made, in the
  // segment nearest to it.
  function addBend(event) {
    if (!editable.value) {
      return;
    }

    event.stopPropagation();

    const matrix = event.currentTarget.ownerSVGElement?.getScreenCTM();

    if (!matrix) {
      return;
    }

    const at = new DOMPoint(event.clientX, event.clientY).matrixTransform(
      matrix.inverse(),
    );

    canvas.store.insertLinePoint(
      props.id,
      nearestSegment(points.value, at),
      snapped(at),
    );
  }

  // --- a focused point ---------------------------------------------------------

  // The line's wrapper, which takes focus when no handle can.
  function wrapper() {
    return root.value?.closest('.vue-flow__node');
  }

  function focusPoint(index) {
    nextTick(() => {
      const handles = labelLayer?.value?.querySelectorAll(
        `.builder-line__point[data-line="${props.id}"]`,
      );
      const handle = handles?.[Math.min(index, handles.length - 1)];

      (handle || wrapper())?.focus({ preventScroll: true });
    });
  }

  // What each key does is linePointKey's (see model.js).
  function onPointKey(index, event) {
    if (!editing.value || drag) {
      return;
    }

    const pressed = linePointKey(
      props.data.node,
      index,
      event,
      canvas.store.doc.grid,
    );

    if (!pressed) {
      return;
    }

    event.preventDefault();
    event.stopPropagation();

    if (pressed.action === 'leave') {
      wrapper()?.focus({ preventScroll: true });

      return;
    }

    if (pressed.action === 'remove') {
      canvas.store.removeLinePoint(props.id, index);
    } else {
      canvas.store.moveLinePoint(props.id, index, pressed.point);
    }

    focusPoint(index);
  }
</script>
