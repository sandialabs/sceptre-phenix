// Library independent builder document model.
//
// This module is the single source of truth for the wire contract implemented
// by phenix/types/builder (Go). Every exported function takes a document and
// returns a new document; nothing here knows about Vue, Vue Flow or JSON Forms.
//
// Wire shape (see src/go/types/builder/document.go):
//
//   { $schema, revision, id, name?, description?, nodes[], networks[],
//     edges[], viewport, grid, scenario?, source? }
//
// Node payloads are discriminated by kind: device | switch | note | group.
// `owner` is a property of the draft envelope and is never part of a document.

import { iconKeyForSpec, isIconKey, kindMeta } from './catalog.js';
import { newId, uniqueName } from './ids.js';

export const SCHEMA_URI = 'https://phenix.sandia.gov/schemas/builder/v1';
export const SCHEMA_REVISION = 1;
export const DEFAULT_GRID_SIZE = 16;

export const DEFAULT_SIZES = {
  device: { width: 160, height: 96 },
  switch: { width: 180, height: 72 },
  note: { width: 200, height: 120 },
  group: { width: 320, height: 240 },
};

const DEFAULT_NETWORK_COLORS = [
  '#2f6fbf',
  '#b05c17',
  '#1f7a5a',
  '#7a3fb0',
  '#a3273f',
  '#0f6f77',
  '#6b6f18',
  '#8a4b8f',
];

/**
 * Size of a node, falling back to the default for its kind.
 *
 * @param {object} node
 * @returns {{width: number, height: number}}
 */
export function sizeOf(node) {
  const fallback = DEFAULT_SIZES[node?.kind] || DEFAULT_SIZES.device;

  return {
    width: node?.size?.width ?? fallback.width,
    height: node?.size?.height ?? fallback.height,
  };
}

/**
 * Creates a new, valid, empty document.
 *
 * @param {object} [init] name, description, id
 * @returns {object} document
 */
export function createDocument(init = {}) {
  return {
    $schema: SCHEMA_URI,
    revision: SCHEMA_REVISION,
    id: init.id || newId(),
    name: init.name || 'Untitled diagram',
    description: init.description || '',
    nodes: [],
    networks: [],
    edges: [],
    viewport: { x: 0, y: 0, zoom: 1 },
    grid: { enabled: true, size: DEFAULT_GRID_SIZE, snap: true },
  };
}

/**
 * Deep copy of a document.
 *
 * @param {object} doc
 * @returns {object}
 */
export function cloneDocument(doc) {
  return JSON.parse(JSON.stringify(doc));
}

/** @returns {object|undefined} */
export function findNode(doc, id) {
  return (doc?.nodes || []).find((node) => node.id === id);
}

/** @returns {object|undefined} */
export function findEdge(doc, id) {
  return (doc?.edges || []).find((edge) => edge.id === id);
}

/** @returns {object|undefined} */
export function findNetwork(doc, id) {
  return (doc?.networks || []).find((network) => network.id === id);
}

/**
 * @param {object} doc
 * @param {string} name
 * @returns {object|undefined} network matched case-insensitively
 */
export function networkByName(doc, name) {
  const wanted = String(name || '').toLowerCase();

  return (doc?.networks || []).find(
    (network) => network.name.toLowerCase() === wanted,
  );
}

/**
 * Network of a switch node.
 *
 * @param {object} doc
 * @param {object} node
 * @returns {object|undefined}
 */
export function networkOfSwitch(doc, node) {
  return node?.kind === 'switch'
    ? findNetwork(doc, node.switch?.networkId)
    : undefined;
}

/**
 * Interface handles of a device node.
 *
 * @param {object} node
 * @returns {object[]} handles ({id, name, index})
 */
export function deviceHandles(node) {
  return node?.kind === 'device' ? node.device?.interfaces || [] : [];
}

/**
 * Spec interface entries of a device node.
 *
 * @param {object} node
 * @returns {object[]}
 */
export function specInterfaces(node) {
  const interfaces = node?.device?.spec?.network?.interfaces;

  return Array.isArray(interfaces) ? interfaces : [];
}

/**
 * Spec interface entry a handle maps onto.
 *
 * @param {object} node
 * @param {string} handleId
 * @returns {object|undefined}
 */
