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
import { stableHash } from '../ids.js';
import {
  deviceHandles,
  findNetwork,
  findNode,
  networkOfSwitch,
  nodeComment,
  nodeLabel,
  sizeOf,
  specInterfaceFor,
} from '../model.js';

export const FLOW_NODE_TYPES = {
  device: 'builderDevice',
  switch: 'builderSwitch',
  note: 'builderNote',
  group: 'builderGroup',
};

export const SWITCH_HANDLE_ID = 'bus';

// Dash patterns give every network a non-color cue as well as a color token.
export const NETWORK_PATTERNS = ['solid', 'dashed', 'dotted', 'dash-dot'];
export const NETWORK_DASH_ARRAYS = {
  solid: undefined,
  dashed: '8 4',
  dotted: '2 4',
  'dash-dot': '10 4 2 4',
};
export const NETWORK_TOKEN_COUNT = 8;

/**
 * Deterministic visual treatment for a network. Documents with the same
 * networks always render identically.
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
  const pattern = NETWORK_PATTERNS[seed % NETWORK_PATTERNS.length];

  return {
    token: seed % NETWORK_TOKEN_COUNT,
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
 * @returns {{x: number, y: number}}
 */
export function relativePosition(doc, node) {
  if (!node.parentId) {
    return { ...node.position };
  }

  const parent = findNode(doc, node.parentId);

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
 * @returns {{id: string, label: string, kind: string}[]}
 */
export function handlesFor(doc, node) {
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

  const connected = new Map();

  (doc.edges || []).forEach((edge) => {
    [edge.sourceHandleId, edge.targetHandleId].filter(Boolean).forEach((id) => {
      connected.set(id, edge.networkId);
    });
  });

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
 * Converts model nodes into Vue Flow nodes.
 *
 * @param {object} doc
 * @param {object} [options] selectedIds
 * @returns {object[]}
 */
export function toFlowNodes(doc, options = {}) {
  const selected = new Set(options.selectedIds || []);

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

    return {
      id: node.id,
      type: FLOW_NODE_TYPES[node.kind] || FLOW_NODE_TYPES.device,
      position: relativePosition(doc, node),
      selected: selected.has(node.id),
      parentNode: node.parentId || undefined,
      expandParent: Boolean(node.parentId),
      zIndex: node.kind === 'group' ? 0 : 1,
      style: { width: `${size.width}px`, height: `${size.height}px` },
      ariaLabel: nodeAriaLabel(doc, node),
      data: {
        node,
        label: nodeLabel(node),
        iconKey: nodeIconKey(node),
        shape: kindMeta(node.kind).shape,
        comment: nodeComment(node),
        network:
          node.kind === 'switch' ? networkOfSwitch(doc, node) : undefined,
        networkStyle:
          node.kind === 'switch'
            ? networkStyle(doc, node.switch?.networkId)
            : undefined,
        handles: handlesFor(doc, node),
      },
    };
  });
}

/**
 * Accessible name for a canvas node.
 *
 * @param {object} doc
 * @param {object} node
 * @returns {string}
 */
export function nodeAriaLabel(doc, node) {
  const links = (doc.edges || []).filter(
    (edge) => edge.sourceNodeId === node.id || edge.targetNodeId === node.id,
  ).length;
  const kind = kindMeta(node.kind).label;
  const comment = nodeComment(node);
  const parts = [`${kind} ${nodeLabel(node)}`];

  if (node.kind === 'switch') {
    const network = networkOfSwitch(doc, node);

    parts.push(`network ${network ? network.name : 'unassigned'}`);
  }

  if (node.kind === 'device' || node.kind === 'switch') {
    parts.push(`${links} ${links === 1 ? 'connection' : 'connections'}`);
  }

  if (comment && node.kind !== 'note') {
    parts.push(`comment: ${comment}`);
  }

  return parts.join(', ');
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

  return (doc.edges || []).map((edge) => {
    const style = networkStyle(doc, edge.networkId);
    const source = findNode(doc, edge.sourceNodeId);
    const target = findNode(doc, edge.targetNodeId);
    const network = findNetwork(doc, edge.networkId);

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
        `${target ? nodeLabel(target) : edge.targetNodeId}`,
      data: { edge, network, style },
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
    !handle || handle === SWITCH_HANDLE_ID ? null : handle;

  return {
    sourceNodeId: connection.source,
    targetNodeId: connection.target,
    sourceHandleId: normalizeHandle(connection.sourceHandle),
    targetHandleId: normalizeHandle(connection.targetHandle),
  };
}
