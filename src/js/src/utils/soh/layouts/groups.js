// VLAN-grouped constraint layout with cola.js (webcola): one box per VLAN
// holding the VLAN and the hosts only on it, with routers and multi-homed
// hosts free to sit between the boxes they join. Only the layout engine is
// imported; webcola's d3 adaptor targets an older d3.
import { Layout } from 'webcola/dist/src/layout';

import { adjacency, isVlan } from '../graph.js';

export const kind = 'static';

// a small deterministic generator, so the same graph gets the same picture
function lcg(seed) {
  let s = seed >>> 0;
  return () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s / 4294967296;
  };
}

export function compute(graph) {
  const positions = new Map();
  if (!graph.nodes.length) return { positions, groups: [] };

  const adj = adjacency(graph);
  const idx = new Map(graph.nodes.map((n, i) => [n.id, i]));
  const rnd = lcg(11);
  const nodes = graph.nodes.map(() => ({
    width: 34,
    height: 18,
    x: rnd() * 400,
    y: rnd() * 400,
  }));
  const links = graph.edges.map((e) => ({
    source: idx.get(e.source),
    target: idx.get(e.target),
  }));

  const groups = [];
  const boxes = [];
  for (const n of graph.nodes) {
    if (!isVlan(n)) continue;
    const members = [n.id];
    for (const v of adj.get(n.id)) if (adj.get(v).length === 1) members.push(v);
    groups.push({ leaves: members.map((id) => idx.get(id)), padding: 8 });
    boxes.push({ id: `vlan-${n.id}`, label: n.label, members, padding: 8 });
  }

  new Layout()
    .size([800, 560])
    .nodes(nodes)
    .links(links)
    .groups(groups)
    .linkDistance(32)
    .avoidOverlaps(true)
    .handleDisconnected(true)
    .start(10, 15, 20, 0, false);

  graph.nodes.forEach((n, i) =>
    positions.set(n.id, { x: nodes[i].x, y: nodes[i].y }),
  );
  return { positions, groups: boxes };
}
