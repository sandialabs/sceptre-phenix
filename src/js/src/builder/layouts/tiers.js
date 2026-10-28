// By layer: ELK layered top to bottom, with each tier (effectiveNodeLayer) an
// ELK partition, highest layer ID at the top. Inside a tier, connections run
// away from the roots by breadth-first distance. Kind only breaks ties, or
// every switch would sit above its devices. The document order and a fixed seed
// make it repeatable. The canvas keeps no ELK route.

import { MAX_LAYER_ID, effectiveNodeLayer, sizeOf } from '../model.js';

import { GROUP_PADDING, parentsOf, snap, snapUp } from './common.js';
import { elkEngine } from './elk.js';

// Where the diagram's top-left corner goes, as for the other layouts.
const ORIGIN = 32;

// The tier of a layer: 0 for the highest layer ID, so that a lower tier is
// nearer the top.
const tierOfLayer = (id) => MAX_LAYER_ID - id;

// The tiers below every layer: connected nodes no layer reaches, then the
// nodes that join no network.
const UNTIERED = MAX_LAYER_ID + 1;
const LOOSE = UNTIERED + 1;

// The order of the kinds of nodes inside a tier (see tierKind), and the
// kinds that are roots when nothing is selected (see distances).
const KIND_RANKS = { firewall: 0, router: 1, switch: 2, host: 3, external: 4 };
const ROOT_KINDS = ['firewall', 'router'];

// The points that keep the tiers in order inside a group (see tierGraph).
const TIER_BREAK = 'tier:';

const GAP = 32;
const GAP_BETWEEN_LAYERS = 64;

const PADDING = `[top=${GROUP_PADDING},left=${GROUP_PADDING},bottom=${GROUP_PADDING},right=${GROUP_PADDING}]`;

// The options of the root graph and of each group's graph: ELK reads them
// from each graph of the hierarchy.
const GRAPH_OPTIONS = {
  'elk.algorithm': 'layered',
  'elk.direction': 'DOWN',
  'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
  'elk.partitioning.activate': true,
  // One drawing, so the tiers align across sets of connected nodes.
  'elk.separateConnectedComponents': false,
  'elk.spacing.nodeNode': GAP,
  'elk.layered.spacing.nodeNodeBetweenLayers': GAP_BETWEEN_LAYERS,
  'elk.randomSeed': 1,
};

/**
 * A device or switch's kind for its order in a tier, by icon, else node type.
 * spec.external or node type HIL is always 'external'.
 */
export function tierKind(node) {
  if (node.kind === 'switch') {
    return 'switch';
  }

  const spec = node.device?.spec || {};
  const type = String(spec.type || '').toLowerCase();
  const icon = node.device?.iconKey;

  if (spec.external === true || type === 'hil' || icon === 'external') {
    return 'external';
  }

  if (icon === 'firewall' || type === 'firewall') {
    return 'firewall';
  }

  if (icon === 'router' || type === 'router') {
    return 'router';
  }

  return 'host';
}

// The connections between devices and switches, and each node's neighbors,
// in the document's order.
function readConnections(doc, nodeById) {
  const neighbors = new Map();
  const edges = [];

  for (const edge of doc.edges || []) {
    const source = nodeById.get(edge.sourceNodeId);
    const target = nodeById.get(edge.targetNodeId);

    if (!source || !target || source === target) {
      continue;
    }

    edges.push({ id: edge.id, a: source, b: target });

    for (const [from, to] of [
      [source, target],
      [target, source],
    ]) {
      if (!neighbors.has(from.id)) {
        neighbors.set(from.id, []);
      }
      neighbors.get(from.id).push(to);
    }
  }

  return { edges, neighbors };
}

