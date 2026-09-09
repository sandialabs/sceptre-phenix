<!--
  Vue Flow canvas.

  The canvas is a *view* of the document: every gesture is translated into a
  pure model operation on the store, and the rendered graph is derived back
  from the document. Keyboard equivalents exist for every pointer gesture, and
  the semantic outline (BuilderOutline.vue) covers everything drag does.
-->
<template>
  <div
    ref="root"
    class="builder-canvas builder-panel"
    data-testid="builder-canvas"
    @keydown="onKeydown"
    @dragover.prevent
    @drop="onDrop">
    <VueFlow
      :nodes="flowNodes"
      :edges="flowEdges"
      :node-types="nodeTypes"
      :edge-types="edgeTypes"
      :default-viewport="{ x: 0, y: 0, zoom: 1 }"
      :min-zoom="0.2"
      :max-zoom="2"
      :snap-to-grid="snapToGrid"
      :snap-grid="snapGrid"
      :nodes-draggable="!store.readOnly"
      :nodes-connectable="!store.readOnly"
      :connect-on-click="true"
      :delete-key-code="null"
      :multi-selection-key-code="'Shift'"
      aria-label="Topology canvas. Use the outline panel for keyboard editing."
      @connect="onConnect"
      @node-drag-stop="onNodeDragStop"
      @nodes-change="onNodesChange"
      @edges-change="onEdgesChange"
      @pane-click="onPaneClick">
      <Background
        v-if="gridEnabled"
        :pattern-color="gridColor"
        :gap="gridSize"
        variant="lines"
        aria-hidden="true" />
      <Controls
        position="bottom-left"
        :show-interactive="false"
        role="group"
        aria-label="Canvas zoom controls">
        <template #icon-zoom-in>
          <span aria-hidden="true">+</span>
          <span class="builder-visually-hidden">Zoom in</span>
        </template>
        <template #icon-zoom-out>
          <span aria-hidden="true">-</span>
          <span class="builder-visually-hidden">Zoom out</span>
        </template>
        <template #icon-fit-view>
          <span aria-hidden="true">[]</span>
          <span class="builder-visually-hidden">Fit diagram to view</span>
        </template>
      </Controls>
      <MiniMap
        v-if="showMinimap"
        pannable
        zoomable
        aria-label="Diagram minimap"
        :node-color="minimapColor" />
    </VueFlow>
  </div>
</template>

