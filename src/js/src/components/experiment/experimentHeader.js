// What the running experiment page's header shows: its scenarios, its apps'
// grid and states, and the labels and hosts its VM table filters by.

import { compare } from '@/utils/compare.js';

// The apps phenix runs itself at each stage, which cannot be triggered at the
// running stage.
export const DEFAULT_APPS = Object.freeze([
  'ntp',
  'serial',
  'startup',
  'vrouter',
  'scorch',
]);

// The stages the trigger dialog can run apps at, in the order phenix runs them.
export const TRIGGER_STAGES = Object.freeze([
  'configure',
  'pre-start',
  'post-start',
  'running',
  'cleanup',
]);

// The apps the trigger dialog offers at a stage: every app, but the built-in
// ones only at a stage other than running.
export function triggerableApps(apps, stage) {
  const all = apps ?? [];
  if (stage !== 'running') return [...all];
  return all.filter((app) => !DEFAULT_APPS.includes(app));
}

// Why a user may not trigger these apps (allowed says whether the role may),
// or null when the user can.
export function triggerBlocked(allowed, apps) {
  if (!allowed) return 'Cannot trigger apps: you do not have permission';
  if (!(apps ?? []).some((app) => !DEFAULT_APPS.includes(app))) {
    return 'Cannot trigger apps: this experiment has only built-in apps';
  }
  return null;
}

// The browser preference that keeps the experiment page header expanded;
// without it, the header shows only its one-line summary.
export const HEADER_EXPANDED_PREF = 'phenix.experiment.headerExpanded';

// Where the Builder opens the experiment: its draft when it has one, else its
// topology; null when the Builder did not make it.
export function builderRoute(experiment) {
  const builder = experiment?.builder;
  if (!builder?.draft && !builder?.document) return null;
  const query = builder.draft
    ? { draft: builder.draft }
    : { topology: experiment.topology };
  return { name: 'builder', query };
}

// The hosts tile's names: the first `max`, and how many more there are.
export function hostsShown(hosts, max) {
  const all = hosts ?? [];
  return { shown: all.slice(0, max), more: Math.max(all.length - max, 0) };
}

// The experiment's scenarios, its own first; a copy cached before the server
// listed them names only its own.
export function experimentScenarios(experiment) {
  const listed = experiment?.scenarios;
  if (Array.isArray(listed) && listed.length > 0) return listed;
  return experiment?.scenario ? [experiment.scenario] : [];
}

// The columns that lay `count` tiles in the fewest rows of at most `max`, the
// rows as even as can be: 5 tiles in rows of up to 4 make rows of 3 and 2.
export function balancedColumns(count, max) {
  if (count < 1 || max < 1) return 1;
  const rows = Math.ceil(count / max);
  return Math.ceil(count / rows);
}

// What an apps/<app> message's action says the app is doing now, or null for
// an action that says nothing of it.
export function appStateAfter(action) {
  switch (action) {
    case 'start':
      return 'running';
    case 'success':
      return 'succeeded';
    case 'error':
      return 'failed';
    case 'canceled':
      return 'canceled';
    default:
      return null;
  }
}

// The words and icon each app state shows, so no state is told by color alone.
export const APP_STATES = Object.freeze({
  running: { text: 'Running', icon: 'hourglass-half' },
  succeeded: { text: 'Last run succeeded', icon: 'check-circle' },
  failed: { text: 'Last run failed', icon: 'times-circle' },
  canceled: { text: 'Last run canceled', icon: 'ban' },
  builtin: { text: 'Built-in', icon: 'circle' },
  user: { text: 'User app', icon: 'play-circle' },
});

// The state an app's tile shows: the last one the page heard of, or else
// whether it is built in.
export function appTileState(app, states) {
  return states?.[app] ?? (DEFAULT_APPS.includes(app) ? 'builtin' : 'user');
}

// A label as the filter names it, and a key unique to it.
export const labelText = (label) => `${label.key}=${label.value}`;
export const labelId = (label) => JSON.stringify([label.key, label.value]);

// Whether a VM tag is a label people set; phenix keeps its own (notes
// included) under keys starting with "__".
const isUserLabel = (key) => !key.startsWith('__');

// The labels to filter by, once each in key then value order: the server's
// for the experiment, those on the rows shown (maybe newer), and those picked.
export function labelOptions(listed, vms, picked = []) {
  const byId = new Map();
  const add = (label) => byId.set(labelId(label), label);
  for (const label of listed ?? []) add({ key: label.key, value: label.value });
  for (const vm of vms ?? []) {
    for (const [key, value] of Object.entries(vm?.tags ?? {})) {
      if (key && isUserLabel(key)) add({ key, value: String(value) });
    }
  }
  for (const label of picked) add(label);
  return [...byId.values()].sort(
    (a, b) => compare(a.key, b.key) || compare(a.value, b.value),
  );
}

// The cluster hosts to filter by, once each and sorted: the server's for the
// experiment, those running the rows shown (maybe newer), and those picked.
export function hostOptions(listed, vms, picked = []) {
  const hosts = new Set([...(listed ?? []), ...picked]);
  for (const vm of vms ?? []) {
    if (vm?.host && !vm.external) hosts.add(vm.host);
  }
  return [...hosts].sort(compare);
}
