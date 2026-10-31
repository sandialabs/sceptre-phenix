// The standard auto-layout: the Builder's original, dagre top to bottom. dagre
// is deterministic for a stable insert order, so it lays out a sorted copy.
// Results snap to the grid, which keeps positions integral.

import dagre from '@dagrejs/dagre';

import { sizeOf } from '../model.js';
import { parentsOf, snap } from './common.js';

export const LAYOUT_DEFAULTS = {
  direction: 'TB',
  nodeSep: 60,
  rankSep: 90,
  grid: 10,
};

/**
 * Positions for every node and sizes for groups with members. Such a group is a
 * dagre compound node, which keeps every other node out of its box.
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
    const size = sizeOf(node);

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
      const size = sizeOf(node);

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
