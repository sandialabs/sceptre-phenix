<!--
  The mouse resize of a note, group, shape or icon. Handles go in the edge label
  layer, so a shape stays under devices. The frame stops a handle click from
  clearing the selection.
-->
<template>
  <Teleport v-if="visible && labelLayer" :to="labelLayer">
    <div
      class="builder-resizer nopan"
      :style="frame"
      :data-node-id="id"
      @click.stop
      @dblclick.stop>
      <node-resizer
        :node-id="id"
        :min-width="least.width"
        :min-height="least.height"
        :keep-aspect-ratio="keepAspectRatio"
        handle-class-name="builder-resizer__handle"
        line-class-name="builder-resizer__line"
        @resize-start="onStart"
        @resize-end="onEnd" />
    </div>
  </Teleport>
</template>

<script setup>
  import { computed, inject } from 'vue';
  import { NodeResizer } from '@vue-flow/node-resizer';

  import '@vue-flow/node-resizer/dist/style.css';

  import { useCanvasEditing, useResizeFrame } from './canvasEditing.js';

  import { minimumSize, sizeOf } from '@/builder/model.js';

  const props = defineProps({
    id: { type: String, required: true },
    // The document's node, whose position is absolute.
    node: { type: Object, required: true },
    selected: { type: Boolean, default: false },
    // A circle keeps its shape while it is resized from a corner.
    keepAspectRatio: { type: Boolean, default: false },
  });

  const canvas = useCanvasEditing();
  // Vue Flow's layer for edge labels, once the canvas has it (see
  // BuilderCanvas.vue).
  const labelLayer = inject('builderEdgeLabels', null);

  const visible = useResizeFrame(() => props.selected);

  // The node's box as Vue Flow draws it, on the canvas: it follows the node
  // while it is dragged or resized, before the document has its new box.
  const frame = computed(() => {
    const flowNode = canvas?.flowNode(props.id);
    const at = flowNode?.computedPosition || props.node.position;
    const size = sizeOf(props.node);
    const width = flowNode?.dimensions?.width || size.width;
    const height = flowNode?.dimensions?.height || size.height;

    return {
      transform: `translate(${at.x}px, ${at.y}px)`,
      width: `${width}px`,
      height: `${height}px`,
    };
  });

  // The least size, but never more than the node's size. NodeResizer grows a
  // node that is smaller than its least size as soon as the resizer shows.
  // The canvas would then draw the node at a size the document does not
  // have.
  const least = computed(() => {
    const min = minimumSize(canvas?.store.doc, props.id);
    const size = sizeOf(props.node);

    return {
      width: Math.min(min.width, size.width),
      height: Math.min(min.height, size.height),
    };
  });

  // Where the node was when the drag started: on the canvas (Vue Flow's
  // position, relative to its group) and in the document (absolute).
  let start = null;

  function onStart({ params }) {
    start = {
      flow: { x: params.x, y: params.y },
      position: { ...props.node.position },
      size: sizeOf(props.node),
    };
  }

  function onEnd({ params }) {
    const from = start;

    start = null;

    if (!from || !canvas) {
      return;
    }

    const entry = canvas.store.resizeNodeBox(props.id, {
      x: from.position.x + params.x - from.flow.x,
      y: from.position.y + params.y - from.flow.y,
      width: params.width,
      height: params.height,
    });

    // Nothing changed, such as a group dragged smaller than its members and
    // back: the canvas draws the node as the document has it again.
    const flowNode = entry ? null : canvas.flowNode(props.id);

    if (flowNode) {
      flowNode.position = { ...from.flow };
      flowNode.style = {
        ...flowNode.style,
        width: `${from.size.width}px`,
        height: `${from.size.height}px`,
      };
    }
  }
</script>
