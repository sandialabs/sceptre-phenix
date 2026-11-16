// Layered layout with the Eclipse Layout Kernel (elkjs), with the same host
// blocks as the dagre layout. ELK runs in a Web Worker from the same files
// as the Builder's ELK, so a browser downloads elkjs once for both.
import {
  adjacency,
  blockEdges,
  blockGroups,
  leafBlocks,
  orientedEdges,
  placeBlock,
} from '../graph.js';

export const kind = 'static';

// The engine, as a Promise, while there is one.
let engine = null;

// elk-api.js here and the layout code in the worker, from the files the
// Builder loads too. A worker that cannot start makes the layout fail.
async function workerEngine() {
  const [{ default: ELK }, { default: workerUrl }] = await Promise.all([
    import('elkjs/lib/elk-api.js'),
    import('elkjs/lib/elk-worker.min.js?url'),
  ]);
  let worker = null;
  let failed = null;
  const failure = new Promise((_, reject) => {
    failed = reject;
  });
  // Unhandled until a layout waits on it.
  failure.catch(() => {});

  const elk = new ELK({
    workerFactory: () => {
      worker = new Worker(workerUrl);
      worker.addEventListener('error', (event) => {
        event.preventDefault?.();
        failed(new Error('The ELK layout engine could not start.'));
      });
      return worker;
    },
  });
  const created = engine;

  return {
    async layout(graph) {
      try {
        return await Promise.race([elk.layout(graph), failure]);
      } catch (error) {
        worker?.terminate();
        if (engine === created) engine = null;
        throw error;
      }
    },
  };
}

// Node, where the unit tests run, has no Worker: ELK runs in-thread there.
// A browser build does not include that path.
async function testEngine() {
  const { default: ELK } = await import('elkjs/lib/elk.bundled.js');
  return new ELK();
}

// A failed load is tried again on the next layout.
function elkEngine() {
  engine ??= (
    import.meta.env.MODE === 'test' ? testEngine() : workerEngine()
  ).catch((error) => {
    engine = null;
    throw error;
  });
  return engine;
}

export async function compute(graph) {
  const adj = adjacency(graph);
  const blocks = leafBlocks(graph, adj);
  const folded = new Set([...blocks.values()].flatMap((b) => b.leaves));

  const elk = await elkEngine();
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
