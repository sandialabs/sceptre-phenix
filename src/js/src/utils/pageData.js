// How each cached page fetches its data. They live here rather than in the
// pages so the data can be preloaded before a page is ever opened.
import * as configsApi from '@/api/configs.js';
import * as disksApi from '@/api/disks.js';
import * as experimentsApi from '@/api/experiments.js';
import * as hostsApi from '@/api/hosts.js';
import * as logsApi from '@/api/logs.js';
import * as rolesApi from '@/api/roles.js';
import * as scorchApi from '@/api/scorch.js';
import * as settingsApi from '@/api/settings.js';
import * as usersApi from '@/api/users.js';
import * as vmsApi from '@/api/vms.js';
import { useErrorNotification } from '@/utils/errorNotif.js';
import {
  cachePage,
  cachedPage,
  fetchIntoCache,
  isLoadingPage,
  recentPage,
} from '@/utils/pageCache.js';
import { whenIdle } from '@/utils/prefetch.js';
import {
  FreshnessMeta,
  mergeFreshness,
  whenServing,
} from '@/utils/freshness.js';
import { serverNotAnswering } from '@/utils/unreachable.js';
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

// Copies of VM rows without their screenshots, for the cache: each is tens of
// kilobytes, and the live page keeps updating its own rows' screenshots.
export const withoutScreenshots = (vms) =>
  (vms ?? []).map((vm) => {
    const row = { ...vm };
    delete row.screenshot;
    return row;
  });

// the data a request answers with
const body = async (request) => (await request).data;

// The running experiment page's data without its VMs, which it lists over
// the websocket, plus the rows it last showed, without screenshots.
export async function fetchRunningExperiment(name, signal, meta) {
  const experiment = await body(
    experimentsApi.getExperiment(name, {
      signal,
      meta,
      params: { vms: false },
    }),
  );
  return {
    ...experiment,
    vms: withoutScreenshots(cachedPage(experimentKey(name))?.data?.vms),
  };
}

// The experiment list for pages built on it (SCORCH, WebShark), sharing the
// Experiments page's request or recent result (see the reuse window in
// freshness.js): listing experiments has the server query minimega for every
// running one. meta, if given, takes on the list's freshness.
export async function experimentList(meta) {
  const experiments = await recentPage('experiments', pageFetchers.experiments);
  mergeFreshness(meta, cachedPage('experiments')?.meta);
  return experiments;
}

// The names of the hosts VMs can be scheduled on, sorted, from the Hosts
// page's cached list when it is recent (see the reuse window in freshness.js)
// or its request in flight, rather than a new cluster-wide host scan.
export async function schedulableHostNames() {
  const hosts = await recentPage('hosts', pageFetchers.hosts);
  return (hosts ?? [])
    .filter((host) => host.schedulable)
    .map((host) => host.name)
    .sort();
}

// Whether an experiment (as the experiment list or a websocket experiment
// message carries it) has the SCORCH app configured.
const hasScorch = (exp) => (exp?.apps ?? []).includes('scorch');

// Whether the SCORCH page lists an experiment: it has SCORCH configured and
// the role may see it and its runs.
export const scorchListed = (exp) =>
  hasScorch(exp) &&
  roleAllowed('experiments/apps', 'get', exp.name) &&
  roleAllowed('experiments', 'get', exp.name);

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

// Each fetcher takes (signal, meta) and passes meta with its requests, so the
// page cache knows how fresh the result is.
export const pageFetchers = {
  experiments: async (signal, meta) =>
    // the list only shows VM counts, which need nothing from minimega
    (await body(experimentsApi.listExperiments({ signal, meta })))
      .experiments ?? [],

  configs: async (signal, meta) =>
    (await body(configsApi.listConfigs({ signal, meta }))).configs ?? [],

  // rescan: have the server inspect every image again, not only changed ones
  disks: async (signal, meta, { rescan = false } = {}) =>
    (
      await body(
        disksApi.listDisks(rescan ? { refresh: true } : undefined, {
          signal,
          meta,
        }),
      )
    ).disks ?? [],

  hosts: async (signal, meta) =>
    (await body(hostsApi.listHosts({ signal, meta }))).hosts ?? [],

  users: async (signal, meta) => {
    // roles are only used for the role dropdown when creating/editing
    const [users, roles] = await Promise.all([
      body(usersApi.listUsers({ signal, meta })),
      roleAllowed('roles', 'list')
        ? body(rolesApi.listRoles({ signal, meta })).catch((err) => {
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

  logs: async (signal, meta) => {
    const start = new Date(Date.now() - DEFAULT_LOG_WINDOW * 1000);
    return withLogIds(
      (await body(logsApi.listLogs(start, null, { signal, meta }))) ?? [],
    );
  },

  // experiments that have SCORCH configured, with their run state and run
  // names
  scorch: async (signal, meta) => {
    const experiments = await experimentList(meta);

    // The list already names each experiment's apps, so only experiments with
    // SCORCH configured are asked for their pipelines. Fetched concurrently
    // rather than one after another.
    const scorchExps = await Promise.all(
      experiments.map(async (exp) => {
        // one forbidden experiment must not fail the whole list
        if (!scorchListed(exp)) return null;

        const runMeta = new FreshnessMeta();
        const pipelines = await body(
          scorchApi.listScorchPipelines(exp.name, { signal, meta: runMeta }),
        );
        mergeFreshness(meta, runMeta);
        // the experiment's runs page shows the same response, so it opens at
        // once too, unless it has fetched a fresher copy of its own
        const runsKey = scorchRunsKey(exp.name);
        if (!isLoadingPage(runsKey)) cachePage(runsKey, pipelines, runMeta);
        return scorchRow(exp, pipelines);
      }),
    );

    return scorchExps.filter((exp) => exp !== null);
  },

  // every experiment's state of health, as far as the role may get each
  soh: async (signal, meta) =>
    (await body(experimentsApi.listSoh({ signal, meta }))).experiments ?? [],

  settings: (signal, meta) => body(settingsApi.getSettings({ signal, meta })),

  // the tiles fetch their screenshots themselves, only while on screen
  vmtiles: async (signal, meta) =>
    (await body(vmsApi.listAllVms({ signal, meta }))).vms ?? [],
};

// Tabs worth loading ahead of a visit, cheapest first, with who may see them
// (mirrors the navbar). VM tiles are left out: they list every running VM.
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
      // paused while the server is shutting down
      await whenServing();
      if (cachedPage(key) || isLoadingPage(key)) continue;
      const request = fetchIntoCache(key, pageFetchers[key]);
      request.release(); // nothing waits on it; the background limit applies
      const failure = await request.promise.then(
        () => null,
        (err) => err,
      );
      // the server is unreachable: the rest would fail too
      if (serverNotAnswering(failure)) return;
    }
  };

  await Promise.all(Array.from({ length: PRELOAD_CONCURRENCY }, worker));
}

// Preloads once the browser is idle, so the page being opened loads first.
export function schedulePagePreload() {
  whenIdle(() => preloadPages(), 3000);
}
