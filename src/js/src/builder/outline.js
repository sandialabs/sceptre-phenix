// Semantic outline: the accessible, non-drag mirror of the canvas.
//
// The outline is derived from the same document as the canvas. Its rows are
// the nodes, and its forms connect and group them; Disconnect in the
// Inspector's Connection points or the command palette removes a connection,
// so nothing needs a drag. A row's name
// (outlineLabel) is also the canvas node's accessible name, which is why it
// names the networks a device is on rather than relying on the colors of
// its connections on the canvas.

import { count, listOf } from './announce.js';
import { nodeIcon, nodeIconKey } from './catalog.js';
import {
  connectionEndLabel,
  deviceTypeLabel,
  documentSummary,
  findNetwork,
  findNode,
  includedFrom,
  includedReason,
  kindLabel,
  nodeComment,
  nodeLabel,
} from './model.js';
import { capitalize } from './text.js';

// Notes are named by the start of their text, up to this many characters.
const NOTE_SUMMARY_LENGTH = 80;

// Words for the device icon keys, which are the only visible sign of a
// device's type. A plain device is a Linux VM, shown with the server or Linux
// icon, so those two keys are not named: saying "Linux" on every row is noise.
// `also` lists other words that name the type in a label.
const DEVICE_TYPES = {
  centos: { name: 'CentOS' },
  container: { name: 'container' },
  desktop: { name: 'workstation', also: ['desktop'] },
  external: { name: 'external device', also: ['external'] },
  firewall: { name: 'firewall' },
  printer: { name: 'printer' },
  redhat: { name: 'Red Hat', also: ['redhat', 'rhel'] },
  router: { name: 'router' },
  switch: { name: 'switch' },
  vlan: { name: 'VLAN' },
  windows: { name: 'Windows' },
};

/**
 * Builds the outline tree: groups contain their members and ungrouped nodes
 * are top level. It lists nodes only; a node's name counts its connections
 * and names a device's networks, and the canvas, the Inspector's Connection
 * points and the command palette's Disconnect list the connections.
 *
 * @param {object} doc
 * @returns {object[]} outline items
 */
export function buildOutline(doc) {
  const nodes = (doc?.nodes || []).slice().sort(compareNodes);
  const byParent = new Map();
  const index = labelIndex(doc);

  nodes.forEach((node) => {
    const key = node.parentId || '';
    if (!byParent.has(key)) {
      byParent.set(key, []);
    }
    byParent.get(key).push(node);
  });

  const build = (parentId, depth, seen) =>
    (byParent.get(parentId) || [])
      .filter((node) => !seen.has(node.id))
      .map((node) => {
        seen.add(node.id);

        return {
          id: node.id,
          kind: node.kind,
          label: nodeLabel(node),
          iconKey: nodeIconKey(node),
          // The name of its custom icon, which the row draws in place of the
          // icon of its key (see iconSrcFor in BuilderOutline).
          icon: nodeIcon(node),
          depth,
          description: nodeComment(node),
          includedFrom: includedFrom(node),
          networkId:
            node.kind === 'switch' ? node.switch?.networkId : undefined,
          accessibleName: outlineLabel(doc, node, index),
          children:
            node.kind === 'group' ? build(node.id, depth + 1, seen) : [],
        };
      });

  return build('', 0, new Set());
}

/**
 * The nodes an outline row stands for on the canvas: a group with every
 * node in it, a switch with its network (each switch of the network and
 * each node connected on it), and anything else alone.
 *
 * @param {object} doc
 * @param {string} id the row's node
 * @returns {string[]} node ids, the row's own first; none when it is gone
 */
export function rowNodeIds(doc, id) {
  const nodes = doc?.nodes || [];
  const node = findNode(doc, id);
  const ids = new Set([id]);

  if (!node) {
    return [];
  }

  if (node.kind === 'group') {
    for (let grew = true; grew; ) {
      grew = false;

      for (const entry of nodes) {
        if (ids.has(entry.parentId) && !ids.has(entry.id)) {
          ids.add(entry.id);
          grew = true;
        }
      }
    }
  }

  const networkId = node.kind === 'switch' ? node.switch?.networkId : '';

  if (networkId) {
    for (const entry of nodes) {
      if (entry.kind === 'switch' && entry.switch?.networkId === networkId) {
        ids.add(entry.id);
      }
    }

    for (const edge of doc.edges || []) {
      if (edge.networkId === networkId) {
        ids.add(edge.sourceNodeId);
        ids.add(edge.targetNodeId);
      }
    }
  }

  return [...ids];
}

