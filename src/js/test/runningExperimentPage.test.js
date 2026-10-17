// The running experiment page: the experiment it loads, its VMs over the
// websocket, what it reloads on reconnect, and traffic that is not its own.
import { flushPromises, RouterLinkStub } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, resetMocks, ws } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/notify.js', modules.notify);
// the options the page gives its loader, and the loader's load()
vi.mock('@/utils/pageLoader.js', capturing);

import { cachedPage, clearPageCache } from '@/utils/pageCache.js';
import { experimentKey } from '@/utils/pageData.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { wsMsg } from './helpers/fixtures.js';
import { mountPage, tableRows, textOf, toasts } from './helpers/mount.js';

// the page on exp1, with this state; its links do not resolve once it leaves
const openPage = (data) =>
  mountPage(RunningExperiment, {
    route: '/experiment/exp1',
    data,
    stubs: { RouterLink: RouterLinkStub },
  });

const running = (vms) => ({ name: 'exp1', running: true, vm_count: 2, vms });

// the VM list requests and screenshot sizes the page sent
const vmLists = () =>
  ws.sent.filter((msg) => msg.resource.type == 'experiment/vms');
const screenshotSizes = () =>
  ws.sent
    .filter((msg) => msg.resource.type == 'metadata/screenshot')
    .map((msg) => msg.request.size);

// the server's answer to a VM list request
const vmList = (name, vms) =>
  wsMsg('experiment/vms', name, 'list', { vms, total: vms.length });

// gives the page the experiment its loader fetched, and waits for it to draw
async function apply(experiment) {
  loaderSpy.options.apply(experiment);
  await flushPromises();
}

// passes a websocket message to the page, and waits for it to draw
async function handle(msg) {
  for (const handler of ws.handlers) handler(msg);
  await flushPromises();
}

// the name in each row of the VM table
const names = (page) =>
  tableRows(page).map((cells) => textOf(cells.Node.find('.tag')));

// what the VM table says when it has no rows
const emptyText = (page) => textOf(page.find('.table tbody'));

beforeEach(() => {
  setActivePinia(createPinia());
  // the timer that gives up waiting for screenshots
  vi.useFakeTimers();
  resetMocks({ store: { token: 't', features: [], role: null } });
  axios.get.mockRejectedValue(new Error('no netflow'));
});

afterEach(() => {
  vi.useRealTimers();
  clearPageCache();
});

describe('loading', () => {
  it('lists the VMs once for the cached and the fresh experiment', async () => {
    const page = await openPage();

    // each list has the server ask minimega for every VM
    await apply(running([{ name: 'vm1' }]));
    await apply(running([{ name: 'vm1' }]));

    expect(vmLists()).toHaveLength(1);
    expect(names(page)).toEqual(['vm1']);
    expect(page.vm.table.total).toBe(2);
  });

  it('lists the VMs again when the header refreshes the page', async () => {
    await openPage();
    await apply(running([]));

    loaderSpy.options.refresh();
    expect(vmLists()).toHaveLength(2);
    expect(loaderSpy.load).toHaveBeenCalled();
  });

  it('keeps the websocket rows once they have arrived', async () => {
    const page = await openPage();
    await handle(vmList('exp1', [{ name: 'live' }]));

    await apply(running([]));
    expect(names(page)).toEqual(['live']);
  });

  it('says the VMs are loading until the websocket lists them', async () => {
    const page = await openPage();
    await apply(running([]));
    expect(emptyText(page)).toBe('Loading VMs…');

    await handle(vmList('exp1', []));
    expect(emptyText(page)).toBe('This experiment has no VMs');
    await page.find('.autocomplete input').setValue('web');
    expect(emptyText(page)).toBe('No VMs match your search');
  });

  it('hands over to the stopped view when the experiment has stopped', async () => {
    const page = await openPage();
    await apply({ name: 'exp1', running: false });

    expect(page.emitted('running')).toEqual([[false]]);
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
  ])('asks again for %s', async (_, data, sizes) => {
    await openPage(data);
    await apply(running([]));
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
    await apply(running([{ name: 'vm1' }]));

    await handle(vmList('exp2', [{ name: 'other-vm' }]));
    expect(names(page)).toEqual(['vm1']);
    expect(page.vm.vmsLoaded).toBe(false);

    await handle(vmList('exp1', [{ name: 'web' }]));
    expect(names(page)).toEqual(['web']);
    expect(page.vm.vmsLoaded).toBe(true);
    expect(
      cachedPage(experimentKey('exp1')).data.vms.map((vm) => vm.name),
    ).toEqual(['web']);
  });

  it.each([
    ['stopped', 'stop'],
    ['deleted', 'delete'],
  ])(
    'leaves the page when its experiment is %s elsewhere',
    async (done, action) => {
      const page = await openPage();
      const path = () => page.router.currentRoute.value.path;
      const message = (name, act) => wsMsg('experiment', name, act);

      // another experiment, or a message that does not end this one
      await handle(message('exp2', action));
      await handle(message('exp1', 'stopping'));
      expect(path()).toBe('/experiment/exp1');

      await handle(message('exp1', action));
      expect(path()).toBe('/experiments/');
      expect(toasts()).toEqual([
        { text: `The exp1 experiment was ${done} elsewhere.`, type: 'is-info' },
      ]);
    },
  );

  it('leaves its own Stop to navigate', async () => {
    const page = await openPage({ isWaiting: true });

    await handle(wsMsg('experiment', 'exp1', 'stop'));
    expect(page.router.currentRoute.value.path).toBe('/experiment/exp1');
    expect(toasts()).toEqual([]);
  });
});

