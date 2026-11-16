// Layered (Sugiyama) layout with dagre: the network backbone as tiers from the
// most central router down, and the hosts that sit on a single VLAN packed as
// a block under that VLAN so a big VLAN does not make the drawing very wide.
import dagre from '@dagrejs/dagre';

import {
  adjacency,
  blockEdges,
  blockGroups,
  leafBlocks,
  orientedEdges,
  placeBlock,
} from '../graph.js';

export const kind = 'static';

export function compute(graph) {
  const adj = adjacency(graph);
  const blocks = leafBlocks(graph, adj);
  const folded = new Set([...blocks.values()].flatMap((b) => b.leaves));

  const g = new dagre.graphlib.Graph();
  g.setGraph({
    rankdir: 'TB',
    nodesep: 28,
    ranksep: 48,
    ranker: 'network-simplex',
  });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of graph.nodes) {
    if (folded.has(n.id)) continue;
    const b = blocks.get(n.id);
    g.setNode(String(n.id), { width: b ? b.w : 40, height: b ? b.h : 16 });
  }
  for (const e of orientedEdges(graph, adj)) {
    if (folded.has(e.s) || folded.has(e.t)) continue;
    g.setEdge(String(e.s), String(e.t));
  }
  dagre.layout(g);

  const positions = new Map();
  for (const key of g.nodes()) {
    const p = g.node(key);
    const id = Number(key);
    const b = blocks.get(id);
    if (b) placeBlock(positions, id, b, p.x, p.y - p.height / 2);
    else positions.set(id, { x: p.x, y: p.y });
  }
  return {
    positions,
    groups: blockGroups(blocks),
    faintEdges: blockEdges(graph, blocks),
  };
}
