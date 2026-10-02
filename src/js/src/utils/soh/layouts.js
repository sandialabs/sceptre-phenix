// The State of Health graph layouts. Each layout is a module loaded on
// demand, so a layout's library downloads only when someone picks it:
//
//   kind: 'simulation'  createSimulation(nodes, links, {width, height})
//                       returns a running d3 force simulation
//   kind: 'static'      compute({nodes, edges}) returns (or resolves to)
//                       { positions: Map(id -> {x, y}),
//                         groups?: [{id, label?, members: [id], padding}],
//                         faintEdges?: Set(edge id) }
//
// The view draws every layout with the same d3 code.
import { readPref, writePref } from '../prefs.js';
import * as force from './layouts/force.js';

export const DEFAULT_LAYOUT = 'force';

// cola.js slows down steeply with size (about 4 s at 500 nodes and 45 s at
// 2,000), so the VLAN groups layout is turned off above this many nodes.
export const GROUPS_MAX_NODES = 300;

// label: full name; short: the selector button; hint: one line on what the
// layout shows.
export const LAYOUTS = [
  {
    id: 'force',
    label: 'Force',
    short: 'Force',
    hint: 'Physics simulation (default)',
    load: () => Promise.resolve(force),
  },
  {
    id: 'radial',
    label: 'Radial tree',
    short: 'Radial',
    hint: 'Rings out from the core router',
    load: () => import('./layouts/radial.js'),
  },
  {
    id: 'layered',
    label: 'Layered',
    short: 'Layered',
    hint: 'Top-down tiers (dagre)',
    load: () => import('./layouts/layered.js'),
  },
  {
    id: 'groups',
    label: 'VLAN groups',
    short: 'VLANs',
    hint: 'Hosts boxed by VLAN (cola.js)',
    maxNodes: GROUPS_MAX_NODES,
    load: () => import('./layouts/groups.js'),
  },
  {
    id: 'elk',
    label: 'ELK layered',
    short: 'ELK',
    hint: 'Top-down tiers with host blocks (ELK)',
    load: () => import('./layouts/elk.js'),
  },
];

export function findLayout(id) {
  return LAYOUTS.find((l) => l.id === id);
}

// Why a layout cannot draw a graph of this size, or '' when it can.
function unavailableReason(layout, nodeCount) {
  if (layout.maxNodes && nodeCount > layout.maxNodes) {
    return `${layout.label} is off above ${layout.maxNodes} nodes (this graph has ${nodeCount}): it would take many seconds to compute`;
  }
  return '';
}

// The choices for the selector, with the ones this graph is too big for
// disabled and the reason why.
export function layoutOptions(nodeCount) {
  return LAYOUTS.map((l) => {
    const reason = unavailableReason(l, nodeCount);
    return {
      id: l.id,
      label: l.label,
      short: l.short,
      hint: l.hint,
      disabled: Boolean(reason),
      reason,
    };
  });
}

// The layout to draw for a chosen id: an unknown id or a layout the graph is
// too big for falls back to the default, with a notice saying why.
export function resolveLayout(id, nodeCount) {
  const layout = findLayout(id);
  if (!layout) return { id: DEFAULT_LAYOUT, notice: '' };
  const reason = unavailableReason(layout, nodeCount);
  if (reason) {
    const fallback = findLayout(DEFAULT_LAYOUT).label;
    return { id: DEFAULT_LAYOUT, notice: `${reason}. Showing ${fallback}.` };
  }
  return { id: layout.id, notice: '' };
}

const loaded = new Map();

// Loads a layout module once; a failed load is retried next time.
function loadLayout(id) {
  const layout = findLayout(id);
  if (!layout) return Promise.reject(new Error(`unknown layout "${id}"`));
  if (loaded.has(id)) return loaded.get(id);
  const p = layout.load().then((mod) => {
    if (mod?.kind !== 'simulation' && typeof mod?.compute !== 'function') {
      throw new Error(`layout "${id}" does not provide compute()`);
    }
    return mod;
  });
  loaded.set(id, p);
  p.catch(() => loaded.delete(id));
  return p;
}

// Loads and runs a layout. When it fails, onError gets the error and the
// default layout is used instead. Resolves to {id, mod, result}, where id is
// the layout actually used and result is the static layout's output (null
// for a simulation).
export async function runLayout(id, graph, { onError } = {}) {
  try {
    const mod = await loadLayout(id);
    const result = mod.kind === 'simulation' ? null : await mod.compute(graph);
    if (result && !(result.positions instanceof Map)) {
      throw new Error(`layout "${id}" returned no positions`);
    }
    return { id, mod, result };
  } catch (err) {
    if (id === DEFAULT_LAYOUT) throw err;
    onError?.(err, id);
    const mod = await loadLayout(DEFAULT_LAYOUT);
    return { id: DEFAULT_LAYOUT, mod, result: null };
  }
}

export const LAYOUT_STORAGE_KEY = 'phenix.soh.layout';

// The remembered layout choice, or the default when there is none.
export function readLayoutPref() {
  const id = readPref(LAYOUT_STORAGE_KEY);
  return findLayout(id) ? id : DEFAULT_LAYOUT;
}

export function saveLayoutPref(id) {
  writePref(LAYOUT_STORAGE_KEY, id);
}
