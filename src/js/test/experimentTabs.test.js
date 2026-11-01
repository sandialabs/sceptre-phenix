// The experiment page's tabs: named in its URL with their search, so a reload
// or a shared link opens the same place, and each keeping its own search.
import fc from 'fast-check';
import { flushPromises, RouterLinkStub } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { h } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);
vi.mock('@/utils/notify.js', modules.notify);

import ExperimentFilesTab from '@/components/experiment/ExperimentFilesTab.vue';
import {
  TAB,
  sameQuery,
  searchFromQuery,
  searchKey,
  tabFromQuery,
  tabQuery,
  vmDetailsRoute,
  withoutVm,
} from '@/components/experiment/experimentTabs.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import StoppedExperiment from '@/views/experiment/StoppedExperiment.vue';
import { never } from './helpers/async.js';
import { denyOnly } from './helpers/permissions.js';
import { mountPage } from './helpers/mount.js';

describe('the URL', () => {
  const offered = [TAB.VMS, TAB.FILES, TAB.VNC];

  it.each([
    [{ tab: 'files' }, TAB.FILES],
    [{ tab: 'vnc' }, TAB.VNC],
    // a tab the page does not offer this role, or none at all
    [{ tab: 'netflow' }, TAB.VMS],
    [{ tab: '1' }, TAB.VMS],
    [{ tab: ['files', 'vnc'] }, TAB.VMS],
    [{}, TAB.VMS],
    [undefined, TAB.VMS],
  ])('with the query %j opens the %s tab', (query, tab) => {
    expect(tabFromQuery(query, offered)).toBe(tab);
  });

  it.each([
    [{ q: 'web' }, 'web'],
    [{ q: ['a', 'b'] }, ''],
    [{}, ''],
  ])('with the query %j searches for "%s"', (query, search) => {
    expect(searchFromQuery(query)).toBe(search);
  });

  it('names the tab and search, leaving out the defaults and keeping the rest', () => {
    expect(tabQuery({ tab: 'files', q: 'x', other: '1' }, TAB.VMS, '')).toEqual(
      { other: '1' },
    );
    expect(tabQuery({}, TAB.NETFLOW, 'udp')).toEqual({
      tab: 'netflow',
      q: 'udp',
    });
  });

  it('reads back the tab and search it names', () => {
    fc.assert(
      fc.property(
        fc.constantFrom(...Object.values(TAB)),
        fc.string(),
        fc.dictionary(
          fc.string().filter((k) => !['tab', 'q'].includes(k)),
          fc.string(),
        ),
        (tab, search, rest) => {
          const query = tabQuery(rest, tab, search);
          expect(tabFromQuery(query, Object.values(TAB))).toBe(tab);
          expect(searchFromQuery(query)).toBe(search);
          expect(sameQuery(tabQuery(query, TAB.VMS, ''), rest)).toBe(true);
        },
      ),
    );
  });

  it('gives the VNC tab the VMs tab search', () => {
    expect(Object.values(TAB).map(searchKey)).toEqual([
      TAB.VMS,
      TAB.FILES,
      TAB.VMS,
      TAB.NETFLOW,
    ]);
  });
});

describe('a link to a VM', () => {
  it("names the experiment page with the VM's details open", () => {
    expect(vmDetailsRoute('exp1', 'web')).toEqual({
      name: 'experiment',
      params: { id: 'exp1' },
      query: { vm: 'web' },
    });
  });

  it('leaves the rest of a query when the VM goes', () => {
    expect(withoutVm({ vm: 'web', tab: 'files', q: 'log' })).toEqual({
      tab: 'files',
      q: 'log',
    });
    expect(withoutVm({})).toEqual({});
  });
});

