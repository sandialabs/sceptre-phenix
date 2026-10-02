// Each table's Paginate toggle is remembered in this browser, under the
// table's name, for every user of it.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// the pages holding the tables
vi.mock('@/utils/rbac.js', () => ({ roleAllowed: () => true }));
vi.mock('@/utils/axios.js', () => ({ default: {} }));
vi.mock('@/store.js', () => ({ usePhenixStore: () => ({}) }));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
  onWsReconnect: () => () => {},
  sendWsMsg: () => {},
}));

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
import { runSetup } from './helpers/context.js';
import { blockedStorage, memoryStorage } from './helpers/storage.js';

beforeEach(() => vi.stubGlobal('localStorage', memoryStorage()));
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

    const bindings = runSetup(component, props);
    const table = bindings.table ?? bindings.filesTable;
    expect(table.isPaginated).toBe(true);

    table.isPaginated = false;
    await nextTick();
    expect(localStorage.getItem(key)).toBe('false');
  });
});
