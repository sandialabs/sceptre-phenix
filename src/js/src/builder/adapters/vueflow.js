// Vue Flow adapter.
//
// The document model is library independent; this module is the only place that
// knows Vue Flow's node/edge shape. Positions in the model are absolute, Vue
// Flow expects child positions relative to their parent.
//
// Networks (not edges) own identity here: an edge's appearance is derived from
// the network it belongs to, and every network gets a stroke pattern as well as
// a color so network membership is never communicated by color alone.

import { kindMeta, nodeIconKey } from '../catalog.js';
import { networkColorToken } from '../colors.js';
import { stableHash } from '../ids.js';
import {
  deviceHandles,
  findNetwork,
  findNode,
  includedFrom,
  networkOfSwitch,
  nodeComment,
  nodeLabel,
  sizeOf,
  specInterfaceFor,
} from '../model.js';
import { labelIndex, outlineLabel } from '../outline.js';
import { handleOffsetY } from '../routes.js';

export const FLOW_NODE_TYPES = {
  device: 'builderDevice',
  switch: 'builderSwitch',
  note: 'builderNote',
  group: 'builderGroup',
};

export const SWITCH_HANDLE_ID = 'bus';

/** Device handle that connects on a new interface. */
export const NEW_INTERFACE_HANDLE_ID = 'new-interface';

// Dash patterns give every network a non-color cue as well as a color token.
export const NETWORK_PATTERNS = ['solid', 'dashed', 'dotted', 'dash-dot'];
const NETWORK_DASH_ARRAYS = {
  solid: undefined,
  dashed: '8 4',
  dotted: '2 4',
  'dash-dot': '10 4 2 4',
};
const NETWORK_TOKEN_COUNT = 8;

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
 * Deterministic visual treatment for a network. Documents with the same
 * networks always render identically. A network whose color is one
 * addNetwork picks is drawn with that color's token (see colors.js), which
 * for a network given its color in turn is the token of its place.
 *
 * @param {object} doc
 * @param {string} networkId
 * @returns {{token: number, pattern: string, dashArray: string|undefined,
 *   label: string, color: string, alias: number|undefined}}
 */
export function networkStyle(doc, networkId) {
  const network = findNetwork(doc, networkId);
  const index = (doc?.networks || []).findIndex(
    (entry) => entry.id === networkId,
  );
  const seed = index >= 0 ? index : stableHash(networkId || 'network');
  // The pattern shifts by one every time the colors wrap, so no two of the
  // first 32 networks share both color and pattern.
  const pattern =
    NETWORK_PATTERNS[
      (seed + Math.floor(seed / NETWORK_TOKEN_COUNT)) % NETWORK_PATTERNS.length
    ];

  const chosen = networkColorToken(network?.color);

  return {
    token: chosen === -1 ? seed % NETWORK_TOKEN_COUNT : chosen,
    pattern,
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

// The network of each connected handle, by handle id.
function handleNetworks(doc) {
  const connected = new Map();

  (doc.edges || []).forEach((edge) => {
    [edge.sourceHandleId, edge.targetHandleId].filter(Boolean).forEach((id) => {
      connected.set(id, edge.networkId);
    });
  });

  return connected;
}

/**
 * Handles exposed by a node: one per device interface handle, and a single bus
 * handle for switches.
 *
 * @param {object} doc
 * @param {object} node
 * @param {Map} [connected] each connected handle's network, when the handles
 *   of many nodes are wanted
 * @returns {{id: string, label: string, kind: string}[]}
 */
export function handlesFor(doc, node, connected = handleNetworks(doc)) {
  if (node.kind === 'switch') {
    const network = networkOfSwitch(doc, node);

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
    const network = findNetwork(doc, connected.get(handle.id));

    return {
      id: handle.id,
      name: handle.name,
      index: handle.index,
      kind: 'interface',
      connected: connected.has(handle.id),
      networkId: connected.get(handle.id),
      label: network
        ? `${handle.name} on network ${network.name}`
        : `${handle.name}, not connected`,
      interface: iface,
    };
  });
}

/**
 * Converts model nodes into Vue Flow nodes. A node the diagram checks flag
 * carries what they found (data.issue), and is described by it as well.
 *
 * @param {object} doc
 * @param {object} [options] selectedIds; issues: nodeIssueSummaries by
 *   node id
 * @returns {object[]}
 */
export function toFlowNodes(doc, options = {}) {
  const selected = new Set(options.selectedIds || []);
  const issues = options.issues || new Map();
  const index = labelIndex(doc);
  const connected = handleNetworks(doc);

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
      zIndex: node.kind === 'group' ? 0 : 1,
      style: { width: `${size.width}px`, height: `${size.height}px` },
      ariaLabel: nodeAriaLabel(doc, node, index),
      // Vue Flow spreads these over its own wrapper attributes. The wrapper
      // is the node's only Tab stop, a toggle button pressed while the node is
      // selected; undefined removes Vue Flow's role description.
      domAttributes: {
        role: 'button',
        'aria-pressed': String(selected.has(node.id)),
        'aria-roledescription': undefined,
        'aria-describedby': issue
          ? `${nodeIssueId(node.id)} ${NODE_HINT_ID}`
          : NODE_HINT_ID,
      },
      data: {
        node,
        label: nodeLabel(node),
        iconKey: nodeIconKey(node),
        shape: kindMeta(node.kind).shape,
        comment: nodeComment(node),
        includedFrom: includedFrom(node),
        network:
          node.kind === 'switch' ? networkOfSwitch(doc, node) : undefined,
        networkStyle:
          node.kind === 'switch'
            ? networkStyle(doc, node.switch?.networkId)
            : undefined,
        handles: handlesFor(doc, node, connected),
        issue,
      },
    };
  });
}

