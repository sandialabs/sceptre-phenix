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
import {
  deviceHandles,
  documentSummary,
  findNetwork,
  findNode,
  includedFrom,
  includedReason,
  nodeComment,
  nodeLabel,
} from './model.js';

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
          depth,
          description: nodeComment(node),
          includedFrom: includedFrom(node),
          networkId:
            node.kind === 'switch' ? node.switch?.networkId : undefined,
          accessibleName: outlineLabel(doc, node),
          children:
            node.kind === 'group' ? build(node.id, depth + 1, seen) : [],
        };
      });

  return build('', 0, new Set());
}

function compareNodes(a, b) {
  if (a.kind !== b.kind) {
    return kindRank(a.kind) - kindRank(b.kind);
  }

  return nodeLabel(a).localeCompare(nodeLabel(b), undefined, { numeric: true });
}

function kindRank(kind) {
  return { group: 0, switch: 1, device: 2, note: 3 }[kind] ?? 4;
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
    const label = node ? nodeLabel(node) : id;
    const iface = deviceHandles(node).find(
      (handle) => handle.id === handleId,
    )?.name;

    return { node, name: iface ? `${label} (${iface})` : label };
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
 * Accessible name for a node: kind, label, device type, the topology an
 * included device comes from, group, a switch's network, link or member
 * count and the networks a device is on, as "Device node, 2 connections,
 * on EXP and EXP-2".
 *
 * @param {object} doc
 * @param {object} node
 * @returns {string}
 */
export function outlineLabel(doc, node) {
  const links = (doc?.edges || []).filter(
    (edge) => edge.sourceNodeId === node.id || edge.targetNodeId === node.id,
  );
  const parts = [nodeName(node)];
  const type = deviceType(node);

  if (type) {
    parts.push(type);
  }

  if (includedFrom(node)) {
    parts.push(`from included topology ${includedFrom(node)}, read only`);
  }

  const parent = node.parentId ? findNode(doc, node.parentId) : null;

  if (parent) {
    parts.push(`in ${nodeName(parent)}`);
  }

  if (node.kind === 'switch') {
    const network = findNetwork(doc, node.switch?.networkId);

    parts.push(`network ${network ? network.name : 'unassigned'}`);

    if (network?.alias) {
      parts.push(`VLAN alias ${network.alias}`);
    }
  }

  if (node.kind === 'group') {
    const members = (doc?.nodes || []).filter(
      (entry) => entry.parentId === node.id,
    ).length;

    parts.push(members === 1 ? '1 member' : `${members} members`);
  }

  if (node.kind === 'device' || node.kind === 'switch') {
    parts.push(
      links.length === 1 ? '1 connection' : `${links.length} connections`,
    );
  }

  const networks = node.kind === 'device' ? networkNames(doc, links) : [];

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
function networkNames(doc, links) {
  const names = new Set(
    links.map((edge) => findNetwork(doc, edge.networkId)?.name).filter(Boolean),
  );

  return [...names].sort((a, b) =>
    a.localeCompare(b, undefined, { numeric: true }),
  );
}

// Kind and label, without repeating a label that only names the kind ("Group
// Group"). A note adds the start of its text: its label stays "Note" unless
// renamed, so the text is what tells notes apart.
function nodeName(node) {
  const kind = capitalize(node.kind);
  const label = nodeLabel(node);
  const text = node.kind === 'note' ? noteSummary(node.note?.text) : '';
  // The whole text: a first line longer than the summary starts it too.
  const redundant =
    !label ||
    label.toLowerCase() === node.kind ||
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

// The device's type, unless its label already says it: palette templates
// name their devices after their type ("router", "router-2", "external").
// Only whole words count, so "shredder" does not hide "Red Hat".
function deviceType(node) {
  const key = node.kind === 'device' ? node.device?.iconKey : '';
  const type = Object.hasOwn(DEVICE_TYPES, key || '')
    ? DEVICE_TYPES[key]
    : null;

  if (!type) {
    return '';
  }

  const words = labelWords(nodeLabel(node));
  const name = type.name.toLowerCase().split(' ');
  const named =
    words.some((_, start) =>
      name.every((word, offset) => words[start + offset] === word),
    ) || (type.also || []).some((word) => words.includes(word));

  return named ? '' : type.name;
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

function capitalize(value) {
  const str = String(value || '');
  return str.charAt(0).toUpperCase() + str.slice(1);
}

/**
 * Network list for the outline, with the switches and devices attached to each.
 *
 * @param {object} doc
 * @returns {object[]}
 */
export function networkOutline(doc) {
  return (doc?.networks || [])
    .slice()
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
    .map((network) => {
      const edges = (doc.edges || []).filter(
        (edge) => edge.networkId === network.id,
      );
      const members = new Set();

      edges.forEach((edge) => {
        [edge.sourceNodeId, edge.targetNodeId].forEach((id) => {
          const node = findNode(doc, id);

          if (node?.kind === 'device') {
            members.add(node.device.hostname);
          }
        });
      });

      const alias = network.alias ? `, VLAN alias ${network.alias}` : '';
      const devices =
        members.size === 1 ? '1 device' : `${members.size} devices`;

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

/**
 * The diagram's counts, as the editor header shows them: each kind's
 * number, the word after it and both together ("2 devices").
 *
 * @param {object} doc
 * @returns {{key: string, count: number, noun: string, text: string}[]}
 */
export function diagramCounts(doc) {
  const summary = documentSummary(doc);

  return COUNTED.map(([key, noun, plural = `${noun}s`]) => ({
    key,
    count: summary[key],
    noun: summary[key] === 1 ? noun : plural,
    text: count(summary[key], noun, plural),
  }));
}
