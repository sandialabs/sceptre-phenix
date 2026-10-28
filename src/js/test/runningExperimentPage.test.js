// The running experiment page: the experiment it loads, its VMs over the
// websocket, what it reloads on reconnect, and traffic that is not its own.
import { flushPromises, RouterLinkStub } from '@vue/test-utils';
import fc from 'fast-check';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, notify, resetMocks, ws } =
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
import { httpError, vm, wsMsg } from './helpers/fixtures.js';
import {
  byLabel,
  confirmDialog,
  dialogs,
  mountPage,
  tableRows,
  textOf,
  toasts,
} from './helpers/mount.js';

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
    expect(textOf(page)).not.toContain('Paginate');
    // the fresh one counts more VMs than a page holds
    await apply({ ...running([{ name: 'vm1' }]), vm_count: 11 });

    expect(vmLists()).toHaveLength(1);
    expect(names(page)).toEqual(['vm1']);
    expect(textOf(page)).toContain('Paginate');
  });

  it.each([
    ['says why', 'minimega could not list the VMs', true],
    ['says nothing', '', false],
  ])('%s when its start could not resume', async (_, interrupted, shown) => {
    const page = await openPage();
    await apply({ ...running([]), interrupted });

    const notice = page.find('.start-interrupted');
    expect(notice.exists()).toBe(shown);
    if (shown) expect(textOf(notice)).toContain(interrupted);
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
      { activeTab: 'vnc', vncZoom: 8 },
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

  it('lists no VMs while its tab is hidden, leaving that to its return', async () => {
    await openPage();
    await apply(running([]));
    ws.sent.length = 0;
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    try {
      ws.reconnect.forEach((f) => f());
      expect(vmLists()).toHaveLength(0);

      hidden.mockReturnValue(false);
      document.dispatchEvent(new Event('visibilitychange'));
      expect(vmLists()).toHaveLength(1);
    } finally {
      hidden.mockRestore();
    }
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
    await apply(running([]));

    await handle(vmList('exp2', [{ name: 'other-vm' }]));
    expect(names(page)).toEqual([]);
    expect(emptyText(page)).toBe('Loading VMs…');

    await handle(vmList('exp1', [{ name: 'web' }]));
    expect(names(page)).toEqual(['web']);
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
      expect(path()).toBe('/experiments');
      expect(toasts()).toEqual([
        { text: `The exp1 experiment was ${done} elsewhere.`, type: 'is-info' },
      ]);
    },
  );

  it('shows a VM failure as an error that stays, and its progress as a toast', async () => {
    await openPage();
    await handle(vmList('exp1', [{ name: 'vm1' }]));

    await handle(
      wsMsg('experiment/vm', 'exp1/vm1', 'error', { error: 'boom' }),
    );
    expect(notify.show).toHaveBeenCalledWith('boom');
    expect(toasts()).toEqual([]);

    await handle(wsMsg('experiment/vm/snapshot', 'exp1/vm1', 'creating', {}));
    expect(toasts()).toEqual([
      {
        text: 'phēnix is creating a snapshot for the vm1 VM.',
        type: 'is-warning',
      },
    ]);
  });

  it('offers to force a stop whose VMs survived, then leaves the page', async () => {
    axios.post
      .mockRejectedValueOnce(
        httpError(400, { message: 'unable', metadata: { retry: 'force' } }),
      )
      .mockResolvedValue({});
    const page = await openPage();
    await apply(running([]));

    await byLabel(page, 'Stop experiment').trigger('click');
    await confirmDialog();
    expect(notify.error).toHaveBeenCalledTimes(1);
    expect(page.router.currentRoute.value.path).toBe('/experiment/exp1');

    const dialog = dialogs().at(-1);
    expect(textOf(dialog.querySelector('header'))).toBe('Force the Stop');
    await confirmDialog();
    expect(axios.post.mock.calls).toEqual([
      ['experiments/exp1/stop'],
      ['experiments/exp1/stop', null, { params: { force: true } }],
    ]);
    expect(page.router.currentRoute.value.path).toBe('/experiments');
  });

  it('leaves its own Stop to navigate', async () => {
    const page = await openPage({ isWaiting: true });

    await handle(wsMsg('experiment', 'exp1', 'stop'));
    expect(page.router.currentRoute.value.path).toBe('/experiment/exp1');
    expect(toasts()).toEqual([]);
  });
});