export function specInterfaceFor(node, handleId) {
  const handle = deviceHandles(node).find((entry) => entry.id === handleId);

  if (!handle) {
    return undefined;
  }

  return specInterfaces(node).find((iface) => iface.name === handle.name);
}

// --- networks --------------------------------------------------------------

function nextNetworkColor(doc) {
  return DEFAULT_NETWORK_COLORS[
    (doc.networks || []).length % DEFAULT_NETWORK_COLORS.length
  ];
}

/**
 * Adds a network (VLAN).
 *
 * @param {object} doc
 * @param {object} [init] name, alias, description, color
 * @returns {{doc: object, network: object}}
 */
export function addNetwork(doc, init = {}) {
  const taken = new Set((doc.networks || []).map((n) => n.name.toLowerCase()));
  const base =
    String(init.name || 'EXP')
      .trim()
      .replace(/\s+/g, '-') || 'EXP';
  const name = taken.has(base.toLowerCase())
    ? uniqueName(base, [...taken], (value) => value.toLowerCase())
    : base;

  const network = {
    id: init.id || newId(),
    name,
    description: init.description || '',
    color: init.color || nextNetworkColor(doc),
  };

  if (Number.isInteger(init.alias)) {
    network.alias = init.alias;
  }

  return {
    doc: { ...doc, networks: [...(doc.networks || []), network] },
    network,
  };
}

/**
 * Updates a network. Renaming a network rewrites the VLAN of every connected
 * device interface, keeping the document and the phenix spec consistent.
 *
 * @param {object} doc
 * @param {string} id
 * @param {object} patch name, alias, description, color
 * @returns {object} document
 */
export function updateNetwork(doc, id, patch = {}) {
  const network = findNetwork(doc, id);

  if (!network) {
    return doc;
  }

  const updated = { ...network };

  if (patch.name !== undefined) {
    updated.name = String(patch.name).trim().replace(/\s+/g, '-');
  }

  if (patch.description !== undefined) {
    updated.description = patch.description;
  }

  if (patch.color !== undefined) {
    updated.color = patch.color;
  }

  if (patch.alias !== undefined) {
    if (patch.alias === null || patch.alias === '') {
      delete updated.alias;
    } else {
      updated.alias = Number(patch.alias);
    }
  }

  const next = {
    ...doc,
    networks: doc.networks.map((entry) => (entry.id === id ? updated : entry)),
  };

  return syncInterfaceVLANs(next);
}

/**
 * Removes networks together with their switch nodes and edges.
 *
 * @param {object} doc
 * @param {string[]} ids
 * @returns {object} document
 */
export function removeNetworks(doc, ids) {
  const removing = new Set(ids || []);

  if (removing.size === 0) {
    return doc;
  }

  const switches = (doc.nodes || [])
    .filter(
      (node) => node.kind === 'switch' && removing.has(node.switch?.networkId),
    )
    .map((node) => node.id);

  const pruned = removeElements(doc, { nodes: switches });

  return {
    ...pruned,
    networks: pruned.networks.filter((network) => !removing.has(network.id)),
  };
}

// --- nodes -----------------------------------------------------------------

function deviceSpec(init = {}) {
  const hostname = init.hostname;

  if (init.external) {
    return {
      external: true,
      type: 'HIL',
      general: { hostname, description: init.description || '' },
      network: { interfaces: [] },
    };
  }

  return {
    type: 'VirtualMachine',
    general: {
      hostname,
      description: init.description || '',
      vm_type: 'kvm',
    },
    hardware: {
      os_type: init.osType || 'linux',
      drives: [{ image: init.image || 'ubuntu.qc2' }],
    },
    network: { interfaces: [] },
  };
}

function uniqueHostname(doc, wanted) {
  const taken = (doc.nodes || [])
    .filter((node) => node.kind === 'device')
    .map((node) => node.device.hostname.toLowerCase());

  const base =
    String(wanted || 'node')
      .trim()
      .replace(/\s+/g, '-') || 'node';

  if (!taken.includes(base.toLowerCase())) {
    return base;
  }

  let n = 2;

  while (taken.includes(`${base}-${n}`.toLowerCase())) {
    n += 1;
  }

  return `${base}-${n}`;
}

