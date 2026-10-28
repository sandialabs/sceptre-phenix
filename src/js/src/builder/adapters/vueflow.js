// Vue Flow adapter: the only module that knows Vue Flow's node and edge
// shape. The model keeps absolute positions, and Vue Flow takes child
// positions relative to the parent. Each network gets a stroke pattern and a
// color, so color alone never shows network membership.

import { nodeIcon, nodeIconKey } from '../catalog.js';
import { networkColorToken } from '../colors.js';
import { iconSrc } from '../icons.js';
import { stableHash } from '../ids.js';
import {
  connectionEndLabel,
  deviceHandles,
  deviceTypeLabel,
  drawnNodeComment,
  findNode,
  ICON_SIZE_KINDS,
  includedFrom,
  layerOutlineColor,
  LINE_STYLES,
  nodeIconSize,
  nodeLabel,
  sizeOf,
  specInterfaceFor,
} from '../model.js';
import { interfaceAddress } from '../nodeInfo.js';
import { labelIndex, nodeAccessibleName } from '../diagramText.js';
import { handleOffsetY } from '../routes.js';

export const FLOW_NODE_TYPES = {
  device: 'builderDevice',
  switch: 'builderSwitch',
  note: 'builderNote',
  group: 'builderGroup',
  shape: 'builderShape',
  icon: 'builderIcon',
  line: 'builderLine',
};

// Groups at the bottom, then drawings, then other nodes, so a drawing never
// covers a node it is drawn around. Vue Flow puts a node in a group one layer
// above the higher of the group's and its own, so NODE_LAYER leaves room.
const KIND_LAYERS = { group: 0, shape: 0, line: 0 };
const NODE_LAYER = 10;
// A selected node, and every node in a selected group, goes above the others
// (elevateNodesOnSelect is off, see BuilderCanvas.vue). A shape or line never
// does, so it never covers the nodes and handles it is drawn around.
const SELECTED_LIFT = 1000;
const NEVER_LIFTED = ['shape', 'line'];

/** A canvas node's zIndex: its kind's layer, lifted while it or its group is selected. */
export function nodeZIndex(kind, selected = false, inSelectedGroup = false) {
  const layer = KIND_LAYERS[kind] ?? NODE_LAYER;

  return (selected || inSelectedGroup) && !NEVER_LIFTED.includes(kind)
    ? layer + SELECTED_LIFT
    : layer;
}

// Whether a group that holds the node of `parentId` (its group, or a group
// that group is in) is selected. `parentOf` gives a node's group id.
function inSelectedGroup(parentId, selected, parentOf) {
  const seen = new Set();

  for (let id = parentId; id && !seen.has(id); id = parentOf(id)) {
    if (selected.has(id)) {
      return true;
    }

    seen.add(id);
  }

  return false;
}

export const SWITCH_HANDLE_ID = 'bus';

/** Device handle that connects on a new interface. */
export const NEW_INTERFACE_HANDLE_ID = 'new-interface';

// Dash patterns give every network a non-color cue as well as a color token.
// A network or a connection given a line style (see LINE_STYLES in
// model.js) is drawn in that pattern instead of the one of its place.
export const NETWORK_PATTERNS = ['solid', 'dashed', 'dotted', 'dash-dot'];
const NETWORK_DASH_ARRAYS = {
  solid: undefined,
  dashed: '8 4',
  dotted: '2 4',
  'dash-dot': '10 4 2 4',
};
const NETWORK_TOKEN_COUNT = 8;

/** The SVG dash array of a line style, or undefined for a solid line. */
export function lineDashArray(style) {
  return NETWORK_DASH_ARRAYS[style];
}

// Ids of the canvas's keyboard hints (BuilderCanvas.vue). Every node and
// connection is described by one of them, in place of Vue Flow's own
// instructions, which describe keys the Builder handles differently.
export const NODE_HINT_ID = 'builder-canvas-node-hint';
export const EDGE_HINT_ID = 'builder-canvas-edge-hint';

