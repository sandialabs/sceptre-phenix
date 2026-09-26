// Connection routes: the path a layout drew for a connection (edge.route),
// in absolute canvas coordinates from the source handle to the target
// handle. The ELK layout draws them; the canvas follows one while its ends
// are still at their handles (NetworkEdge.vue), and the document drops one
// once an end moves (see dropStaleRoutes in model.js).
//
// A route is orthogonal: every stretch of it runs across or down.

import { deviceHandles, sizeOf } from './model.js';

// Coordinates this close are the same.
const SAME = 0.5;

const sameX = (a, b) => Math.abs(a.x - b.x) < SAME;
const sameY = (a, b) => Math.abs(a.y - b.y) < SAME;
const distance = (a, b) => Math.hypot(a.x - b.x, a.y - b.y);

/**
 * Where a connection meets a node's side, from the node's top: at the
 * interface's handle on a device (DeviceNode.vue spaces them evenly), and
 * halfway down anything else.
 *
 * @param {object} node
 * @param {string} [handleId]
 * @returns {number}
 */
export function handleOffsetY(node, handleId) {
  const { height } = sizeOf(node);
  const handles = deviceHandles(node);
  const index = handles.findIndex((handle) => handle.id === handleId);

  if (index < 0) {
    return height / 2;
  }

  return (height * (index + 1)) / (handles.length + 1);
}

/**
 * The route with repeated points and the points in the middle of a straight
 * stretch left out.
 *
 * @param {{x: number, y: number}[]} points
 * @returns {{x: number, y: number}[]} copies
 */
export function simplifyRoute(points) {
  const kept = [];

  for (const { x, y } of points) {
    const point = { x, y };
    const last = kept[kept.length - 1];
    const before = kept[kept.length - 2];

    if (last && sameX(last, point) && sameY(last, point)) {
      continue;
    }

    if (
      before &&
      ((sameX(before, last) && sameX(last, point)) ||
        (sameY(before, last) && sameY(last, point)))
    ) {
      kept[kept.length - 1] = point;
    } else {
      kept.push(point);
    }
  }

  return kept;
}

/**
 * A route moved onto new ends: its first and last points become `start` and
 * `end`, and the bends next to them move with them so every stretch still
 * runs across or down. A straight route whose ends no longer line up gets a
 * step halfway.
 *
 * @param {{x: number, y: number}[]} points
 * @param {{x: number, y: number}} start
 * @param {{x: number, y: number}} end
 * @param {number} [tolerance] how far an end may be from where the route
 *   had it; further, and the route no longer fits
 * @returns {{x: number, y: number}[]|null} the fitted route, or null when
 *   there is none or it does not fit
 */
export function fitRoute(points, start, end, tolerance = Infinity) {
  if (!Array.isArray(points) || points.length < 2) {
    return null;
  }

  if (
    !(distance(points[0], start) <= tolerance) ||
    !(distance(points[points.length - 1], end) <= tolerance)
  ) {
    return null;
  }

  const route = simplifyRoute(points);
  const first = { x: start.x, y: start.y };
  const last = { x: end.x, y: end.y };

  if (route.length < 3) {
    if (sameX(first, last) || sameY(first, last)) {
      return [first, last];
    }

    if (route.length === 2 && sameX(route[0], route[1])) {
      const middle = (first.y + last.y) / 2;

      return [first, { x: first.x, y: middle }, { x: last.x, y: middle }, last];
    }

    const middle = (first.x + last.x) / 2;

    return [first, { x: middle, y: first.y }, { x: middle, y: last.y }, last];
  }

  const fitted = route.map((point) => ({ ...point }));
  const n = fitted.length;

  if (sameY(route[0], route[1])) {
    fitted[1].y = first.y;
  } else {
    fitted[1].x = first.x;
  }

  if (sameY(route[n - 2], route[n - 1])) {
    fitted[n - 2].y = last.y;
  } else {
    fitted[n - 2].x = last.x;
  }

  fitted[0] = first;
  fitted[n - 1] = last;

  return simplifyRoute(fitted);
}

/**
 * The SVG path of a route with rounded bends, and the point halfway along
 * it, where the connection's label goes.
 *
 * @param {{x: number, y: number}[]} points at least two
 * @param {number} [radius] of each bend, less where a stretch is short
 * @returns {{path: string, labelX: number, labelY: number}}
 */
export function routePath(points, radius = 8) {
  const at = (point) => `${point.x} ${point.y}`;
  let path = `M${at(points[0])}`;

  for (let i = 1; i < points.length - 1; i += 1) {
    const [a, b, c] = [points[i - 1], points[i], points[i + 1]];
    const r = Math.min(radius, distance(a, b) / 2, distance(b, c) / 2);
    const towards = (to) => {
      const length = distance(b, to) || 1;

      return {
        x: b.x + ((to.x - b.x) * r) / length,
        y: b.y + ((to.y - b.y) * r) / length,
      };
    };

    path += `L${at(towards(a))}Q${at(b)} ${at(towards(c))}`;
  }

  path += `L${at(points[points.length - 1])}`;

  const lengths = points
    .slice(1)
    .map((point, index) => distance(points[index], point));
  let half = lengths.reduce((sum, length) => sum + length, 0) / 2;

  for (let i = 0; i < lengths.length; i += 1) {
    if (half <= lengths[i] || i === lengths.length - 1) {
      const share = lengths[i] ? Math.min(1, half / lengths[i]) : 0;
      const [a, b] = [points[i], points[i + 1]];

      return {
        path,
        labelX: a.x + (b.x - a.x) * share,
        labelY: a.y + (b.y - a.y) * share,
      };
    }

    half -= lengths[i];
  }

  return { path, labelX: points[0].x, labelY: points[0].y };
}
