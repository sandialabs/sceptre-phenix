// The auto-layout algorithms. A draft keeps the one that laid it out last as
// the document's layout. One without it (imported, uploaded, blank, or placed
// by hand) has the Default layout, and a layout run on it uses the viewer's
// default, which the Builder's settings choose (settings.js keeps it as
// layoutAlgorithm). Each is {id, label, summary, description}: the id is
// what is stored, the label names it, the summary says in a few words what
// it does (the toolbar's layout menu), and the description says it in full
// (the Settings dialog).
//
// Each algorithm is a module here with one interface: a document in, and
// each node's position, each group's size and, for some, each connection's
// route out (runLayout).

import { DRAWING_KINDS, sizeOf } from '../model.js';

import { GROUP_PADDING, snapUp } from './common.js';
import { layout as cards } from './cards.js';
import { layout as dagre } from './dagre.js';
import { layout as elk, stopLayoutEngine as stopElk } from './elk.js';
import { layout as force } from './force.js';
import { layoutRadial, layoutSfdp, stopGraphvizEngine } from './graphviz.js';
import { layout as standard } from './standard.js';
import { layout as tiers } from './tiers.js';

export { LayoutError } from './common.js';

/**
 * Stops the layout engines' workers (ELK's and Graphviz's), when the
 * Builder closes or the session ends. A layout under way fails with an
 * AbortError; the next layout starts a new worker.
 */
export function stopLayoutEngine() {
  stopElk();
  stopGraphvizEngine();
}

