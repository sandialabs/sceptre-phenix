// Experiment captures on the running experiment page: the capture dialog, the
// format dialog of interface captures, the Captures tab, the toolbar button,
// and the controls minimega without support greys out.
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, rbac, resetMocks, saver, store, ws } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/notify.js', modules.notify);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);
vi.mock('file-saver', modules.fileSaver);

import CaptureFormatModal from '@/components/experiment/CaptureFormatModal.vue';
import ExperimentCaptureModal from '@/components/experiment/ExperimentCaptureModal.vue';
import ExperimentCapturesTab from '@/components/experiment/ExperimentCapturesTab.vue';
import VmTable from '@/components/experiment/VmTable.vue';
import { RESOURCES, forgetServer } from '@/utils/freshness.js';
import { clearPageCache } from '@/utils/pageCache.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { httpError, vm, wsMsg } from './helpers/fixtures.js';
import { allowAll, denyOnly } from './helpers/permissions.js';
import {
  buttons,
  confirmDialog,
  dialogs,
  mountComponent,
  mountPage,
  tableRows,
  textOf,
  toasts,
  tooltipButtons,
} from './helpers/mount.js';

const OLD = 'requires minimega 3.4.0 or later; the head node runs 3.3.0';
const SUPPORTED = { available: true, reason: '', version: '3.4.0' };
const UNSUPPORTED = { available: false, reason: OLD, version: '3.3.0' };

const counters = (fields = {}) => ({
  ports: 0,
  packets: 0,
  bytes: 0,
  kdrops: 0,
  pdrops: 0,
  qdrops: 0,
  lost: 0,
  late: 0,
  lagMs: 0,
  skewMs: 0,
  ...fields,
});

const stream = (host, fields = {}, skewWarning = false) => ({
  host,
  bridge: 'mega_bridge_exp1',
  state: 'running',
  counters: counters(fields),
  skewWarning,
});

// an experiment capture as the list gives it
const capture = (id, state, fields = {}) => ({
  id,
  state,
  running: ['starting', 'running', 'stalled', 'stopping'].includes(state),
  path: `/phenix/images/demo/files/demo-${id}.pcapng`,
  file: `demo-${id}.pcapng`,
  format: 'pcapng',
  vlans: [],
  filter: '',
  start: '2026-11-05T14:22:33Z',
  size: 0,
  error: '',
  counters: counters(),
  streams: [],
  ...fields,
});

const RUNNING = capture('3', 'running', {
  size: 812.4 * 1024 ** 2,
  counters: counters({ packets: 1284113, kdrops: 4, pdrops: 8, lost: 5 }),
  streams: [
    stream('compute1', {
      ports: 6,
      packets: 512402,
      kdrops: 4,
      late: 21,
      lagMs: 80,
      skewMs: 0.3,
    }),
    stream(
      'compute3',
      {
        ports: 3,
        packets: 272831,
        qdrops: 8,
        lost: 5,
        late: 1904,
        lagMs: 70,
        skewMs: 612,
      },
      true,
    ),
  ],
});
const STOPPED = capture('2', 'stopped', {
  vlans: ['EXP_2'],
  size: 41 * 1024 ** 2,
  counters: counters({ packets: 98221 }),
});
const FAILED = capture('1', 'failed', {
  error: 'bridge mega_bridge_exp1 on compute3 has 32 mirrors',
  file: '',
});

// GET /experiments/demo/captures
const listOf = (fields = {}) => ({
  captures: [],
  experimentCaptures: [],
  support: SUPPORTED,
  budget: [],
  budgetError: '',
  experimentCapturesError: '',
  ...fields,
});

beforeEach(() => {
  resetMocks();
  clearPageCache();
});

