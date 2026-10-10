// ELK layered with network clusters, over each scope (see common.js). Each
// network is one cluster that holds its switches and the devices that go
// with it, in the preferred order. The clusters are laid out left to right
// along the connections between them. Ports sit where the Builder draws
// handles: a connection leaves its source's right side and enters its
// target's left side. So ELK runs every line left to right.
//
// ELK routes the connections inside a cluster around its nodes, and the
// layout keeps those routes (see layoutScopes). ELK lays the clusters out
// one at a time, so it does not route a connection between clusters. The
// canvas draws that connection itself. ELK also does not place the
// clusters by those connections. So each such connection is also a link
// between the two clusters, which ELK lays out in layers, one column each
// (placeClusters continues from there).
//
// elkjs is large (about 440 KB gzipped), so it loads only when this layout
// first runs. It runs in a Web Worker, off the main thread. The worker
// stays for the next layout until the Builder closes or the session ends
// (stopLayoutEngine). phenix serves the UI's files without caching headers,
// so a new worker downloads its 1.6 MB again.

import { onBuilderSessionEnd } from '../session.js';

import {
  collator,
  LayoutError,
  layoutScopes,
  orderMembers,
  packRanks,
} from './common.js';

const CLUSTER = 'cluster:';
const LINK = 'link:';

// Space between clusters: ELK's between layers, and within one layer.
const GAP_X = 80;
const GAP_Y = 40;
const ASPECT = 1.6;

// Slices of the layers of clusters keep lines short, but leave empty space.
// A slicing may take up to this much more space than the most compact one.
const ROOM = 1.4;
// And may be a quarter wider or narrower than the one nearest ASPECT.
const FIT = Math.log(1.25);
const MAX_SLICES = 16;

// A layout that takes longer than this fails, so Auto layout never stays
// busy.
const TIMEOUT_MS = 60000;

// A cluster laid out on its own (SEPARATE_CHILDREN) takes no options from
// the root. So these options set the preferred order.
const CLUSTER_OPTIONS = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.layered.considerModelOrder.strategy': 'NODES_AND_EDGES',
  'elk.layered.crossingMinimization.forceNodeModelOrder': true,
  'elk.padding': '[top=32,left=24,bottom=24,right=24]',
  'elk.spacing.nodeNode': 24,
  'elk.layered.spacing.nodeNodeBetweenLayers': 64,
  // Lines bend clear of the handles, not beside them.
  'elk.layered.spacing.edgeNodeBetweenLayers': 24,
};

const ROOT_OPTIONS = {
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.hierarchyHandling': 'SEPARATE_CHILDREN',
  'elk.edgeRouting': 'ORTHOGONAL',
  'elk.layered.considerModelOrder.strategy': 'NODES_AND_EDGES',
  'elk.layered.nodePlacement.strategy': 'NETWORK_SIMPLEX',
  // One drawing, even of clusters no link joins, for placeClusters to read
  // the layers from.
  'elk.separateConnectedComponents': false,
  'elk.spacing.nodeNode': GAP_Y,
  'elk.layered.spacing.nodeNodeBetweenLayers': GAP_X,
  'elk.randomSeed': 1,
  // Routes in the root's coordinates, as the corners are summed up.
  'elk.json.edgeCoords': 'ROOT',
};

/**
 * The ELK graph for one scope.
 *
 * @param {object} scope see layoutScopes
 * @returns {object} ELK JSON graph
 */
