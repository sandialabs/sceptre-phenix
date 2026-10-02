// Dagre, tuned for how the Builder draws connections, over each scope (see
// common.js). Layers run left to right, so a line leaves a device's right
// side straight into its switch's left side, instead of doubling back as it
// does top to bottom (standard.js).
//
// Dagre's own clusters do not keep a network together once layers run
// left to right: it orders the device layer and the switch layer apart. So
// it runs twice:
// 1. Each network alone: its switches, and the devices that go with it (a
//    device on several networks goes with the smallest), in the preferred
//    order. Dagre's crossing sweeps would shuffle devices that share a
//    switch, which cannot cross, so they are off here and the order stays.
// 2. The networks as boxes, joined by the connections between them, so a
//    network comes before the networks its gateways lead to. Dagre ranks
//    them and orders each rank; the ranks are then columns, and a rank
//    taller than a 16:10 diagram wraps into more (packRanks).

import dagre from '@dagrejs/dagre';

import { collator, layoutScopes, orderMembers, packRanks } from './common.js';

const NETWORK = { rankdir: 'LR', nodesep: 16, ranksep: 80 };
const NETWORKS = { rankdir: 'LR', nodesep: 64, ranksep: 96 };
const ASPECT = 1.6;

function graphOf(options, multigraph) {
  const graph = new dagre.graphlib.Graph({ multigraph });

  graph.setGraph({ ...options, marginx: 0, marginy: 0 });
  graph.setDefaultEdgeLabel(() => ({}));

  return graph;
}

// Dagre gives centres.
function corner(graph, id) {
  const laid = graph.node(id);

  return { x: laid.x - laid.width / 2, y: laid.y - laid.height / 2 };
}

// One network's items, laid out alone: corners from the network's own.
function arrangeNetwork(list, edges) {
  const graph = graphOf(NETWORK, true);
  const members = list.filter((item) => item.kind !== 'switch');
  const ids = new Set(list.map((item) => item.id));

  for (const item of [
    ...orderMembers(members.filter((entry) => entry.multi)),
    ...orderMembers(members.filter((entry) => !entry.multi)),
    ...list
      .filter((entry) => entry.kind === 'switch')
      .sort((a, b) => collator.compare(a.name, b.name)),
  ]) {
    graph.setNode(item.id, { width: item.width, height: item.height });
  }

  for (const edge of edges) {
    if (ids.has(edge.source) && ids.has(edge.target)) {
      graph.setEdge(edge.source, edge.target, {}, edge.id);
    }
  }

  dagre.layout(graph, { disableOptimalOrderHeuristic: true });

  const local = new Map(list.map((item) => [item.id, corner(graph, item.id)]));
  const left = Math.min(...[...local.values()].map((at) => at.x));
  const top = Math.min(...[...local.values()].map((at) => at.y));
  let width = 0;
  let height = 0;

  for (const item of list) {
    const at = local.get(item.id);

    at.x -= left;
    at.y -= top;
    width = Math.max(width, at.x + item.width);
    height = Math.max(height, at.y + item.height);
  }

  return { local, width, height };
}

function arrangeDagre({ items, edges, networks }) {
  const clusters = new Map();
  const home = new Map();

  for (const item of items) {
    clusters.set(item.primary, [...(clusters.get(item.primary) || []), item]);
    home.set(item.id, item.primary);
  }

  // Networks in name order, which dagre keeps where crossings allow.
  const order = [...clusters.keys()].sort(
    (a, b) =>
      collator.compare(networks.get(a).name, networks.get(b).name) ||
      networks.get(a).index - networks.get(b).index,
  );
  const laid = new Map(
    order.map((network) => [
      network,
      arrangeNetwork(clusters.get(network), edges),
    ]),
  );
  const graph = graphOf(NETWORKS, false);

  for (const network of order) {
    const { width, height } = laid.get(network);

    graph.setNode(network, { width, height });
  }

  // How many connections run from each network to each other.
  const links = new Map(order.map((network) => [network, new Map()]));

  for (const edge of edges) {
    const from = home.get(edge.source);
    const to = home.get(edge.target);

    if (from !== to) {
      links.get(from).set(to, (links.get(from).get(to) || 0) + 1);
    }
  }

  // One edge per pair of networks, weighing as many connections as join
  // them. Dagre can fail on parallel edges ("Not possible to find
  // intersection inside of the rectangle"), and it makes parallel edges of
  // a link both ways by turning one way around. The edge runs the way most
  // of the connections do; on a tie, from the network first in name order.
  const index = new Map(order.map((network, at) => [network, at]));

  for (const [from, targets] of links) {
    for (const [to, count] of targets) {
      const back = links.get(to).get(from) || 0;

      if (count > back || (count === back && index.get(from) < index.get(to))) {
        graph.setEdge(from, to, { weight: count + back });
      }
    }
  }

  dagre.layout(graph);

  // Dagre's ranks left to right, each in its order top to bottom.
  const ranks = new Map();

  for (const network of order) {
    const { rank } = graph.node(network);

    ranks.set(rank, [...(ranks.get(rank) || []), network]);
  }

  const packed = packRanks(
    [...ranks.keys()]
      .sort((a, b) => a - b)
      .map((rank) =>
        ranks
          .get(rank)
          .sort((a, b) => graph.node(a).order - graph.node(b).order)
          .map((network) => ({
            id: network,
            width: laid.get(network).width,
            height: laid.get(network).height,
          })),
      ),
    { gapX: NETWORKS.ranksep, gapY: NETWORKS.nodesep, aspect: ASPECT },
  );
  const positions = new Map();

  for (const network of order) {
    const origin = packed.get(network);

    laid.get(network).local.forEach((at, id) => {
      positions.set(id, { x: origin.x + at.x, y: origin.y + at.y });
    });
  }

  return positions;
}

/**
 * @param {object} doc builder document
 * @returns {Promise<{positions: object, sizes: object}>} see layoutScopes
 */
export function layout(doc) {
  return layoutScopes(doc, arrangeDagre);
}
