// The Experiments page: its table, its search, and the rows it keeps up to
// date from websocket messages, including experiments it has not loaded yet
// (created by a workflow, the Configs page or the CLI).
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', capturing);
vi.mock('@/utils/pageData.js', () => ({ pageFetchers: {} }));

import Experiments from '@/views/Experiments.vue';
import { never } from './helpers/async.js';
import { pageContext } from './helpers/page.js';
import { renderSSR, tableRows, textOf } from './helpers/render.js';

beforeEach(() => {
  resetMocks({ store: { token: 't', features: [], role: null } });
  axios.get.mockImplementation(never);
});

function page(experiments) {
  const ctx = pageContext(Experiments, { fields: { reloadSoon: vi.fn() } });
  Experiments.created.call(ctx);
  ctx.experiments = experiments;
  return ctx;
}

describe('the table', () => {
  const experiments = [
    {
      name: 'exp1',
      status: 'stopped',
      topology: 'topo-a',
      scenario: 'scn-a',
      vm_count: 3,
    },
    { name: 'exp2', status: 'stopped', topology: 'topo-b', vm_count: 1 },
  ];

  it('lists each experiment with its topology and scenario', async () => {
    const html = await renderSSR(
      Experiments,
      {},
      {
        route: '/experiments',
        data: { experiments, loaded: true },
        tables: true,
      },
    );
    const rows = tableRows(html).map((cells) =>
      ['name', 'topology', 'scenario'].map((field) => textOf(cells[field])),
    );
    expect(rows).toEqual([
      ['exp1', 'topo-a', 'scn-a'],
      ['exp2', 'topo-b', ''],
    ]);
  });

  it('sorts by each column but the VLAN range and actions', async () => {
    const html = await renderSSR(
      Experiments,
      {},
      { route: '/experiments', tables: true },
    );
    const headers = [...html.matchAll(/<th ([^>]*)>/g)].map(([, attrs]) => ({
      field: attrs.match(/data-field="([^"]*)"/)?.[1],
      // sortable, with the sort arrow beside the label
      sorts:
        attrs.includes('data-sortable') &&
        attrs.includes('header-class="sort-inline"'),
    }));
    expect(headers).toEqual([
      { field: 'name', sorts: true },
      { field: 'status', sorts: true },
      { field: 'topology', sorts: true },
      { field: 'scenario', sorts: true },
      { field: 'start_time', sorts: true },
      { field: 'vm_count', sorts: true },
      { field: 'vlan_range', sorts: false },
      { field: undefined, sorts: false },
    ]);
  });

  it('searches names literally', () => {
    const ctx = page([{ name: 'a(1', start_time: '' }, { name: 'b' }]);
    ctx.searchName = 'a(';
    expect(ctx.filteredExperiments.map((e) => e.name)).toEqual(['a(1']);
  });
});

describe('websocket messages', () => {
  it('add an unknown starting experiment at once and reload the list', () => {
    const ctx = page([{ name: 'old', status: 'stopped' }]);

    ctx.handleWs({
      resource: { type: 'experiment', name: 'new', action: 'starting' },
    });

    expect(ctx.experiments.map((e) => [e.name, e.status])).toEqual([
      ['old', 'stopped'],
      ['new', 'starting'],
    ]);
    expect(ctx.reloadSoon).toHaveBeenCalled();
  });

  it('fill a row known only from messages with the loaded details', () => {
    const ctx = page([]);

    // the progress message came after the server read the list (seq 10)
    ctx.handleWs({
      resource: { type: 'experiment', name: 'new', action: 'progress' },
      result: { percent: 0.5 },
      seq: 20,
    });
    expect(ctx.experiments[0]).toMatchObject({
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

    expect(ctx.experiments).toEqual([
      {
        name: 'new',
        status: 'starting',
        percent: 50,
        topology: 'Topo',
        vm_count: 7,
      },
    ]);
  });

  it('take a list the server read after a change over the row it changed', () => {
    const ctx = page([{ name: 'demo', status: 'started' }]);

    ctx.handleWs({
      resource: { type: 'experiment', name: 'demo', action: 'stop' },
      result: { name: 'demo', running: false },
      seq: 20,
    });
    const shown = ctx.experiments[0];

    // read before the stop: the row the message changed stays
    loaderSpy.options.apply([{ name: 'demo', status: 'started' }], { seq: 15 });
    expect(ctx.experiments[0]).toBe(shown);

    // read after it (say the experiment was started again from the CLI)
    loaderSpy.options.apply([{ name: 'demo', status: 'started' }], { seq: 25 });
    expect(ctx.experiments[0].status).toBe('started');
  });

  it('reload the list when an experiment config is created elsewhere', () => {
    const ctx = page([]);

    ctx.handleWs({
      resource: { type: 'config', name: 'Experiment/new', action: 'create' },
    });

    expect(ctx.reloadSoon).toHaveBeenCalled();
  });

  it('add no second row for an experiment already listed', () => {
    const ctx = page([{ name: 'new', status: 'stopped' }]);

    ctx.handleWs({
      resource: { type: 'experiment', name: 'new', action: 'create' },
      result: { name: 'new' },
    });

    expect(ctx.experiments).toHaveLength(1);
    expect(ctx.reloadSoon).not.toHaveBeenCalled();
  });

  it('report delayed VMs from the started experiment', () => {
    const ctx = page([{ name: 'exp1' }]);

    ctx.handleWs({
      resource: { type: 'experiment', name: 'exp1', action: 'start' },
      result: { name: 'exp1', delayed_vms: 2 },
    });

    expect(ctx.$buefy.toast.open.mock.calls[0][0].message).toContain(
      'with 2 delayed VMs',
    );
  });
});
