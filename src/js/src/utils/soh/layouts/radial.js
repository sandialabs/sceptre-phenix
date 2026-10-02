// Radial tree: a breadth-first spanning tree from the graph center (the most
// central router), laid out with d3.tree and bent onto rings, so each ring is
// one hop further from the core. Extra links of multi-homed hosts are drawn
// as chords.
import { stratify, tree } from 'd3';

import { adjacency, bfs, chooseRoot } from '../graph.js';

export const kind = 'static';

const RING = 110; // distance between rings

export function compute(graph) {
  const positions = new Map();
  if (!graph.nodes.length) return { positions };

  const adj = adjacency(graph);
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const root = chooseRoot(graph, adj);
  const { parent } = bfs(adj, root);

  // components the root does not reach hang off a virtual root
  const rows = [{ id: '__root', parent: null }];
  for (const n of graph.nodes) {
    const p = parent.get(n.id);
    rows.push({
      id: String(n.id),
      parent: p === undefined || p === null ? '__root' : String(p),
    });
  }
  const h = stratify()
    .id((d) => d.id)
    .parentId((d) => d.parent)(rows);

  // leaves before subtrees so hosts spread evenly, then by label
  h.sort(
    (a, b) =>
      a.height - b.height ||
      String(byId.get(Number(a.id))?.label ?? '').localeCompare(
        String(byId.get(Number(b.id))?.label ?? ''),
      ),
  );

  const depth = Math.max(1, h.height - 1);
  const radius = RING * depth;
  tree()
    .size([2 * Math.PI, radius])
    .separation(
      (a, b) => (a.parent === b.parent ? 1 : 2) / Math.max(1, a.depth),
    )(h);

  // the virtual root takes one ring: pull everything in so the real root
  // sits at the center
  const shift = radius / Math.max(1, h.height);
  for (const d of h.descendants()) {
    if (d.id === '__root') continue;
    const r = Math.max(0, d.y - shift);
    const a = d.x - Math.PI / 2;
    positions.set(Number(d.id), { x: r * Math.cos(a), y: r * Math.sin(a) });
  }
  return { positions };
}
