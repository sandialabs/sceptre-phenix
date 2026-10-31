// The Experiments page: its table, its search, and rows kept current from the
// websocket, even for experiments it has not loaded (workflow, Configs, CLI).
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  capturing,
  loaderSpy,
  modules,
  noticeText,
  notices,
  notify,
  rbac,
  resetMocks,
  ws,
} = await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/utils/notify.js', modules.notify);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', capturing);
vi.mock('@/utils/pageData.js', () => ({ pageFetchers: {} }));

import { flushPromises } from '@vue/test-utils';

import CreateExperimentCard from '@/components/CreateExperimentCard.vue';
import Experiments from '@/views/Experiments.vue';
import { never } from './helpers/async.js';
import { httpError } from './helpers/fixtures.js';
import { allowOnly, denyOnly } from './helpers/permissions.js';
import { componentDeclarations } from './helpers/css.js';
import {
  buttons,
  byLabel,
  confirmDialog,
  dialogs,
  mountPage,
  tableHeader,
  tableRows,
  textOf,
  toasts,
  tooltipPositions,
} from './helpers/mount.js';

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

  it('marks a running experiment whose start could not resume', async () => {
    const reason =
      'VMs were waiting to start when phenix stopped tracking them';
    const wrapper = await page([
      { name: 'a', status: 'started', running: true, interrupted: reason },
      { name: 'b', status: 'started', running: true },
      // a stopped experiment no longer waits on anything
      { name: 'c', status: 'stopped', running: false, interrupted: reason },
    ]);

    const marks = tableRows(wrapper).map((cells) => {
      const mark = cells.Status.find('.exp-interrupted');
      if (!mark.exists()) return null;
      return [textOf(mark.find('.tag')), textOf(mark.find('.is-sr-only'))];
    });
    expect(marks).toEqual([['interrupted', `: ${reason}`], null, null]);
  });

  it('sorts by each column but the actions', async () => {
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
      { label: 'VLANs', sorts: true },
      { label: 'Actions', sorts: false },
    ]);
  });

  // the names down the table once the header with this label is clicked
  async function sortedBy(wrapper, label, clicks = 1) {
    for (let i = 0; i < clicks; i++) {
      await tableHeader(wrapper, label).trigger('click');
    }
    return tableRows(wrapper).map((cells) => textOf(cells.Name));
  }

  it('sorts by VLAN count, ties by name from A to Z either way', async () => {
    const wrapper = await page([
      { name: 'b', status: 'stopped', vlan_count: 2 },
      { name: 'a', status: 'stopped', vlan_count: 2 },
      { name: 'c', status: 'stopped', vlan_count: 0 },
      { name: 'd', status: 'stopped', vlan_count: 5 },
    ]);

    expect(await sortedBy(wrapper, 'VLANs')).toEqual(['c', 'a', 'b', 'd']);
    expect(await sortedBy(wrapper, 'VLANs')).toEqual(['d', 'a', 'b', 'c']);
  });

  it('shows start times as sent, then how long ago, with no tooltip', async () => {
    // the clock too, which beforeEach leaves real, for a fixed age
    vi.useRealTimers();
    vi.useFakeTimers({
      toFake: [
        'setTimeout',
        'clearTimeout',
        'setInterval',
        'clearInterval',
        'Date',
      ],
      now: Date.parse('2026-10-29T02:03:22Z'),
    });
    const raw = '2026-10-28T14:03:22Z';
    const wrapper = await page([
      { name: 'a', status: 'started', start_time: raw },
      { name: 'b', status: 'stopped', start_time: '' },
      { name: 'c', status: 'stopped', start_time: 'garbled' },
    ]);

    const [a, b, c] = tableRows(wrapper).map((cells) => cells['Start Time']);
    expect(textOf(a)).toBe(`${raw} (12 hours ago)`);
    expect(a.find('time').attributes('datetime')).toBe(raw);
    expect(
      wrapper
        .findAllComponents({ name: 'BTooltip' })
        .some((t) => a.element.contains(t.element)),
    ).toBe(false);
    // as before: N/A for none, and text that is not a time as it came
    expect([textOf(b), textOf(c)]).toEqual(['N/A', 'garbled']);
  });

  // The table fits a 1,024 px screen: the cells pad less below 1,280 px, and
  // a start time wraps between its date, time and age (see timeWithAge.test).
  it('keeps the table within a desktop screen', () => {
    const style = (selector, media) =>
      componentDeclarations('src/views/Experiments.vue', selector, media);
    const narrow = 'min-width: 769px) and (max-width: 1279px';
    for (const cell of ['th', 'td']) {
      const selector = `.exp-table :deep(${cell}:not(.exp-select-cell))`;
      expect(style(selector, narrow)).toEqual({ 'padding-inline': '0.375em' });
      expect(style(selector)).toEqual({});
    }
  });

  // the column ends at the window's right edge, so a tooltip centered over a
  // button there would run off it
  it('opens the Actions tooltips to the left, inside the window', async () => {
    const wrapper = await page([
      { name: 'exp1', status: 'stopped' },
      { name: 'exp2', status: 'started', start_time: '2026-01-01T06:00:00Z' },
    ]);
    for (const cells of tableRows(wrapper)) {
      const positions = tooltipPositions(cells.Actions);
      expect(positions.length).toBeGreaterThan(2);
      expect(new Set(positions)).toEqual(new Set(['is-left']));
    }
  });

  it('sorts start times by the moment, not the text', async () => {
    const wrapper = await page([
      { name: 'late', status: 'started', start_time: '2026-01-01T06:00:00Z' },
      // 05:00 UTC, though its text sorts after the other's
      {
        name: 'early',
        status: 'started',
        start_time: '2026-01-01T10:00:00+05:00',
      },
      { name: 'never', status: 'stopped', start_time: '' },
    ]);

    expect(await sortedBy(wrapper, 'Start Time')).toEqual([
      'never',
      'early',
      'late',
    ]);
  });

  it.each([
    [
      'may view them',
      () => true,
      ['/configs/?view=Topology/Topo-A', '/configs/?view=Scenario/scn'],
    ],
    [
      'may not view the topology',
      denyOnly(['configs', 'get', 'Topology/Topo-A']),
      [null, '/configs/?view=Scenario/scn'],
    ],
    ['may not list configs', denyOnly(['configs', 'list']), [null, null]],
  ])(
    'links the topology and scenario to their configs when the role %s',
    async (_, role, hrefs) => {
      rbac.allowed = role;
      const wrapper = await page([
        { name: 'a', status: 'stopped', topology: 'Topo-A', scenario: 'scn' },
      ]);

      const [cells] = tableRows(wrapper);
      const href = (cell) =>
        cell.find('a').exists() ? cell.find('a').attributes('href') : null;
      expect([href(cells.Topology), href(cells.Scenario)]).toEqual(hrefs);
      expect([textOf(cells.Topology), textOf(cells.Scenario)]).toEqual([
        'topo-a',
        'scn',
      ]);
    },
  );

  it('puts the checkbox column first when the list arrives after the page opens', async () => {
    const wrapper = await page();
    expect(wrapper.find('thead th').classes()).not.toContain('exp-select-cell');

    loaderSpy.options.apply(three(), { seq: 1 });
    await flushPromises();

    expect(wrapper.find('thead th').classes()).toContain('exp-select-cell');
    expect(Object.values(tableRows(wrapper)[0])[0].classes()).toContain(
      'exp-select-cell',
    );
  });

  it('narrows the checkbox column to its checkboxes', async () => {
    const wrapper = await page(three());

    const [cells] = tableRows(wrapper);
    expect(wrapper.find('thead th').classes()).toContain('exp-select-cell');
    expect(Object.values(cells)[0].classes()).toContain('exp-select-cell');
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

// experiments a, b and c: a and c stopped, b running
const three = () => [
  { name: 'a', status: 'stopped', running: false },
  { name: 'b', status: 'started', running: true },
  { name: 'c', status: 'stopped', running: false },
];

// the control with this aria-label, its tooltip, and whether it is disabled
function control(wrapper, label) {
  const button = byLabel(wrapper, label);
  const tooltip = wrapper
    .findAllComponents({ name: 'BTooltip' })
    .find((t) => t.element.contains(button.element));
  return {
    tooltip: textOf(tooltip.vm.$refs.content),
    disabled: button.attributes('disabled') !== undefined,
    control: button,
  };
}

// the checkbox labeled with this text
const checkbox = (wrapper, text) =>
  wrapper
    .findAll('label.checkbox')
    .find((label) => textOf(label) === text)
    .find('input');

describe('the totals', () => {
  // each total's label and number
  const totals = (wrapper) =>
    wrapper
      .findAll('.stat-strip .stat')
      .map((t) => [textOf(t.find('dt')), textOf(t.find('dd'))]);

  it('count every experiment, those running, their VMs and all VLANs', async () => {
    const wrapper = await page([
      {
        name: 'a',
        status: 'started',
        running: true,
        vm_count: 4,
        vlan_count: 3,
      },
      {
        name: 'b',
        status: 'stopped',
        running: false,
        vm_count: 9,
        vlan_count: 2,
      },
      {
        name: 'c',
        status: 'started',
        running: true,
        vm_count: 1,
        vlan_count: 1,
      },
    ]);
    // the search narrows the table, not the totals
    await wrapper.find('input[placeholder="Find an Experiment"]').setValue('a');

    expect(totals(wrapper)).toEqual([
      ['Experiments', '3'],
      ['Running', '2'],
      ['VMs in running experiments', '5'],
      ['VLANs', '6'],
    ]);
    expect(byLabel(wrapper, 'Experiment totals').attributes('role')).toBe(
      'group',
    );
  });

  it('follow an experiment the websocket says has started', async () => {
    const wrapper = await page(three());

    await send(wrapper, {
      resource: { type: 'experiment', name: 'a', action: 'start' },
      result: { name: 'a', running: true, vm_count: 6, vlan_count: 2 },
    });
    expect(totals(wrapper)).toEqual([
      ['Experiments', '3'],
      ['Running', '2'],
      ['VMs in running experiments', '6'],
      ['VLANs', '2'],
    ]);
  });
});

describe('the Start and Stop buttons', () => {
  // the row's Start and Stop buttons: tooltip, disabled, spinning
  const power = (wrapper, name) =>
    ['Start', 'Stop'].map((verb) => {
      const {
        tooltip,
        disabled,
        control: button,
      } = control(wrapper, `${verb} experiment ${name}`);
      return { tooltip, disabled, spinning: button.classes('is-loading') };
    });

  it.each([
    [
      'a stopped experiment',
      { name: 'x', status: 'stopped', running: false },
      () => true,
      [
        { tooltip: 'Start experiment x', disabled: false, spinning: false },
        {
          tooltip: 'Cannot stop: the experiment is not running',
          disabled: true,
          spinning: false,
        },
      ],
    ],
    [
      'a running experiment',
      { name: 'x', status: 'started', running: true },
      () => true,
      [
        {
          tooltip: 'Cannot start: the experiment is already running',
          disabled: true,
          spinning: false,
        },
        { tooltip: 'Stop experiment x', disabled: false, spinning: false },
      ],
    ],
    [
      'a starting experiment',
      { name: 'x', status: 'starting', running: false },
      () => true,
      [
        {
          tooltip: 'Cannot start: the experiment is starting',
          disabled: true,
          spinning: true,
        },
        {
          tooltip: 'Cannot stop: the experiment is starting',
          disabled: true,
          spinning: false,
        },
      ],
    ],
    [
      'a stopping experiment',
      { name: 'x', status: 'stopping', running: true },
      () => true,
      [
        {
          tooltip: 'Cannot start: the experiment is stopping',
          disabled: true,
          spinning: false,
        },
        {
          tooltip: 'Cannot stop: the experiment is stopping',
          disabled: true,
          spinning: true,
        },
      ],
    ],
    [
      'a role that may not start it',
      { name: 'x', status: 'stopped', running: false },
      denyOnly(['experiments/start', 'update', 'x']),
      [
        {
          tooltip: 'Cannot start: you do not have permission',
          disabled: true,
          spinning: false,
        },
        {
          tooltip: 'Cannot stop: the experiment is not running',
          disabled: true,
          spinning: false,
        },
      ],
    ],
    [
      'a role that may not stop it',
      { name: 'x', status: 'started', running: true },
      denyOnly(['experiments/stop', 'update', 'x']),
      [
        {
          tooltip: 'Cannot start: the experiment is already running',
          disabled: true,
          spinning: false,
        },
        {
          tooltip: 'Cannot stop: you do not have permission',
          disabled: true,
          spinning: false,
        },
      ],
    ],
  ])('for %s', async (_, exp, role, expected) => {
    rbac.allowed = role;
    const wrapper = await page([exp]);

    expect(power(wrapper, 'x')).toEqual(expected);
  });

  it('start the experiment at once, spinning until the server answers', async () => {
    const answer = Promise.withResolvers();
    axios.post.mockReturnValue(answer.promise);
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a').trigger('click');
    expect(dialogs()).toEqual([]);
    expect(axios.post.mock.calls).toEqual([['experiments/a/start']]);
    expect(power(wrapper, 'a')[0]).toMatchObject({
      disabled: true,
      spinning: true,
    });

    answer.resolve({});
    await flushPromises();
    expect(power(wrapper, 'a')[0].spinning).toBe(false);
  });

  it('stop it after confirming, spinning until the server answers', async () => {
    const answer = Promise.withResolvers();
    axios.post.mockReturnValue(answer.promise);
    const wrapper = await page(three());

    await byLabel(wrapper, 'Stop experiment b').trigger('click');
    expect(axios.post).not.toHaveBeenCalled();
    await confirmDialog();
    expect(axios.post.mock.calls).toEqual([['experiments/b/stop']]);
    expect(power(wrapper, 'b')[1]).toMatchObject({
      tooltip: 'Cannot stop: the experiment is being stopped',
      disabled: true,
      spinning: true,
    });
    // nor may it be deleted meanwhile
    expect(control(wrapper, 'Delete experiment b').disabled).toBe(true);

    answer.reject(httpError(500, { message: 'no' }));
    await flushPromises();
    expect(notify.error).toHaveBeenCalledTimes(1);
    expect(power(wrapper, 'b')[1]).toMatchObject({
      disabled: false,
      spinning: false,
    });
  });
});

describe('one experiment', () => {
  it('starts at once, without asking', async () => {
    axios.post.mockResolvedValue({});
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a (stopped)').trigger('click');
    expect(dialogs()).toEqual([]);
    expect(axios.post).toHaveBeenCalledWith('experiments/a/start');
  });

  it('sends one start until the server answers, and shows a failed one stopped', async () => {
    let fail;
    axios.post.mockImplementation(
      () => new Promise((_, reject) => (fail = reject)),
    );
    const wrapper = await page(three());

    const label = 'Start experiment a (stopped)';
    await byLabel(wrapper, label).trigger('click');
    // the websocket has not yet said the experiment is starting
    expect(control(wrapper, label)).toMatchObject({
      tooltip: 'Cannot start: the experiment is being started',
      disabled: true,
    });
    await byLabel(wrapper, label).trigger('click');
    expect(axios.post).toHaveBeenCalledTimes(1);

    await send(wrapper, {
      resource: { type: 'experiment', name: 'a', action: 'starting' },
    });
    fail(httpError(500, { message: 'no VMs' }));
    await flushPromises();
    expect(shown(wrapper)[0]).toEqual(['a', 'stopped']);
    expect(control(wrapper, label).disabled).toBe(false);
  });

  it('stops only after confirming', async () => {
    axios.post.mockResolvedValue({});
    const wrapper = await page(three());

    await byLabel(wrapper, 'Stop experiment b (started)').trigger('click');
    expect(axios.post).not.toHaveBeenCalled();
    await confirmDialog();
    expect(axios.post).toHaveBeenCalledWith('experiments/b/stop');
  });

  it.each([
    ['its VMs survived', { retry: 'force' }, true],
    ['it failed otherwise', undefined, false],
  ])('offers to force a stop only when %s', async (_, metadata, offered) => {
    axios.post
      .mockRejectedValueOnce(
        httpError(400, { message: 'unable to stop', metadata }),
      )
      .mockResolvedValue({});
    const wrapper = await page(three());

    await byLabel(wrapper, 'Stop experiment b (started)').trigger('click');
    await confirmDialog();
    expect(notify.error).toHaveBeenCalledTimes(1);

    const titles = dialogs().map((d) => textOf(d.querySelector('header')));
    expect(titles.includes('Force the Stop')).toBe(offered);
    if (!offered) return;

    await confirmDialog();
    expect(axios.post.mock.calls).toEqual([
      ['experiments/b/stop'],
      ['experiments/b/stop', null, { params: { force: true } }],
    ]);
  });

  it('reloads the list when a stop failed', async () => {
    const wrapper = await page(three());

    await send(wrapper, {
      resource: { type: 'experiment', name: 'b', action: 'errorStopping' },
    });
    expect(reloads()).toEqual([[{ force: true }]]);
  });

  it.each([
    [
      'a stopping experiment',
      { name: 'x', status: 'stopping', running: true },
      'Stop experiment x (stopping)',
      'Cannot stop: the experiment is stopping',
    ],
    [
      'a running experiment',
      { name: 'x', status: 'started', running: true },
      'Delete experiment x',
      'Cannot delete: the experiment is running',
    ],
    [
      'a stopping experiment',
      { name: 'x', status: 'stopping', running: true },
      'Delete experiment x',
      'Cannot delete: the experiment is stopping',
    ],
  ])('disables what %s rules out, saying why', async (_, exp, label, why) => {
    const wrapper = await page([exp]);

    const { tooltip, disabled, control: button } = control(wrapper, label);
    expect({ tooltip, disabled }).toEqual({ tooltip: why, disabled: true });
    await button.trigger('click');
    expect(dialogs()).toEqual([]);
    expect(toasts()).toEqual([]);
  });

  it.each([
    ['may', allowOnly(['experiments/stop', 'update', 'b']), true],
    ['may not', denyOnly(['experiments/stop', 'update', 'b']), false],
  ])(
    'offers Stop to a role that %s stop the experiment',
    async (_, role, offered) => {
      rbac.allowed = role;
      const wrapper = await page(three());

      expect(byLabel(wrapper, 'Stop experiment b (started)').exists()).toBe(
        offered,
      );
      expect(textOf(tableRows(wrapper)[1].Status.find('.tag'))).toBe('started');
    },
  );
});

describe('the selection', () => {
  // checks the rows with these names
  async function select(wrapper, ...names) {
    for (const name of names) {
      await checkbox(wrapper, `Select experiment ${name}`).setValue(true);
    }
  }

  // the selection toolbar's buttons
  const toolbar = (wrapper) =>
    buttons(wrapper.find('.exp-selection-actions')).filter(Boolean);

  it('acts on all it can with one confirm and one summary', async () => {
    const answers = [];
    axios.post.mockImplementation(
      () => new Promise((resolve) => answers.push(resolve)),
    );
    const wrapper = await page(three());

    await checkbox(wrapper, 'Select all experiments').setValue(true);
    expect(toolbar(wrapper)).toEqual([
      'Start (2)',
      'Stop (1)',
      'Delete (2)',
      'Clear the experiment selection',
    ]);

    await wrapper.find('.exp-bulk-start').trigger('click');
    expect(textOf(dialogs()[0])).toContain(
      'This will start the experiments a and c.',
    );
    expect(textOf(dialogs()[0])).toContain(
      'The b experiment is already running.',
    );
    await confirmDialog();
    expect(axios.post.mock.calls).toEqual([
      ['experiments/a/start'],
      ['experiments/c/start'],
    ]);

    // the experiments' own news waits for the summary
    for (const name of ['a', 'c']) {
      await send(wrapper, {
        resource: { type: 'experiment', name, action: 'start' },
        result: { name },
      });
    }
    answers.forEach((resolve) => resolve({}));
    await flushPromises();
    expect(toasts()).toEqual([
      { text: 'Started 2 experiments', type: 'is-success' },
    ]);
  });

  it('reports delayed VMs of an experiment it started', async () => {
    let answer;
    axios.post.mockImplementation(
      () => new Promise((resolve) => (answer = resolve)),
    );
    const wrapper = await page(three());

    await select(wrapper, 'a');
    await wrapper.find('.exp-bulk-start').trigger('click');
    await confirmDialog();
    await send(wrapper, {
      resource: { type: 'experiment', name: 'a', action: 'start' },
      result: { name: 'a', delayed_vms: 2 },
    });
    expect(toasts().map((t) => t.text)).toEqual([
      'The a experiment has been started (with 2 delayed VMs).',
    ]);
    answer({});
    await flushPromises();
    // the newest toast is drawn first, and the delayed VMs stay told
    expect(toasts().map((t) => t.text)).toEqual([
      'Started 1 experiment',
      'The a experiment has been started (with 2 delayed VMs).',
    ]);
  });

  it('skips an experiment that changed while the confirm was open', async () => {
    axios.post.mockResolvedValue({});
    const wrapper = await page(three());

    await select(wrapper, 'a', 'c');
    await wrapper.find('.exp-bulk-start').trigger('click');
    await send(wrapper, {
      resource: { type: 'experiment', name: 'c', action: 'starting' },
    });
    await confirmDialog();
    await flushPromises();

    expect(axios.post.mock.calls).toEqual([['experiments/a/start']]);
    expect(noticeText()).toContain('Started 1 of 2 experiments: 1 skipped');
    expect(noticeText()).toContain('c: the experiment is starting');
  });

  it("skips the experiments outside the role's permissions", async () => {
    rbac.allowed = allowOnly(
      ['experiments/start', 'update', 'a'],
      ['experiments', 'delete', 'c'],
    );
    axios.post.mockResolvedValue({});
    const wrapper = await page(three());

    // the role may take no action on b
    expect(
      checkbox(wrapper, 'Select experiment b').attributes('disabled'),
    ).toBeDefined();
    await select(wrapper, 'a', 'c');
    expect(toolbar(wrapper)).toEqual([
      'Start (1)',
      'Stop (0) (disabled)',
      'Delete (1)',
      'Clear the experiment selection',
    ]);
    expect(control(wrapper, 'Stop selected experiments').tooltip).toBe(
      'Cannot stop: you do not have permission',
    );

    await wrapper.find('.exp-bulk-start').trigger('click');
    expect(textOf(dialogs()[0])).toContain(
      "The c experiment is outside your role's permissions.",
    );
    await confirmDialog();
    expect(axios.post.mock.calls).toEqual([['experiments/a/start']]);
  });

  it('says why a selection offers an action to none of it', async () => {
    const wrapper = await page(three());

    await select(wrapper, 'b');
    expect(control(wrapper, 'Start selected experiments')).toMatchObject({
      tooltip: 'Cannot start: none of the selected experiments is stopped',
      disabled: true,
    });
  });

  it('reports the experiments it could not delete, and offers a retry', async () => {
    axios.delete.mockImplementation(async (url) => {
      if (url === 'experiments/c') throw httpError(409, { message: 'busy' });
      return {};
    });
    const wrapper = await page(three());

    await select(wrapper, 'a', 'c');
    await wrapper.find('.exp-bulk-delete').trigger('click');
    await confirmDialog();
    await flushPromises();

    expect(tableRows(wrapper).map((cells) => textOf(cells.Name))).toEqual([
      'b',
      'c',
    ]);
    expect(notices.open).toHaveBeenCalledTimes(1);
    expect(noticeText()).toContain('Deleted 1 of 2 experiments: 1 failed');
    expect(noticeText()).toContain('c: busy');
    expect(noticeText()).toContain('Retry failed (1)');
  });

  it('offers to force the stops whose VMs survived, and only those', async () => {
    axios.post.mockImplementation(async (url, _, config) => {
      if (config?.params?.force) return {};
      if (url === 'experiments/d/stop')
        throw httpError(409, { message: 'busy' });
      if (url !== 'experiments/b/stop') {
        throw httpError(400, {
          message: 'unable',
          metadata: { retry: 'force' },
        });
      }
      return {};
    });
    const wrapper = await page(
      ['a', 'b', 'c', 'd'].map((name) => ({
        name,
        status: 'started',
        running: true,
      })),
    );

    await select(wrapper, 'a', 'b', 'c', 'd');
    await wrapper.find('.exp-bulk-stop').trigger('click');
    await confirmDialog();
    await flushPromises();
    expect(noticeText()).toContain('Stopped 1 of 4 experiments: 3 failed');

    const dialog = dialogs().at(-1);
    expect(textOf(dialog.querySelector('header'))).toBe('Force the Stops');
    expect(textOf(dialog)).toContain(
      'the experiments a and c, so they are still running',
    );
    await confirmDialog();
    await flushPromises();
    expect(axios.post.mock.calls.slice(4)).toEqual([
      ['experiments/a/stop', null, { params: { force: true } }],
      ['experiments/c/stop', null, { params: { force: true } }],
    ]);
    expect(toasts()).toEqual([
      { text: 'Stopped 2 experiments', type: 'is-success' },
    ]);
  });
});

describe('the create card', () => {
  const request = { name: 'new', topology: 'topo' };

  async function opened() {
    const wrapper = await page(three());
    await byLabel(wrapper, 'Create a new experiment').trigger('click');
    return wrapper;
  }

  const cardOf = (wrapper) => wrapper.findComponent(CreateExperimentCard);
  const isOpen = (wrapper) =>
    wrapper.findComponent({ name: 'BModal' }).props('modelValue');

  it('stays open while the experiment is created, with no page overlay', async () => {
    let answer;
    axios.post.mockImplementation(
      () => new Promise((resolve) => (answer = resolve)),
    );
    const wrapper = await opened();

    cardOf(wrapper).vm.$emit('create', request, { start: false });
    await flushPromises();
    expect(isOpen(wrapper)).toBe(true);
    expect(cardOf(wrapper).props('pending')).toBe(true);
    expect(wrapper.find('.loading-overlay').exists()).toBe(false);

    answer({});
    await flushPromises();
    expect(isOpen(wrapper)).toBe(false);
    expect(axios.post.mock.calls).toEqual([
      ['experiments', request, { timeout: 0 }],
    ]);
  });

  it('shows a failed create in the card and tries again from it', async () => {
    axios.post.mockRejectedValueOnce(
      httpError(400, { message: 'experiment new already exists' }),
    );
    const wrapper = await opened();

    cardOf(wrapper).vm.$emit('create', request, { start: false });
    await flushPromises();
    expect(isOpen(wrapper)).toBe(true);
    expect(cardOf(wrapper).props()).toMatchObject({
      pending: false,
      error: 'experiment new already exists',
    });
    expect(notify.error).not.toHaveBeenCalled();

    axios.post.mockResolvedValue({});
    cardOf(wrapper).vm.$emit('create', request, { start: false });
    await flushPromises();
    expect(isOpen(wrapper)).toBe(false);
  });

  it.each([
    ['starts the experiment once created', { start: true }, true],
    ['leaves the experiment stopped', { start: false }, false],
  ])('%s when asked', async (_, opts, starts) => {
    axios.post.mockResolvedValue({});
    const wrapper = await opened();

    cardOf(wrapper).vm.$emit('create', request, opts);
    await flushPromises();
    expect(axios.post.mock.calls.map(([url]) => url)).toEqual(
      starts ? ['experiments', 'experiments/new/start'] : ['experiments'],
    );
  });
});