export const LAYOUT_ALGORITHMS = Object.freeze([
  {
    id: 'elk',
    label: 'ELK layered',
    summary: 'Clusters by network, left to right',
    description:
      'Clusters each network’s switch with its devices, a device on several networks in its smallest one, and runs connections left to right between the clusters.',
  },
  {
    id: 'tiers',
    label: 'Layered by tier',
    summary: 'Purdue layers, top to bottom',
    description:
      'ELK layered from top to bottom: each node at its Purdue layer, level 5 at the top. A node without a Purdue layer takes the layer of the nearest node that has one. Without any, firewalls and routers go above switches, and switches above other devices.',
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
  {
    id: 'sfdp',
    label: 'Yifan Hu',
    summary: 'Spring model, fast on large diagrams',
    description:
      'Graphviz sfdp, the spring model of Yifan Hu: a connection keeps its nodes near each other, and each node keeps a distance from the others, so busy parts become clusters. It is fast on large diagrams.',
  },
  {
    id: 'force',
    label: 'Force',
    summary: 'Spring model, for small diagrams',
    description:
      'A d3-force spring model: a connection keeps its nodes near each other, and each node keeps a distance from the others. It is best for small diagrams.',
  },
  {
    id: 'radial',
    label: 'Radial',
    summary: 'Rings around one node',
    description:
      'Graphviz twopi: one node at the center, and the other nodes in rings by how many connections away they are. The center is the selected node, else a router or firewall, else the switch with the most devices.',
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
 * The layout a document keeps as its own, the one that laid it out last,
 * when that is one of LAYOUT_ALGORITHMS.
 *
 * @param {object} doc builder document
 * @returns {string} a LAYOUT_ALGORITHMS id, or '' for none (Default)
 */
export function ownLayout(doc) {
  return layoutAlgorithm(doc?.layout) ? doc.layout : '';
}

/**
 * The layout a layout run uses on a document: its own (ownLayout), or else
 * the viewer's default.
 *
 * @param {object} doc builder document
 * @param {string} [fallback] the viewer's default (the layoutAlgorithm
 *   setting); an unknown one is DEFAULT_LAYOUT_ALGORITHM
 * @returns {string} a LAYOUT_ALGORITHMS id
 */
export function documentLayout(doc, fallback = DEFAULT_LAYOUT_ALGORITHM) {
  if (ownLayout(doc)) {
    return doc.layout;
  }

  return layoutAlgorithm(fallback) ? fallback : DEFAULT_LAYOUT_ALGORITHM;
}

const LAYOUTS = {
  elk,
  tiers,
  cards,
  dagre,
  standard,
  sfdp: layoutSfdp,
  force,
  radial: layoutRadial,
};

/**
 * Lays a document out with one of the algorithms. Positions are absolute,
 * as the document keeps them; ELK's arrive later, from a Web Worker.
 *
 * Shapes, icons and lines are drawn where the user put them, so no
 * algorithm sees them: one in no group stays where it is, and one in a
 * group moves as far as its group does, keeping its place in it. A group
 * the algorithm sized around its other members then grows, to the right
 * and down, to hold its drawings with the room it leaves around members
 * (growAroundDrawings).
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
  const nodes = doc?.nodes || [];
  const drawings = nodes.filter((node) => DRAWING_KINDS.includes(node.kind));

  if (drawings.length === 0) {
    return run(doc, options);
  }

  const laid = await run(
    { ...doc, nodes: nodes.filter((node) => !drawings.includes(node)) },
    options,
  );
  const byId = new Map(nodes.map((node) => [node.id, node]));
  const positions = { ...laid.positions };

  for (const drawing of drawings) {
    const group = byId.get(drawing.parentId);
    const moved = group && laid.positions[group.id];

    if (moved) {
      positions[drawing.id] = {
        x: drawing.position.x + moved.x - group.position.x,
        y: drawing.position.y + moved.y - group.position.y,
      };
    }
  }

  const sizes = { ...laid.sizes };

  growAroundDrawings(nodes, byId, positions, sizes);

  return { ...laid, positions, sizes };
}

// How many groups a node is in, through its group and the groups that one
// is in.
function groupDepth(node, byId) {
  const seen = new Set([node.id]);
  let depth = 0;

  for (
    let above = byId.get(node.parentId);
    above && !seen.has(above.id);
    above = byId.get(above.parentId)
  ) {
    seen.add(above.id);
    depth += 1;
  }

  return depth;
}

/**
 * Grows each group that holds a shape, an icon or a line, to the right and
 * down, so the drawing is inside it with GROUP_PADDING to spare, as a
 * layout leaves around a group's members; innermost groups first, so a
 * group that holds a grown group grows around it too. A drawing keeps its
 * place in its group, so the group's top left corner never needs to move.
 * A group's new size is on the grid.
 *
 * @param {object[]} nodes the document's
 * @param {Map<string, object>} byId the document's nodes by id
 * @param {object} positions the layout's, by node id: positions of the
 *   drawings in groups included
 * @param {object} sizes the layout's group sizes, by node id; changed in
 *   place
 */
function growAroundDrawings(nodes, byId, positions, sizes) {
  const at = (node) => positions[node.id] || node.position;
  const boxOf = (node) => sizes[node.id] || sizeOf(node);
  const groups = nodes
    .filter((node) => node.kind === 'group')
    .sort((a, b) => groupDepth(b, byId) - groupDepth(a, byId));
  const grown = new Set();

  for (const group of groups) {
    const members = nodes.filter(
      (node) =>
        node.parentId === group.id &&
        (DRAWING_KINDS.includes(node.kind) || grown.has(node.id)),
    );

    if (members.length === 0) {
      continue;
    }

    const origin = at(group);
    const size = boxOf(group);
    let right = origin.x + size.width;
    let bottom = origin.y + size.height;

    for (const member of members) {
      const corner = at(member);
      const box = boxOf(member);

      right = Math.max(right, corner.x + box.width + GROUP_PADDING);
      bottom = Math.max(bottom, corner.y + box.height + GROUP_PADDING);
    }

    if (right > origin.x + size.width || bottom > origin.y + size.height) {
      sizes[group.id] = {
        width:
          right > origin.x + size.width ? snapUp(right - origin.x) : size.width,
        height:
          bottom > origin.y + size.height
            ? snapUp(bottom - origin.y)
            : size.height,
      };
      grown.add(group.id);
    }
  }
}
