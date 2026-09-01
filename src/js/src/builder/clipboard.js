// Copy/paste for canvas elements, implemented as pure document transforms so
// it can be driven from the canvas, the keyboard or the semantic outline.
//
// A payload is self contained: it carries the networks its switches reference
// so a paste into another document still produces a valid document. Identifiers
// are always regenerated on paste, never reused.

import {
  addNetwork,
  addNode,
  connect,
  findNetwork,
  networkByName,
} from './model.js';

export const PASTE_OFFSET = { x: 40, y: 40 };

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

  const created = addNetwork(doc, {
    name: payloadNetwork.name,
    alias: payloadNetwork.alias,
    description: payloadNetwork.description,
    color: payloadNetwork.color,
  });

  return { doc: created.doc, networkId: created.network.id };
}

/**
 * Pastes a clipboard payload, remapping every identifier and offsetting
 * positions.
 *
 * @param {object} doc
 * @param {{nodes: object[], edges?: object[], networks?: object[]}} payload
 * @param {object} [options] offset
 * @returns {{doc: object, nodeIds: string[]}}
 */
export function pasteClipboard(doc, payload, options = {}) {
  if (!payload || !Array.isArray(payload.nodes) || payload.nodes.length === 0) {
    return { doc, nodeIds: [] };
  }

  const offset = options.offset || PASTE_OFFSET;
  const nodeIds = new Map();
  const handleIds = new Map();
  let next = doc;

  payload.nodes.forEach((node) => {
    const init = {
      kind: node.kind,
      label: node.label,
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
    });

    next = result.doc;
  });

  return { doc: next, nodeIds: [...nodeIds.values()] };
}