/** Id of the hidden text with a node's diagram check results (see NodeIssueMark.vue). */
export function nodeIssueId(nodeId) {
  return `builder-node-issues-${nodeId}`;
}

/** Id of the hidden text with a connection's diagram check results (see NetworkEdge.vue). */
export function edgeIssueId(edgeId) {
  return `builder-edge-issues-${edgeId}`;
}

/** Id of the hidden text with a node's info tooltip (see useNodeInfo in nodes/nodeTooltip.js). */
export function nodeInfoId(nodeId) {
  return `builder-node-info-${nodeId}`;
}

/**
 * The deterministic look of a network. A color that addNetwork picks draws
 * with its token (see colors.js). The pattern is the given line style, else
 * the pattern of its place, which autoPattern always is.
 */
export function networkStyle(doc, networkId) {
  const index = (doc?.networks || []).findIndex(
    (entry) => entry.id === networkId,
  );

  return styleAt(doc?.networks?.[index], networkId, index);
}

// Each network's style by id, calculated once, for a whole document.
function networkStyles(doc) {
  const places = new Map();
  const styles = new Map();

  (doc?.networks || []).forEach((network, index) => {
    if (!places.has(network.id)) {
      places.set(network.id, index);
    }
  });

  return (networkId) => {
    if (!styles.has(networkId)) {
      const index = places.has(networkId) ? places.get(networkId) : -1;

      styles.set(networkId, styleAt(doc?.networks?.[index], networkId, index));
    }

    return styles.get(networkId);
  };
}

// The style of the network at `index` of the document's networks (-1 when
// it has none of that id).
function styleAt(network, networkId, index) {
  const seed = index >= 0 ? index : stableHash(networkId || 'network');
  // The pattern shifts by one every time the colors wrap, so no two of the
  // first 32 networks share both color and pattern.
  const autoPattern =
    NETWORK_PATTERNS[
      (seed + Math.floor(seed / NETWORK_TOKEN_COUNT)) % NETWORK_PATTERNS.length
    ];
  const pattern = LINE_STYLES.includes(network?.lineStyle)
    ? network.lineStyle
    : autoPattern;

  const chosen = networkColorToken(network?.color);

  return {
    token: chosen === -1 ? seed % NETWORK_TOKEN_COUNT : chosen,
    pattern,
    autoPattern,
    dashArray: NETWORK_DASH_ARRAYS[pattern],
    label: network?.name || '',
  };
}

/** Absolute -> parent relative position. */
export function relativePosition(doc, node, find = (id) => findNode(doc, id)) {
  if (!node.parentId) {
    return { ...node.position };
  }

  const parent = find(node.parentId);

  if (!parent) {
    return { ...node.position };
  }

  return {
    x: node.position.x - parent.position.x,
    y: node.position.y - parent.position.y,
  };
}

/** Parent relative -> absolute position. */
export function absolutePosition(doc, parentId, position) {
  const parent = parentId ? findNode(doc, parentId) : null;

  if (!parent) {
    return { x: position.x, y: position.y };
  }

  return {
    x: position.x + parent.position.x,
    y: position.y + parent.position.y,
  };
}

/** A node's handles: one per device interface, or one bus handle for a switch. */
export function handlesFor(doc, node, index = labelIndex(doc)) {
  if (node.kind === 'switch') {
    const network = index.network(node.switch?.networkId);

    return [
      {
        id: SWITCH_HANDLE_ID,
        label: `${network ? network.name : 'switch'} bus`,
        kind: 'bus',
      },
    ];
  }

  if (node.kind !== 'device') {
    return [];
  }

  return deviceHandles(node).map((handle) => {
    const iface = specInterfaceFor(node, handle.id);
    const connection = index.handle(handle.id);
    const network = connection && index.network(connection.networkId);

    return {
      id: handle.id,
      name: handle.name,
      index: handle.index,
      kind: 'interface',
      connected: Boolean(connection),
      networkId: connection?.networkId,
      label: network
        ? `${handle.name} on network ${network.name}`
        : `${handle.name}, not connected`,
      interface: iface,
    };
  });
}

