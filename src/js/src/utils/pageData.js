// How each cached page fetches its data. They live here rather than in the
// pages so the data can be preloaded before a page is ever opened.
import axiosInstance from '@/utils/axios.js';
import { useErrorNotification } from '@/utils/errorNotif.js';
import {
  cachePage,
  cachedPage,
  fetchIntoCache,
  isLoadingPage,
  recentPage,
} from '@/utils/pageCache.js';
import { whenIdle } from '@/utils/prefetch.js';
import { roleAllowed } from '@/utils/rbac.js';
import { usePhenixStore } from '@/store.js';

// the Logs page's default range, in seconds
export const DEFAULT_LOG_WINDOW = 10 * 60;

// Several log entries can share the same millisecond timestamp, so the Logs
// page's virtual scroller keys each entry by its own id rather than by `time`,
// which would drop rows and leave blank gaps. The counter is shared by every
// fetch and the websocket stream, so cached entries never share a key with
// ones streamed in later.
let nextLogId = 0;
export const withLogIds = (logs) =>
  logs.map((log) => ({ ...log, id: nextLogId++ }));

// cache keys for an experiment's page, running and stopped
export const experimentKey = (name) => `experiment/${name}`;
export const stoppedExperimentKey = (name) => `experiment/${name}/stopped`;
// cache key for an experiment's SCORCH runs page (its pipelines response)
export const scorchRunsKey = (name) => `scorchruns/${name}`;

const get = async (url, signal, config = {}) =>
  (await axiosInstance.get(url, { signal, ...config })).data;

// The running experiment page's data, without its VMs: the page lists them
// over the websocket (experiment/vms), so having the server also ask minimega
// for them here would only repeat that work. The VM rows the page last showed
// are kept, so the next visit opens with them while the fresh list loads.
export async function fetchRunningExperiment(name, signal) {
  const experiment = await get(`experiments/${name}`, signal, {
    params: { vms: false },
  });
  return {
    ...experiment,
    vms: cachedPage(experimentKey(name))?.data?.vms ?? [],
  };
}

// An experiment list this recent is reused rather than asked for again.
const EXPERIMENT_LIST_REUSE_MS = 5000;

// The experiment list for pages built on it (SCORCH, WebShark), sharing the
// Experiments page's request or recent result: listing experiments has the
// server query minimega for every running one.
export function experimentList() {
  return recentPage(
    'experiments',
    pageFetchers.experiments,
    EXPERIMENT_LIST_REUSE_MS,
  );
}

// A host list this recent is reused by pages that only need host names.
export const HOST_LIST_REUSE_MS = 60 * 1000;

// The names of the hosts VMs can be scheduled on, sorted, from the Hosts
// page's cached list when it is recent (or its request in flight) rather than
// a new cluster-wide host scan.
export async function schedulableHostNames() {
  const hosts = await recentPage(
    'hosts',
    pageFetchers.hosts,
    HOST_LIST_REUSE_MS,
  );
  return (hosts ?? [])
    .filter((host) => host.schedulable)
    .map((host) => host.name)
    .sort();
}

// Whether an experiment (as the experiment list or a websocket experiment
// message carries it) has the SCORCH app configured.
export const hasScorch = (exp) => (exp?.apps ?? []).includes('scorch');

// Whether the SCORCH page lists an experiment: it has SCORCH configured and
// the role may see it and its runs.
export const scorchListed = (exp) =>
  hasScorch(exp) &&
  roleAllowed('experiments/apps', 'get', exp.name) &&
  roleAllowed('experiments', 'get', exp.name);

// GET /experiments/{name}/scorch/pipelines, which also says whether the SCORCH
// app is running
export const scorchPipelines = (name, signal) =>
  get(`experiments/${name}/scorch/pipelines`, signal, {
    headers: { Accept: 'application/json' },
  });

// An experiment's row on the SCORCH page: a copy of it (the experiment
// objects are shared with the Experiments page) with its SCORCH run state and
// run names.
export const scorchRow = (exp, pipelines) => ({
  ...exp,
  scorch: {
    running: !!pipelines.app_running,
    run: pipelines.running,
    runs: (pipelines.pipelines ?? []).map((p) => p.name),
    pending: false,
  },
});

