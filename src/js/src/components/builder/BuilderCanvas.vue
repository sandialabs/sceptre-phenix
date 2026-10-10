<!--
  Vue Flow canvas.

  The canvas is a *view* of the document: every gesture is translated into a
  pure model operation on the store, and the rendered graph is derived back
  from the document. Keyboard equivalents exist for every pointer gesture, and
  the semantic outline (BuilderOutline.vue) covers everything drag does.

  The diagram is one Tab stop (a roving tabindex): the node or connection
  that last had focus, or else the canvas itself. The arrow keys move focus
  to the nearest node in their direction, and Page Down and Page Up through
  a node's connections; Shift with an arrow key moves the selected nodes.
  Each node and connection is Vue Flow's wrapper element, named and
  described by adapters/vueflow.js. Vue Flow's own keyboard layer is off;
  this component handles the keys, so the hints it gives are the keys that
  work.

  Accepted deviation, as in BuilderOutlineList.vue: the nodes are not each
  a Tab stop, and the canvas has no composite role. In screen reader
  browse mode (NVDA, JAWS) the arrow keys and Page Down move the virtual
  cursor, not the focus, so Tab leaves the canvas. The canvas's description
  says to turn on the screen reader's focus mode for these keys, or to use
  the Outline, which lists every node and connection. Activating a node in
  browse mode (Enter) selects and focuses it.

  A node or connection pressed into the selection (a click, Enter or Space)
  shows the Inspector again when it is hidden (see BuilderPanes.vue); a
  press on the one selected item then shows it rather than deselecting it.
  Moving focus alone does not.

  The minimap has a handle at its top left corner that resizes it, as a
  splitter between it and the canvas (WAI-ARIA APG window splitter): it is
  dragged, or focused and moved with the arrow keys, and Home and End make
  the minimap smallest and largest; Enter or a double-click restores the
  default size. The size is remembered in this browser (see
  builder/panes.js), and Reset view forgets it.
-->
<template>
  <section
    id="builder-canvas"
    ref="root"
    class="builder-canvas builder-panel"
    :class="{ 'is-resizing-minimap': Boolean(minimapDrag) }"
    data-testid="builder-canvas"
    aria-labelledby="builder-canvas-title"
    aria-describedby="builder-canvas-help-summary"
    :tabindex="tabStop ? -1 : 0"
    @keydown="onKeydown"
    @focusin="onFocusIn"
    @pointerdown.capture="startGesture"
    @click.capture="startClick"
    @click="endGesture"
    @dragover.prevent
    @drop="onDrop">
    <h2 id="builder-canvas-title" class="builder-visually-hidden">
      Diagram canvas
    </h2>

    <!-- Why a gesture did nothing. The live region has announced it already;
         it stays until dismissed or the next connection attempt. -->
    <div
      v-if="store.notice"
      :key="store.notice.seq"
      class="builder-canvas__notice"
      data-testid="canvas-notice">
      <p class="builder-canvas__notice-text">{{ store.notice.text }}</p>
      <button
        type="button"
        class="builder-canvas__notice-close"
        aria-label="Dismiss message"
        title="Dismiss message"
        data-testid="canvas-notice-dismiss"
        @click="dismissNotice">
        <span aria-hidden="true">&times;</span>
      </button>
    </div>

    <p :id="NODE_HINT_ID" hidden>{{ hints.node }}</p>
    <p :id="EDGE_HINT_ID" hidden>{{ hints.edge }}</p>
    <!-- The canvas's description: its keys, the advice for screen readers,
         and where every shortcut is listed. -->
    <p id="builder-canvas-help-summary" hidden>{{ hints.canvas }}</p>
    <p :id="MINIMAP_HINT_ID" hidden>
      Up and Left arrows make the minimap larger, Down and Right smaller, Home
      and End make it smallest and largest, and Enter restores its default size.
    </p>

    <!-- The nodes and connections are handed to Vue Flow as they change
         (see syncNodes), not bound here. A selected node is lifted over the
         rest by its zIndex (see nodeZIndex in adapters/vueflow.js), not by
         Vue Flow, which would lift a selected shape or line over the
         devices it is drawn around. -->
    <VueFlow
      class="builder-canvas__flow"
      :node-types="nodeTypes"
      :edge-types="edgeTypes"
      :default-viewport="openViewport"
      :min-zoom="minZoom"
      :max-zoom="MAX_ZOOM"
      :snap-to-grid="snapToGrid"
      :snap-grid="snapGrid"
      :nodes-draggable="!store.readOnly"
      :nodes-connectable="!store.readOnly"
      :connect-on-click="false"
      :is-valid-connection="isValidConnection"
      :connection-mode="ConnectionMode.Loose"
      :connection-radius="30"
      :delete-key-code="null"
      :multi-selection-key-code="'Shift'"
      :disable-keyboard-a11y="true"
      :nodes-focusable="false"
      :edges-focusable="false"
      :elevate-nodes-on-select="false"
      @connect="onConnect"
      @connect-start="onConnectStart"
      @connect-end="onConnectEnd"
      @node-drag-start="hideNodeTip"
      @node-drag-stop="onNodeDragStop"
      @nodes-change="onNodesChange"
      @edges-change="onEdgesChange"
      @node-click="onNodeClick"
      @edge-click="onEdgeClick"
      @pane-click="onPaneClick"
      @nodes-initialized="onNodesInitialized">
      <Background
        v-if="gridEnabled"
        :pattern-color="gridColor"
        :gap="gridSize"
        variant="lines"
        aria-hidden="true" />
      <!-- The zoom buttons stay focusable at their limits (aria-disabled),
           so focus does not fall to the page when one stops applying. Each
           shows its name in a tooltip on hover and focus, with the keys
           that do the same on the canvas. Those keys do nothing on the
           buttons themselves, so the buttons have no aria-keyshortcuts.
           The third fits the whole diagram into view, which pans as well
           as zooms; it then restores the view from before, until the view
           changes some other way (see toggleFit). -->
      <Controls
        position="bottom-left"
        :show-interactive="false"
        role="group"
        aria-label="Canvas zoom controls">
        <template #control-zoom-in>
          <ControlButton
            class="vue-flow__controls-zoomin"
            :aria-disabled="atMaxZoom ? 'true' : undefined"
            v-on="zoomTip('Zoom in', 'view.zoomIn')"
            @click="atMaxZoom || zoomIn()">
            <span aria-hidden="true">+</span>
            <span class="builder-visually-hidden">Zoom in</span>
          </ControlButton>
        </template>
        <template #control-zoom-out>
          <ControlButton
            class="vue-flow__controls-zoomout"
            :aria-disabled="atMinZoom ? 'true' : undefined"
            v-on="zoomTip('Zoom out', 'view.zoomOut')"
            @click="atMinZoom || zoomOut()">
            <span aria-hidden="true">&minus;</span>
            <span class="builder-visually-hidden">Zoom out</span>
          </ControlButton>
        </template>
        <template #control-fit-view>
          <ControlButton
            class="vue-flow__controls-fitview"
            v-on="FIT_TIP_EVENTS"
            @click="toggleFit()">
            <builder-icon
              class="builder-canvas__fit-icon"
              :name="fitRestores ? 'fit-view-restore' : 'fit-view'"
              :size="14" />
            <span class="builder-visually-hidden">{{ fitName }}</span>
          </ControlButton>
        </template>
      </Controls>
      <MiniMap
        v-if="showMinimap"
        :id="MINIMAP_ID"
        pannable
        zoomable
        aria-label="Diagram minimap"
        :width="minimap.width"
        :height="minimap.height"
        :node-color="minimapColor"
        :node-class-name="minimapClass" />
      <!-- Over the minimap's top left corner. Its name, like the zoom
           buttons', is its tooltip too. -->
      <div
        v-if="showMinimap"
        v-bind="minimapHandle"
        v-on="MINIMAP_HANDLE_EVENTS">
        <builder-icon name="resize-diagonal" :size="14" />
      </div>
    </VueFlow>

    <builder-fixed-tooltip :tooltip="tooltip" testid="zoom-tooltip" />
    <builder-node-tooltip :tooltip="nodeTip" />
  </section>
