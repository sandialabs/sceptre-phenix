// Vue Flow adapter.
//
// The document model is library independent. This module is the only place
// that knows Vue Flow's node/edge shape. Positions in the model are
// absolute. Vue Flow expects child positions relative to their parent.
//
// Networks (not edges) own identity here. An edge's appearance comes from
// the network it belongs to. Every network gets a stroke pattern and a
// color, so color alone never shows network membership.

import { kindMeta, nodeIcon, nodeIconKey } from '../catalog.js';
import { networkColorToken } from '../colors.js';
import { iconSrc } from '../icons.js';
import { stableHash } from '../ids.js';
import {
  connectionEndLabel,
  deviceHandles,
  deviceTypeLabel,
  DRAWING_KINDS,
  findNode,
  ICON_SIZE_KINDS,
  includedFrom,
  kindLabel,
  LINE_STYLES,
  nodeComment,
  nodeIconSize,
  nodeLabel,
  sizeOf,
  specInterfaceFor,
} from '../model.js';
import { interfaceAddress } from '../nodeInfo.js';
import { labelIndex, outlineLabel } from '../outline.js';
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

// The stacking of each kind on the canvas: groups at the bottom, the shapes
// and lines drawn over them, then devices, switches, notes and icons, so a
// drawing never covers a node it is drawn around.
//
// Vue Flow draws a node in a group one layer above the higher of its
// group's layer and its own. So a node's layer is far enough above a
// drawing's layer that a drawing in a group stays under the devices outside
// it.
const KIND_LAYERS = { group: 0, shape: 0, line: 0 };
const NODE_LAYER = 10;
// A selected node, and every node in a selected group, goes above the
// other nodes, as with Vue Flow's own elevateNodesOnSelect. The canvas
// disables that option (see BuilderCanvas.vue). A shape or a line never
// goes above: when selected, it would cover the devices and switches it is
// drawn around, and their connection handles. Its resize handles, and the
// handles of a line's points, are drawn over every node instead (see
// NodeResize.vue and LineNode.vue).
const SELECTED_LIFT = 1000;
const NEVER_LIFTED = ['shape', 'line'];

/**
 * A canvas node's stacking order (Vue Flow's zIndex): its kind's layer. The
 * node goes above the other nodes while it, or a group it is in, is
 * selected, unless it is a shape or a line.
 *
 * @param {string} kind the node's kind
 * @param {boolean} [selected] the node is selected
 * @param {boolean} [inSelectedGroup] a group it is in is selected
 * @returns {number}
 */
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

/**
 * The SVG dash array of a line style, as a connection is drawn in it.
 *
 * @param {string} [style] one of LINE_STYLES; solid for any other
 * @returns {string|undefined} undefined for a solid line
 */
export function lineDashArray(style) {
  return NETWORK_DASH_ARRAYS[style];
}

// Ids of the canvas's keyboard hints (BuilderCanvas.vue). Every node and
// connection is described by one of them, in place of Vue Flow's own
// instructions, which describe keys the Builder handles differently.
export const NODE_HINT_ID = 'builder-canvas-node-hint';
export const EDGE_HINT_ID = 'builder-canvas-edge-hint';

/**
 * Id of the hidden text that says what the diagram checks found about a
 * node (see NodeIssueMark.vue), which describes the node.
 *
 * @param {string} nodeId
 * @returns {string}
 */
export function nodeIssueId(nodeId) {
  return `builder-node-issues-${nodeId}`;
}

/**
 * Id of the hidden text that says what a device's or a switch's info
 * tooltip shows (see useNodeInfo in nodes/nodeTooltip.js), which describes
 * the node.
 *
 * @param {string} nodeId
 * @returns {string}
 */
export function nodeInfoId(nodeId) {
  return `builder-node-info-${nodeId}`;
}

/**
 * Deterministic visual treatment for a network. Documents with the same
 * networks always render identically. A network whose color is one that
 * addNetwork picks is drawn with that color's token (see colors.js). For a
 * network that got its color in turn, that is the token of its place. Its
 * pattern is the line style it was given, else the pattern of its place.
 * `autoPattern` is always the pattern of its place.
 *
 * @param {object} doc
 * @param {string} networkId
 * @returns {{token: number, pattern: string, autoPattern: string,
 *   dashArray: string|undefined, label: string, color: string,
 *   alias: number|undefined}}
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
    color: network?.color || '',
    alias: network?.alias,
  };
}

/**
 * Absolute -> parent relative position.
 *
 * @param {object} doc
 * @param {object} node
 * @param {Function} [find] looks a node up by id
 * @returns {{x: number, y: number}}
 */
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

