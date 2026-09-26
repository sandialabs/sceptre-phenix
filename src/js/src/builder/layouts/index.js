// The auto-layout algorithms. A draft keeps its own choice as the
// document's layout; one without it uses the viewer's default, which the
// Builder's settings choose (settings.js keeps it as layoutAlgorithm). Each
// is {id, label, summary, description}: the id is what is stored, the label
// names it, the summary says in a few words what it does (the toolbar's
// layout menu), and the description says it in full (the Settings dialog).
//
// Each algorithm is a module here with one interface: a document in, and
// each node's position, each group's size and, for some, each connection's
// route out (runLayout).

import { layout as cards } from './cards.js';
import { layout as dagre } from './dagre.js';
import { layout as elk } from './elk.js';
import { layout as standard } from './standard.js';

export { LayoutError } from './common.js';
export { stopLayoutEngine } from './elk.js';

export const LAYOUT_ALGORITHMS = Object.freeze([
  {
    id: 'elk',
    label: 'ELK layered',
    summary: 'Clusters by network, left to right',
    description:
      'Clusters each network’s switch with its devices, a device on several networks in its smallest one, and runs connections left to right between the clusters.',
  },
  {
    id: 'cards',
    label: 'Network cards',
    summary: 'A card per network, on a grid',
    description:
      'A card per network, its devices in a column grouped by name with the switch at their head, and the cards on a grid.',
  },
  {
    id: 'dagre',
    label: 'Dagre',
    summary: 'Networks in layers, left to right',
    description:
      'Dagre from left to right: each network’s devices in a column beside their switch, and the networks in layers along the connections between them, a tall layer wrapped into more columns.',
  },
  {
    id: 'standard',
    label: 'Standard',
    summary: 'Devices above switches, top to bottom',
    description:
      'The Builder’s original Auto layout: dagre from top to bottom, every device in a row above the switches.',
  },
]);

export const DEFAULT_LAYOUT_ALGORITHM = 'elk';

/**
 * @param {string} id
 * @returns {{id: string, label: string, summary: string,
 *   description: string}|undefined}
 */
export function layoutAlgorithm(id) {
  return LAYOUT_ALGORITHMS.find((algorithm) => algorithm.id === id);
}

/**
 * The layout a document is laid out with: its own choice, when that is one
 * of LAYOUT_ALGORITHMS, or else the viewer's default.
 *
 * @param {object} doc builder document
 * @param {string} [fallback] the viewer's default (the layoutAlgorithm
 *   setting); an unknown one is DEFAULT_LAYOUT_ALGORITHM
 * @returns {string} a LAYOUT_ALGORITHMS id
 */
export function documentLayout(doc, fallback = DEFAULT_LAYOUT_ALGORITHM) {
  if (layoutAlgorithm(doc?.layout)) {
    return doc.layout;
  }

  return layoutAlgorithm(fallback) ? fallback : DEFAULT_LAYOUT_ALGORITHM;
}

const LAYOUTS = { elk, cards, dagre, standard };

/**
 * Lays a document out with one of the algorithms. Positions are absolute,
 * as the document keeps them; ELK's arrive later, from a Web Worker.
 *
 * @param {string} id a LAYOUT_ALGORITHMS id; an unknown one runs the default
 * @param {object} doc builder document
 * @param {object} [options] the algorithm's own
 * @returns {Promise<{positions: object, sizes: object, routes?: object}>}
 *   each node's top-left corner, and the size of each group sized around
 *   its members, by node id; and from some, each connection's route, by
 *   edge id (see withGeometry in layout.js)
 */
export async function runLayout(id, doc, options = {}) {
  const run = LAYOUTS[layoutAlgorithm(id) ? id : DEFAULT_LAYOUT_ALGORITHM];

  return run(doc, options);
}