describe('ordering VM lists against VM changes', () => {
  // the server's answer to a VM list request it read at seq
  const listAt = (seq, vms, extra = {}) => ({
    ...vmList('exp1', vms),
    seq,
    result: { vms, total: vms.length, ...extra },
  });
  const vmMsg = (action, vm, seq) =>
    wsMsg('experiment/vm', `exp1/${vm.name}`, action, vm, seq);

  // each VM's name and the color of its tag: running or shut down
  const COLORS = { 'is-success': 'RUNNING', 'is-danger': 'QUIT' };
  const states = (page) =>
    tableRows(page).map((cells) => {
      const tag = cells.Node.find('.tag');
      const color = tag.classes().find((c) => c in COLORS);
      return `${textOf(tag)}:${COLORS[color]}`;
    });

  async function withVms(vms) {
    const page = await openPage();
    await apply(running([]));
    await handle(listAt(5, vms));
    return page;
  }

  it('keeps a VM stopped when a list read before the stop arrives after it', async () => {
    const page = await withVms([{ name: 'vm1', state: 'RUNNING' }]);

    await handle(vmMsg('stop', { name: 'vm1', state: 'QUIT' }, 20));
    await handle(listAt(10, [{ name: 'vm1', state: 'RUNNING' }]));
    expect(states(page)).toEqual(['vm1:QUIT']);

    // a list read after the stop is taken as it is
    await handle(listAt(30, [{ name: 'vm1', state: 'RUNNING' }]));
    expect(states(page)).toEqual(['vm1:RUNNING']);
  });

  it('keeps a deleted VM out of a list read before the delete', async () => {
    const page = await withVms([{ name: 'vm1' }, { name: 'vm2' }]);

    await handle(vmMsg('delete', { name: 'vm1' }, 20));
    await handle(listAt(10, [{ name: 'vm1' }, { name: 'vm2' }]));
    expect(names(page)).toEqual(['vm2']);
  });

  it('forgets the changes it ordered by when the websocket reconnects', async () => {
    const page = await withVms([{ name: 'vm1', state: 'RUNNING' }]);
    await handle(vmMsg('stop', { name: 'vm1', state: 'QUIT' }, 20));

    // a restarted server numbers its messages afresh
    ws.reconnect.forEach((f) => f());
    await handle(listAt(3, [{ name: 'vm1', state: 'RUNNING' }]));
    expect(states(page)).toEqual(['vm1:RUNNING']);
  });

  it('says when minimega did not answer for the VMs', async () => {
    const page = await withVms([{ name: 'vm1' }]);
    const warning = () => page.find('.runtime-unknown');
    expect(warning().exists()).toBe(false);

    await handle(listAt(10, [{ name: 'vm1' }], { runtime_unknown: true }));
    expect(textOf(warning())).toBe(
      'minimega did not answer; showing the VMs as configured.',
    );
  });
});