function compareNodes(a, b) {
  if (a.kind !== b.kind) {
    return kindRank(a.kind) - kindRank(b.kind);
  }

  return nodeLabel(a).localeCompare(nodeLabel(b), undefined, { numeric: true });
}

// Groups first, then the nodes that publish, then what is only drawn.
function kindRank(kind) {
  return (
    { group: 0, switch: 1, device: 2, note: 3, shape: 4, icon: 5, line: 6 }[
      kind
    ] ?? 7
  );
}

/**
 * Every connection, named from its device's end with the interface, as
 * "alpha (eth0) to EXP", with its network and label. The outline lists
 * nodes only, so these are what the command palette's Disconnect offers.
 * Nodes and networks are looked up once, so thousands of connections stay
 * linear.
 *
 * @param {object} doc
 * @returns {{id: string, name: string, networkName: string, label: string,
 *   locked: string}[]} `locked` is why it cannot be removed, or ''
 */
export function connectionList(doc) {
  const nodes = new Map((doc?.nodes || []).map((node) => [node.id, node]));
  const networks = new Map(
    (doc?.networks || []).map((network) => [network.id, network]),
  );

  const end = (id, handleId) => {
    const node = nodes.get(id);

    return { node, name: node ? connectionEndLabel(node, handleId) : id };
  };

  return (doc?.edges || [])
    .map((edge) => {
      const source = end(edge.sourceNodeId, edge.sourceHandleId);
      const target = end(edge.targetNodeId, edge.targetHandleId);
      // A connection is made from a device, so its device end comes first.
      const [from, to] =
        target.node?.kind === 'device' && source.node?.kind !== 'device'
          ? [target, source]
          : [source, target];
      const included = [source.node, target.node].find((node) =>
        includedFrom(node),
      );

      return {
        id: edge.id,
        name: `${from.name} to ${to.name}`,
        networkName: networks.get(edge.networkId)?.name || 'no network',
        label: edge.label || '',
        // A connection of an included device cannot be removed.
        locked: included ? includedReason(included) : '',
      };
    })
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
}

/**
 * What outlineLabel and the canvas's handles (handlesFor) look up, gathered
 * once for a whole document: each node's connections, the network of each
 * connected handle, each group's member count, and the nodes and networks
 * by id. Naming and drawing every node then stays linear in the size of
 * the document.
 *
 * @param {object} doc
 * @returns {{links: Function, handle: Function, members: Function,
 *   node: Function, network: Function}} lookups by id; handle gives
 *   {networkId} for a connected handle, else null
 */
export function labelIndex(doc) {
  const links = new Map();
  const handles = new Map();
  const members = new Map();
  const nodes = new Map();
  const networks = new Map();

  for (const edge of doc?.edges || []) {
    for (const id of new Set([edge.sourceNodeId, edge.targetNodeId])) {
      if (!links.has(id)) {
        links.set(id, []);
      }

      links.get(id).push(edge);
    }

    // A handle in more than one connection is on the last one's network.
    for (const id of [edge.sourceHandleId, edge.targetHandleId]) {
      if (id) {
        handles.set(id, edge.networkId);
      }
    }
  }

  // The first of an id, as findNode and findNetwork find it.
  for (const node of doc?.nodes || []) {
    if (!nodes.has(node.id)) {
      nodes.set(node.id, node);
    }

    if (node.parentId) {
      members.set(node.parentId, (members.get(node.parentId) || 0) + 1);
    }
  }

  for (const network of doc?.networks || []) {
    if (!networks.has(network.id)) {
      networks.set(network.id, network);
    }
  }

  return {
    links: (id) => links.get(id) || [],
    handle: (id) => (handles.has(id) ? { networkId: handles.get(id) } : null),
    members: (id) => members.get(id) || 0,
    node: (id) => nodes.get(id),
    network: (id) => networks.get(id),
  };
}