/**
 * Adds a node of any kind. Switch nodes without an explicit network create one.
 *
 * @param {object} doc
 * @param {object} options kind, position, size, parentId, label, and kind
 *   specific fields (template/hostname/spec, networkId, text, title)
 * @returns {{doc: object, node: object, network?: object}}
 */
export function addNode(doc, options = {}) {
  const kind = options.kind || 'device';
  const position = {
    x: options.position?.x ?? 0,
    y: options.position?.y ?? 0,
  };

  const node = {
    id: options.id || newId(),
    kind,
    label: options.label || '',
    position,
  };

  if (options.size) {
    node.size = { ...options.size };
  }

  if (options.parentId) {
    node.parentId = options.parentId;
  }

  let next = doc;
  let network;

  switch (kind) {
    case 'device': {
      const hostname = uniqueHostname(
        doc,
        options.hostname || options.label || options.template?.label || 'node',
      );
      const spec = options.spec
        ? JSON.parse(JSON.stringify(options.spec))
        : deviceSpec({ ...(options.template || {}), hostname });

      setSpecHostname(spec, hostname);

      node.label = node.label || hostname;
      node.device = {
        hostname,
        iconKey: pickIconKey(
          options.iconKey || options.template?.iconKey,
          spec,
        ),
        spec,
        interfaces: [],
      };

      const wanted = Array.isArray(options.interfaces)
        ? options.interfaces
        : [];

      wanted.forEach((iface) => {
        appendInterface(node, iface);
      });

      break;
    }
    case 'switch': {
      if (options.networkId && findNetwork(doc, options.networkId)) {
        node.switch = { networkId: options.networkId };
      } else {
        const created = addNetwork(doc, {
          name: options.networkName || options.label || 'EXP',
        });

        next = created.doc;
        network = created.network;
        node.switch = { networkId: network.id };
      }

      const bound = network || findNetwork(next, node.switch.networkId);
      node.label = node.label || bound.name;
      break;
    }
    case 'note':
      node.note = { text: options.text || '', color: options.color || '' };
      node.label = node.label || 'Note';
      break;
    case 'group':
      node.group = {
        title: options.title || options.label || 'Group',
        color: options.color || '',
        collapsed: false,
      };
      node.label = node.label || node.group.title;
      break;
    default:
      throw new Error(`Unknown node kind: ${kind}`);
  }

  return {
    doc: { ...next, nodes: [...(next.nodes || []), node] },
    node,
    network,
  };
}

function pickIconKey(wanted, spec) {
  if (isIconKey(wanted)) {
    return wanted;
  }

  return iconKeyForSpec(spec);
}

function setSpecHostname(spec, hostname) {
  if (!spec.general || typeof spec.general !== 'object') {
    spec.general = {};
  }

  spec.general.hostname = hostname;
}

/**
 * Updates node presentation and payload.
 *
 * @param {object} doc
 * @param {string} id
 * @param {object} patch label, position, size, parentId, device, switch, note,
 *   group
 * @returns {object} document
 */
export function updateNode(doc, id, patch = {}) {
  const node = findNode(doc, id);

  if (!node) {
    return doc;
  }

  const updated = { ...node };

  if (patch.label !== undefined) {
    updated.label = patch.label;
  }

  if (patch.position) {
    updated.position = { x: patch.position.x, y: patch.position.y };
  }

  if (patch.size !== undefined) {
    if (patch.size === null) {
      delete updated.size;
    } else {
      updated.size = { width: patch.size.width, height: patch.size.height };
    }
  }

  if (patch.device && node.kind === 'device') {
    const device = { ...node.device, ...patch.device };

    if (patch.device.spec) {
      device.spec = JSON.parse(JSON.stringify(patch.device.spec));
    } else if (node.device.spec) {
      device.spec = JSON.parse(JSON.stringify(node.device.spec));
    }

    if (patch.device.hostname !== undefined) {
      device.hostname = patch.device.hostname;
    } else if (device.spec?.general?.hostname) {
      device.hostname = device.spec.general.hostname;
    }

    setSpecHostname(device.spec, device.hostname);

    if (patch.device.interfaces) {
      device.interfaces = patch.device.interfaces.map((handle, index) => ({
        id: handle.id || newId(),
        name: handle.name,
        index: handle.index ?? index,
      }));
    }

    reconcileDeviceHandles(device);

    updated.device = device;
    updated.label = patch.label !== undefined ? patch.label : device.hostname;
  }

  if (patch.switch && node.kind === 'switch') {
    updated.switch = { networkId: patch.switch.networkId };
  }

  if (patch.note && node.kind === 'note') {
    updated.note = { ...node.note, ...patch.note };
  }

  if (patch.group && node.kind === 'group') {
    updated.group = { ...node.group, ...patch.group };

    if (patch.group.title !== undefined && patch.label === undefined) {
      updated.label = patch.group.title;
    }
  }

  const next = {
    ...doc,
    nodes: doc.nodes.map((entry) => (entry.id === id ? updated : entry)),
  };

  if (node.kind !== 'device') {
    return next;
  }

  const live = new Set(deviceHandles(updated).map((handle) => handle.id));

  return syncInterfaceVLANs({
    ...next,
    edges: (next.edges || []).filter((edge) => {
      const handleId = edge.sourceHandleId || edge.targetHandleId;
      const touchesNode = edge.sourceNodeId === id || edge.targetNodeId === id;

      return !touchesNode || !handleId || live.has(handleId);
    }),
  });
}

