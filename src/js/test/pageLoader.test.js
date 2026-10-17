import {
  createPageLoader,
  currentStaleReason,
  loadingText,
  pageStatus,
  stillLoading,
} from '@/utils/pageLoader.js';
import {
  BACKGROUND_TIMEOUT_MS,
  cachedPage,
  cachePage,
  clearPageCache,
  fetchIntoCache,
  invalidatePages,
  isInvalidated,
  isLoadingPage,
} from '@/utils/pageCache.js';
import { FreshnessMeta, setWsDown } from '@/utils/freshness.js';
import { useErrorNotification } from '@/utils/errorNotif.js';
import { usePhenixStore } from '@/store.js';
import { createPinia, setActivePinia } from 'pinia';
import fc from 'fast-check';
import { beforeEach, test, expect, vi } from 'vitest';
import { flush } from './helpers/async.js';

vi.mock('@/utils/errorNotif.js', () => ({ useErrorNotification: vi.fn() }));
vi.mock('@/router', () => ({ default: { replace: vi.fn() } }));

beforeEach(() => {
  clearPageCache();
  useErrorNotification.mockClear();
});

// a fetch the test resolves by hand, recording the signal it was given
function manualFetch() {
  const calls = [];
  const fetch = (signal) =>
    new Promise((resolve) => calls.push({ signal, resolve }));
  return { fetch, calls };
}

test('shows cached data at once and replaces it when fresh data arrives', async () => {
  cachePage('hosts', ['cached']);
  const { fetch, calls } = manualFetch();
  const apply = vi.fn();
  const loader = createPageLoader({ key: 'hosts', fetch, apply });

  const done = loader.start();
  expect(apply).toHaveBeenCalledWith(['cached'], expect.anything());
  expect(pageStatus.loading).toBe(true);
  expect(pageStatus.updatedAt).not.toBeNull();

  calls[0].resolve(['fresh']);
  await done;
  expect(apply).toHaveBeenLastCalledWith(['fresh'], expect.anything());
  expect(pageStatus.loading).toBe(false);
  loader.stop();
});

test("the header's refresh button loads the page again", async () => {
  const { fetch, calls } = manualFetch();
  const apply = vi.fn();
  const loader = createPageLoader({ key: 'hosts', fetch, apply });
  const first = loader.start();
  calls[0].resolve(['h1']);
  await first;

  const again = pageStatus.refresh();
  expect(calls).toHaveLength(2);
  expect(pageStatus.loading).toBe(true);
  calls[1].resolve(['h2']);
  expect(await again).toBe(true);
  expect(apply).toHaveBeenLastCalledWith(['h2'], expect.anything());
  loader.stop();

  // a page that also reloads secondary data gives its own action
  const refresh = vi.fn();
  const page = createPageLoader({ fetch, apply, refresh });
  page.start();
  pageStatus.refresh();
  expect(refresh).toHaveBeenCalledTimes(1);
  page.stop();
});

test('a failed load is reported, and a table waiting on it says so', async () => {
  const error = new Error('unreachable');
  const fetch = vi
    .fn()
    .mockRejectedValueOnce(error)
    .mockResolvedValueOnce(['h1']);
  const loader = createPageLoader({ key: 'hosts', fetch, apply: () => {} });

  expect(await loader.start()).toBe(false);
  expect(useErrorNotification).toHaveBeenCalledWith(error);
  expect(pageStatus).toMatchObject({ loading: false, failed: true });
  expect(loadingText('hosts')).toBe('Could not load hosts');
  expect(cachedPage('hosts')).toBeUndefined();

  expect(await pageStatus.refresh()).toBe(true);
  expect(pageStatus.failed).toBe(false);
  expect(loadingText('hosts')).toBe('Loading hosts…');
  loader.stop();
});

test('leaving a page lets its first load finish into the cache', async () => {
  const { fetch, calls } = manualFetch();
  const apply = vi.fn();
  const loader = createPageLoader({ key: 'disks', fetch, apply });

  const done = loader.start();
  expect(pageStatus.refresh).not.toBeNull();
  loader.stop();
  expect(calls[0].signal.aborted).toBe(false);
  expect(pageStatus.refresh).toBeNull();

  calls[0].resolve(['late']);
  expect(await done).toBe(false);
  expect(apply).not.toHaveBeenCalled();
  expect(cachedPage('disks').data).toEqual(['late']);
});

