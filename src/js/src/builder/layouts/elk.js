// ELK layered with network clusters, over each scope (see common.js): one
// cluster per network, holding its switches and the devices that go with
// it in the preferred order, and the clusters laid out left to right along
// the connections between them. Ports sit where the Builder draws handles:
// a connection leaves its source's right side and enters its target's left
// side, so ELK runs every line left to right.
//
// elkjs is large (about 440 KB gzipped), so it is loaded only when this
// layout first runs, and it runs in a Web Worker, off the main thread. The
// worker stays for the next layout until the Builder closes or the session
// ends (stopLayoutEngine): phenix serves the UI's files without caching
// headers, so a new worker downloads its 1.6 MB again.

import { onBuilderSessionEnd } from '../session.js';

import { collator, LayoutError, layoutScopes, orderMembers } from './common.js';

const CLUSTER = 'cluster:';

// A layout that takes longer than this is given up, so Auto layout never
// stays busy.
const TIMEOUT_MS = 60000;

// Laid out on its own (SEPARATE_CHILDREN), a cluster takes no options from
// the root: the preferred order is forced here.
const CLUSTER_OPTIONS = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.layered.considerModelOrder.strategy': 'NODES_AND_EDGES',
  'elk.layered.crossingMinimization.forceNodeModelOrder': true,
  'elk.padding': '[top=32,left=24,bottom=24,right=24]',
  'elk.spacing.nodeNode': 24,
  'elk.layered.spacing.nodeNodeBetweenLayers': 64,
};

const ROOT_OPTIONS = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.hierarchyHandling': 'SEPARATE_CHILDREN',
  'elk.edgeRouting': 'ORTHOGONAL',
  'elk.layered.considerModelOrder.strategy': 'NODES_AND_EDGES',
  'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF',
  'elk.spacing.nodeNode': 40,
  'elk.layered.spacing.nodeNodeBetweenLayers': 80,
  'elk.spacing.componentComponent': 80,
  'elk.aspectRatio': 1.6,
  'elk.randomSeed': 1,
};

/**
 * The ELK graph for one scope.
 *
 * @param {object} scope see layoutScopes
 * @returns {object} ELK JSON graph
 */
export function elkGraph({ items, edges, networks }) {
  const ports = new Map(items.map((item) => [item.id, new Map()]));
  const byId = new Map(items.map((item) => [item.id, item]));

  for (const edge of edges) {
    const source = byId.get(edge.source);

    ports.get(edge.source).set(edge.sourcePort.id, {
      id: edge.sourcePort.id,
      width: 1,
      height: 1,
      x: source.width,
      y: edge.sourcePort.y,
      layoutOptions: { 'elk.port.side': 'EAST' },
    });
    ports.get(edge.target).set(edge.targetPort.id, {
      id: edge.targetPort.id,
      width: 1,
      height: 1,
      x: 0,
      y: edge.targetPort.y,
      layoutOptions: { 'elk.port.side': 'WEST' },
    });
  }

  const node = (item) => ({
    id: item.id,
    width: item.width,
    height: item.height,
    layoutOptions: { 'elk.portConstraints': 'FIXED_POS' },
    ports: [...ports.get(item.id).values()].sort((a, b) => a.y - b.y),
  });

  // Clusters in the document's order of networks.
  const clusters = new Map();

  for (const item of items) {
    clusters.set(item.primary, [...(clusters.get(item.primary) || []), item]);
  }

  const children = [...clusters.keys()]
    .sort((a, b) => networks.get(a).index - networks.get(b).index)
    .map((network) => {
      const list = clusters.get(network);
      const members = list.filter((item) => item.kind !== 'switch');
      const switches = list
        .filter((item) => item.kind === 'switch')
        .sort((a, b) => collator.compare(a.name, b.name));

      return {
        id: `${CLUSTER}${network}`,
        layoutOptions: CLUSTER_OPTIONS,
        children: [
          ...orderMembers(members.filter((item) => item.multi)),
          ...orderMembers(members.filter((item) => !item.multi)),
          ...switches,
        ].map(node),
      };
    });

  return {
    id: 'root',
    layoutOptions: ROOT_OPTIONS,
    children,
    edges: edges.map((edge) => ({
      id: edge.id,
      sources: [edge.sourcePort.id],
      targets: [edge.targetPort.id],
    })),
  };
}

