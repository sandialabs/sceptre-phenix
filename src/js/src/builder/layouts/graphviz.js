// The Graphviz layouts, over each scope (see common.js):
// - Yifan Hu: Graphviz sfdp, Yifan Hu's multilevel spring-electrical
//   model, which places a large diagram fast.
// - Radial: Graphviz twopi, rings around a root by how many connections
//   away each node is (radialRoots chooses the root).
// Graphviz places points, so both end with separateBoxes. Graphviz's own
// overlap removal (prism) needs a triangulation library that this
// WebAssembly build leaves out, and falls back to one that spreads the
// diagram far wider.
//
// Graphviz is large (about 630 KB with Brotli), so it is loaded only when
// one of these layouts first runs, and it runs in a Web Worker, off the
// main thread. As with ELK (elk.js), the worker stays for the next layout
// until the Builder closes or the session ends (stopGraphvizEngine).

import { onBuilderSessionEnd } from '../session.js';

import { collator, LayoutError, layoutScopes, parentsOf } from './common.js';
import { BOX_GAP, seedOf, separateBoxes } from './separate.js';

// Graphviz measures in points, 72 to the inch, and takes node sizes in
// inches: a pixel is a point.
const INCH = 72;

// sfdp without its overlap removal gives a drawing far smaller than the
// boxes: it is scaled up so that the median connection is this long, in
// points, before separateBoxes.
const SFDP_LENGTH = 260;

// The least room between two of twopi's rings, in points.
const RING_GAP = 96;

// A layout that takes longer than this is given up, as with ELK.
const TIMEOUT_MS = 60000;

