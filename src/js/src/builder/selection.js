// Pressing a node or connection, on the canvas or in the outline, or one of
// the header's counts.
//
// Both surfaces show the selection as toggle buttons, so a press means the
// same on each, and the live region says the same about it. Counts cover
// nodes and connections alike: Delete and Copy act on both. A count in the
// header selects every item of its kind (kindSelection).

import { count } from './announce.js';
import { connectionEndLabel, findNode, nodeLabel } from './model.js';

/**
 * Name of a node or connection in selection announcements.
 *
 * @param {object} doc
 * @param {{kind: 'nodes'|'edges', id: string}} item
 * @returns {string}
 */
export function selectionItemName(doc, { kind, id }) {
  if (kind === 'nodes') {
    return nodeLabel(findNode(doc, id)) || 'node';
  }

  const edge = (doc?.edges || []).find((entry) => entry.id === id);
  const ends = [
    [edge?.sourceNodeId, edge?.sourceHandleId],
    [edge?.targetNodeId, edge?.targetHandleId],
  ]
    .map(([end, handle]) => connectionEndLabel(findNode(doc, end), handle))
    .filter(Boolean);

  return ends.length === 2
    ? `the connection between ${ends[0]} and ${ends[1]}`
    : 'the connection';
}

/**
 * The selection after a press on `item`, and what to announce.
 *
 * A plain press selects the item alone, or deselects it when it already is
 * the only selected item. "only" says the press also dropped other selected
 * items, which the pressed item's own state does not reveal. An additive
 * press (Shift, Ctrl or Cmd) adds the item to the selection or takes it out.
 *
 * @param {object} doc
 * @param {{nodes: string[], edges: string[]}} before the selection the press
 *   was made on
 * @param {{kind: 'nodes'|'edges', id: string}} item
 * @param {boolean} [additive]
 * @returns {{selection: {nodes: string[], edges: string[]}, message: string}}
 */
export function pressSelection(doc, before, item, additive = false) {
  const name = selectionItemName(doc, item);
  const selected = before[item.kind].includes(item.id);
  const others = before.nodes.length + before.edges.length - (selected ? 1 : 0);

  if (!additive) {
    if (selected && others === 0) {
      return {
        selection: { nodes: [], edges: [] },
        message: `Deselected ${name}`,
      };
    }

    return {
      selection: { nodes: [], edges: [], [item.kind]: [item.id] },
      message: others > 0 ? `Selected ${name} only` : `Selected ${name}`,
    };
  }

  const selection = {
    nodes: [...before.nodes],
    edges: [...before.edges],
    [item.kind]: selected
      ? before[item.kind].filter((id) => id !== item.id)
      : [...before[item.kind], item.id],
  };
  const size = `${count(selection.nodes.length + selection.edges.length, 'item')} selected`;

  return {
    selection,
    message: selected
      ? `Removed ${name} from the selection, ${size}`
      : `Added ${name} to the selection, ${size}`,
  };
}

// The node kind each of the header's counts selects, with its singular and
// plural words. Networks and connections are not nodes (see kindSelection).
const COUNTED_NODES = {
  devices: ['device', 'device'],
  switches: ['switch', 'switch', 'switches'],
  groups: ['group', 'group'],
  notes: ['note', 'note'],
};

/**
 * The selection after a press on one of the header's counts, and what to
 * announce: every item of that kind, replacing the selection. Devices,
 * switches, groups and notes are nodes, included ones too, and connections
 * are every connection. A network is not an object on the canvas: its
 * count selects the switches that show a network of the document, as Go to
 * network does for one.
 *
 * @param {object} doc
 * @param {string} key a diagramCounts key: devices, switches, networks,
 *   links, groups or notes
 * @returns {{selection: {nodes: string[], edges: string[]}|null,
 *   message: string}} selection is null when there is nothing to select,
 *   which leaves the selection as it is; the message then says why
 */
export function kindSelection(doc, key) {
  const nodes = doc?.nodes || [];

  if (key === 'links') {
    const edges = (doc?.edges || []).map((edge) => edge.id);

    return edges.length
      ? {
          selection: { nodes: [], edges },
          message: `Selected ${count(edges.length, 'connection')}.`,
        }
      : { selection: null, message: 'There are no connections to select.' };
  }

  if (key === 'networks') {
    const networks = new Set((doc?.networks || []).map(({ id }) => id));
    const switches = nodes.filter(
      (node) => node.kind === 'switch' && networks.has(node.switch?.networkId),
    );

    if (networks.size === 0) {
      return { selection: null, message: 'There are no networks to select.' };
    }

    if (switches.length === 0) {
      return {
        selection: null,
        message: 'No switch shows a network, so there is nothing to select.',
      };
    }

    const without =
      networks.size -
      new Set(switches.map((node) => node.switch.networkId)).size;

    return {
      selection: { nodes: switches.map((node) => node.id), edges: [] },
      message: `Selected ${count(switches.length, 'switch', 'switches')} of ${count(
        networks.size,
        'network',
      )}.${
        without > 0
          ? ` ${count(without, 'network')} ${without === 1 ? 'has' : 'have'} no switch.`
          : ''
      }`,
    };
  }

  if (!Object.hasOwn(COUNTED_NODES, key)) {
    return { selection: null, message: '' };
  }

  const [kind, noun, plural = `${noun}s`] = COUNTED_NODES[key];
  const ids = nodes.filter((node) => node.kind === kind).map(({ id }) => id);

  return ids.length
    ? {
        selection: { nodes: ids, edges: [] },
        message: `Selected ${count(ids.length, noun, plural)}.`,
      }
    : { selection: null, message: `There are no ${plural} to select.` };
}