test('revisiting a page joins its background load', async () => {
  const { fetch, calls } = manualFetch();
  const first = createPageLoader({ key: 'hosts', fetch, apply: () => {} });
  first.start();
  first.stop();

  const apply = vi.fn();
  const again = createPageLoader({ key: 'hosts', fetch, apply });
  const done = again.start();
  expect(calls).toHaveLength(1);

  calls[0].resolve(['h1']);
  expect(await done).toBe(true);
  expect(apply).toHaveBeenCalledWith(['h1'], expect.anything());
  again.stop();
});

test('leaving an uncached page cancels its request', () => {
  const { fetch, calls } = manualFetch();
  const loader = createPageLoader({ fetch, apply: () => {} });

  loader.start();
  loader.stop();
  expect(calls[0].signal.aborted).toBe(true);
});

test('a background load is abandoned after the time limit', () => {
  vi.useFakeTimers();
  try {
    const { fetch, calls } = manualFetch();
    const loader = createPageLoader({ key: 'logs', fetch, apply: () => {} });
    loader.start();
    loader.stop();

    vi.advanceTimersByTime(BACKGROUND_TIMEOUT_MS - 1);
    expect(calls[0].signal.aborted).toBe(false);
    vi.advanceTimersByTime(1);
    expect(calls[0].signal.aborted).toBe(true);
  } finally {
    vi.useRealTimers();
  }
});

test('an open page is never cut off by the background time limit', () => {
  vi.useFakeTimers();
  try {
    const { fetch, calls } = manualFetch();
    const loader = createPageLoader({ key: 'scorch', fetch, apply: () => {} });
    loader.start();

    vi.advanceTimersByTime(BACKGROUND_TIMEOUT_MS * 2);
    expect(calls[0].signal.aborted).toBe(false);
    loader.stop(); // left after the limit: given up at once
    expect(calls[0].signal.aborted).toBe(true);
  } finally {
    vi.useRealTimers();
  }
});

test('a newer load supersedes an older one', async () => {
  const { fetch, calls } = manualFetch();
  const apply = vi.fn();
  const loader = createPageLoader({ fetch, apply });

  const first = loader.start();
  const second = loader.load();
  expect(calls[0].signal.aborted).toBe(true);

  calls[1].resolve('new');
  calls[0].resolve('old');
  await Promise.all([first, second]);
  expect(apply).toHaveBeenCalledTimes(1);
  expect(apply).toHaveBeenCalledWith('new', expect.anything());
  loader.stop();
});

test('forced loads during a slow request queue one follow-up and do not abort it', async () => {
  const { fetch, calls } = manualFetch();
  const apply = vi.fn();
  const loader = createPageLoader({ key: 'disks', fetch, apply });

  const first = loader.start();
  loader.load({ force: true });
  loader.load({ force: true });
  const forced = loader.load({ force: true });
  expect(calls).toHaveLength(1);
  expect(calls[0].signal.aborted).toBe(false);

  calls[0].resolve(['old']);
  await flush();
  expect(calls).toHaveLength(2);
  // known to predate the change: kept, but marked out of date
  expect(cachedPage('disks').data).toEqual(['old']);
  expect(isInvalidated(cachedPage('disks'))).toBe(true);

  calls[1].resolve(['new']);
  expect(await forced).toBe(true);
  expect(await first).toBe(false);
  expect(calls).toHaveLength(2);
  expect(apply).toHaveBeenCalledTimes(1);
  expect(apply).toHaveBeenCalledWith(['new'], expect.anything());
  expect(cachedPage('disks').data).toEqual(['new']);
  expect(isInvalidated(cachedPage('disks'))).toBe(false);
  loader.stop();
});

test('a stream of forced loads still lands data, newest last', async () => {
  const { fetch, calls } = manualFetch();
  const apply = vi.fn();
  const loader = createPageLoader({ key: 'disks', fetch, apply });

  loader.start();
  let last;
  for (let i = 0; i < 6; i++) {
    last = loader.load({ force: true });
    calls[i].resolve([i]);
    await flush();
    expect(calls).toHaveLength(i + 2);
  }
  calls[6].resolve([6]);
  expect(await last).toBe(true);

  expect(calls.some((call) => call.signal.aborted)).toBe(false);
  expect(apply.mock.calls.map(([data]) => data[0])).toEqual([1, 3, 5, 6]);
  expect(cachedPage('disks').data).toEqual([6]);
  loader.stop();
});

