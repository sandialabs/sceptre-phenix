// Auto-group: puts ungrouped nodes into groups of their own, by network or
// by name. The store then lays the diagram out, so the new groups do not
// overlap (see autoGroup in store.js).
//
// By network, each network's switch goes with the devices on that network,
// and a device on several networks with its smallest one: the rule the
// network layouts cluster by (see common.js), so the layout draws each new
// group where it drew that cluster. By name, devices whose hostnames share
// a family (web-01, web-02) go together; switches stay out. Community
// detection on the connection graph was tried and left out: a network most
// devices join, such as a management network, pulls unrelated networks into
// one group with no name that fits.
//
// Only ungrouped devices and switches are grouped (only the selected ones,
// when a selection is given), existing groups are left alone, and a group
// would need two members.

import { DEFAULT_NETWORK_COLORS, addNode, boundsOf } from './model.js';
import { drawnColor } from './colors.js';
import { uniqueName } from './ids.js';
import { collator, prefixOf } from './layouts/common.js';

export const GROUPING_STRATEGIES = Object.freeze([
  {
    id: 'network',
    label: 'By network',
    summary: 'Each network’s switch with its devices',
  },
  {
    id: 'name',
    label: 'By name',
    summary: 'Devices with names like web-01 and web-02',
  },
]);

const DEFAULT_GROUPING = 'network';

// Room between a new group's border and its members, as groupNodes leaves.
const GROUP_PADDING = 40;

/**
 * @param {string} id
 * @returns {{id: string, label: string, summary: string}|undefined}
 */
function groupingStrategy(id) {
  return GROUPING_STRATEGIES.find((strategy) => strategy.id === id);
}

// The nodes Auto-group may put in a group: devices and switches in no group,
// of the selection when there is one.
function candidatesOf(doc, selection) {
  const selected = new Set(selection || []);

  return (doc.nodes || []).filter(
    (node) =>
      (node.kind === 'device' || node.kind === 'switch') &&
      !node.parentId &&
      (selected.size === 0 || selected.has(node.id)),
  );
}

// Each device's networks, and how many devices each network joins, over the
// whole diagram.
function networksOfDevices(doc) {
  const nodes = new Map((doc.nodes || []).map((node) => [node.id, node]));
  const joined = new Map();
  const size = new Map();

  for (const edge of doc.edges || []) {
    const ends = [nodes.get(edge.sourceNodeId), nodes.get(edge.targetNodeId)];
    const device = ends.find((node) => node?.kind === 'device');
    const network = ends.find((node) => node?.kind === 'switch')?.switch
      ?.networkId;

    if (!device || !network) {
      continue;
    }

    const networks = joined.get(device.id) || new Set();

    if (!networks.has(network)) {
      networks.add(network);
      size.set(network, (size.get(network) || 0) + 1);
    }

    joined.set(device.id, networks);
  }

  return { joined, size };
}

function byNetwork(doc, candidates) {
  const networks = new Map(
    (doc.networks || []).map((network, index) => [
      network.id,
      { ...network, index },
    ]),
  );
  const { joined, size } = networksOfDevices(doc);
  // The smallest network; ties by name, then document order.
  const primaryOf = (ids) =>
    [...ids]
      .filter((id) => networks.has(id))
      .sort(
        (a, b) =>
          size.get(a) - size.get(b) ||
          collator.compare(networks.get(a).name, networks.get(b).name) ||
          networks.get(a).index - networks.get(b).index,
      )[0];
  const members = new Map();

  for (const node of candidates) {
    const network =
      node.kind === 'switch'
        ? node.switch?.networkId
        : primaryOf(joined.get(node.id) || []);

    if (networks.has(network)) {
      members.set(network, [...(members.get(network) || []), node.id]);
    }
  }

  return [...members]
    .sort(([a], [b]) => networks.get(a).index - networks.get(b).index)
    .map(([network, ids]) => {
      const { name, color, index } = networks.get(network);

      // A network with no color is drawn in the palette color of its place
      // (see networkStyle), and its group takes that color too.
      return {
        title: name,
        color:
          drawnColor(color) ||
          DEFAULT_NETWORK_COLORS[index % DEFAULT_NETWORK_COLORS.length],
        members: ids,
      };
    });
}

