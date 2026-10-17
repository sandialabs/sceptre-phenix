// Each table's Paginate toggle is remembered in this browser, under the
// table's name, for every user of it.
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
import { mountComponent } from './helpers/mount.js';
import { blockedStorage } from './helpers/storage.js';

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

  it('starts unpaginated when storage is unavailable', () => {
    vi.stubGlobal('localStorage', blockedStorage());

    expect(loadPaginate('disks')).toBe(false);
    expect(() => savePaginate('disks', true)).not.toThrow();
  });
});

describe('the tables with a Paginate toggle', () => {
  it.each([
    ['configs', ConfigsList],
    ['disks', Disks],
    ['experiments', Experiments],
    ['hosts', Hosts],
    ['users', Users],
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