/**
 * Keeps interface handles aligned with the spec's interface list: an interface
 * added through the inspector gains a handle, one removed loses it. Handles are
 * matched by name because that is the only stable key the phenix spec carries.
 *
 * @param {object} device device payload (mutated)
 */
function reconcileDeviceHandles(device) {
  const specNames = (device.spec?.network?.interfaces || [])
    .map((iface) => iface?.name)
    .filter((name) => typeof name === 'string' && name !== '');

  if (specNames.length === 0 && (device.interfaces || []).length === 0) {
    return;
  }

  const byName = new Map(
    (device.interfaces || []).map((handle) => [handle.name, handle]),
  );

  device.interfaces = specNames.map((name, index) => {
    const existing = byName.get(name);

    return existing ? { ...existing, index } : { id: newId(), name, index };
  });
}

/**
 * Moves a single node.
 *
 * @param {object} doc
 * @param {string} id
 * @param {{x: number, y: number}} position
 * @returns {object} document
 */
export function moveNode(doc, id, position) {
  return moveNodes(doc, [{ id, position }]);
}

/**
 * Moves several nodes at once so a multi-node drag or keyboard nudge is a
 * single history commit.
 *
 * @param {object} doc
 * @param {{id: string, position: {x: number, y: number}}[]} moves
 * @returns {object} document
 */
export function moveNodes(doc, moves = []) {
  if (moves.length === 0) {
    return doc;
  }

  const byId = new Map(moves.map((move) => [move.id, move.position]));

  return {
    ...doc,
    nodes: doc.nodes.map((node) => {
      const position = byId.get(node.id);

      return position
        ? { ...node, position: { x: position.x, y: position.y } }
        : node;
    }),
  };
}

/**
 * @param {object} doc
 * @param {string} id
 * @param {{width: number, height: number}} size
 * @returns {object} document
 */
export function resizeNode(doc, id, size) {
  return updateNode(doc, id, { size });
}

/**
 * Re-parents a node into (or out of) a group, rejecting cycles and non-group
 * parents.
 *
 * @param {object} doc
 * @param {string} id
 * @param {string|null} parentId
 * @returns {object} document
 */
export function setParent(doc, id, parentId) {
  const node = findNode(doc, id);

  if (!node) {
    return doc;
  }

  if (!parentId) {
    const updated = { ...node };
    delete updated.parentId;

    return {
      ...doc,
      nodes: doc.nodes.map((entry) => (entry.id === id ? updated : entry)),
    };
  }

  const parent = findNode(doc, parentId);

  if (!parent || parent.kind !== 'group' || parentId === id) {
    return doc;
  }

  if (isDescendant(doc, parentId, id)) {
    return doc;
  }

  return {
    ...doc,
    nodes: doc.nodes.map((entry) =>
      entry.id === id ? { ...entry, parentId } : entry,
    ),
  };
}

/**
 * @param {object} doc
 * @param {string} candidateId
 * @param {string} ancestorId
 * @returns {boolean} true when candidate is a descendant of ancestor
 */