/**
 * Parent relative -> absolute position.
 *
 * @param {object} doc
 * @param {string} parentId
 * @param {{x: number, y: number}} position
 * @returns {{x: number, y: number}}
 */
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

/**
 * Handles exposed by a node: one per device interface handle, and a single bus
 * handle for switches.
 *
 * @param {object} doc
 * @param {object} node
 * @param {object} [index] labelIndex(doc), when the handles of many nodes
 *   are wanted
 * @returns {{id: string, label: string, kind: string}[]}
 */
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

/**
 * The devices connected to a switch node, each once, with the address of
 * the interface each of its connections to the switch uses (see
 * interfaceAddress), in the order of the connections. A switch's info
 * tooltip lists them.
 *
 * @param {object} node switch node
 * @param {object} index labelIndex(doc)
 * @returns {{label: string, addresses: string[]}[]}
 */
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
 * Converts model nodes into Vue Flow nodes. The data of each node carries:
 * - issue: for a node that the diagram checks flag, what they found. It
 *   also describes the node.
 * - typeLabel: for a device, the type it shows.
 * - connected: for a switch, the devices connected to it.
 * - iconSrc: for a device, a group or an icon node whose custom icon
 *   resolves (to the document's copy or to the icon library's icon of that
 *   name), the address the icon is drawn from (see iconSrc in icons.js).
 *   It is '' otherwise, and the node draws its built-in icon.
 * - iconSize: for a device, a switch and a group, the size their icon is
 *   drawn at (see nodeIconSize in model.js).
 * - kindLabel: for a shape, an icon and a line, the name of their kind.
 * - dashArray: for a line, the dash array of its line style.
 *
 * @param {object} doc
 * @param {object} [options] selectedIds. issues: nodeIssueSummaries by
 *   node id. library: the icon library (see iconLibrary.js), whose reactive
 *   state makes nodes follow its changes. Without a library, only the
 *   document's copies resolve
 * @returns {object[]}
 */
export function toFlowNodes(doc, options = {}) {
  const selected = new Set(options.selectedIds || []);
  const issues = options.issues || new Map();
  const library = options.library || null;
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
      selected: selected.has(node.id),
      parentNode: node.parentId || undefined,
      expandParent: Boolean(node.parentId),
      zIndex: nodeZIndex(
        node.kind,
        selected.has(node.id),
        inSelectedGroup(
          node.parentId,
          selected,
          (id) => index.node(id)?.parentId,
        ),
      ),
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
        'aria-pressed': String(selected.has(node.id)),
        'aria-roledescription': undefined,
        'aria-describedby': describedBy(node, issue),
      },
      data: {
        node,
        label: nodeLabel(node),
        iconKey: nodeIconKey(node),
        iconSrc: iconSrc(nodeIcon(node), doc.icons, library),
        iconSize: ICON_SIZE_KINDS.includes(node.kind)
          ? nodeIconSize(doc, node)
          : undefined,
        shape: kindMeta(node.kind).shape,
        comment: nodeComment(node),
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
        kindLabel: DRAWING_KINDS.includes(node.kind)
          ? kindLabel(node)
          : undefined,
        dashArray:
          node.kind === 'line'
            ? lineDashArray(node.line?.lineStyle)
            : undefined,
        issue,
      },
    };
  });
}

/**
 * Flow nodes or edges with a selection applied, and each node stacked for
 * that selection (nodeZIndex). Only the items that the selection changes
 * are copied. The other items stay the same objects, so Vue Flow redraws
 * only what the selection changed.
 *
 * @param {object[]} items from toFlowNodes or toFlowEdges
 * @param {string[]} [selectedIds]
 * @returns {object[]}
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

/**
 * Flow nodes or edges with the canvas's one Tab stop applied (a roving
 * tabindex): the item of `id` takes tabindex 0. Only that item is copied,
 * as in withSelection.
 *
 * @param {object[]} items from toFlowNodes or toFlowEdges
 * @param {string|null} [id]
 * @returns {object[]}
 */
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
 * What Vue Flow has to change to go from one list of flow nodes (or edges)
 * to the next: the items to add, the kept items that changed and the ids
 * that left. An unchanged item is the same object in both lists (see
 * withSelection and keepUnchanged), so the work is the size of the change.
 * `reordered` is set when the next list is not the kept items in their
 * old order followed by the added ones. `rebuild` asks for the whole list
 * instead. This occurs when ids repeat, or when a kept item changed one of
 * the `fixed` keys, which Vue Flow calculates only for a whole list.
 *
 * @param {object[]} previous
 * @param {object[]} next
 * @param {string[]} [fixed] keys whose change needs a rebuild
 * @returns {{added: object[], changed: object[], removed: string[],
 *   reordered: boolean, rebuild: boolean}}
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

