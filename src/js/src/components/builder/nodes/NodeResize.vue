<!--
  The mouse resize of a note, a group, a shape or an icon. It is Vue Flow's
  NodeResizer, shown while the node is selected and the draft can be
  changed. A resize is one edit, made when the drag ends (resizeNodeBox in
  store.js). From whichever side or corner the drag starts, the edit keeps a
  node at its least size and a group around its members. Alt+Shift and an
  arrow key resize a selected node from the keyboard (see
  BuilderCanvas.vue). The Inspector's Width and Height fields set the size
  of a shape or an icon.

  The handles are drawn in Vue Flow's edge label layer, over every node.
  They are in a frame that covers the node's box as Vue Flow draws it, also
  during a resize. Thus a selected shape stays under the devices and
  switches it is drawn around (see nodeZIndex in adapters/vueflow.js), and
  its handles stay where the pointer can reach them. NodeResizer works from
  the pointer's place on the canvas and the node's id, not from where its
  handles are in the page. Outside the node's wrapper, the frame stops a
  click on a handle from reaching the pane, which would clear the selection.

  The handles are for the pointer only. The keyboard and the Inspector are
  their equivalents, so nothing here takes focus or is announced. Drawn
  without the canvas (see canvasEditing.js), the node has no handles.
  While the handles show, the node hides its selection check mark, which
  the top right handle covers (see useResizeFrame in canvasEditing.js).
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
  import { builderSettings } from '@/builder/settings.js';

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
  // have. A group's least size holds its members' notes while the canvas
  // shows them.
  const least = computed(() => {
    const min = minimumSize(canvas?.store.doc, props.id, {
      showNotes: builderSettings.showNodeNotes,
    });
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
