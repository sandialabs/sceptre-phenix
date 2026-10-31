// Auto-layout's document side: applying the geometry a layout computes
// (layouts/index.js runs the draft's own choice or the viewer's default),
// and putting it back.

// A route from a layout, copied as plain points. undefined for a route that
// the document cannot keep (validateRoute in validate.js): fewer than two
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
 * A new document with a layout's positions, group sizes and routes. Unplaced
 * nodes keep their position. A connection with no new route loses its old one,
 * because the new positions would leave it behind.
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
 * A new document that records layout `id`, or no layout (the viewer's default)
 * for ''.
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

function sameGeometry(a, b) {
  return (
    a.position?.x === b.position?.x &&
    a.position?.y === b.position?.y &&
    a.size?.width === b.size?.width &&
    a.size?.height === b.size?.height
  );
}

/**
 * The old position and size of each node that `after` moved or resized, for
 * restoreGeometry.
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
 * A new document with saved positions and sizes put back. A node saved without
 * a size takes its kind's default size. Unlisted nodes do not change.
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
 * What a layout changed, as `before` had it: node geometry, routes (null for
 * none) and the layout choice when it changed. Null when nothing changed. This
 * is what restoreLayoutChanges needs to undo the layout.
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
 * A new document with a layout's changes put back: geometry, routes and the
 * layout choice.
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
