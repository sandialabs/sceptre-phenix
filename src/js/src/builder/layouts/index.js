// The auto-layout algorithms a viewer can choose in the Builder's settings
// (settings.js keeps the choice as layoutAlgorithm). Each is {id, label,
// description}: the id is what is stored, the label names it in the
// Settings dialog, and the description says there what it does.
//
// Each algorithm is a module here with one interface: a document in, and
// each node's position and each group's size out (runLayout).

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
    description:
      'Clusters each network’s switch with its devices, a device on several networks in its smallest one, and runs connections left to right between the clusters.',
  },
  {
    id: 'cards',
    label: 'Network cards',
    description:
      'A card per network, its devices in a column grouped by name with the switch at their head, and the cards on a grid.',
  },
  {
    id: 'dagre',
    label: 'Dagre',
    description:
      'Dagre from left to right: each network’s devices in a column beside their switch, and the networks in layers along the connections between them, a tall layer wrapped into more columns.',
  },
  {
    id: 'standard',
    label: 'Standard',
    description:
      'The Builder’s original Auto layout: dagre from top to bottom, every device in a row above the switches.',
  },
]);

export const DEFAULT_LAYOUT_ALGORITHM = 'elk';

/**
 * @param {string} id
 * @returns {{id: string, label: string, description: string}|undefined}
 */
export function layoutAlgorithm(id) {
  return LAYOUT_ALGORITHMS.find((algorithm) => algorithm.id === id);
}

const LAYOUTS = { elk, cards, dagre, standard };

/**
 * Lays a document out with one of the algorithms. Positions are absolute,
 * as the document keeps them; ELK's arrive later, from a Web Worker.
 *
 * @param {string} id a LAYOUT_ALGORITHMS id; an unknown one runs the default
 * @param {object} doc builder document
 * @param {object} [options] the algorithm's own
 * @returns {Promise<{positions: object, sizes: object}>} each node's
 *   top-left corner, and the size of each group sized around its members,
 *   by node id (see withGeometry in layout.js)
 */
export async function runLayout(id, doc, options = {}) {
  const run = LAYOUTS[layoutAlgorithm(id) ? id : DEFAULT_LAYOUT_ALGORITHM];

  return run(doc, options);
}
