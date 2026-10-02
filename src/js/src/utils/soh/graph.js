// Helpers shared by the State of Health graph layouts and exports. The graph is
// the GET /experiments/{name}/soh response: every VM is a node, every VLAN is a
// node with image "switch", and each VM interface is a VM-VLAN edge, so routers
// only reach each other through transit VLANs.

export const isVlan = (n) => String(n?.image).toLowerCase() === 'switch';
export const isInfra = (n) => /^(router|firewall)$/i.test(String(n?.image));

// What the UI draws for each node status; colors match the legend.
export const STATUS_COLORS = {
  running: '#4F8F00',
  notrunning: '#670b00',
  notboot: 'black',
  notdeploy: '#FFD479',
  external: '#005493',
};
export const SOH_ERROR_COLOR = '#FF9900';
export const VLAN_COLOR = '#9aa7b5';

export const STATUS_LABELS = {
  running: 'Running',
  notrunning: 'Not running',
  notboot: 'Not booted',
  notdeploy: 'Not deployed',
  external: 'External / HIL',
};

// The number of VMs among the nodes a filter shows: routers and firewalls
// are VMs, VLAN segments are not.
export function countVms(nodes) {
  return (nodes ?? []).filter((n) => !isVlan(n)).length;
}

// The number of VLAN segments among the nodes a filter shows.
export function countVlans(nodes) {
  return (nodes ?? []).filter(isVlan).length;
}

export function adjacency(graph) {
  const adj = new Map(graph.nodes.map((n) => [n.id, []]));
  for (const e of graph.edges) {
    if (!adj.has(e.source) || !adj.has(e.target)) continue;
    adj.get(e.source).push(e.target);
    adj.get(e.target).push(e.source);
  }
  return adj;
}

export function bfs(adj, root) {
  const depth = new Map([[root, 0]]);
  const parent = new Map([[root, null]]);
  const order = [root];
  for (let i = 0; i < order.length; i++) {
    const u = order[i];
    for (const v of adj.get(u) ?? []) {
      if (!depth.has(v)) {
        depth.set(v, depth.get(u) + 1);
        parent.set(v, u);
        order.push(v);
      }
    }
  }
  return { depth, parent, order };
}

// The graph center: the router or firewall with the smallest eccentricity,
// else the best VLAN, else any node. Only the 40 busiest candidates are
// tried so large ranges stay fast.
export function chooseRoot(graph, adj) {
  let cands = graph.nodes.filter(isInfra);
  if (!cands.length) cands = graph.nodes.filter(isVlan);
  if (!cands.length) cands = graph.nodes;
  if (!cands.length) return undefined;
  if (cands.length > 40) {
    cands = [...cands]
      .sort((a, b) => adj.get(b.id).length - adj.get(a.id).length)
      .slice(0, 40);
  }
  let best = cands[0].id;
  let bestScore = Infinity;
  for (const c of cands) {
    const { depth } = bfs(adj, c.id);
    // prefer the candidate that reaches the most nodes, then the most central
    let ecc = 0;
    for (const d of depth.values()) ecc = Math.max(ecc, d);
    const score = (graph.nodes.length - depth.size) * 1e6 + ecc;
    if (score < bestScore) {
      bestScore = score;
      best = c.id;
    }
  }
  return best;
}

// Hop distance of every node from the root; each component the root does
// not reach is measured from its own first node.
function depthsFrom(graph, adj, root) {
  const { depth } = bfs(adj, root);
  for (const n of graph.nodes) {
    if (depth.has(n.id)) continue;
    for (const [k, v] of bfs(adj, n.id).depth) {
      if (!depth.has(k)) depth.set(k, v);
    }
  }
  return depth;
}

// Edges pointed away from the root, so layered layouts put the core on
// top, VLANs under their routers and hosts below.
export function orientedEdges(graph, adj) {
  const depth = depthsFrom(graph, adj, chooseRoot(graph, adj));
  return graph.edges
    .filter((e) => depth.has(e.source) && depth.has(e.target))
    .map((e) =>
      depth.get(e.source) <= depth.get(e.target)
        ? { id: e.id, s: e.source, t: e.target }
        : { id: e.id, s: e.target, t: e.source },
    );
}

// Grid cell of a host packed under its VLAN: wide enough for a label.
const CELL_W = 42;
const CELL_H = 18;
const BLOCK_HEAD = 14; // room for the VLAN node above its hosts

// The hosts attached only to one VLAN, packed as a grid block under it.
// Returns Map(vlanId -> {leaves, cols, rows, w, h}).
export function leafBlocks(graph, adj) {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const blocks = new Map();
  for (const n of graph.nodes) {
    if (!isVlan(n)) continue;
    const leaves = adj
      .get(n.id)
      .filter((v) => adj.get(v).length === 1 && !isInfra(byId.get(v)))
      .sort((a, b) =>
        String(byId.get(a).label).localeCompare(String(byId.get(b).label)),
      );
    if (!leaves.length) continue;
    const cols = Math.max(1, Math.min(8, Math.ceil(Math.sqrt(leaves.length))));
    const rows = Math.ceil(leaves.length / cols);
    blocks.set(n.id, {
      leaves,
      cols,
      rows,
      w: Math.max(40, cols * CELL_W),
      h: BLOCK_HEAD + rows * CELL_H,
    });
  }
  return blocks;
}

