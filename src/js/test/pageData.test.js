// How each page's data is fetched, shared and preloaded (utils/pageData.js),
// with the server answered by a mocked axios.
import fc from 'fast-check';
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
  invalidatePages,
  isInvalidated,
  isLoadingPage,
  BACKGROUND_TIMEOUT_MS,
} from '@/utils/pageCache.js';
import {
  BACK_SPREAD_MS,
  forgetServer,
  FreshnessMeta,
  RESOURCES,
  setWsDown,
} from '@/utils/freshness.js';
import {
  DEFAULT_LOG_WINDOW,
  experimentKey,
  experimentList,
  fetchRunningExperiment,
  pageFetchers,
  preloadPages,
  schedulableHostNames,
  schedulePagePreload,
  scorchRow,
  scorchRunsKey,
  stoppedExperimentKey,
  withLogIds,
  withoutScreenshots,
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
    ['vmtiles', 'vms', { vms: [1] }],
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
    await pageFetchers.disks(undefined, undefined, { rescan: true });
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

  it('gives every entry of every batch its own id, keeping the rest of it', () => {
    const log = fc.record({
      time: fc.constant('2026-09-29T15:00:00Z'),
      msg: fc.string(),
    });
    fc.assert(
      fc.property(
        fc.array(fc.array(log, { maxLength: 5 }), { maxLength: 4 }),
        (batches) => {
          const keyed = batches.map(withLogIds);
          const ids = keyed.flat().map((entry) => entry.id);
          expect(ids.every(Number.isInteger)).toBe(true);
          expect(new Set(ids).size).toBe(ids.length);
          const kept = (log) => ({ ...log, id: expect.any(Number) });
          expect(keyed).toEqual(batches.map((b) => b.map(kept)));
        },
      ),
    );
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

  it('keeps the VM rows it last showed for that experiment, without screenshots', async () => {
    const rows = [{ name: 'vm1', running: true, screenshot: 'data:shot' }];
    cachePage(experimentKey('exp'), { name: 'exp', vms: rows });
    axiosInstance.get.mockResolvedValue({
      data: { name: 'exp', running: true },
    });

    expect((await fetchRunningExperiment('exp')).vms).toEqual([
      { name: 'vm1', running: true },
    ]);
    expect(rows[0].screenshot).toBe('data:shot');
    // another experiment's rows are not borrowed
    expect((await fetchRunningExperiment('other')).vms).toEqual([]);
  });

  it('copies any VM rows without their screenshots, leaving them as they were', () => {
    const row = fc.record(
      {
        name: fc.string(),
        running: fc.boolean(),
        screenshot: fc.oneof(fc.constant(''), fc.string()),
      },
      { requiredKeys: ['name'] },
    );
    fc.assert(
      fc.property(fc.array(row), (rows) => {
        const before = structuredClone(rows);
        const copies = withoutScreenshots(rows);

        expect(rows).toEqual(before);
        expect(copies).toHaveLength(rows.length);
        copies.forEach((copy, i) => {
          expect(copy).not.toBe(rows[i]);
          expect('screenshot' in copy).toBe(false);
          const { screenshot: _, ...rest } = rows[i];
          expect(copy).toEqual(rest);
        });
      }),
    );
    expect(withoutScreenshots(undefined)).toEqual([]);
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

  it('name every schedulable host of any list once, sorted', async () => {
    const host = fc.record({
      name: fc.string({ maxLength: 4 }),
      schedulable: fc.boolean(),
    });
    await fc.assert(
      fc.asyncProperty(
        fc.uniqueArray(host, { selector: (h) => h.name }),
        async (hosts) => {
          clearPageCache();
          cachePage('hosts', hosts);
          const names = await schedulableHostNames();
          const can = new Set(
            hosts.filter((h) => h.schedulable).map((h) => h.name),
          );
          expect(names).toHaveLength(can.size);
          expect(names.every((n) => can.has(n))).toBe(true);
          expect([...can].every((n) => names.includes(n))).toBe(true);
          expect(names.every((n, i) => i === 0 || names[i - 1] <= n)).toBe(
            true,
          );
        },
      ),
    );
  });

  it('fetch the hosts into the cache when no list is recent', async () => {
    vi.useFakeTimers();
    cachePage('hosts', [{ name: 'old', schedulable: true }]);
    vi.advanceTimersByTime(RESOURCES.hosts.reuseMs + 1);
    serve({ hosts: { hosts: [{ name: 'new', schedulable: true }] } });

    expect(await schedulableHostNames()).toEqual(['new']);
    expect(urls()).toEqual(['hosts']);
    expect(cachedPage('hosts').data).toEqual([
      { name: 'new', schedulable: true },
    ]);
  });
});

describe('live updates of cached pages', () => {
  const loadedAt = (seq) => Object.assign(new FreshnessMeta(), { seq });
  const experimentMsg = (seq) => ({
    resource: { type: 'experiment', name: 'a', action: 'stop' },
    seq,
  });

  it('mark a cached page changed only by a message newer than its load', () => {
    cachePage('experiments', [{ name: 'a' }], loadedAt(10));
    cachePage('hosts', [], loadedAt(10));

    invalidatePages(experimentMsg(9));
    expect(isInvalidated(cachedPage('experiments'))).toBe(false);

    invalidatePages(experimentMsg(11));
    expect(isInvalidated(cachedPage('experiments'))).toBe(true);
    // hosts are not changed by experiment messages
    expect(isInvalidated(cachedPage('hosts'))).toBe(false);

    // a load the server read before the change does not clear it
    cachePage('experiments', [{ name: 'a' }], loadedAt(10));
    expect(isInvalidated(cachedPage('experiments'))).toBe(true);
    cachePage('experiments', [{ name: 'a' }], loadedAt(12));
    expect(isInvalidated(cachedPage('experiments'))).toBe(false);
  });

  it('are loaded again rather than reused once changed', async () => {
    cachePage('experiments', [{ name: 'old' }], loadedAt(10));
    expect(await experimentList()).toEqual([{ name: 'old' }]);

    invalidatePages(experimentMsg(11));
    serve({ 'experiments?vms=false': { experiments: [{ name: 'new' }] } });
    expect(await experimentList()).toEqual([{ name: 'new' }]);
    expect(urls()).toEqual(['experiments?vms=false']);
  });

  const deleted = (name, seq) => ({
    resource: { type: 'experiment', name, action: 'delete' },
    ...(seq === undefined ? {} : { seq }),
  });

  it("drop a deleted experiment's pages, and no one else's", () => {
    const keys = [
      experimentKey('a'),
      stoppedExperimentKey('a'),
      scorchRunsKey('a'),
      experimentKey('ab'),
      stoppedExperimentKey('b'),
      scorchRunsKey('b'),
      'experiments',
      'scorch',
    ];
    for (const key of keys) cachePage(key, [key], loadedAt(10));

    invalidatePages(deleted('a', 11));

    expect(keys.filter((key) => cachedPage(key))).toEqual([
      experimentKey('ab'),
      stoppedExperimentKey('b'),
      scorchRunsKey('b'),
      'experiments',
      'scorch',
    ]);
    // the lists that named it are loaded again
    expect(isInvalidated(cachedPage('experiments'))).toBe(true);
    expect(isInvalidated(cachedPage(experimentKey('ab')))).toBe(false);
  });

  it('let a load running at the delete finish without caching what it read before', async () => {
    const answer = Promise.withResolvers();
    const fetch = (_, meta) => {
      meta.seq = 10;
      return answer.promise;
    };
    const request = fetchIntoCache(experimentKey('a'), fetch);

    invalidatePages(deleted('a', 11));
    // still shared with a page that opens now
    expect(isLoadingPage(experimentKey('a'))).toBe(true);
    const joined = fetchIntoCache(experimentKey('a'), fetch);
    expect(joined.promise).toBe(request.promise);

    answer.resolve({ name: 'a' });
    expect(await request.promise).toEqual({ name: 'a' });
    request.release();
    joined.release();
    expect(isLoadingPage(experimentKey('a'))).toBe(false);
    expect(cachedPage(experimentKey('a'))).toBeUndefined();

    // an experiment of that name created since is cached again
    cachePage(experimentKey('a'), { name: 'a' }, loadedAt(12));
    expect(cachedPage(experimentKey('a')).data).toEqual({ name: 'a' });
  });

  it("keep the SCORCH list from putting back a deleted experiment's runs", async () => {
    const pipelines = Promise.withResolvers();
    axiosInstance.get.mockImplementation(async (url, { meta }) => {
      meta.seq = 10;
      if (url === 'experiments?vms=false') {
        return { data: { experiments: [{ name: 'a', apps: ['scorch'] }] } };
      }
      return { data: await pipelines.promise };
    });

    const request = fetchIntoCache('scorch', pageFetchers.scorch);
    await flush();
    invalidatePages(deleted('a', 11));
    pipelines.resolve({ pipelines: [] });
    await request.promise;
    request.release();

    expect(cachedPage(scorchRunsKey('a'))).toBeUndefined();
  });

  it('without seqs, cache nothing for a deleted experiment until loads outlast the delete', () => {
    vi.useFakeTimers();
    cachePage(experimentKey('a'), { name: 'a' });

    invalidatePages(deleted('a'));
    expect(cachedPage(experimentKey('a'))).toBeUndefined();
    cachePage(experimentKey('a'), { name: 'a' });
    expect(cachedPage(experimentKey('a'))).toBeUndefined();
    // another experiment is not held back
    cachePage(experimentKey('b'), { name: 'b' });
    expect(cachedPage(experimentKey('b'))).toBeDefined();

    // no load runs this long while a page waits on it
    vi.advanceTimersByTime(BACKGROUND_TIMEOUT_MS);
    cachePage(experimentKey('a'), { name: 'a' });
    expect(cachedPage(experimentKey('a'))).toBeDefined();
  });

  it('keep only the pages loaded after every delete of their experiment, whatever the order', () => {
    const name = fc.constantFrom('a', 'b');
    const keysOf = (n) => [
      experimentKey(n),
      stoppedExperimentKey(n),
      scorchRunsKey(n),
    ];
    const step = fc.oneof(
      fc.record({
        name,
        write: fc.integer({ min: 0, max: 2 }),
        seq: fc.nat(30),
      }),
      fc.record({ name, seq: fc.nat(30) }),
    );
    fc.assert(
      fc.property(fc.array(step, { maxLength: 20 }), (steps) => {
        clearPageCache();
        // what each key should hold: the seq of the load cached for it
        const expected = new Map();
        const lastDelete = new Map();
        for (const { name: n, write, seq } of steps) {
          if (write !== undefined) {
            const key = keysOf(n)[write];
            cachePage(key, key, loadedAt(seq));
            if (seq > (lastDelete.get(n) ?? -Infinity)) expected.set(key, seq);
          } else {
            invalidatePages(deleted(n, seq));
            lastDelete.set(n, Math.max(lastDelete.get(n) ?? -Infinity, seq));
            for (const key of keysOf(n)) expected.delete(key);
          }
        }
        for (const key of [...keysOf('a'), ...keysOf('b')]) {
          expect(cachedPage(key)?.meta.seq).toBe(expected.get(key));
        }
      }),
    );
  });

  it("pass each request the load's freshness, which a composite load folds in", async () => {
    axiosInstance.get.mockImplementation(async (url, { meta }) => {
      // as the axios interceptor fills it in
      meta.seq = Math.min(
        meta.seq ?? Infinity,
        url === 'experiments?vms=false' ? 7 : 9,
      );
      return {
        data:
          url === 'experiments?vms=false'
            ? { experiments: [{ name: 'a', apps: ['scorch'] }] }
            : { pipelines: [] },
      };
    });

    const request = fetchIntoCache('scorch', pageFetchers.scorch);
    await request.promise;
    request.release();
    // never newer than the experiment list it is built on
    expect(cachedPage('scorch').meta.seq).toBe(7);
    expect(cachedPage(scorchRunsKey('a')).meta.seq).toBe(9);
  });
});

describe('the SCORCH page', () => {
  it('asks only SCORCH experiments for their pipelines', async () => {
    cachePage('experiments', [
      { name: 'a', apps: ['scorch'] },
      { name: 'b', apps: ['ntp'] },
      { name: 'c', apps: ['scorch', 'soh'] },
      { name: 'd', apps: [] },
      { name: 'e' },
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

  it('lists the SCORCH experiments the role may get in list order, whatever order their answers come in', async () => {
    const exps = fc.uniqueArray(
      fc.record({
        name: fc.stringMatching(/^[a-z][a-z0-9]{0,3}$/),
        apps: fc.subarray(['scorch', 'ntp', 'soh']),
      }),
      { selector: (exp) => exp.name, maxLength: 6 },
    );
    await fc.assert(
      fc.asyncProperty(
        exps.chain((list) =>
          fc.tuple(fc.constant(list), fc.subarray(list.map((e) => e.name))),
        ),
        fc.scheduler(),
        async ([list, denied], s) => {
          clearPageCache();
          cachePage('experiments', list);
          roleAllowed.mockImplementation(
            (...args) => !denied.includes(args[2]),
          );
          const answers = {};
          axiosInstance.get.mockImplementation((url) => {
            const name = url.split('/')[1];
            answers[name] = {
              pipelines: [{ name: `${name}-run` }],
              running: 1,
            };
            return s.schedule(Promise.resolve({ data: answers[name] }), name);
          });

          const rows = await s.waitFor(pageFetchers.scorch(undefined));

          const listed = list.filter(
            (e) => e.apps.includes('scorch') && !denied.includes(e.name),
          );
          expect(rows.map((row) => row.name)).toEqual(
            listed.map((e) => e.name),
          );
          for (const row of rows) {
            expect(row.scorch.runs).toEqual([`${row.name}-run`]);
            expect(cachedPage(scorchRunsKey(row.name)).data).toBe(
              answers[row.name],
            );
          }
          expect(Object.keys(answers).sort()).toEqual(
            listed.map((e) => e.name).sort(),
          );
        },
      ),
    );
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

  it('never has more than two requests out, and loads each tab once, whatever answers first or fails', async () => {
    const tabs = Object.keys(pageFetchers).filter((key) => key !== 'vmtiles');
    await fc.assert(
      fc.asyncProperty(
        fc.subarray(tabs),
        fc.scheduler(),
        async (failing, s) => {
          clearPageCache();
          let out = 0;
          let most = 0;
          for (const key of tabs) {
            pageFetchers[key].mockReset().mockImplementation(() => {
              most = Math.max(most, ++out);
              return s.schedule(Promise.resolve(), key).then(() => {
                out--;
                if (failing.includes(key)) throw new Error('down');
                return [key];
              });
            });
          }

          await s.waitFor(preloadPages());

          expect(most).toBe(2);
          for (const key of tabs) {
            expect(pageFetchers[key]).toHaveBeenCalledOnce();
            expect(cachedPage(key)?.data).toEqual(
              failing.includes(key) ? undefined : [key],
            );
          }
        },
      ),
    );
  });

  it('waits while the server shuts down, and goes on once it is back', async () => {
    vi.useFakeTimers();
    try {
      setWsDown(true, 'shutdown');
      const done = preloadPages();
      await vi.advanceTimersByTimeAsync(30_000);
      for (const key of Object.keys(pageFetchers)) {
        expect(pageFetchers[key]).not.toHaveBeenCalled();
      }

      setWsDown(false);
      await vi.advanceTimersByTimeAsync(BACK_SPREAD_MS);
      await done;
      expect(cachedPage('experiments').data).toEqual(['experiments']);
      expect(cachedPage('disks').data).toEqual(['disks']);
    } finally {
      forgetServer();
      vi.useRealTimers();
    }
  });

  it.each([
    [
      'the server does not answer',
      { code: 'ERR_NETWORK', request: {}, message: 'Network Error' },
    ],
    [
      'a proxy cannot reach the server',
      { response: { status: 502, headers: {} }, message: 'Bad Gateway' },
    ],
  ])('stops once %s, leaving the pages to load when opened', async (_, err) => {
    for (const key of Object.keys(pageFetchers)) {
      pageFetchers[key].mockRejectedValue(
        Object.assign(new Error(err.message), err),
      );
    }
    await preloadPages();

    const asked = Object.keys(pageFetchers).filter(
      (key) => pageFetchers[key].mock.calls.length > 0,
    );
    // one request per worker
    expect(asked).toEqual(['experiments', 'configs']);
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