/** The devices connected to a switch, each once, with the addresses of their interfaces to it. */
export function connectedDevices(node, index) {
  const devices = new Map();

  for (const edge of index.links(node.id)) {
    for (const [id, handleId] of [
      [edge.sourceNodeId, edge.sourceHandleId],
      [edge.targetNodeId, edge.targetHandleId],
    ]) {
      const device = id === node.id ? null : index.node(id);

      if (device?.kind !== 'device') {
        continue;
      }

      if (!devices.has(id)) {
        devices.set(id, { label: nodeLabel(device), addresses: [] });
      }

      devices
        .get(id)
        .addresses.push(interfaceAddress(specInterfaceFor(device, handleId)));
    }
  }

  return [...devices.values()];
}

// The texts that describe a node, by id: a device's or a switch's info,
// what the diagram checks found, then the canvas's keys.
function describedBy(node, issue) {
  return [
    (node.kind === 'device' || node.kind === 'switch') && nodeInfoId(node.id),
    issue && nodeIssueId(node.id),
    NODE_HINT_ID,
  ]
    .filter(Boolean)
    .join(' ');
}

/**
 * Converts model nodes into Vue Flow nodes. options: issues by node id, the
 * icon library (its reactive state makes nodes follow it), readOnly, and the
 * viewer's settings iconSize and showDescriptions (on unless false).
 */
export function toFlowNodes(doc, options = {}) {
  const issues = options.issues || new Map();
  const library = options.library || null;
  const readOnly = Boolean(options.readOnly);
  const index = labelIndex(doc);
  const styleOf = networkStyles(doc);

  // Parents must be registered before children in Vue Flow.
  const ordered = [...(doc.nodes || [])].sort((a, b) => {
    const depthA = a.parentId ? 1 : 0;
    const depthB = b.parentId ? 1 : 0;

    if (depthA !== depthB) {
      return depthA - depthB;
    }

    if (a.kind === 'group' && b.kind !== 'group') {
      return -1;
    }

    if (b.kind === 'group' && a.kind !== 'group') {
      return 1;
    }

    return 0;
  });

  return ordered.map((node) => {
    const size = sizeOf(node);
    const issue = issues.get(node.id) || null;

    return {
      id: node.id,
      type: FLOW_NODE_TYPES[node.kind] || FLOW_NODE_TYPES.device,
      position: relativePosition(doc, node, index.node),
      selected: false,
      parentNode: node.parentId || undefined,
      expandParent: Boolean(node.parentId),
      zIndex: nodeZIndex(node.kind),
      // A line takes the pointer only on its stroke (see LineNode.vue). It
      // does not take the pointer across the box of its points, over what
      // is under it.
      style: {
        width: `${size.width}px`,
        height: `${size.height}px`,
        ...(node.kind === 'line' ? { pointerEvents: 'none' } : {}),
      },
      ariaLabel: nodeAriaLabel(doc, node, index),
      // Vue Flow spreads these over its own wrapper attributes. The wrapper
      // is a toggle button, pressed while the node is selected. It takes
      // focus but is out of the Tab order unless it is the canvas's one Tab
      // stop (withTabStop). undefined removes Vue Flow's role description.
      domAttributes: {
        tabindex: -1,
        role: 'button',
        'aria-pressed': 'false',
        'aria-roledescription': undefined,
        'aria-describedby': describedBy(node, issue),
      },
      data: {
        node,
        label: nodeLabel(node),
        iconKey: nodeIconKey(node),
        iconSrc: iconSrc(nodeIcon(node), doc.icons, library),
        iconSize: ICON_SIZE_KINDS.includes(node.kind)
          ? nodeIconSize(doc, node, options.iconSize)
          : undefined,
        comment: drawnNodeComment(node, options.showDescriptions !== false),
        typeLabel: node.kind === 'device' ? deviceTypeLabel(node) : undefined,
        includedFrom: includedFrom(node),
        network:
          node.kind === 'switch'
            ? index.network(node.switch?.networkId)
            : undefined,
        networkStyle:
          node.kind === 'switch' ? styleOf(node.switch?.networkId) : undefined,
        connected:
          node.kind === 'switch' ? connectedDevices(node, index) : undefined,
        handles: handlesFor(doc, node, index),
        dashArray:
          node.kind === 'line'
            ? lineDashArray(node.line?.lineStyle)
            : undefined,
        layerColor: layerOutlineColor(doc, node) || undefined,
        issue,
        readOnly,
      },
    };
  });
}