// A hostname's family as it is written: "web" of "web-01", "IT" of
// "IT-WS-01" (prefixOf, keeping the case).
function familyTitle(hostname) {
  const head = String(hostname || '').split(/[-_.\s]/)[0];

  return head.replace(/\d+$/, '') || head;
}

function byName(doc, candidates) {
  const families = new Map();

  const devices = candidates
    .filter((node) => node.kind === 'device')
    .sort((a, b) =>
      collator.compare(a.device?.hostname || '', b.device?.hostname || ''),
    );

  for (const node of devices) {
    const hostname = node.device?.hostname || '';
    const key = prefixOf(hostname);
    const family = families.get(key) || {
      title: familyTitle(hostname),
      members: [],
    };

    family.members.push(node.id);
    families.set(key, family);
  }

  // The palette color fewest groups use, earliest first.
  const uses = new Map(DEFAULT_NETWORK_COLORS.map((color) => [color, 0]));

  for (const node of doc.nodes || []) {
    const color = node.kind === 'group' ? node.group?.color : '';

    if (uses.has(color)) {
      uses.set(color, uses.get(color) + 1);
    }
  }

  return [...families.values()]
    .filter((family) => family.members.length > 1)
    .sort((a, b) => collator.compare(a.title, b.title))
    .map((family) => {
      const color = [...uses].sort((a, b) => a[1] - b[1])[0][0];

      uses.set(color, uses.get(color) + 1);

      return { ...family, color };
    });
}

const STRATEGIES = { network: byNetwork, name: byName };

/**
 * The groups Auto-group would make, in the diagram's order of networks or
 * in name order: each {title, color, members}, members being node ids.
 * Groups of one are left out.
 *
 * @param {object} doc builder document
 * @param {string} strategy a GROUPING_STRATEGIES id; an unknown one is the
 *   default
 * @param {object} [options] selection: node ids to group; empty or left out
 *   for every ungrouped node
 * @returns {{title: string, color: string, members: string[]}[]}
 */
export function planGroups(doc, strategy, { selection = [] } = {}) {
  const run =
    STRATEGIES[groupingStrategy(strategy) ? strategy : DEFAULT_GROUPING];

  return run(doc, candidatesOf(doc, selection)).filter(
    (group) => group.members.length > 1,
  );
}

/**
 * Makes the planned groups: a group node for each, around its members,
 * named after it (numbered past a group of the same name), and its members
 * put in it. Where the groups go is left to a layout.
 *
 * @param {object} doc
 * @param {{title: string, color: string, members: string[]}[]} planned
 *   from planGroups
 * @returns {{doc: object, groups: object[]}} the document, and the group
 *   nodes made
 */
export function applyGroups(doc, planned) {
  let next = doc;
  const groups = [];
  const titles = (doc.nodes || [])
    .filter((node) => node.kind === 'group')
    .map((node) => node.group?.title || node.label || '');

  for (const plan of planned) {
    const ids = new Set(plan.members);
    const members = next.nodes.filter((node) => ids.has(node.id));
    const bounds = boundsOf(members, GROUP_PADDING);
    const title = uniqueName(plan.title || 'Group', titles, (v) => v, ' ');
    const created = addNode(next, {
      kind: 'group',
      title,
      color: plan.color,
      position: { x: bounds.x, y: bounds.y },
      size: { width: bounds.width, height: bounds.height },
    });

    titles.push(title);
    groups.push(created.node);
    next = {
      ...created.doc,
      nodes: created.doc.nodes.map((node) =>
        ids.has(node.id) ? { ...node, parentId: created.node.id } : node,
      ),
    };
  }

  return { doc: next, groups };
}

/**
 * Why Auto-group has nothing to do, in words.
 *
 * @param {string} strategy
 * @param {object} [options] selected: whether it looked at a selection
 * @returns {string}
 */
export function nothingToGroup(strategy, { selected = false } = {}) {
  const which = selected ? 'selected ' : '';

  return strategy === 'name'
    ? `Nothing to group: no ungrouped ${which}devices have similar names.`
    : `Nothing to group: no ungrouped ${which}nodes share a network.`;
}