function elkGraph({ items, edges, networks }) {
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

  const links = edges
    .filter(
      (edge) => byId.get(edge.source).primary !== byId.get(edge.target).primary,
    )
    .map((edge) => ({
      id: `${LINK}${edge.id}`,
      sources: [`${CLUSTER}${byId.get(edge.source).primary}`],
      targets: [`${CLUSTER}${byId.get(edge.target).primary}`],
    }));

  return {
    id: 'root',
    layoutOptions: ROOT_OPTIONS,
    children,
    edges: [
      ...edges.map((edge) => ({
        id: edge.id,
        sources: [edge.sourcePort.id],
        targets: [edge.targetPort.id],
      })),
      ...links,
    ],
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

// Each connection's route, from the graph ELK laid out: the points of its
// one section, source port to target port.
function routesOf(graph) {
  const routes = new Map();

  for (const edge of graph.edges || []) {
    const [section, ...more] = edge.sections || [];

    if (section && !more.length) {
      routes.set(edge.id, [
        section.startPoint,
        ...(section.bendPoints || []),
        section.endPoint,
      ]);
    }
  }

  return routes;
}

// --- placing the clusters ----------------------------------------------------
//
// ELK lays the clusters out in layers, one column each, as tall as the
// largest layer. For a large diagram, that is far taller than wide. So the
// pass cuts the columns across into slices. Each slice's layers wrap into
// more columns where they are tall (packRanks), and the slices go left to
// right. A cluster that a link from a later slice leads to moves to that
// slice. So every line still runs left to right, except a link that ELK
// reversed to break a cycle. More slices keep lines short but leave empty
// space. The pass chooses a slicing in three steps:
// 1. Keep the slicings about as near the aspect ratio as the nearest one.
// 2. Of those, keep the ones that take at most ROOM times the space of the
//    most compact one.
// 3. Of those, take the one with the shortest lines between clusters.

// Each cluster's layer, from where ELK put it. The clusters of one layer
// share a horizontal range, and two layers never do.
function layersOf(clusters) {
  const layers = new Map();
  let layer = -1;
  let right = -Infinity;

  for (const cluster of [...clusters].sort((a, b) => a.x - b.x)) {
    if (cluster.x >= right) {
      layer += 1;
    }
    layers.set(cluster.id, layer);
    right = Math.max(right, cluster.x + cluster.width);
  }

  return layers;
}

const middle = (cluster) => cluster.y + cluster.height / 2;

/**
 * Where each cluster goes.
 *
 * @param {object} graph the ELK graph laid out
 * @param {object} scope see layoutScopes
 * @param {Map<string, {x: number, y: number}>} corners each node's corner,
 *   from ELK
 * @returns {Map<string, {x: number, y: number}>} each cluster's corner
 */
function placeClusters(graph, scope, corners) {
  const clusters = graph.children;
  const clusterById = new Map(clusters.map((cluster) => [cluster.id, cluster]));
  const links = (graph.edges || [])
    .filter((edge) => edge.id.startsWith(LINK))
    .map((edge) => [
      clusterById.get(edge.sources[0]),
      clusterById.get(edge.targets[0]),
    ]);
  const layers = layersOf(clusters);
  const top = Math.min(...clusters.map((cluster) => cluster.y));
  const height =
    Math.max(...clusters.map((cluster) => cluster.y + cluster.height)) - top;

  // Where the columns can be cut across, between clusters, and how many
  // links each cut would cross.
  const gaps = [];
  let bottom = -Infinity;

  for (const cluster of [...clusters].sort((a, b) => a.y - b.y)) {
    if (bottom > -Infinity && cluster.y > bottom) {
      const y = (bottom + cluster.y) / 2;

      gaps.push({
        y,
        crossed: links.filter(
          ([from, to]) => middle(from) < y !== middle(to) < y,
        ).length,
      });
    }
    bottom = Math.max(bottom, cluster.y + cluster.height);
  }

  // The links into each cluster from an earlier layer.
  const into = new Map(clusters.map((cluster) => [cluster.id, []]));

  for (const [from, to] of links) {
    if (layers.get(from.id) < layers.get(to.id)) {
      into.get(to.id).push(from);
    }
  }

  const byLayer = [...clusters].sort(
    (a, b) => layers.get(a.id) - layers.get(b.id) || a.y - b.y,
  );
  const byHeight = [...clusters].sort((a, b) => a.y - b.y);

  // The clusters in `count` slices, each almost as tall as the others, cut
  // where the fewest links cross.
  const slicing = (count) => {
    const cuts = [];

    for (let at = 1; at < count; at += 1) {
      const ideal = top + (at * height) / count;
      const after = gaps.filter((gap) => gap.y > (cuts.at(-1) ?? -Infinity));
      const near = after.filter(
        (gap) => Math.abs(gap.y - ideal) <= height / count / 4,
      );
      const [cut] = (near.length ? near : after).sort(
        (a, b) =>
          (near.length ? a.crossed - b.crossed : 0) ||
          Math.abs(a.y - ideal) - Math.abs(b.y - ideal),
      );

      if (!cut) {
        return null;
      }
      cuts.push(cut.y);
    }

    const slices = new Map();

    for (const cluster of byLayer) {
      let slice = cuts.filter((y) => y < middle(cluster)).length;

      for (const from of into.get(cluster.id)) {
        slice = Math.max(slice, slices.get(from.id));
      }
      slices.set(cluster.id, slice);
    }

    // A rank for each layer of each slice, in ELK's order.
    const ranks = new Map();

    for (const cluster of byHeight) {
      const key =
        slices.get(cluster.id) * clusters.length + layers.get(cluster.id);

      ranks.set(key, [...(ranks.get(key) || []), cluster]);
    }

    return packRanks(
      [...ranks.keys()].sort((a, b) => a - b).map((key) => ranks.get(key)),
      { gapX: GAP_X, gapY: GAP_Y, aspect: ASPECT },
    );
  };

  const home = new Map();

  for (const cluster of clusters) {
    for (const child of cluster.children) {
      home.set(child.id, cluster);
    }
  }

  // A node's corner, with its cluster's corner at `at`.
  const cornerAt = (id, at) => {
    const cluster = home.get(id);
    const corner = corners.get(id);

    return {
      x: corner.x - cluster.x + at.get(cluster.id).x,
      y: corner.y - cluster.y + at.get(cluster.id).y,
    };
  };
  const itemById = new Map(scope.items.map((item) => [item.id, item]));
  // How long the lines between clusters are, with the clusters at `at`.
  const lengthOf = (at) => {
    let length = 0;

    for (const edge of scope.edges) {
      if (home.get(edge.source) !== home.get(edge.target)) {
        const source = cornerAt(edge.source, at);
        const target = cornerAt(edge.target, at);

        length += Math.hypot(
          target.x - source.x - itemById.get(edge.source).width,
          target.y + edge.targetPort.y - source.y - edge.sourcePort.y,
        );
      }
    }

    return length;
  };
  const plans = [];

  for (let count = 1; count <= MAX_SLICES; count += 1) {
    const at = slicing(count);

    if (!at) {
      break;
    }

    let width = 0;
    let depth = 0;

    for (const cluster of clusters) {
      width = Math.max(width, at.get(cluster.id).x + cluster.width);
      depth = Math.max(depth, at.get(cluster.id).y + cluster.height);
    }

    plans.push({
      at,
      room: width * depth,
      // How far from the aspect ratio, as packRanks weighs it.
      off: Math.abs(Math.log(width / Math.max(1, depth) / ASPECT)),
      length: lengthOf(at),
    });

    // More slices take more space. Two slicings in a row that are too large
    // end the search.
    const least = Math.min(...plans.map((plan) => plan.room));

    if (plans.slice(-2).every((plan) => plan.room > least * ROOM)) {
      break;
    }
  }

  // The plans near the best fit to the aspect ratio, then the most compact
  // of those, then the shortest lines.
  const fit = Math.min(...plans.map((plan) => plan.off)) + FIT;
  const near = plans.filter((plan) => plan.off <= fit);
  const least = Math.min(...near.map((plan) => plan.room));
  const [best] = near
    .filter((plan) => plan.room <= least * ROOM)
    .sort((a, b) => a.length - b.length);

  return best.at;
}

// Each node's corner, and the routes inside a cluster, after the clusters
// are placed.
function arranged(graph, scope) {
  const corners = cornersOf(graph);
  const routes = routesOf(graph);
  const at = placeClusters(graph, scope, corners);
  const positions = new Map();
  const moved = new Map();

  for (const cluster of graph.children) {
    const to = at.get(cluster.id);
    const by = { x: to.x - cluster.x, y: to.y - cluster.y };

    for (const { id } of cluster.children) {
      const from = corners.get(id);

      moved.set(id, by);
      positions.set(id, { x: from.x + by.x, y: from.y + by.y });
    }
  }

  // A route between two clusters would be from before they moved.
  const kept = new Map();

  for (const edge of scope.edges) {
    const points = routes.get(edge.id);
    const by = moved.get(edge.source);

    if (points && by === moved.get(edge.target)) {
      kept.set(
        edge.id,
        points.map((point) => ({ x: point.x + by.x, y: point.y + by.y })),
      );
    }
  }

  return { positions, routes: kept };
}

// --- the engine --------------------------------------------------------------

// The engine, as a Promise, while there is one.
let engine = null;

// ELK in a Web Worker: elk-api.js here (2 KB), the layout code in the
// worker, from its own file. A worker that cannot start, or a layout that
// takes too long, makes the layout fail instead of wait.
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

    // Ends the worker, and fails the layouts in progress.
    stop(reason) {
      failed(reason);
      drop();
    },
  };
}

// Node, where the unit tests run, has no Worker: ELK runs in-thread there.
// A browser build does not include that path, so it ships ELK once.
async function testEngine() {
  const { default: ELK } = await import('elkjs/lib/elk.bundled.js');

  return new ELK({ algorithms: ['layered'] });
}

// A browser keeps a module it could not fetch as failed, so only a reload
// fetches elkjs again. The Layered by tier layout (tiers.js) shares the
// engine.
export function elkEngine() {
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
 * in progress fails with an AbortError, which is not a failure to report.
 * The next layout starts a new worker.
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
 *   of the worker. showNotes: see layoutScopes
 * @returns {Promise<{positions: object, sizes: object, routes?: object}>}
 *   see layoutScopes
 */
export async function layout(doc, options = {}) {
  // One engine for every scope. If it stops during the layout, the other
  // scopes fail, and no other worker starts.
  let elk = options.elk;

  return layoutScopes(
    doc,
    async (scope) => {
      elk ||= await elkEngine();

      return arranged(await elk.layout(elkGraph(scope)), scope);
    },
    options,
  );
}