/**
 * Flow items with a selection applied and stacked for it (nodeZIndex). Only
 * changed items are copied, so Vue Flow redraws only those.
 */
export function withSelection(items, selectedIds = []) {
  const selected = new Set(selectedIds);
  const parents = new Map(items.map((item) => [item.id, item.parentNode]));
  const parentOf = (id) => parents.get(id);

  return items.map((item) => {
    const pressed = selected.has(item.id);
    const kind = item.data?.node?.kind;
    const zIndex = kind
      ? nodeZIndex(
          kind,
          pressed,
          inSelectedGroup(item.parentNode, selected, parentOf),
        )
      : item.zIndex;

    return pressed === item.selected && zIndex === item.zIndex
      ? item
      : {
          ...item,
          selected: pressed,
          ...(kind ? { zIndex } : {}),
          domAttributes: {
            ...item.domAttributes,
            'aria-pressed': String(pressed),
          },
        };
  });
}

/** Flow items with the canvas's one roving Tab stop on id. Only that item is copied. */
export function withTabStop(items, id) {
  if (!id) {
    return items;
  }

  return items.map((item) =>
    item.id === id
      ? { ...item, domAttributes: { ...item.domAttributes, tabindex: 0 } }
      : item,
  );
}

/**
 * What Vue Flow must change between two lists of flow items: added, changed,
 * removed and reordered. rebuild asks for the whole list when ids repeat or a
 * fixed key changed, as Vue Flow computes those only for a whole list.
 */
export function flowChanges(previous, next, fixed = []) {
  const before = new Map(previous.map((item) => [item.id, item]));
  const ids = new Set(next.map((item) => item.id));
  const added = [];
  const changed = [];
  let rebuild = before.size !== previous.length || ids.size !== next.length;

  for (const item of next) {
    const old = before.get(item.id);

    if (!old) {
      added.push(item);
    } else if (old !== item) {
      changed.push(item);
      rebuild ||= fixed.some((key) => old[key] !== item[key]);
    }
  }

  const removed = previous
    .filter((item) => !ids.has(item.id))
    .map((item) => item.id);
  const order = [...previous.filter((item) => ids.has(item.id)), ...added];

  return {
    added,
    changed,
    removed,
    reordered: order.some((item, index) => item.id !== next[index]?.id),
    rebuild,
  };
}

/** Accessible name for a canvas node (see nodeAccessibleName). */
export function nodeAriaLabel(doc, node, index) {
  return nodeAccessibleName(doc, node, index);
}

// Where a connection meets a node's side, in canvas coordinates.
function anchorY(node, handleId) {
  return (node.position?.y || 0) + handleOffsetY(node, handleId);
}

/**
 * The bend of each connection that shares a switch end with others, by edge
 * id. Into a switch, the connection with the nearest other end bends first,
 * and out of one it bends last, so no line crosses another.
 */
