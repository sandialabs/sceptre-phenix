// Copy and paste for canvas elements, implemented as pure document
// transforms. The canvas, the keyboard and the semantic outline can all use
// them.
//
// A payload is self-contained. It carries the networks that its switches
// reference, and the document's copies of the custom icons that its nodes
// use. Thus a paste into another document still produces a valid document
// that shows them. A paste always makes new identifiers. It never reuses them.

import { MAX_DOCUMENT_ICONS, iconRefs } from './icons.js';
import {
  addNetwork,
  addNode,
  clearInterfaceVLANs,
  connect,
  DEFAULT_NETWORK_COLORS,
  deviceHandles,
  DRAWING_KINDS,
  findNetwork,
  findNode,
  lookOf,
  networkByName,
  networkColorInUse,
  specInterfaceFor,
  syncInterfaceVLANs,
} from './model.js';

const PASTE_OFFSET = { x: 40, y: 40 };

// Why Duplicate does nothing for a selection with no node in it: a copy
// holds a connection only with both of its nodes (see copySelection).
export const DUPLICATE_NEEDS_NODES =
  'Select the nodes to duplicate; a connection is copied with both of its nodes.';

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

/**
 * Builds a self-contained clipboard payload from a selection.
 *
 * @param {object} doc
 * @param {{nodes?: string[], edges?: string[]}} selection
 * @returns {{nodes: object[], edges: object[], networks: object[],
 *   icons: object}} icons: the document's copies of the custom icons that the
 *   nodes use, by name. The icon library has the others.
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

  const icons = {};

  for (const name of iconRefs({ nodes })) {
    if (doc.icons && Object.hasOwn(doc.icons, name)) {
      icons[name] = clone(doc.icons[name]);
    }
  }

  return { nodes, edges, networks, icons };
}

// The document, with the copies of icons from `icons` that its nodes name
// and that it does not have, while it carries fewer than MAX_DOCUMENT_ICONS.
// A copy that it already carries stays. A name past the maximum is left to
// the icon library.
function withCopies(doc, icons) {
  if (!icons || typeof icons !== 'object') {
    return doc;
  }

  const carried = { ...(doc.icons || {}) };
  let added = false;

  for (const name of iconRefs(doc)) {
    if (
      Object.hasOwn(icons, name) &&
      !Object.hasOwn(carried, name) &&
      Object.keys(carried).length < MAX_DOCUMENT_ICONS
    ) {
      carried[name] = clone(icons[name]);
      added = true;
    }
  }

  return added ? { ...doc, icons: carried } : doc;
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
    lineStyle: payloadNetwork.lineStyle,
  });

  return { doc: created.doc, networkId: created.network.id };
}

// Each paste of the same nodes goes one PASTE_OFFSET further than all earlier
// copies. Thus no copy lands on top of another node of its kind at the same
// position.
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

// A copy gets the name a new node of its kind gets (see addNode). A device is
// named after its own hostname, unless it had a different label. A switch is
// named after its network. Any other label is kept.
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
 * Pastes a clipboard payload, with new identifiers and offset positions. The
 * document gets the payload's copies of custom icons that the pasted nodes
 * name and that the document does not have. When the paste is committed, it
 * drops the copies that are identical in the icon library (see settleIcons in
 * icons.js).
 *
 * @param {object} doc
 * @param {{nodes: object[], edges?: object[], networks?: object[],
 *   icons?: object}} payload
 * @param {object} [options] offset. By default, each paste of the same nodes
 *   goes PASTE_OFFSET further than the copies already there.
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
      init.look = lookOf(node.device);
      init.purdueLevel = node.device?.purdueLevel;
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
      init.outlineColor = node.switch?.outlineColor;
      init.fillColor = node.switch?.fillColor;
      init.iconSize = node.switch?.iconSize;
      init.purdueLevel = node.switch?.purdueLevel;
      // A switch's notes are its own, so a copy carries them as a device's
      // general.notes go with its spec.
      init.notes = node.switch?.notes;
    }

    if (node.kind === 'note') {
      init.text = node.note?.text;
      init.color = node.note?.color;
    }

    if (node.kind === 'group') {
      init.title = node.group?.title;
      init.color = node.group?.color;
      init.description = node.group?.description;
      init.borderStyle = node.group?.borderStyle;
      init.iconKey = node.group?.iconKey;
      init.icon = node.group?.icon;
      init.iconSize = node.group?.iconSize;
    }

    // A drawing's payload is what addNode takes for it: a shape's figure, an
    // icon's icon, a line's points (relative to its position), and the label,
    // colors and styles of each.
    if (DRAWING_KINDS.includes(node.kind)) {
      Object.assign(init, node[node.kind]);
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
      lineStyle: edge.lineStyle,
    });

    next = result.doc;
  });

  // A copy keeps the spec entries of its interfaces, VLAN included. A copy
  // pasted without its connection is on no network. Thus its VLAN is emptied
  // when the VLAN names a network of the diagram. A VLAN that names no
  // network is kept, as on the original.
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
    doc: withCopies(clearInterfaceVLANs(synced, naming), payload.icons),
    nodeIds: [...nodeIds.values()],
  };
}