// The same lookups, each a scan of the document: for naming one node.
function scanIndex(doc) {
  return {
    links: (id) =>
      (doc?.edges || []).filter(
        (edge) => edge.sourceNodeId === id || edge.targetNodeId === id,
      ),
    members: (id) =>
      (doc?.nodes || []).filter((entry) => entry.parentId === id).length,
    node: (id) => findNode(doc, id),
    network: (id) => findNetwork(doc, id),
  };
}

/**
 * Accessible name for a node: kind, label, device type (its icon's word,
 * then the phenix node type the canvas shows), the topology an included
 * device comes from, group, a switch's network, link or member count and
 * the networks a device is on, as "Device node, 2 connections, on EXP and
 * EXP-2".
 *
 * @param {object} doc
 * @param {object} node
 * @param {object} [index] labelIndex(doc), when naming many nodes
 * @returns {string}
 */
export function outlineLabel(doc, node, index = scanIndex(doc)) {
  const links = index.links(node.id);
  const parts = [nodeName(node), ...deviceTypes(node)];

  if (includedFrom(node)) {
    parts.push(`from included topology ${includedFrom(node)}, read only`);
  }

  const parent = node.parentId ? index.node(node.parentId) : null;

  if (parent) {
    parts.push(`in ${nodeName(parent)}`);
  }

  if (node.kind === 'switch') {
    const network = index.network(node.switch?.networkId);

    parts.push(`network ${network ? network.name : 'unassigned'}`);

    if (network?.alias) {
      parts.push(`VLAN alias ${network.alias}`);
    }
  }

  if (node.kind === 'group') {
    const members = index.members(node.id);

    parts.push(count(members, 'member'));
  }

  if (node.kind === 'device' || node.kind === 'switch') {
    parts.push(count(links.length, 'connection'));
  }

  const arrows = node.kind === 'line' ? arrowheads(node.line) : '';

  if (arrows) {
    parts.push(arrows);
  }

  const networks = node.kind === 'device' ? networkNames(index, links) : [];

  if (networks.length) {
    parts.push(`on ${listOf(networks)}`);
  }

  const comment = nodeComment(node);

  if (comment && node.kind !== 'note') {
    parts.push(`comment: ${comment}`);
  }

  return parts.join(', ');
}

// The names of the networks the connections are on, once each, sorted as
// the Networks list sorts them.
function networkNames(index, links) {
  const names = new Set(
    links.map((edge) => index.network(edge.networkId)?.name).filter(Boolean),
  );

  return [...names].sort((a, b) =>
    a.localeCompare(b, undefined, { numeric: true }),
  );
}

// Where a line has arrowheads, which only the canvas shows otherwise.
function arrowheads(line) {
  if (line?.startArrow && line?.endArrow) {
    return 'arrowheads at both ends';
  }

  if (line?.startArrow) {
    return 'arrowhead at its start';
  }

  return line?.endArrow ? 'arrowhead at its end' : '';
}

// Kind and label, without repeating a label that only names the kind ("Group
// Group"). A shape's kind is its figure ("Circle DMZ"). A note adds the start
// of its text: its label stays "Note" unless renamed, so the text is what
// tells notes apart.
function nodeName(node) {
  const kind = node.kind === 'shape' ? kindLabel(node) : capitalize(node.kind);
  const label = nodeLabel(node);
  const text = node.kind === 'note' ? noteSummary(node.note?.text) : '';
  // The whole text: a first line longer than the summary starts it too.
  const redundant =
    !label ||
    label.toLowerCase() === kind.toLowerCase() ||
    (text && noteSummary(node.note?.text, Infinity).startsWith(label));
  const name = redundant ? kind : `${kind} ${label}`;

  return text ? `${name}: ${text}` : name;
}

function noteSummary(text, length = NOTE_SUMMARY_LENGTH) {
  const flat = String(text || '')
    .replace(/\s+/g, ' ')
    .trim();

  return flat.length > length
    ? `${flat.slice(0, length - 1).trimEnd()}…`
    : flat;
}

// Whether a label's words hold all the words of a name, in a row. Only
// whole words count, so "shredder" does not hide "Red Hat".
function holdsWords(words, name) {
  return (
    name.length > 0 &&
    words.some((_, start) =>
      name.every((word, offset) => words[start + offset] === word),
    )
  );
}

