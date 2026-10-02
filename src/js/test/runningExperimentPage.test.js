// The running experiment page: how it shows the experiment it loads and lists
// its VMs over the websocket, what it asks for again after the websocket
// reconnects, and what it does with websocket traffic that is not its own.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/rbac.js', () => ({ roleAllowed: () => true }));
vi.mock('@/utils/axios.js', () => ({
  default: { get: vi.fn(() => Promise.reject(new Error('no netflow'))) },
}));
vi.mock('@/store', () => ({
  usePhenixStore: () => ({ token: 't', features: [], role: null }),
}));

const ws = vi.hoisted(() => ({ sent: [], reconnect: [] }));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
  onWsReconnect: (f) => {
    ws.reconnect.push(f);
    return () => {};
  },
  sendWsMsg: (msg) => ws.sent.push(msg),
}));

// the options the page gives its loader, and the loader's load()
const loader = vi.hoisted(() => ({ options: null, load: null }));
vi.mock('@/utils/pageLoader.js', () => ({
  createPageLoader: (options) => {
    loader.options = options;
    return { start: () => {}, stop: () => {}, load: loader.load };
  },
  loadingText: (what) => `Loading ${what}…`,
}));

import { cachedPage, clearPageCache } from '@/utils/pageCache.js';
import { experimentKey } from '@/utils/pageData.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { makeContext } from './helpers/context.js';
import { stubBrowser } from './helpers/render.js';

// the page on exp1, created as it is when opened
async function openPage(fields = {}) {
  const page = makeContext(RunningExperiment, {
    $route: { params: { id: 'exp1' } },
    $router: { replace: vi.fn() },
    $buefy: { toast: { open: vi.fn() } },
    $emit: vi.fn(),
    ...fields,
  });
  await RunningExperiment.created.call(page);
  return page;
}

const running = (vms) => ({ name: 'exp1', running: true, vm_count: 2, vms });

// the VM list requests and screenshot sizes the page sent
const vmLists = () =>
  ws.sent.filter((msg) => msg.resource.type == 'experiment/vms');
const screenshotSizes = () =>
  ws.sent
    .filter((msg) => msg.resource.type == 'metadata/screenshot')
    .map((msg) => msg.request.size);

// the server's answer to a VM list request
const vmList = (name, vms) => ({
  resource: { type: 'experiment/vms', name, action: 'list' },
  result: { vms, total: vms.length },
});

beforeEach(() => {
  stubBrowser();
  // the timer that gives up waiting for screenshots
  vi.useFakeTimers();
  ws.sent.length = 0;
  ws.reconnect.length = 0;
  loader.load = vi.fn();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  clearPageCache();
});

describe('loading', () => {
  it('lists the VMs once for the cached and the fresh experiment', async () => {
    const page = await openPage();

    // each list has the server ask minimega for every VM
    loader.options.apply(running([{ name: 'vm1' }]));
    loader.options.apply(running([{ name: 'vm1' }]));

    expect(vmLists()).toHaveLength(1);
    expect(page.experiment.vms.map((vm) => vm.name)).toEqual(['vm1']);
    expect(page.table.total).toBe(2);
  });

  it('lists the VMs again when the header refreshes the page', async () => {
    await openPage();
    loader.options.apply(running([]));

    loader.options.refresh();
    expect(vmLists()).toHaveLength(2);
    expect(loader.load).toHaveBeenCalled();
  });

  it('keeps the websocket rows once they have arrived', async () => {
    const page = await openPage();
    page.handleWs(vmList('exp1', [{ name: 'live' }]));

    loader.options.apply(running([]));
    expect(page.experiment.vms).toEqual([{ name: 'live' }]);
  });

  it('says the VMs are loading until the websocket lists them', async () => {
    const page = await openPage();
    loader.options.apply(running([]));
    expect(page.vmsEmptyText).toBe('Loading VMs…');

    page.handleWs(vmList('exp1', []));
    expect(page.vmsEmptyText).toBe('This experiment has no VMs');
    page.search.filter = 'web';
    expect(page.vmsEmptyText).toBe('No VMs match your search');
  });

  it('hands over to the stopped view when the experiment has stopped', async () => {
    const page = await openPage();
    loader.options.apply({ name: 'exp1', running: false });

    expect(page.$emit).toHaveBeenCalledWith('running', false);
    expect(vmLists()).toHaveLength(0);
  });
});

describe('after the websocket reconnects', () => {
  it.each([
    [
      'the zoomed screenshot size and the VM list',
      { activeTab: 2, vncZoom: 8 },
      ['400'],
    ],
    ['only the VM list at the default screenshot size', {}, []],
  ])('asks again for %s', async (_, fields, sizes) => {
    await openPage(fields);
    loader.options.apply(running([]));
    ws.sent.length = 0;

    ws.reconnect.forEach((f) => f());
    expect(screenshotSizes()).toEqual(sizes);
    expect(vmLists()).toHaveLength(1);
  });

  it('lists no VMs before the experiment has loaded', async () => {
    await openPage();

    ws.reconnect.forEach((f) => f());
    expect(vmLists()).toHaveLength(0);
  });
});

describe('websocket traffic', () => {
  it("lists only its own experiment's VMs, and caches the list", async () => {
    const page = await openPage();
    loader.options.apply(running([{ name: 'vm1' }]));

    page.handleWs(vmList('exp2', [{ name: 'other-vm' }]));
    expect(page.experiment.vms.map((vm) => vm.name)).toEqual(['vm1']);
    expect(page.vmsLoaded).toBe(false);

    page.handleWs(vmList('exp1', [{ name: 'web' }]));
    expect(page.experiment.vms.map((vm) => vm.name)).toEqual(['web']);
    expect(page.vmsLoaded).toBe(true);
    expect(
      cachedPage(experimentKey('exp1')).data.vms.map((vm) => vm.name),
    ).toEqual(['web']);
  });

  it.each([
    ['stopped', 'stop'],
    ['deleted', 'delete'],
  ])(
    'leaves the page when its experiment is %s elsewhere',
    async (_, action) => {
      const page = await openPage();
      const message = (name, act) => ({
        resource: { type: 'experiment', name, action: act },
      });

      // another experiment, or a message that does not end this one
      page.handleWs(message('exp2', action));
      page.handleWs(message('exp1', 'stopping'));
      expect(page.$router.replace).not.toHaveBeenCalled();

      page.handleWs(message('exp1', action));
      expect(page.$router.replace).toHaveBeenCalledWith('/experiments/');
      expect(page.$buefy.toast.open).toHaveBeenCalledTimes(1);
    },
  );

  it('leaves its own Stop to navigate', async () => {
    const page = await openPage();
    page.isWaiting = true;

    page.handleWs({
      resource: { type: 'experiment', name: 'exp1', action: 'stop' },
    });
    expect(page.$router.replace).not.toHaveBeenCalled();
    expect(page.$buefy.toast.open).not.toHaveBeenCalled();
  });
});