</template>

<script setup>
  import {
    computed,
    inject,
    markRaw,
    nextTick,
    onMounted,
    provide,
    ref,
    shallowRef,
    watch,
  } from 'vue';
  import {
    ConnectionMode,
    VueFlow,
    getTransformForBounds,
    useVueFlow,
  } from '@vue-flow/core';
  import { Background } from '@vue-flow/background';
  import { ControlButton, Controls } from '@vue-flow/controls';
  import { MiniMap } from '@vue-flow/minimap';

  import '@vue-flow/core/dist/style.css';
  import '@vue-flow/core/dist/theme-default.css';
  import '@vue-flow/controls/dist/style.css';
  import '@vue-flow/minimap/dist/style.css';

  import DeviceNode from './nodes/DeviceNode.vue';
  import SwitchNode from './nodes/SwitchNode.vue';
  import NoteNode from './nodes/NoteNode.vue';
  import GroupNode from './nodes/GroupNode.vue';
  import ShapeNode from './nodes/ShapeNode.vue';
  import IconNode from './nodes/IconNode.vue';
  import LineNode from './nodes/LineNode.vue';
  import NetworkEdge from './edges/NetworkEdge.vue';
  import BuilderFixedTooltip from './BuilderFixedTooltip.vue';
  import BuilderIcon from './BuilderIcon.vue';
  import BuilderNodeTooltip from './BuilderNodeTooltip.vue';
  import { useFixedTooltip } from './fixedTooltip.js';
  import { CANVAS_EDITING } from './nodes/canvasEditing.js';
  import { NODE_TIP } from './nodes/nodeTooltip.js';

  import { canvasHints, focusLost, shortcutLabel } from '@/builder/commands.js';
  import { iconLibrary } from '@/builder/iconLibrary.js';
  import { nodeIssueSummaries } from '@/builder/issues.js';
  import {
    boundsOf,
    canConnect,
    findEdge,
    findNode,
    keyResizedSize,
    nodeLabel,
    sizeOf,
  } from '@/builder/model.js';
  import {
    MINIMAP_DEFAULT_WIDTH,
    clampPane,
    loadMinimap,
    minimapDragWidth,
    minimapKeyWidth,
    minimapLimits,
    minimapSize,
    saveMinimap,
  } from '@/builder/panes.js';
  import { pressSelection, selectionItemName } from '@/builder/selection.js';
  import { builderSettings } from '@/builder/settings.js';
  import { keepUnchanged } from '@/builder/stable.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    EDGE_HINT_ID,
    NODE_HINT_ID,
    absolutePosition,
    connectionStep,
    fitPadding,
    fitZoom,
    flowChanges,
    freeSpot,
    fromFlowConnection,
    hiddenArea,
    itemPoint,
    keepFit,
    keptFit,
    nearestNode,
    nodeInDirection,
    toFlowEdges,
    toFlowNodes,
    withSelection,
    withTabStop,
    zoomFloor,
  } from '@/builder/adapters/vueflow.js';
  import {
    PALETTE_MIME,
    PALETTE_TEMPLATE_MIME,
    paletteNode,
  } from './paletteDnd.js';

  const props = defineProps({
    showMinimap: { type: Boolean, default: true },
    reducedMotion: { type: Boolean, default: false },
  });

  // The least zoom, unless a diagram needs less to be seen whole (see
  // minZoom).
  const MIN_ZOOM = 0.2;
  const MAX_ZOOM = 2;
  // The zoom and pan the canvas opens with, unless the settings ask for a
  // fit: 100% from the diagram's origin, or the settings' own percentage.
  const START_VIEWPORT = { x: 0, y: 0, zoom: 1 };

  function startViewport() {
    return builderSettings.openZoom === 'custom'
      ? { x: 0, y: 0, zoom: builderSettings.openZoomPercent / 100 }
      : START_VIEWPORT;
  }

  // What this canvas opened with. Reset view reads the settings again.
  const openViewport = startViewport();

  const store = useBuilderStore();
  const root = ref(null);
  const {
    addEdges,
    addNodes,
    d3Zoom,
    dimensions,
    findEdge: findFlowEdge,
    findNode: findFlowNode,
    fitView,
    nodes: graphNodes,
    project,
    removeEdges,
    setEdges,
    setNodes,
    setViewport,
    updateNode: updateFlowNode,
    viewport,
    vueFlowRef,
    zoomIn,
    zoomOut,
  } = useVueFlow();

  // A double-click on the empty canvas zooms in, as d3-zoom does it: over
  // 250ms, or at once with reduced motion, as every other view change.
  watch(
    [d3Zoom, () => props.reducedMotion],
    ([zoom, reduced]) => zoom?.duration(reduced ? 0 : 250),
    { immediate: true },
  );

  // What the keys do on a focused node or connection, and a summary for the
  // canvas itself; a read-only draft can only be selected. Both follow the
  // platform and the user's keys (see commands.js).
  const hints = computed(() => canvasHints({ readOnly: store.readOnly }));

  // The side columns (BuilderPanes.vue), for showing the Inspector.
  const panes = inject('builderPanes', null);

  // --- the minimap's size ----------------------------------------------------

  const MINIMAP_ID = 'builder-minimap';
  const MINIMAP_HINT_ID = 'builder-minimap-hint';
  // Vue Flow keeps its panels 15px in from the pane's edges; the minimap has
  // a 1px frame (builder.css).
  const PANEL_MARGIN = 15;
  const MINIMAP_FRAME = 1;

  // The width this viewer chose, or null for the default.
  const minimapChosen = ref(loadMinimap());
  const minimapRange = computed(() => minimapLimits(dimensions.value));
  // As drawn: the chosen width within what the pane allows now.
  const minimap = computed(() =>
    minimapSize(
      clampPane(
        minimapChosen.value ?? MINIMAP_DEFAULT_WIDTH,
        minimapRange.value,
      ),
    ),
  );
  // Where the minimap is on the pane, in the pane's own pixels.
  const minimapBox = computed(() => {
    const { width, height } = dimensions.value;
    const right = width - PANEL_MARGIN;
    const bottom = height - PANEL_MARGIN;

    return {
      left: right - minimap.value.width - 2 * MINIMAP_FRAME,
      top: bottom - minimap.value.height - 2 * MINIMAP_FRAME,
      right,
      bottom,
    };
  });
  const minimapDrag = ref(null);

  const minimapHandle = computed(() => {
    const { width, height } = minimap.value;

    return {
      class: 'builder-minimap-handle nopan nowheel',
      role: 'separator',
      tabindex: 0,
      'aria-label': 'Resize minimap',
      'aria-controls': MINIMAP_ID,
      'aria-valuenow': width,
      'aria-valuemin': minimapRange.value.min,
      'aria-valuemax': minimapRange.value.max,
      'aria-valuetext': `${width} by ${height} pixels`,
      'aria-describedby': MINIMAP_HINT_ID,
      'data-testid': 'minimap-resize',
      style: {
        '--builder-minimap-width': `${width}px`,
        '--builder-minimap-height': `${height}px`,
      },
    };
  });

  // The handle moves with the minimap's corner, so its tooltip goes rather
  // than stay behind.
  function setMinimapWidth(width) {
    hideTip();
    minimapChosen.value = width;
    saveMinimap(width);
  }

  // Said after a key press; the minimap's size is its handle's value too.
  function announceMinimap() {
    const { width, height } = minimap.value;
    const { min, max } = minimapRange.value;
    const limit =
      (width >= max && ' (largest)') || (width <= min && ' (smallest)') || '';

    store.announce(`Minimap ${width} by ${height} pixels${limit}.`, {
      slot: 'minimap',
    });
  }

  function onMinimapKeydown(event) {
    if (event.key === 'Escape' && minimapDrag.value) {
      event.preventDefault();
      cancelMinimapDrag();
      return;
    }

    if (event.key === 'Enter') {
      event.preventDefault();
      setMinimapWidth(null);
      announceMinimap();
      return;
    }

    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) {
      return;
    }

    const width = minimapKeyWidth(
      event.key,
      minimap.value.width,
      minimapRange.value,
    );

    if (width === undefined) {
      return;
    }

    event.preventDefault();
    setMinimapWidth(width);
    announceMinimap();
  }

  // A drag keeps the pointer, so it goes on over the canvas; it is stored
  // when it ends, and Escape puts the size back.
  function onMinimapPointerDown(event) {
    if (event.button !== 0 || minimapDrag.value) {
      return;
    }

    event.preventDefault();
    event.currentTarget.focus({ preventScroll: true });
    event.currentTarget.setPointerCapture?.(event.pointerId);
    minimapDrag.value = {
      pointerId: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      width: minimap.value.width,
      before: minimapChosen.value,
      moved: false,
    };
  }

  function onMinimapPointerMove(event) {
    const drag = minimapDrag.value;

    if (!drag || event.pointerId !== drag.pointerId) {
      return;
    }

    const dx = event.clientX - drag.x;
    const dy = event.clientY - drag.y;

    if (!drag.moved && Math.hypot(dx, dy) < 1) {
      return;
    }

    drag.moved = true;
    minimapChosen.value = minimapDragWidth(
      drag.width,
      dx,
      dy,
      minimapRange.value,
    );
  }

  function endMinimapDrag(event) {
    const drag = minimapDrag.value;

    if (!drag || (event && event.pointerId !== drag.pointerId)) {
      return;
    }

    minimapDrag.value = null;

    if (drag.moved) {
      saveMinimap(minimapChosen.value);
    }
  }

  function cancelMinimapDrag() {
    const drag = minimapDrag.value;

    if (drag) {
      minimapDrag.value = null;
      minimapChosen.value = drag.before;
    }
  }

  // Reset view: the default size, which is no longer stored.
  function resetMinimap() {
    minimapDrag.value = null;
    setMinimapWidth(null);
  }

  // For the view's commands, which offer the handle's sizes.
  const minimapState = computed(() => ({
    width: minimap.value.width,
    ...minimapRange.value,
  }));

  function resizeMinimap(width) {
    setMinimapWidth(clampPane(width, minimapRange.value));
    announceMinimap();
  }

  // Below MIN_ZOOM when that is what it takes to show the whole diagram in
  // the pane, clear of the minimap, as Fit does.
  const diagramFloor = computed(() =>
    zoomFloor(
      boundsOf(store.doc.nodes || []),
      dimensions.value,
      MIN_ZOOM,
      props.showMinimap ? [minimapBox.value] : [],
    ),
  );
  // A floor that rises past the zoom (a larger window, a smaller diagram)
  // waits there: zooming out never zooms in.
  const minZoom = computed(() =>
    Math.min(diagramFloor.value, viewport.value.zoom),
  );

  const atMaxZoom = computed(() => viewport.value.zoom >= MAX_ZOOM);
  const atMinZoom = computed(() => viewport.value.zoom <= minZoom.value);

  // The zoom buttons' names and keys, as tooltips (WCAG 1.4.13); the
  // palette's entries show theirs the same way. The keys work on the
  // canvas, its nodes and connections, not on the buttons, and say so.
  const tooltip = useFixedTooltip();
  const { tip, showTip, hideTip } = tooltip;

  // The control whose tooltip was shown last, and the text it showed, so a
  // tooltip can follow its button's name (see the Fit button's).
  let shownTip = null;

  function tipEvents(tipText) {
    return tooltip.tipEvents(tipText, {
      onShow: (event, text) => {
        shownTip = { target: event.currentTarget, tipText, text };
      },
    });
  }

  // The name is a function for a button whose name changes.
  function zoomTip(name, command) {
    return tipEvents(() => {
      const text = typeof name === 'function' ? name() : name;
      const keys = shortcutLabel(command);

      return keys ? `${text} (${keys} on the canvas)` : text;
    });
  }

  // The info tooltip of the devices and switches (see nodes/nodeTooltip.js
  // and BuilderNodeTooltip.vue): one for the whole canvas, above its node,
  // clear of the node's focus ring and corner marks at any zoom. It waits
  // for the pointer to rest on a node, as the pointer crosses nodes on its
  // way elsewhere; keyboard focus shows it at once. Escape pressed on its
  // node only closes it, and leaves the selection as it is.
  const NODE_TIP_DELAY_MS = 400;
  const NODE_TIP_GAP = 10;
  const nodeTip = useFixedTooltip({
    side: 'above',
    delay: NODE_TIP_DELAY_MS,
    stopEscape: true,
    gap: () => Math.max(4, NODE_TIP_GAP * viewport.value.zoom),
  });

  provide(NODE_TIP, nodeTip);

  // A drag, of a node or of a new connection, is done with the tooltip.
  function hideNodeTip() {
    nodeTip.hideTip();
  }

  const MINIMAP_HANDLE_EVENTS = {
    ...tipEvents(() => 'Resize minimap'),
    keydown: onMinimapKeydown,
    pointerdown: onMinimapPointerDown,
    pointermove: onMinimapPointerMove,
    pointerup: endMinimapDrag,
    lostpointercapture: endMinimapDrag,
    pointercancel: cancelMinimapDrag,
    dblclick: () => setMinimapWidth(null),
  };

  // Components, never made reactive: Vue Flow would otherwise wrap them.
  const nodeTypes = markRaw({
    builderDevice: markRaw(DeviceNode),
    builderSwitch: markRaw(SwitchNode),
    builderNote: markRaw(NoteNode),
    builderGroup: markRaw(GroupNode),
    builderShape: markRaw(ShapeNode),
    builderIcon: markRaw(IconNode),
    builderLine: markRaw(LineNode),
  });

  const edgeTypes = markRaw({ builderNetwork: markRaw(NetworkEdge) });

  // Vue Flow's layer for edge labels, looked up once for all the edges
  // (see NetworkEdge.vue). It is drawn with the canvas.
  const labelLayer = shallowRef(null);

  provide('builderEdgeLabels', labelLayer);

  // What the nodes edited with the pointer need (see nodes/canvasEditing.js):
  // the store, Vue Flow's node of an id and the zoom.
  provide(CANVAS_EDITING, {
    store,
    flowNode: (id) => findFlowNode(id),
    zoom: () => viewport.value.zoom,
  });

  onMounted(() => {
    labelLayer.value =
      root.value?.querySelector('.vue-flow__edge-labels') || null;
  });

  // What the diagram checks found about each node, which marks it.
  const nodeIssues = computed(() =>
    nodeIssueSummaries(store.doc, store.issues),
  );

  // The canvas's one Tab stop: the node or connection that last had focus,
  // while the diagram still has it, or else (null) the canvas itself. The
  // others take focus from the arrow keys, Page Down and Page Up, a click
  // or Go to node, but not from Tab.
  const lastFocused = shallowRef(null);
  const tabStop = computed(() => {
    const item = lastFocused.value;
    const list = item?.kind === 'edges' ? store.doc.edges : store.doc.nodes;

    return item && (list || []).some((entry) => entry.id === item.id)
      ? item
      : null;
  });
  const tabStopOf = (kind) =>
    tabStop.value?.kind === kind ? tabStop.value.id : null;

  // The graph follows the document. An edit changes only the nodes and
  // connections it touches, the selection only those it selects or
  // deselects, and a new Tab stop only the old and the new one: the rest
  // stay the same objects (see keepUnchanged, withSelection and
  // withTabStop). Custom icons resolve to the diagram's copies, else to the
  // server's icon library, whose reactive state the graph follows too.
  const baseNodes = computed((previous) =>
    keepUnchanged(
      toFlowNodes(store.doc, {
        issues: nodeIssues.value,
        library: iconLibrary,
      }),
      previous,
    ),
  );
  const baseEdges = computed((previous) =>
    keepUnchanged(toFlowEdges(store.doc), previous),
  );
  const flowNodes = computed((previous) =>
    keepUnchanged(
      withTabStop(
        withSelection(baseNodes.value, store.selection.nodes),
        tabStopOf('nodes'),
      ),
      previous,
    ),
  );
  const flowEdges = computed((previous) =>
    keepUnchanged(
      withTabStop(
        withSelection(baseEdges.value, store.selection.edges),
        tabStopOf('edges'),
      ),
      previous,
    ),
  );

  // Vue Flow is handed only what changed, so a click or an edit on a large
  // diagram redraws only what it changed. Given a whole list, Vue Flow
  // remakes every item, and finds each node's group by a search of all
  // the nodes. Nodes come before the connections that end at them.
  watch(
    [flowNodes, flowEdges],
    ([nodes, edges], [nodesBefore, edgesBefore]) => {
      syncNodes(nodes, nodesBefore);
      syncEdges(edges, edgesBefore);
      // A node moved or removed takes its tooltip with it.
      nodeTip.follow();
    },
    { immediate: true },
  );

  // A node that moved into or out of a group, or one Vue Flow does not
  // have, takes the whole list.
  function syncNodes(nodes, before) {
    const changes =
      before && flowChanges(before, nodes, ['type', 'parentNode']);

    if (
      !changes ||
      changes.rebuild ||
      changes.changed.some((item) => !findFlowNode(item.id))
    ) {
      setNodes(nodes);

      return;
    }

    changes.changed.forEach((item) => updateFlowNode(item.id, item));

    // Vue Flow grows a group while a member is dragged past its edge. Once
    // the member lands, the group is drawn as the document has it again.
    const groups = new Set(changes.changed.map((item) => item.parentNode));

    nodes
      .filter((item) => groups.has(item.id))
      .forEach((item) => updateFlowNode(item.id, item));

    if (changes.added.length) {
      addNodes(changes.added);
    }

    // Vue Flow draws its nodes, and Tab reaches them, in its list's order.
    if (changes.removed.length || changes.reordered) {
      graphNodes.value = nodes
        .map((item) => findFlowNode(item.id))
        .filter(Boolean);
    }
  }

  // A connection that changed its ends, or one Vue Flow does not have,
  // takes the whole list, as does a new order.
  function syncEdges(edges, before) {
    const changes =
      before &&
      flowChanges(before, edges, [
        'source',
        'target',
        'sourceHandle',
        'targetHandle',
      ]);

    if (
      !changes ||
      changes.rebuild ||
      changes.reordered ||
      changes.changed.some((item) => !findFlowEdge(item.id))
    ) {
      setEdges(edges);

      return;
    }

    changes.changed.forEach((item) =>
      Object.assign(findFlowEdge(item.id), item),
    );

    if (changes.removed.length) {
      removeEdges(changes.removed);
    }

    if (changes.added.length) {
      addEdges(changes.added);
    }
  }

  const gridEnabled = computed(() => store.doc.grid?.enabled !== false);
  const gridSize = computed(() => store.doc.grid?.size || 16);
  const snapToGrid = computed(() => store.doc.grid?.snap !== false);
  const snapGrid = computed(() => [gridSize.value, gridSize.value]);

  const gridColor = computed(() =>
    store.resolvedTheme === 'dark' ? '#27303c' : '#dfe4ec',
  );

  // The minimap is drawn outside the theme's custom properties, so its colors
  // follow the resolved theme here: --bx-net-0 and --bx-warning, which keep
  // 3:1 against the minimap in both themes. Switches are also rounded (see
  // builder.css), so the two kinds differ by shape as well. Shapes, icons
  // and lines are drawings, in the theme's muted text color, so they do not
  // read as devices.
  const MINIMAP_COLORS = {
    light: { device: '#1f5fa9', switch: '#8a5300', drawing: '#5b6575' },
    dark: { device: '#7fb2f0', switch: '#e5b567', drawing: '#a3adbb' },
  };
  const DRAWING_TYPES = ['builderShape', 'builderIcon', 'builderLine'];

  function minimapColor(node) {
    const colors =
      MINIMAP_COLORS[store.resolvedTheme === 'dark' ? 'dark' : 'light'];

    if (DRAWING_TYPES.includes(node?.type)) {
      return colors.drawing;
    }

    return node?.type === 'builderSwitch' ? colors.switch : colors.device;
  }

  function minimapClass(node) {
    return node?.type === 'builderSwitch' ? 'builder-minimap-switch' : '';
  }

  function dismissNotice() {
    store.dismissNotice();
    // The button that had focus is gone; keep focus in the canvas.
    root.value?.focus();
  }

  // Drag-to-connect works between any devices and switches, in either
  // direction, like the legacy Builder. Vue Flow reports a connection only
  // when the drag ends on a handle; ending anywhere else on a node connects to
  // that node on a new interface (see onConnectEnd).
  let dragStart = null;
  let connectedThisDrag = false;

  // Refused targets (a used interface, two switches, notes, groups, the node
  // itself) never look valid while dragging. Releasing on one of them falls
  // through to the node-body rule below.
  //
  // Vue Flow also runs this check on every edge it draws. An existing edge has
  // an id and already holds its interface, so canConnect() would refuse it and
  // the edge would vanish; only a connection being dragged has no id.
  function isValidConnection(connection) {
    if (connection.id) {
      return true;
    }

    return canConnect(store.doc, fromFlowConnection(connection)).valid;
  }

  function onConnectStart(params) {
    hideNodeTip();
    store.dismissNotice();
    dragStart = params?.nodeId
      ? { nodeId: params.nodeId, handleId: params.handleId || null }
      : null;
    connectedThisDrag = false;
  }

  function onConnect(connection) {
    connectedThisDrag = true;

    if (store.readOnly) {
      return;
    }

    store.connectNodes(fromFlowConnection(connection));
  }

  function onConnectEnd(event) {
    const start = dragStart;
    dragStart = null;

    if (store.readOnly || connectedThisDrag || !start) {
      return;
    }

    const targetId = nodeIdAt(event);

    if (!targetId || targetId === start.nodeId) {
      return;
    }

    store.connectNodes(
      fromFlowConnection({
        source: start.nodeId,
        sourceHandle: start.handleId,
        target: targetId,
        targetHandle: null,
      }),
    );
  }

  function nodeIdAt(event) {
    const point = event?.changedTouches?.[0] || event;

    if (!point || typeof point.clientX !== 'number') {
      return null;
    }

    // The topmost device or switch under the pointer; a release on a group's
    // empty area or on a note is a cancel, like a release on the pane.
    for (const element of document.elementsFromPoint(
      point.clientX,
      point.clientY,
    )) {
      const node = element.closest?.('.vue-flow__node');
      const id = node?.getAttribute('data-id');
      const kind = id && store.doc.nodes.find((item) => item.id === id)?.kind;

      if (kind === 'device' || kind === 'switch') {
        return id;
      }
    }

    return null;
  }

  // A drag of several nodes is one edit: it becomes a single history commit and
  // therefore a single server snapshot. A drag that moved nothing is no edit
  // (the store drops it); when it also ended where it began, it was a click
  // whose pointer wobbled less than a grid step. Vue Flow reports that as a
  // drag and the browser drops its click, so it is handled as a click here.
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

    if (
      !store.moveNodes(moves) &&
      event.node &&
      endedWhereItBegan(event.event)
    ) {
      clickItem({ kind: 'nodes', id: event.node.id }, event.event);
      keepFocusOn(event.node.id);
    }
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

  // Vue Flow reports a change batch on every drag frame. Only a batch that
  // actually changes the selection may update the store: a new selection
  // rebuilds the controlled node list, which would put every node back at its
  // saved position mid-drag, so nodes could never be moved.
  function sameMembers(a, b) {
    return a.length === b.length && a.every((id) => b.includes(id));
  }

  function onNodesChange(changes) {
    if (!changes?.some((change) => change.type === 'select')) {
      return;
    }

    const nodes = selectedAfterChanges(store.selection.nodes, changes);

    if (!sameMembers(nodes, store.selection.nodes)) {
      store.select({ nodes, edges: store.selection.edges });
    }
  }

  function onEdgesChange(changes) {
    if (!changes?.some((change) => change.type === 'select')) {
      return;
    }

    const edges = selectedAfterChanges(store.selection.edges, changes);

    if (!sameMembers(edges, store.selection.edges)) {
      store.select({ nodes: store.selection.nodes, edges });
    }
  }

  // A click acts like Enter on the selection it was made on (see pressItem),
  // but Vue Flow has already changed the selection when it reports the click,
  // so the selection is noted when the pointer goes down, or when a click
  // comes without one (from assistive technology).
  //
  // With snap to grid on, Vue Flow can report one click on a node twice: on
  // mouseup, then on the click that follows. Only the first report counts,
  // and the click is kept from Vue Flow, which would otherwise select the
  // node again right after a click deselected it.
  const REPEAT_CLICK_MS = 500;
  // How far the pointer may wander between press and release and still make
  // a click, as a touch-friendly slop in CSS pixels.
  const CLICK_SLOP_PX = 8;
  let gesture = null;

  function startGesture(event) {
    gesture = {
      before: {
        nodes: [...store.selection.nodes],
        edges: [...store.selection.edges],
      },
      at: pointOf(event),
      handledAt: null,
    };
  }

  function pointOf(event) {
    const point = event?.changedTouches?.[0] || event;

    return typeof point?.clientX === 'number'
      ? { x: point.clientX, y: point.clientY }
      : null;
  }

  function endedWhereItBegan(event) {
    const start = gesture?.at;
    const end = pointOf(event);

    return Boolean(
      start &&
        end &&
        Math.hypot(end.x - start.x, end.y - start.y) <= CLICK_SLOP_PX,
    );
  }

  function startClick(event) {
    if (
      gesture?.handledAt != null &&
      event.timeStamp - gesture.handledAt < REPEAT_CLICK_MS
    ) {
      event.stopPropagation();
      gesture = null;

      return;
    }

    if (!gesture || gesture.handledAt != null) {
      startGesture();
    }
  }

  function endGesture() {
    gesture = null;
  }

  function clickItem(item, event) {
    if (!gesture || gesture.handledAt != null) {
      return;
    }

    gesture.handledAt = event?.timeStamp ?? performance.now();
    pressItem(item, Boolean(event?.shiftKey), gesture.before);
  }

  // Vue Flow blurs a node that a Shift+click takes out of the selection,
  // which would leave focus on the page; the clicked node keeps it instead.
  // Focus set by script after a click does not match :focus-visible, so the
  // view does not pan.
  function onNodeClick({ event, node }) {
    clickItem({ kind: 'nodes', id: node.id }, event);
    keepFocusOn(node.id);
  }

  function keepFocusOn(id) {
    nextTick(() => {
      const element = nodeElement(id);

      if (element && !element.contains(document.activeElement)) {
        element.focus({ preventScroll: true });
      }
    });
  }

  // A clicked node takes focus by itself; a clicked connection does not, so
  // focus it here and Delete then removes it.
  function onEdgeClick({ event, edge }) {
    clickItem({ kind: 'edges', id: edge.id }, event);
    edgeElement(edge.id)?.focus({ preventScroll: true });
  }

  function onPaneClick() {
    store.clearSelection();
    store.dismissNotice();
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

    const bounds = (vueFlowRef.value || root.value)?.getBoundingClientRect();
    const position = project
      ? project({
          x: event.clientX - (bounds?.left || 0),
          y: event.clientY - (bounds?.top || 0),
        })
      : { x: event.clientX, y: event.clientY };

    store.addNode({
      ...paletteNode(
        store,
        kind,
        event.dataTransfer.getData(PALETTE_TEMPLATE_MIME),
      ),
      position: { x: Math.round(position.x), y: Math.round(position.y) },
    });
  }

  // --- keyboard ------------------------------------------------------------

  // The node or connection whose wrapper is the event target, if any.
  function itemOf(element) {
    const kind =
      (element?.classList?.contains('vue-flow__node') && 'nodes') ||
      (element?.classList?.contains('vue-flow__edge') && 'edges') ||
      '';
    const id = kind && element.getAttribute('data-id');

    return id ? { kind, id } : null;
  }

  function isSelected({ kind, id }) {
    return store.selection[kind].includes(id);
  }

  // A node or connection is a toggle button, pressed while it is selected, so
  // Enter, Space or a click on it acts on the selection it was pressed on
  // (`before`); pressSelection says what a plain or a Shift press does, the
  // same as on an outline row. The selection is set in full each time,
  // whatever Vue Flow did with the click, so aria-pressed always matches it.
  //
  // An item the press selects is shown in the Inspector, which shows again
  // if it was hidden; a plain press on the one selected item does only that
  // while the Inspector is hidden, rather than deselect it.
  function pressItem(item, additive, before = store.selection) {
    const alone =
      !additive &&
      before[item.kind].includes(item.id) &&
      before.nodes.length + before.edges.length === 1;

    if (alone && panes?.isHidden('end')) {
      store.select({ nodes: [], edges: [], [item.kind]: [item.id] });
      showInspector(item);
      return;
    }

    const { selection, message } = pressSelection(
      store.doc,
      before,
      item,
      additive,
    );

    store.select(selection);
    store.announce(message);

    if (selection[item.kind].includes(item.id)) {
      showInspector(item);
    }
  }

  // Shows the Inspector if it was hidden. The canvas is narrower then, so
  // the item is kept in view once it is drawn there.
  function showInspector(item) {
    if (!panes?.show('end')) {
      return;
    }

    nextTick(() =>
      requestAnimationFrame(() =>
        reveal(
          item.kind === 'edges' ? edgeElement(item.id) : nodeElement(item.id),
        ),
      ),
    );
  }

  function clearSelection() {
    if (store.selection.nodes.length || store.selection.edges.length) {
      store.clearSelection();
      store.announce('Selection cleared');
    }
  }

  // Delete removes the focused item. When that item is part of the selection,
  // or the canvas itself has focus, it removes the whole selection.
  // Focus then moves to the node nearest the deleted item, or to the canvas
  // itself, never to the page.
  async function deleteFromKeyboard(item) {
    const from = item && itemPoint(store.doc, item);

    if (item && !isSelected(item)) {
      // The rest of the selection stays selected (store.remove keeps it).
      store.remove({ nodes: [], edges: [], [item.kind]: [item.id] });
    } else {
      store.removeSelection();
    }

    // Vue Flow renders the new graph a tick after the store changes. Focus
    // that was not on a removed item stays where it is.
    await nextTick();
    await nextTick();
    if (!focusLost()) {
      return;
    }

    const id = from && nearestNode(store.doc, from);
    ((id && nodeElement(id)) || root.value)?.focus();
  }

  // Focus moved by a key: onFocusIn brings a :focus-visible item into view,
  // and this any other.
  function focusItem(element) {
    element.focus({ preventScroll: true });

    if (!element.matches(':focus-visible')) {
      reveal(element);
    }
  }

  // The middle of what the pane shows, in flow coordinates.
  function viewMiddle() {
    const area = visibleArea();

    return area
      ? { x: area.x + area.width / 2, y: area.y + area.height / 2 }
      : null;
  }

  // An arrow key moves focus from a node or connection to the nearest node
  // that way, and from the canvas itself to the node nearest the middle of
  // the view. Where no node lies that way, focus stays.
  function moveFocus(item, key) {
    const from = item ? itemPoint(store.doc, item) : viewMiddle();
    const id =
      from &&
      (item
        ? nodeInDirection(
            store.doc,
            from,
            key,
            item.kind === 'nodes' ? item.id : undefined,
          )
        : nearestNode(store.doc, from));
    const element = id && nodeElement(id);

    if (element) {
      focusItem(element);
    }
  }

  // The node whose connections Page Down and Page Up go through: the one
  // that last had focus, while focus is on one of its connections.
  let connectionsOf = null;

  // Page Down and Page Up move focus through the connections of the focused
  // node, or of the node focus came from to the focused connection (its
  // device end when it came from elsewhere), round from last to first.
  function stepConnections(item, step) {
    if (!item) {
      return;
    }

    let nodeId = item.id;

    if (item.kind === 'edges') {
      const edge = findEdge(store.doc, item.id);

      if (!edge) {
        return;
      }

      nodeId = [edge.sourceNodeId, edge.targetNodeId].includes(connectionsOf)
        ? connectionsOf
        : edge.sourceNodeId;
    }

    const next = connectionStep(
      store.doc,
      nodeId,
      item.kind === 'edges' ? item.id : null,
      step,
    );

    if (!next) {
      const name = selectionItemName(store.doc, { kind: 'nodes', id: nodeId });

      store.announce(`${name} has no connections.`);
      return;
    }

    connectionsOf = nodeId;
    const element = edgeElement(next);

    if (element) {
      focusItem(element);
    }
  }

  const ARROWS = {
    ArrowUp: { x: 0, y: -1 },
    ArrowDown: { x: 0, y: 1 },
    ArrowLeft: { x: -1, y: 0 },
    ArrowRight: { x: 1, y: 0 },
  };
  // How far Shift with an arrow key moves the selected nodes.
  const MOVE_STEP = 10;

  function onKeydown(event) {
    const item = itemOf(event.target);
    const meta = event.ctrlKey || event.metaKey;
    const arrow = ARROWS[event.key];

    // The keys act only on a node, a connection or the canvas itself. On the
    // canvas's own controls (the zoom buttons and the notice's Dismiss
    // button) they keep their usual meaning and never edit
    // the diagram. The shortcuts with Ctrl or ⌘ (select all, copy, paste,
    // undo...), the zoom keys and F2 are the view's key dispatcher's, from
    // the command registry (commands.js).
    if (!item && event.target !== root.value) {
      return;
    }

    // Selecting is not editing, so it works in a read-only draft too.
    if (item && !meta && (event.key === 'Enter' || event.key === ' ')) {
      event.preventDefault();
      pressItem(item, event.shiftKey);
      return;
    }

    if (event.key === 'Escape') {
      store.dismissNotice();
      clearSelection();
      return;
    }

    // Moving focus is not editing either. Alt with Left or Right goes back
    // or forward in the browser, so an arrow key with any modifier is left
    // alone here; Shift with one moves nodes, below.
    const plain = !meta && !event.altKey && !event.shiftKey;

    if (arrow && plain) {
      event.preventDefault();
      moveFocus(item, event.key);
      return;
    }

    if ((event.key === 'PageDown' || event.key === 'PageUp') && plain) {
      event.preventDefault();
      stepConnections(item, event.key === 'PageDown' ? 1 : -1);
      return;
    }

    if (store.readOnly) {
      return;
    }

    if (event.key === 'Delete' || event.key === 'Backspace') {
      if (
        (item && !isSelected(item)) ||
        store.selection.nodes.length ||
        store.selection.edges.length
      ) {
        event.preventDefault();
        deleteFromKeyboard(item);
      }
      return;
    }

    // Alt (Option) and Shift with an arrow key resize the selected group,
    // note, shape or icon, from the same places Shift and an arrow key move
    // it.
    if (
      arrow &&
      event.altKey &&
      event.shiftKey &&
      !meta &&
      (!item || isSelected(item))
    ) {
      event.preventDefault();
      resizeFromKeyboard(event.key);
      return;
    }

    // Shift and an arrow key move the selection, but not from an item
    // outside it: the focused node would stay put while nodes elsewhere
    // moved.
    if (arrow && event.shiftKey && !event.altKey && !meta) {
      event.preventDefault();

      if (!store.selection.nodes.length || (item && !isSelected(item))) {
        store.announce('Select the nodes to move first.');
        return;
      }

      store.nudgeSelection(arrow.x * MOVE_STEP, arrow.y * MOVE_STEP);

      // A nudge can carry a node under the minimap or off screen: keep the
      // focused item in view or, when the canvas itself has focus, the one
      // selected node.
      const target = item
        ? event.target
        : store.selection.nodes.length === 1 &&
          nodeElement(store.selection.nodes[0]);
      if (target) {
        nextTick(() => requestAnimationFrame(() => reveal(target)));
      }
    }
  }

  // Resizes the one selected group, note, shape or icon from its bottom
  // right corner: Right and Down grow it, Left and Up shrink it, never past
  // its least size or what a group's members need (keyResizedSize). The
  // store says the new size.
  const RESIZE_STEP = 10;

  function resizeFromKeyboard(key) {
    const { nodes, edges } = store.selection;
    const group =
      nodes.length === 1 && edges.length === 0
        ? findNode(store.doc, nodes[0])
        : null;
    const next = group && keyResizedSize(store.doc, group.id, key, RESIZE_STEP);

    if (!next) {
      store.announce('Select one group, note, shape or icon to resize it.');
      return;
    }

    const size = sizeOf(group);

    if (next.width === size.width && next.height === size.height) {
      store.announce(
        group.kind === 'group'
          ? `${nodeLabel(group)} cannot be smaller: its members need the room.`
          : `${nodeLabel(group)} cannot be smaller.`,
      );
      return;
    }

    store.resizeNode(group.id, next);
  }

  // --- keeping keyboard focus in view (WCAG 2.4.11) -------------------------

  // Pans the canvas so an item is fully visible and not under the minimap,
  // the zoom controls or the notice, which float over the pane. The zoom
  // stays as it is.
  // The part of an item to keep in view: the item itself, or, for a
  // connection too long to fit in the pane, its label, which marks the
  // middle of the line.
  function targetBox(element, pane) {
    const box = element.getBoundingClientRect();
    const id = element.getAttribute('data-id');

    if (
      (box.width <= pane.width && box.height <= pane.height) ||
      !element.classList.contains('vue-flow__edge') ||
      !id
    ) {
      return box;
    }

    const label = root.value.querySelector(
      `.builder-edge__label[data-edge-id="${CSS.escape(id)}"]`,
    );

    return label ? label.getBoundingClientRect() : box;
  }

  function nodeElement(id) {
    return root.value?.querySelector(
      `.vue-flow__node[data-id="${CSS.escape(id)}"]`,
    );
  }

  function edgeElement(id) {
    return root.value?.querySelector(
      `.vue-flow__edge[data-id="${CSS.escape(id)}"]`,
    );
  }

  // Only a node or a connection is kept in view. The canvas section itself is
  // taller than the pane, so "revealing" it would pan the whole diagram.
  function reveal(element) {
    const pane = vueFlowRef.value?.getBoundingClientRect();

    if (!element?.isConnected || !itemOf(element) || !pane?.width) {
      return;
    }

    const box = targetBox(element, pane);
    const overlays = overlayBoxes();

    if (hiddenArea(box, pane, overlays) === 0) {
      return;
    }

    const spot = freeSpot(pane, box.width, box.height, overlays);
    const { x, y, zoom } = viewport.value;
    setViewport(
      {
        x: x + spot.x - (box.left - pane.left + box.width / 2),
        y: y + spot.y - (box.top - pane.top + box.height / 2),
        zoom,
      },
      // A straight pan. The default smooth path zooms out on the way, so a
      // reveal that interrupts another would keep a smaller zoom each time.
      { duration: props.reducedMotion ? 0 : 200, interpolate: 'linear' },
    );
  }

  // What floats over the pane: the minimap and the zoom controls, in its
  // corners, and the notice.
  const CORNER_OVERLAYS = '.vue-flow__minimap, .vue-flow__controls';

  function overlayBoxes(
    selector = `${CORNER_OVERLAYS}, .builder-canvas__notice`,
  ) {
    return [...(root.value?.querySelectorAll(selector) || [])].map((overlay) =>
      overlay.getBoundingClientRect(),
    );
  }

  // Room kept around nodes brought into view, in screen pixels.
  const REVEAL_MARGIN = 24;

  // The room, on each side of the pane, to fit nodes with bounds `bounds`
  // into it clear of the overlays (see fitPadding). Null before the pane has
  // a size.
  function fitRoom(bounds, overlays) {
    const pane = vueFlowRef.value?.getBoundingClientRect();

    if (!pane?.width || !pane?.height) {
      return null;
    }

    const room = fitPadding(bounds, pane, overlays);

    return {
      pane,
      zoom: fitZoom(bounds, pane, room),
      // As Vue Flow takes it: pixels, not a share of the pane.
      padding: {
        top: `${room.top}px`,
        right: `${room.right}px`,
        bottom: `${room.bottom}px`,
        left: `${room.left}px`,
      },
    };
  }

  // Brings nodes into view, leaving focus where it is. When any of them is
  // outside the pane or under what floats over it, the view centers on them
  // all: at the same zoom when they fit clear of what floats over the pane,
  // or else zoomed out to fit them, which the least zoom always allows (see
  // zoomFloor). They are found in the document, so they need not be drawn
  // yet.
  function revealNodes(ids) {
    const pane = vueFlowRef.value?.getBoundingClientRect();
    const wanted = new Set(ids);
    const nodes = (store.doc.nodes || []).filter((node) => wanted.has(node.id));

    if (!pane?.width || !pane?.height || !nodes.length) {
      return;
    }

    const { x, y, zoom } = viewport.value;
    const overlays = overlayBoxes();
    const onScreen = (node) => {
      const { width, height } = sizeOf(node);
      const left = pane.left + x + node.position.x * zoom;
      const top = pane.top + y + node.position.y * zoom;

      return {
        left,
        top,
        right: left + width * zoom,
        bottom: top + height * zoom,
      };
    };

    if (
      nodes.every((node) => hiddenArea(onScreen(node), pane, overlays) === 0)
    ) {
      return;
    }

    const bounds = boundsOf(nodes);
    const width = bounds.width * zoom + 2 * REVEAL_MARGIN;
    const height = bounds.height * zoom + 2 * REVEAL_MARGIN;
    const duration = props.reducedMotion ? 0 : 200;
    const spot =
      width <= pane.width &&
      height <= pane.height &&
      freeSpot(pane, width, height, overlays);

    if (!spot || spot.hidden > 0) {
      // Zoomed out, never in.
      setViewport(
        getTransformForBounds(
          bounds,
          pane.width,
          pane.height,
          0,
          Math.min(zoom, MAX_ZOOM),
          fitRoom(bounds, overlays).padding,
        ),
        { duration },
      );

      return;
    }

    setViewport(
      {
        x: spot.x - (bounds.x + bounds.width / 2) * zoom,
        y: spot.y - (bounds.y + bounds.height / 2) * zoom,
        zoom,
      },
      // A straight pan, as reveal's.
      { duration, interpolate: 'linear' },
    );
  }

  // A node or connection that takes focus, by any means, becomes the
  // canvas's Tab stop. Only keyboard focus pans: a click that focuses a node
  // must not move the canvas under the pointer.
  function onFocusIn(event) {
    const item = itemOf(event.target);

    if (!item) {
      return;
    }

    if (
      item.kind !== lastFocused.value?.kind ||
      item.id !== lastFocused.value?.id
    ) {
      lastFocused.value = item;
    }

    if (item.kind === 'nodes') {
      connectionsOf = item.id;
    }

    if (event.target.matches(':focus-visible')) {
      reveal(event.target);
    }
  }

  /**
   * Element rasterized for a PNG or SVG image: Vue Flow's transformation pane
   * holds the whole graph, including parts scrolled out of view, and carries
   * the live pan and zoom, which the image replaces with its own. The
   * viewport around it would clip the image to its own box.
   *
   * @returns {HTMLElement|null}
   */
  function viewportElement() {
    const pane = '.vue-flow__transformationpane';

    return (
      vueFlowRef.value?.querySelector(pane) ||
      root.value?.querySelector(pane) ||
      null
    );
  }

  // A diagram opens at 100% or at the settings' own percentage (see
  // startViewport), or fitted to the canvas as Fit does, if the settings
  // say so. The fit waits for Vue Flow to measure the nodes, which
  // it shows only then, so the diagram is never seen at 100% first. The
  // view makes a canvas for each diagram it opens, so this runs for each.
  let fitWhenMeasured =
    builderSettings.openZoom === 'fit' && store.doc.nodes.length > 0;

  function onNodesInitialized() {
    if (fitWhenMeasured) {
      fitWhenMeasured = false;
      fitDiagram();
    }
  }

  // Fit: the whole diagram in view, at whatever zoom that takes, and clear
  // of the minimap and the zoom controls (see fitPadding). The least zoom is
  // given as well, in case Vue Flow has not had it yet, or less when that is
  // what the room takes.
  function fitDiagram(options = {}) {
    const bounds = boundsOf(store.doc.nodes || []);
    const fit = fitRoom(bounds, overlayBoxes(CORNER_OVERLAYS));

    return fitView({
      ...options,
      ...(fit && { padding: fit.padding }),
      minZoom: Math.min(diagramFloor.value, fit?.zoom ?? Infinity),
    });
  }

  // --- Fit, and back to the view before it ----------------------------------

  // What the Fit button keeps to go back to (see keepFit): null until Fit
  // changes the view, and again once anything else does. The button, its
  // tooltip and icon, and the view.fit command's title follow it.
  const beforeFit = shallowRef(null);
  const fitRestores = computed(() => beforeFit.value !== null);
  const fitName = computed(() =>
    fitRestores.value ? 'Restore previous view' : 'Fit diagram to view',
  );
  const FIT_TIP_EVENTS = zoomTip(() => fitName.value, 'view.fit');

  // A pan or a zoom moves the nodes: a node's tooltip follows it, and goes
  // when the node leaves the view.
  watch(viewport, (view) => {
    beforeFit.value = keptFit(beforeFit.value, view);
    nodeTip.follow();
  });

  // A press from the keyboard leaves the tooltip up, so it takes the
  // button's new name, while it is still that button's.
  watch(fitName, () => {
    const target = shownTip?.target;

    if (!target?.isConnected || tip.value?.text !== shownTip.text) {
      return;
    }

    const text = shownTip.tipText();

    if (text !== shownTip.text) {
      shownTip.text = text;
      showTip({ currentTarget: target }, text);
    }
  });

  // The Fit button and the view.fit command: Fit, or, after Fit, back to
  // the view from before it. Neither is animated, so the view has changed
  // when they return. Each says what it did, and a fit what the next press
  // does, which the key on the canvas does not show.
  function toggleFit() {
    const kept = beforeFit.value;

    if (kept) {
      beforeFit.value = null;
      setViewport(kept.before);
      store.announce('Restored the previous view');
      return;
    }

    const before = { ...viewport.value };

    fitDiagram();
    beforeFit.value = keepFit(before, viewport.value);
    store.announce(
      beforeFit.value
        ? 'Fitted the diagram to the view. A second press restores the previous view.'
        : 'The diagram already fits the view.',
    );
  }

  // Reset view: the zoom and pan the canvas opens with, which the
  // minimap's view follows. A fit waits for the columns and the minimap
  // that Reset view puts back to be drawn, and for Vue Flow to measure the
  // pane again, which it does before the next frame is painted. The view
  // Fit kept is forgotten, even when the view does not change.
  async function resetViewport() {
    const duration = props.reducedMotion ? 0 : 200;

    beforeFit.value = null;

    if (builderSettings.openZoom === 'fit' && store.doc.nodes.length) {
      await nextTick();
      await new Promise((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(resolve)),
      );
      fitDiagram({ duration });
    } else {
      setViewport(startViewport(), { duration });
    }
  }

  // For the view's commands: the zoom keys, and Go to node, which focuses a
  // node and pans it into view as keyboard focus does.
  function zoomInView() {
    if (!atMaxZoom.value) {
      zoomIn();
    }
  }

  function zoomOutView() {
    if (!atMinZoom.value) {
      zoomOut();
    }
  }

  async function showNode(id) {
    await nextTick();
    const element = nodeElement(id);

    if (element) {
      element.focus({ preventScroll: true });
      reveal(element);
    }
  }

  // For adding nodes where the user is looking (see addInView): the part of
  // the diagram the pane shows, in flow coordinates.
  function visibleArea() {
    const pane = vueFlowRef.value?.getBoundingClientRect();
    const { x, y, zoom } = viewport.value;

    if (!pane?.width || !pane?.height || !zoom) {
      return null;
    }

    return {
      x: -x / zoom,
      y: -y / zoom,
      width: pane.width / zoom,
      height: pane.height / zoom,
    };
  }

  // Brings a node, or several, into view once Vue Flow has drawn them,
  // leaving focus where it is: a node just added (on the palette entry that
  // added it), or the nodes of an outline row (on the row).
  async function revealNode(ids) {
    await nextTick();
    await nextTick();
    requestAnimationFrame(() => revealNodes([ids].flat()));
  }

  defineExpose({
    viewportElement,
    fitView: toggleFit,
    fitRestores,
    zoomIn: zoomInView,
    zoomOut: zoomOutView,
    atMaxZoom,
    atMinZoom,
    showNode,
    visibleArea,
    revealNode,
    resetViewport,
    resetMinimap,
    minimapState,
    resizeMinimap,
  });
</script>