describe.each([
  ['RunningExperiment', RunningExperiment],
  ['StoppedExperiment', StoppedExperiment],
])('%s', (_, view) => {
  beforeEach(() => {
    setActivePinia(createPinia());
    resetMocks({ store: { token: 't', features: [], role: null } });
    // the page's own lists never arrive
    axios.get.mockReturnValue(never());
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // the Files tab, recording the search the page hands it
  const filters = [];
  const FilesTab = {
    props: ExperimentFilesTab.props,
    emits: ExperimentFilesTab.emits,
    watch: {
      filter: {
        handler(filter) {
          filters.push(filter);
        },
        immediate: true,
      },
    },
    methods: { reload: vi.fn() },
    render: () => h('div'),
  };

  async function open(route = '/experiment/exp1') {
    filters.length = 0;
    FilesTab.methods.reload.mockClear();
    return mountPage(view, {
      route,
      data: { experiment: { name: 'exp1', vms: [] } },
      stubs: { ExperimentFilesTab: FilesTab, RouterLink: RouterLinkStub },
    });
  }

  const activeTab = (page) => textOf(page.find('.tabs li.is-active'));
  const searchBox = (page) => page.find('.autocomplete input');
  const query = (page) => page.router.currentRoute.value.query;
  const textOf = (w) => w.text().trim();

  async function pick(page, label) {
    const tab = page
      .findAll('.tabs li a')
      .find((a) => a.text().trim() === label);
    await tab.trigger('click');
    await flushPromises();
  }

  async function search(page, text) {
    vi.useFakeTimers();
    await searchBox(page).setValue(text);
    await vi.advanceTimersByTimeAsync(250);
    vi.useRealTimers();
    await flushPromises();
  }

  it('opens on the tab and search its URL names', async () => {
    const page = await open('/experiment/exp1?tab=files&q=log');

    expect(activeTab(page)).toBe('Files');
    expect(searchBox(page).element.value).toBe('log');
    expect(searchBox(page).attributes('placeholder')).toBe('Find a File');
    expect(filters.at(-1)).toBe('log');
  });

  it.each([
    ['/experiment/exp1?tab=files', 1],
    ['/experiment/exp1?tab=files&q=log', 1],
    ['/experiment/exp1', 0],
    ['/experiment/exp1?tab=vnc', 0],
  ])('opening %s lists the files %i times', async (route, times) => {
    await open(route);

    expect(FilesTab.methods.reload).toHaveBeenCalledTimes(times);
  });

  it('opens on the VMs tab when the role may not list files', async () => {
    rbac.allowed = denyOnly(['experiments/files', 'list']);
    const page = await open('/experiment/exp1?tab=files');

    expect(activeTab(page)).toBe('VMs');
  });

  it("keeps each tab's search, and names the open one in the URL", async () => {
    const page = await open();
    const history = page.router.options.history;
    const entries = history.state.position;

    await search(page, 'web');
    expect(query(page)).toEqual({ q: 'web' });

    await pick(page, 'Files');
    expect(searchBox(page).element.value).toBe('');
    expect(query(page)).toEqual({ tab: 'files' });

    await search(page, 'log');
    expect(query(page)).toEqual({ tab: 'files', q: 'log' });
    expect(filters.at(-1)).toBe('log');

    await pick(page, 'VMs');
    expect(searchBox(page).element.value).toBe('web');
    expect(query(page)).toEqual({ q: 'web' });

    await pick(page, 'Files');
    expect(searchBox(page).element.value).toBe('log');
    // the URL is replaced, not pushed, so Back leaves the page
    expect(history.state.position).toBe(entries);
  });

  it('follows a link to another of its tabs', async () => {
    const page = await open();

    await page.router.replace({ query: { tab: 'files', q: 'pcap' } });
    await flushPromises();

    expect(activeTab(page)).toBe('Files');
    expect(searchBox(page).element.value).toBe('pcap');
    expect(filters.at(-1)).toBe('pcap');
    expect(query(page)).toEqual({ tab: 'files', q: 'pcap' });
  });

  it("opens a linked VM's details, and drops the link as they close", async () => {
    const web = { name: 'web', host: 'node1', disk: '', running: false };
    // the VM's details answer; the page's other requests never do
    axios.get.mockImplementation((url) =>
      url === 'experiments/exp1/vms/web'
        ? Promise.resolve({ data: web })
        : never(),
    );
    const page = await open();
    page.vm.experiment = { name: 'exp1', vms: [web] };

    await page.router.replace({ query: { tab: 'files', vm: 'web' } });
    await flushPromises();
    expect(page.vm.expModal.active).toBe(true);
    expect(page.vm.expModal.vm.name).toBe('web');

    page.vm.expModal = { ...page.vm.expModal, active: false };
    await flushPromises();
    expect(query(page)).toEqual({ tab: 'files' });
    expect(activeTab(page)).toBe('Files');
  });

  it('opens nothing for a VM the experiment does not have', async () => {
    axios.get.mockImplementation((url) =>
      url === 'experiments/exp1/vms/gone'
        ? Promise.reject(new Error('not found'))
        : never(),
    );
    rbac.allowed = denyOnly(['vms', 'get']);
    const page = await open();

    await page.router.replace({ query: { vm: 'gone' } });
    await flushPromises();
    expect(page.vm.expModal.active).toBe(false);
  });

  it.runIf(view === RunningExperiment)(
    'follows a link to the VNC tab with a new search',
    async () => {
      const page = await open();
      await search(page, 'web');

      await page.router.replace({ query: { tab: 'vnc', q: 'db' } });
      await flushPromises();

      expect(activeTab(page)).toBe('VNC');
      expect(searchBox(page).element.value).toBe('db');
      expect(query(page)).toEqual({ tab: 'vnc', q: 'db' });
    },
  );
});
