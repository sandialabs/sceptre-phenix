// The pages that poll the server on a timer, the hosts and the VM tiles with
// their screenshots, and how they hold back while the server is away.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { capturing, loaderSpy, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
// the pages' loads are recorded
vi.mock('@/utils/pageLoader.js', capturing);

import Hosts from '@/views/Hosts.vue';
import VMtilesView from '@/views/experiment/VMtilesView.vue';
import {
  BACK_SPREAD_MS,
  forgetServer,
  RESOURCES,
  serverUnavailable,
  setWsDown,
} from '@/utils/freshness.js';
import { mountComponent } from './helpers/mount.js';

beforeEach(() => {
  resetMocks();
  vi.useFakeTimers();
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
});

afterEach(() => {
  forgetServer();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe.each([
  ['the hosts', Hosts, '/hosts', RESOURCES.hosts.pollMs],
  ['the VM tiles', VMtilesView, '/vmtiles', RESOURCES.vmtiles.pollMs],
])('polling %s', (_, page, route, pollMs) => {
  const open = () => mountComponent(page, { route });
  const loads = () => loaderSpy.load.mock.calls.length;

  it('reloads at its rate while in view, as a poll', async () => {
    await open();
    vi.advanceTimersByTime(3 * pollMs);
    expect(loaderSpy.load.mock.calls).toEqual([
      [{ poll: true }],
      [{ poll: true }],
      [{ poll: true }],
    ]);
  });

  it('asks nothing while its tab is hidden, and resumes when shown', async () => {
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    await open();
    vi.advanceTimersByTime(3 * pollMs);
    expect(loads()).toBe(0);

    hidden.mockReturnValue(false);
    vi.advanceTimersByTime(pollMs);
    expect(loads()).toBe(1);
  });

  it('waits out the Retry-After of a refused request', async () => {
    await open();
    serverUnavailable('300');
    vi.advanceTimersByTime(300_000 - 1);
    expect(loads()).toBe(0);
    vi.advanceTimersByTime(1);
    expect(loads()).toBe(1);
  });

  it('stops polling once the page is left', async () => {
    const wrapper = await open();
    wrapper.unmount();
    vi.advanceTimersByTime(3 * pollMs);
    setWsDown(true, 'shutdown');
    setWsDown(false);
    vi.advanceTimersByTime(BACK_SPREAD_MS);
    expect(loads()).toBe(0);
  });
});
