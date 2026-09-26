<!--
  Vue Flow canvas.

  The canvas is a *view* of the document: every gesture is translated into a
  pure model operation on the store, and the rendered graph is derived back
  from the document. Keyboard equivalents exist for every pointer gesture, and
  the semantic outline (BuilderOutline.vue) covers everything drag does.

  Every node and connection is one Tab stop: Vue Flow's wrapper element,
  named and described by adapters/vueflow.js. Vue Flow's own keyboard layer is
  off; this component handles the keys, so the hints it gives are the keys
  that work.
-->
<template>
  <section
    id="builder-canvas"
    ref="root"
    class="builder-canvas builder-panel"
    data-testid="builder-canvas"
    aria-labelledby="builder-canvas-title"
    aria-describedby="builder-canvas-help-summary"
    tabindex="-1"
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
    <!-- Always rendered, so the canvas has a description while the Keyboard
         help below is closed. -->
    <p id="builder-canvas-help-summary" hidden>{{ hints.canvas }}</p>

    <VueFlow
      class="builder-canvas__flow"
      :nodes="flowNodes"
      :edges="flowEdges"
      :node-types="nodeTypes"
      :edge-types="edgeTypes"
      :default-viewport="START_VIEWPORT"
      :min-zoom="MIN_ZOOM"
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
      @connect="onConnect"
      @connect-start="onConnectStart"
      @connect-end="onConnectEnd"
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
           as zooms. -->
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
            v-on="zoomTip('Fit diagram to view', 'view.fit')"
            @click="fitView()">
            <svg
              class="builder-canvas__fit-icon"
              viewBox="0 0 24 24"
              width="14"
              height="14"
              aria-hidden="true">
              <path d="M4 9V4h5M15 4h5v5M20 15v5h-5M9 20H4v-5" />
            </svg>
            <span class="builder-visually-hidden">Fit diagram to view</span>
          </ControlButton>
        </template>
      </Controls>
      <MiniMap
        v-if="showMinimap"
        pannable
        zoomable
        aria-label="Diagram minimap"
        :node-color="minimapColor"
        :node-class-name="minimapClass" />
    </VueFlow>

    <div
      v-if="tip"
      ref="tipEl"
      class="builder-tooltip builder-tooltip--fixed"
      data-testid="zoom-tooltip"
      aria-hidden="true"
      :style="{ top: `${tip.top}px`, left: `${tip.left}px` }">
      {{ tip.text }}
    </div>

    <!-- A bar below the diagram, in normal flow, so opening it never covers
         a node, a connection or the notice (WCAG 2.4.11). -->
    <details class="builder-canvas__help" data-testid="canvas-help">
      <!-- nokey: Vue Flow's pan key handler would otherwise take Space, which
           opens and closes it. -->
      <summary class="nokey">Keyboard help</summary>
      <!-- Written from the command registry, so it names the keys of this
           platform, as the user set them. -->
      <ul id="builder-canvas-help">
        <li v-for="line in helpLines" :key="line">{{ line }}</li>
      </ul>
      <!-- The shortcut sheet, where the shortcuts can be changed; ? opens it
           too, unless single-key shortcuts are off. -->
      <p class="builder-canvas__help-more">
        <button
          type="button"
          class="builder-button"
          aria-haspopup="dialog"
          data-testid="canvas-help-shortcuts"
          @click="openShortcuts">
          Show all keyboard shortcuts
        </button>
      </p>
    </details>
  </section>
</template>