test('applies only the newest load, whatever order the answers come in', async () => {
  const loads = fc.integer({ min: 1, max: 6 });
  const order = (n) =>
    fc.shuffledSubarray([...Array(n).keys()], { minLength: n, maxLength: n });
  await fc.assert(
    fc.asyncProperty(loads.chain(order), async (answerOrder) => {
      const { fetch, calls } = manualFetch();
      const apply = vi.fn();
      const loader = createPageLoader({ fetch, apply });
      const done = [loader.start()];
      while (done.length < answerOrder.length) done.push(loader.load());

      for (const i of answerOrder) calls[i].resolve(i);
      const newest = answerOrder.length - 1;
      expect(await Promise.all(done)).toEqual(done.map((_, i) => i === newest));
      expect(apply.mock.calls.map(([data]) => data)).toEqual([newest]);
      loader.stop();
    }),
  );
});

test('a cached page lands newer data only, ending on the last request, however loads and answers interleave', async () => {
  const step = fc.constantFrom('load', 'force', 'answer');
  await fc.assert(
    fc.asyncProperty(fc.array(step, { maxLength: 10 }), async (steps) => {
      clearPageCache();
      const { fetch, calls } = manualFetch();
      const apply = vi.fn();
      const loader = createPageLoader({ key: 'disks', fetch, apply });
      const done = [loader.start()];
      let answered = 0;
      const answer = async () => {
        calls[answered].resolve([answered++]);
        await flush();
      };
      for (const s of steps) {
        if (s !== 'answer') done.push(loader.load({ force: s === 'force' }));
        else if (answered < calls.length) await answer();
      }
      while (answered < calls.length) await answer();
      await Promise.all(done);

      const landed = apply.mock.calls.map(([data]) => data[0]);
      expect(landed).toEqual([...new Set(landed)].sort((a, b) => a - b));
      expect(landed.at(-1)).toBe(calls.length - 1);
      expect(cachedPage('disks').data).toEqual([calls.length - 1]);
      expect(pageStatus.loading).toBe(false);
      loader.stop();
    }),
    { numRuns: 40 },
  );
});

test('a forced request starts once the one it replaces settles, so the older never lands last', async () => {
  const { fetch, calls } = manualFetch();
  const older = fetchIntoCache('disks', fetch);
  const newer = fetchIntoCache('disks', fetch, { force: true });
  const joined = fetchIntoCache('disks', fetch, { force: true });
  const plain = fetchIntoCache('disks', fetch);
  expect(joined.promise).toBe(newer.promise);
  expect(plain.promise).toBe(newer.promise);
  expect(newer.startedAt).toBeUndefined();

  // nobody waits on the older one any more, but the newer has not run yet
  older.release();
  expect(calls).toHaveLength(1);
  expect(calls[0].signal.aborted).toBe(false);

  calls[0].resolve(['old']);
  expect(await older.promise).toEqual(['old']);
  await flush();
  expect(calls).toHaveLength(2);
  expect(newer.startedAt).toBeDefined();

  calls[1].resolve(['new']);
  expect(await newer.promise).toEqual(['new']);
  expect(cachedPage('disks').data).toEqual(['new']);
  for (const request of [newer, joined, plain]) request.release();
});

test('clearing the cache cancels a request a forced load replaced', async () => {
  const { fetch, calls } = manualFetch();
  const older = fetchIntoCache('disks', fetch);
  const newer = fetchIntoCache('disks', fetch, { force: true });

  clearPageCache();
  expect(calls[0].signal.aborted).toBe(true);
  calls[0].resolve(['userA']);
  await older.promise;
  await newer.promise.catch(() => {});
  expect(calls).toHaveLength(1);
  expect(cachedPage('disks')).toBeUndefined();
  older.release();
  newer.release();
});

test("a page left behind does not clear the next page's header", () => {
  const { fetch } = manualFetch();
  const previous = createPageLoader({ fetch, apply: () => {} });
  const next = createPageLoader({ fetch, apply: () => {} });

  previous.start();
  next.start();
  previous.stop();
  expect(pageStatus.refresh).not.toBeNull();
  next.stop();
});

