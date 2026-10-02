// How each page's data is fetched, shared and preloaded (utils/pageData.js),
// with the server answered by a mocked axios.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/axios.js', () => ({ default: { get: vi.fn() } }));
vi.mock('@/utils/errorNotif.js', () => ({ useErrorNotification: vi.fn() }));
vi.mock('@/utils/rbac.js', () => ({ roleAllowed: vi.fn() }));
const store = vi.hoisted(() => ({ role: null }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => store }));

import axiosInstance from '@/utils/axios.js';
import { useErrorNotification } from '@/utils/errorNotif.js';
import {
  cachedPage,
  cachePage,
  clearPageCache,
  fetchIntoCache,
} from '@/utils/pageCache.js';
import {
  DEFAULT_LOG_WINDOW,
  HOST_LIST_REUSE_MS,
  experimentKey,
  experimentList,
  fetchRunningExperiment,
  hasScorch,
  pageFetchers,
  preloadPages,
  schedulableHostNames,
  schedulePagePreload,
  scorchRow,
  scorchRunsKey,
  withLogIds,
} from '@/utils/pageData.js';
import { roleAllowed } from '@/utils/rbac.js';
import { flush } from './helpers/async.js';

// answers each GET with the body given for its URL
function serve(bodies) {
  axiosInstance.get.mockImplementation(async (url) => {
    if (!(url in bodies)) throw new Error(`unexpected GET ${url}`);
    return { data: bodies[url] };
  });
}

const urls = () => axiosInstance.get.mock.calls.map(([url]) => url);