export function edgeLanes(doc, nodeById) {
  const nodes =
    nodeById || new Map((doc.nodes || []).map((node) => [node.id, node]));
  const hubs = new Map();

  for (const edge of doc.edges || []) {
    const source = nodes.get(edge.sourceNodeId);
    const target = nodes.get(edge.targetNodeId);
    const key =
      (target?.kind === 'switch' && `in:${target.id}`) ||
      (source?.kind === 'switch' && `out:${source.id}`) ||
      '';

    if (!key || !source || !target) {
      continue;
    }

    const rise = Math.abs(
      anchorY(target, edge.targetHandleId) -
        anchorY(source, edge.sourceHandleId),
    );

    if (!hubs.has(key)) {
      hubs.set(key, []);
    }

    hubs.get(key).push({ id: edge.id, rise });
  }

  const lanes = new Map();

  hubs.forEach((list, key) => {
    if (list.length < 2) {
      return;
    }

    const out = key.startsWith('out:');

    list
      .sort(
        (a, b) =>
          (out ? b.rise - a.rise : a.rise - b.rise) ||
          (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
      )
      .forEach((entry, index) => {
        lanes.set(entry.id, { index, count: list.length });
      });
  });

  return lanes;
}

/**
 * Converts model edges into Vue Flow edges. data.style is a connection's own
 * line style, and data.issue what the diagram checks found.
 */
export function toFlowEdges(doc, options = {}) {
  const issues = options.issues || new Map();
  // The first of an id, as findNode and findNetwork find it.
  const nodeById = new Map();

  for (const node of doc.nodes || []) {
    if (!nodeById.has(node.id)) {
      nodeById.set(node.id, node);
    }
  }

  const networks = new Map();

  for (const network of doc.networks || []) {
    if (!networks.has(network.id)) {
      networks.set(network.id, network);
    }
  }

  const styleOf = networkStyles(doc);
  const lanes = edgeLanes(doc, nodeById);

  return (doc.edges || []).map((edge) => {
    const shared = styleOf(edge.networkId);
    // A copy: the network's style is shared by its other connections.
    const style = LINE_STYLES.includes(edge.lineStyle)
      ? {
          ...shared,
          pattern: edge.lineStyle,
          dashArray: NETWORK_DASH_ARRAYS[edge.lineStyle],
        }
      : shared;
    const source = nodeById.get(edge.sourceNodeId);
    const target = nodeById.get(edge.targetNodeId);
    const network = networks.get(edge.networkId);
    const labelled =
      edge.label && edge.label !== network?.name
        ? `, labeled ${edge.label}`
        : '';
    const issue = issues.get(edge.id) || null;

    return {
      id: edge.id,
      type: 'builderNetwork',
      source: edge.sourceNodeId,
      target: edge.targetNodeId,
      sourceHandle: edge.sourceHandleId || SWITCH_HANDLE_ID,
      targetHandle: edge.targetHandleId || SWITCH_HANDLE_ID,
      selected: false,
      label: edge.label || network?.name || '',
      // Each device end names its interface, because two connections
      // between the same nodes differ only there.
      ariaLabel:
        `Network ${network ? network.name : 'unassigned'} from ` +
        `${source ? connectionEndLabel(source, edge.sourceHandleId) : edge.sourceNodeId} to ` +
        `${target ? connectionEndLabel(target, edge.targetHandleId) : edge.targetNodeId}${labelled}`,
      // Vue Flow writes tabIndex in camel case, which an SVG element
      // ignores, so connections could never take focus. Like a node, a
      // connection is a toggle button, pressed while it is selected. It is
      // out of the Tab order unless it is the canvas's Tab stop.
      domAttributes: {
        tabindex: -1,
        role: 'button',
        'aria-pressed': 'false',
        'aria-roledescription': undefined,
        'aria-describedby': issue
          ? `${edgeIssueId(edge.id)} ${EDGE_HINT_ID}`
          : EDGE_HINT_ID,
      },
      data: { edge, network, style, lane: lanes.get(edge.id) || null, issue },
    };
  });
}

/** Normalizes a Vue Flow connection into a model one. A switch's bus handle is dropped. */
export function fromFlowConnection(connection = {}) {
  const normalizeHandle = (handle) =>
    !handle || handle === SWITCH_HANDLE_ID || handle === NEW_INTERFACE_HANDLE_ID
      ? null
      : handle;

  return {
    sourceNodeId: connection.source,
    targetNodeId: connection.target,
    sourceHandleId: normalizeHandle(connection.sourceHandle),
    targetHandleId: normalizeHandle(connection.targetHandle),
  };
}

// --- keeping keyboard focus in view (WCAG 2.4.11) ---------------------------
//
// Boxes are {left, top, right, bottom} in screen pixels, like a DOMRect.

function overlapArea(a, b) {
  const width = Math.min(a.right, b.right) - Math.max(a.left, b.left);
  const height = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);

  return width > 0 && height > 0 ? width * height : 0;
}

