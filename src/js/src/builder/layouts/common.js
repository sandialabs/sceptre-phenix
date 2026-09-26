// What the network layouts (cards.js, dagre.js, elk.js) share: the
// document read as networks, the grouping preference, the groups the user
// drew, and ranks of networks packed into columns (packRanks).
//
// A layout arranges one scope at a time: the diagram's top level, or the
// members of one group. Groups are laid out innermost first, and each is
// then one box in the scope around it, sized after its members; so members
// stay inside their group and nothing else goes in, nested groups too.
//
// The grouping preference: a network's devices together, then devices with
// similar names (the prefix of a hostname: "IT" of "IT-WS-01"), then
// natural name order (IT-WS-2 before IT-WS-10). A device on several
// networks goes with the smallest one: the network it is the gateway of.
//
// Every connection is drawn out of its source's right side and into its
// target's left side (a device interface or a switch bus), so a layout that
// puts each target to the right of its source draws no line backwards.

import { DEFAULT_GRID_SIZE, nodeLabel, sizeOf } from '../model.js';
import { fitRoute, handleOffsetY } from '../routes.js';

// Positions are on the Builder's grid, and every gap a layout leaves is at
// least a grid step, so snapping puts no node on another.
export const GRID = DEFAULT_GRID_SIZE;

// Room between a group's border and its members: groupNodes' 40, on the
// grid.
const GROUP_PADDING = 48;

// Where the diagram's top-left corner goes.
const ORIGIN = { x: 32, y: 32 };

// Nodes that join no network (notes, unconnected devices, groups with no
// connection out of them) go in rows below the rest.
const LOOSE_GAP = 32;
const LOOSE_BELOW = 64;
const LOOSE_MIN_WIDTH = 960;
const LOOSE_ORDER = ['device', 'group', 'switch', 'note'];

// The most packings packRanks tries for the aspect ratio.
const PACKING_STEPS = 400;

/**
 * A layout failure the viewer can act on, such as a layout engine that
 * could not load: its message is shown as it is. Any other error is a fault
 * in a layout, and its text means nothing to the viewer.
 */
export class LayoutError extends Error {
  constructor(message, options) {
    super(message, options);
    this.name = 'LayoutError';
  }
}

// Natural order: IT-WS-2 before IT-WS-10.
export const collator = new Intl.Collator('en', {
  numeric: true,
  sensitivity: 'base',
});

