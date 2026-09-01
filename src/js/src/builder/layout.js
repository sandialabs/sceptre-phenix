// Deterministic auto-layout built on dagre.
//
// dagre itself is deterministic when nodes and edges are inserted in a stable
// order, so the layout is applied to a sorted copy of the document. The result
// is snapped to a grid, which keeps positions integral (and diffs small).

import dagre from '@dagrejs/dagre';

import { sizeOf } from './model.js';

export const LAYOUT_DEFAULTS = {
  direction: 'TB',
  nodeSep: 60,
  rankSep: 90,
  grid: 10,
};

function snap(value, grid) {
  return Math.round(value / grid) * grid;
}

/**
 * Computes layout positions without mutating the document.
 *
 * @param {object} doc builder document
 * @param {object} [options] direction, nodeSep, rankSep, grid
 * @returns {Record<string, {x: number, y: number}>} positions by node id
 */
export function computeLayout(doc, options = {}) {
  const config = { ...LAYOUT_DEFAULTS, ...options };
  const graph = new dagre.graphlib.Graph({ multigraph: true });

  graph.setGraph({
    rankdir: config.direction,
    nodesep: config.nodeSep,
    ranksep: config.rankSep,
    marginx: 20,
    marginy: 20,
  });
  graph.setDefaultEdgeLabel(() => ({}));

  const layoutNodes = (doc.nodes || [])
    .filter((node) => node.kind !== 'group')
    .slice()
    .sort((a, b) => a.id.localeCompare(b.id));

  layoutNodes.forEach((node) => {
    const size = sizeOf(node);

    graph.setNode(node.id, { width: size.width, height: size.height });
  });

  const known = new Set(layoutNodes.map((n) => n.id));

  (doc.edges || [])
    .slice()
    .sort((a, b) => a.id.localeCompare(b.id))
    .forEach((edge) => {
      if (known.has(edge.sourceNodeId) && known.has(edge.targetNodeId)) {
        graph.setEdge(edge.sourceNodeId, edge.targetNodeId, {}, edge.id);
      }
    });

  dagre.layout(graph);

  const positions = {};

  layoutNodes.forEach((node) => {
    const laid = graph.node(node.id);

    if (!laid) {
      return;
    }

    const size = sizeOf(node);

    positions[node.id] = {
      x: snap(laid.x - size.width / 2, config.grid),
      y: snap(laid.y - size.height / 2, config.grid),
    };
  });

  return positions;
}

/**
 * Returns a new document with dagre positions applied. Group nodes are resized
 * to contain their members so grouping survives a re-layout.
 *
 * @param {object} doc
 * @param {object} [options]
 * @returns {object} document
 */
export function applyLayout(doc, options = {}) {
  const positions = computeLayout(doc, options);
  const padding = options.padding ?? 40;

  let nodes = (doc.nodes || []).map((node) =>
    positions[node.id] ? { ...node, position: positions[node.id] } : node,
  );

  nodes = nodes.map((node) => {
    if (node.kind !== 'group') {
      return node;
    }

    const members = nodes.filter((child) => child.parentId === node.id);

    if (members.length === 0) {
      return node;
    }

    const minX = Math.min(...members.map((m) => m.position.x));
    const minY = Math.min(...members.map((m) => m.position.y));
    const maxX = Math.max(
      ...members.map((m) => m.position.x + sizeOf(m).width),
    );
    const maxY = Math.max(
      ...members.map((m) => m.position.y + sizeOf(m).height),
    );

    return {
      ...node,
      position: { x: minX - padding, y: minY - padding },
      size: {
        width: maxX - minX + padding * 2,
        height: maxY - minY + padding * 2,
      },
    };
  });

  return { ...doc, nodes };
}