/** The square pixels of a box outside the pane or under an overlay, 0 when it is in view. */
export function hiddenArea(box, pane, overlays = []) {
  return (
    (box.right - box.left) * (box.bottom - box.top) -
    overlapArea(box, pane) +
    overlays.reduce((sum, overlay) => sum + overlapArea(box, overlay), 0)
  );
}

// Candidate centres per axis, minus one.
const REVEAL_STEPS = 8;

/**
 * Where to center an item so it shows: the grid point nearest the pane's
 * center that keeps it in the pane and clear of overlays, else the point that
 * hides the least of it.
 */
export function freeSpot(pane, width, height, overlays = []) {
  let best = null;

  for (let i = 0; i <= REVEAL_STEPS; i += 1) {
    for (let j = 0; j <= REVEAL_STEPS; j += 1) {
      const x = width / 2 + ((pane.width - width) * i) / REVEAL_STEPS;
      const y = height / 2 + ((pane.height - height) * j) / REVEAL_STEPS;
      const box = {
        left: pane.left + x - width / 2,
        right: pane.left + x + width / 2,
        top: pane.top + y - height / 2,
        bottom: pane.top + y + height / 2,
      };
      const hidden = hiddenArea(box, pane, overlays);
      const distance = Math.hypot(x - pane.width / 2, y - pane.height / 2);

      if (
        !best ||
        hidden < best.hidden ||
        (hidden === best.hidden && distance < best.distance)
      ) {
        best = { x, y, hidden, distance };
      }
    }
  }

  return { x: best.x, y: best.y, hidden: best.hidden };
}

// --- moving keyboard focus between nodes ------------------------------------
//
// The canvas is one Tab stop (withTabStop). The arrow keys move focus to the
// nearest node in their direction, and Page Down and Page Up through a
// node's connections. Points are in flow coordinates.

/** A node's point for the arrow keys: its center, or a group's title, so Up reaches the group. */
export function nodePoint(node) {
  const { width, height } = sizeOf(node);
  const x = (node.position?.x || 0) + width / 2;
  const top = node.position?.y || 0;

  return { x, y: node.kind === 'group' ? top : top + height / 2 };
}

/** A node's or connection's point for the arrow keys. A connection's is between its ends. */
export function itemPoint(doc, { kind, id }) {
  if (kind === 'nodes') {
    const node = findNode(doc, id);

    return node ? nodePoint(node) : null;
  }

  const edge = (doc.edges || []).find((entry) => entry.id === id);
  // Devices and switches, so nodePoint is their centre.
  const ends = [edge?.sourceNodeId, edge?.targetNodeId]
    .map((end) => end && findNode(doc, end))
    .filter(Boolean)
    .map(nodePoint);

  if (!ends.length) {
    return null;
  }

  return {
    x: ends.reduce((sum, point) => sum + point.x, 0) / ends.length,
    y: ends.reduce((sum, point) => sum + point.y, 0) / ends.length,
  };
}