export function isDescendant(doc, candidateId, ancestorId) {
  let current = findNode(doc, candidateId);
  const seen = new Set();

  while (current?.parentId) {
    if (current.parentId === ancestorId) {
      return true;
    }

    if (seen.has(current.parentId)) {
      return false;
    }

    seen.add(current.parentId);
    current = findNode(doc, current.parentId);
  }

  return false;
}

/**
 * Bounding box of nodes, using default sizes when a node has none.
 *
 * @param {object[]} nodes
 * @param {number} [padding]
 * @returns {{x: number, y: number, width: number, height: number}}
 */
export function boundsOf(nodes, padding = 0) {
  if (!nodes || nodes.length === 0) {
    return { x: 0, y: 0, width: 0, height: 0 };
  }

  const xs = nodes.map((node) => node.position.x);
  const ys = nodes.map((node) => node.position.y);
  const rights = nodes.map((node) => node.position.x + sizeOf(node).width);
  const bottoms = nodes.map((node) => node.position.y + sizeOf(node).height);

  const x = Math.min(...xs) - padding;
  const y = Math.min(...ys) - padding;

  return {
    x,
    y,
    width: Math.max(...rights) + padding - x,
    height: Math.max(...bottoms) + padding - y,
  };
}

/**
 * Wraps nodes in a new group node.
 *
 * @param {object} doc
 * @param {string[]} ids
 * @param {object} [options] title, padding
 * @returns {{doc: object, group: object|null}}
 */
export function groupNodes(doc, ids, options = {}) {
  const members = (ids || [])
    .map((id) => findNode(doc, id))
    .filter((node) => node && !node.parentId);

  if (members.length === 0) {
    return { doc, group: null };
  }

  const padding = options.padding ?? 40;
  const bounds = boundsOf(members, padding);

  const created = addNode(doc, {
    kind: 'group',
    title: options.title || 'Group',
    label: options.title || 'Group',
    position: { x: bounds.x, y: bounds.y },
    size: { width: bounds.width, height: bounds.height },
  });

  const memberIds = new Set(members.map((node) => node.id));

  return {
    doc: {
      ...created.doc,
      nodes: created.doc.nodes.map((node) =>
        memberIds.has(node.id) ? { ...node, parentId: created.node.id } : node,
      ),
    },
    group: created.node,
  };
}

/**
 * Removes a group node, keeping its members (re-parented to the group's own
 * parent, if any).
 *
 * @param {object} doc
 * @param {string} groupId
 * @returns {object} document
 */
export function ungroup(doc, groupId) {
  const group = findNode(doc, groupId);

  if (!group || group.kind !== 'group') {
    return doc;
  }

  return {
    ...doc,
    nodes: doc.nodes
      .filter((node) => node.id !== groupId)
      .map((node) => {
        if (node.parentId !== groupId) {
          return node;
        }

        const updated = { ...node };

        if (group.parentId) {
          updated.parentId = group.parentId;
        } else {
          delete updated.parentId;
        }

        return updated;
      }),
  };
}

/**
 * Removes nodes (with their descendants), edges and networks.
 *
 * @param {object} doc
 * @param {{nodes?: string[], edges?: string[], networks?: string[]}} selection
 * @returns {object} document
 */
export function removeElements(doc, selection = {}) {
  const removing = new Set(selection.nodes || []);

  let changed = true;

  while (changed) {
    changed = false;
    (doc.nodes || []).forEach((node) => {
      if (
        node.parentId &&
        removing.has(node.parentId) &&
        !removing.has(node.id)
      ) {
        removing.add(node.id);
        changed = true;
      }
    });
  }

  const removedEdges = new Set(selection.edges || []);
  const nodes = (doc.nodes || []).filter((node) => !removing.has(node.id));
  const edges = (doc.edges || []).filter(
    (edge) =>
      !removedEdges.has(edge.id) &&
      !removing.has(edge.sourceNodeId) &&
      !removing.has(edge.targetNodeId),
  );

  const next = { ...doc, nodes, edges };
  const networks = new Set(selection.networks || []);

  const pruned = networks.size > 0 ? removeNetworks(next, [...networks]) : next;

  return syncInterfaceVLANs(pruned);
}

// --- interfaces ------------------------------------------------------------

