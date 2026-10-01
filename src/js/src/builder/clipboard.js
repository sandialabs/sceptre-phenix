// Copy/paste for canvas elements, implemented as pure document transforms so
// it can be driven from the canvas, the keyboard or the semantic outline.
//
// A payload is self contained: it carries the networks its switches reference
// so a paste into another document still produces a valid document. Identifiers
// are always regenerated on paste, never reused.

import {
  addNetwork,
  addNode,
  clearInterfaceVLANs,
  connect,
  DEFAULT_NETWORK_COLORS,
  deviceHandles,
  findNetwork,
  findNode,
  networkByName,
  networkColorInUse,
  specInterfaceFor,
  syncInterfaceVLANs,
} from './model.js';

const PASTE_OFFSET = { x: 40, y: 40 };

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

/**
 * Builds a self-contained clipboard payload from a selection.
 *
 * @param {object} doc
 * @param {{nodes?: string[], edges?: string[]}} selection
 * @returns {{nodes: object[], edges: object[], networks: object[]}}
 */
export function copySelection(doc, selection = {}) {
  const ids = new Set(selection.nodes || []);

  // Pull in children of selected groups so pasting a group keeps its members.
  let changed = true;
  while (changed) {
    changed = false;
    (doc.nodes || []).forEach((node) => {
      if (node.parentId && ids.has(node.parentId) && !ids.has(node.id)) {
        ids.add(node.id);
        changed = true;
      }
    });
  }

  const nodes = (doc.nodes || []).filter((node) => ids.has(node.id)).map(clone);

  const edges = (doc.edges || [])
    .filter((edge) => ids.has(edge.sourceNodeId) && ids.has(edge.targetNodeId))
    .map(clone);

  const networkIds = new Set(
    nodes
      .filter((node) => node.kind === 'switch')
      .map((node) => node.switch?.networkId)
      .filter(Boolean),
  );

  const networks = (doc.networks || [])
    .filter((network) => networkIds.has(network.id))
    .map(clone);

  return { nodes, edges, networks };
}

function resolveNetwork(doc, payloadNetwork, networkId) {
  if (findNetwork(doc, networkId)) {
    return { doc, networkId };
  }

  if (!payloadNetwork) {
    return { doc, networkId: null };
  }

  const existing = networkByName(doc, payloadNetwork.name);

  if (existing) {
    return { doc, networkId: existing.id };
  }

  // A palette color another network here already uses is left for
  // addNetwork to choose again, so two networks never draw alike.
  const repeated =
    DEFAULT_NETWORK_COLORS.includes(payloadNetwork.color) &&
    networkColorInUse(doc, payloadNetwork.color);
  const created = addNetwork(doc, {
    name: payloadNetwork.name,
    alias: payloadNetwork.alias,
    description: payloadNetwork.description,
    color: repeated ? undefined : payloadNetwork.color,
  });

  return { doc: created.doc, networkId: created.network.id };
}

// Pasting the same nodes again cascades: each paste goes one PASTE_OFFSET
// further, past every earlier copy, so no copy lands on top of another: a
// node of its kind at the very same spot.
const MAX_CASCADE = 100;

function cascadeOffset(doc, nodes) {
  const spot = (node, x = 0, y = 0) =>
    `${node.kind}@${node.position.x + x},${node.position.y + y}`;
  const taken = new Set((doc.nodes || []).map((node) => spot(node)));

  for (let step = 1; step < MAX_CASCADE; step += 1) {
    const x = PASTE_OFFSET.x * step;
    const y = PASTE_OFFSET.y * step;
    const stacked = nodes.some((node) => taken.has(spot(node, x, y)));

    if (!stacked) {
      return { x, y };
    }
  }

  return { x: PASTE_OFFSET.x * MAX_CASCADE, y: PASTE_OFFSET.y * MAX_CASCADE };
}