// Each arrow key's distance along it and across it, from an offset.
const ARROW_AXES = {
  ArrowRight: ({ x, y }) => [x, Math.abs(y)],
  ArrowLeft: ({ x, y }) => [-x, Math.abs(y)],
  ArrowDown: ({ x, y }) => [y, Math.abs(x)],
  ArrowUp: ({ x, y }) => [-y, Math.abs(x)],
};

// A step across the arrow's direction counts this many steps along it, so
// a node in line comes before a nearer one off to the side.
const ACROSS_WEIGHT = 2;

/**
 * The node an arrow key moves focus to: the nearest in that direction, with
 * cross distance counted twice. A node at the same point comes first, in
 * document order, so the keys reach every node.
 */
export function nodeInDirection(doc, from, key, fromId) {
  const measure = ARROW_AXES[key];

  if (!measure || !from) {
    return null;
  }

  const nodes = doc.nodes || [];
  const at = fromId ? nodes.findIndex((node) => node.id === fromId) : -1;
  const forward = key === 'ArrowRight' || key === 'ArrowDown';
  let best = null;

  nodes.forEach((node, index) => {
    if (node.id === fromId) {
      return;
    }

    const point = nodePoint(node);
    const [along, across] = measure({
      x: point.x - from.x,
      y: point.y - from.y,
    });
    const samePoint = along === 0 && across === 0;

    if (
      along <= 0 &&
      !(samePoint && at >= 0 && (forward ? index > at : index < at))
    ) {
      return;
    }

    const score = along + ACROSS_WEIGHT * across;

    if (
      !best ||
      score < best.score ||
      (samePoint && !forward && score === best.score)
    ) {
      best = { id: node.id, score };
    }
  });

  return best?.id ?? null;
}

/** The node nearest a point, in any direction. */
export function nearestNode(doc, point) {
  let best = null;

  for (const node of doc.nodes || []) {
    const at = nodePoint(node);
    const distance = Math.hypot(at.x - point.x, at.y - point.y);

    if (!best || distance < best.distance) {
      best = { id: node.id, distance };
    }
  }

  return best?.id ?? null;
}

/**
 * The connection Page Down (1) or Page Up (-1) moves to among a node's
 * connections, in document order with wrap, or null for none.
 */
export function connectionStep(doc, nodeId, edgeId, step) {
  const edges = (doc.edges || []).filter(
    (edge) => edge.sourceNodeId === nodeId || edge.targetNodeId === nodeId,
  );

  if (!edges.length) {
    return null;
  }

  const at = edges.findIndex((edge) => edge.id === edgeId);
  const next =
    at === -1
      ? step > 0
        ? 0
        : edges.length - 1
      : (at + step + edges.length) % edges.length;

  return edges[next].id;
}

// --- fitting nodes into view ------------------------------------------------

// The space that Vue Flow's fit view keeps around a diagram, as a share of
// its size.
const FIT_PADDING = 0.1;

/**
 * The zoom at which Vue Flow's fit view shows a box whole in a pane, with
 * padding as Vue Flow takes it. Infinity for no size, 0 for no space left.
 */
export function fitZoom(bounds, pane, padding = FIT_PADDING) {
  if (!bounds?.width || !bounds?.height || !pane?.width || !pane?.height) {
    return Infinity;
  }

  if (typeof padding === 'object') {
    const width = pane.width - padding.left - padding.right;
    const height = pane.height - padding.top - padding.bottom;

    return width > 0 && height > 0
      ? Math.min(width / bounds.width, height / bounds.height)
      : 0;
  }

  // Vue Flow's own calculation, which rounds the padding down to whole
  // pixels.
  const room = (length) =>
    length - 2 * Math.floor((length - length / (1 + padding)) * 0.5);

  return Math.min(
    room(pane.width) / bounds.width,
    room(pane.height) / bounds.height,
  );
}

/**
 * The pixel padding for fit view: Vue Flow's own, plus room on one side of
 * each overlay (the minimap, the zoom controls), the side that lets the
 * nodes be larger, so no node goes under one.
 */