export const pageFetchers = {
  experiments: async (signal) =>
    // the list only shows VM counts, which need nothing from minimega
    (await get('experiments?vms=false', signal)).experiments ?? [],

  configs: async (signal) => (await get('configs', signal)).configs ?? [],

  // rescan: have the server inspect every image again, not only changed ones
  disks: async (signal, { rescan = false } = {}) =>
    (await get(rescan ? 'disks?refresh=true' : 'disks', signal)).disks ?? [],

  hosts: async (signal) => (await get('hosts', signal)).hosts ?? [],

  users: async (signal) => {
    // roles are only used for the role dropdown when creating/editing
    const [users, roles] = await Promise.all([
      get('users', signal),
      roleAllowed('roles', 'list')
        ? get('roles', signal).catch((err) => {
            // the user list is still worth showing without roles
            useErrorNotification(err);
            return null;
          })
        : null,
    ]);
    users.users.forEach((u) => (u.role_name = u.role.name));
    return {
      users: users.users,
      roleNames: roles ? roles.roles.map((r) => r.name) : [],
    };
  },

  logs: async (signal) => {
    const start = new Date(Date.now() - DEFAULT_LOG_WINDOW * 1000);
    return withLogIds(
      (await get(`logs?start=${start.toISOString()}`, signal)) ?? [],
    );
  },

  // experiments that have SCORCH configured, with their run state and run
  // names
  scorch: async (signal) => {
    const experiments = await experimentList();

    // The list already names each experiment's apps, so only experiments with
    // SCORCH configured are asked for their pipelines. Fetched concurrently
    // rather than one after another.
    const scorchExps = await Promise.all(
      experiments.map(async (exp) => {
        // one forbidden experiment must not fail the whole list
        if (!scorchListed(exp)) return null;

        const pipelines = await scorchPipelines(exp.name, signal);
        // the experiment's runs page shows the same response, so it opens at
        // once too, unless it has fetched a fresher copy of its own
        const runsKey = scorchRunsKey(exp.name);
        if (!isLoadingPage(runsKey)) cachePage(runsKey, pipelines);
        return scorchRow(exp, pipelines);
      }),
    );

    return scorchExps.filter((exp) => exp !== null);
  },

  // every experiment's state of health, as far as the role may get each
  soh: async (signal) => (await get('soh', signal)).experiments ?? [],

  settings: async (signal) => await get('settings', signal),

  vmtiles: async (signal) =>
    (await get('vms?screenshot=500', signal)).vms ?? [],
};

// Tabs worth loading ahead of a visit, cheapest first, with who may see them
// (mirrors the navbar). VM tiles are left out: they carry a screenshot per VM.
const PRELOADS = [
  ['experiments', () => roleAllowed('experiments', 'list')],
  ['configs', () => roleAllowed('configs', 'list')],
  ['users', () => usePhenixStore().role?.name !== 'Disabled'],
  ['logs', () => roleAllowed('logs', 'get')],
  ['hosts', () => roleAllowed('hosts', 'list')],
  ['scorch', () => roleAllowed('experiments', 'list')],
  ['soh', () => roleAllowed('experiments', 'list')],
  ['settings', () => roleAllowed('settings', 'update')],
  // last: the server inspects every disk image, one at a time
  ['disks', () => roleAllowed('disks', 'list')],
];

// Preload requests in flight at once, so the page on screen keeps its share
// of the browser's connections.
const PRELOAD_CONCURRENCY = 2;

// Loads every tab's data into the page cache in the background, a couple of
// requests at a time. Tabs already cached or loading are skipped. A failed
// preload is not reported here; the page reports its own failure when opened.
export async function preloadPages() {
  const queue = PRELOADS.filter(
    ([key, allowed]) => allowed() && !cachedPage(key) && !isLoadingPage(key),
  ).map(([key]) => key);

  const worker = async () => {
    for (let key = queue.shift(); key; key = queue.shift()) {
      const request = fetchIntoCache(key, pageFetchers[key]);
      request.release(); // nothing waits on it; the background limit applies
      await request.promise.catch(() => {});
    }
  };

  await Promise.all(Array.from({ length: PRELOAD_CONCURRENCY }, worker));
}

// Preloads once the browser is idle, so the page being opened loads first.
export function schedulePagePreload() {
  whenIdle(() => preloadPages(), 3000);
}
