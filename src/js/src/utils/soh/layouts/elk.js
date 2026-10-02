// Layered layout with the Eclipse Layout Kernel (elkjs), with the same host
// blocks as the dagre layout. elk.bundled.js runs ELK on the main thread
// without a web worker; it is large, so it loads only when chosen.
import ELK from 'elkjs/lib/elk.bundled.js';

import {
  adjacency,
  blockEdges,
  blockGroups,
  leafBlocks,
  orientedEdges,
  placeBlock,
} from '../graph.js';

export const kind = 'static';

let elk;

export async function compute(graph) {
  const adj = adjacency(graph);
  const blocks = leafBlocks(graph, adj);
  const folded = new Set([...blocks.values()].flatMap((b) => b.leaves));

  elk ??= new ELK();
  const out = await elk.layout({
    id: 'root',
    layoutOptions: {
      'elk.algorithm': 'layered',
      'elk.direction': 'DOWN',
      'elk.edgeRouting': 'POLYLINE',
      'elk.spacing.nodeNode': '28',
      'elk.layered.spacing.nodeNodeBetweenLayers': '44',
      'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX',
      'elk.separateConnectedComponents': 'true',
    },
    children: graph.nodes
      .filter((n) => !folded.has(n.id))
      .map((n) => {
        const b = blocks.get(n.id);
        return { id: String(n.id), width: b ? b.w : 40, height: b ? b.h : 16 };
      }),
    edges: orientedEdges(graph, adj)
      .filter((e) => !folded.has(e.s) && !folded.has(e.t))
      .map((e) => ({
        id: `e${e.id}`,
        sources: [String(e.s)],
        targets: [String(e.t)],
      })),
  });

  const positions = new Map();
  for (const c of out.children ?? []) {
    const id = Number(c.id);
    const b = blocks.get(id);
    if (b) placeBlock(positions, id, b, c.x + c.width / 2, c.y);
    else positions.set(id, { x: c.x + c.width / 2, y: c.y + c.height / 2 });
  }
  return {
    positions,
    groups: blockGroups(blocks),
    faintEdges: blockEdges(graph, blocks),
  };
}
