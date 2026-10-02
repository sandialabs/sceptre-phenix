import {
  createPageLoader,
  loadingText,
  pageStatus,
  stillLoading,
} from '@/utils/pageLoader.js';
import {
  BACKGROUND_TIMEOUT_MS,
  cachedPage,
  cachePage,
  clearPageCache,
  isLoadingPage,
} from '@/utils/pageCache.js';
import { useErrorNotification } from '@/utils/errorNotif.js';
import { usePhenixStore } from '@/store.js';
import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, test, expect, vi } from 'vitest';
import { memoryStorage } from './helpers/storage.js';

vi.mock('@/utils/errorNotif.js', () => ({ useErrorNotification: vi.fn() }));
vi.mock('@/router', () => ({ default: { replace: vi.fn() } }));

// the store reads the saved login from web storage when it is created
vi.stubGlobal('localStorage', memoryStorage());
vi.stubGlobal('sessionStorage', memoryStorage());

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