export function fitPadding(bounds, pane, overlays = []) {
  const share = (length) =>
    Math.floor((length - length / (1 + FIT_PADDING)) * 0.5);
  const base = {
    top: share(pane.height),
    right: share(pane.width),
    bottom: share(pane.height),
    left: share(pane.width),
  };
  // Each box's two ways to be kept clear: [side, pixels from that edge].
  const ways = overlays
    .filter((box) => overlapArea(box, pane) > 0)
    .map((box) => [
      box.top + box.bottom > pane.top + pane.bottom
        ? ['bottom', pane.bottom - box.top]
        : ['top', box.bottom - pane.top],
      box.left + box.right > pane.left + pane.right
        ? ['right', pane.right - box.left]
        : ['left', box.right - pane.left],
    ]);
  let best = { room: base, zoom: 0 };

  for (let pick = 0; pick < 2 ** ways.length; pick += 1) {
    const extra = { top: 0, right: 0, bottom: 0, left: 0 };

    ways.forEach((choices, index) => {
      const [side, pixels] = choices[(pick >> index) & 1];

      extra[side] = Math.max(extra[side], Math.ceil(pixels));
    });

    const room = {
      top: base.top + extra.top,
      right: base.right + extra.right,
      bottom: base.bottom + extra.bottom,
      left: base.left + extra.left,
    };
    const zoom = fitZoom(bounds, pane, room);

    if (zoom > best.zoom) {
      best = { room, zoom };
    }
  }

  return best.room;
}

/**
 * The least zoom: least, or half the zoom that fits the diagram clear of the
 * overlays when that is less, so Fit can always show a large diagram whole.
 */
export function zoomFloor(bounds, pane, least, overlays = []) {
  const box = { left: 0, top: 0, right: pane.width, bottom: pane.height };
  const clear =
    overlays.length > 0 &&
    fitZoom(bounds, pane, fitPadding(bounds, { ...pane, ...box }, overlays));

  // Padding that leaves no space to show the diagram in sets no floor.
  return Math.min(least, (clear || fitZoom(bounds, pane)) / 2);
}

// Below this zoom, the text of a node is too small to read, so Go to node
// zooms in (see readableZoom).
export const READABLE_ZOOM = 0.6;

/** The zoom Go to node uses: the current one from READABLE_ZOOM up, else 100% or openZoom, capped at most. */
export function readableZoom(zoom, openZoom = 1, most = Infinity) {
  if (!(zoom < READABLE_ZOOM)) {
    return zoom;
  }

  return Math.min(most, Math.max(1, openZoom));
}

/** The viewport that puts the center of bounds at spot (pane pixels) at a zoom. */
export function centeredViewport(bounds, spot, zoom) {
  return {
    x: spot.x - (bounds.x + bounds.width / 2) * zoom,
    y: spot.y - (bounds.y + bounds.height / 2) * zoom,
    zoom,
  };
}

// --- Fit, and back to the view before it ------------------------------------
// After Fit, Fit again restores the view from before it, until the view
// changes another way. Fit keeps null or {before, fitted}, Vue Flow viewports.

// Less than half a pixel of pan, and a thousandth of zoom, is no change.
function sameView(a, b) {
  return (
    Math.abs(a.x - b.x) < 0.5 &&
    Math.abs(a.y - b.y) < 0.5 &&
    Math.abs(a.zoom - b.zoom) < 0.001
  );
}

/** What Fit keeps after it moved the view, or null when it did not move it. */
export function keepFit(before, fitted) {
  if (sameView(before, fitted)) {
    return null;
  }

  const view = ({ x, y, zoom }) => ({ x, y, zoom });

  return { before: view(before), fitted: view(fitted) };
}

/** What Fit keeps at view: the same while it is the view Fit made, else null. */
export function keptFit(kept, view) {
  return kept && sameView(kept.fitted, view) ? kept : null;
}