test('an empty table says it is loading until a load comes back empty', async () => {
  expect(stillLoading(false, [])).toBe(true);

  // an empty cached copy is shown while this visit's own load runs
  cachePage('soh', []);
  const { fetch, calls } = manualFetch();
  const loader = createPageLoader({ key: 'soh', fetch, apply: () => {} });
  const done = loader.start();
  expect(stillLoading(true, [])).toBe(true);
  expect(stillLoading(true, [{ name: 'a' }])).toBe(false);

  // still loading even if the header lost track of the request
  pageStatus.loading = false;
  expect(stillLoading(true, [])).toBe(true);

  calls[0].resolve([]);
  await done;
  expect(stillLoading(true, [])).toBe(false);

  // the next page starts over
  loader.stop();
  expect(stillLoading(true, [])).toBe(true);
});

test('signing out drops cached pages and cancels their background loads', async () => {
  setActivePinia(createPinia());
  cachePage('hosts', ['host1']);
  const { fetch, calls } = manualFetch();
  const left = createPageLoader({ key: 'disks', fetch, apply: () => {} });
  const done = left.start();
  left.stop();
  expect(isLoadingPage('disks')).toBe(true);

  usePhenixStore().logout();
  expect(calls[0].signal.aborted).toBe(true);
  expect(isLoadingPage('disks')).toBe(false);

  // an answer that comes after signing out is not kept
  calls[0].resolve(['late']);
  expect(await done).toBe(false);
  expect(cachedPage('disks')).toBeUndefined();

  // the next user's first visit shows only what it loads itself
  const apply = vi.fn();
  const next = createPageLoader({ key: 'hosts', fetch, apply });
  const loaded = next.start();
  expect(apply).not.toHaveBeenCalled();
  calls[1].resolve(['host2']);
  await loaded;
  expect(apply.mock.calls).toEqual([[['host2'], expect.anything()]]);
  next.stop();
});

// a FreshnessMeta as a load's responses fill it in
const loadMeta = (fields) => Object.assign(new FreshnessMeta(), fields);

test("a page is told the cached and the fresh data's seq", async () => {
  cachePage('hosts', ['cached'], loadMeta({ seq: 5, observedAt: 1000 }));
  const apply = vi.fn();
  const loader = createPageLoader({
    key: 'hosts',
    fetch: async (_, meta) => {
      Object.assign(meta, { seq: 9, observedAt: 2000, stale: true });
      return ['fresh'];
    },
    apply,
  });

  const done = loader.start();
  expect(apply).toHaveBeenCalledWith(['cached'], { seq: 5 });
  expect(pageStatus).toMatchObject({ observedAt: 1000, staleReason: null });

  await done;
  expect(apply).toHaveBeenLastCalledWith(['fresh'], { seq: 9 });
  // the server served earlier data
  expect(pageStatus).toMatchObject({
    observedAt: 2000,
    staleReason: 'server',
  });
  loader.stop();
  expect(pageStatus).toMatchObject({ observedAt: null, staleReason: null });
});

test('a cached page changed by a live update shows as out of date until it reloads', async () => {
  cachePage('experiments', ['cached'], loadMeta({ seq: 5, observedAt: 1000 }));
  invalidatePages({
    resource: { type: 'experiment', name: 'a', action: 'stop' },
    seq: 6,
  });

  const { fetch, calls } = manualFetch();
  const loader = createPageLoader({
    key: 'experiments',
    fetch,
    apply: () => {},
  });
  const done = loader.start();
  expect(pageStatus.staleReason).toBe('invalidated');

  calls[0].resolve(['fresh']);
  await done;
  expect(pageStatus.staleReason).toBe(null);
  loader.stop();
});

test('a live page shows as out of date while live updates are down', async () => {
  // the Experiments page is kept current by live updates
  const live = createPageLoader({
    key: 'experiments',
    fetch: async () => [],
    apply: () => {},
  });
  await live.start();
  setWsDown(true);
  try {
    expect(currentStaleReason()).toBe('disconnected');
  } finally {
    setWsDown(false);
  }
  expect(currentStaleReason()).toBeNull();
  live.stop();
});