beforeEach(() => {
  clearPageCache();
  axiosInstance.get.mockReset();
  roleAllowed.mockReset();
  roleAllowed.mockReturnValue(true);
  useErrorNotification.mockReset();
  store.role = { name: 'Global Admin' };
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('the list pages', () => {
  it.each([
    ['experiments', 'experiments?vms=false', { experiments: [1] }],
    ['configs', 'configs', { configs: [1] }],
    ['disks', 'disks', { disks: [1] }],
    ['hosts', 'hosts', { hosts: [1] }],
    ['soh', 'soh', { experiments: [1] }],
    ['vmtiles', 'vms?screenshot=500', { vms: [1] }],
  ])('%s lists what GET %s answers, or nothing', async (page, url, body) => {
    const signal = new AbortController().signal;
    axiosInstance.get.mockResolvedValue({ data: body });
    expect(await pageFetchers[page](signal)).toEqual([1]);
    expect(axiosInstance.get).toHaveBeenCalledWith(url, { signal });

    axiosInstance.get.mockResolvedValue({ data: {} });
    expect(await pageFetchers[page](signal)).toEqual([]);
  });

  it('asks the server to inspect every disk again on a rescan', async () => {
    serve({ 'disks?refresh=true': { disks: [] } });
    await pageFetchers.disks(undefined, { rescan: true });
    expect(urls()).toEqual(['disks?refresh=true']);
  });

  it('shows the settings as the server sends them', async () => {
    serve({ settings: { a: 1 } });
    expect(await pageFetchers.settings()).toEqual({ a: 1 });
  });
});

describe('the Users page', () => {
  const users = { users: [{ name: 'a', role: { name: 'Viewer' } }] };

  it('lists the users with their role names, and the roles to pick', async () => {
    serve({ users, roles: { roles: [{ name: 'Viewer' }, { name: 'Admin' }] } });
    expect(await pageFetchers.users()).toEqual({
      users: [{ name: 'a', role: { name: 'Viewer' }, role_name: 'Viewer' }],
      roleNames: ['Viewer', 'Admin'],
    });
  });

  it('lists the users without roles the role may not list', async () => {
    roleAllowed.mockImplementation((resource) => resource != 'roles');
    serve({ users });
    expect((await pageFetchers.users()).roleNames).toEqual([]);
    expect(urls()).toEqual(['users']);
  });

  it('still lists the users when the roles fail to load, and says so', async () => {
    const err = new Error('forbidden');
    axiosInstance.get.mockImplementation(async (url) => {
      if (url == 'roles') throw err;
      return { data: users };
    });
    const page = await pageFetchers.users();
    expect(page.users).toHaveLength(1);
    expect(page.roleNames).toEqual([]);
    expect(useErrorNotification).toHaveBeenCalledWith(err);
  });
});

describe('the Logs page', () => {
  it(`asks for the last ${DEFAULT_LOG_WINDOW / 60} minutes`, async () => {
    vi.useFakeTimers({ now: Date.parse('2026-09-29T15:10:00Z') });
    serve({ 'logs?start=2026-09-29T15:00:00.000Z': null });
    expect(await pageFetchers.logs()).toEqual([]);
  });

  it('keys each entry by an id no other entry has', async () => {
    const time = '2026-09-29T15:00:00Z';
    axiosInstance.get.mockResolvedValue({
      data: [
        { time, msg: 'first' },
        { time, msg: 'second' },
      ],
    });

    const logs = await pageFetchers.logs(undefined);
    const streamed = withLogIds([{ time, msg: 'streamed' }]);

    // entries sharing a timestamp still get their own key, and entries
    // streamed in later never reuse a preloaded entry's key
    const ids = [...logs, ...streamed].map((log) => log.id);
    expect(ids.every(Number.isInteger)).toBe(true);
    expect(new Set(ids).size).toBe(3);
    expect(logs.map((log) => log.msg)).toEqual(['first', 'second']);
  });
});

describe('the running experiment page', () => {
  it('asks for the experiment without its VMs', async () => {
    axiosInstance.get.mockResolvedValue({
      data: { name: 'exp', running: true, vm_count: 2 },
    });
    const signal = new AbortController().signal;

    const exp = await fetchRunningExperiment('exp', signal);

    expect(axiosInstance.get).toHaveBeenCalledWith('experiments/exp', {
      signal,
      params: { vms: false },
    });
    expect(exp).toEqual({ name: 'exp', running: true, vm_count: 2, vms: [] });
  });

  it('keeps the VM rows it last showed for that experiment', async () => {
    const rows = [{ name: 'vm1', running: true }];
    cachePage(experimentKey('exp'), { name: 'exp', vms: rows });
    axiosInstance.get.mockResolvedValue({
      data: { name: 'exp', running: true },
    });

    expect((await fetchRunningExperiment('exp')).vms).toBe(rows);
    // another experiment's rows are not borrowed
    expect((await fetchRunningExperiment('other')).vms).toEqual([]);
  });
});

describe('the shared lists', () => {
  it('reuse the Experiments page request or its recent result', async () => {
    const answer = Promise.withResolvers();
    axiosInstance.get.mockReturnValue(answer.promise);

    // the Experiments page's request is still loading
    const page = fetchIntoCache('experiments', pageFetchers.experiments);
    const joined = experimentList();
    answer.resolve({ data: { experiments: [{ name: 'a' }] } });
    expect(await joined).toEqual([{ name: 'a' }]);
    page.release();
    expect(axiosInstance.get).toHaveBeenCalledTimes(1);

    // and then its result is recent enough to reuse
    expect(await experimentList()).toEqual([{ name: 'a' }]);
    expect(axiosInstance.get).toHaveBeenCalledTimes(1);
  });

  it('name the schedulable hosts from a recent Hosts page list', async () => {
    cachePage('hosts', [
      { name: 'b', schedulable: true },
      { name: 'a', schedulable: true },
      { name: 'head', schedulable: false },
    ]);

    expect(await schedulableHostNames()).toEqual(['a', 'b']);
    expect(axiosInstance.get).not.toHaveBeenCalled();
  });

  it('fetch the hosts into the cache when no list is recent', async () => {
    vi.useFakeTimers();
    cachePage('hosts', [{ name: 'old', schedulable: true }]);
    vi.advanceTimersByTime(HOST_LIST_REUSE_MS + 1);
    serve({ hosts: { hosts: [{ name: 'new', schedulable: true }] } });

    expect(await schedulableHostNames()).toEqual(['new']);
    expect(urls()).toEqual(['hosts']);
    expect(cachedPage('hosts').data).toEqual([
      { name: 'new', schedulable: true },
    ]);
  });
});

describe('the SCORCH page', () => {
  it('knows SCORCH experiments by the apps they carry', () => {
    expect(hasScorch({ apps: ['ntp', 'scorch'] })).toBe(true);
    expect(hasScorch({ apps: ['ntp'] })).toBe(false);
    expect(hasScorch({})).toBe(false);
    expect(hasScorch(undefined)).toBe(false);
  });

  it('asks only SCORCH experiments for their pipelines', async () => {
    cachePage('experiments', [
      { name: 'a', apps: ['scorch'] },
      { name: 'b', apps: ['ntp'] },
      { name: 'c', apps: ['scorch', 'soh'] },
      { name: 'd', apps: [] },
    ]);
    const pipelines = {
      a: { pipelines: [{ name: 'first' }], running: 2, app_running: true },
      c: { pipelines: [], running: -1, app_running: false },
    };
    axiosInstance.get.mockImplementation(async (url) => ({
      data: pipelines[url.split('/')[1]],
    }));

    const exps = await pageFetchers.scorch(undefined);

    expect(urls().sort()).toEqual([
      'experiments/a/scorch/pipelines',
      'experiments/c/scorch/pipelines',
    ]);
    expect(exps.map((e) => e.name)).toEqual(['a', 'c']);
    expect(exps[0].scorch).toEqual({
      running: true,
      run: 2,
      runs: ['first'],
      pending: false,
    });
    expect(exps[1].scorch.running).toBe(false);
    // each experiment's runs page opens with the same answer
    expect(cachedPage(scorchRunsKey('a')).data).toBe(pipelines.a);
  });

  it("leaves the runs page's own request for its answer alone", async () => {
    cachePage('experiments', [{ name: 'a', apps: ['scorch'] }]);
    const fresher = { pipelines: [{ name: 'fresher' }] };
    const answer = Promise.withResolvers();
    const own = fetchIntoCache(scorchRunsKey('a'), () => answer.promise);
    axiosInstance.get.mockResolvedValue({ data: { pipelines: [] } });

    await pageFetchers.scorch(undefined);
    expect(cachedPage(scorchRunsKey('a'))).toBeUndefined();

    answer.resolve(fresher);
    await own.promise;
    expect(cachedPage(scorchRunsKey('a')).data).toBe(fresher);
  });

  it('skips experiments the role may not get', async () => {
    cachePage('experiments', [
      { name: 'a', apps: ['scorch'] },
      { name: 'b', apps: ['scorch'] },
    ]);
    roleAllowed.mockImplementation((...args) => args[2] !== 'b');
    axiosInstance.get.mockResolvedValue({
      data: { pipelines: [], running: -1, app_running: false },
    });

    const exps = await pageFetchers.scorch(undefined);

    expect(exps.map((e) => e.name)).toEqual(['a']);
    expect(axiosInstance.get).toHaveBeenCalledTimes(1);
  });

  it('shows each experiment as a copy with its run state', () => {
    const exp = { name: 'a', status: 'started', apps: ['scorch'] };
    const row = scorchRow(exp, {
      app_running: true,
      running: 1,
      pipelines: [{ name: 'baseline' }, { name: 'attack' }],
    });
    expect(row).toEqual({
      ...exp,
      scorch: {
        running: true,
        run: 1,
        runs: ['baseline', 'attack'],
        pending: false,
      },
    });
    expect(row).not.toBe(exp);
    expect(scorchRow(exp, {}).scorch).toMatchObject({
      running: false,
      runs: [],
    });
  });
});

describe('preloading', () => {
  // every page's fetcher answers with its own name, or as the test says
  beforeEach(() => {
    for (const key of Object.keys(pageFetchers)) {
      vi.spyOn(pageFetchers, key).mockResolvedValue([key]);
    }
  });

  it('loads every tab the role may see except the VM tiles', async () => {
    roleAllowed.mockImplementation((resource) => resource !== 'disks');
    await preloadPages();

    for (const key of [
      'experiments',
      'configs',
      'users',
      'logs',
      'hosts',
      'scorch',
      'soh',
      'settings',
    ]) {
      expect(cachedPage(key).data).toEqual([key]);
    }
    expect(cachedPage('disks')).toBeUndefined();
    expect(pageFetchers.vmtiles).not.toHaveBeenCalled();
  });

  it('leaves out the Users tab for a disabled account', async () => {
    store.role = { name: 'Disabled' };
    await preloadPages();
    expect(pageFetchers.users).not.toHaveBeenCalled();
  });

  it('skips tabs already cached or loading', async () => {
    cachePage('hosts', ['cached']);
    const answer = Promise.withResolvers();
    const loading = fetchIntoCache('configs', () => answer.promise);
    await preloadPages();

    expect(pageFetchers.hosts).not.toHaveBeenCalled();
    expect(pageFetchers.configs).not.toHaveBeenCalled();
    expect(cachedPage('hosts').data).toEqual(['cached']);
    answer.resolve([]);
    await loading.promise;
  });

  it('runs two requests at a time until every tab has loaded', async () => {
    const started = [];
    for (const key of Object.keys(pageFetchers)) {
      pageFetchers[key].mockImplementation(() => {
        const request = Promise.withResolvers();
        started.push(request);
        return request.promise;
      });
    }

    const done = preloadPages();
    let finished = 0;
    for (;;) {
      await flush();
      const left = 9 - finished;
      expect(started.length - finished).toBe(Math.min(2, left));
      if (left == 0) break;
      started[finished++].resolve([]);
    }
    await done;
  });

  it('starts once the browser is idle, unless saving data', async () => {
    const requestIdleCallback = vi.fn();
    vi.stubGlobal('requestIdleCallback', requestIdleCallback);
    schedulePagePreload();
    expect(requestIdleCallback).toHaveBeenCalledWith(expect.any(Function), {
      timeout: 3000,
    });
    expect(pageFetchers.experiments).not.toHaveBeenCalled();
    await requestIdleCallback.mock.calls[0][0]();
    expect(cachedPage('experiments').data).toEqual(['experiments']);

    requestIdleCallback.mockClear();
    vi.stubGlobal('navigator', { connection: { saveData: true } });
    schedulePagePreload();
    expect(requestIdleCallback).not.toHaveBeenCalled();
  });
});
