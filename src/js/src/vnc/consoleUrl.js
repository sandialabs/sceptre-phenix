// The URL of the console page: /vnc?vm=exp/vm&vm=…&layout=…. The order of the
// vm parameters is the order of the panes, and the panes can come from
// different experiments.

/** The name of the console page route. */
export const CONSOLE_ROUTE = 'vnc';

/** The most panes that one console page shows. */
export const MAX_PANES = 4;

/** The layouts, in the order of the layout picker. */
export const LAYOUTS = ['single', 'cols', 'rows', 'main-2', 'grid'];

/** The number of panes in each layout. */
export const LAYOUT_SLOTS = Object.freeze({
  single: 1,
  cols: 2,
  rows: 2,
  'main-2': 3,
  grid: 4,
});

/** The accessible name of each layout. */
export const LAYOUT_LABELS = Object.freeze({
  single: 'One pane',
  cols: 'Two columns',
  rows: 'Two rows',
  'main-2': 'One large pane and two stacked',
  grid: 'Grid of four',
});

const DEFAULT_LAYOUTS = ['single', 'single', 'cols', 'main-2', 'grid'];

/**
 * The layout for this number of VMs when the URL names none.
 *
 * @param {number} count
 */
export function defaultLayout(count) {
  return DEFAULT_LAYOUTS[Math.min(Math.max(count, 0), MAX_PANES)];
}

/**
 * Whether the layout has a pane for each of `count` VMs.
 *
 * @param {string} layout
 * @param {number} count
 */
export function layoutFits(layout, count) {
  return Object.hasOwn(LAYOUT_SLOTS, layout) && LAYOUT_SLOTS[layout] >= count;
}

/**
 * The experiment and VM of a key such as `demo/web`, or null when it is not
 * exactly two names with a slash between them.
 *
 * @param {string} key
 * @returns {{ experiment: string, vm: string }|null}
 */
export function splitVmKey(key) {
  if (typeof key !== 'string') return null;
  const parts = key.split('/');
  if (parts.length !== 2) return null;
  const [experiment, vm] = parts.map((part) => part.trim());
  return experiment && vm ? { experiment, vm } : null;
}

/**
 * The VMs and the layout of the console page from its query. A bad vm value,
 * a VM named again and the VMs after the fourth are dropped. A layout that is
 * unknown or has too few panes gives the default layout.
 *
 * @param {Record<string, string|null|(string|null)[]>} query
 * @returns {{ vms: string[], layout: string }}
 */
export function parseConsoleQuery(query) {
  const vms = [];
  for (const value of [query?.vm ?? []].flat()) {
    const parts = splitVmKey(value);
    if (!parts) continue;
    const key = `${parts.experiment}/${parts.vm}`;
    if (!vms.includes(key)) vms.push(key);
  }
  vms.splice(MAX_PANES);
  const layout = query?.layout;
  return {
    vms,
    layout: layoutFits(layout, vms.length) ? layout : defaultLayout(vms.length),
  };
}

/**
 * The router location of the console page for these VMs, as `exp/vm` keys.
 * It leaves out the layout when it is the default.
 *
 * @param {string[]} vms
 * @param {string} [layout]
 */
export function consoleLocation(vms, layout) {
  const query = { vm: [...vms] };
  if (layout && layout !== defaultLayout(vms.length)) query.layout = layout;
  return { name: CONSOLE_ROUTE, query };
}

/**
 * The URL of the console page for these VMs, as `exp/vm` keys, for a link
 * that keeps a real `href`.
 *
 * @param {import('vue-router').Router} router
 * @param {string[]} vms
 * @param {string} [layout]
 */
export function consoleHref(router, vms, layout) {
  return router.resolve(consoleLocation(vms, layout)).href;
}
