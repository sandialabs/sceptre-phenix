// Pressing a node or connection, on the canvas or in the outline.
//
// Both surfaces show the selection as toggle buttons, so a press means the
// same on each, and the live region says the same about it. Counts cover
// nodes and connections alike: Delete and Copy act on both.

import { count } from './announce.js';
import { findNode, nodeLabel } from './model.js';

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
  const ends = [edge?.sourceNodeId, edge?.targetNodeId]
    .map((end) => nodeLabel(findNode(doc, end)))
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