// The types the canvas shows no word for: no type at all, and the type of
// a plain device.
const PLAIN_TYPES = ['device', 'virtualmachine'];

// A device's type, as up to two parts of its name. First the word for its
// icon, unless its label already says it: palette templates name their
// devices after their type ("router", "router-2", "external"). Then the
// phenix node type the node shows on the canvas (deviceTypeLabel), unless
// it is that of a plain device or the label or the icon's word already
// says it: a Router with the Linux icon named "edge-1" is "Device edge-1,
// Router".
function deviceTypes(node) {
  if (node.kind !== 'device') {
    return [];
  }

  const key = node.device?.iconKey;
  const icon = Object.hasOwn(DEVICE_TYPES, key || '')
    ? DEVICE_TYPES[key]
    : null;
  const words = labelWords(nodeLabel(node));
  const iconNames = icon ? [icon.name, ...(icon.also || [])] : [];
  const parts = [];

  if (icon && !iconNames.some((name) => holdsWords(words, labelWords(name)))) {
    parts.push(icon.name);
  }

  const type = deviceTypeLabel(node);
  const typeWords = labelWords(type);

  if (
    !PLAIN_TYPES.includes(type.toLowerCase()) &&
    !holdsWords(words, typeWords) &&
    !iconNames.some((name) => holdsWords(labelWords(name), typeWords))
  ) {
    parts.push(type);
  }

  return parts;
}

// A label's words, lower case and without a trailing number: "router-2" and
// "router2" both hold the word "router".
function labelWords(label) {
  return String(label || '')
    .toLowerCase()
    .split(/[^\p{L}\p{N}]+/u)
    .map((word) => word.replace(/(\D)\d+$/u, '$1'))
    .filter(Boolean);
}

/**
 * Network list for the outline, with the switches and devices attached to each.
 *
 * @param {object} doc
 * @returns {object[]}
 */
export function networkOutline(doc) {
  const index = labelIndex(doc);
  // The hostnames of the devices connected on each network.
  const hosts = new Map();

  for (const edge of doc?.edges || []) {
    if (!hosts.has(edge.networkId)) {
      hosts.set(edge.networkId, new Set());
    }

    for (const id of [edge.sourceNodeId, edge.targetNodeId]) {
      const node = index.node(id);

      if (node?.kind === 'device') {
        hosts.get(edge.networkId).add(node.device.hostname);
      }
    }
  }

  return (doc?.networks || [])
    .slice()
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
    .map((network) => {
      const members = hosts.get(network.id) || new Set();
      const alias = network.alias ? `, VLAN alias ${network.alias}` : '';
      const devices = count(members.size, 'device');

      return {
        id: network.id,
        name: network.name,
        alias: network.alias,
        color: network.color,
        description: network.description,
        members: [...members].sort(),
        devices,
        accessibleName: `Network ${network.name}${alias}, ${devices}`,
      };
    });
}

// The kinds the editor header counts, in order, with the documentSummary
// field that counts each and its singular and plural words.
const COUNTED = [
  ['devices', 'device'],
  ['switches', 'switch', 'switches'],
  ['networks', 'network'],
  ['links', 'connection'],
  ['groups', 'group'],
  ['notes', 'note'],
];

// What pressing a count does, as its tooltip and description say it. A
// count of none selects nothing, and its tooltip is the count in words.
function countTip(key, n, text) {
  if (n === 0) {
    return text;
  }

  if (key === 'networks') {
    return `Select the switches of ${n === 1 ? 'the' : 'all'} ${text}`;
  }

  return `Select ${n === 1 ? 'the' : 'all'} ${text}`;
}

/**
 * The diagram's counts, as the editor header shows them: each kind's
 * number, the word after it, both together ("2 devices"), and what pressing
 * the count does ("Select all 2 devices"; see kindSelection in
 * selection.js).
 *
 * @param {object} doc
 * @returns {{key: string, count: number, noun: string, text: string,
 *   tip: string}[]}
 */
export function diagramCounts(doc) {
  const summary = documentSummary(doc);

  return COUNTED.map(([key, noun, plural = `${noun}s`]) => {
    const text = count(summary[key], noun, plural);

    return {
      key,
      count: summary[key],
      noun: summary[key] === 1 ? noun : plural,
      text,
      tip: countTip(key, summary[key], text),
    };
  });
}
