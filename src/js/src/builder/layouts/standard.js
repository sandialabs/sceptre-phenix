// The standard auto-layout: the Builder's original, built on dagre, top to
// bottom.
//
// dagre itself is deterministic when nodes and edges are inserted in a stable
// order, so the layout is applied to a sorted copy of the document. The result
// is snapped to a grid, which keeps positions integral (and diffs small). A
// node takes its footprint, its notes block included while notes show (see
// nodeFootprint), as in the other layouts.

import dagre from '@dagrejs/dagre';

import { nodeFootprint } from '../nodeNotes.js';
import { parentsOf, snap } from './common.js';

export const LAYOUT_DEFAULTS = {
  direction: 'TB',
  nodeSep: 60,
  rankSep: 90,
  grid: 10,
};

/**
 * Positions for every node and sizes for the groups with members. Such a
 * group is a dagre compound node: dagre lays its members out together and
 * keeps every other node out of the box it draws around them, its own
 * groups included. A group without members is laid out as a node.
 *
 * @param {object} doc builder document
 * @param {object} [options] direction, nodeSep, rankSep, grid; showNotes,
 *   whether the canvas shows node notes (see nodeFootprint)
 * @returns {{positions: object, sizes: object}} each node's top-left
 *   corner, and the size of each group with members, by node id
 */
export function layout(doc, options = {}) {
  const config = { ...LAYOUT_DEFAULTS, ...options };
  const graph = new dagre.graphlib.Graph({ multigraph: true, compound: true });

  graph.setGraph({
    rankdir: config.direction,
    nodesep: config.nodeSep,
    ranksep: config.rankSep,
    marginx: 20,
    marginy: 20,
  });
  graph.setDefaultEdgeLabel(() => ({}));

  const nodes = (doc.nodes || [])
    .slice()
    .sort((a, b) => a.id.localeCompare(b.id));
  // A group's members are its dagre children.
  const parents = parentsOf(
    nodes,
    new Map(nodes.map((node) => [node.id, node])),
  );
  const clusters = new Set(parents.values());

  nodes.forEach((node) => {
    const size = nodeFootprint(node, config);

    graph.setNode(
      node.id,
      clusters.has(node.id) ? {} : { width: size.width, height: size.height },
    );
  });

  nodes.forEach((node) => {
    if (parents.has(node.id)) {
      graph.setParent(node.id, parents.get(node.id));
    }
  });

  (doc.edges || [])
    .slice()
    .sort((a, b) => a.id.localeCompare(b.id))
    .forEach((edge) => {
      const ends = [edge.sourceNodeId, edge.targetNodeId];

      if (ends.every((id) => graph.hasNode(id) && !clusters.has(id))) {
        graph.setEdge(edge.sourceNodeId, edge.targetNodeId, {}, edge.id);
      }
    });

  dagre.layout(graph);

  const positions = {};
  const sizes = {};
  const grid = config.grid;

  nodes.forEach((node) => {
    const laid = graph.node(node.id);

    if (!laid) {
      return;
    }

    if (!clusters.has(node.id)) {
      const size = nodeFootprint(node, config);

      positions[node.id] = {
        x: snap(laid.x - size.width / 2, grid),
        y: snap(laid.y - size.height / 2, grid),
      };

      return;
    }

    // Snapped outwards: the box dagre left around the members is wider than
    // the grid, so they stay inside it once snapped themselves.
    const left = Math.floor((laid.x - laid.width / 2) / grid) * grid;
    const top = Math.floor((laid.y - laid.height / 2) / grid) * grid;

    positions[node.id] = { x: left, y: top };
    sizes[node.id] = {
      width: Math.ceil((laid.x + laid.width / 2) / grid) * grid - left,
      height: Math.ceil((laid.y + laid.height / 2) / grid) * grid - top,
    };
  });

  return { positions, sizes };
}