afterEach(() => {
  forgetServer();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('the capture dialog', () => {
  const VLANS = [
    { alias: 'EXP_1', vlan: 101 },
    { alias: 'EXP_2', vlan: 102 },
  ];

  const open = (props = {}) =>
    mountComponent(ExperimentCaptureModal, {
      props: { active: true, experiment: 'exp1', vlans: VLANS, ...props },
    });

  const checked = (wrapper) =>
    wrapper
      .findAll('.capture-vlans input[type="checkbox"]')
      .filter((c) => c.element.checked)
      .map((c) => c.element.value);
  const footer = (wrapper) => buttons(wrapper.find('footer'));
  const notices = (wrapper) =>
    wrapper.findAll('.capture-budget-notice').map((n) => ({
      text: textOf(n),
      type: n
        .find('.notification')
        .classes()
        .find((c) => /^is-(warning|danger)$/.test(c)),
    }));
  const submit = async (wrapper) => {
    await wrapper.find('form').trigger('submit');
    await flushPromises();
  };

  it.each([
    ['', 'Capture all experiment traffic', []],
    ['EXP_2', 'Capture VLAN traffic', ['EXP_2']],
  ])(
    'opened on VLAN %j is titled %s, with that VLAN checked',
    async (vlan, title, want) => {
      const wrapper = await open({ vlan });
      expect(textOf(wrapper.find('h2'))).toBe(title);
      expect(checked(wrapper)).toEqual(want);
      expect(wrapper.findAll('.capture-vlans label').map(textOf)).toEqual([
        'EXP_1 (101)',
        'EXP_2 (102)',
      ]);
      expect(wrapper.find('.capture-snaplen').element.value).toBe('1600');
      expect(
        wrapper.find('input[type="radio"][value="pcapng"]').element.checked,
      ).toBe(true);
      expect(textOf(wrapper.find('.capture-trunk-note'))).toBe(
        'Frames from hardware behind a trunk are not captured.',
      );
      expect(footer(wrapper)).toEqual(['Cancel', 'Start capture (submits)']);
    },
  );

  it('offers the default file name of the VLAN and format as a placeholder', async () => {
    const wrapper = await open({ vlan: 'EXP_1' });
    const name = () => wrapper.find('.capture-name').attributes('placeholder');
    expect(name()).toMatch(/^exp1-EXP_1-\d{8}-\d{6}\.pcapng$/);
    await wrapper.find('input[type="radio"][value="pcap"]').setValue();
    await wrapper.findAll('.capture-vlans input')[1].setValue(true);
    expect(name()).toMatch(/^exp1-\d{8}-\d{6}\.pcap$/);
  });

  it('starts the capture with what was entered, then says so and closes', async () => {
    axios.post.mockResolvedValue({
      status: 201,
      data: { capture: capture('4', 'starting'), warnings: [] },
    });
    const wrapper = await open({ vlan: 'EXP_1' });
    await wrapper.findAll('.capture-vlans input')[1].setValue(true);
    await wrapper.find('.capture-filter').setValue(' tcp port 80 ');
    await wrapper.find('.capture-snaplen').setValue('9000');
    await wrapper.find('input[type="radio"][value="pcap"]').setValue();
    await wrapper.find('.capture-name').setValue('web.pcap');
    await submit(wrapper);

    expect(axios.post.mock.calls).toEqual([
      [
        'experiments/exp1/captures',
        {
          vlans: ['EXP_1', 'EXP_2'],
          filter: 'tcp port 80',
          snaplen: 9000,
          name: 'web.pcap',
          format: 'pcap',
        },
      ],
    ]);
    expect(toasts()).toEqual([
      { text: 'Started experiment capture 4.', type: 'is-success' },
    ]);
    expect(wrapper.emitted('update:active')).toEqual([[false]]);
    expect(wrapper.emitted('starting')).toEqual([[]]);
    expect(wrapper.emitted('started')).toEqual([
      [expect.objectContaining({ id: '4' })],
    ]);
  });

  it('sends all VLANs, no filter and the server default name by default', async () => {
    axios.post.mockResolvedValue({
      data: { capture: capture('5', 'running'), warnings: [] },
    });
    const wrapper = await open();
    await submit(wrapper);
    expect(axios.post.mock.lastCall[1]).toEqual({
      vlans: [],
      filter: '',
      snaplen: 1600,
      name: '',
      format: 'pcapng',
    });
  });

  it("shows each of the server's warnings in its toast", async () => {
    axios.post.mockResolvedValue({
      data: {
        capture: capture('6', 'running'),
        warnings: ['exp1 uses 18 of 32 mirrors on compute1 / mega_bridge_exp1'],
      },
    });
    await submit(await open());
    expect(toasts()).toEqual([
      {
        text: 'Started experiment capture 6. exp1 uses 18 of 32 mirrors on compute1 / mega_bridge_exp1',
        type: 'is-warning',
      },
    ]);
  });

  it('refuses a filter with a quote, saying why', async () => {
    const wrapper = await open();
    await wrapper.find('.capture-filter').setValue('host "a"');
    expect(textOf(wrapper.find('.capture-filter-error'))).toContain(
      'Use only letters',
    );
    expect(wrapper.find('.capture-filter').attributes('aria-invalid')).toBe(
      'true',
    );
    expect(footer(wrapper)).toContain('Start capture (submits) (disabled)');
    await submit(wrapper);
    expect(axios.post).not.toHaveBeenCalled();
  });

  it('refuses a snaplen out of range, saying why', async () => {
    const wrapper = await open();
    await wrapper.find('.capture-snaplen').setValue('13');
    expect(textOf(wrapper.find('.capture-snaplen-error'))).toBe(
      'Use a whole number from 14 to 65535.',
    );
    expect(footer(wrapper)).toContain('Start capture (submits) (disabled)');
  });

  it("shows the server's answer in the dialog and stays open", async () => {
    const message =
      'compute1 / mega_bridge_exp1 has 32 of 32 Open vSwitch mirrors';
    axios.post.mockRejectedValue(httpError(409, { message }));
    const wrapper = await open();
    await submit(wrapper);
    expect(textOf(wrapper.find('.capture-start-error'))).toBe(message);
    expect(wrapper.find('.capture-start-error').attributes('role')).toBe(
      'alert',
    );
    expect(wrapper.emitted('update:active')).toBeUndefined();
    expect(wrapper.emitted('started')).toEqual([[null]]);
    expect(notify.error).not.toHaveBeenCalled();
    expect(toasts()).toEqual([]);
  });

  const budgetRow = (total, experiment) => ({
    host: 'compute1',
    bridge: 'mega_bridge_exp1',
    total,
    experiment,
    limit: 32,
    level: total >= 32 ? 'full' : 'ok',
  });

  it('refuses a start that needs a 33rd mirror', async () => {
    const wrapper = await open({ budget: [budgetRow(32, 21)] });
    expect(notices(wrapper)).toEqual([
      {
        text: 'compute1 / mega_bridge_exp1 has 32 of 32 Open vSwitch mirrors. Stop a capture or a tap mirror on that host to free one. exp1 uses 21; 11 are from other experiments or tools.',
        type: 'is-danger',
      },
    ]);
    expect(footer(wrapper)).toEqual([
      'Close',
      'Start capture (submits) (disabled)',
    ]);
    await submit(wrapper);
    expect(axios.post).not.toHaveBeenCalled();
  });

  it('refuses a start of all VLANs again once no VLAN is selected', async () => {
    const wrapper = await open({ budget: [budgetRow(32, 21)] });
    const vlan = wrapper.findAll('.capture-vlans input')[0];
    await vlan.setValue(true);
    expect(notices(wrapper)).toEqual([
      {
        text: 'compute1 / mega_bridge_exp1 has 32 of 32 Open vSwitch mirrors. The start may be refused when the selected VLANs use that bridge. Stop a capture or a tap mirror on that host to free one. exp1 uses 21; 11 are from other experiments or tools.',
        type: 'is-warning',
      },
    ]);
    expect(footer(wrapper)).toEqual(['Cancel', 'Start capture (submits)']);
    await vlan.setValue(false);
    expect(notices(wrapper).map((n) => n.type)).toEqual(['is-danger']);
    expect(footer(wrapper)).toEqual([
      'Close',
      'Start capture (submits) (disabled)',
    ]);
  });

  it.each([
    ['', []],
    ['EXP_9', ['EXP_9']],
  ])(
    'opened on VLAN %j without a VLAN list offers %j, checked',
    async (vlan, labels) => {
      const wrapper = await mountComponent(ExperimentCaptureModal, {
        props: { active: true, experiment: 'exp1', vlan },
      });
      expect(wrapper.findAll('.capture-vlans label').map(textOf)).toEqual(
        labels,
      );
      expect(checked(wrapper)).toEqual(labels);
      expect(textOf(wrapper.find('.capture-vlans-field .help'))).toBe(
        'Empty means all experiment VLANs.',
      );
    },
  );

  it('closes from Cancel, starting nothing', async () => {
    const wrapper = await open();
    await wrapper.find('footer button[type="button"]').trigger('click');
    await flushPromises();
    expect(wrapper.emitted('update:active')).toEqual([[false]]);
    expect(wrapper.emitted('starting')).toBeUndefined();
    expect(axios.post).not.toHaveBeenCalled();
  });

  it('says it started a capture whose answer names none', async () => {
    axios.post.mockResolvedValue({ status: 201 });
    const wrapper = await open();
    await submit(wrapper);
    expect(toasts()).toEqual([
      { text: 'Started experiment capture.', type: 'is-success' },
    ]);
    expect(wrapper.emitted('update:active')).toEqual([[false]]);
    expect(wrapper.emitted('started')).toEqual([[null]]);
  });

  it('opens again with an empty form', async () => {
    const wrapper = await open();
    await wrapper.find('.capture-filter').setValue('tcp');
    await wrapper.setProps({ active: false });
    await wrapper.setProps({ active: true, vlan: 'EXP_1' });
    expect(wrapper.find('.capture-filter').element.value).toBe('');
    expect(checked(wrapper)).toEqual(['EXP_1']);
  });
});

describe('the format dialog', () => {
  it('offers pcapng first and emits the format picked', async () => {
    const wrapper = await mountComponent(CaptureFormatModal, {
      props: { active: true, title: 'Start capture on interface 0 of web' },
    });
    expect(textOf(wrapper.find('h2'))).toBe(
      'Start capture on interface 0 of web',
    );
    expect(
      wrapper.find('input[type="radio"][value="pcapng"]').element.checked,
    ).toBe(true);
    await wrapper.find('input[type="radio"][value="pcap"]').setValue();
    await wrapper.find('form').trigger('submit');
    expect(wrapper.emitted('start')).toEqual([['pcap']]);
    expect(wrapper.emitted('update:active')).toEqual([[false]]);
  });
});

describe('the format dialog, cancelled', () => {
  it('closes from Cancel, emitting no format, and opens again on pcapng', async () => {
    const wrapper = await mountComponent(CaptureFormatModal, {
      props: { active: true, title: 'Start interface captures on VLAN DMZ' },
    });
    await wrapper.find('input[type="radio"][value="pcap"]').setValue();
    await wrapper.find('footer button[type="button"]').trigger('click');
    await flushPromises();
    expect(wrapper.emitted('update:active')).toEqual([[false]]);
    expect(wrapper.emitted('start')).toBeUndefined();

    await wrapper.setProps({ active: false });
    await wrapper.setProps({ active: true });
    expect(
      wrapper.find('input[type="radio"][value="pcapng"]').element.checked,
    ).toBe(true);
  });
});

describe('the Captures tab', () => {
  const mountTab = (props = {}) =>
    mountComponent(ExperimentCapturesTab, {
      route: '/experiment/demo',
      props: {
        experiment: 'demo',
        active: true,
        captures: [RUNNING, STOPPED, FAILED],
        support: SUPPORTED,
        loaded: true,
        ...props,
      },
    });

  const cells = (wrapper) =>
    tableRows(wrapper).map((row) =>
      Object.fromEntries(
        ['ID', 'Status', 'Scope', 'Size', 'Packets', 'Drops'].map((k) => [
          k,
          textOf(row[k]),
        ]),
      ),
    );
  const actions = (wrapper, id) => {
    const row = tableRows(wrapper).find((r) => textOf(r.ID) === id);
    return [...row.Actions.findAll('a, button')].map(textOf);
  };

  beforeEach(() => {
    store.features = ['webshark'];
  });

  it('lists each capture with its status, scope, size and counts', async () => {
    const wrapper = await mountTab();
    expect(textOf(wrapper.find('.captures-intro'))).toBe(
      'Experiment captures merge the traffic of all hosts into one file on the head node.',
    );
    expect(cells(wrapper)).toEqual([
      {
        ID: '3',
        Status: 'Running',
        Scope: 'All VLANs',
        Size: '812.4 MiB',
        Packets: '1,284,113',
        Drops: '12',
      },
      {
        ID: '2',
        Status: 'Stopped',
        Scope: 'EXP_2',
        Size: '41.0 MiB',
        Packets: '98,221',
        Drops: '0',
      },
      {
        ID: '1',
        Status: 'Failed',
        Scope: 'bridge mega_bridge_exp1 on compute3 has 32 mirrors',
        Size: '0 B',
        Packets: '0',
        Drops: '0',
      },
    ]);
    // running is green, stopped the neutral tag, failed red
    const tags = wrapper.findAll('.capture-state').map((t) => t.classes());
    expect(tags[0]).toContain('is-success');
    expect(tags[0]).not.toContain('is-danger');
    expect(tags[1]).not.toContain('is-success');
    expect(tags[1]).not.toContain('is-danger');
    expect(tags[2]).toContain('is-danger');
  });

  it("shows a capture's hosts under its row, with a warning for a large skew", async () => {
    const wrapper = await mountTab();
    const toggle = wrapper.find('.streams-toggle');
    expect(toggle.attributes()).toMatchObject({
      'aria-label': 'Hosts of capture 3',
      'aria-expanded': 'false',
    });
    // a capture without streams has nothing to open
    expect(wrapper.findAll('.streams-toggle')).toHaveLength(1);

    await toggle.trigger('click');
    expect(toggle.attributes('aria-expanded')).toBe('true');
    const rows = wrapper
      .findAll('.capture-streams tbody tr')
      .map((tr) => tr.findAll('td').map(textOf));
    expect(rows).toEqual([
      [
        'compute1',
        'mega_bridge_exp1',
        '6',
        '512,402',
        '4',
        '0',
        '21',
        '80 ms',
        '+0.3 ms',
      ],
      [
        'compute3',
        'mega_bridge_exp1',
        '3',
        '272,831',
        '8',
        '5',
        '1,904',
        '70 ms',
        '+612 ms (warning: more than half the reorder window)',
      ],
    ]);
    expect(wrapper.find('.skew-warning [data-icon]').exists()).toBe(true);
  });

  it('offers the actions of each state', async () => {
    const wrapper = await mountTab();
    expect(actions(wrapper, '3')).toEqual(['Open in WebShark', 'Stop']);
    expect(actions(wrapper, '2')).toEqual([
      'Open in WebShark',
      'Download',
      'Delete',
    ]);
    expect(actions(wrapper, '1')).toEqual([]);
    const failedWithFile = await mountTab({
      captures: [{ ...FAILED, file: 'demo-1.pcapng' }],
    });
    expect(actions(failedWithFile, '1')).toEqual(['Delete']);
  });

  it.each([
    ['Stop', '3', ['experiments/captures', 'delete']],
    ['Download', '2', ['experiments/files', 'get']],
    ['Delete', '2', ['experiments/files', 'delete']],
    ['Open in WebShark', '3', ['experiments/captures', 'get']],
    ['Open in WebShark', '2', ['experiments/files', 'get']],
    [
      'Capture all experiment traffic',
      null,
      ['experiments/captures', 'create'],
    ],
  ])('offers %s (capture %s) only with %j', async (action, id, perm) => {
    const shown = async () => {
      const wrapper = await mountTab();
      return id === null
        ? buttons(wrapper).includes(action)
        : actions(wrapper, id).includes(action);
    };
    expect(await shown()).toBe(true);
    rbac.allowed = denyOnly(perm);
    expect(await shown()).toBe(false);
  });

  it('offers no Open in WebShark without WebShark', async () => {
    store.features = [];
    const wrapper = await mountTab();
    expect(actions(wrapper, '3')).toEqual(['Stop']);
  });

  it('opens WebShark on the capture', async () => {
    const wrapper = await mountTab();
    expect(wrapper.find('.capture-webshark').attributes('href')).toBe(
      '/packets?exp=demo&capture=3',
    );
  });

  it('stops a running capture without asking, spinning until it stops', async () => {
    const stop = Promise.withResolvers();
    axios.delete.mockReturnValue(stop.promise);
    const wrapper = await mountTab();
    await wrapper.find('.capture-stop').trigger('click');
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(axios.delete).toHaveBeenCalledWith('experiments/demo/captures/3');
    expect(wrapper.emitted('stopping')).toEqual([['3']]);
    expect(wrapper.find('.capture-stop').classes()).toContain('is-loading');

    stop.resolve({ data: RUNNING });
    await flushPromises();
    expect(wrapper.find('.capture-stop').classes()).not.toContain('is-loading');
    expect(toasts()).toEqual([
      { text: 'Stopped experiment capture 3.', type: 'is-success' },
    ]);
    expect(wrapper.emitted('reload')).toEqual([[]]);
  });

  it('says why a stop failed', async () => {
    const err = httpError(404, { message: 'no capture 3' });
    axios.delete.mockRejectedValue(err);
    const wrapper = await mountTab();
    await wrapper.find('.capture-stop').trigger('click');
    await flushPromises();
    expect(notify.error).toHaveBeenCalledWith(err);
  });

  it("deletes a stopped capture's file after asking, as the Files tab does", async () => {
    axios.delete.mockResolvedValue({});
    const wrapper = await mountTab({ attachTo: document.body });
    await wrapper.find('.capture-delete').trigger('click');
    await flushPromises();
    expect(dialogs()).toHaveLength(1);
    expect(textOf(dialogs()[0])).toContain(
      'Delete demo-2.pcapng from experiment demo?',
    );
    expect(axios.delete).not.toHaveBeenCalled();

    await confirmDialog();
    expect(axios.delete).toHaveBeenCalledWith(
      'experiments/demo/files/demo-2.pcapng',
      { params: { path: 'demo-2.pcapng' } },
    );
    expect(toasts()).toEqual([
      { text: 'Deleted demo-2.pcapng', type: 'is-success' },
    ]);
    expect(wrapper.emitted('reload')).toEqual([[]]);
  });

  it("downloads a stopped capture's file", async () => {
    const blob = new Blob(['x']);
    axios.get.mockResolvedValue({ data: blob });
    const wrapper = await mountTab();
    await wrapper.find('.capture-download').trigger('click');
    await flushPromises();
    expect(axios.get).toHaveBeenCalledWith(
      'experiments/demo/files/demo-2.pcapng',
      { params: { path: 'demo-2.pcapng' }, responseType: 'blob' },
    );
    expect(saver.saveAs).toHaveBeenCalledWith(blob, 'demo-2.pcapng');
  });

  it.each([
    ['as minimega gives no size', '0'],
    ['of a size that is not a number', 'big'],
    ['larger than 256 MiB', String(300 * 1024 ** 2)],
  ])(
    "downloads a stopped capture's file %s through a link",
    async (_, size) => {
      vi.useFakeTimers({ toFake: ['setTimeout'] });
      const links = [];
      vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
        function () {
          links.push(this.getAttribute('href'));
        },
      );
      const wrapper = await mountTab({
        active: false,
        captures: [{ ...STOPPED, size }],
      });
      await wrapper.find('.capture-download').trigger('click');
      await flushPromises();
      expect(axios.get).not.toHaveBeenCalled();
      expect(links).toEqual([
        expect.stringMatching(
          /api\/v1\/experiments\/demo\/files\/demo-2\.pcapng\?path=demo-2\.pcapng/,
        ),
      ]);
      vi.advanceTimersByTime(2000);
      vi.useRealTimers();
    },
  );

  it('lists none and shows a capture with no state or size as unknown', async () => {
    const empty = await mountComponent(ExperimentCapturesTab, {
      props: { experiment: 'demo', support: SUPPORTED, loaded: true },
    });
    expect(textOf(empty.find('tbody'))).toBe(
      'This experiment has no experiment captures',
    );
    const bare = capture('7', '');
    delete bare.state;
    delete bare.size;
    const wrapper = await mountTab({ captures: [bare] });
    expect(cells(wrapper)).toEqual([
      {
        ID: '7',
        Status: 'Unknown',
        Scope: 'All VLANs',
        Size: '0 B',
        Packets: '0',
        Drops: '0',
      },
    ]);
  });

  it("closes a capture's hosts again from its button", async () => {
    const wrapper = await mountTab();
    const toggle = wrapper.find('.streams-toggle');
    await toggle.trigger('click');
    expect(wrapper.findAll('.capture-streams tbody tr')).toHaveLength(2);
    await toggle.trigger('click');
    expect(toggle.attributes('aria-expanded')).toBe('false');
    expect(wrapper.find('.capture-streams').exists()).toBe(false);
  });

  it('offers nothing for a stopped capture that left no file', async () => {
    const wrapper = await mountTab({ captures: [{ ...STOPPED, file: '' }] });
    expect(actions(wrapper, '2')).toEqual([]);
  });

  it('sends one stop while a stop waits', async () => {
    const stop = Promise.withResolvers();
    axios.delete.mockReturnValue(stop.promise);
    const wrapper = await mountTab();
    await wrapper.find('.capture-stop').trigger('click');
    await wrapper.find('.capture-stop').trigger('click');
    await flushPromises();
    expect(axios.delete).toHaveBeenCalledTimes(1);
    expect(wrapper.emitted('stopping')).toEqual([['3']]);
    stop.resolve({ data: RUNNING });
    await flushPromises();
    expect(wrapper.emitted('reload')).toEqual([[]]);
  });

  it('says and asks nothing once left before a stop is answered', async () => {
    const stop = Promise.withResolvers();
    axios.delete.mockReturnValue(stop.promise);
    const wrapper = await mountTab({ active: false });
    await wrapper.find('.capture-stop').trigger('click');
    await flushPromises();
    wrapper.unmount();
    stop.resolve({ data: RUNNING });
    await flushPromises();
    expect(toasts()).toEqual([]);
    expect(wrapper.emitted('reload')).toBeUndefined();
  });

  it('sends one download while a download waits, and says why it failed', async () => {
    const got = Promise.withResolvers();
    axios.get.mockReturnValue(got.promise);
    const wrapper = await mountTab({ active: false });
    await wrapper.find('.capture-download').trigger('click');
    await wrapper.find('.capture-download').trigger('click');
    await flushPromises();
    expect(axios.get).toHaveBeenCalledTimes(1);
    expect(wrapper.find('.capture-download').classes()).toContain('is-loading');

    const err = httpError(404, { message: 'no file demo-2.pcapng' });
    got.reject(err);
    await flushPromises();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(saver.saveAs).not.toHaveBeenCalled();
    expect(wrapper.find('.capture-download').classes()).not.toContain(
      'is-loading',
    );
  });

  it("names the folder of a capture's file when it asks to delete it", async () => {
    const wrapper = await mountTab({
      attachTo: document.body,
      captures: [{ ...STOPPED, file: 'caps/demo-2.pcapng' }],
    });
    await wrapper.find('.capture-delete').trigger('click');
    await flushPromises();
    expect(textOf(dialogs()[0])).toContain(
      'Delete demo-2.pcapng (caps/demo-2.pcapng) from experiment demo?',
    );
  });

  it('sends one delete while a delete waits, and says why it failed', async () => {
    const gone = Promise.withResolvers();
    axios.delete.mockReturnValue(gone.promise);
    const wrapper = await mountTab({ active: false, attachTo: document.body });
    await wrapper.find('.capture-delete').trigger('click');
    await flushPromises();
    await confirmDialog();
    await wrapper.find('.capture-delete').trigger('click');
    await flushPromises();
    await confirmDialog();
    expect(axios.delete).toHaveBeenCalledTimes(1);

    const err = httpError(500, { message: 'rm failed' });
    gone.reject(err);
    await flushPromises();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(toasts()).toEqual([]);
    expect(wrapper.emitted('reload')).toEqual([[]]);
  });

  it('says and asks nothing once left before a delete is answered', async () => {
    const gone = Promise.withResolvers();
    axios.delete.mockReturnValue(gone.promise);
    const wrapper = await mountTab({ active: false, attachTo: document.body });
    await wrapper.find('.capture-delete').trigger('click');
    await flushPromises();
    await confirmDialog();
    wrapper.unmount();
    gone.resolve({});
    await flushPromises();
    expect(toasts()).toEqual([]);
    expect(wrapper.emitted('reload')).toBeUndefined();
  });

  it('shows a banner at the full budget level', async () => {
    const budget = [
      {
        host: 'compute1',
        bridge: 'mega_bridge_exp1',
        limit: 32,
        level: 'full',
        total: 32,
        experiment: 21,
      },
    ];
    const banner = (await mountTab({ budget })).find('.captures-budget');
    expect(textOf(banner)).toBe(
      'compute1 / mega_bridge_exp1 has 32 of 32 Open vSwitch mirrors; none are left. demo uses 21 of them, other experiments and tools 11. A capture that needs a 33rd mirror is refused.',
    );
    expect(banner.find('.notification').classes()).toContain('is-danger');
  });

  it('shows no banner at the ok level, and says when it cannot count mirrors', async () => {
    const wrapper = await mountTab({
      budget: [
        {
          host: 'c1',
          bridge: 'b',
          total: 2,
          experiment: 1,
          limit: 32,
          level: 'ok',
        },
      ],
      budgetError: 'mesh send failed',
    });
    expect(wrapper.find('.captures-budget').exists()).toBe(false);
    expect(textOf(wrapper.find('.captures-budget-error'))).toBe(
      'The Open vSwitch mirrors cannot be counted: mesh send failed',
    );
  });

  it('greys out its capture button and says why while minimega lacks support', async () => {
    const wrapper = await mountTab({
      support: { available: false, reason: OLD },
      captures: [],
    });
    expect(textOf(wrapper.find('.captures-unsupported'))).toBe(
      'Experiment captures are not available. Requires minimega 3.4.0 or later; the head node runs 3.3.0. VM interface captures still work, without the new options.',
    );
    const [button] = tooltipButtons(wrapper);
    expect(button).toMatchObject({
      tooltip: `Capture all experiment traffic is not available: ${OLD}`,
      disabled: true,
    });
    // the reason reaches a screen reader too, not only the tooltip
    const reason = wrapper.find(
      `#${button.control.attributes('aria-describedby')}`,
    );
    expect(textOf(reason)).toBe(
      `Capture all experiment traffic is not available: ${OLD}`,
    );
    await button.control.trigger('click');
    expect(wrapper.emitted('capture')).toBeUndefined();
  });

  it('asks for the capture dialog from its button', async () => {
    const wrapper = await mountTab();
    expect(
      wrapper.find('.captures-new').attributes('aria-describedby'),
    ).toBeUndefined();
    await wrapper.find('.captures-new').trigger('click');
    expect(wrapper.emitted('capture')).toEqual([[]]);
  });

  it('says why the list could not be read, and that there is none', async () => {
    const wrapper = await mountTab({
      captures: [],
      error: 'minimega timed out',
    });
    expect(textOf(wrapper.find('.captures-error'))).toBe(
      'Could not read the captures: minimega timed out',
    );
    expect(textOf(wrapper.find('tbody'))).toBe(
      'This experiment has no experiment captures',
    );
  });

  describe('polling', () => {
    const pollMs = RESOURCES.captures.pollMs;

    beforeEach(() => {
      vi.useFakeTimers();
      vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
    });

    const reloads = (wrapper) => wrapper.emitted('reload')?.length ?? 0;

    it('asks for the list every 5 s while it shows', async () => {
      expect(pollMs).toBe(5000);
      const wrapper = await mountTab();
      vi.advanceTimersByTime(3 * pollMs);
      expect(reloads(wrapper)).toBe(3);
    });

    it('asks for the list without support too, to see the support start', async () => {
      const wrapper = await mountTab({
        support: { available: false, reason: OLD },
        captures: [],
      });
      vi.advanceTimersByTime(3 * pollMs);
      expect(reloads(wrapper)).toBe(3);
    });

    it.each([
      ['while another tab shows', { active: false }],
      ['while a load waits', { loading: true }],
    ])('asks for nothing %s', async (_, props) => {
      const wrapper = await mountTab(props);
      vi.advanceTimersByTime(3 * pollMs);
      expect(reloads(wrapper)).toBe(0);
    });

    it('asks for nothing while the browser tab is hidden', async () => {
      vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
      const wrapper = await mountTab();
      vi.advanceTimersByTime(3 * pollMs);
      expect(reloads(wrapper)).toBe(0);
    });

    it('lists at once when shown, then polls, and stops when hidden or left', async () => {
      const wrapper = await mountTab({ active: false });
      await wrapper.setProps({ active: true });
      expect(reloads(wrapper)).toBe(1);
      vi.advanceTimersByTime(pollMs);
      expect(reloads(wrapper)).toBe(2);

      await wrapper.setProps({ active: false });
      vi.advanceTimersByTime(3 * pollMs);
      expect(reloads(wrapper)).toBe(2);

      await wrapper.setProps({ active: true });
      const emitted = wrapper.emitted('reload');
      expect(emitted).toHaveLength(3);
      wrapper.unmount();
      vi.advanceTimersByTime(3 * pollMs);
      expect(emitted).toHaveLength(3);
    });
  });
});

describe('the running experiment page', () => {
  const web = vm({ networks: ['DMZ (149)', 'MGMT (151)'] });
  const LIST_URL = 'experiments/demo/captures';

  // answers the page's GETs: the capture list with `list` (a function, so a
  // test can fail it), a VM's captures with none, and fails the rest
  function answerGets(list) {
    axios.get.mockImplementation(async (url) => {
      if (url === LIST_URL) return { data: list() };
      if (url === 'experiments/demo/vms/web/captures') {
        return { data: { captures: [] } };
      }
      throw new Error(`no answer for ${url}`);
    });
  }

  const openPage = (list = listOf(), { tab } = {}) => {
    answerGets(typeof list === 'function' ? list : () => list);
    return mountPage(RunningExperiment, {
      route: tab ? `/experiment/demo?tab=${tab}` : '/experiment/demo',
      data: {
        experiment: {
          name: 'demo',
          running: true,
          vms: [{ ...web }],
          vlans: [
            { alias: 'DMZ', vlan: 149 },
            { alias: 'MGMT', vlan: 151 },
          ],
        },
      },
      attachTo: document.body,
    });
  };

  const listReads = () =>
    axios.get.mock.calls.filter(([url]) => url === LIST_URL).length;
  const toolbarButton = (page) => page.find('.experiment-capture-button');
  const captureDialog = (page) => page.findComponent(ExperimentCaptureModal);
  const formatDialog = (page) => page.findComponent(CaptureFormatModal);

  async function handle(msg) {
    for (const handler of ws.handlers) handler(msg);
    await flushPromises();
  }

  // picks an item from the network menu of web's interface
  async function pickNetworkItem(page, iface, item) {
    const row = tableRows(page).find(
      (cells) => textOf(cells.Node.find('button.tag')) === 'web',
    );
    await row.Network.findAll('.vm-network-trigger')[iface].trigger('click');
    await flushPromises();
    // Vue ignores a click in the millisecond its handler was attached
    await new Promise((resolve) => setTimeout(resolve, 2));
    document.querySelector(`.vm-network-${item}`).click();
    await flushPromises();
  }

  it('reads the capture list once as it opens', async () => {
    await openPage();
    expect(listReads()).toBe(1);
  });

  it('opens the capture dialog on all VLANs from its toolbar button', async () => {
    const page = await openPage();
    expect(toolbarButton(page).attributes('aria-label')).toBe(
      'Capture all experiment traffic',
    );
    expect(toolbarButton(page).attributes('aria-disabled')).toBeUndefined();
    expect(toolbarButton(page).attributes('aria-describedby')).toBeUndefined();
    await toolbarButton(page).trigger('click');
    await flushPromises();
    expect(captureDialog(page).props()).toMatchObject({
      active: true,
      vlan: '',
      experiment: 'demo',
    });
    // the budget is read again for the dialog
    expect(listReads()).toBe(2);
  });

  it.each([
    [
      'the list says minimega lacks support',
      listOf({ support: UNSUPPORTED }),
      [],
      OLD,
    ],
    [
      'the list cannot be read and the features lack it',
      () => {
        throw httpError(500, 'boom');
      },
      ['minimega-captures-not'],
      'the server did not report capture support',
    ],
  ])(
    'greys out the toolbar button, saying why, when %s',
    async (_, list, features, reason) => {
      store.features = features;
      const page = await openPage(list);
      expect(toolbarButton(page).attributes()).toMatchObject({
        'aria-label': 'Capture all experiment traffic (not available)',
        'aria-disabled': 'true',
      });
      const tip = page
        .findAllComponents({ name: 'BTooltip' })
        .find((t) => t.find('.experiment-capture-button').exists());
      expect(textOf(tip.vm.$refs.content)).toBe(
        `Capture all experiment traffic is not available: ${reason}`,
      );
      // the reason reaches a screen reader too, not only the tooltip
      const described = page.find(
        `#${toolbarButton(page).attributes('aria-describedby')}`,
      );
      expect(textOf(described)).toBe(
        `Capture all experiment traffic is not available: ${reason}`,
      );
      await toolbarButton(page).trigger('click');
      expect(captureDialog(page).props('active')).toBe(false);
      expect(notify.error).not.toHaveBeenCalled();
    },
  );

  it('takes the support from the features when the list cannot be read', async () => {
    store.features = ['minimega-captures'];
    const page = await openPage(() => {
      throw httpError(500, 'boom');
    });
    expect(toolbarButton(page).attributes('aria-disabled')).toBeUndefined();
  });

  it.each([
    [
      'the toolbar button',
      ['experiments/captures', 'create'],
      (page) => toolbarButton(page).exists(),
    ],
    [
      'the Captures tab',
      ['experiments/captures', 'list'],
      (page) =>
        page
          .findAll('.tabs li')
          .some((li) => textOf(li).startsWith('Captures')),
    ],
  ])('shows %s only with %j', async (_, perm, shown) => {
    rbac.allowed = allowAll;
    expect(shown(await openPage())).toBe(true);
    document.body.innerHTML = '';
    rbac.allowed = denyOnly(perm);
    expect(shown(await openPage())).toBe(false);
  });

  it('counts the running captures on the Captures tab', async () => {
    const page = await openPage(
      listOf({
        experimentCaptures: [RUNNING, STOPPED, capture('4', 'starting')],
      }),
    );
    const tab = page
      .findAll('.tabs li')
      .find((li) => textOf(li).startsWith('Captures'));
    expect(textOf(tab)).toBe('Captures2, 2 running');
    expect(tab.find('.captures-count [aria-hidden="true"]').text()).toBe('2');
    expect(tab.find('.captures-count .is-sr-only').text()).toBe(', 2 running');
    expect(tab.find('.captures-count').classes()).toContain('is-success');
  });

  describe('while another tab shows', () => {
    const pollMs = RESOURCES.captureCount.pollMs;
    const capturesTab = (page) =>
      textOf(
        page
          .findAll('.tabs li')
          .find((li) => textOf(li).startsWith('Captures')),
      );
    const wait = async (ms) => {
      vi.advanceTimersByTime(ms);
      await flushPromises();
    };

    beforeEach(() => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
      vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
    });

    it('reads the list every 30 s while a capture runs, to keep the count', async () => {
      expect(pollMs).toBe(30000);
      let list = listOf({ experimentCaptures: [RUNNING] });
      const page = await openPage(() => list);
      expect(capturesTab(page)).toBe('Captures1, 1 running');

      list = listOf({
        experimentCaptures: [{ ...RUNNING, state: 'full', running: false }],
      });
      await wait(pollMs - 1);
      expect(listReads()).toBe(1);
      await wait(1);
      expect(listReads()).toBe(2);
      expect(capturesTab(page)).toBe('Captures');

      await wait(3 * pollMs);
      expect(listReads()).toBe(2);
    });

    it.each([
      ['while no capture runs', listOf({ experimentCaptures: [STOPPED] }), ''],
      [
        'while the Captures tab shows, which polls itself',
        listOf({ experimentCaptures: [RUNNING] }),
        'captures',
      ],
    ])('reads nothing more %s', async (_, list, tab) => {
      await openPage(list, { tab });
      const reads = listReads();
      await wait(3 * pollMs);
      const tabPolls = tab ? (3 * pollMs) / RESOURCES.captures.pollMs : 0;
      expect(listReads()).toBe(reads + tabPolls);
    });

    it('reads nothing more once left', async () => {
      const page = await openPage(listOf({ experimentCaptures: [RUNNING] }));
      page.unmount();
      await wait(3 * pollMs);
      expect(listReads()).toBe(1);
    });
  });

  it('opens the Captures tab from a link, showing the list', async () => {
    const page = await openPage(listOf({ experimentCaptures: [RUNNING] }), {
      tab: 'captures',
    });
    const tab = page.findComponent(ExperimentCapturesTab);
    expect(tab.props('active')).toBe(true);
    expect(tableRows(tab).map((r) => textOf(r.ID))).toEqual(['3']);
  });

  it('lists the captures again on a capture event, with a toast for one it did not start', async () => {
    const page = await openPage();
    await handle(wsMsg('experiment/capture', 'demo/7', 'start', {}));
    expect(listReads()).toBe(2);
    expect(toasts()).toEqual([
      { text: 'Experiment capture 7 started.', type: 'is-info' },
    ]);
    await handle(wsMsg('experiment/capture', 'other/7', 'stop', {}));
    expect(listReads()).toBe(2);
    expect(page.exists()).toBe(true);
  });

  it('lists the captures again, with no toast, when the experiment stops them all', async () => {
    await openPage();
    await handle(wsMsg('experiment/capture', 'demo/*', 'stop', null));
    expect(listReads()).toBe(2);
    expect(toasts()).toEqual([]);
  });

  it('says why the experiment captures could not be listed', async () => {
    const page = await openPage(
      listOf({ experimentCapturesError: 'minimega timed out' }),
      { tab: 'captures' },
    );
    const tab = page.findComponent(ExperimentCapturesTab);
    expect(textOf(tab.find('.captures-error'))).toBe(
      'Could not read the captures: minimega timed out',
    );
  });

  it('lists the captures again after the websocket reconnects', async () => {
    let list = listOf({ support: UNSUPPORTED });
    const page = await openPage(() => list);
    expect(toolbarButton(page).attributes('aria-disabled')).toBe('true');

    list = listOf({ experimentCaptures: [RUNNING] });
    ws.reconnect.forEach((f) => f());
    await flushPromises();
    expect(listReads()).toBe(2);
    expect(toolbarButton(page).attributes('aria-disabled')).toBeUndefined();
    const tab = page
      .findAll('.tabs li')
      .find((li) => textOf(li).startsWith('Captures'));
    expect(textOf(tab)).toBe('Captures1, 1 running');
  });

  it('lists no captures after a reconnect for a role that may not list them', async () => {
    rbac.allowed = denyOnly(['experiments/captures', 'list']);
    await openPage();
    ws.reconnect.forEach((f) => f());
    await flushPromises();
    expect(listReads()).toBe(0);
  });

  it('shows no toast for the capture events of its own start and stop', async () => {
    const started = Promise.withResolvers();
    axios.post.mockReturnValue(started.promise);
    const page = await openPage(listOf({ experimentCaptures: [RUNNING] }), {
      tab: 'captures',
    });
    await toolbarButton(page).trigger('click');
    await flushPromises();
    await captureDialog(page).find('form').trigger('submit');
    // the server tells of the start before it answers
    await handle(wsMsg('experiment/capture', 'demo/8', 'start', {}));
    started.resolve({
      data: { capture: capture('8', 'running'), warnings: [] },
    });
    await flushPromises();
    await handle(wsMsg('experiment/capture', 'demo/8', 'start', {}));

    axios.delete.mockResolvedValue({ data: RUNNING });
    await page.find('.capture-stop').trigger('click');
    await handle(wsMsg('experiment/capture', 'demo/3', 'stop', {}));
    await flushPromises();
    expect(toasts().map((t) => t.text)).toEqual([
      'Stopped experiment capture 3.',
      'Started experiment capture 8.',
    ]);
  });

  it('opens the capture dialog on all VLANs from the Captures tab', async () => {
    const page = await openPage(listOf(), { tab: 'captures' });
    const reads = listReads();
    await page
      .findComponent(ExperimentCapturesTab)
      .find('.captures-new')
      .trigger('click');
    await flushPromises();
    expect(captureDialog(page).props()).toMatchObject({
      active: true,
      vlan: '',
    });
    expect(textOf(captureDialog(page).find('h2'))).toBe(
      'Capture all experiment traffic',
    );
    expect(listReads()).toBe(reads + 1);
  });

  it('opens no capture dialog for a VM table request while minimega cannot capture', async () => {
    const page = await openPage(listOf({ support: UNSUPPORTED }));
    const reads = listReads();
    page.findComponent(VmTable).vm.$emit('capture-experiment', 'DMZ');
    await flushPromises();
    expect(captureDialog(page).props('active')).toBe(false);
    expect(listReads()).toBe(reads);
  });

  it('opens the capture dialog on all VLANs for a VM table request that names none', async () => {
    const page = await openPage();
    page.findComponent(VmTable).vm.$emit('capture-experiment');
    await flushPromises();
    expect(captureDialog(page).props()).toMatchObject({
      active: true,
      vlan: '',
    });
  });

  it('starts nothing for a format the format dialog sends with nothing to start', async () => {
    const page = await openPage();
    formatDialog(page).vm.$emit('start', 'pcap');
    await flushPromises();
    expect(axios.post).not.toHaveBeenCalled();
    expect(notify.error).not.toHaveBeenCalled();
  });

  it.each([
    [['minimega-captures'], undefined],
    [[], 'true'],
  ])(
    'takes the support from the features %j when the list answers nothing',
    async (features, disabled) => {
      store.features = features;
      const page = await openPage(() => undefined, { tab: 'captures' });
      expect(toolbarButton(page).attributes('aria-disabled')).toBe(disabled);
      const tab = page.findComponent(ExperimentCapturesTab);
      expect(textOf(tab.find('tbody'))).toBe(
        'This experiment has no experiment captures',
      );
      expect(tab.find('.captures-error').exists()).toBe(false);
    },
  );

  it('keeps the newest list when an older request fails after it', async () => {
    const page = await openPage(listOf(), { tab: 'captures' });
    const older = Promise.withResolvers();
    let reads = 0;
    axios.get.mockImplementation(async (url) => {
      expect(url).toBe(LIST_URL);
      reads += 1;
      if (reads === 1) return older.promise;
      return { data: listOf({ experimentCaptures: [RUNNING] }) };
    });
    await handle(wsMsg('experiment/capture', 'demo/7', 'start', {}));
    await handle(wsMsg('experiment/capture', 'demo/8', 'start', {}));
    older.reject(httpError(500, { message: 'minimega timed out' }));
    await flushPromises();

    const tab = page.findComponent(ExperimentCapturesTab);
    expect(tab.find('.captures-error').exists()).toBe(false);
    expect(tableRows(tab).map((r) => textOf(r.ID))).toEqual(['3']);
    expect(tab.props('loading')).toBe(false);
  });

  it('lists nothing again after a failed start, and toasts the next start it did not make', async () => {
    axios.post.mockRejectedValue(
      httpError(409, { message: 'no mirror is left' }),
    );
    const page = await openPage();
    await toolbarButton(page).trigger('click');
    await flushPromises();
    const reads = listReads();
    await captureDialog(page).find('form').trigger('submit');
    await flushPromises();
    expect(textOf(captureDialog(page).find('.capture-start-error'))).toBe(
      'no mirror is left',
    );
    expect(listReads()).toBe(reads);

    await handle(wsMsg('experiment/capture', 'demo/9', 'start', {}));
    expect(toasts()).toEqual([
      { text: 'Experiment capture 9 started.', type: 'is-info' },
    ]);
  });

  it('toasts the stop of its own capture once the hush is over', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    axios.delete.mockResolvedValue({ data: RUNNING });
    const page = await openPage(listOf({ experimentCaptures: [RUNNING] }), {
      tab: 'captures',
    });
    await page.find('.capture-stop').trigger('click');
    await flushPromises();
    await handle(wsMsg('experiment/capture', 'demo/3', 'stop', {}));
    expect(toasts().map((t) => t.text)).toEqual([
      'Stopped experiment capture 3.',
    ]);

    vi.setSystemTime(Date.now() + 60_001);
    await handle(wsMsg('experiment/capture', 'demo/3', 'stop', {}));
    expect(toasts()).toContainEqual({
      text: 'Experiment capture 3 stopped.',
      type: 'is-info',
    });
  });

  it('keeps a capture it stopped in the list as stopped, as the server lists it', async () => {
    store.features = ['webshark'];
    const stoppedAgain = { ...RUNNING, state: 'stopped', running: false };
    let list = listOf({ experimentCaptures: [RUNNING] });
    axios.delete.mockImplementation(async () => {
      list = listOf({ experimentCaptures: [stoppedAgain] });
      return { data: stoppedAgain };
    });
    const page = await openPage(() => list, { tab: 'captures' });
    const row = () =>
      tableRows(page.find('.experiment-captures')).find(
        (r) => r.ID && textOf(r.ID) === '3',
      );
    expect(textOf(row().Status)).toBe('Running');
    expect(row().Status.find('.tag').classes()).toContain('is-success');

    await page.find('.capture-stop').trigger('click');
    await flushPromises();
    await handle(wsMsg('experiment/capture', 'demo/3', 'stop', {}));
    expect(textOf(row().Status)).toBe('Stopped');
    expect(row().Status.find('.tag').classes()).not.toContain('is-success');
    expect([...row().Actions.findAll('a, button')].map(textOf)).toEqual([
      'Open in WebShark',
      'Download',
      'Delete',
    ]);
    expect(page.find('.captures-count').exists()).toBe(false);

    // a new page reads the same list, with the capture still stopped
    document.body.innerHTML = '';
    const reloaded = await openPage(() => list, { tab: 'captures' });
    const again = tableRows(reloaded.find('.experiment-captures')).find(
      (r) => r.ID && textOf(r.ID) === '3',
    );
    expect(textOf(again.Status)).toBe('Stopped');
    expect(reloaded.find('.capture-stop').exists()).toBe(false);
  });

  it('toasts a stop it did not make as stopped', async () => {
    await openPage();
    await handle(wsMsg('experiment/capture', 'demo/4', 'stop', {}));
    expect(toasts()).toEqual([
      { text: 'Experiment capture 4 stopped.', type: 'is-info' },
    ]);
  });

  it.each([
    ['names no capture', undefined, 'start'],
    ['is neither a start nor a stop', 'demo/4', 'delete'],
  ])(
    'lists nothing again for a capture event that %s',
    async (_, name, action) => {
      await openPage();
      await handle(wsMsg('experiment/capture', name, action, {}));
      expect(listReads()).toBe(1);
      expect(toasts()).toEqual([]);
    },
  );

  describe('with support', () => {
    it('asks for the format of an interface capture, then starts it in it', async () => {
      axios.post.mockResolvedValue({});
      const page = await openPage();
      await pickNetworkItem(page, 0, 'start');
      expect(formatDialog(page).props()).toMatchObject({
        active: true,
        title: 'Start capture on interface 0 of web',
      });
      expect(axios.post).not.toHaveBeenCalled();

      await formatDialog(page)
        .find('input[type="radio"][value="pcap"]')
        .setValue();
      await formatDialog(page).find('form').trigger('submit');
      await flushPromises();
      expect(axios.post).toHaveBeenCalledWith(
        'experiments/demo/vms/web/captures',
        {
          interface: 0,
          filename: expect.stringMatching(/^web_0_.*\.pcap$/),
          format: 'pcap',
        },
      );
    });

    it('asks for the format of interface captures on a VLAN, in pcapng first', async () => {
      axios.post.mockResolvedValue({ data: { captures: [] } });
      const page = await openPage();
      await pickNetworkItem(page, 0, 'start-vlan');
      expect(formatDialog(page).props('title')).toBe(
        'Start interface captures on VLAN DMZ',
      );
      await formatDialog(page).find('form').trigger('submit');
      await flushPromises();
      expect(axios.post.mock.calls).toEqual([
        ['experiments/demo/captureVlan', { vlan: 'DMZ', format: 'pcapng' }],
      ]);
    });

    it('stops interface captures on a VLAN without asking', async () => {
      axios.post.mockResolvedValue({ data: { vms: ['web'] } });
      const page = await openPage();
      page.vm.experiment.vms[0].captures = [{ interface: 0 }];
      await flushPromises();
      await pickNetworkItem(page, 0, 'stop-vlan');
      expect(formatDialog(page).props('active')).toBe(false);
      expect(axios.post.mock.calls).toEqual([
        ['experiments/demo/stopCaptureVlan', { vlan: 'DMZ' }],
      ]);
    });

    it('opens the capture dialog on the VLAN from the network menu', async () => {
      const page = await openPage();
      await pickNetworkItem(page, 1, 'capture-experiment');
      expect(captureDialog(page).props()).toMatchObject({
        active: true,
        vlan: 'MGMT',
      });
      expect(textOf(captureDialog(page).find('h2'))).toBe(
        'Capture VLAN traffic',
      );
    });
  });

  describe('without support', () => {
    const old = listOf({ support: UNSUPPORTED });

    it('starts an interface capture at once, with no format', async () => {
      axios.post.mockResolvedValue({});
      const page = await openPage(old);
      await pickNetworkItem(page, 0, 'start');
      expect(formatDialog(page).props('active')).toBe(false);
      expect(axios.post).toHaveBeenCalledWith(
        'experiments/demo/vms/web/captures',
        {
          interface: 0,
          filename: expect.stringMatching(/^web_0_.*\.pcap$/),
        },
      );
    });

    it('starts interface captures on a VLAN at once, with no format', async () => {
      axios.post.mockResolvedValue({ data: { captures: [] } });
      const page = await openPage(old);
      await pickNetworkItem(page, 0, 'start-vlan');
      expect(formatDialog(page).props('active')).toBe(false);
      expect(axios.post.mock.calls).toEqual([
        ['experiments/demo/captureVlan', { vlan: 'DMZ' }],
      ]);
    });

    it('greys out the VLAN capture item, saying why', async () => {
      const page = await openPage(old);
      const row = tableRows(page)[0];
      await row.Network.findAll('.vm-network-trigger')[0].trigger('click');
      await flushPromises();
      const item = document.querySelector('.vm-network-capture-experiment');
      expect(textOf(item)).toBe(
        'Capture VLAN dmz… (requires minimega 3.4.0 or later)',
      );
      expect(item.disabled).toBe(true);
    });
  });
});