const byId = (a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

// Each pair of connected items once, whichever way the connection runs.
function linksOf(edges) {
  const seen = new Map();

  for (const edge of edges) {
    const key = [edge.source, edge.target].sort().join('\n');

    if (!seen.has(key)) {
      seen.set(key, [edge.source, edge.target].sort());
    }
  }

  return [...seen.values()].sort(([a, b], [c, d]) =>
    a < c ? -1 : a > c ? 1 : b < d ? -1 : b > d ? 1 : 0,
  );
}

// Each item's neighbours: the items a connection joins it to.
function neighboursOf(items, edges) {
  const neighbours = new Map(items.map((item) => [item.id, new Set()]));

  for (const [a, b] of linksOf(edges)) {
    neighbours.get(a).add(b);
    neighbours.get(b).add(a);
  }

  return neighbours;
}

// The items in parts that no connection joins, each part in id order.
function componentsOf(items, neighbours) {
  const seen = new Set();
  const components = [];

  for (const item of [...items].sort(byId)) {
    if (seen.has(item.id)) {
      continue;
    }

    const part = [];
    const pending = [item.id];

    seen.add(item.id);
    while (pending.length) {
      const id = pending.pop();

      part.push(id);
      for (const next of neighbours.get(id)) {
        if (!seen.has(next)) {
          seen.add(next);
          pending.push(next);
        }
      }
    }
    components.push(part.sort());
  }

  return components;
}

// A router or a firewall: by its built-in icon, or by its node type.
function isGateway(item) {
  const device = item.kind === 'device' ? item.node?.device : null;
  const type = String(device?.spec?.type || '')
    .trim()
    .toLowerCase();

  return (
    ['router', 'firewall'].includes(device?.iconKey) ||
    ['router', 'firewall'].includes(type)
  );
}

/**
 * The Radial layout's root in each part of a scope that no connection
 * joins to another: the selected node, or the group of the scope that
 * holds it; else the router or firewall with the most connections; else
 * the switch with the most devices; else the item with the most
 * connections. A tie goes to the first by name, then by id.
 *
 * @param {object[]} items scope items (see layoutScopes)
 * @param {object[]} edges scope edges
 * @param {Set<string>} [selected] the selected node and the groups that
 *   hold it
 * @returns {string[]} the root of each part, by item id
 */
export function radialRoots(items, edges, selected = new Set()) {
  const itemById = new Map(items.map((item) => [item.id, item]));
  const neighbours = neighboursOf(items, edges);
  const most = (list) =>
    [...list].sort(
      (a, b) =>
        neighbours.get(b.id).size - neighbours.get(a.id).size ||
        collator.compare(a.name, b.name) ||
        byId(a, b),
    )[0];

  return componentsOf(items, neighbours).map((part) => {
    const members = part.map((id) => itemById.get(id));
    const chosen =
      members.find((item) => selected.has(item.id)) ||
      most(members.filter(isGateway)) ||
      most(members.filter((item) => item.kind === 'switch')) ||
      most(members);

    return chosen.id;
  });
}

// How far each of the scope's items is from its part's root, in
// connections.
function depthsOf(items, edges, roots) {
  const neighbours = neighboursOf(items, edges);
  const depths = new Map(roots.map((root) => [root, 0]));
  let ring = [...roots];

  while (ring.length) {
    const next = [];

    for (const id of ring) {
      for (const other of [...neighbours.get(id)].sort()) {
        if (!depths.has(other)) {
          depths.set(other, depths.get(id) + 1);
          next.push(other);
        }
      }
    }
    ring = next;
  }

  return depths;
}

// twopi's ranksep, in inches: the radius of the first ring, then the room
// from each ring to the next. A ring is large enough for its boxes side by
// side, and each ring at least RING_GAP clear of the one inside it.
function ringsOf(items, depths) {
  const along = new Map();
  const across = new Map();

  for (const item of items) {
    const depth = depths.get(item.id);

    along.set(
      depth,
      (along.get(depth) || 0) + (item.width + item.height) / 2 + BOX_GAP,
    );
    across.set(
      depth,
      Math.max(across.get(depth) || 0, Math.hypot(item.width, item.height)),
    );
  }

  const deepest = Math.max(0, ...depths.values());
  const steps = [];
  let radius = 0;

  for (let depth = 1; depth <= deepest; depth += 1) {
    const least =
      radius +
      ((across.get(depth - 1) || 0) + (across.get(depth) || 0)) / 2 +
      RING_GAP;
    const next = Math.max(least, (along.get(depth) || 0) / (2 * Math.PI));

    steps.push(((next - radius) / INCH).toFixed(3));
    radius = next;
  }

  return steps.length ? steps.join(':') : '1';
}

// A graph attribute's value in DOT: a number as it is, text quoted.
function dotValue(value) {
  return typeof value === 'number'
    ? String(value)
    : `"${String(value).replace(/["\\]/g, '\\$&')}"`;
}

/**
 * One scope in DOT: each item a fixed-size box, its size in inches, named
 * by its place in id order, and each connected pair joined once.
 *
 * @param {object[]} items scope items
 * @param {object[]} edges scope edges
 * @param {object} attributes the engine's graph attributes
 * @param {string[]} [roots] item ids marked as twopi roots
 * @returns {{dot: string, names: Map<string, string>}} the graph, and
 *   each item's id by node name
 */
export function graphOf(items, edges, attributes, roots = []) {
  const sorted = [...items].sort(byId);
  const names = new Map(sorted.map((item, index) => [item.id, `n${index}`]));
  const graph = { overlap: 'true', splines: 'false', ...attributes };
  const lines = [
    'strict graph {',
    ...Object.entries(graph).map(
      ([key, value]) => `  ${key}=${dotValue(value)};`,
    ),
    '  node [shape=box, fixedsize=true, label=""];',
    ...sorted.map(
      (item) =>
        `  ${names.get(item.id)} [width=${(item.width / INCH).toFixed(4)}, height=${(item.height / INCH).toFixed(4)}${roots.includes(item.id) ? ', root=true' : ''}];`,
    ),
    ...linksOf(edges).map(([a, b]) => `  ${names.get(a)} -- ${names.get(b)};`),
    '}',
  ];

  return {
    dot: lines.join('\n'),
    names: new Map([...names].map(([id, name]) => [name, id])),
  };
}

// --- the engine --------------------------------------------------------------

const LOAD_FAILED =
  'The layout engine could not be loaded. Reload the page to try again.';

// The engine, as a Promise, while there is one.
let engine = null;

// Graphviz in a Web Worker, from its own file. A worker that cannot start,
// or a layout that takes too long, fails the layout instead of leaving it
// waiting. `dropped` is called when the worker ends. The worker is a module
// worker because its source uses import. The dev server serves it unbundled.
async function workerEngine(dropped) {
  const worker = new Worker(new URL('./graphvizWorker.js', import.meta.url), {
    type: 'module',
  });
  const pending = new Map();
  let next = 0;

  const failAll = (error) => {
    for (const { reject } of pending.values()) {
      reject(error);
    }
    pending.clear();
  };
  const drop = (error) => {
    worker.terminate();
    failAll(error);
    dropped();
  };

  worker.addEventListener('error', (event) => {
    event.preventDefault?.();
    drop(new LayoutError('The layout engine could not start.'));
  });
  worker.addEventListener('message', ({ data }) => {
    const waiting = pending.get(data?.id);

    if (!waiting) {
      return;
    }

    pending.delete(data.id);
    if (data.failed === 'load') {
      drop(new LayoutError(LOAD_FAILED));
      waiting.reject(new LayoutError(LOAD_FAILED));
    } else if (data.failed) {
      waiting.reject(new Error(data.message));
    } else {
      waiting.resolve(data.centres);
    }
  });

  return {
    async render(request) {
      let timer = null;

      next += 1;

      const id = next;
      const answer = new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
      });
      const timeout = new Promise((_, reject) => {
        timer = setTimeout(
          () => reject(new LayoutError('The layout took too long.')),
          TIMEOUT_MS,
        );
      });

      worker.postMessage({ id, ...request });

      try {
        return await Promise.race([answer, timeout]);
      } catch (error) {
        if (error instanceof LayoutError) {
          drop(error);
        }
        throw error;
      } finally {
        clearTimeout(timer);
        pending.delete(id);
      }
    },

    // Ends the worker, and fails the layouts under way.
    stop(reason) {
      drop(reason);
    },
  };
}

