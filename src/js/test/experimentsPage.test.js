// The Experiments page: its table, its search, and the rows it keeps up to
// date from websocket messages, including experiments it has not loaded yet
// (created by a workflow, the Configs page or the CLI).
import { describe, expect, it, vi } from 'vitest';

vi.mock('@/utils/rbac.js', () => ({ roleAllowed: () => true }));
vi.mock('@/utils/axios.js', () => ({
  default: { get: vi.fn(() => new Promise(() => {})) },
}));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ token: 't', features: [], role: null }),
}));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
}));
const loaderOpts = vi.hoisted(() => ({}));
vi.mock('@/utils/pageLoader.js', () => ({
  createPageLoader: (opts) => {
    Object.assign(loaderOpts, opts);
    return { start: () => {}, stop: () => {}, load: vi.fn() };
  },
  loadingText: (what) => `Loading ${what}…`,
  stillLoading: (loaded) => !loaded,
}));
vi.mock('@/utils/pageData.js', () => ({ pageFetchers: {} }));

import Experiments from '@/views/Experiments.vue';
import { makeContext } from './helpers/context.js';
import { renderSSR, tableRows, textOf } from './helpers/render.js';

function page(experiments) {
  const ctx = makeContext(Experiments, {
    reloadSoon: vi.fn(),
    $buefy: { toast: { open: vi.fn() } },
  });
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
    const requestedAt = Date.now();

    ctx.handleWs({
      resource: { type: 'experiment', name: 'new', action: 'progress' },
      result: { percent: 0.5 },
    });
    expect(ctx.experiments[0]).toMatchObject({
      name: 'new',
      status: 'starting',
      percent: 50,
      partial: true,
    });

    loaderOpts.apply(
      [
        {
          name: 'new',
          status: 'starting',
          percent: 0.3,
          topology: 'Topo',
          vm_count: 7,
        },
      ],
      { requestedAt },
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