<script setup>
  import { computed, inject, markRaw, nextTick, ref } from 'vue';
  import { ConnectionMode, VueFlow, useVueFlow } from '@vue-flow/core';
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
  import NetworkEdge from './edges/NetworkEdge.vue';
  import { useFixedTooltip } from './fixedTooltip.js';

  import {
    canvasHelp,
    canvasHints,
    runCommand,
    shortcutLabel,
  } from '@/builder/commands.js';
  import { nodeIssueSummaries } from '@/builder/issues.js';
  import {
    boundsOf,
    canConnect,
    findNode,
    groupMinimumSize,
    nodeLabel,
    sizeOf,
  } from '@/builder/model.js';
  import { pressSelection } from '@/builder/selection.js';
  import { builderSettings } from '@/builder/settings.js';
  import { useBuilderStore } from '@/builder/store.js';
  import {
    EDGE_HINT_ID,
    NODE_HINT_ID,
    absolutePosition,
    freeSpot,
    fromFlowConnection,
    hiddenArea,
    toFlowEdges,
    toFlowNodes,
    withSelection,
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

  const MIN_ZOOM = 0.2;
  const MAX_ZOOM = 2;
  // The zoom and pan the canvas opens with.
  const START_VIEWPORT = { x: 0, y: 0, zoom: 1 };

  const store = useBuilderStore();
  const root = ref(null);
  const {
    fitBounds,
    fitView,
    project,
    setViewport,
    viewport,
    vueFlowRef,
    zoomIn,
    zoomOut,
  } = useVueFlow();

  // What the keys do on a focused node or connection, and a summary for the
  // canvas itself; a read-only draft can only be selected. Both follow the
  // platform and the user's keys (see commands.js).
  const hints = computed(() => canvasHints({ readOnly: store.readOnly }));
  const helpLines = computed(() => canvasHelp({ readOnly: store.readOnly }));

  // The view's command context (BuilderBeta.vue), for the Keyboard help's
  // button to the shortcut sheet.
  const commands = inject('builderCommands', null);

  function openShortcuts() {
    if (commands) {
      runCommand('shortcuts.open', commands);
    }
  }

  const atMaxZoom = computed(() => viewport.value.zoom >= MAX_ZOOM);
  const atMinZoom = computed(() => viewport.value.zoom <= MIN_ZOOM);

  // The zoom buttons' names and keys, as tooltips (WCAG 1.4.13); the
  // palette's entries show theirs the same way. The keys work on the
  // canvas, its nodes and connections, not on the buttons, and say so.
  const { tip, tipEl, showTip, scheduleHide, hideTip } = useFixedTooltip();

  function zoomTip(text, command) {
    const tipText = () => {
      const keys = shortcutLabel(command);

      return keys ? `${text} (${keys} on the canvas)` : text;
    };
    const show = (event) => showTip(event, tipText());

    return {
      mouseenter: show,
      mouseleave: scheduleHide,
      focus: show,
      blur: hideTip,
    };
  }

  // Components, never made reactive: Vue Flow would otherwise wrap them.
  const nodeTypes = markRaw({
    builderDevice: markRaw(DeviceNode),
    builderSwitch: markRaw(SwitchNode),
    builderNote: markRaw(NoteNode),
    builderGroup: markRaw(GroupNode),
  });

  const edgeTypes = markRaw({ builderNetwork: markRaw(NetworkEdge) });

  // What the diagram checks found about each node, which marks it.
  const nodeIssues = computed(() =>
    nodeIssueSummaries(store.doc, store.issues),
  );

  // The graph follows the document; the selection then changes only the
  // nodes and connections it selects or deselects, so a click on a large
  // diagram redraws those alone (see withSelection).
  const baseNodes = computed(() =>
    toFlowNodes(store.doc, { issues: nodeIssues.value }),
  );
  const baseEdges = computed(() => toFlowEdges(store.doc));
  const flowNodes = computed(() =>
    withSelection(baseNodes.value, store.selection.nodes),
  );
  const flowEdges = computed(() =>
    withSelection(baseEdges.value, store.selection.edges),
  );

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
  // builder.css), so the two kinds differ by shape as well.
  const MINIMAP_COLORS = {
    light: { device: '#1f5fa9', switch: '#8a5300' },
    dark: { device: '#7fb2f0', switch: '#e5b567' },
  };

  function minimapColor(node) {
    const colors =
      MINIMAP_COLORS[store.resolvedTheme === 'dark' ? 'dark' : 'light'];

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
    root.value
      ?.querySelector(`.vue-flow__edge[data-id="${CSS.escape(edge.id)}"]`)
      ?.focus({ preventScroll: true });
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
      ...paletteNode(kind, event.dataTransfer.getData(PALETTE_TEMPLATE_MIME)),
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
  function pressItem(item, additive, before = store.selection) {
    const { selection, message } = pressSelection(
      store.doc,
      before,
      item,
      additive,
    );

    store.select(selection);
    store.announce(message);
  }

  function clearSelection() {
    if (store.selection.nodes.length || store.selection.edges.length) {
      store.clearSelection();
      store.announce('Selection cleared');
    }
  }

  // The wrappers that take focus, in Tab order: connections, then nodes.
  function focusableItems() {
    return [
      ...(root.value?.querySelectorAll(
        '.vue-flow__edge[tabindex="0"], .vue-flow__node[tabindex="0"]',
      ) || []),
    ];
  }

  // Delete removes the focused item. When that item is part of the selection,
  // or the canvas itself has focus, it removes the whole selection.
  // Focus then moves to the item that took the deleted one's place, or to the
  // canvas itself, never to the page.
  async function deleteFromKeyboard(item) {
    const position = focusableItems().indexOf(document.activeElement);

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
    const active = document.activeElement;

    if (active && active !== document.body && active.isConnected) {
      return;
    }

    const items = focusableItems();
    const next = items[Math.min(Math.max(position, 0), items.length - 1)];
    (next || root.value)?.focus();
  }

  function onKeydown(event) {
    const item = itemOf(event.target);
    const meta = event.ctrlKey || event.metaKey;

    // The keys act only on a node, a connection or the canvas itself. On the
    // canvas's own controls (the zoom buttons, the Keyboard help and the
    // notice's Dismiss button) they keep their usual meaning and never edit
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

    if (store.readOnly) {
      return;
    }

    const step = event.shiftKey ? 1 : 10;

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
    // from the same places the arrow keys move it.
    if (
      event.key.startsWith('Arrow') &&
      event.altKey &&
      event.shiftKey &&
      !meta &&
      (!item || isSelected(item))
    ) {
      event.preventDefault();
      resizeFromKeyboard(event.key);
      return;
    }

    // Arrow keys move the selection, but not from an item outside it: the
    // focused node would stay put while nodes elsewhere moved.
    if (
      event.key.startsWith('Arrow') &&
      store.selection.nodes.length &&
      (!item || isSelected(item))
    ) {
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

  // Resizes the one selected group from its bottom right corner: Right and
  // Down grow it, Left and Up shrink it, never past what its members need
  // (groupMinimumSize). The store says the new size.
  const RESIZE_STEP = 10;

  function resizeFromKeyboard(key) {
    const { nodes, edges } = store.selection;
    const group =
      nodes.length === 1 && edges.length === 0
        ? findNode(store.doc, nodes[0])
        : null;

    if (group?.kind !== 'group') {
      store.announce('Select one group to resize it.');
      return;
    }

    const [dx, dy] = {
      ArrowRight: [RESIZE_STEP, 0],
      ArrowLeft: [-RESIZE_STEP, 0],
      ArrowDown: [0, RESIZE_STEP],
      ArrowUp: [0, -RESIZE_STEP],
    }[key] || [0, 0];
    const size = sizeOf(group);
    const least = groupMinimumSize(store.doc, group.id);
    const resized = (now, delta, min) =>
      delta < 0 ? Math.min(now, Math.max(min, now + delta)) : now + delta;
    const next = {
      width: resized(size.width, dx, least.width),
      height: resized(size.height, dy, least.height),
    };

    if (next.width === size.width && next.height === size.height) {
      store.announce(
        `${nodeLabel(group)} cannot be smaller: its members need the room.`,
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

  // What floats over the pane: the minimap, the zoom controls, the notice.
  function overlayBoxes() {
    return [
      ...(root.value?.querySelectorAll(
        '.vue-flow__minimap, .vue-flow__controls, .builder-canvas__notice',
      ) || []),
    ].map((overlay) => overlay.getBoundingClientRect());
  }

  // Room kept around nodes brought into view, in screen pixels.
  const REVEAL_MARGIN = 24;
  // Room fitBounds keeps around them, as a share of their size.
  const REVEAL_PADDING = 0.1;

  // Brings nodes into view, leaving focus where it is. When any of them is
  // outside the pane or under what floats over it, the view centers on them
  // all: at the same zoom when they fit, or zoomed out to fit them. When
  // they would not fit even at the least zoom, the first of them (an
  // outline row's own node) is brought into view alone instead. They are
  // found in the document, so they need not be drawn yet.
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

    if (width > pane.width || height > pane.height) {
      // The zoom fitBounds would take for them.
      const fit = Math.min(
        pane.width / (bounds.width * (1 + REVEAL_PADDING)),
        pane.height / (bounds.height * (1 + REVEAL_PADDING)),
      );

      if (fit < MIN_ZOOM && nodes.length > 1) {
        revealNodes([ids[0]]);
      } else {
        fitBounds(bounds, { padding: REVEAL_PADDING, duration });
      }

      return;
    }

    const spot = freeSpot(pane, width, height, overlays);

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

  // Only keyboard focus pans: a click that focuses a node must not move the
  // canvas under the pointer.
  function onFocusIn(event) {
    if (itemOf(event.target) && event.target.matches(':focus-visible')) {
      reveal(event.target);
    }
  }

  /**
   * Element rasterized by PNG/SVG export: Vue Flow's transformation pane
   * holds the whole graph, including parts scrolled out of view, and carries
   * the live pan and zoom, which the export replaces with its own. The
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

  // A diagram opens at 100%, or fitted to the canvas as Fit does, if the
  // settings say so. The fit waits for Vue Flow to measure the nodes, which
  // it shows only then, so the diagram is never seen at 100% first. The
  // view makes a canvas for each diagram it opens, so this runs for each.
  let fitWhenMeasured =
    builderSettings.openZoom === 'fit' && store.doc.nodes.length > 0;

  function onNodesInitialized() {
    if (fitWhenMeasured) {
      fitWhenMeasured = false;
      fitView();
    }
  }

  // Reset view: the zoom and pan the canvas opens with, which the
  // minimap's view follows.
  function resetViewport() {
    const duration = props.reducedMotion ? 0 : 200;

    if (builderSettings.openZoom === 'fit' && store.doc.nodes.length) {
      fitView({ duration });
    } else {
      setViewport(START_VIEWPORT, { duration });
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
    fitView,
    zoomIn: zoomInView,
    zoomOut: zoomOutView,
    atMaxZoom,
    atMinZoom,
    showNode,
    visibleArea,
    revealNode,
    resetViewport,
  });
</script>