/**
 * Flow nodes or edges with a selection applied. Only those it changes are
 * copied; the rest stay the very objects they were, so Vue Flow redraws
 * only what the selection changed.
 *
 * @param {object[]} items from toFlowNodes or toFlowEdges
 * @param {string[]} [selectedIds]
 * @returns {object[]}
 */
export function withSelection(items, selectedIds = []) {
  const selected = new Set(selectedIds);

  return items.map((item) => {
    const pressed = selected.has(item.id);

    return pressed === item.selected
      ? item
      : {
          ...item,
          selected: pressed,
          domAttributes: {
            ...item.domAttributes,
            'aria-pressed': String(pressed),
          },
        };
  });
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
 * The bend of each connection that shares its end at a switch with others,
 * as {index, count}: a connection drawn without a route bends in a column of
 * its own (see NetworkEdge.vue), so the lines into one switch do not run
 * down one column. Into a switch, the connection whose other end is nearest
 * the switch's handle, up or down, bends first (leftmost), so no line
 * crosses another on its way; out of one, it bends last.
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
 * Converts model edges into Vue Flow edges.
 *
 * @param {object} doc
 * @param {object} [options] selectedIds
 * @returns {object[]}
 */
export function toFlowEdges(doc, options = {}) {
  const selected = new Set(options.selectedIds || []);
  // The first of an id, as findNode finds it.
  const nodeById = new Map();

  for (const node of doc.nodes || []) {
    if (!nodeById.has(node.id)) {
      nodeById.set(node.id, node);
    }
  }

  const styles = new Map();
  const styleOf = (id) => {
    if (!styles.has(id)) {
      styles.set(id, networkStyle(doc, id));
    }

    return styles.get(id);
  };
  const lanes = edgeLanes(doc, nodeById);

  return (doc.edges || []).map((edge) => {
    const style = styleOf(edge.networkId);
    const source = nodeById.get(edge.sourceNodeId);
    const target = nodeById.get(edge.targetNodeId);
    const network = findNetwork(doc, edge.networkId);
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
      ariaLabel:
        `Network ${network ? network.name : 'unassigned'} from ` +
        `${source ? nodeLabel(source) : edge.sourceNodeId} to ` +
        `${target ? nodeLabel(target) : edge.targetNodeId}${labelled}`,
      // Vue Flow writes tabIndex in camel case, which an SVG element ignores,
      // so connections could never take focus. Like nodes, a connection is a
      // toggle button pressed while it is selected.
      domAttributes: {
        tabindex: 0,
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
 * @returns {number} square pixels; 0 when the box is fully in view
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
 * Where to put the centre of an item of the given size so it can be seen:
 * of the points on a grid over the pane, the one nearest the pane's centre
 * where the item is inside the pane and clear of the overlays, or else the
 * one where the least of it is hidden.
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