/**
 * Accessible name for a canvas node: the node's outline row name, so the
 * canvas and the outline name each node the same way.
 *
 * @param {object} doc
 * @param {object} node
 * @param {object} [index] labelIndex(doc), when naming many nodes
 * @returns {string}
 */
export function nodeAriaLabel(doc, node, index) {
  return outlineLabel(doc, node, index);
}

// Where a connection meets a node's side, in canvas coordinates.
function anchorY(node, handleId) {
  return (node.position?.y || 0) + handleOffsetY(node, handleId);
}

/**
 * The bend of each connection that shares its end at a switch with other
 * connections, as {index, count}. A connection drawn without a route bends
 * in a column of its own (see NetworkEdge.vue), so the lines into one
 * switch do not run down one column. Into a switch, the connection whose
 * other end is nearest the switch's handle, up or down, bends first
 * (leftmost), so no line crosses another on its way. Out of a switch, it
 * bends last.
 *
 * @param {object} doc
 * @param {Map} [nodeById]
 * @returns {Map<string, {index: number, count: number}>} by edge id, for the
 *   connections that share such an end
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
 * Converts model edges into Vue Flow edges. A connection given a line style
 * of its own is drawn in it (data.style), in place of its network's.
 *
 * @param {object} doc
 * @param {object} [options] selectedIds
 * @returns {object[]}
 */
export function toFlowEdges(doc, options = {}) {
  const selected = new Set(options.selectedIds || []);
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
        ? `, labelled ${edge.label}`
        : '';

    return {
      id: edge.id,
      type: 'builderNetwork',
      source: edge.sourceNodeId,
      target: edge.targetNodeId,
      sourceHandle: edge.sourceHandleId || SWITCH_HANDLE_ID,
      targetHandle: edge.targetHandleId || SWITCH_HANDLE_ID,
      selected: selected.has(edge.id),
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
        'aria-pressed': String(selected.has(edge.id)),
        'aria-roledescription': undefined,
        'aria-describedby': EDGE_HINT_ID,
      },
      data: { edge, network, style, lane: lanes.get(edge.id) || null },
    };
  });
}

/**
 * Normalizes a Vue Flow connection event into a model connection. The bus
 * handle of a switch is not a document handle, so it is dropped.
 *
 * @param {object} connection
 * @returns {{sourceNodeId: string, targetNodeId: string,
 *   sourceHandleId: string|null, targetHandleId: string|null}}
 */
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

/**
 * How much of a box is hidden: outside the pane, or under an overlay that
 * floats over the pane (the minimap, the zoom controls, a notice).
 *
 * @param {object} box
 * @param {object} pane
 * @param {object[]} overlays
 * @returns {number} square pixels. 0 when the box is fully in view
 */
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
 * Where to put the centre of an item of the given size so it can be seen.
 * Of the points on a grid over the pane, it is the point nearest the pane's
 * centre where the item is inside the pane and clear of the overlays. If
 * there is no such point, it is the point where the least of the item is
 * hidden.
 *
 * @param {object} pane box with width and height
 * @param {number} width
 * @param {number} height
 * @param {object[]} overlays
 * @returns {{x: number, y: number, hidden: number}} x and y relative to the
 *   pane's top left corner
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

/**
 * Where a node is, for the arrow keys: its centre, or, for a group, the
 * middle of its top edge, where its title is, so Up from a member reaches
 * the group.
 *
 * @param {object} node
 * @returns {{x: number, y: number}}
 */
export function nodePoint(node) {
  const { width, height } = sizeOf(node);
  const x = (node.position?.x || 0) + width / 2;
  const top = node.position?.y || 0;

  return { x, y: node.kind === 'group' ? top : top + height / 2 };
}

/**
 * Where a node or connection is, for the arrow keys. For a node, see
 * nodePoint. A connection is halfway between the centres of its ends.
 *
 * @param {object} doc
 * @param {{kind: 'nodes'|'edges', id: string}} item
 * @returns {{x: number, y: number}|null}
 */
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
 * The node an arrow key moves focus to: of the nodes whose point
 * (nodePoint) is in that direction from `from`, the nearest, with distance
 * across the direction counted twice. A node at the same point as the
 * focused node comes first: the next in the document for Right and Down,
 * the previous for Left and Up. So the keys can reach every node.
 *
 * @param {object} doc
 * @param {{x: number, y: number}} from
 * @param {string} key ArrowUp, ArrowDown, ArrowLeft or ArrowRight
 * @param {string} [fromId] the focused node, which is never the answer
 * @returns {string|null} the node's id, or null when none lies that way
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