// Each node's corner, from the graph ELK laid out.
function cornersOf(graph) {
  const positions = new Map();
  const walk = (parent, x, y) => {
    for (const child of parent.children || []) {
      const at = { x: x + child.x, y: y + child.y };

      if (child.id.startsWith(CLUSTER)) {
        walk(child, at.x, at.y);
      } else {
        positions.set(child.id, at);
      }
    }
  };

  walk(graph, 0, 0);

  return positions;
}

// --- the engine --------------------------------------------------------------

// The engine, as a Promise, while there is one.
let engine = null;

// ELK in a Web Worker: elk-api.js here (2 KB), the layout code in the
// worker, from its own file. A worker that cannot start, or a layout that
// takes too long, fails the layout instead of leaving it waiting.
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
  const elk = new ELK({
    algorithms: ['layered'],
    workerFactory: () => {
      worker = new Worker(workerUrl);
      worker.addEventListener('error', (event) => {
        event.preventDefault?.();
        failed(new LayoutError('The layout engine could not start.'));
      });

      return worker;
    },
  });

  // Unhandled until a layout waits on it.
  failure.catch(() => {});

  const created = engine;
  const drop = () => {
    worker?.terminate();
    if (engine === created) {
      engine = null;
    }
  };

  return {
    async layout(graph) {
      let timer = null;
      const timeout = new Promise((_, reject) => {
        timer = setTimeout(
          () => reject(new LayoutError('The layout took too long.')),
          TIMEOUT_MS,
        );
      });

      try {
        return await Promise.race([elk.layout(graph), failure, timeout]);
      } catch (error) {
        drop();
        throw error;
      } finally {
        clearTimeout(timer);
      }
    },

    // Ends the worker, and fails the layouts under way.
    stop(reason) {
      failed(reason);
      drop();
    },
  };
}

// Node, where the unit tests run, has no Worker: ELK runs in-thread there.
// A browser build leaves that path out, so it ships ELK once.
async function testEngine() {
  const { default: ELK } = await import('elkjs/lib/elk.bundled.js');

  return new ELK({ algorithms: ['layered'] });
}

// A browser keeps a module it could not fetch as failed, so only a reload
// fetches elkjs again.
function elkEngine() {
  if (!engine) {
    engine = (
      import.meta.env.MODE === 'test' ? testEngine() : workerEngine()
    ).catch((error) => {
      engine = null;
      throw new LayoutError(
        'The layout engine could not be loaded. Reload the page to try again.',
        { cause: error },
      );
    });
  }

  return engine;
}

/**
 * Stops ELK's worker, when the Builder closes or the session ends. A layout
 * under way fails with an AbortError, which is no failure to report; the
 * next layout starts a new worker.
 */
export function stopLayoutEngine() {
  const stopping = engine;

  engine = null;
  stopping
    ?.then((running) =>
      running.stop?.(new DOMException('The layout was stopped.', 'AbortError')),
    )
    .catch(() => {});
}

onBuilderSessionEnd(stopLayoutEngine);

/**
 * @param {object} doc builder document
 * @param {object} [options] elk: an ELK instance to lay out with, in place
 *   of the worker
 * @returns {Promise<{positions: object, sizes: object}>} see layoutScopes
 */
export async function layout(doc, options = {}) {
  // One engine for every scope: one stopped meanwhile fails the rest,
  // rather than starting another worker.
  let elk = options.elk;

  return layoutScopes(doc, async (scope) => {
    elk ||= await elkEngine();

    return cornersOf(await elk.layout(elkGraph(scope)));
  });
}
