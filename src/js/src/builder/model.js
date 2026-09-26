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

// The colors addNetwork gives networks in turn. A network with one of them is
// drawn in its theme token instead (see colors.js).
export const DEFAULT_NETWORK_COLORS = [
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
 * @returns {object|undefined} network matched case-insensitively, the one
 *   of that very name first: networks may differ only by case, as minimega
 *   VLANs may
 */
export function networkByName(doc, name) {
  const wanted = String(name || '').toLowerCase();
  const networks = doc?.networks || [];

  return (
    networks.find((network) => network.name === name) ||
    networks.find((network) => network.name.toLowerCase() === wanted)
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

// --- included devices ------------------------------------------------------
//
// A diagram generated from a topology with includeTopologies also shows the
// devices of the included topologies, marked with device.includedFrom. They
// are defined in their own topology, and publishing leaves them out (the
// includeTopologies reference brings them back), so they are read only here:
// they can be moved, grouped and laid out, but not changed, renamed, deleted,
// or connected differently. What would change them indirectly is refused too:
// their connections, the switches they are on, and the name of their network.

/**
 * The topology an included device comes from.
 *
 * @param {object} node
 * @returns {string} topology name, or '' for any other node
 */
export function includedFrom(node) {
  return node?.kind === 'device' ? node.device?.includedFrom || '' : '';
}

/**
 * Why an included device cannot be changed, for refusals.
 *
 * @param {object} node
 * @returns {string} '' when the node is not an included device
 */
export function includedReason(node) {
  const from = includedFrom(node);

  return from
    ? `${nodeLabel(node)} comes from included topology ${from}, so it is read only here. Change it in ${from}.`
    : '';
}

// The included device an edge attaches, if any.
function includedEndpoint(doc, edge) {
  return [edge?.sourceNodeId, edge?.targetNodeId]
    .map((id) => findNode(doc, id))
    .find((node) => includedFrom(node));
}

/**
 * The included device on a network: its name is fixed, because renaming the
 * network would move the device's interface to a VLAN its own topology does
 * not name.
 *
 * @param {object} doc
 * @param {string} networkId
 * @returns {object|undefined} device node
 */
function includedMember(doc, networkId) {
  for (const edge of doc?.edges || []) {
    const device = edge.networkId === networkId && includedEndpoint(doc, edge);

    if (device) {
      return device;
    }
  }

  return undefined;
}

/**
 * Why removing a node, connection or network would change an included device.
 *
 * @param {object} doc
 * @param {'nodes'|'edges'|'networks'} kind
 * @param {string} id
 * @returns {string} '' when it can be removed
 */
export function removalRefusal(doc, kind, id) {
  if (kind === 'nodes') {
    const node = findNode(doc, id);

    if (includedFrom(node)) {
      return includedReason(node);
    }

    return node?.kind === 'switch'
      ? networkRefusal(doc, node.switch?.networkId, 'removed')
      : '';
  }

  if (kind === 'edges') {
    const device = includedEndpoint(doc, findEdge(doc, id));

    return device ? includedReason(device) : '';
  }

  return networkRefusal(doc, id, 'removed');
}

/**
 * Why a network cannot be renamed or removed.
 *
 * @param {object} doc
 * @param {string} networkId
 * @param {string} [change] what would happen to the network
 * @returns {string} '' when it can
 */
export function networkRefusal(doc, networkId, change = 'renamed') {
  const device = includedMember(doc, networkId);
  const network = findNetwork(doc, networkId);

  return device
    ? `${network ? `Network ${network.name}` : 'This network'} cannot be ` +
        `${change}: ${nodeLabel(device)} from included topology ` +
        `${includedFrom(device)} is on it.`
    : '';
}

// --- networks --------------------------------------------------------------

function nextNetworkColor(doc) {
  // The palette color fewest networks use, earliest first, so a network
  // added after another was removed does not repeat a color in use.
  const uses = DEFAULT_NETWORK_COLORS.map(
    (color) =>
      (doc.networks || []).filter((network) => network.color === color).length,
  );

  return DEFAULT_NETWORK_COLORS[uses.indexOf(Math.min(...uses))];
}

/**
 * Whether a network in the document already uses the color.
 *
 * @param {object} doc
 * @param {string} color
 * @returns {boolean}
 */
export function networkColorInUse(doc, color) {
  return (doc.networks || []).some((network) => network.color === color);
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

  // An included device on the network keeps its name (see networkRefusal).
  if (patch.name !== undefined && !includedMember(doc, id)) {
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

  return syncInterfaceVLANs(nameSwitches(next, id));
}

// A switch is named after its network: its label is the network's name,
// kept here for the places that name a node without the document. Every
// switch of the network is renamed with it.
function nameSwitches(doc, networkId) {
  const name = findNetwork(doc, networkId)?.name;

  if (name === undefined) {
    return doc;
  }

  return {
    ...doc,
    nodes: (doc.nodes || []).map((node) =>
      node.kind === 'switch' &&
      node.switch?.networkId === networkId &&
      node.label !== name
        ? { ...node, label: name }
        : node,
    ),
  };
}

/**
 * The document with every switch named after its network (see
 * nameSwitches), as a document saved or imported with a stale switch label
 * is opened; the same document when every switch is already.
 *
 * @param {object} doc
 * @returns {object} document
 */
export function namedSwitches(doc) {
  return (doc?.networks || []).reduce(
    (next, network) => nameSwitches(next, network.id),
    doc,
  );
}

/**
 * Removes networks together with their switch nodes and edges.
 *
 * @param {object} doc
 * @param {string[]} ids
 * @returns {object} document
 */
export function removeNetworks(doc, ids) {
  const removing = new Set(
    (ids || []).filter((id) => !removalRefusal(doc, 'networks', id)),
  );

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
    type: init.type || 'VirtualMachine',
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

      // Named after its network (see nameSwitches), whatever label it came
      // with.
      const bound = network || findNetwork(next, node.switch.networkId);
      node.label = bound.name;
      break;
    }
    case 'note':
      // Named after its first line (see nodeLabel) unless given a label.
      node.note = { text: options.text || '', color: options.color || '' };
      break;
    case 'group':
      // Numbered past the groups there are, so each has a name of its own
      // in the outline, its forms and its announcements.
      node.group = {
        title:
          options.title ||
          options.label ||
          uniqueName(
            'Group',
            (doc.nodes || [])
              .filter((entry) => entry.kind === 'group')
              .map((entry) => entry.group?.title || entry.label),
            (value) => value,
            ' ',
          ),
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

  // An included device keeps its name and spec; where it sits is the
  // diagram's own.
  if (includedFrom(node)) {
    patch = { position: patch.position, size: patch.size };
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

    const networks = handleNetworks(doc);

    reconcileDeviceHandles(
      device,
      (handleId, vlan) =>
        networks.get(handleId)?.id === networkByName(doc, vlan)?.id,
    );

    updated.device = device;
    updated.label = patch.label !== undefined ? patch.label : device.hostname;
  }

  if (patch.switch && node.kind === 'switch') {
    updated.switch = { networkId: patch.switch.networkId };
  }

  // Named after its network (see nameSwitches), whatever the patch says.
  if (node.kind === 'switch') {
    updated.label =
      findNetwork(doc, updated.switch?.networkId)?.name ?? updated.label;
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
  const pruned = {
    ...next,
    edges: (next.edges || []).filter((edge) => {
      const handleId = edge.sourceHandleId || edge.targetHandleId;
      const touchesNode = edge.sourceNodeId === id || edge.targetNodeId === id;

      return !touchesNode || !handleId || live.has(handleId);
    }),
  };

  // A spec applied from the Inspector sets the connections its changed
  // VLANs name.
  return syncInterfaceVLANs(
    patch.device?.spec ? connectByVLAN(pruned, id, node) : pruned,
  );
}

/**
 * Keeps interface handles aligned with the spec's interface list: an interface
 * added through the inspector gains a handle, one removed loses it. Handles are
 * matched by name because that is the only stable key the phenix spec carries.
 * An interface renamed in the Inspector keeps its handle, and so its
 * connection: when the spec has as many interfaces as there are handles, a
 * name no handle has takes, in order, a handle whose name the spec no longer
 * has and whose connection its VLAN still names (sameNetwork). An interface
 * removed while another is added is no rename, and keeps no handle. Two spec
 * entries of one name get a handle each.
 *
 * @param {object} device device payload (mutated)
 * @param {Function} [sameNetwork] (handleId, vlan) whether the VLAN names the
 *   network the handle is connected to, or none when it is not connected
 */
function reconcileDeviceHandles(device, sameNetwork = () => true) {
  const handles = device.interfaces || [];
  const specInterfaces = (device.spec?.network?.interfaces || []).filter(
    (iface) => typeof iface?.name === 'string' && iface.name !== '',
  );
  const specNames = specInterfaces.map((iface) => iface.name);

  if (specNames.length === 0 && handles.length === 0) {
    return;
  }

  const byName = new Map(handles.map((handle) => [handle.name, handle]));
  const renamed = handles.filter((handle) => !specNames.includes(handle.name));
  const unnamed = specInterfaces.filter((iface) => !byName.has(iface.name));

  if (specNames.length === handles.length) {
    unnamed.forEach((iface) => {
      const index = renamed.findIndex((handle) =>
        sameNetwork(handle.id, iface.vlan),
      );

      if (index >= 0) {
        byName.set(iface.name, renamed.splice(index, 1)[0]);
      }
    });
  }

  const used = new Set();

  device.interfaces = specNames.map((name, index) => {
    const existing = byName.get(name);

    if (!existing || used.has(existing.id)) {
      return { id: newId(), name, index };
    }

    used.add(existing.id);

    return { ...existing, name, index };
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

  // Positions are absolute, so moving a group must carry its members (and
  // theirs) along; members moved explicitly keep their own new position.
  const shift = new Map();
  const nodes = doc.nodes || [];

  for (const node of nodes) {
    const position = byId.get(node.id);

    if (node.kind !== 'group' || !position) {
      continue;
    }

    const dx = position.x - node.position.x;
    const dy = position.y - node.position.y;

    if (dx === 0 && dy === 0) {
      continue;
    }

    for (const member of descendantsOf(nodes, node.id)) {
      if (!byId.has(member.id) && !shift.has(member.id)) {
        shift.set(member.id, { dx, dy });
      }
    }
  }

  return {
    ...doc,
    nodes: nodes.map((node) => {
      const position = byId.get(node.id);

      if (position) {
        return { ...node, position: { x: position.x, y: position.y } };
      }

      const delta = shift.get(node.id);

      return delta
        ? {
            ...node,
            position: {
              x: node.position.x + delta.dx,
              y: node.position.y + delta.dy,
            },
          }
        : node;
    }),
  };
}

// Every node nested under a group, at any depth.
function descendantsOf(nodes, groupId) {
  const found = [];
  const queue = [groupId];

  while (queue.length) {
    const parent = queue.shift();

    for (const node of nodes) {
      if (node.parentId === parent && !found.includes(node)) {
        found.push(node);
        queue.push(node.id);
      }
    }
  }

  return found;
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

// Room between a group's border and its members, as groupNodes leaves it.
const GROUP_PADDING = 40;
// Room kept around a node that a change of group moves.
const GROUP_GAP = 20;
// A group is never resized smaller than this, so its title stays readable.
const GROUP_MIN_SIZE = { width: 120, height: 80 };

function boxOf(node) {
  return { ...node.position, ...sizeOf(node) };
}

function overlaps(a, b) {
  return (
    a.x < b.x + b.width &&
    b.x < a.x + a.width &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height
  );
}

function contains(outer, inner) {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.width <= outer.x + outer.width &&
    inner.y + inner.height <= outer.y + outer.height
  );
}

// A node's groups, nearest first.
function ancestorsOf(doc, node) {
  const found = [];
  let current = node;

  while (current?.parentId) {
    const parent = findNode(doc, current.parentId);

    if (!parent || found.includes(parent)) {
      break;
    }

    found.push(parent);
    current = parent;
  }

  return found;
}

// Clear space freeSpot keeps between the node it places and the nodes
// already there.
const CLEARANCE = 20;
// Enough slots for any diagram: a walk stops at the first free one.
const MAX_SLOTS = 10000;

/**
 * The first slot of a grid walk, in reading order, where a node of `size`
 * would overlap no node already there, a group's box included, with room to
 * spare. Positions follow the document's grid while it snaps.
 *
 * @param {object} doc
 * @param {{width: number, height: number}} size
 * @param {object} walk origin: the first slot's corner; slot: the step
 *   across and down; columns: slots across; within: a box the node must
 *   stay inside, which ends the walk at the first row below it; ignore: the
 *   ids of the nodes not in the way (the node placed, its members, and the
 *   groups it goes into)
 * @returns {{x: number, y: number}|null} null when no slot within is free
 */
export function freeSpot(
  doc,
  size,
  { origin, slot, columns, within = null, ignore = [] },
) {
  const skip = new Set(ignore);
  const boxes = (doc?.nodes || [])
    .filter((node) => !skip.has(node.id))
    .map(boxOf);
  const grid = doc?.grid?.snap !== false ? doc?.grid?.size || 0 : 0;
  const snap = (value) =>
    grid > 0 ? Math.round(value / grid) * grid : Math.round(value);
  const across = Math.max(1, Math.floor(columns) || 1);

  for (let index = 0; index < MAX_SLOTS; index += 1) {
    const position = {
      x: snap(origin.x + (index % across) * slot.width),
      y: snap(origin.y + Math.floor(index / across) * slot.height),
    };

    if (within && position.y + size.height > within.y + within.height) {
      return null;
    }

    if (within && !contains(within, { ...position, ...size })) {
      continue;
    }

    const room = {
      x: position.x - CLEARANCE,
      y: position.y - CLEARANCE,
      width: size.width + CLEARANCE * 2,
      height: size.height + CLEARANCE * 2,
    };

    if (!boxes.some((box) => overlaps(room, box))) {
      return position;
    }
  }

  return null;
}

// The ids of a node, the nodes nested under it and the groups it is in.
function familyOf(doc, node) {
  return [
    node.id,
    ...descendantsOf(doc.nodes || [], node.id).map((entry) => entry.id),
    ...ancestorsOf(doc, node).map((entry) => entry.id),
  ];
}

// A walk for freeSpot of slots the size of `box`, with a gap between them.
function slotsFor(box, origin, columns) {
  return {
    origin,
    slot: { width: box.width + GROUP_GAP, height: box.height + GROUP_GAP },
    columns,
  };
}

// Where a node moving to group `target` (null for none) goes so that its box
// says which group it is in, and whether that group must grow to hold it:
// a free spot inside the new group, or else below its members, which the
// group grows to hold; outside the groups it leaves, in a free spot beside
// the outermost. Position null when it can stay where it is.
function regroupedPosition(doc, node, target) {
  const box = boxOf(node);
  const group = target ? findNode(doc, target) : null;
  const staying = new Set(group ? [group, ...ancestorsOf(doc, group)] : []);
  const left = ancestorsOf(doc, node).filter((entry) => !staying.has(entry));
  const clear = left.every((entry) => !overlaps(box, boxOf(entry)));
  const moving = [
    node.id,
    ...descendantsOf(doc.nodes || [], node.id).map((entry) => entry.id),
  ];

  if (group) {
    if (clear && contains(boxOf(group), box)) {
      return { position: null, grow: false };
    }

    const room = boxOf(group);
    const inner = {
      x: room.x + GROUP_PADDING,
      y: room.y + GROUP_PADDING,
      width: room.width - GROUP_PADDING * 2,
      height: room.height - GROUP_PADDING * 2,
    };
    const spot =
      inner.width >= box.width && inner.height >= box.height
        ? freeSpot(doc, box, {
            ...slotsFor(
              box,
              inner,
              (inner.width + GROUP_GAP) / (box.width + GROUP_GAP),
            ),
            within: inner,
            ignore: [...moving, ...[...staying].map((entry) => entry.id)],
          })
        : null;

    if (spot) {
      return { position: spot, grow: false };
    }

    const members = (doc.nodes || []).filter(
      (entry) => entry.parentId === group.id && entry.id !== node.id,
    );

    return {
      position: {
        x: group.position.x + GROUP_PADDING,
        y: members.length
          ? Math.max(
              ...members.map(
                (entry) => entry.position.y + sizeOf(entry).height,
              ),
            ) + GROUP_GAP
          : group.position.y + GROUP_PADDING,
      },
      grow: true,
    };
  }

  if (clear) {
    return { position: null, grow: false };
  }

  // Beside the outermost group it leaves, level with where it was, or the
  // first free spot after that.
  const outer = boxOf(left[left.length - 1]);

  return {
    position: freeSpot(doc, box, {
      ...slotsFor(box, { x: outer.x + outer.width + GROUP_GAP, y: box.y }, 4),
      ignore: moving,
    }),
    grow: false,
  };
}

// Moves the nodes a group, grown, now covers out of its way, each to the
// first free spot below it, with what they hold. A node in a group the grown
// one is in may stay in that group, which grows to hold it in turn.
function clearGroup(doc, id) {
  const group = findNode(doc, id);

  if (!group) {
    return doc;
  }

  const box = boxOf(group);
  const family = new Set(familyOf(doc, group));
  const covered = (doc.nodes || []).filter(
    (entry) => !family.has(entry.id) && overlaps(box, boxOf(entry)),
  );
  const outermost = covered.filter(
    (entry) =>
      !ancestorsOf(doc, entry).some((other) => covered.includes(other)),
  );
  let next = doc;

  for (const entry of outermost) {
    const current = findNode(next, entry.id);
    const own = boxOf(current);
    const position = freeSpot(next, own, {
      ...slotsFor(own, { x: own.x, y: box.y + box.height + GROUP_GAP }, 4),
      ignore: familyOf(next, current),
    });

    if (position) {
      next = moveNodes(next, [{ id: entry.id, position }]);

      if (current.parentId) {
        next = fitGroups(next, [current.parentId]);
      }
    }
  }

  return next;
}

/**
 * Grows each group, and the groups it is in, to hold its members with room
 * to spare. A group never shrinks here, and its members stay where they are.
 *
 * @param {object} doc
 * @param {string[]} ids group ids
 * @returns {object} document
 */
export function fitGroups(doc, ids = []) {
  let next = doc;
  const queue = [...ids];
  const seen = new Set();

  while (queue.length) {
    const id = queue.shift();
    const group = findNode(next, id);

    if (seen.has(id) || group?.kind !== 'group') {
      continue;
    }

    seen.add(id);

    const members = next.nodes.filter((entry) => entry.parentId === id);

    if (members.length) {
      const need = boundsOf(members, GROUP_PADDING);
      const has = boxOf(group);
      const x = Math.min(has.x, need.x);
      const y = Math.min(has.y, need.y);
      const width = Math.max(has.x + has.width, need.x + need.width) - x;
      const height = Math.max(has.y + has.height, need.y + need.height) - y;

      if (
        x !== has.x ||
        y !== has.y ||
        width !== has.width ||
        height !== has.height
      ) {
        next = {
          ...next,
          nodes: next.nodes.map((entry) =>
            entry.id === id
              ? { ...entry, position: { x, y }, size: { width, height } }
              : entry,
          ),
        };
      }
    }

    if (group.parentId) {
      queue.push(group.parentId);
    }
  }

  return next;
}

/**
 * The smallest size a group can be resized to: its members stay inside it,
 * with a grid step to spare, and its title stays readable.
 *
 * @param {object} doc
 * @param {string} id group id
 * @returns {{width: number, height: number}}
 */
export function groupMinimumSize(doc, id) {
  const group = findNode(doc, id);
  const members = (doc?.nodes || []).filter((entry) => entry.parentId === id);
  const spare = doc?.grid?.size || DEFAULT_GRID_SIZE;

  if (!group || members.length === 0) {
    return { ...GROUP_MIN_SIZE };
  }

  const bounds = boundsOf(members);

  return {
    width: Math.max(
      GROUP_MIN_SIZE.width,
      bounds.x + bounds.width + spare - group.position.x,
    ),
    height: Math.max(
      GROUP_MIN_SIZE.height,
      bounds.y + bounds.height + spare - group.position.y,
    ),
  };
}

/**
 * Moves a node into a group, into another one, or out of its group
 * (`parentId` null), rejecting cycles and non-group parents. The node, with
 * any members of its own, moves so that its place on the canvas agrees:
 * into a free spot in the new group, or else below its members, the group
 * growing to hold it and moving the nodes it would cover out of its way; or
 * to a free spot beside the group it left. So no node overlaps another, or
 * sits in a group it is not in. Moving a node to the group it is in already
 * changes nothing.
 *
 * @param {object} doc
 * @param {string} id
 * @param {string|null} parentId
 * @returns {object} document; the same one when nothing changes
 */
export function setParent(doc, id, parentId) {
  const node = findNode(doc, id);
  const target = parentId || null;

  if (!node || (node.parentId || null) === target) {
    return doc;
  }

  if (target) {
    const parent = findNode(doc, target);

    if (
      !parent ||
      parent.kind !== 'group' ||
      target === id ||
      isDescendant(doc, target, id)
    ) {
      return doc;
    }
  }

  const { position, grow } = regroupedPosition(doc, node, target);
  const regrouped = {
    ...doc,
    nodes: doc.nodes.map((entry) => {
      if (entry.id !== id) {
        return entry;
      }

      const updated = { ...entry };

      if (target) {
        updated.parentId = target;
      } else {
        delete updated.parentId;
      }

      return updated;
    }),
  };
  let next = position ? moveNodes(regrouped, [{ id, position }]) : regrouped;

  if (!grow) {
    return next;
  }

  // The group, and each group it is in, grows to hold the node, and moves
  // what it then covers out of its way, innermost first.
  const joined = findNode(next, target);

  for (const group of [joined, ...ancestorsOf(next, joined)]) {
    next = clearGroup(fitGroups(next, [group.id]), group.id);
  }

  return next;
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
    title: options.title,
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

// The nodes a removal of `ids` takes: those nodes and every node nested in
// them, less what would change an included device (see removalRefusal).
// `kept` holds the refusal of each node left in place.
function nodeRemoval(doc, ids = []) {
  const removing = new Set(ids);

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

  const kept = [];

  removing.forEach((id) => {
    const reason = removalRefusal(doc, 'nodes', id);

    if (reason) {
      removing.delete(id);
      kept.push(reason);
    }
  });

  return { removing, kept };
}

/**
 * Why a removal leaves some of what it would take in place: the refusal (see
 * removalRefusal) of each selected connection, and of each selected node or
 * node nested in a selected group, that removeElements keeps.
 *
 * @param {object} doc
 * @param {{nodes?: string[], edges?: string[]}} selection
 * @returns {string[]} one reason per kept item, selected nodes first
 */
export function removalKept(doc, selection = {}) {
  return [
    ...nodeRemoval(doc, selection.nodes).kept,
    ...(selection.edges || [])
      .map((id) => removalRefusal(doc, 'edges', id))
      .filter(Boolean),
  ];
}

/**
 * Removes nodes (with their descendants), edges and networks.
 *
 * @param {object} doc
 * @param {{nodes?: string[], edges?: string[], networks?: string[]}} selection
 * @returns {object} document
 */
export function removeElements(doc, selection = {}) {
  const { removing } = nodeRemoval(doc, selection.nodes);

  // A node left inside a removed group, such as an included device, moves to
  // the nearest group that stays, as ungroup does.
  const survivingParent = (node) => {
    let parentId = node.parentId;
    const seen = new Set();

    while (parentId && removing.has(parentId) && !seen.has(parentId)) {
      seen.add(parentId);
      parentId = findNode(doc, parentId)?.parentId;
    }

    return parentId && !removing.has(parentId) ? parentId : '';
  };

  const removedEdges = new Set(
    (selection.edges || []).filter((id) => !removalRefusal(doc, 'edges', id)),
  );
  const nodes = (doc.nodes || [])
    .filter((node) => !removing.has(node.id))
    .map((node) => {
      if (!removing.has(node.parentId)) {
        return node;
      }

      const kept = { ...node };
      const parentId = survivingParent(node);

      if (parentId) {
        kept.parentId = parentId;
      } else {
        delete kept.parentId;
      }

      return kept;
    });
  const edges = (doc.edges || []).filter(
    (edge) =>
      !removedEdges.has(edge.id) &&
      !removing.has(edge.sourceNodeId) &&
      !removing.has(edge.targetNodeId),
  );

  const next = { ...doc, nodes, edges };
  const networks = new Set(selection.networks || []);

  const pruned = networks.size > 0 ? removeNetworks(next, [...networks]) : next;
  const remaining = new Set(pruned.edges.map((edge) => edge.id));
  const disconnected = (doc.edges || [])
    .filter((edge) => !remaining.has(edge.id))
    .flatMap((edge) => [edge.sourceHandleId, edge.targetHandleId]);

  return clearInterfaceVLANs(syncInterfaceVLANs(pruned), disconnected);
}

// --- interfaces ------------------------------------------------------------

function appendInterface(node, init = {}) {
  const taken = new Set(deviceHandles(node).map((handle) => handle.name));
  const base = String(init.name || '').trim() || nextInterfaceName(node);
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

  // A spec entry of that name already describes the interface (a pasted
  // device brings its own), so only an interface created here is added, as
  // an Ethernet interface with no address management (manual).
  const interfaces = node.device.spec.network.interfaces;

  if (!interfaces.some((iface) => iface?.name === name)) {
    interfaces.push({
      name,
      type: init.type || 'ethernet',
      proto: init.proto || 'manual',
      vlan: init.vlan || '',
    });
  }

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

  if (!node || node.kind !== 'device' || includedFrom(node)) {
    return { doc, handle: null };
  }

  const copy = JSON.parse(JSON.stringify(node));
  const handle = appendInterface(copy, {
    ...init,
    name: init.name || nextInterfaceName(node),
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
 * The name of a new interface: one past the highest ethN the device has, so
 * a device with eth1 and eth2 gets eth3 rather than a gap such as eth0. Spec
 * entries count as well as handles. Every new interface is named this way:
 * a drawn connection, Add connection point and the Inspector's interfaces.
 *
 * @param {object} node device node
 * @returns {string} eth0 when the device has no ethN interface yet
 */
export function nextInterfaceName(node) {
  const numbers = [
    ...deviceHandles(node).map((handle) => handle.name),
    ...specInterfaces(node).map((iface) => iface?.name),
  ]
    .map((name) => /^eth(\d+)$/.exec(String(name ?? ''))?.[1])
    .filter((digits) => digits !== undefined)
    .map(Number);

  return `eth${numbers.length ? Math.max(...numbers) + 1 : 0}`;
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

  if (!node || node.kind !== 'device' || includedFrom(node)) {
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

  if (!handle || includedFrom(node)) {
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

  if (includedFrom(device)) {
    return { valid: false, reason: includedReason(device) };
  }

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
 *   targetHandleId, label, color
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

  if (connection.color) {
    edge.color = connection.color;
  }

  next = { ...next, edges: [...(next.edges || []), edge] };

  return { doc: syncInterfaceVLANs(next), edge };
}

/**
 * Connects any two nodes the way the legacy Builder did, for drag-to-connect
 * on the canvas:
 *
 * - device and switch: the device joins the switch's network, on the given
 *   interface or on a new one;
 * - two devices: they get a network of their own, drawn as a new switch
 *   between them (a network is always a switch here), each on the given
 *   interface or a new one;
 * - anything else (two switches, notes, groups) is refused with a reason.
 *
 * @param {object} doc
 * @param {object} connection sourceNodeId, sourceHandleId, targetNodeId,
 *   targetHandleId
 * @returns {{doc: object, edges: object[], node?: object, error?: string}}
 */
export function connectNodes(doc, connection = {}) {
  const check = canConnect(doc, connection);

  if (!check.valid) {
    return { doc, edges: [], error: check.reason };
  }

  const source = findNode(doc, connection.sourceNodeId);
  const target = findNode(doc, connection.targetNodeId);

  if (source.kind === 'device' && target.kind === 'device') {
    return connectDevices(doc, source, target, connection);
  }

  const result = connect(doc, connection);

  return result.error
    ? { doc, edges: [], error: result.error }
    : { doc: result.doc, edges: [result.edge] };
}

/**
 * Whether two nodes can be joined by drag-to-connect, and why not. The canvas
 * uses it while dragging so refused targets never look valid.
 *
 * @param {object} doc
 * @param {object} connection sourceNodeId, sourceHandleId, targetNodeId,
 *   targetHandleId
 * @returns {{valid: boolean, reason?: string}}
 */
export function canConnect(doc, connection = {}) {
  const source = findNode(doc, connection.sourceNodeId);
  const target = findNode(doc, connection.targetNodeId);

  if (!source || !target) {
    return { valid: false, reason: 'Choose two nodes to connect.' };
  }

  if (source.id === target.id) {
    return { valid: false, reason: 'A node cannot be connected to itself.' };
  }

  const kinds = [source.kind, target.kind];

  if (kinds.includes('note') || kinds.includes('group')) {
    return {
      valid: false,
      reason: 'Notes and groups do not take connections.',
    };
  }

  if (source.kind === 'switch' && target.kind === 'switch') {
    return {
      valid: false,
      reason:
        'Two switches cannot be connected: each switch is one network. Connect devices to it instead.',
    };
  }

  const included = [source, target].find((node) => includedFrom(node));

  if (included) {
    return { valid: false, reason: includedReason(included) };
  }

  for (const [node, handleId] of [
    [source, connection.sourceHandleId],
    [target, connection.targetHandleId],
  ]) {
    if (node.kind !== 'device' || !handleId) {
      continue;
    }

    const handle = deviceHandles(node).find((entry) => entry.id === handleId);

    if (!handle) {
      return {
        valid: false,
        reason: `Unknown interface on device ${node.device.hostname}.`,
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

  if (source.kind !== target.kind) {
    const switchNode = source.kind === 'switch' ? source : target;

    if (!findNetwork(doc, switchNode.switch?.networkId)) {
      return { valid: false, reason: 'That switch has no network.' };
    }
  }

  return { valid: true };
}

// Joins two devices through a new switch between them. The switch goes
// halfway between the devices when that spot is free; otherwise just below
// or above the pair, or beside it, wherever it overlaps no other node. Two
// devices in the same group keep their switch in that group.
function connectDevices(doc, a, b, connection) {
  const size = DEFAULT_SIZES.switch;
  const parentId = a.parentId && a.parentId === b.parentId ? a.parentId : '';
  const added = addNode(doc, {
    kind: 'switch',
    position: freeSpotBetween(doc, a, b, size),
    ...(parentId ? { parentId } : {}),
  });

  const first = connect(added.doc, {
    sourceNodeId: a.id,
    sourceHandleId: connection.sourceHandleId,
    targetNodeId: added.node.id,
  });
  const second = first.error
    ? first
    : connect(first.doc, {
        sourceNodeId: added.node.id,
        targetNodeId: b.id,
        targetHandleId: connection.targetHandleId,
      });

  if (second.error) {
    return { doc, edges: [], error: second.error };
  }

  return {
    doc: second.doc,
    edges: [first.edge, second.edge],
    node: added.node,
  };
}

// The top-left corner for a box of `size` near the middle of two nodes that
// overlaps no other node (groups excluded: a box may sit inside a group).
function freeSpotBetween(doc, a, b, size) {
  const box = (node) => {
    const { width, height } = sizeOf(node);

    return {
      left: node.position.x,
      top: node.position.y,
      right: node.position.x + width,
      bottom: node.position.y + height,
    };
  };
  const [first, second] = [box(a), box(b)];
  const gap = 40;
  const snap = (value) => {
    const grid = doc.grid?.size || DEFAULT_GRID_SIZE;

    return doc.grid?.snap === false
      ? Math.round(value)
      : Math.round(value / grid) * grid;
  };
  const middle = {
    x: (first.left + first.right + second.left + second.right) / 4,
    y: (first.top + first.bottom + second.top + second.bottom) / 4,
  };
  const candidates = [
    { x: middle.x - size.width / 2, y: middle.y - size.height / 2 },
    {
      x: middle.x - size.width / 2,
      y: Math.max(first.bottom, second.bottom) + gap,
    },
    {
      x: middle.x - size.width / 2,
      y: Math.min(first.top, second.top) - gap - size.height,
    },
    {
      x: Math.max(first.right, second.right) + gap,
      y: middle.y - size.height / 2,
    },
  ].map((spot) => ({ x: snap(spot.x), y: snap(spot.y) }));
  const obstacles = (doc.nodes || [])
    .filter((node) => node.kind !== 'group')
    .map(box);
  const clear = (spot) =>
    obstacles.every(
      (other) =>
        spot.x + size.width <= other.left ||
        spot.x >= other.right ||
        spot.y + size.height <= other.top ||
        spot.y >= other.bottom,
    );

  return candidates.find(clear) || candidates[1];
}

/**
 * @param {object} doc
 * @param {string} id
 * @param {object} patch label, and color, the connection's own, drawn in
 *   place of its network's; '' removes either
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

      for (const key of ['label', 'color']) {
        if (patch[key] === '') {
          delete updated[key];
        } else if (patch[key] !== undefined) {
          updated[key] = patch[key];
        }
      }

      return updated;
    }),
  };
}

// --- interface VLANs -------------------------------------------------------
//
// An interface's VLAN is the network it is on, in phenix and here alike: a
// connected interface's VLAN is its network's name, and typing a VLAN in the
// Inspector chooses the connection (see connectByVLAN). An unconnected
// interface keeps the VLAN it has, which may name a network the diagram does
// not have: phenix creates VLANs by name, and publishing keeps it. One with
// no VLAN is on no network, which a draft may have but a published topology
// may not: publishing refuses it (see validate.js).

// The network each connected interface handle is on, by handle id.
function handleNetworks(doc) {
  const networks = new Map();

  (doc.edges || []).forEach((edge) => {
    const network = findNetwork(doc, edge.networkId);

    if (!network) {
      return;
    }

    [edge.sourceHandleId, edge.targetHandleId]
      .filter(Boolean)
      .forEach((handleId) => networks.set(handleId, network));
  });

  return networks;
}

// Sets the VLAN of the interface each handle maps onto to vlanOf(handle),
// or leaves it when that is undefined. Unchanged nodes are kept as they are.
function setInterfaceVLANs(doc, vlanOf) {
  return {
    ...doc,
    nodes: (doc.nodes || []).map((node) => {
      if (node.kind !== 'device') {
        return node;
      }

      const vlanByName = new Map();

      deviceHandles(node).forEach((handle) => {
        const vlan = vlanOf(handle);

        if (vlan !== undefined) {
          vlanByName.set(handle.name, vlan);
        }
      });

      const interfaces = specInterfaces(node);
      const changed = interfaces.some(
        (iface) =>
          vlanByName.has(iface?.name) &&
          iface.vlan !== vlanByName.get(iface.name),
      );

      if (!changed) {
        return node;
      }

      const copy = JSON.parse(JSON.stringify(node));

      copy.device.spec.network.interfaces = interfaces.map((iface) =>
        vlanByName.has(iface?.name)
          ? { ...iface, vlan: vlanByName.get(iface.name) }
          : iface,
      );

      return copy;
    }),
  };
}

/**
 * Rewrites the VLAN of every connected device interface to the name of its
 * network. The VLAN of an unconnected interface is left as it is (see
 * clearInterfaceVLANs for one that loses its connection).
 *
 * @param {object} doc
 * @returns {object} document
 */
export function syncInterfaceVLANs(doc) {
  const networks = handleNetworks(doc);

  return setInterfaceVLANs(doc, (handle) => networks.get(handle.id)?.name);
}

/**
 * Empties the VLAN of each of these interfaces that has no connection. One
 * whose connection was removed, or that was pasted without it, is on no
 * network, but its VLAN would still name the network, and publishing would
 * put it there.
 *
 * @param {object} doc
 * @param {string[]} handleIds interface handle ids
 * @returns {object} document
 */
export function clearInterfaceVLANs(doc, handleIds = []) {
  const clearing = new Set(handleIds.filter(Boolean));
  const connected = new Set(
    (doc.edges || []).flatMap((edge) => [
      edge.sourceHandleId,
      edge.targetHandleId,
    ]),
  );

  if (clearing.size === 0) {
    return doc;
  }

  return setInterfaceVLANs(doc, (handle) =>
    clearing.has(handle.id) && !connected.has(handle.id) ? '' : undefined,
  );
}

// A network's first switch, where a connection its VLAN makes goes.
function firstSwitch(doc, networkId) {
  return (doc.nodes || []).find(
    (node) => node.kind === 'switch' && node.switch?.networkId === networkId,
  );
}

/**
 * Connects each interface of a device whose VLAN an edit changed, or that
 * it added, to the network its VLAN names, as the Inspector applies a VLAN:
 * typing one is choosing the connection. A VLAN naming a network of the
 * diagram, regardless of case, connects the interface to that network's
 * first switch, moving a connection it has to another network; a network
 * with no switch on the canvas gets one beside the device, as a connection
 * drawn between two devices does (see connectDevices), since publishing
 * puts the interface on that network. Any other VLAN, naming no network
 * here, leaves the interface unconnected and is kept as it is, and an empty
 * one is stored as '', as a connection removed on the canvas leaves it.
 * Other interfaces keep their connection.
 *
 * @param {object} doc
 * @param {string} nodeId device node
 * @param {object} previous the device node before the edit
 * @returns {object} document
 */
function connectByVLAN(doc, nodeId, previous) {
  const node = findNode(doc, nodeId);
  const edges = [...(doc.edges || [])];
  const emptied = [];
  const vlanOf = (iface) => String(iface?.vlan ?? '').trim();
  const before = new Map(
    specInterfaces(previous).map((iface) => [iface?.name, vlanOf(iface)]),
  );
  let next = doc;

  deviceHandles(node).forEach((handle) => {
    const vlan = vlanOf(specInterfaceFor(node, handle.id));

    if (before.has(handle.name) && before.get(handle.name) === vlan) {
      return;
    }

    const network = vlan ? networkByName(next, vlan) : undefined;
    let hub = network ? firstSwitch(next, network.id) : undefined;

    if (network && !hub) {
      const added = addNode(next, {
        kind: 'switch',
        networkId: network.id,
        position: freeSpotBetween(next, node, node, DEFAULT_SIZES.switch),
        ...(node.parentId ? { parentId: node.parentId } : {}),
      });

      next = added.doc;
      hub = added.node;
    }

    const index = edges.findIndex(
      (edge) =>
        edge.sourceHandleId === handle.id || edge.targetHandleId === handle.id,
    );
    const edge = edges[index];

    if (!hub) {
      if (edge) {
        edges.splice(index, 1);
      }

      if (!vlan) {
        emptied.push(handle.id);
      }
    } else if (!edge) {
      edges.push({
        id: newId(),
        sourceNodeId: node.id,
        sourceHandleId: handle.id,
        targetNodeId: hub.id,
        networkId: network.id,
      });
    } else if (edge.networkId !== network.id) {
      // Moved, keeping its id, label and color; the device keeps its end.
      edges[index] = {
        ...edge,
        [edge.sourceHandleId === handle.id ? 'targetNodeId' : 'sourceNodeId']:
          hub.id,
        networkId: network.id,
      };
    }
  });

  return clearInterfaceVLANs({ ...next, edges }, emptied);
}

/**
 * What an edit changed about the connections of a device's interfaces, for
 * its announcement: each switch it added for them first, then each
 * interface whose connection changed. Interfaces are matched by handle,
 * which one renamed keeps (see reconcileDeviceHandles), else by name, as
 * their spec entries are; one the edit removed is not listed.
 *
 * @param {object} before document
 * @param {object} after document
 * @param {string} nodeId device node
 * @returns {string[]} "added a switch for network EXP", "connected eth0 to
 *   network EXP", "moved eth1 from network EXP to network LAN",
 *   "disconnected eth2 from network EXP"
 */
export function connectionChanges(before, after, nodeId) {
  const handlesBefore = deviceHandles(findNode(before, nodeId));
  const ids = new Set(handlesBefore.map((handle) => handle.id));
  const was = new Map(handlesBefore.map((handle) => [handle.name, handle.id]));
  const networksBefore = handleNetworks(before);
  const networksAfter = handleNetworks(after);
  const switches = new Set(
    (before.nodes || [])
      .filter((node) => node.kind === 'switch')
      .map((node) => node.id),
  );
  const added = (after.nodes || [])
    .filter((node) => node.kind === 'switch' && !switches.has(node.id))
    .map(
      (node) =>
        `added a switch for network ${networkOfSwitch(after, node)?.name}`,
    );

  const changed = deviceHandles(findNode(after, nodeId)).flatMap((handle) => {
    const from = networksBefore.get(
      ids.has(handle.id) ? handle.id : was.get(handle.name),
    );
    const to = networksAfter.get(handle.id);

    if (from?.id === to?.id) {
      return [];
    }

    if (!from) {
      return [`connected ${handle.name} to network ${to.name}`];
    }

    return to
      ? [`moved ${handle.name} from network ${from.name} to network ${to.name}`]
      : [`disconnected ${handle.name} from network ${from.name}`];
  });

  return [...added, ...changed];
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
 * Summary counts for status text and the drafts list. `included` counts the
 * devices (among `devices`) that come from included topologies, and
 * `includedLinks` the connections (among `links`) of those devices.
 * `includedSwitches` and `includedNetworks` count the switches and networks
 * that only those devices use: every connection they have is one of theirs.
 *
 * @param {object} doc
 * @returns {{devices: number, included: number, switches: number,
 *   includedSwitches: number, notes: number, groups: number,
 *   networks: number, includedNetworks: number, links: number,
 *   includedLinks: number}}
 */
export function documentSummary(doc) {
  const nodes = doc?.nodes || [];
  const edges = doc?.edges || [];
  const included = new Set(
    nodes.filter((node) => includedFrom(node)).map((node) => node.id),
  );
  const theirs = (edge) =>
    included.has(edge.sourceNodeId) || included.has(edge.targetNodeId);
  // Used, and only by included devices.
  const onlyTheirs = (connections) =>
    connections.length > 0 && connections.every(theirs);
  const switches = nodes.filter((node) => node.kind === 'switch');
  const networks = doc?.networks || [];

  return {
    devices: nodes.filter((node) => node.kind === 'device').length,
    included: included.size,
    switches: switches.length,
    includedSwitches: switches.filter((node) =>
      onlyTheirs(
        edges.filter(
          (edge) =>
            edge.sourceNodeId === node.id || edge.targetNodeId === node.id,
        ),
      ),
    ).length,
    notes: nodes.filter((node) => node.kind === 'note').length,
    groups: nodes.filter((node) => node.kind === 'group').length,
    networks: networks.length,
    includedNetworks: networks.filter((network) =>
      onlyTheirs(edges.filter((edge) => edge.networkId === network.id)),
    ).length,
    links: edges.length,
    includedLinks: edges.filter(theirs).length,
  };
}

/**
 * A note's first line of text, its spaces collapsed: what an unlabelled note
 * is named after.
 *
 * @param {object} node
 * @returns {string} '' for a note without text
 */
function noteTitle(node) {
  const line = String(node?.note?.text || '')
    .split('\n')
    .find((entry) => entry.trim() !== '');

  return (line || '').replace(/\s+/g, ' ').trim();
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

  // A note labelled only with its kind, as notes once were, is named after
  // its text too.
  if (node.label && !(node.kind === 'note' && node.label === 'Note')) {
    return node.label;
  }

  switch (node.kind) {
    case 'device':
      return node.device?.hostname || 'device';
    case 'group':
      return node.group?.title || 'Group';
    case 'note':
      return noteTitle(node) || 'Note';
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
