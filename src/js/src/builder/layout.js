// Auto-layout's document side: laying a document out with the standard
// layout, applying the geometry any layout computes (layouts/index.js runs
// the one the Builder's settings choose), and putting it back.

import {
  LAYOUT_DEFAULTS,
  layout as standardLayout,
} from './layouts/standard.js';

export { LAYOUT_DEFAULTS };

/**
 * Computes the standard layout's positions without mutating the document.
 *
 * @param {object} doc builder document
 * @param {object} [options] direction, nodeSep, rankSep, grid
 * @returns {Record<string, {x: number, y: number}>} positions by node id
 */
export function computeLayout(doc, options = {}) {
  return standardLayout(doc, options).positions;
}

/**
 * Returns a new document with laid-out geometry applied: each node's
 * position, and the size of each group the layout sized around its members.
 * Nodes the layout did not place keep theirs.
 *
 * @param {object} doc
 * @param {{positions: object, sizes?: object}} geometry by node id, as a
 *   layout returns it
 * @returns {object} document
 */
export function withGeometry(doc, { positions, sizes = {} }) {
  const nodes = (doc.nodes || []).map((node) => {
    if (!positions[node.id]) {
      return node;
    }

    const laid = { ...node, position: positions[node.id] };

    if (sizes[node.id]) {
      laid.size = sizes[node.id];
    }

    return laid;
  });

  return { ...doc, nodes };
}

/**
 * Returns a new document with the standard layout's positions applied. A
 * group with members gets the box dagre drew around them, so its members,
 * and only they, are inside it, its own groups included.
 *
 * @param {object} doc
 * @param {object} [options]
 * @returns {object} document
 */
export function applyLayout(doc, options = {}) {
  return withGeometry(doc, standardLayout(doc, options));
}

function sameGeometry(a, b) {
  return (
    a.position?.x === b.position?.x &&
    a.position?.y === b.position?.y &&
    a.size?.width === b.size?.width &&
    a.size?.height === b.size?.height
  );
}

/**
 * The position and size each node had in `before`, for the nodes that
 * `after` moved or resized: all restoreGeometry needs to undo a layout.
 *
 * @param {object} before document
 * @param {object} after the same document, laid out
 * @returns {Record<string, {position: object, size?: object}>} by node id;
 *   empty when nothing moved
 */
export function changedGeometry(before, after) {
  const laid = new Map((after.nodes || []).map((node) => [node.id, node]));
  const geometry = {};

  for (const node of before.nodes || []) {
    const next = laid.get(node.id);

    if (next && !sameGeometry(node, next)) {
      geometry[node.id] = node.size
        ? { position: { ...node.position }, size: { ...node.size } }
        : { position: { ...node.position } };
    }
  }

  return geometry;
}

/**
 * Returns a new document with saved positions and sizes put back. A node
 * saved without a size goes back to its kind's default; nodes the record
 * does not list are left alone.
 *
 * @param {object} doc
 * @param {Record<string, {position: object, size?: object}>} geometry from
 *   changedGeometry
 * @returns {object} document
 */
export function restoreGeometry(doc, geometry) {
  const nodes = (doc.nodes || []).map((node) => {
    const saved = geometry[node.id];

    if (!saved) {
      return node;
    }

    // Copied, so the document never holds the (reactive) saved record.
    const restored = { ...node, position: { ...saved.position } };

    if (saved.size) {
      restored.size = { ...saved.size };
    } else {
      delete restored.size;
    }

    return restored;
  });

  return { ...doc, nodes };
}