/**
 * The node nearest a point, in any direction.
 *
 * @param {object} doc
 * @param {{x: number, y: number}} point
 * @returns {string|null}
 */
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
 * The connection that Page Down (step 1) or Page Up (-1) moves focus to
 * among a node's connections. The order is the document's, and it wraps
 * from the last to the first. The step starts from `edgeId`, or from the
 * node itself when `edgeId` is not one of them.
 *
 * @param {object} doc
 * @param {string} nodeId
 * @param {string|null} edgeId
 * @param {1|-1} step
 * @returns {string|null} null when the node has no connections
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
 * the space it keeps around it.
 *
 * @param {{width: number, height: number}} bounds in flow coordinates
 * @param {{width: number, height: number}} pane in screen pixels
 * @param {number|{top: number, right: number, bottom: number, left: number}}
 *   [padding] as Vue Flow takes it: a share of the pane, or pixels on each
 *   side (see fitPadding)
 * @returns {number} Infinity when either has no size. 0 when the padding
 *   leaves no space to show the box in
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
 * The padding, in whole pixels on each side, to fit nodes into a pane
 * with. It is Vue Flow's own padding (a share of the pane, as in fitZoom),
 * and more on a side for each box that floats over the pane (the minimap,
 * the zoom controls). So no node goes under a box. Each box is kept clear
 * on one of the two sides it is against, the one that lets the nodes be
 * larger. Vue Flow's fit view takes the result as its padding, in pixels.
 *
 * @param {{width: number, height: number}} bounds the nodes', in flow
 *   coordinates
 * @param {object} pane box, in screen pixels
 * @param {object[]} [overlays] boxes, in screen pixels
 * @param {number} [padding] Vue Flow's padding, as a share
 * @returns {{top: number, right: number, bottom: number, left: number}}
 */
export function fitPadding(bounds, pane, overlays = [], padding = FIT_PADDING) {
  const share = (length) => Math.floor((length - length / (1 + padding)) * 0.5);
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
 * The least zoom the canvas allows: `least`, or half the zoom that fits
 * the diagram clear of the boxes that float over the pane, when that is
 * less. So Fit, and zoom out by hand, can always show a large diagram
 * whole.
 *
 * @param {{width: number, height: number}} bounds the diagram's
 * @param {{width: number, height: number}} pane
 * @param {number} least
 * @param {object[]} [overlays] boxes over the pane (the minimap), in the
 *   pane's own pixels. Its top left corner is 0, 0
 * @returns {number}
 */
export function zoomFloor(bounds, pane, least, overlays = []) {
  const box = { left: 0, top: 0, right: pane.width, bottom: pane.height };
  const clear =
    overlays.length > 0 &&
    fitZoom(bounds, pane, fitPadding(bounds, { ...pane, ...box }, overlays));

  // Padding that leaves no space to show the diagram in sets no floor.
  return Math.min(least, (clear || fitZoom(bounds, pane)) / 2);
}

// --- Fit, and back to the view before it ------------------------------------
//
// After Fit, the zoom controls' Fit button (and the view.fit command)
// restores the view from just before Fit. This works until the view
// changes in some other way: a pan or a zoom, the minimap, Reset view, or
// a node brought into view. What Fit keeps is null, or {before, fitted}:
// the view to restore and the view Fit made. Views are {x, y, zoom}, as
// Vue Flow's viewport.

// Less than half a pixel of pan, and a thousandth of zoom, is no change.
function sameView(a, b) {
  return (
    Math.abs(a.x - b.x) < 0.5 &&
    Math.abs(a.y - b.y) < 0.5 &&
    Math.abs(a.zoom - b.zoom) < 0.001
  );
}

/**
 * What Fit keeps after it changed the view from `before` to `fitted`.
 * Nothing when it did not change the view, because there is no view to go
 * back to.
 *
 * @param {{x: number, y: number, zoom: number}} before
 * @param {{x: number, y: number, zoom: number}} fitted
 * @returns {{before: object, fitted: object}|null}
 */
export function keepFit(before, fitted) {
  if (sameView(before, fitted)) {
    return null;
  }

  const view = ({ x, y, zoom }) => ({ x, y, zoom });

  return { before: view(before), fitted: view(fitted) };
}

/**
 * What Fit keeps when the view is `view`: the same while it is still the
 * view Fit made, and nothing after any other change moved it.
 *
 * @param {{before: object, fitted: object}|null} kept
 * @param {{x: number, y: number, zoom: number}} view
 * @returns {{before: object, fitted: object}|null}
 */
export function keptFit(kept, view) {
  return kept && sameView(kept.fitted, view) ? kept : null;
}
