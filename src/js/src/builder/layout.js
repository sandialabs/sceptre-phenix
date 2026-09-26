// Auto-layout's document side: laying a document out with the standard
// layout, applying the geometry any layout computes (layouts/index.js runs
// the draft's own choice or the viewer's default), and putting it back.

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

// A route as a layout gave it, copied as plain points; undefined for one the
// document could not keep (validateRoute in validate.js): fewer than two
// points, or a point off the canvas.
function cleanRoute(points) {
  if (!Array.isArray(points) || points.length < 2) {
    return undefined;
  }

  const route = points.map((point) => ({ x: point?.x, y: point?.y }));

  return route.every(({ x, y }) => Number.isFinite(x) && Number.isFinite(y))
    ? route
    : undefined;
}

// The edge with this route, or without one when route is undefined.
function withRoute(edge, route) {
  if (route) {
    return { ...edge, route };
  }

  if (edge.route === undefined) {
    return edge;
  }

  const kept = { ...edge };

  delete kept.route;

  return kept;
}

/**
 * Returns a new document with laid-out geometry applied: each node's
 * position, the size of each group the layout sized around its members, and
 * each connection's route. Nodes the layout did not place keep theirs; a
 * connection it drew no route for loses the one it had, which the new
 * positions would leave behind.
 *
 * @param {object} doc
 * @param {{positions: object, sizes?: object, routes?: object}} geometry
 *   by node id, and routes by edge id, as a layout returns them
 * @returns {object} document
 */
export function withGeometry(doc, { positions, sizes = {}, routes = {} }) {
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
  const edges = (doc.edges || []).map((edge) =>
    withRoute(edge, cleanRoute(routes?.[edge.id])),
  );

  return { ...doc, nodes, edges };
}

/**
 * Returns a new document that keeps a layout of its own, or none (the
 * viewer's default) for an empty id.
 *
 * @param {object} doc
 * @param {string} [id] a LAYOUT_ALGORITHMS id
 * @returns {object} document
 */
export function withLayoutChoice(doc, id) {
  if (id) {
    return doc.layout === id ? doc : { ...doc, layout: id };
  }

  if (doc.layout === undefined) {
    return doc;
  }

  const next = { ...doc };

  delete next.layout;

  return next;
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
function changedGeometry(before, after) {
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
function restoreGeometry(doc, geometry) {
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

function sameRoute(a, b) {
  return JSON.stringify(a ?? null) === JSON.stringify(b ?? null);
}

/**
 * What a layout changed, as `before` had it: each moved or resized node's
 * geometry (changedGeometry), each connection's route, and the draft's
 * layout choice. All restoreLayoutChanges needs to undo the layout.
 *
 * @param {object} before document
 * @param {object} after the same document, laid out
 * @returns {{geometry: object, routes: object, layout?: {id: string|null}}
 *   |null} routes by edge id, null for an edge that had none; layout only
 *   when the choice changed; null when nothing changed
 */
export function layoutChanges(before, after) {
  const geometry = changedGeometry(before, after);
  const drawn = new Map((after.edges || []).map((edge) => [edge.id, edge]));
  const routes = {};

  for (const edge of before.edges || []) {
    const next = drawn.get(edge.id);

    if (next && !sameRoute(edge.route, next.route)) {
      routes[edge.id] = edge.route
        ? edge.route.map((point) => ({ ...point }))
        : null;
    }
  }

  const changes = { geometry, routes };

  if ((before.layout || '') !== (after.layout || '')) {
    changes.layout = { id: before.layout || null };
  }

  const changed =
    Object.keys(geometry).length > 0 ||
    Object.keys(routes).length > 0 ||
    Boolean(changes.layout);

  return changed ? changes : null;
}

/**
 * Returns a new document with what a layout changed put back: positions and
 * sizes (restoreGeometry), routes, and the layout choice.
 *
 * @param {object} doc
 * @param {{geometry: object, routes?: object, layout?: {id: string|null}}}
 *   changes from layoutChanges
 * @returns {object} document
 */
export function restoreLayoutChanges(doc, { geometry, routes = {}, layout }) {
  const restored = restoreGeometry(doc, geometry);
  const edges = (restored.edges || []).map((edge) =>
    Object.hasOwn(routes, edge.id)
      ? withRoute(edge, cleanRoute(routes[edge.id]))
      : edge,
  );
  const next = { ...restored, edges };

  return layout ? withLayoutChoice(next, layout.id) : next;
}
