<!--
  Shape node: a rectangle or a circle drawn on the canvas, with no phenix
  semantics and never published. The figure fills the node's box (a circle
  in a box that is not square is an ellipse), in the fill, outline and
  border pattern chosen for it, solid without one, and its label is at its
  center. Vue Flow's wrapper is the focusable, named element (see
  DeviceNode.vue); the name says the figure, which only the canvas shows
  otherwise.

  On a fill the label is black or white, whichever reads on it (see
  nodeColors in colors.js). Without a fill the shape is see-through, so it
  frames what it is drawn around: groups lie under it, and devices and
  switches over it, also while it is selected (see nodeZIndex in
  adapters/vueflow.js). Its resize handles are drawn over every node (see
  NodeResize.vue).
-->
<template>
  <div
    class="builder-node builder-node--shape"
    :class="[
      { 'is-selected': selected, 'has-resize-frame': framed },
      colorClasses,
    ]"
    :style="colorStyle"
    :data-shape="figure"
    :data-border="border"
    :data-node-id="id"
    data-node-kind="shape"
    data-testid="builder-node">
    <span v-if="label" class="builder-node__label">{{ label }}</span>
    <node-issue-mark v-if="data.issue" :node-id="id" :issue="data.issue" />
    <node-resize
      :id="id"
      :node="data.node"
      :selected="selected"
      :keep-aspect-ratio="figure === 'circle'" />
  </div>
</template>

<script setup>
  import { computed } from 'vue';

  import NodeIssueMark from './NodeIssueMark.vue';
  import { useResizeFrame } from './canvasEditing.js';
  import NodeResize from './NodeResize.vue';
  import { useNodeColors } from './nodeColors.js';

  import { BORDER_STYLES } from '@/builder/model.js';

  // Vue Flow passes its node state as attributes as well; none belong on
  // the node's element.
  defineOptions({ inheritAttrs: false });

  const props = defineProps({
    id: { type: String, required: true },
    data: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  });

  const shape = computed(() => props.data.node.shape || {});
  const figure = computed(() =>
    shape.value.shape === 'circle' ? 'circle' : 'rectangle',
  );
  const label = computed(() => shape.value.label || '');
  // A pattern the editor does not know, which only an edited file can
  // hold, is drawn as none: solid.
  const border = computed(() =>
    BORDER_STYLES.includes(shape.value.borderStyle)
      ? shape.value.borderStyle
      : undefined,
  );
  const { colorClasses, colorStyle } = useNodeColors(() => shape.value);
  const framed = useResizeFrame(() => props.selected);
</script>
