// The diagram in words: the accessible name of each node on the canvas
// (nodeAccessibleName), each connection by name for Disconnect
// (connectionList), and the counts of the editor header (diagramCounts).
// A node's name names the networks a device is on, instead of the colors of
// its connections on the canvas.

import { count, listOf } from './announce.js';
import { ICON_ORDER, iconLabel } from './iconLabels.js';
import {
  connectionEndLabel,
  deviceTypeLabel,
  documentSummary,
  effectiveLayers,
  findNetwork,
  findNode,
  includedFrom,
  includedReason,
  kindLabel,
  nodeComment,
  nodeLabel,
  someNodeOnLayer,
} from './model.js';
import { capitalize } from './text.js';

// Notes are named by the start of their text, up to this many characters.
const NOTE_SUMMARY_LENGTH = 80;

// Words for the device icon keys, which are the only visible sign of a
// device's type. Each word is the icon's label (iconLabel). A plain device
// is a Linux VM, shown with the server or Linux icon. Those two keys have
// no word, because "Linux" on every node is noise. ALSO_WORDS lists other
// words that name the type in a label.
const PLAIN_ICONS = ['server', 'linux'];
const ALSO_WORDS = {
  blackhat: ['blackhat'],
  desktop: ['workstation'],
  external: ['external'],
  kali: ['kali'],
  ntp: ['ntp'],
  phenix: ['phenix'],
  redhat: ['redhat', 'rhel'],
  smartphone: ['phone'],
};

// The words of a device's icon key: its label, then the other words that
// name it. Empty for no key, a plain device's key and a key that is not
// a built-in icon.
function iconWords(key) {
  if (!ICON_ORDER.includes(key) || PLAIN_ICONS.includes(key)) {
    return [];
  }

  return [iconLabel(key), ...(ALSO_WORDS[key] || [])];
}

/**
 * Every connection, named from its device's end ("alpha (eth0) to EXP"), for
 * the palette's Disconnect. `locked` is why it cannot be removed, or ''.
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
 * The lookups that nodeAccessibleName and handlesFor use, collected once for
 * a whole document so naming every node stays linear.
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
 * Accessible name for a node, as "Device node, 2 connections, on EXP and
 * EXP-2". Pass labelIndex(doc) when naming many nodes.
 */
export function nodeAccessibleName(doc, node, index = scanIndex(doc)) {
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

// The names of the networks the connections are on, once each, sorted by
// name, numbers in numeric order.
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

// Kind and label, without a repeat of a label that only names the kind
// ("Group Group"). A shape's kind is its figure ("Circle DMZ"). A note adds
// the start of its text. Its label stays "Note" unless renamed, so the text
// is what makes notes different.
function nodeName(node) {
  const kind = node.kind === 'shape' ? kindLabel(node) : capitalize(node.kind);
  const label = nodeLabel(node);
  const text = node.kind === 'note' ? noteSummary(node.note?.text) : '';
  // The whole text, because a first line longer than the summary also
  // starts it.
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

// A device's type, as up to two parts of its name: its icon's word and its
// phenix node type, each left out when the label already says it. A Router
// with the Linux icon named "edge-1" is "Device edge-1, Router".
function deviceTypes(node) {
  if (node.kind !== 'device') {
    return [];
  }

  const iconNames = iconWords(node.device?.iconKey);
  const words = labelWords(nodeLabel(node));
  const parts = [];

  if (
    iconNames.length &&
    !iconNames.some((name) => holdsWords(words, labelWords(name)))
  ) {
    parts.push(iconNames[0]);
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

  // The nodes, not the layers, which are not on the canvas. A node may be
  // on a layer that the diagram does not define.
  if (key === 'layers') {
    return 'Select all nodes on a layer';
  }

  return `Select ${n === 1 ? 'the' : 'all'} ${text}`;
}

/**
 * The diagram's counts, as the editor header shows them, with what a press on
 * each does. The layers count comes last, only while a node is at a layer.
 */
export function diagramCounts(doc) {
  const summary = {
    ...documentSummary(doc),
    layers: someNodeOnLayer(doc) ? effectiveLayers(doc).length : 0,
  };
  const counted = summary.layers ? [...COUNTED, ['layers', 'layer']] : COUNTED;

  return counted.map(([key, noun, plural = `${noun}s`]) => {
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