function appendInterface(node, init = {}) {
  const taken = new Set(deviceHandles(node).map((handle) => handle.name));
  const base = String(init.name || 'eth0').trim() || 'eth0';
  const name = taken.has(base) ? uniqueName(base, taken) : base;

  const handle = {
    id: init.id || newId(),
    name,
    index: deviceHandles(node).length,
  };

  node.device.interfaces = [...deviceHandles(node), handle];

  if (
    !node.device.spec.network ||
    typeof node.device.spec.network !== 'object'
  ) {
    node.device.spec.network = { interfaces: [] };
  }

  if (!Array.isArray(node.device.spec.network.interfaces)) {
    node.device.spec.network.interfaces = [];
  }

  node.device.spec.network.interfaces.push({
    name,
    type: init.type || 'ethernet',
    proto: init.proto || 'dhcp',
    vlan: init.vlan || '',
  });

  return handle;
}

/**
 * Adds an interface to a device: a stable handle plus the matching spec entry.
 *
 * @param {object} doc
 * @param {string} nodeId
 * @param {object} [init] name, proto, vlan
 * @returns {{doc: object, handle: object|null}}
 */
export function addInterface(doc, nodeId, init = {}) {
  const node = findNode(doc, nodeId);

  if (!node || node.kind !== 'device') {
    return { doc, handle: null };
  }

  const copy = JSON.parse(JSON.stringify(node));
  const handle = appendInterface(copy, {
    name: init.name || nextInterfaceName(node),
    ...init,
  });

  return {
    doc: {
      ...doc,
      nodes: doc.nodes.map((entry) => (entry.id === nodeId ? copy : entry)),
    },
    handle,
  };
}

/**
 * @param {object} node device node
 * @returns {string} next unused interface name (eth0, eth1, ...)
 */
export function nextInterfaceName(node) {
  const taken = new Set(deviceHandles(node).map((handle) => handle.name));

  let index = 0;

  while (taken.has(`eth${index}`)) {
    index += 1;
  }

  return `eth${index}`;
}

/**
 * Removes an interface handle, its spec entry and any edge using it.
 *
 * @param {object} doc
 * @param {string} nodeId
 * @param {string} handleId
 * @returns {object} document
 */
export function removeInterface(doc, nodeId, handleId) {
  const node = findNode(doc, nodeId);

  if (!node || node.kind !== 'device') {
    return doc;
  }

  const handle = deviceHandles(node).find((entry) => entry.id === handleId);

  if (!handle) {
    return doc;
  }

  const copy = JSON.parse(JSON.stringify(node));

  copy.device.interfaces = deviceHandles(node)
    .filter((entry) => entry.id !== handleId)
    .map((entry, index) => ({ ...entry, index }));

  if (Array.isArray(copy.device.spec?.network?.interfaces)) {
    copy.device.spec.network.interfaces =
      copy.device.spec.network.interfaces.filter(
        (iface) => iface.name !== handle.name,
      );
  }

  return {
    ...doc,
    nodes: doc.nodes.map((entry) => (entry.id === nodeId ? copy : entry)),
    edges: (doc.edges || []).filter(
      (edge) =>
        edge.sourceHandleId !== handleId && edge.targetHandleId !== handleId,
    ),
  };
}

/**
 * Renames an interface, keeping the handle, the spec entry and edges aligned.
 *
 * @param {object} doc
 * @param {string} nodeId
 * @param {string} handleId
 * @param {string} name
 * @returns {object} document
 */
export function renameInterface(doc, nodeId, handleId, name) {
  const node = findNode(doc, nodeId);
  const handle = deviceHandles(node).find((entry) => entry.id === handleId);

  if (!handle) {
    return doc;
  }

  const wanted = String(name || '').trim();

  if (!wanted || deviceHandles(node).some((entry) => entry.name === wanted)) {
    return doc;
  }

  const copy = JSON.parse(JSON.stringify(node));

  copy.device.interfaces = copy.device.interfaces.map((entry) =>
    entry.id === handleId ? { ...entry, name: wanted } : entry,
  );

  if (Array.isArray(copy.device.spec?.network?.interfaces)) {
    copy.device.spec.network.interfaces =
      copy.device.spec.network.interfaces.map((iface) =>
        iface.name === handle.name ? { ...iface, name: wanted } : iface,
      );
  }

  return {
    ...doc,
    nodes: doc.nodes.map((entry) => (entry.id === nodeId ? copy : entry)),
  };
}