// Breadth-first distance from the roots of each set of connected nodes.
function distances(connected, neighbors, selected) {
  const depth = new Map();
  const index = new Map(connected.map((node, at) => [node.id, at]));
  const degree = (node) => neighbors.get(node.id)?.length || 0;

  for (const start of connected) {
    if (depth.has(start.id)) {
      continue;
    }

    // The set of connected nodes `start` is in, in the document's order.
    const found = new Set([start.id]);
    const queue = [start];

    for (let at = 0; at < queue.length; at += 1) {
      for (const next of neighbors.get(queue[at].id) || []) {
        if (!found.has(next.id)) {
          found.add(next.id);
          queue.push(next);
        }
      }
    }

    const members = queue.sort((a, b) => index.get(a.id) - index.get(b.id));
    let roots = members.filter((node) => selected.has(node.id));

    for (const kind of ROOT_KINDS) {
      if (!roots.length) {
        roots = members.filter((node) => tierKind(node) === kind);
      }
    }

    if (!roots.length) {
      const switches = members.filter((node) => node.kind === 'switch');
      const most = Math.max(...switches.map(degree));

      roots = [switches.find((node) => degree(node) === most) || members[0]];
    }

    let frontier = roots;

    roots.forEach((node) => depth.set(node.id, 0));
    for (let level = 1; frontier.length; level += 1) {
      const reached = [];

      for (const node of frontier) {
        for (const next of neighbors.get(node.id) || []) {
          if (!depth.has(next.id)) {
            depth.set(next.id, level);
            reached.push(next);
          }
        }
      }
      frontier = reached;
    }
  }

  return depth;
}

// Each device's and switch's tier: that of its layer (see tierOfLayer),
// else that of the nearest node with one (the higher when two are equally
// near). Else UNTIERED for a connected node, and LOOSE for a node with no
// connection.
function tiersOf(doc, connectable, neighbors) {
  const tier = new Map();
  let frontier = [];

  for (const node of connectable) {
    const layer = effectiveNodeLayer(doc, node);

    if (layer !== null) {
      tier.set(node.id, tierOfLayer(layer));
      frontier.push(node);
    }
  }

  while (frontier.length) {
    const reached = new Map();

    for (const node of frontier) {
      for (const next of neighbors.get(node.id) || []) {
        if (tier.has(next.id)) {
          continue;
        }

        const near = reached.get(next.id);

        reached.set(next.id, {
          node: next,
          tier: Math.min(near?.tier ?? Infinity, tier.get(node.id)),
        });
      }
    }

    reached.forEach((entry, id) => tier.set(id, entry.tier));
    frontier = [...reached.values()].map((entry) => entry.node);
  }

  for (const node of connectable) {
    if (!tier.has(node.id)) {
      tier.set(node.id, neighbors.has(node.id) ? UNTIERED : LOOSE);
    }
  }

  return tier;
}

/**
 * The ELK graph of a document without drawings: each group a compound node, and
 * each connection run from its higher end down, held by the innermost graph
 * with both ends. Selected devices and switches are the roots.
 */
