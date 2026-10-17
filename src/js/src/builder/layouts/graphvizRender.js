// Graphviz itself, compiled to WebAssembly (@hpcc-js/wasm-graphviz): what
// the Graphviz layouts' worker runs (graphvizWorker.js), and what the unit
// tests run in-thread. The WebAssembly is inside the package's JavaScript,
// so nothing is fetched from the network.

import { Graphviz } from '@hpcc-js/wasm-graphviz';

// Graphviz, as a Promise, once it loads.
let loaded = null;

/**
 * Loads Graphviz once. A failure is not kept: the next call tries again.
 *
 * @returns {Promise<object>} the Graphviz instance
 */
export function loadGraphviz() {
  if (!loaded) {
    loaded = Graphviz.load().catch((error) => {
      loaded = null;
      throw error;
    });
  }

  return loaded;
}

/**
 * Lays a graph out with one Graphviz engine.
 *
 * @param {object} graphviz the Graphviz instance (loadGraphviz)
 * @param {{dot: string, engine: string}} request the graph in DOT, and
 *   'sfdp' or 'twopi'
 * @returns {Object<string, {x: number, y: number}>} each node's centre, by
 *   name, in points with y down
 * @throws {Error} with Graphviz's words, when the layout fails
 */
export function renderLayout(graphviz, { dot, engine }) {
  // sfdp's first layout of a graph depends on the layout before it, even
  // with a seed (start): the second layout of the same graph does not. So
  // sfdp lays each graph out twice, and the second layout is kept.
  if (engine === 'sfdp') {
    graphviz.layout(dot, 'json0', engine);
  }

  const output = graphviz.layout(dot, 'json0', engine, { yInvert: true });
  const centres = {};

  for (const object of JSON.parse(output).objects || []) {
    const [x, y] = String(object.pos || '')
      .split(',')
      .map(Number);

    if (Number.isFinite(x) && Number.isFinite(y)) {
      centres[object.name] = { x, y };
    }
  }

  return centres;
}
