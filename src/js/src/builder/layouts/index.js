// The auto-layout algorithms, each {id, label, summary, description}, in the
// order every list of layouts shows them. A draft stores the last one that
// ran as its `layout`. Without one, a run uses the viewer's default
// (layoutAlgorithm in settings.js). Each module takes a document and returns
// positions, group sizes and, for some, routes.

import { DRAWING_KINDS, sizeOf } from '../model.js';

import { GROUP_PADDING, snapUp } from './common.js';
import { layout as cards } from './cards.js';
import { layout as dagre } from './dagre.js';
import { layout as elk, stopLayoutEngine as stopElk } from './elk.js';
import { layout as standard } from './standard.js';
import { layout as tiers } from './tiers.js';

export { LayoutError } from './common.js';

/**
 * Stops the ELK worker, when the Builder closes or the session ends. A
 * layout in progress fails with an AbortError. The next layout starts a new
 * worker.
 */
export function stopLayoutEngine() {
  stopElk();
}

export const LAYOUT_ALGORITHMS = Object.freeze([
  {
    id: 'cards',
    label: 'Network cards',
    summary: 'A card per network, on a grid',
    description:
      'A card per network, its devices in a column grouped by name with the switch at their head, and the cards on a grid.',
  },
  {
    id: 'elk',
    label: 'ELK layered',
    summary: 'Clusters by network, left to right',
    description:
      'Clusters each network’s switch with its devices, a device on several networks in its smallest one, and runs connections left to right between the clusters.',
  },
  {
    id: 'tiers',
    label: 'By layer',
    summary: 'Diagram layers, top to bottom',
    description:
      'ELK layered from top to bottom: each node at its layer, the highest layer ID at the top. A node without a layer takes the layer of the nearest node that has one. Without any, firewalls and routers go above switches, and switches above other devices.',
  },
  {
    id: 'dagre',
    label: 'Dagre',
    summary: 'Networks in layers, left to right',
    description:
      'Dagre from left to right: each network’s devices in a column beside their switch, and the networks in layers along the connections between them. A tall layer wraps into more columns.',
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
 * The LAYOUT_ALGORITHMS id that laid a document out last, or '' (Default).
 * An id not listed, such as that of a removed layout, gives ''.
 */
export function ownLayout(doc) {
  return layoutAlgorithm(doc?.layout) ? doc.layout : '';
}

/**
 * The layout a run uses on a document: its own, else the viewer's default
 * `fallback`.
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
};

/**
 * Lays a document out with one algorithm, in absolute positions. No algorithm
 * sees shapes, icons and lines: they move with their group, and the group grows
 * around them (growAroundDrawings).
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
 * Grows each group that holds a drawing to the right and down, so the drawing
 * has GROUP_PADDING around it. Innermost groups grow first. The top-left corner
 * never moves, and sizes stay on the grid. Changes `sizes` in place.
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