export function tierGraph(doc, options = {}) {
  const nodes = doc.nodes || [];
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const order = new Map(nodes.map((node, at) => [node.id, at]));
  const parents = parentsOf(nodes, nodeById);
  const { edges, neighbors } = readConnections(doc, nodeById);
  const connectable = nodes.filter((node) =>
    ['device', 'switch'].includes(node.kind),
  );
  const tier = tiersOf(doc, connectable, neighbors);
  const depth = distances(
    connectable.filter((node) => neighbors.has(node.id)),
    neighbors,
    new Set(options.selected || []),
  );

  // The members of each group, and of the top level ('').
  const members = new Map([['', []]]);

  for (const node of nodes) {
    const scope = parents.get(node.id) || '';

    if (!members.has(scope)) {
      members.set(scope, []);
    }
    members.get(scope).push(node);
  }

  // A group's tier is the highest of its members', at any depth.
  const tierOf = (node) => {
    if (tier.has(node.id)) {
      return tier.get(node.id);
    }

    const inside = members.get(node.id) || [];

    tier.set(node.id, Math.min(LOOSE, ...inside.map(tierOf)));

    return tier.get(node.id);
  };

  // Which end of a connection is higher: the higher tier, then the nearer
  // a root, then the kind, then the document's order.
  const key = (node) => [
    tierOf(node),
    depth.get(node.id) ?? 0,
    KIND_RANKS[tierKind(node)],
    order.get(node.id),
  ];
  const higher = (a, b) => {
    const [x, y] = [key(a), key(b)];
    const at = x.findIndex((value, index) => value !== y[index]);

    return at < 0 || x[at] < y[at];
  };

  // A node, then its groups, outermost last.
  const chainOf = (id) => {
    const chain = [id];

    for (let up = parents.get(id); up; up = parents.get(up)) {
      chain.push(up);
    }

    return chain;
  };
  const graphEdges = new Map([...members.keys()].map((scope) => [scope, []]));

  for (const { id, a, b } of edges) {
    const [source, target] = higher(a, b) ? [a, b] : [b, a];
    const above = chainOf(source.id);
    const holder =
      chainOf(target.id).find(
        (group) => group !== target.id && above.includes(group),
      ) || '';

    graphEdges.get(members.has(holder) ? holder : '').push({
      id,
      sources: [source.id],
      targets: [target.id],
    });
  }

  // ELK 0.12 reads partitions only in the root graph with INCLUDE_CHILDREN. So
  // in a group, a hidden point between two tiers, with edges from the higher
  // tier's members and to the lower tier's, keeps them in order.
  const tierBreaks = (group, inside) => {
    const byTier = new Map();

    for (const member of inside) {
      const at = tierOf(member);

      byTier.set(at, [...(byTier.get(at) || []), member]);
    }

    const tiers = [...byTier.keys()].sort((a, b) => a - b);
    const children = [];
    const edges = [];

    tiers.slice(1).forEach((at, index) => {
      const id = `${TIER_BREAK}${group}:${index}`;

      children.push({ id, width: 1, height: 1 });
      byTier.get(tiers[index]).forEach((member) => {
        edges.push({
          id: `${id}:${member.id}`,
          sources: [member.id],
          targets: [id],
        });
      });
      byTier.get(at).forEach((member) => {
        edges.push({
          id: `${id}:${member.id}`,
          sources: [id],
          targets: [member.id],
        });
      });
    });

    return { children, edges };
  };

  const elkNode = (node) => {
    const layoutOptions = { 'elk.partitioning.partition': tierOf(node) };

    if (members.has(node.id)) {
      const inside = members.get(node.id);
      const breaks = tierBreaks(node.id, inside);

      return {
        id: node.id,
        layoutOptions: {
          ...GRAPH_OPTIONS,
          ...layoutOptions,
          'elk.padding': PADDING,
        },
        children: [...inside.map(elkNode), ...breaks.children],
        edges: [...graphEdges.get(node.id), ...breaks.edges],
      };
    }

    return { id: node.id, ...sizeOf(node), layoutOptions };
  };

  return {
    id: 'root',
    layoutOptions: {
      ...GRAPH_OPTIONS,
      'elk.padding': '[top=0,left=0,bottom=0,right=0]',
      // The document's order breaks ties. Only the root graph sets it: ELK
      // 0.12 fails on a nested graph that sets it too.
      'elk.layered.considerModelOrder.strategy': 'NODES_AND_EDGES',
    },
    children: members.get('').map(elkNode),
    edges: graphEdges.get(''),
  };
}

// Each node's top-left corner, snapped, from the top-left corner of the
// whole. Also the size of each group with members, on the grid.
function placed(graph) {
  const positions = {};
  const sizes = {};
  const walk = (parent, x, y) => {
    for (const child of parent.children || []) {
      const at = { x: x + (child.x || 0), y: y + (child.y || 0) };

      if (child.id.startsWith(TIER_BREAK)) {
        continue;
      }

      positions[child.id] = at;
      if (child.children?.length) {
        sizes[child.id] = {
          width: snapUp(child.width),
          height: snapUp(child.height),
        };
        walk(child, at.x, at.y);
      }
    }
  };

  walk(graph, 0, 0);

  const corners = Object.values(positions);
  const left = Math.min(...corners.map((at) => at.x));
  const top = Math.min(...corners.map((at) => at.y));

  for (const at of corners) {
    at.x = ORIGIN + snap(at.x - left);
    at.y = ORIGIN + snap(at.y - top);
  }

  return { positions, sizes };
}

/** By layer over the whole document. options.elk replaces the worker. */
export async function layout(doc, options = {}) {
  if (!(doc?.nodes || []).length) {
    return { positions: {}, sizes: {} };
  }

  const elk = options.elk || (await elkEngine());

  return placed(await elk.layout(tierGraph(doc, options)));
}