// --- edges -----------------------------------------------------------------

/**
 * Normalizes an edge's endpoints into (device node, handle id, switch node).
 *
 * @param {object} doc
 * @param {object} connection sourceNodeId, sourceHandleId, targetNodeId,
 *   targetHandleId
 * @returns {{device: object, handleId: string, switchNode: object}|null}
 */
export function edgeEndpoints(doc, connection = {}) {
  const source = findNode(doc, connection.sourceNodeId);
  const target = findNode(doc, connection.targetNodeId);

  if (!source || !target || source.id === target.id) {
    return null;
  }

  if (source.kind === 'device' && target.kind === 'switch') {
    return {
      device: source,
      handleId: connection.sourceHandleId,
      switchNode: target,
    };
  }

  if (source.kind === 'switch' && target.kind === 'device') {
    return {
      device: target,
      handleId: connection.targetHandleId,
      switchNode: source,
    };
  }

  return null;
}

/**
 * Validates a proposed connection against the server's edge rules.
 *
 * @param {object} doc
 * @param {object} connection
 * @returns {{valid: boolean, reason?: string}}
 */
export function validateConnection(doc, connection = {}) {
  const endpoints = edgeEndpoints(doc, connection);

  if (!endpoints) {
    return {
      valid: false,
      reason: 'A connection must join one device interface to one switch.',
    };
  }

  const { device, handleId, switchNode } = endpoints;

  if (!findNetwork(doc, switchNode.switch?.networkId)) {
    return { valid: false, reason: 'That switch has no network.' };
  }

  if (handleId) {
    const handle = deviceHandles(device).find((entry) => entry.id === handleId);

    if (!handle) {
      return {
        valid: false,
        reason: `Unknown interface on device ${device.device.hostname}.`,
      };
    }

    const used = (doc.edges || []).some(
      (edge) =>
        edge.sourceHandleId === handleId || edge.targetHandleId === handleId,
    );

    if (used) {
      return {
        valid: false,
        reason: `Interface ${handle.name} is already connected.`,
      };
    }
  }

  return { valid: true };
}

/**
 * Connects a device interface to a switch. When no handle is supplied a new
 * interface is created on the device.
 *
 * @param {object} doc
 * @param {object} connection sourceNodeId, sourceHandleId, targetNodeId,
 *   targetHandleId, label
 * @returns {{doc: object, edge: object|null, error?: string}}
 */
export function connect(doc, connection = {}) {
  const check = validateConnection(doc, connection);

  if (!check.valid) {
    return { doc, edge: null, error: check.reason };
  }

  const endpoints = edgeEndpoints(doc, connection);
  let next = doc;
  let handleId = endpoints.handleId;

  if (!handleId) {
    const created = addInterface(doc, endpoints.device.id, {});

    next = created.doc;
    handleId = created.handle.id;
  }

  const deviceIsSource = endpoints.device.id === connection.sourceNodeId;

  const edge = {
    id: newId(),
    sourceNodeId: deviceIsSource
      ? endpoints.device.id
      : endpoints.switchNode.id,
    targetNodeId: deviceIsSource
      ? endpoints.switchNode.id
      : endpoints.device.id,
    networkId: endpoints.switchNode.switch.networkId,
  };

  if (deviceIsSource) {
    edge.sourceHandleId = handleId;
  } else {
    edge.targetHandleId = handleId;
  }

  if (connection.label) {
    edge.label = connection.label;
  }

  next = { ...next, edges: [...(next.edges || []), edge] };

  return { doc: syncInterfaceVLANs(next), edge };
}

/**
 * @param {object} doc
 * @param {string} id
 * @param {object} patch label
 * @returns {object} document
 */
export function updateEdge(doc, id, patch = {}) {
  return {
    ...doc,
    edges: (doc.edges || []).map((edge) => {
      if (edge.id !== id) {
        return edge;
      }

      const updated = { ...edge };

      if (patch.label !== undefined) {
        if (patch.label === '') {
          delete updated.label;
        } else {
          updated.label = patch.label;
        }
      }

      return updated;
    }),
  };
}

/**
 * Rewrites the VLAN of every device interface from the network of the edge that
 * uses its handle. Unconnected interfaces keep an empty VLAN, which the working
 * copy allows and publishing rejects.
 *
 * @param {object} doc
 * @returns {object} document
 */
