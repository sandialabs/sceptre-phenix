// Each table's Paginate toggle is remembered in this browser, under the
// table's name, for every user of it.
import fc from 'fast-check';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// the pages holding the tables
const { axios, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);
vi.mock('@/utils/notify.js', modules.notify);

import { nextTick } from 'vue';

import ConfigsList from '@/components/configs/ConfigsList.vue';
import ExperimentFilesTab from '@/components/experiment/ExperimentFilesTab.vue';
import { loadPaginate, savePaginate } from '@/utils/paginatePref.js';
import { useTable } from '@/utils/useTable.js';
import Disks from '@/views/Disks.vue';
import Experiments from '@/views/Experiments.vue';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import StoppedExperiment from '@/views/experiment/StoppedExperiment.vue';
import Hosts from '@/views/Hosts.vue';
import Users from '@/views/Users.vue';
import { disk } from './helpers/fixtures.js';
import { mountComponent, tableRows, textOf } from './helpers/mount.js';
import { blockedStorage, memoryStorage } from './helpers/storage.js';

beforeEach(() => {
  setActivePinia(createPinia());
  resetMocks({ store: {} });
  axios.get.mockResolvedValue({ data: {} });
});
afterEach(() => vi.unstubAllGlobals());

describe('paginate preference', () => {
  it('is off until someone turns it on', () => {
    expect(loadPaginate('disks')).toBe(false);
    expect(useTable({ name: 'disks' }).table.isPaginated).toBe(false);
  });

  it('is remembered per table, under one key for all users', async () => {
    const { table } = useTable({ name: 'disks' });
    table.isPaginated = true;
    await nextTick();

    expect(localStorage.getItem('phenix.paginate.disks')).toBe('true');
    expect(useTable({ name: 'disks' }).table.isPaginated).toBe(true);
    expect(useTable({ name: 'hosts' }).table.isPaginated).toBe(false);

    table.isPaginated = false;
    await nextTick();
    expect(useTable({ name: 'disks' }).table.isPaginated).toBe(false);
  });

  it('leaves unnamed tables unpaginated and unsaved', async () => {
    const { table } = useTable();
    expect(table.isPaginated).toBe(false);

    table.isPaginated = true;
    await nextTick();
    expect(localStorage.getItem('phenix.paginate.undefined')).toBeNull();
  });

  it("keeps a table's own fields beside its paging", () => {
    localStorage.setItem('phenix.paginate.stopped-vms', 'true');
    const { table } = useTable({
      name: 'stopped-vms',
      fields: { total: 0, sortColumn: 'name' },
    });
    expect(table).toMatchObject({
      isPaginated: true,
      perPage: 10,
      currentPage: 1,
      total: 0,
      sortColumn: 'name',
    });
  });

  it('reads back the setting last saved for each table', () => {
    const name = fc.oneof(
      fc.constantFrom('disks', 'hosts', 'stopped-vms'),
      fc.string({ maxLength: 3 }),
    );
    fc.assert(
      fc.property(
        fc.array(fc.tuple(name, fc.anything())),
        name,
        (saves, other) => {
          vi.stubGlobal('localStorage', memoryStorage());
          const last = new Map();
          for (const [table, on] of saves) {
            savePaginate(table, on);
            last.set(table, Boolean(on));
          }
          for (const table of [...last.keys(), other]) {
            expect(loadPaginate(table)).toBe(last.get(table) ?? false);
          }
        },
      ),
    );
  });

  it('starts unpaginated when storage is unavailable', () => {
    vi.stubGlobal('localStorage', blockedStorage());

    expect(loadPaginate('disks')).toBe(false);
    expect(() => savePaginate('disks', true)).not.toThrow();
  });
});

// more rows than a page holds, so each page offers its Paginate toggle
const ROWS = 11;
const rows = (row) => Array.from({ length: ROWS }, (_, i) => row(i));
const host = (i) => ({
  name: `node${i}`,
  cpus: 4,
  load: ['0.1', '0.2', '0.3'],
  memused: 1024,
  memtotal: 4096,
  diskusage: { diskphenix: 10, diskminimega: 20 },
  bandwidth: '0 MB/s',
  vms: 0,
  uptime: 60,
});

// Clicks a page's Paginate toggle.
async function togglePaginate(wrapper) {
  const toggle = wrapper
    .findAll('label.switch')
    .find((label) => textOf(label) === 'Paginate');
  const input = toggle.find('input');
  await input.setValue(!input.element.checked);
}

describe('the pages with a Paginate toggle', () => {
  it.each([
    [
      'configs',
      ConfigsList,
      '/configs/',
      {
        configs: rows((i) => ({
          kind: 'Topology',
          metadata: { name: `t${i}`, updated: '2025-01-02T03:04:05Z' },
        })),
      },
    ],
    [
      'disks',
      Disks,
      '/disks/',
      {
        disks: rows((i) => disk(`/phenix/images/d${i}.qc2`, `d${i}.qc2`)),
      },
    ],
    ['hosts', Hosts, '/hosts', { hosts: rows(host) }],
    [
      'users',
      Users,
      '/users',
      {
        users: rows((i) => ({
          username: `u${i}`,
          first_name: '',
          last_name: '',
          role_name: 'Viewer',
        })),
      },
    ],
  ])('remember theirs as %s', async (name, component, route, data) => {
    const key = `phenix.paginate.${name}`;
    const page = () =>
      mountComponent(component, { route, data: { ...data, loaded: true } });

    const first = await page();
    expect(tableRows(first)).toHaveLength(ROWS);
    await togglePaginate(first);
    expect(localStorage.getItem(key)).toBe('true');
    expect(tableRows(first)).toHaveLength(10);
    first.unmount();

    // opened again, the page pages until the toggle is clicked off
    const again = await page();
    expect(tableRows(again)).toHaveLength(10);
    await togglePaginate(again);
    expect(localStorage.getItem(key)).toBe('false');
    expect(tableRows(again)).toHaveLength(ROWS);
  });
});

// tables whose toggle needs a running experiment or its files to show
describe('the tables with a Paginate toggle', () => {
  it.each([
    ['experiments', Experiments],
    ['stopped-vms', StoppedExperiment],
    ['running-vms', RunningExperiment],
    // each experiment page names its Files tab's table
    ['stopped-files', ExperimentFilesTab, { paginateKey: 'stopped-files' }],
    ['running-files', ExperimentFilesTab, { paginateKey: 'running-files' }],
  ])('remember theirs as %s', async (name, component, props) => {
    const key = `phenix.paginate.${name}`;
    localStorage.setItem(key, 'true');

    const { vm } = await mountComponent(component, {
      props,
      route: '/experiment/exp1',
    });
    const table = vm.table ?? vm.filesTable;
    expect(table.isPaginated).toBe(true);

    table.isPaginated = false;
    await nextTick();
    expect(localStorage.getItem(key)).toBe('false');
  });
});
