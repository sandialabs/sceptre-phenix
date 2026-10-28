// Presses on canvas nodes, connections and the header's counts, and what the
// live region says. A header count selects every item of its kind.

import { count } from './announce.js';
import {
  connectionEndLabel,
  DRAWING_KINDS,
  effectiveNodeLayer,
  findNode,
  kindLabel,
  nodeLabel,
} from './model.js';

// The name of a node or connection in selection announcements. A shape, icon
// or line also says what it is: "line uplink", "the circle".
export function selectionItemName(doc, { kind, id }) {
  if (kind === 'nodes') {
    const node = findNode(doc, id);

    if (DRAWING_KINDS.includes(node?.kind)) {
      const what = kindLabel(node).toLowerCase();
      const label = node[node.kind]?.label;

      return label ? `${what} ${label}` : `the ${what}`;
    }

    return nodeLabel(node) || 'node';
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

// The selection after a press on `item`, and what to announce. A plain press
// selects the item alone, or deselects it when it is the only one. "only" says
// that the press removed other items. An additive press adds or removes it.
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

/** Whether a node is on a layer, chosen or matched (effectiveNodeLayer). */
export function nodeHasLayer(doc, node) {
  return effectiveNodeLayer(doc, node) !== null;
}

// The node kind each of the header's counts selects, with its singular and
// plural words. Networks and connections are not nodes (see kindSelection).
const COUNTED_NODES = {
  devices: ['device', 'device'],
  switches: ['switch', 'switch', 'switches'],
  groups: ['group', 'group'],
  notes: ['note', 'note'],
};

// The selection after a press on a header count: every item of that kind. A
// network count selects the switches that show a network, and a layer count
// every node on a layer. The selection is null, with the reason, when empty.
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
        message: 'No switch shows a network. There is nothing to select.',
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

  if (key === 'layers') {
    const ids = nodes
      .filter((node) => nodeHasLayer(doc, node))
      .map(({ id }) => id);

    return ids.length
      ? {
          selection: { nodes: ids, edges: [] },
          message: `Selected ${count(ids.length, 'node')} on a layer.`,
        }
      : {
          selection: null,
          message: 'No node is on a layer. There is nothing to select.',
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