// Positions of a block's VLAN node and hosts, given the block's center x
// and top y. The VLAN node sits centered above the grid.
export function placeBlock(positions, vlanId, block, cx, top) {
  positions.set(vlanId, { x: cx, y: top + 4 });
  const x0 = cx - ((block.cols - 1) * CELL_W) / 2 - 8;
  block.leaves.forEach((v, i) =>
    positions.set(v, {
      x: x0 + (i % block.cols) * CELL_W,
      y: top + BLOCK_HEAD + 6 + Math.floor(i / block.cols) * CELL_H,
    }),
  );
}

// Group boxes for the blocks: the VLAN node and its packed hosts.
export function blockGroups(blocks) {
  return [...blocks.entries()].map(([vlanId, b]) => ({
    id: `block-${vlanId}`,
    members: [vlanId, ...b.leaves],
    padding: 6,
  }));
}

// Edges from packed hosts to their VLAN: drawn faintly, since the block
// already shows which VLAN the hosts are on.
export function blockEdges(graph, blocks) {
  const folded = new Set([...blocks.values()].flatMap((b) => b.leaves));
  return new Set(
    graph.edges
      .filter((e) => folded.has(e.source) || folded.has(e.target))
      .map((e) => e.id),
  );
}

// Bounding box of positions (and optional group boxes) as {x0, y0, x1, y1}.
export function boundsOf(points, pad = 0) {
  let x0 = Infinity;
  let y0 = Infinity;
  let x1 = -Infinity;
  let y1 = -Infinity;
  for (const p of points) {
    if (!Number.isFinite(p?.x) || !Number.isFinite(p?.y)) continue;
    x0 = Math.min(x0, p.x);
    y0 = Math.min(y0, p.y);
    x1 = Math.max(x1, p.x);
    y1 = Math.max(y1, p.y);
  }
  if (x0 === Infinity) return null;
  return { x0: x0 - pad, y0: y0 - pad, x1: x1 + pad, y1: y1 + pad };
}

// Plain {nodes, edges} copy of the graph that the layouts work on.
export function layoutInput(nodes, edges) {
  const ids = new Set(nodes.map((n) => n.id));
  return {
    nodes: nodes.map((n) => ({
      id: n.id,
      label: n.label,
      image: n.image,
      status: n.status,
    })),
    edges: edges
      .filter((e) => ids.has(e.source) && ids.has(e.target))
      .map((e) => ({ id: e.id, source: e.source, target: e.target })),
  };
}

// The fill the graph gives a node: VLAN segments show the VLAN icon
// pattern, and VMs show no status color while the experiment is stopped
// (external VMs always do).
export function nodeFill(node, running) {
  const status = String(node?.status ?? '').toLowerCase();
  if (status === 'external') return STATUS_COLORS.external;
  if (status === 'ignore') return 'url(#switch)';
  if (!running) return undefined;
  return STATUS_COLORS[status];
}

export const SOH_STYLE_LABEL_KEY = '__sohStyle';

// The fill a VM's custom style tag sets, if any.
function customFill(node) {
  if (isVlan(node)) return undefined;
  const style = node?.tags?.[SOH_STYLE_LABEL_KEY];
  const m = /(?:^|;)\s*fill\s*:\s*([^;]+)/i.exec(style ?? '');
  return m ? m[1].trim() : undefined;
}

const NAMED = { black: '#000000', white: '#ffffff' };

// #rrggbb for a CSS hex, rgb() or rgba() color, black or white; null for
// anything else.
function toHex(color) {
  const c = String(color ?? '')
    .trim()
    .toLowerCase();
  if (NAMED[c]) return NAMED[c];
  let m = /^#([0-9a-f]{3,4})$/.exec(c);
  if (m) {
    return '#' + [...m[1].slice(0, 3)].map((d) => d + d).join('');
  }
  m = /^#([0-9a-f]{6})(?:[0-9a-f]{2})?$/.exec(c);
  if (m) return '#' + m[1];
  m = /^rgba?\(\s*(\d+)\s*[, ]\s*(\d+)\s*[, ]\s*(\d+)/.exec(c);
  if (m) {
    return (
      '#' +
      m
        .slice(1, 4)
        .map((v) => Math.min(255, Number(v)).toString(16).padStart(2, '0'))
        .join('')
    );
  }
  return null;
}

// The color a node shows in the graph, as #rrggbb, for exports: custom
// style first, then the status color; VLANs get the VLAN color, and a VM
// with no status color is drawn black (the SVG default fill).
export function displayColor(node, running) {
  const custom = toHex(customFill(node));
  if (custom) return custom;
  if (isVlan(node)) return VLAN_COLOR;
  return toHex(nodeFill(node, running)) ?? '#000000';
}