const byId = (a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

/**
 * The part of a name that groups it with similar names: "IT-WS-01" and
 * "it_backup" are "IT", "plc7" is "PLC". A name with no separator and no
 * trailing digits is its own prefix.
 *
 * @param {string} name
 * @returns {string}
 */
export function prefixOf(name) {
  const text = String(name || '').toUpperCase();
  const head = text.split(/[-_.\s]/)[0];

  return head.replace(/\d+$/, '') || head || text;
}

/**
 * @param {number} value
 * @param {number} [grid]
 * @returns {number} value on the grid
 */
export function snap(value, grid = GRID) {
  return Math.round(value / grid) * grid;
}

function snapUp(value, grid = GRID) {
  return Math.ceil(value / grid) * grid;
}

/**
 * Orders the members of one network the preferred way: by name prefix,
 * the largest prefix group first, then naturally by name.
 *
 * @param {object[]} items scope items (see layoutScopes)
 * @returns {object[]} a sorted copy
 */
export function orderMembers(items) {
  const count = new Map();

  for (const item of items) {
    count.set(item.prefix, (count.get(item.prefix) || 0) + 1);
  }

  return [...items].sort(
    (a, b) =>
      count.get(b.prefix) - count.get(a.prefix) ||
      collator.compare(a.prefix, b.prefix) ||
      collator.compare(a.name, b.name) ||
      byId(a, b),
  );
}

// Each node's group, for the nodes in a group the document has. A parent
// that is missing, is not a group or would close a loop is left out, as the
// standard layout does.
function parentsOf(nodes, nodeById) {
  const parents = new Map();

  for (const node of nodes) {
    const parent = nodeById.get(node.parentId);

    if (parent?.kind !== 'group') {
      continue;
    }

    const seen = new Set([node.id]);
    let above = parent;

    while (above && !seen.has(above.id)) {
      seen.add(above.id);
      above = nodeById.get(above.parentId);
    }

    if (!above) {
      parents.set(node.id, parent.id);
    }
  }

  return parents;
}

// The document's connections and networks, as the layouts read them.
function readNetworks(doc, nodeById) {
  const networks = new Map(
    (doc.networks || []).map((network, index) => [
      network.id,
      { id: network.id, name: network.name || '', index },
    ]),
  );
  const edges = [];
  const devices = new Map();

  for (const edge of [...(doc.edges || [])].sort(byId)) {
    const source = nodeById.get(edge.sourceNodeId);
    const target = nodeById.get(edge.targetNodeId);

    if (!source || !target || source === target) {
      continue;
    }

    const bus = [source, target].find((node) => node.kind === 'switch');
    const network = bus?.switch?.networkId ?? edge.networkId;

    edges.push({
      id: edge.id,
      source: source.id,
      target: target.id,
      sourceHandle: edge.sourceHandleId,
      targetHandle: edge.targetHandleId,
      network: networks.has(network) ? network : null,
    });

    for (const node of [source, target]) {
      if (node.kind === 'device' && networks.has(network)) {
        const joined = devices.get(network) || new Set();

        joined.add(node.id);
        devices.set(network, joined);
      }
    }
  }

  // A network's size: how many devices it joins.
  const size = (id) => devices.get(id)?.size || 0;

  return { networks, edges, size };
}

// Places the nodes that join no network in rows, in a stable order.
function placeLoose(items, top, width) {
  const limit = Math.max(width, LOOSE_MIN_WIDTH);
  const rank = (item) => {
    const index = LOOSE_ORDER.indexOf(item.kind);

    return index < 0 ? LOOSE_ORDER.length : index;
  };
  const ordered = [...items].sort(
    (a, b) =>
      rank(a) - rank(b) ||
      collator.compare(a.prefix, b.prefix) ||
      collator.compare(a.name, b.name) ||
      byId(a, b),
  );
  const positions = new Map();
  let x = 0;
  let y = top;
  let row = 0;

  for (const item of ordered) {
    if (x > 0 && x + item.width > limit) {
      x = 0;
      y = snapUp(y + row + LOOSE_GAP);
      row = 0;
    }

    positions.set(item.id, { x, y });
    x = snapUp(x + item.width + LOOSE_GAP);
    row = Math.max(row, item.height);
  }

  return positions;
}

// The box around positioned items.
function extent(items, positions) {
  let right = 0;
  let bottom = 0;

  for (const item of items) {
    const at = positions.get(item.id);

    right = Math.max(right, at.x + item.width);
    bottom = Math.max(bottom, at.y + item.height);
  }

  return { width: right, height: bottom };
}

// Ranks left to right, a rank wrapping into another column past `limit`.
function placeColumns(ranks, limit, { gapX, gapY }) {
  const at = new Map();
  let x = 0;
  let bottom = 0;

  for (const list of ranks) {
    let y = 0;
    let width = 0;

    for (const box of list) {
      if (y > 0 && y + box.height > limit) {
        x += width + gapX;
        y = 0;
        width = 0;
      }
      at.set(box.id, { x, y });
      y += box.height + gapY;
      width = Math.max(width, box.width);
      bottom = Math.max(bottom, y - gapY);
    }
    x += width + gapX;
  }

  return { at, width: x - gapX, height: bottom };
}

/**
 * Places ranks of boxes left to right, each rank top to bottom in its
 * order. A rank taller than a limit wraps into more columns, and the limit
 * is the one that brings the whole closest to `aspect`, width over height.
 *
 * @param {Array<Array<{id: string, width: number, height: number}>>} ranks
 * @param {{gapX: number, gapY: number, aspect: number}} spacing gapX
 *   between columns, gapY between boxes in a column
 * @returns {Map<string, {x: number, y: number}>} each box's top-left
 *   corner, by id
 */
export function packRanks(ranks, spacing) {
  const boxes = ranks.flat();
  const low = Math.max(...boxes.map((box) => box.height));
  const high = boxes.reduce((sum, box) => sum + box.height + spacing.gapY, 0);
  const step = Math.max(20, (high - low) / PACKING_STEPS);
  let best = null;

  for (let limit = low; limit <= high + step; limit += step) {
    const packed = placeColumns(ranks, limit, spacing);
    const score = Math.abs(
      Math.log(packed.width / Math.max(1, packed.height) / spacing.aspect),
    );

    if (!best || score < best.score - 1e-9) {
      best = { ...packed, score };
    }
  }

  return best.at;
}

/**
 * Lays a document out one scope at a time with `arrange`, which places the
 * nodes of one scope that join a network; the rest go in rows below them.
 *
 * `arrange({items, edges, networks})` gets, for one scope:
 * - items: the nodes in the scope that join a network, each {id, kind,
 *   node, width, height, name, prefix, networks (ids, the network's own for
 *   a switch), primary (the network it goes with), multi (on several
 *   networks), roles (network id -> Set of 'source' and 'target': the ends
 *   of its connections it is on)}. A group in the scope is one item, the
 *   size its members need, joining the networks of the connections that
 *   leave it.
 * - edges: the connections between those items, {id, source, target,
 *   network, sourcePort, targetPort}, a port being {id, y}: where the line
 *   meets the item's right (source) or left (target) side. A connection to
 *   a node in a group is one to the group.
 * - networks: network id -> {id, name, index}.
 * It returns each item's top-left corner (a Map, or a Promise of one), in
 * any coordinates: the result is moved to the scope's corner and snapped.
 * A layout that routes the connections too returns {positions, routes}
 * instead: the corners, and each connection's points (a Map by edge id) in
 * the same coordinates, from its source port to its target port. A route
 * is kept for a connection between two nodes of the scope, not one to a
 * group, and moved onto the handles of the nodes as snapped (fitRoute).
 *
 * @param {object} doc builder document
 * @param {function} arrange
 * @returns {Promise<{positions: object, sizes: object, routes?: object}>}
 *   each node's top-left corner, and the size of each group with members,
 *   by node id; and each route kept, by edge id, in absolute coordinates
 */
export async function layoutScopes(doc, arrange) {
  const nodes = [...(doc.nodes || [])].sort(byId);
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const parents = parentsOf(nodes, nodeById);
  const { networks, edges, size } = readNetworks(doc, nodeById);

  // The members of each scope: '' for the top level.
  const members = new Map([['', []]]);

  for (const node of nodes) {
    const scope = parents.get(node.id) || '';

    if (!members.has(scope)) {
      members.set(scope, []);
    }
    members.get(scope).push(node);
  }

  // Each node's connections.
  const edgesOf = new Map();

  for (const edge of edges) {
    for (const id of [edge.source, edge.target]) {
      edgesOf.set(id, [...(edgesOf.get(id) || []), edge]);
    }
  }

  // A node, then its groups, outermost last.
  const chains = new Map();
  const chainOf = (id) => {
    if (!chains.has(id)) {
      const parent = parents.get(id);

      chains.set(id, parent ? [id, ...chainOf(parent)] : [id]);
    }

    return chains.get(id);
  };
  // The node standing for `id` in a scope: itself, or the group of the
  // scope's that holds it; undefined when it is outside the scope.
  const inScope = (id, scope) => {
    const chain = chainOf(id);

    if (!scope) {
      return chain[chain.length - 1];
    }

    const at = chain.indexOf(scope);

    return at > 0 ? chain[at - 1] : undefined;
  };

  // Innermost first: a group after the groups in it, the top level last.
  const order = [];
  const visit = (scope) => {
    for (const node of members.get(scope)) {
      if (members.has(node.id)) {
        visit(node.id);
      }
    }
    order.push(scope);
  };

  visit('');

  const sizes = {};
  // Group id -> each node inside it -> its corner, from the group's.
  const offsets = new Map();
  const primaryOf = (ids) =>
    [...ids].sort(
      (a, b) =>
        size(a) - size(b) ||
        collator.compare(networks.get(a).name, networks.get(b).name) ||
        networks.get(a).index - networks.get(b).index,
    )[0] ?? null;

  // A scope's node, as `arrange` reads it.
  const itemOf = (node) => {
    const box = sizes[node.id] || sizeOf(node);
    const roles = new Map();
    const join = (network, role) => {
      if (network) {
        roles.set(network, (roles.get(network) || new Set()).add(role));
      }
    };

    if (node.kind === 'switch') {
      const network = node.switch?.networkId;

      if (networks.has(network)) {
        roles.set(network, new Set());
      }
    } else if (offsets.has(node.id)) {
      // A group: the connections with one end inside it.
      for (const edge of edges) {
        const from = chainOf(edge.source).includes(node.id);
        const to = chainOf(edge.target).includes(node.id);

        if (from !== to) {
          join(edge.network, from ? 'source' : 'target');
        }
      }
    } else {
      for (const edge of edgesOf.get(node.id) || []) {
        join(edge.network, edge.source === node.id ? 'source' : 'target');
      }
    }

    const name =
      node.kind === 'device'
        ? node.device?.hostname || nodeLabel(node)
        : nodeLabel(node);
    const joined = [...roles.keys()];

    return {
      id: node.id,
      kind: node.kind,
      node,
      width: box.width,
      height: box.height,
      name,
      prefix: prefixOf(name),
      networks: joined,
      primary: node.kind === 'switch' ? joined[0] || null : primaryOf(joined),
      multi: joined.length > 1,
      roles,
    };
  };

  // Where a connection meets the item standing for its end in a scope.
  const portOf = (item, edge, role) => {
    const end = role === 'source' ? edge.source : edge.target;
    const handle = role === 'source' ? edge.sourceHandle : edge.targetHandle;
    const node = nodeById.get(end);

    if (item.id === end) {
      return {
        id: `${item.id}:${node.kind === 'device' ? handle : ''}:${role}`,
        y: handleOffsetY(node, handle),
      };
    }

    // Inside a group, laid out already.
    const inside = offsets.get(item.id).get(end);

    return {
      id: `${item.id}:${edge.id}:${role}`,
      y: Math.min(
        item.height,
        Math.max(0, inside.y + handleOffsetY(node, handle)),
      ),
    };
  };

  let top = new Map();
  // Scope -> each route in it, from the scope's corner.
  const routes = new Map();

  for (const scope of order) {
    const items = members.get(scope).map(itemOf);
    const connected = items.filter((item) => item.primary);
    const placedIds = new Set(connected.map((item) => item.id));
    const itemById = new Map(items.map((item) => [item.id, item]));
    const scoped = [];
    // The connections whose ends are both nodes of the scope.
    const direct = new Set();

    for (const edge of edges) {
      const source = inScope(edge.source, scope);
      const target = inScope(edge.target, scope);

      if (
        source &&
        target &&
        source !== target &&
        placedIds.has(source) &&
        placedIds.has(target)
      ) {
        scoped.push({
          id: edge.id,
          source,
          target,
          network: edge.network,
          sourcePort: portOf(itemById.get(source), edge, 'source'),
          targetPort: portOf(itemById.get(target), edge, 'target'),
        });

        if (source === edge.source && target === edge.target) {
          direct.add(edge.id);
        }
      }
    }

    const result = connected.length
      ? await arrange({ items: connected, edges: scoped, networks })
      : new Map();
    const arranged = result instanceof Map ? result : result.positions;
    const left = Math.min(...connected.map((item) => arranged.get(item.id).x));
    const up = Math.min(...connected.map((item) => arranged.get(item.id).y));
    const local = new Map(
      connected.map((item) => {
        const at = arranged.get(item.id);

        return [item.id, { x: snap(at.x - left), y: snap(at.y - up) }];
      }),
    );
    const scopeRoutes = new Map();

    for (const edge of result instanceof Map ? [] : scoped) {
      const points = result.routes?.get(edge.id);

      if (!points || !direct.has(edge.id)) {
        continue;
      }

      const from = local.get(edge.source);
      const to = local.get(edge.target);
      const route = fitRoute(
        points.map((point) => ({ x: point.x - left, y: point.y - up })),
        {
          x: from.x + itemById.get(edge.source).width,
          y: from.y + edge.sourcePort.y,
        },
        { x: to.x, y: to.y + edge.targetPort.y },
      );

      if (route) {
        scopeRoutes.set(edge.id, route);
      }
    }

    routes.set(scope, scopeRoutes);

    const main = extent(connected, local);
    const loose = items.filter((item) => !placedIds.has(item.id));

    placeLoose(
      loose,
      connected.length ? snapUp(main.height + LOOSE_BELOW) : 0,
      main.width,
    ).forEach((at, id) => local.set(id, at));

    if (!scope) {
      top = local;
      break;
    }

    const box = extent(items, local);
    const inside = new Map();

    sizes[scope] = {
      width: snapUp(box.width + GROUP_PADDING * 2),
      height: snapUp(box.height + GROUP_PADDING * 2),
    };

    for (const item of items) {
      const at = {
        x: GROUP_PADDING + local.get(item.id).x,
        y: GROUP_PADDING + local.get(item.id).y,
      };

      inside.set(item.id, at);
      offsets
        .get(item.id)
        ?.forEach((below, id) =>
          inside.set(id, { x: at.x + below.x, y: at.y + below.y }),
        );
    }

    offsets.set(scope, inside);
  }

  const positions = {};

  top.forEach((at, id) => {
    const corner = { x: ORIGIN.x + at.x, y: ORIGIN.y + at.y };

    positions[id] = corner;
    offsets.get(id)?.forEach((below, inner) => {
      positions[inner] = { x: corner.x + below.x, y: corner.y + below.y };
    });
  });

  const drawn = {};
  const round = (value) => Math.round(value * 100) / 100;

  routes.forEach((list, scope) => {
    const origin = scope
      ? {
          x: positions[scope].x + GROUP_PADDING,
          y: positions[scope].y + GROUP_PADDING,
        }
      : ORIGIN;

    list.forEach((points, id) => {
      drawn[id] = points.map((point) => ({
        x: round(origin.x + point.x),
        y: round(origin.y + point.y),
      }));
    });
  });

  return Object.keys(drawn).length
    ? { positions, sizes, routes: drawn }
    : { positions, sizes };
}