// A copy is named as a new node of its kind would be: a device after its
// own hostname, unless it was labelled otherwise; a switch after its network
// (addNode). Any other label is kept.
function pastedLabel(node) {
  if (
    node.kind === 'device' &&
    (!node.label || node.label === node.device?.hostname)
  ) {
    return undefined;
  }

  return node.label;
}

/**
 * Pastes a clipboard payload, remapping every identifier and offsetting
 * positions.
 *
 * @param {object} doc
 * @param {{nodes: object[], edges?: object[], networks?: object[]}} payload
 * @param {object} [options] offset; by default each paste of the same nodes
 *   goes PASTE_OFFSET further than the copies already there
 * @returns {{doc: object, nodeIds: string[]}}
 */
export function pasteClipboard(doc, payload, options = {}) {
  if (!payload || !Array.isArray(payload.nodes) || payload.nodes.length === 0) {
    return { doc, nodeIds: [] };
  }

  const offset = options.offset || cascadeOffset(doc, payload.nodes);
  const nodeIds = new Map();
  const handleIds = new Map();
  let next = doc;

  payload.nodes.forEach((node) => {
    const init = {
      kind: node.kind,
      label: pastedLabel(node),
      size: node.size,
      position: {
        x: node.position.x + offset.x,
        y: node.position.y + offset.y,
      },
    };

    if (node.kind === 'device') {
      init.hostname = node.device?.hostname;
      init.spec = node.device?.spec;
      init.iconKey = node.device?.iconKey;
      init.interfaces = (node.device?.interfaces || []).map((handle) => ({
        name: handle.name,
      }));
    }

    if (node.kind === 'switch') {
      const network = (payload.networks || []).find(
        (entry) => entry.id === node.switch?.networkId,
      );
      const resolved = resolveNetwork(next, network, node.switch?.networkId);

      next = resolved.doc;
      init.networkId = resolved.networkId;
      init.networkName = network?.name;
    }

    if (node.kind === 'note') {
      init.text = node.note?.text;
      init.color = node.note?.color;
    }

    if (node.kind === 'group') {
      init.title = node.group?.title;
      init.color = node.group?.color;
    }

    const added = addNode(next, init);

    next = added.doc;
    nodeIds.set(node.id, added.node.id);

    (node.device?.interfaces || []).forEach((handle, index) => {
      const copied = added.node.device?.interfaces?.[index];

      if (copied) {
        handleIds.set(handle.id, copied.id);
      }
    });
  });

  // Re-parent copies inside copied groups.
  next = {
    ...next,
    nodes: next.nodes.map((node) => {
      const original = payload.nodes.find((n) => nodeIds.get(n.id) === node.id);

      if (!original || !original.parentId || !nodeIds.has(original.parentId)) {
        return node;
      }

      return { ...node, parentId: nodeIds.get(original.parentId) };
    }),
  };

  (payload.edges || []).forEach((edge) => {
    const sourceNodeId = nodeIds.get(edge.sourceNodeId);
    const targetNodeId = nodeIds.get(edge.targetNodeId);

    if (!sourceNodeId || !targetNodeId) {
      return;
    }

    const result = connect(next, {
      sourceNodeId,
      targetNodeId,
      sourceHandleId: handleIds.get(edge.sourceHandleId),
      targetHandleId: handleIds.get(edge.targetHandleId),
      label: edge.label,
      color: edge.color,
    });

    next = result.doc;
  });

  // A copy keeps its interfaces' spec entries, VLAN included. One pasted
  // without its connection is on no network, so its VLAN is emptied when it
  // names a network of the diagram; one naming no network is kept, as on
  // the original.
  const synced = syncInterfaceVLANs(next);
  const naming = [...nodeIds.values()].flatMap((id) => {
    const node = findNode(synced, id);

    return deviceHandles(node)
      .filter((handle) =>
        networkByName(
          synced,
          String(specInterfaceFor(node, handle.id)?.vlan ?? '').trim(),
        ),
      )
      .map((handle) => handle.id);
  });

  return {
    doc: clearInterfaceVLANs(synced, naming),
    nodeIds: [...nodeIds.values()],
  };
}
