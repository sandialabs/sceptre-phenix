// The Experiments page: its table, its search, and rows kept current from the
// websocket, even for experiments it has not loaded (workflow, Configs, CLI).
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  capturing,
  lastNotice,
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
import { pageStatus } from '@/utils/pageLoader.js';
import { never } from './helpers/async.js';
import { httpError } from './helpers/fixtures.js';
import { allowOnly, denyOnly } from './helpers/permissions.js';
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

// the bar in a status cell: "bar <percent>", then its label when it has one
const barOf = (cell) => {
  const label = cell.find('.exp-progress-label');
  return [
    `bar ${textOf(cell.find('.progress-value'))}`,
    ...(label.exists() ? [textOf(label)] : []),
  ].join(' ');
};

// Each row's name and status, and its topology, VM count and VLANs when
// `details`; a row with a bar shows the bar, not its status.
const shown = (wrapper, details) =>
  tableRows(wrapper).map((cells) => {
    const tag = cells.Status.find('.exp-status');
    const bar = cells.Status.find('.progress').exists() || !tag.exists();
    const status = bar ? barOf(cells.Status) : textOf(tag);
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
      { numRuns: 8 },
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
      ['new', 'bar 0%'],
      ['newer', 'bar 0%'],
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
    expect(shown(wrapper, true)).toEqual([['new', 'bar 50%', '', '', '']]);

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
      ['new', 'bar 50%', 'topo', '7', '- ()'],
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

  it('follow an experiment the websocket says has started, not the search', async () => {
    const wrapper = await page(three());
    await wrapper.find('input[placeholder="Find an Experiment"]').setValue('a');

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
    expect(byLabel(wrapper, 'Experiment totals').attributes('role')).toBe(
      'group',
    );
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

  it('start the experiment after confirming, spinning until the server answers', async () => {
    const answer = Promise.withResolvers();
    axios.post.mockReturnValue(answer.promise);
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a').trigger('click');
    expect(axios.post).not.toHaveBeenCalled();
    await confirmDialog();
    expect(axios.post.mock.calls).toEqual([['experiments/a/start']]);
    expect(power(wrapper, 'a')[0]).toMatchObject({
      disabled: true,
      spinning: true,
    });

    // the server tells of the start before it answers
    await send(wrapper, {
      resource: { type: 'experiment', name: 'a', action: 'start' },
      result: { name: 'a', running: true },
    });
    answer.resolve({});
    await flushPromises();
    expect(power(wrapper, 'a')[0].spinning).toBe(false);
    expect(reloads()).toEqual([]);
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
      tooltip: 'Cannot stop: the experiment is stopping',
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

describe('the status', () => {
  // how the row's status draws: element, classes, icon and text
  const drawn = (wrapper) => {
    const status = tableRows(wrapper)[0].Status.find('.exp-status');
    return {
      element: status.element.tagName.toLowerCase(),
      button: status.classes('button'),
      tag: status.classes('tag'),
      color: status
        .classes()
        .find((c) => /^is-(success|danger|warning)$/.test(c)),
      fixedWidth: status.classes('status-width'),
      icon: status.element.querySelector('[data-icon]')?.dataset.icon ?? null,
      text: textOf(status),
    };
  };
  const button = (color, icon, text) => ({
    element: 'button',
    button: true,
    tag: false,
    color,
    fixedWidth: true,
    icon,
    text,
  });
  const tag = (color, text) => ({
    element: 'span',
    button: false,
    tag: true,
    color,
    fixedWidth: true,
    icon: null,
    text,
  });

  it.each([
    [
      'a stopped experiment, a role that may start it',
      { status: 'stopped', running: false },
      () => true,
      button('is-danger', 'play', 'stopped'),
    ],
    [
      'a stopped experiment, a role that may not start it',
      { status: 'stopped', running: false },
      denyOnly(['experiments/start', 'update', 'x']),
      tag('is-danger', 'stopped'),
    ],
    [
      'a running experiment, a role that may stop it',
      { status: 'started', running: true },
      () => true,
      button('is-success', 'stop', 'started'),
    ],
    [
      'a running experiment, a role that may not stop it',
      { status: 'started', running: true },
      denyOnly(['experiments/stop', 'update', 'x']),
      tag('is-success', 'started'),
    ],
  ])('draws %s', async (_, exp, role, expected) => {
    rbac.allowed = role;
    const wrapper = await page([{ name: 'x', ...exp }]);

    expect(drawn(wrapper)).toEqual(expected);
  });

  it.each([
    ['starting', { status: 'starting', running: false }],
    ['stopping', { status: 'stopping', running: true }],
  ])(
    'gives the bar of a %s experiment the same width, in place of its status',
    async (_, exp) => {
      const wrapper = await page([{ name: 'x', ...exp }]);

      const cell = tableRows(wrapper)[0].Status;
      expect(cell.find('.status-progress .progress').exists()).toBe(true);
      expect(cell.find('.exp-status').exists()).toBe(false);
    },
  );
});

describe('the start and stop bar', () => {
  // a websocket message about experiment `name`
  const msg = (name, action, result, seq) => ({
    resource: { type: 'experiment', name, action },
    result,
    seq,
  });
  const progress = (operation, percent, phase, done, total, step) => ({
    operation,
    percent,
    phase,
    done,
    total,
    step,
  });
  // the bar or status of row `i`, and its failure when it has one
  const statusOf = (wrapper, i = 0) => {
    const cell = tableRows(wrapper)[i].Status;
    const failure = cell.find('.exp-progress-failure');
    return [
      shown(wrapper)[i][1],
      ...(failure.exists() ? [textOf(failure)] : []),
    ];
  };

  // Main bug: the start bar showed VMs left to launch, not the part done.
  it('follows a start from the click to its post-start apps', async () => {
    const answer = Promise.withResolvers();
    axios.post.mockReturnValue(answer.promise);
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a').trigger('click');
    await confirmDialog();
    expect(statusOf(wrapper)).toEqual(['bar 0% Sending start request']);

    await send(wrapper, msg('a', 'starting'));
    expect(statusOf(wrapper)).toEqual(['bar 0% Sending start request']);

    const steps = [
      [
        progress('start', 0.1, 'pre-start-apps', 1, 3, 'ntp'),
        'bar 10% Pre-start apps 1/3: ntp',
      ],
      [progress('start', 0.2, 'prepare', 0, 0, ''), 'bar 20% Preparing'],
      [
        progress('start', 0.6, 'launch-vms', 12, 40, ''),
        'bar 60% Launching VMs 12/40',
      ],
      // a late message does not take the bar back
      [
        progress('start', 0.5, 'start-vms', 3, 40, ''),
        'bar 60% Starting VMs 3/40',
      ],
    ];
    for (const [body, want] of steps) {
      await send(wrapper, msg('a', 'progress', body));
      expect(statusOf(wrapper)).toEqual([want]);
    }

    // running before its post-start apps are done
    await send(wrapper, msg('a', 'start', { name: 'a', running: true }));
    answer.resolve({});
    await flushPromises();
    await send(
      wrapper,
      msg(
        'a',
        'progress',
        progress('start', 0.9, 'post-start-apps', 1, 2, 'scorch'),
      ),
    );
    expect(statusOf(wrapper)).toEqual(['bar 90% Post-start apps 1/2: scorch']);
    expect(byLabel(wrapper, 'Stop experiment a').exists()).toBe(true);

    await send(
      wrapper,
      msg('a', 'progress', progress('start', 1, 'done', 2, 2)),
    );
    expect(statusOf(wrapper)).toEqual(['started']);
    expect(reloads()).toEqual([]);
  });

  it('follows a stop from its own start at 0, then ends it at the stop message', async () => {
    axios.post.mockReturnValue(never());
    const wrapper = await page([
      {
        name: 'b',
        status: 'started',
        running: true,
        progress: progress('start', 0.95, 'post-start-apps', 1, 2, 'scorch'),
      },
    ]);
    expect(statusOf(wrapper)).toEqual(['bar 95% Post-start apps 1/2: scorch']);

    await byLabel(wrapper, 'Stop experiment b').trigger('click');
    await confirmDialog();
    expect(statusOf(wrapper)).toEqual(['bar 0% Sending stop request']);

    await send(wrapper, msg('b', 'stopping'));
    await send(
      wrapper,
      msg('b', 'progress', progress('stop', 0.1, 'cleanup-apps', 0, 1)),
    );
    expect(statusOf(wrapper)).toEqual(['bar 10% Cleanup apps 0/1']);
    await send(
      wrapper,
      msg('b', 'progress', progress('stop', 0.5, 'stop-vms', 2, 4)),
    );
    expect(statusOf(wrapper)).toEqual(['bar 50% Stopping VMs 2/4']);
    await send(
      wrapper,
      msg('b', 'progress', progress('stop', 0.9, 'cleanup', 0, 0)),
    );
    expect(statusOf(wrapper)).toEqual(['bar 90% Cleaning up']);

    // a stop sends no done: its stop message ends the bar
    await send(wrapper, msg('b', 'stop', { name: 'b', running: false }));
    expect(statusOf(wrapper)).toEqual(['stopped']);
  });

  // the status button of row `i` beside its bar: its label, and whether it
  // is disabled; null when the row shows no status button
  const powerBesideBar = (wrapper, i = 0) => {
    const cell = tableRows(wrapper)[i].Status;
    const button = cell.find('button.exp-status');
    if (!cell.find('.status-progress .progress').exists()) return 'no bar';
    if (!button.exists()) return null;
    return {
      label: button.attributes('aria-label'),
      disabled: button.attributes('disabled') !== undefined,
    };
  };

  it('shows the Stop button beside the bar of post-start apps, and only the bar while starting', async () => {
    const wrapper = await page(three());

    await send(wrapper, msg('a', 'starting'));
    await send(
      wrapper,
      msg('a', 'progress', progress('start', 0.6, 'launch-vms', 12, 40, '')),
    );
    expect(powerBesideBar(wrapper)).toBeNull();

    await send(wrapper, msg('a', 'start', { name: 'a', running: true }));
    await send(
      wrapper,
      msg(
        'a',
        'progress',
        progress('start', 0.9, 'post-start-apps', 1, 2, 'scorch'),
      ),
    );
    expect(statusOf(wrapper)).toEqual(['bar 90% Post-start apps 1/2: scorch']);
    expect(powerBesideBar(wrapper)).toEqual({
      label: 'Stop experiment a (started)',
      disabled: false,
    });

    axios.post.mockReturnValue(never());
    await byLabel(wrapper, 'Stop experiment a (started)').trigger('click');
    await confirmDialog();
    expect(axios.post.mock.calls).toEqual([['experiments/a/stop']]);
    // stopping shows only the bar
    expect(statusOf(wrapper)).toEqual(['bar 0% Sending stop request']);
    expect(powerBesideBar(wrapper)).toBeNull();
  });

  it.each([
    [
      'a stopping experiment',
      { status: 'stopping', running: true },
      () => true,
    ],
    [
      'a running experiment the role may not stop',
      { status: 'started', running: true },
      denyOnly(['experiments/stop', 'update', 'a']),
    ],
  ])('shows only the bar of %s', async (_, exp, role) => {
    rbac.allowed = role;
    const wrapper = await page([
      {
        name: 'a',
        ...exp,
        progress: progress('start', 0.9, 'post-start-apps', 1, 2, 'scorch'),
      },
    ]);

    expect(powerBesideBar(wrapper)).toBeNull();
    expect(tableRows(wrapper)[0].Status.find('.exp-status').exists()).toBe(
      false,
    );
  });

  it.each([
    [
      'start',
      'Start experiment a',
      { name: 'a', status: 'stopped', running: false },
    ],
    [
      'stop',
      'Stop experiment a',
      {
        name: 'a',
        status: 'started',
        running: true,
        progress: progress('start', 0.9, 'post-start-apps', 1, 2, 'scorch'),
      },
    ],
  ])(
    'puts back the status and bar a row had when its %s request fails',
    async (_, label, row) => {
      axios.post.mockRejectedValue(httpError(500, { message: 'no' }));
      const wrapper = await page([row]);
      const before = statusOf(wrapper);

      await byLabel(wrapper, label).trigger('click');
      await confirmDialog();

      expect(notify.error).toHaveBeenCalledTimes(1);
      expect(statusOf(wrapper)).toEqual(before);
      expect(wrapper.vm.experiments[0]).toMatchObject(row);
      expect(reloads()).toEqual([]);
    },
  );

  it('keeps the bar of its own request over a list read before the server began', async () => {
    axios.post.mockReturnValue(never());
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a').trigger('click');
    await confirmDialog();
    loaderSpy.options.apply(three(), { seq: 50 });
    await wrapper.vm.$nextTick();

    expect(statusOf(wrapper)).toEqual(['bar 0% Sending start request']);
  });

  it('keeps the whole progress of a loaded list, its percent never going back', async () => {
    const wrapper = await page(three());
    await send(
      wrapper,
      msg('a', 'progress', progress('start', 0.5, 'launch-vms', 5, 10, ''), 20),
    );
    // read after that message, with the phase alone (another process)
    const listed = (body) => [
      {
        name: 'a',
        status: 'starting',
        running: false,
        percent: 0.3,
        progress: body,
      },
      ...three().slice(1),
    ];

    loaderSpy.options.apply(
      listed({ operation: 'start', percent: 0.3, phase: 'start-vms' }),
      { seq: 30 },
    );
    await wrapper.vm.$nextTick();
    expect(statusOf(wrapper)).toEqual(['bar 50% Starting VMs']);

    // a stop afterwards starts from its own percent
    loaderSpy.options.apply(
      listed({ operation: 'stop', percent: 0.2, phase: 'stop-vms' }),
      { seq: 40 },
    );
    await wrapper.vm.$nextTick();
    expect(statusOf(wrapper)).toEqual(['bar 20% Stopping VMs']);
  });

  describe('a failure', () => {
    const failed = {
      operation: 'start',
      phase: 'launch-vms',
      step: 'vm-3',
      error: 'no image',
    };

    // Main bug: other tabs kept a failed start as starting until a reload.
    it('shows where a start failed under the status, until the next start', async () => {
      axios.post.mockReturnValue(never());
      const wrapper = await page(three());
      await send(wrapper, msg('a', 'starting'));
      await send(
        wrapper,
        msg('a', 'progress', progress('start', 0.3, 'launch-vms', 1, 9)),
      );

      await send(wrapper, msg('a', 'errorStarting', failed));

      const reason = 'Failed at Launching VMs: vm-3: no image';
      expect(statusOf(wrapper)).toEqual(['stopped', reason]);
      expect(notify.show.mock.calls).toEqual([
        ['The a experiment did not start', reason],
      ]);
      expect(reloads()).toEqual([[{ force: true }]]);

      // a list loaded since keeps it
      loaderSpy.options.apply(three(), { seq: 99 });
      await wrapper.vm.$nextTick();
      expect(statusOf(wrapper)).toEqual(['stopped', reason]);

      await byLabel(wrapper, 'Start experiment a').trigger('click');
      await confirmDialog();
      expect(statusOf(wrapper)).toEqual(['bar 0% Sending start request']);
    });

    it('shows where a stop failed, and the status it had', async () => {
      const wrapper = await page(three());
      await send(wrapper, msg('b', 'stopping'));

      await send(
        wrapper,
        msg('b', 'errorStopping', {
          operation: 'stop',
          phase: 'stop-vms',
          step: '',
          error: 'VMs survived',
        }),
      );

      // running, as it was, until the list says otherwise
      expect(statusOf(wrapper, 1)).toEqual([
        'started',
        'Failed at Stopping VMs: VMs survived',
      ]);
      expect(notify.show).toHaveBeenCalledWith(
        'The b experiment did not stop',
        'Failed at Stopping VMs: VMs survived',
      );
      expect(reloads()).toEqual([[{ force: true }]]);
    });

    it.each([
      ['errorStarting', 'start', 'stopped'],
      ['errorStopping', 'stop', 'started'],
    ])(
      'from an old server (%s with no body) tells it with no reason',
      async (action, verb, status) => {
        const wrapper = await page([
          { name: 'x', status: 'started', running: true },
        ]);

        await send(wrapper, msg('x', action, null));

        expect(statusOf(wrapper)).toEqual([status]);
        expect(notify.show.mock.calls).toEqual([
          [`The x experiment did not ${verb}`, ''],
        ]);
        expect(reloads()).toEqual([[{ force: true }]]);
      },
    );

    it('of an experiment not listed adds no row', async () => {
      const wrapper = await page(three());
      await send(wrapper, msg('new', 'errorStarting', failed));
      expect(shown(wrapper).map(([name]) => name)).toEqual(['a', 'b', 'c']);
    });

    it("of this page's own start is told once, by its request", async () => {
      const answer = Promise.withResolvers();
      axios.post.mockReturnValue(answer.promise);
      const wrapper = await page(three());
      await byLabel(wrapper, 'Start experiment a').trigger('click');
      await confirmDialog();

      await send(wrapper, msg('a', 'errorStarting', failed));
      answer.reject(httpError(500, { message: 'no image' }));
      await flushPromises();

      expect(notify.show).not.toHaveBeenCalled();
      expect(notify.error).toHaveBeenCalledTimes(1);
      expect(statusOf(wrapper)).toEqual([
        'stopped',
        'Failed at Launching VMs: vm-3: no image',
      ]);
    });
  });
});

describe('one experiment', () => {
  it('starts only after confirming', async () => {
    axios.post.mockResolvedValue({});
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a (stopped)').trigger('click');
    expect(axios.post).not.toHaveBeenCalled();
    expect(textOf(dialogs()[0])).toContain('This will start the a experiment.');
    await confirmDialog();
    expect(axios.post).toHaveBeenCalledWith('experiments/a/start');
  });

  it('does not start when the confirm is canceled', async () => {
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a (stopped)').trigger('click');
    dialogs()[0].querySelector('footer .button:first-child').click();
    await flushPromises();
    expect(axios.post).not.toHaveBeenCalled();
    expect(control(wrapper, 'Start experiment a (stopped)').disabled).toBe(
      false,
    );
  });

  it('sends one start until the server answers, and shows a failed one stopped', async () => {
    let fail;
    axios.post.mockImplementation(
      () => new Promise((_, reject) => (fail = reject)),
    );
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a (stopped)').trigger('click');
    await confirmDialog();
    // the websocket has not yet said the experiment is starting
    const label = 'Start experiment a';
    expect(control(wrapper, label)).toMatchObject({
      tooltip: 'Cannot start: the experiment is starting',
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

  it.each([
    [
      'a stopping experiment',
      { name: 'x', status: 'stopping', running: true },
      'Stop experiment x',
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
      expect(textOf(tableRows(wrapper)[1].Status.find('.exp-status'))).toBe(
        'started',
      );
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

// checks the newest dialog's option with this label, as a user does
async function pickOption(label, on = true) {
  const box = [...dialogs().at(-1).querySelectorAll('label.checkbox')].find(
    (el) => textOf(el) === label,
  );
  const input = box.querySelector('input');
  input.checked = on;
  input.dispatchEvent(new Event('change'));
  await flushPromises();
}

describe('the start options', () => {
  const script = '/phenix/experiments/a/mm_files/a.mm';

  it.each([
    ['the defaults', [], ['experiments/a/start']],
    [
      'a dry run',
      ['Dry run (do not call minimega)'],
      ['experiments/a/start', { dryRun: true }],
    ],
    [
      'minimega errors as warnings, no scheduled runs',
      [
        'Treat minimega errors as warnings',
        ["Run the apps' running stage on its schedule", false],
      ],
      [
        'experiments/a/start',
        { treatMinimegaErrorsAsWarnings: true, honorRunPeriodically: false },
      ],
    ],
  ])('send %s', async (_, options, request) => {
    axios.post.mockResolvedValue({});
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a').trigger('click');
    for (const option of options) {
      await pickOption(...[option].flat());
    }
    await confirmDialog();
    expect(axios.post.mock.calls).toEqual([request]);
  });

  it('keep a dry run stopped, and tell what it did in place of the websocket news', async () => {
    const answer = Promise.withResolvers();
    axios.post.mockReturnValue(answer.promise);
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a').trigger('click');
    await pickOption('Dry run (do not call minimega)');
    await confirmDialog();
    // the server tells of the start, then of the experiment stopped again
    await send(wrapper, {
      resource: { type: 'experiment', name: 'a', action: 'starting' },
    });
    await send(wrapper, {
      resource: { type: 'experiment', name: 'a', action: 'stop' },
      result: { name: 'a', running: false },
    });
    answer.resolve({
      data: {
        experiment: { name: 'a', running: false, status: 'stopped' },
        dryRun: true,
        warnings: ['no host for vm1'],
        mmScript: script,
      },
    });
    await flushPromises();

    expect(shown(wrapper)).toEqual([
      ['a', 'stopped'],
      ['b', 'started'],
      ['c', 'stopped'],
    ]);
    expect(toasts()).toEqual([]);
    expect(noticeText()).toContain('Dry run of the a experiment');
    expect(noticeText()).toContain(`minimega script: ${script}`);
    expect(noticeText()).toContain('no host for vm1');
  });

  it('shows a dry run stopped even when its websocket news is late', async () => {
    axios.post.mockResolvedValue({
      data: {
        experiment: { name: 'a', running: false, status: 'stopped' },
        dryRun: true,
        warnings: [],
        mmScript: script,
      },
    });
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a').trigger('click');
    await pickOption('Dry run (do not call minimega)');
    await send(wrapper, {
      resource: { type: 'experiment', name: 'a', action: 'starting' },
    });
    await confirmDialog();

    expect(shown(wrapper)[0]).toEqual(['a', 'stopped']);
    expect(lastNotice().type).toBe('is-info');
  });

  it('tell the minimega errors a start took as warnings', async () => {
    axios.post.mockResolvedValue({
      data: {
        experiment: { name: 'a', running: true, status: 'started' },
        warnings: ['vm1: no such bridge'],
      },
    });
    const wrapper = await page(three());

    await byLabel(wrapper, 'Start experiment a').trigger('click');
    await pickOption('Treat minimega errors as warnings');
    await confirmDialog();

    expect(lastNotice().type).toBe('is-warning');
    expect(noticeText()).toContain('Started the a experiment with 1 warning');
    expect(noticeText()).toContain('vm1: no such bridge');
  });

  it('send nothing when the dialog is canceled', async () => {
    const wrapper = await page(three());
    await byLabel(wrapper, 'Start experiment a').trigger('click');
    dialogs().at(-1).querySelector('footer .button:first-child').click();
    await flushPromises();
    expect(axios.post).not.toHaveBeenCalled();
    expect(shown(wrapper)[0]).toEqual(['a', 'stopped']);
  });

  it('start a selection with the options picked, and retry with them', async () => {
    axios.post
      .mockResolvedValueOnce({})
      .mockRejectedValueOnce(httpError(503, { message: 'busy' }))
      .mockResolvedValue({});
    const wrapper = await page(three());

    await checkbox(wrapper, 'Select all experiments').setValue(true);
    await wrapper.find('.exp-bulk-start').trigger('click');
    expect(textOf(dialogs().at(-1))).toContain(
      'The b experiment is already running.',
    );
    await pickOption("Run the apps' running stage on its schedule", false);
    await confirmDialog();
    const body = { honorRunPeriodically: false };
    expect(axios.post.mock.calls).toEqual([
      ['experiments/a/start', body],
      ['experiments/c/start', body],
    ]);

    // the outcome notice is mocked, so its Retry failed button is pressed
    // through its VNode
    const retry = lastNotice().message.children.find(
      (child) => child?.type === 'button',
    );
    expect(retry.children).toBe('Retry failed (1)');
    retry.props.onClick();
    await flushPromises();
    expect(axios.post.mock.calls.at(-1)).toEqual(['experiments/c/start', body]);
  });
});

describe('the empty page', () => {
  // an outlined button is dark green text on the dark and orange background
  it('shows a filled Create One Now! button that opens the create card', async () => {
    const wrapper = await page([]);
    // an empty list shows the empty page only once this visit's load is done
    pageStatus.fresh = true;
    try {
      await flushPromises();
      const create = wrapper
        .findAll('button')
        .find((b) => textOf(b) === 'Create One Now!');

      expect(create.classes()).toContain('is-success');
      expect(create.classes()).not.toContain('is-outlined');
      await create.trigger('click');
      expect(
        wrapper.findComponent({ name: 'BModal' }).props('modelValue'),
      ).toBe(true);
    } finally {
      pageStatus.fresh = false;
    }
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
    expect(dialogs()).toEqual([]);
    expect(axios.post.mock.calls.map(([url]) => url)).toEqual(
      starts ? ['experiments', 'experiments/new/start'] : ['experiments'],
    );
  });
});
