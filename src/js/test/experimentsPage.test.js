// The Experiments page: its table, its search, and rows kept current from the
// websocket, even for experiments it has not loaded (workflow, Configs, CLI).
import fc from 'fast-check';
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

beforeEach(() => {
  resetMocks({ store: { token: 't', features: [], role: null } });
  axios.get.mockImplementation(never);
  // the page waits a moment before it reloads the list
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
});

afterEach(() => {
  vi.useRealTimers();
});

const page = (experiments = []) =>
  mountPage(Experiments, {
    route: '/experiments',
    data: { experiments, loaded: true },
  });

// what the server sends the page over the websocket
const send = async (wrapper, msg) => {
  for (const handler of ws.handlers) handler(msg);
  await wrapper.vm.$nextTick();
};

// the list requests the page has made, once its wait is over
const reloads = () => {
  vi.advanceTimersByTime(250);
  return loaderSpy.load.mock.calls;
};

// Each row's name and status, and its topology, VM count and VLANs when
// `details`; a starting experiment shows its progress in place of a tag.
const shown = (wrapper, details) =>
  tableRows(wrapper).map((cells) => {
    const tag = cells.Status.find('.tag');
    const status = tag.exists()
      ? textOf(tag)
      : `starting ${textOf(cells.Status.find('.progress-value'))}`;
    return [
      textOf(cells.Name),
      status,
      ...(details ? ['Topology', 'VMs', 'VLANs'] : []).map((label) =>
        textOf(cells[label]),
      ),
    ];
  });

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

  it('finds the experiments with any part of their names, in any case', async () => {
    const chars = /^[aA1(.[]{1,4}$/;
    const listAndSearch = fc
      .uniqueArray(fc.stringMatching(chars), { maxLength: 6 })
      .chain((names) =>
        fc.tuple(
          fc.constant(names),
          fc.oneof(
            fc.stringMatching(/^[aA1(.[]{0,2}$/),
            ...names.map((n) => fc.nat(n.length).map((i) => n.slice(i, i + 2))),
          ),
        ),
      );
    await fc.assert(
      fc.asyncProperty(listAndSearch, async ([names, search]) => {
        const wrapper = await page(
          names.map((name) => ({ name, status: 'stopped' })),
        );
        await wrapper
          .find('input[placeholder="Find an Experiment"]')
          .setValue(search);
        const found = (n) => n.toLowerCase().includes(search.toLowerCase());
        expect(
          tableRows(wrapper)
            .map((cells) => textOf(cells.Name))
            .toSorted(),
        ).toEqual(names.filter(found).toSorted());
        wrapper.unmount();
      }),
      { numRuns: 20 },
    );
  });
});

describe('websocket messages', () => {
  it('add an unknown starting experiment at once and reload the list', async () => {
    const wrapper = await page([{ name: 'old', status: 'stopped' }]);

    await send(wrapper, {
      resource: { type: 'experiment', name: 'new', action: 'starting' },
    });
    await send(wrapper, {
      resource: { type: 'experiment', name: 'newer', action: 'starting' },
    });

    expect(shown(wrapper)).toEqual([
      ['new', 'starting 0%'],
      ['newer', 'starting 0%'],
      ['old', 'stopped'],
    ]);
    // one request covers both
    expect(reloads()).toEqual([[{ force: true }]]);
  });

  it('fill a row known only from messages with the loaded details', async () => {
    const wrapper = await page();

    // the progress message came after the server read the list (seq 10)
    await send(wrapper, {
      resource: { type: 'experiment', name: 'new', action: 'progress' },
      result: { percent: 0.5 },
      seq: 20,
    });
    // with no details yet, not even its VLANs
    expect(shown(wrapper, true)).toEqual([['new', 'starting 50%', '', '', '']]);

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
    await wrapper.vm.$nextTick();

    expect(shown(wrapper, true)).toEqual([
      ['new', 'starting 50%', 'topo', '7', '- ()'],
    ]);
  });

  it('take a list the server read after a change over the row it changed', async () => {
    const wrapper = await page([{ name: 'demo', status: 'started' }]);

    await send(wrapper, {
      resource: { type: 'experiment', name: 'demo', action: 'stop' },
      result: { name: 'demo', running: false },
      seq: 20,
    });
    // a list read at seq, saying the experiment runs
    const listAt = async (seq) => {
      loaderSpy.options.apply([{ name: 'demo', status: 'started' }], { seq });
      await wrapper.vm.$nextTick();
      return shown(wrapper);
    };

    // read before the stop: the row the message changed stays
    expect(await listAt(15)).toEqual([['demo', 'stopped']]);
    // read after it (say the experiment was started again from the CLI)
    expect(await listAt(25)).toEqual([['demo', 'started']]);
  });

  it('reload the list when an experiment config is created elsewhere', async () => {
    const wrapper = await page();

    await send(wrapper, {
      resource: { type: 'config', name: 'Experiment/new', action: 'create' },
    });

    expect(reloads()).toEqual([[{ force: true }]]);
  });

  it('add no second row for an experiment already listed', async () => {
    const wrapper = await page([{ name: 'new', status: 'stopped' }]);

    await send(wrapper, {
      resource: { type: 'experiment', name: 'new', action: 'create' },
      result: { name: 'new' },
    });

    expect(shown(wrapper)).toEqual([['new', 'stopped']]);
    expect(reloads()).toEqual([]);
  });

  it('report delayed VMs from the started experiment', async () => {
    const wrapper = await page([{ name: 'exp1', status: 'stopped' }]);

    await send(wrapper, {
      resource: { type: 'experiment', name: 'exp1', action: 'start' },
      result: { name: 'exp1', delayed_vms: 2 },
    });

    expect(toasts()[0].text).toContain('with 2 delayed VMs');
  });
});
