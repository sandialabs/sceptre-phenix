// The Experiments page: its table, its search, and rows kept current from the
// websocket, even for experiments it has not loaded (workflow, Configs, CLI).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, resetMocks, ws } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', capturing);
vi.mock('@/utils/pageData.js', () => ({ pageFetchers: {} }));

import Experiments from '@/views/Experiments.vue';
import { never } from './helpers/async.js';
import { mountPage, tableRows, textOf, toasts } from './helpers/mount.js';

let reloadSoon;
beforeEach(() => {
  resetMocks({ store: { token: 't', features: [], role: null } });
  axios.get.mockImplementation(never);
  reloadSoon = vi
    .spyOn(Experiments.methods, 'reloadSoon')
    .mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

const page = (experiments = []) =>
  mountPage(Experiments, {
    route: '/experiments',
    data: { experiments, loaded: true },
  });

// what the server sends the page over the websocket
const send = (msg) => {
  for (const handler of ws.handlers) handler(msg);
};

describe('the table', () => {
  it('lists each experiment with its topology and scenario', async () => {
    const wrapper = await page([
      {
        name: 'exp1',
        status: 'stopped',
        topology: 'topo-a',
        scenario: 'scn-a',
        vm_count: 3,
      },
      { name: 'exp2', status: 'stopped', topology: 'topo-b', vm_count: 1 },
    ]);

    const rows = tableRows(wrapper).map((cells) =>
      ['Name', 'Topology', 'Scenario'].map((label) => textOf(cells[label])),
    );
    expect(rows).toEqual([
      ['exp1', 'topo-a', 'scn-a'],
      ['exp2', 'topo-b', ''],
    ]);
  });

  it('sorts by each column but the VLAN range and actions', async () => {
    const wrapper = await page();

    const headers = wrapper.findAll('thead th').map((th) => ({
      label: textOf(th),
      // sortable, with the sort arrow beside the label
      sorts: th.classes('is-sortable') && th.classes('sort-inline'),
    }));
    expect(headers).toEqual([
      { label: 'Name', sorts: true },
      { label: 'Status', sorts: true },
      { label: 'Topology', sorts: true },
      { label: 'Scenario', sorts: true },
      { label: 'Start Time', sorts: true },
      { label: 'VMs', sorts: true },
      { label: 'VLANs', sorts: false },
      { label: 'Actions', sorts: false },
    ]);
  });

  it('searches names literally', async () => {
    const wrapper = await page([
      { name: 'a(1', status: 'stopped', start_time: '' },
      { name: 'b', status: 'stopped' },
    ]);

    await wrapper
      .find('input[placeholder="Find an Experiment"]')
      .setValue('a(');
    expect(tableRows(wrapper).map((cells) => textOf(cells.Name))).toEqual([
      'a(1',
    ]);
  });
});

describe('websocket messages', () => {
  it('add an unknown starting experiment at once and reload the list', async () => {
    const wrapper = await page([{ name: 'old', status: 'stopped' }]);

    send({ resource: { type: 'experiment', name: 'new', action: 'starting' } });

    expect(wrapper.vm.experiments.map((e) => [e.name, e.status])).toEqual([
      ['old', 'stopped'],
      ['new', 'starting'],
    ]);
    expect(reloadSoon).toHaveBeenCalled();
  });

  it('fill a row known only from messages with the loaded details', async () => {
    const wrapper = await page();

    // the progress message came after the server read the list (seq 10)
    send({
      resource: { type: 'experiment', name: 'new', action: 'progress' },
      result: { percent: 0.5 },
      seq: 20,
    });
    expect(wrapper.vm.experiments[0]).toMatchObject({
      name: 'new',
      status: 'starting',
      percent: 50,
      partial: true,
    });

    loaderSpy.options.apply(
      [
        {
          name: 'new',
          status: 'starting',
          percent: 0.3,
          topology: 'Topo',
          vm_count: 7,
        },
      ],
      { seq: 10 },
    );

    expect(wrapper.vm.experiments).toEqual([
      {
        name: 'new',
        status: 'starting',
        percent: 50,
        topology: 'Topo',
        vm_count: 7,
      },
    ]);
  });

  it('take a list the server read after a change over the row it changed', async () => {
    const wrapper = await page([{ name: 'demo', status: 'started' }]);

    send({
      resource: { type: 'experiment', name: 'demo', action: 'stop' },
      result: { name: 'demo', running: false },
      seq: 20,
    });
    const shown = wrapper.vm.experiments[0];

    // read before the stop: the row the message changed stays
    loaderSpy.options.apply([{ name: 'demo', status: 'started' }], { seq: 15 });
    expect(wrapper.vm.experiments[0]).toBe(shown);

    // read after it (say the experiment was started again from the CLI)
    loaderSpy.options.apply([{ name: 'demo', status: 'started' }], { seq: 25 });
    expect(wrapper.vm.experiments[0].status).toBe('started');
  });

  it('reload the list when an experiment config is created elsewhere', async () => {
    await page();

    send({
      resource: { type: 'config', name: 'Experiment/new', action: 'create' },
    });

    expect(reloadSoon).toHaveBeenCalled();
  });

  it('add no second row for an experiment already listed', async () => {
    const wrapper = await page([{ name: 'new', status: 'stopped' }]);

    send({
      resource: { type: 'experiment', name: 'new', action: 'create' },
      result: { name: 'new' },
    });

    expect(wrapper.vm.experiments).toHaveLength(1);
    expect(reloadSoon).not.toHaveBeenCalled();
  });

  it('report delayed VMs from the started experiment', async () => {
    await page([{ name: 'exp1', status: 'stopped' }]);

    send({
      resource: { type: 'experiment', name: 'exp1', action: 'start' },
      result: { name: 'exp1', delayed_vms: 2 },
    });

    expect(toasts()[0].text).toContain('with 2 delayed VMs');
  });
});