// Node, where the unit tests run, has no Worker: Graphviz runs in-thread
// there. A browser build leaves that path out, so it ships Graphviz once.
async function testEngine() {
  const { loadGraphviz, renderLayout } = await import('./graphvizRender.js');
  const graphviz = await loadGraphviz();

  return { render: async (request) => renderLayout(graphviz, request) };
}

function graphvizEngine() {
  if (!engine) {
    const starting = (
      import.meta.env.MODE === 'test'
        ? testEngine()
        : workerEngine(() => {
            if (engine === starting) {
              engine = null;
            }
          })
    ).catch((error) => {
      if (engine === starting) {
        engine = null;
      }
      throw new LayoutError(LOAD_FAILED, { cause: error });
    });

    engine = starting;
  }

  return engine;
}

/**
 * Stops the Graphviz worker, when the Builder closes or the session ends.
 * A layout under way fails with an AbortError, which is no failure to
 * report; the next layout starts a new worker.
 */
export function stopGraphvizEngine() {
  const stopping = engine;

  engine = null;
  stopping
    ?.then((running) =>
      running.stop?.(new DOMException('The layout was stopped.', 'AbortError')),
    )
    .catch(() => {});
}

onBuilderSessionEnd(stopGraphvizEngine);

// --- the layouts -------------------------------------------------------------

// Scales centres about the origin so that the median connection is
// `length` long. Centres with no connection, or all on one point, stay.
function scaleTo(centres, edges, length) {
  const lengths = linksOf(edges)
    .map(([a, b]) =>
      Math.hypot(
        centres.get(a).x - centres.get(b).x,
        centres.get(a).y - centres.get(b).y,
      ),
    )
    .sort((a, b) => a - b);
  const median = lengths[Math.floor(lengths.length / 2)];

  if (!median) {
    return centres;
  }

  return new Map(
    [...centres].map(([id, at]) => [
      id,
      { x: (at.x * length) / median, y: (at.y * length) / median },
    ]),
  );
}

// Lays one scope out with a Graphviz engine, and moves the boxes apart.
async function arrangeWith(
  engineName,
  { items, edges },
  attributes,
  roots,
  options,
) {
  const { dot, names } = graphOf(items, edges, attributes, roots);
  const graphviz = options.graphviz || (await graphvizEngine());
  const laid = await graphviz.render({ dot, engine: engineName });
  const centres = new Map();

  for (const [name, id] of names) {
    if (!laid[name]) {
      throw new Error(`Graphviz placed no node ${name}.`);
    }
    centres.set(id, laid[name]);
  }

  return separateBoxes(
    [...items].sort(byId),
    engineName === 'sfdp' ? scaleTo(centres, edges, SFDP_LENGTH) : centres,
  );
}

/**
 * Yifan Hu: Graphviz sfdp over each scope.
 *
 * @param {object} doc builder document
 * @param {object} [options] showNotes, see layoutScopes; graphviz: an
 *   engine to lay out with ({render(request)}), in place of the worker
 * @returns {Promise<{positions: object, sizes: object}>} see layoutScopes
 */
export function layoutSfdp(doc, options = {}) {
  return layoutScopes(
    doc,
    (scope) =>
      arrangeWith(
        'sfdp',
        scope,
        {
          // Leaves in a circle around the node they hang from.
          beautify: 'true',
          start: seedOf(
            scope.items
              .map((item) => item.id)
              .sort()
              .join('\n'),
          ),
        },
        [],
        options,
      ),
    options,
  );
}

/**
 * Radial: Graphviz twopi over each scope, around the roots radialRoots
 * chooses.
 *
 * @param {object} doc builder document
 * @param {object} [options] root: the id of the selected node; showNotes,
 *   see layoutScopes; graphviz, see layoutSfdp
 * @returns {Promise<{positions: object, sizes: object}>} see layoutScopes
 */
export function layoutRadial(doc, options = {}) {
  // The selected node and the groups that hold it: in a scope, the one of
  // them that is an item stands for it.
  const nodes = doc?.nodes || [];
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const parents = parentsOf(nodes, nodeById);
  const selected = new Set();

  for (
    let id = nodeById.has(options.root) ? options.root : null;
    id && !selected.has(id);
    id = parents.get(id)
  ) {
    selected.add(id);
  }

  return layoutScopes(
    doc,
    (scope) => {
      const roots = radialRoots(scope.items, scope.edges, selected);
      const depths = depthsOf(scope.items, scope.edges, roots);

      return arrangeWith(
        'twopi',
        scope,
        { ranksep: ringsOf(scope.items, depths) },
        roots,
        options,
      );
    },
    options,
  );
}