<script setup>
  import { computed, onMounted, ref } from 'vue';
  import { VueFlow, useVueFlow } from '@vue-flow/core';
  import { Background } from '@vue-flow/background';
  import { Controls } from '@vue-flow/controls';
  import { MiniMap } from '@vue-flow/minimap';

  import '@vue-flow/core/dist/style.css';
  import '@vue-flow/core/dist/theme-default.css';
  import '@vue-flow/controls/dist/style.css';
  import '@vue-flow/minimap/dist/style.css';

  import DeviceNode from './nodes/DeviceNode.vue';
  import SwitchNode from './nodes/SwitchNode.vue';
  import NoteNode from './nodes/NoteNode.vue';
  import GroupNode from './nodes/GroupNode.vue';
  import NetworkEdge from './edges/NetworkEdge.vue';

  import { useBuilderStore } from '@/builder/store.js';
  import {
    absolutePosition,
    fromFlowConnection,
    toFlowEdges,
    toFlowNodes,
  } from '@/builder/adapters/vueflow.js';
  import { PALETTE_MIME } from './paletteDnd.js';

  const props = defineProps({
    showMinimap: { type: Boolean, default: true },
    reducedMotion: { type: Boolean, default: false },
  });

  const store = useBuilderStore();
  const root = ref(null);
  const { fitView, project, vueFlowRef } = useVueFlow();

  const nodeTypes = {
    builderDevice: DeviceNode,
    builderSwitch: SwitchNode,
    builderNote: NoteNode,
    builderGroup: GroupNode,
  };

  const edgeTypes = { builderNetwork: NetworkEdge };

  const flowNodes = computed(() =>
    toFlowNodes(store.doc, { selectedIds: store.selection.nodes }),
  );
  const flowEdges = computed(() =>
    toFlowEdges(store.doc, { selectedIds: store.selection.edges }),
  );

  const gridEnabled = computed(() => store.doc.grid?.enabled !== false);
  const gridSize = computed(() => store.doc.grid?.size || 16);
  const snapToGrid = computed(() => store.doc.grid?.snap !== false);
  const snapGrid = computed(() => [gridSize.value, gridSize.value]);

  const gridColor = computed(() =>
    store.resolvedTheme === 'dark' ? '#27303c' : '#dfe4ec',
  );

  function minimapColor(node) {
    return node?.type === 'builderSwitch' ? '#8a5300' : '#1f5fa9';
  }

  function onConnect(connection) {
    if (store.readOnly) {
      return;
    }

    store.connect(fromFlowConnection(connection));
  }

  // A drag of several nodes is one edit: it becomes a single history commit and
  // therefore a single server snapshot.
  function onNodeDragStop(event) {
    if (store.readOnly) {
      return;
    }

    const nodes = event.nodes || (event.node ? [event.node] : []);
    const moves = nodes
      .map((node) => {
        const model = store.doc.nodes.find((item) => item.id === node.id);

        return model
          ? {
              id: node.id,
              position: absolutePosition(
                store.doc,
                model.parentId,
                node.position,
              ),
            }
          : null;
      })
      .filter(Boolean);

    store.moveNodes(moves);
  }

  function selectedAfterChanges(current, changes) {
    const selected = new Set(current);

    for (const change of changes || []) {
      if (change.type !== 'select') {
        continue;
      }

      if (change.selected) {
        selected.add(change.id);
      } else {
        selected.delete(change.id);
      }
    }

    return [...selected];
  }

  function onNodesChange(changes) {
    store.select({
      nodes: selectedAfterChanges(store.selection.nodes, changes),
      edges: store.selection.edges,
    });
  }

  function onEdgesChange(changes) {
    store.select({
      nodes: store.selection.nodes,
      edges: selectedAfterChanges(store.selection.edges, changes),
    });
  }

  function onPaneClick() {
    store.clearSelection();
  }

  function onDrop(event) {
    event.preventDefault();

    if (store.readOnly) {
      return;
    }

    const kind = event.dataTransfer?.getData(PALETTE_MIME);

    if (!kind) {
      return;
    }

    const bounds = root.value?.getBoundingClientRect();
    const position = project
      ? project({
          x: event.clientX - (bounds?.left || 0),
          y: event.clientY - (bounds?.top || 0),
        })
      : { x: event.clientX, y: event.clientY };

    store.addNode({
      kind,
      position: { x: Math.round(position.x), y: Math.round(position.y) },
    });
  }

  function onKeydown(event) {
    if (store.readOnly) {
      return;
    }

    const meta = event.ctrlKey || event.metaKey;
    const step = event.shiftKey ? 1 : 10;

    if (event.key === 'Delete' || event.key === 'Backspace') {
      if (store.selection.nodes.length || store.selection.edges.length) {
        event.preventDefault();
        store.removeSelection();
      }
      return;
    }

    if (meta && event.key.toLowerCase() === 'c') {
      event.preventDefault();
      store.copy();
      return;
    }

    if (meta && event.key.toLowerCase() === 'v') {
      event.preventDefault();
      store.paste();
      return;
    }

    if (meta && event.key.toLowerCase() === 'd') {
      event.preventDefault();
      store.duplicate();
      return;
    }

    if (meta && event.key.toLowerCase() === 'a') {
      event.preventDefault();
      store.selectAll();
      return;
    }

    if (meta && event.key.toLowerCase() === 'z') {
      event.preventDefault();
      if (event.shiftKey) {
        store.redo();
      } else {
        store.undo();
      }
      return;
    }

    if (meta && event.key.toLowerCase() === 'y') {
      event.preventDefault();
      store.redo();
      return;
    }

    if (event.key.startsWith('Arrow') && store.selection.nodes.length) {
      const delta = {
        ArrowUp: { x: 0, y: -step },
        ArrowDown: { x: 0, y: step },
        ArrowLeft: { x: -step, y: 0 },
        ArrowRight: { x: step, y: 0 },
      }[event.key];

      if (!delta) {
        return;
      }

      event.preventDefault();
      store.nudgeSelection(delta.x, delta.y);
    }
  }

  /**
   * Element rasterized by PNG/SVG export: the Vue Flow viewport holds the
   * whole graph, including parts scrolled out of view.
   *
   * @returns {HTMLElement|null}
   */
  function viewportElement() {
    return (
      vueFlowRef.value?.querySelector('.vue-flow__viewport') ||
      root.value?.querySelector('.vue-flow__viewport') ||
      null
    );
  }

  onMounted(() => {
    if (store.doc.nodes.length) {
      fitView({ padding: 0.2, duration: props.reducedMotion ? 0 : 200 });
    }
  });

  defineExpose({ viewportElement, fitView });
</script>