describe('the cached copy of the page', () => {
  const shot = (name, screenshot) =>
    wsMsg('experiment/vm/screenshot', `exp1/${name}`, 'update', {
      screenshot,
    });
  const cachedRows = () => cachedPage(experimentKey('exp1'))?.data.vms;
  const hasShot = (row) => 'screenshot' in row;
  // the pictures the VM table draws
  const pictures = (page) =>
    page.findAll('.table img').map((img) => img.attributes('src'));

  it('keeps the VM rows without the screenshots the page shows', async () => {
    const page = await openPage();
    await apply(running([]));
    await handle(vmList('exp1', [vm({ screenshot: 'data:first' })]));
    expect(pictures(page)).toContain('data:first');
    expect(cachedRows().map((row) => row.name)).toEqual(['web']);
    expect(cachedRows().some(hasShot)).toBe(false);

    await handle(shot('web', 'data:next'));
    expect(pictures(page)).toContain('data:next');
    expect(cachedRows().some(hasShot)).toBe(false);

    // a later list keeps the screenshot on screen, and out of the cache
    await handle(vmList('exp1', [vm()]));
    expect(pictures(page)).toContain('data:next');
    expect(cachedRows().some(hasShot)).toBe(false);
  });

  it('reopens with the VM rows, waiting for their screenshots', async () => {
    const first = await openPage();
    await apply(running([]));
    await handle(vmList('exp1', [vm({ screenshot: 'data:first' })]));
    first.unmount();

    const page = await openPage();
    await apply(cachedPage(experimentKey('exp1')).data);
    expect(names(page)).toEqual(['web']);
    expect(page.find('.table img').attributes('alt')).toContain(
      'Loading the screenshot of VM web',
    );
  });

  it('never holds a screenshot, whatever messages arrive', async () => {
    const name = fc.constantFrom('web', 'db');
    const picture = fc.constantFrom('data:a', 'data:b', '');
    const step = fc.oneof(
      fc
        .uniqueArray(fc.record({ name, screenshot: picture }), {
          selector: (row) => row.name,
        })
        .map((rows) =>
          vmList(
            'exp1',
            rows.map((row) => vm(row)),
          ),
        ),
      fc.tuple(name, picture).map(([n, screenshot]) => shot(n, screenshot)),
      fc
        .tuple(name, picture, fc.constantFrom('update', 'start', 'stop'))
        .map(([n, screenshot, action]) =>
          wsMsg(
            'experiment/vm',
            `exp1/${n}`,
            action,
            vm({ name: n, screenshot, running: action !== 'stop' }),
          ),
        ),
    );

    await fc.assert(
      fc.asyncProperty(fc.array(step, { maxLength: 8 }), async (msgs) => {
        clearPageCache();
        const page = await openPage();
        await apply(running([]));
        try {
          for (const msg of msgs) {
            await handle(msg);
            expect((cachedRows() ?? []).some(hasShot)).toBe(false);
          }
        } finally {
          page.unmount();
        }
      }),
      { numRuns: 20 },
    );
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

  it('shows each VM as its latest news says, by sequence number', async () => {
    const VMS = ['vm1', 'vm2'];
    const state = fc.constantFrom('RUNNING', 'QUIT');
    // a change to one VM, or a list read up to `back` changes ago
    const step = fc.oneof(
      fc.record({ vm: fc.constantFrom(...VMS), state }),
      fc.record({ states: fc.tuple(state, state), back: fc.nat(3) }),
    );
    await fc.assert(
      fc.asyncProperty(fc.array(step, { maxLength: 6 }), async (steps) => {
        const page = await withVms(
          VMS.map((name) => ({ name, state: 'RUNNING' })),
        );
        // each VM's latest news: the seq it was read or sent at, and its state
        const news = new Map(
          VMS.map((vm) => [vm, { seq: 5, state: 'RUNNING' }]),
        );
        // changes arrive in order, and a list after the changes it read
        let seq = 5;
        let listSeq = 5;
        for (const { vm, state, states: listed, back } of steps) {
          if (vm) {
            news.set(vm, { seq: ++seq, state });
            const action = state == 'QUIT' ? 'stop' : 'start';
            await handle(vmMsg(action, { name: vm, state }, seq));
            continue;
          }
          listSeq = Math.max(listSeq, seq - back);
          VMS.forEach((name, i) => {
            if (listSeq >= news.get(name).seq) {
              news.set(name, { seq: listSeq, state: listed[i] });
            }
          });
          await handle(
            listAt(
              listSeq,
              VMS.map((name, i) => ({ name, state: listed[i] })),
            ),
          );
        }
        expect(states(page)).toEqual(
          VMS.map((vm) => `${vm}:${news.get(vm).state}`),
        );
        page.unmount();
      }),
      { numRuns: 20 },
    );
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
      'minimega did not answer. Showing the VMs as configured.',
    );
  });
});

describe('searching', () => {
  const searchBox = (page) => page.find('.autocomplete input');

  it('lists nothing for a search typed just before the page closes', async () => {
    const page = await openPage();
    await apply(running([]));
    const lists = () => vmLists().filter((m) => m.resource.action == 'list');
    const before = lists().length;

    await searchBox(page).setValue('vm-1');
    page.unmount();
    await vi.runAllTimersAsync();
    expect(lists()).toHaveLength(before);
  });

  it('adds a search that found VMs to a full history in place of the oldest', async () => {
    // oldest first
    const full = ['zzzz', ...Array.from({ length: 9 }, (_, i) => `vm-${i}`)];
    const page = await openPage({ searchHistory: full });
    await apply(running([]));

    await searchBox(page).setValue('aaaa');
    await vi.runAllTimersAsync();
    await handle(vmList('exp1', [{ name: 'aaaa' }]));
    expect(page.findComponent({ name: 'BAutocomplete' }).props('data')).toEqual(
      ['aaaa', ...full.slice(1)].toSorted(),
    );
  });
});

describe('the header', () => {
  const tileText = (page) =>
    page.findAll('.app-tile').map((tile) => textOf(tile));

  it('lists every scenario and the apps, each as last heard of', async () => {
    const page = await openPage();
    await apply({
      ...running([]),
      scenario: 'scn',
      scenarios: ['scn', 'base'],
      apps: ['ntp', 'mine'],
    });

    expect(textOf(page.find('.exp-fact-scenarios'))).toContain('scn, base');
    expect(tileText(page)).toEqual(['ntp, Built-in', 'Trigger mine, User app']);

    await handle(wsMsg('apps/mine', 'exp1', 'start'));
    await handle(wsMsg('apps/ntp', 'exp2', 'error'));
    expect(tileText(page)).toEqual(['ntp, Built-in', 'Trigger mine, Running']);
    await handle(wsMsg('apps/mine', 'exp1', 'error', { error: 'boom' }));
    expect(tileText(page)).toEqual([
      'ntp, Built-in',
      'Trigger mine, Last run failed',
    ]);
    await handle(wsMsg('apps/mine', 'exp1', 'canceled'));
    expect(tileText(page)).toEqual([
      'ntp, Built-in',
      'Trigger mine, Last run canceled',
    ]);
  });

  it('shows the apps running when it opened', async () => {
    axios.get.mockImplementation((url) =>
      url === 'experiments/exp1/apps'
        ? Promise.resolve({ data: { mine: true, ntp: false } })
        : Promise.reject(new Error('no netflow')),
    );
    const page = await openPage();
    await apply({ ...running([]), apps: ['ntp', 'mine'] });

    expect(tileText(page)).toEqual(['ntp, Built-in', 'Trigger mine, Running']);
  });

  it("opens the trigger dialog with an app's tile picked", async () => {
    const page = await openPage();
    await apply({ ...running([]), apps: ['ntp', 'mine', 'other'] });

    await page.find('.app-tile button').trigger('click');
    const modal = page.findComponent({ name: 'TriggerAppsModal' });
    expect(modal.props()).toMatchObject({
      active: true,
      triggerable: ['mine', 'other'],
      apps: ['mine'],
    });
  });
});

describe('filtering by label', () => {
  const web = { key: 'role', value: 'web' };
  const filter = (page) => page.findComponent({ name: 'VmLabelFilter' });
  const lastList = () => vmLists().at(-1).request;
  // a VM list with the labels on the experiment's VMs, of total VMs in all
  const labeledList = (vms, labels, total = vms.length) =>
    wsMsg('experiment/vms', 'exp1', 'list', { vms, total, labels });

  it('offers the labels the server lists, beside Paginate', async () => {
    const page = await openPage();
    await apply({ ...running([]), vm_count: 11 });
    await handle(labeledList([{ name: 'vm1' }], [web], 11));

    // the filter comes first, to the left of the Paginate switch
    const html = page.find('.vm-table-bar').html();
    expect(html.indexOf('vm-label-filter-trigger')).toBeGreaterThan(-1);
    expect(html.indexOf('vm-label-filter-trigger')).toBeLessThan(
      html.indexOf('Paginate'),
    );
    expect(filter(page).props('options')).toEqual([web]);
  });

  it('keeps Paginate beside the filter while the checked labels fit a page', async () => {
    const page = await openPage();
    await apply({ ...running([]), vm_count: 11 });
    await handle(labeledList([{ name: 'vm1' }], [web], 11));

    filter(page).vm.$emit('update:picked', [web]);
    await handle(labeledList([{ name: 'vm1', tags: { role: 'web' } }], [web]));
    expect(textOf(page.find('.vm-table-bar'))).toContain('Paginate');
  });

  it('leaves the bar out with no labels and one page', async () => {
    const page = await openPage();
    await apply(running([]));
    await handle(labeledList([{ name: 'vm1' }], []));
    expect(page.find('.vm-table-bar').exists()).toBe(false);
  });

  it('lists the VMs with the labels checked, from the first page, uncached', async () => {
    const page = await openPage();
    await apply({ ...running([]), vm_count: 30 });
    // paged, and on the third page
    Object.assign(page.vm.table, { isPaginated: true, currentPage: 3 });
    await handle(labeledList([{ name: 'vm1', tags: { role: 'web' } }], [web]));
    clearPageCache();

    filter(page).vm.$emit('update:picked', [web]);
    await flushPromises();
    expect(lastList()).toMatchObject({
      labels: [web],
      filter: '',
      page_number: 1,
    });

    await handle(labeledList([], [web]));
    expect(emptyText(page)).toBe('No VMs have any of the checked labels');
    expect(cachedPage(experimentKey('exp1'))).toBeUndefined();

    await page.find('.autocomplete input').setValue('db');
    expect(emptyText(page)).toBe(
      'No VMs match your search and the checked labels',
    );
  });

  it('offers no more labels once the request would be too long for the server', async () => {
    const page = await openPage();
    await apply({ ...running([]), vm_count: 30 });
    const many = Array.from({ length: 60 }, (_, i) => ({
      key: `key-${i}`,
      value: 'v'.repeat(40),
    }));
    await handle(labeledList([{ name: 'vm1' }], many, 30));
    const boxes = () => page.findAll('.vm-label-filter-list input');
    expect(boxes().every((box) => !box.element.disabled)).toBe(true);

    // checks every label it still offers, in order
    for (let i = 0; i < many.length; i++) {
      const box = boxes()[i];
      if (!box.element.disabled) await box.setValue(true);
    }
    const disabled = boxes().filter((box) => box.element.disabled);
    expect(disabled.length).toBeGreaterThan(0);
    expect(lastList().labels.length + disabled.length).toBe(many.length);
    expect(textOf(page.find('.vm-label-filter'))).toContain(
      'Uncheck a label to check another',
    );
    const bytes = (msg) => new TextEncoder().encode(JSON.stringify(msg)).length;
    expect(vmLists().every((msg) => bytes(msg) <= 2048)).toBe(true);
  });

  it('sorts the VMs by label count through the server', async () => {
    const page = await openPage();
    await apply(running([]));

    page.findComponent({ name: 'VmTable' }).vm.$emit('sort', 'labels', 'desc');
    expect(lastList()).toMatchObject({
      sort_column: 'labels',
      sort_asc: false,
    });
  });
});

describe('the netflow capture', () => {
  class FakeWebSocket extends EventTarget {
    static instances = [];
    constructor(url) {
      super();
      this.url = url;
      FakeWebSocket.instances.push(this);
    }
    close() {}
    // the socket closes with this code
    closeWith(code, reason = '') {
      this.dispatchEvent(Object.assign(new Event('close'), { code, reason }));
    }
  }

  let freshness;
  beforeEach(async () => {
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
    freshness = await import('@/utils/freshness.js');
    // a capture is running on the server, so the page joins it
    axios.get.mockImplementation(async (path) => {
      if (path.endsWith('/netflow')) return { data: {} };
      throw new Error('not found');
    });
  });

  afterEach(() => {
    freshness.setWsDown(false);
    vi.unstubAllGlobals();
  });

  // the page on the netflow tab, joined to the running capture
  async function capturing() {
    const page = await openPage({ activeTab: 'netflow' });
    await apply(running([]));
    const socket = FakeWebSocket.instances.at(-1);
    socket.dispatchEvent(new Event('open'));
    return { page, socket };
  }

  const markers = (page) => page.findAll('.netflow-marker td').map(textOf);
  const toggle = (page) =>
    page.find('button[aria-label$="netflow capture"]').attributes('aria-label');

  it.each([
    [
      'the server shutting down',
      1001,
      'Capture stopped: phēnix server shutting down',
      'Start netflow capture',
    ],
    [
      'a lost connection',
      1006,
      'Lost the connection to the phēnix server',
      'Stop netflow capture',
    ],
  ])(
    'says the capture stream ended after %s',
    async (_, code, marker, button) => {
      const { page, socket } = await capturing();
      expect(toggle(page)).toBe('Stop netflow capture');

      socket.closeWith(code);
      await flushPromises();
      expect(markers(page).filter((t) => t.includes('phēnix'))).toEqual([
        marker,
      ]);
      expect(toggle(page)).toBe(button);
    },
  );

  it('offers the recorded captures for download', async () => {
    axios.get.mockImplementation(async (path) => {
      if (path.endsWith('/netflow')) return { data: {} };
      if (path.endsWith('/netflow/captures')) return { data: { captures: [] } };
      throw new Error('not found');
    });
    const { page } = await capturing();
    const button = page
      .findAll('button')
      .find((b) => textOf(b) === 'Download captures');
    expect(button).toBeDefined();

    await button.trigger('click');
    await flushPromises();
    expect(axios.get).toHaveBeenCalledWith(
      expect.stringMatching(/^experiments\/[^/]+\/netflow\/captures$/),
    );
    expect(textOf(page.find('.netflow-captures .modal-card-body'))).toBe(
      'No captures recorded yet. Starting a netflow capture records it.',
    );
  });

  it('says nothing of a stream the server ended normally', async () => {
    const { page, socket } = await capturing();
    socket.closeWith(1000, 'netflow stopped');
    await flushPromises();
    expect(markers(page).filter((t) => t.includes('phēnix'))).toEqual([]);
  });
});