export function syncInterfaceVLANs(doc) {
  const networkByHandle = new Map();

  (doc.edges || []).forEach((edge) => {
    const network = findNetwork(doc, edge.networkId);

    if (!network) {
      return;
    }

    if (edge.sourceHandleId) {
      networkByHandle.set(edge.sourceHandleId, network.name);
    }

    if (edge.targetHandleId) {
      networkByHandle.set(edge.targetHandleId, network.name);
    }
  });

  return {
    ...doc,
    nodes: (doc.nodes || []).map((node) => {
      if (node.kind !== 'device') {
        return node;
      }

      const vlanByName = new Map();

      deviceHandles(node).forEach((handle) => {
        vlanByName.set(handle.name, networkByHandle.get(handle.id) || '');
      });

      const interfaces = specInterfaces(node);
      const changed = interfaces.some(
        (iface) =>
          vlanByName.has(iface.name) &&
          iface.vlan !== vlanByName.get(iface.name),
      );

      if (!changed) {
        return node;
      }

      const copy = JSON.parse(JSON.stringify(node));

      copy.device.spec.network.interfaces = interfaces.map((iface) =>
        vlanByName.has(iface.name)
          ? { ...iface, vlan: vlanByName.get(iface.name) }
          : iface,
      );

      return copy;
    }),
  };
}

// --- viewport / grid -------------------------------------------------------

/**
 * @param {object} doc
 * @param {{x: number, y: number, zoom: number}} viewport
 * @returns {object} document
 */
export function setViewport(doc, viewport) {
  return {
    ...doc,
    viewport: {
      x: viewport?.x ?? 0,
      y: viewport?.y ?? 0,
      zoom: viewport?.zoom ?? 1,
    },
  };
}

/**
 * @param {object} doc
 * @param {object} patch enabled, size, snap
 * @returns {object} document
 */
export function setGrid(doc, patch = {}) {
  return { ...doc, grid: { ...doc.grid, ...patch } };
}

/**
 * @param {object} doc
 * @param {object} patch name, description
 * @returns {object} document
 */
export function setDocumentInfo(doc, patch = {}) {
  const next = { ...doc };

  if (patch.name !== undefined) {
    next.name = patch.name;
  }

  if (patch.description !== undefined) {
    next.description = patch.description;
  }

  return next;
}

/**
 * Sets or clears the document's scenario reference.
 *
 * @param {object} doc
 * @param {object|null} scenario
 * @returns {object} document
 */
export function setScenario(doc, scenario) {
  const next = { ...doc };

  if (!scenario) {
    delete next.scenario;

    return next;
  }

  next.scenario = scenario;

  return next;
}

/**
 * Summary counts for status text and the drafts list.
 *
 * @param {object} doc
 * @returns {{devices: number, switches: number, notes: number, groups: number,
 *   networks: number, links: number}}
 */
export function documentSummary(doc) {
  const nodes = doc?.nodes || [];

  return {
    devices: nodes.filter((node) => node.kind === 'device').length,
    switches: nodes.filter((node) => node.kind === 'switch').length,
    notes: nodes.filter((node) => node.kind === 'note').length,
    groups: nodes.filter((node) => node.kind === 'group').length,
    networks: (doc?.networks || []).length,
    links: (doc?.edges || []).length,
  };
}

/**
 * Display label for a node, used by the canvas, the outline and dialogs.
 *
 * @param {object} node
 * @returns {string}
 */
export function nodeLabel(node) {
  if (!node) {
    return '';
  }

  if (node.label) {
    return node.label;
  }

  switch (node.kind) {
    case 'device':
      return node.device?.hostname || 'device';
    case 'group':
      return node.group?.title || 'Group';
    case 'note':
      return node.note?.text?.split('\n')[0] || 'Note';
    default:
      return kindMeta(node.kind).label;
  }
}

/**
 * Comment shown on hover/focus. Device comments are the phenix node
 * description; notes use their text.
 *
 * @param {object} node
 * @returns {string}
 */
export function nodeComment(node) {
  if (node?.kind === 'device') {
    return node.device?.spec?.general?.description || '';
  }

  if (node?.kind === 'note') {
    return node.note?.text || '';
  }

  return '';
}
