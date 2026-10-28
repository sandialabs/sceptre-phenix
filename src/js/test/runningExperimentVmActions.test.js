// How the running experiment page runs the VM actions its details modal,
// Actions column and selection toolbar ask for, and its VM table and modal.
import { flushPromises, mount } from '@vue/test-utils';
import fc from 'fast-check';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  lastNotice,
  modules,
  notices,
  noticeText,
  notify,
  rbac,
  resetMocks,
  ws,
} = await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/notify.js', modules.notify);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);

import delayedImg from '@/assets/imgs/delayed.png';
import externalImg from '@/assets/imgs/external.png';
import loadingImg from '@/assets/imgs/loading-screenshot.svg';
import notAvailableImg from '@/assets/imgs/not-available.png';
import notRunningImg from '@/assets/imgs/not-running.png';
import VmDetailsCard from '@/components/experiment/VmDetailsCard.vue';
import VmRowActions from '@/components/experiment/VmRowActions.vue';
import {
  VM_ACTIONS,
  partitionVMsForAction,
  skippedVMsText,
} from '@/components/experiment/vmActions.js';
import { namedList } from '@/utils/plural.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { disk } from './helpers/fixtures.js';
import {
  byLabel,
  confirmDialog,
  dialogs,
  mountPage,
  tableRows,
  textOf,
  toasts,
  tooltipButtons,
} from './helpers/mount.js';
import { denyOnly } from './helpers/permissions.js';
import { blockedStorage } from './helpers/storage.js';

beforeEach(() => {
  resetMocks();
  setActivePinia(createPinia());
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

// a VM as the server lists it, with no interfaces unless given
const listed = (vm) => ({
  ipv4: [],
  taps: [],
  networks: [],
  captures: [],
  ...vm,
});

// The page on the demo experiment with this state, its Files tab left out.
// The netflow check it makes as it opens fails, and its requests are cleared.
async function open({ experiment = { name: 'demo', vms: [] }, ...data } = {}) {
  axios.get.mockRejectedValue(new Error('no netflow'));
  experiment = { ...experiment, vms: experiment.vms.map(listed) };
  const page = await mountPage(RunningExperiment, {
    route: '/experiment/demo',
    data: structuredClone({ experiment, ...data }),
    stubs: { ExperimentFilesTab: true },
  });
  axios.get.mockReset();
  return page;
}

// the VM table's rows, each by column label
const vmRows = (page) => tableRows(page.findComponent({ ref: 'vmTable' }));
const nodeOf = (cells) => textOf(cells.Node.find('.tag'));

// whether the page shows it is waiting on the server
const waiting = (page) => page.find('.loading-overlay').exists();

// the open modal with this title; a closed one is hidden
const modalTitled = (page, title) =>
  page
    .findAll('.modal.is-active')
    .find(
      (m) =>
        m.element.style.display !== 'none' &&
        m.find('.modal-card-title').exists() &&
        textOf(m.find('.modal-card-title')) === title,
    );

const titleOf = (dialog) => textOf(dialog.querySelector('.modal-card-title'));
const messageOf = (dialog) =>
  dialog.querySelector('.media-content p > div').innerHTML;
const confirmTextOf = (dialog) =>
  textOf(dialog.querySelector('footer .button:last-child'));
// an alert only says, with one button; a question can be cancelled
const isAlert = (dialog) =>
  dialog.querySelectorAll('footer .button').length === 1;
const questions = () => dialogs().filter((d) => !isAlert(d));
const alerts = () => dialogs().filter(isAlert);
const asked = (dialog) => ({
  title: titleOf(dialog),
  message: messageOf(dialog),
  confirmText: confirmTextOf(dialog),
});

// the toolbar's buttons, by aria-label, with and without the modify state
// actions open
const MAIN_BAR = [
  'Start selected VMs',
  'Pause selected VMs',
  'Create memory snapshots of selected VMs',
  'Create backing images for selected VMs',
  'Create snapshots of selected VMs',
  'Modify state of selected VMs',
];
const STATE_BAR = [
  'Redeploy selected VMs',
  'Reset disk state of selected VMs',
  'Restart selected VMs',
  'Shut down selected VMs',
  'Kill selected VMs',
  'Close state actions',
];

// Selects the named VMs, which the table need not show, and clicks the
// toolbar button with this label, opening the modify state actions first.
async function act(page, label, names) {
  await page.setData({ vmSelectedArray: names });
  if (STATE_BAR.includes(label)) {
    await byLabel(page, 'Modify state of selected VMs').trigger('click');
  }
  await byLabel(page, label).trigger('click');
  await flushPromises();
}

// the page's handler for each VM action, and whether it takes the VM itself
// rather than its name
const handlers = [
  ['start', 'startVm'],
  ['pause', 'pauseVm'],
  ['restart', 'restartVm'],
  ['shutdown', 'shutdownVm'],
  ['kill', 'killVm'],
  ['redeploy', 'redeploy'],
  ['resetDisk', 'resetVmState'],
  ['snapshot', 'captureSnapshot'],
  ['commit', 'diskImage'],
  ['memorySnapshot', 'queueMemorySnapshotVMs'],
  ['portForward', 'showPortForwardDialog'],
  ['mount', 'showMountDialog'],
  ['cdrom', 'showChangeDisc', true],
  ['captureAll', 'captureAllInterfaces', true],
  ['stopCaptures', 'stopAllCaptures', true],
];

it('runs every VM action', () => {
  expect(handlers.map(([action]) => action).sort()).toEqual(
    Object.keys(VM_ACTIONS).sort(),
  );
});

it.each(handlers)(
  "runs %s from a row's actions with %s",
  async (action, handler, whole) => {
    const vm = { name: 'vm1', cdRom: '/iso/a.iso', running: true };
    const page = await open({ experiment: { name: 'demo', vms: [vm] } });
    const run = vi.spyOn(page.vm, handler).mockImplementation(() => {});

    page.findComponent(VmRowActions).vm.$emit('action', action);
    expect(run).toHaveBeenCalledWith(whole ? listed(vm) : 'vm1');
  },
);

it("shows the open VM's details with its row's live state", async () => {
  const details = {
    name: 'vm1',
    state: 'RUNNING',
    running: true,
    busy: false,
    captures: [],
    cpus: 2,
  };
  const row = {
    name: 'vm1',
    state: 'RUNNING',
    running: true,
    busy: true,
    percent: 40,
    captures: [{ interface: 0 }],
    screenshot: 'data:image/png;base64,x',
  };
  const page = await open({
    expModal: { active: true, vm: details },
    experiment: { name: 'demo', vms: [row] },
  });
  expect(page.findComponent(VmDetailsCard).props('vm')).toMatchObject({
    cpus: 2,
    busy: true,
    percent: 40,
    captures: [{ interface: 0 }],
    screenshot: row.screenshot,
  });
});

it('shows the details alone when the VM has no row', async () => {
  const details = { name: 'gone' };
  const page = await open({ expModal: { active: true, vm: details } });
  expect(page.findComponent(VmDetailsCard).props('vm')).toEqual(details);
});

it("opens the VLAN change for the interface's network", async () => {
  const vm = { name: 'vm1', running: true, networks: ['a (1)', 'b (2)'] };
  const page = await open({
    expModal: { active: true, vm },
    experiment: { name: 'demo', vms: [vm] },
  });
  await page.findAll('.vm-iface-name').at(1).trigger('click');

  const change = modalTitled(page, 'Change the VLAN');
  expect(textOf(change.find('.modal-card-body'))).toContain(
    'Move interface 1 from b (2) to a new one for the vm1 VM.',
  );
  expect(byLabel(change, 'New VLAN for interface 1 of VM vm1').exists()).toBe(
    true,
  );
});

describe('the VM table', () => {
  const web = {
    name: 'web',
    state: 'RUNNING',
    running: true,
    busy: false,
    ipv4: ['10.0.0.1', '10.0.0.2'],
    taps: ['tap0', 'tap1'],
    networks: ['dmz (100)', 'mgmt (200)'],
    captures: [{ interface: 1 }],
  };
  const busy = { ...web, name: 'db', busy: true, percent: 40, captures: [] };
  // the page showing these VMs, with its state set over `data`
  const render = (vms, data) =>
    open({ experiment: { name: 'demo', vms }, vmsLoaded: true, ...data });
  const row = async () => vmRows(await render([web, busy]))[0];
  // the IPs or taps a cell marks as being captured
  const captured = (cell) => cell.findAll('.field > .is-success').map(textOf);
  // the column toggle with this label
  const toggle = (page, label) =>
    page
      .findAll('.dropdown-item label.b-checkbox')
      .find((box) => textOf(box) === label)
      .find('input');

  it("colors each VM's name by its state, a busy one as a warning", async () => {
    const rows = vmRows(await render([web, busy]));
    const shape = ['tag', 'is-medium', 'is-clickable'];
    const colors = rows.map((cells) =>
      cells.Node.find('.tag')
        .classes()
        .filter((c) => !shape.includes(c)),
    );
    expect(colors).toEqual([['is-success'], ['is-warning']]);
  });

  it('marks the IPs and taps of the interfaces being captured', async () => {
    const { IP, Taps } = await row();
    expect(captured(IP)).toEqual(['10.0.0.2']);
    expect(captured(Taps)).toEqual(['tap1']);
  });

  it('keeps the columns each user hides hidden, in this browser', async () => {
    await toggle(await render([web]), 'Host').setValue(false);
    expect(localStorage.getItem('alice.showHostColumn')).toBe('false');

    const columns = Object.keys(await row());
    expect(columns).not.toContain('Host');
    expect(columns).toContain('IP');
  });

  it('shows every column when the browser blocks storage', async () => {
    vi.stubGlobal('localStorage', blockedStorage());
    await toggle(await render([web]), 'Host').setValue(false);
    expect(Object.keys(await row())).toContain('Host');
  });

  // what a picture of a VM's screen shows: the image, its alt text, and the
  // console it opens, if any
  const picture = (el) => {
    const img = el.querySelector('img');
    return {
      image: img.getAttribute('src'),
      alt: img.getAttribute('alt'),
      console: el.querySelector('a[href]')?.getAttribute('href') ?? null,
    };
  };
  // the pictures on the VNC tab's tiles
  const tiles = (page) =>
    page
      .findAll('.vnc-tile-name')
      .map((name) => picture(name.element.parentElement));
  const vnc = '/api/v1/experiments/demo/vms/vm1/vnc?token=t';

  // Each VM state vmScreenshot pictures: the VM, the page's state, the
  // image, its alt text, and whether the table links it to the console.
  it.each([
    [
      'is an external node',
      { external: true },
      {},
      externalImg,
      'VM vm1 is an external node',
      false,
    ],
    [
      'has a screenshot',
      { running: true, screenshot: 'data:image/png;base64,AA==' },
      {},
      'data:image/png;base64,AA==',
      'Open the console of VM vm1',
      true,
    ],
    [
      'is waiting for its screenshot',
      { running: true },
      {},
      loadingImg,
      'Open the console of VM vm1: Loading the screenshot of VM vm1',
      true,
    ],
    [
      'got no screenshot in time',
      { running: true },
      { screenshotsDue: true },
      notAvailableImg,
      'Open the console of VM vm1: Screenshot of VM vm1 is not available',
      true,
    ],
    [
      'is busy',
      { running: true, busy: true, percent: 40 },
      {},
      notAvailableImg,
      'Screenshot of VM vm1 is not available while it is busy',
      false,
    ],
    [
      'has a delayed start',
      { delayed_start: true, state: 'BUILDING' },
      {},
      delayedImg,
      'Open the console of VM vm1: VM vm1 has a delayed start',
      true,
    ],
    [
      'is not running',
      { running: false },
      {},
      notRunningImg,
      'VM vm1 is not running',
      false,
    ],
  ])(
    'says what the picture of a VM that %s shows',
    async (_, fields, data, image, alt, linked) => {
      const vm = {
        name: 'vm1',
        ipv4: [],
        taps: [],
        networks: [],
        captures: [],
        ...fields,
      };
      const page = await render([vm], data);

      expect(picture(vmRows(page)[0].Screenshot.element)).toEqual({
        image,
        alt,
        console: linked ? vnc : null,
      });
      // a VNC tile always opens the console, and says so first
      expect(tiles(page)).toEqual(
        vm.external
          ? []
          : [
              {
                image,
                alt: linked ? alt : `Open the console of VM vm1: ${alt}`,
                console: vnc,
              },
            ],
      );
    },
  );

  it('hides the Actions column from roles that can take none of its actions', async () => {
    // the column's actions each need a permission on a VM subresource
    rbac.allowed = (resource) => !resource.startsWith('vms/');
    const columns = Object.keys(await row());
    expect(columns).toContain('Node');
    expect(columns).not.toContain('Actions');

    rbac.allowed = (resource, verb) =>
      !resource.startsWith('vms/') ||
      (resource === 'vms/restart' && verb === 'update');
    expect(Object.keys(await row())).toContain('Actions');
  });
});

describe('changing a VLAN', () => {
  const experiment = {
    name: 'demo',
    vms: [
      {
        name: 'web',
        running: true,
        networks: ['dmz (100)'],
        screenshot: 'shot',
      },
    ],
    vlans: [{ alias: 'MGMT', vlan: 200 }],
  };

  // picks the VLAN option at `index` for web's interface 0 and clicks Change
  async function change(index) {
    const page = await open({ experiment });
    await vmRows(page)[0].Network.find('.field > div').trigger('click');
    const modal = modalTitled(page, 'Change the VLAN');
    await modal.findAll('select option').at(index).setSelected();
    await modal.find('footer button').trigger('click');
    return page;
  }

  it('moves the interface to the picked VLAN and shows the VM the server returns', async () => {
    const patch = Promise.withResolvers();
    axios.patch.mockReturnValue(patch.promise);
    const page = await change(1);

    const [dialog] = dialogs();
    expect(asked(dialog)).toEqual({
      title: 'Change the VLAN',
      message:
        'This will change the VLAN from dmz (100) to mgmt (200) for the web VM.',
      confirmText: 'Change',
    });

    await confirmDialog();
    expect(waiting(page)).toBe(true);
    expect(axios.patch).toHaveBeenCalledWith('experiments/demo/vms/web', {
      interface: { index: 0, vlan: 'MGMT' },
    });
    patch.resolve({
      data: listed({ name: 'web', running: true, networks: ['mgmt (200)'] }),
    });
    await flushPromises();
    expect(waiting(page)).toBe(false);
    const [cells] = vmRows(page);
    expect(textOf(cells.Network.find('.field'))).toBe('mgmt (200)');
    expect(cells.Screenshot.find('img').attributes('src')).toBe('shot');
  });

  it('disconnects the interface', async () => {
    const err = new Error('boom');
    axios.patch.mockRejectedValue(err);
    const page = await change(0);

    expect(asked(dialogs()[0])).toEqual({
      title: 'Disconnect a VM Network Interface',
      message: 'This will disconnect the 0 interface for the web VM.',
      confirmText: 'Disconnect',
    });

    await confirmDialog();
    expect(axios.patch).toHaveBeenCalledWith('experiments/demo/vms/web', {
      interface: { index: 0, vlan: '' },
    });
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(waiting(page)).toBe(false);
  });
});

describe("the open VM's port forwards", () => {
  const forwards = {
    listeners: [
      { srcPort: 8080, dstHost: '127.0.0.1', dstPort: 80, owner: 'alice' },
      { srcPort: 2222, dstHost: '127.0.0.1', dstPort: 22, owner: 'bob' },
    ],
  };
  // the port forward modal, filled in
  const modal = {
    active: true,
    vmName: 'web',
    srcPort: '8080',
    dstHost: '127.0.0.1',
    dstPort: '80',
  };
  const experiment = { name: 'demo', vms: [{ name: 'web', running: true }] };
  // the web VM's details, open
  const details = { active: true, fullName: 'demo/web', vm: { name: 'web' } };
  // the listed forwards: which port each forwards, and who may delete it
  const shown = (page) =>
    page
      .findComponent(VmDetailsCard)
      .props('forwards')
      .map((f) => [f.srcPort, f.canDelete]);
  // opens web's details from its name in the table
  const openDetails = async (page) => {
    await vmRows(page)[0].Node.find('.field > div').trigger('click');
    await flushPromises();
    return page.findComponent(VmDetailsCard);
  };
  const create = async (page) => {
    await modalTitled(page, 'Create New Port Forward')
      .find('footer button')
      .trigger('click');
    await flushPromises();
  };

  it('labels each field of the dialog that creates one', async () => {
    const page = await open({ portForwardModal: modal });
    const dialog = page.find('.modal.is-active');

    expect(
      dialog.findAll('label').map((label) => ({
        label: textOf(label),
        control: dialog.find(`#${label.attributes('for')}`).element.localName,
      })),
    ).toEqual([
      { label: 'Source Port', control: 'input' },
      { label: 'Destination Host', control: 'input' },
      { label: 'Destination Port', control: 'input' },
    ]);
  });

  it("lists them with the VM's details, and only its owner may delete one", async () => {
    const page = await open({ experiment });
    axios.get.mockImplementation(async (url) => ({
      data: url.endsWith('/forwards')
        ? forwards
        : url.endsWith('/snapshots')
          ? { snapshots: ['snap1'] }
          : { name: 'web', cpus: 2 },
    }));

    const card = await openDetails(page);
    expect(card.props()).toMatchObject({
      fullName: 'demo/web',
      vm: { name: 'web', cpus: 2 },
      snapshots: ['snap1'],
    });
    expect(shown(page)).toEqual([
      [8080, true],
      [2222, false],
    ]);
  });

  it("shows the details to roles that can't list snapshots or forwards", async () => {
    rbac.allowed = denyOnly(['vms/snapshots'], ['vms/forwards']);
    const page = await open({ experiment });
    axios.get.mockResolvedValue({ data: { name: 'web' } });

    const card = await openDetails(page);
    expect(axios.get.mock.calls.map(([url]) => url)).toEqual([
      'experiments/demo/vms/web',
    ]);
    expect(card.props()).toMatchObject({ vm: { name: 'web' }, forwards: [] });
  });

  it('creates one and lists them again', async () => {
    const page = await open({
      experiment,
      expModal: details,
      portForwardModal: modal,
    });
    axios.post.mockResolvedValue({});
    axios.get.mockResolvedValue({ data: forwards });

    await create(page);
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/web/forwards',
      null,
      { params: { src: '8080', host: '127.0.0.1', dst: '80' } },
    );
    expect(toasts().map((t) => t.text)).toEqual([
      'Port forward 8080 → 127.0.0.1:80 created for the web VM.',
    ]);
    expect(shown(page)).toEqual([
      [8080, true],
      [2222, false],
    ]);
    expect(modalTitled(page, 'Create New Port Forward')).toBeUndefined();
    expect(page.vm.portForwardModal).toMatchObject({ vmName: null });
  });

  it("doesn't list them again for a role that can't", async () => {
    rbac.allowed = denyOnly(['vms/forwards', 'list']);
    const page = await open({ portForwardModal: modal });
    axios.post.mockResolvedValue({});

    await create(page);
    expect(axios.get).not.toHaveBeenCalled();
    expect(toasts()).toHaveLength(1);
  });

  it('deletes one and lists the rest', async () => {
    const listed = forwards.listeners.map((f) => ({
      ...f,
      canDelete: f.owner === 'alice',
    }));
    const page = await open({
      experiment,
      expModal: { ...details, forwards: listed },
    });
    axios.delete.mockResolvedValue({});
    axios.get.mockResolvedValue({
      data: { listeners: forwards.listeners.slice(1) },
    });

    await byLabel(page, 'delete this port forward').trigger('click');
    await flushPromises();
    expect(axios.delete).toHaveBeenCalledWith(
      'experiments/demo/vms/web/forwards',
      { params: { host: '127.0.0.1', dst: 80 } },
    );
    expect(shown(page)).toEqual([[2222, false]]);
  });
});

describe("saving the open VM's annotations", () => {
  // saves these annotations from the open VM's card, and gives what the card
  // was told
  async function save(annotations) {
    const page = await open({
      expModal: { active: true, vm: { name: 'vm1', annotations: { a: '1' } } },
    });
    const card = page.findComponent(VmDetailsCard);
    const done = vi.fn();
    card.vm.$emit('save-annotations', annotations, done);
    await flushPromises();
    return { card, done };
  }

  it('replaces them and shows what the server saved', async () => {
    axios.patch.mockResolvedValueOnce({
      data: { name: 'vm1', annotations: { b: true } },
    });
    const { card, done } = await save({ b: true });
    expect(axios.patch).toHaveBeenCalledWith('experiments/demo/vms/vm1', {
      annotations: { b: true },
    });
    expect(card.props('vm')).toEqual({
      name: 'vm1',
      annotations: { b: true },
    });
    expect(done).toHaveBeenCalledWith(true);
  });

  it('keeps the editor open and says why when the server refuses', async () => {
    const err = new Error('invalid annotations');
    axios.patch.mockRejectedValueOnce(err);
    const { card, done } = await save({ 'phenix/default-apps': 'no' });
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(card.props('vm').annotations).toEqual({ a: '1' });
    expect(done).toHaveBeenCalledWith(false);
  });
});

// Each action's rule is in vmActions.test.js; these check that the page
// applies it, asks about the VMs it leaves, and sends their requests.
describe('actions on VMs', () => {
  const vms = [
    { name: 'web', state: 'RUNNING', running: true, snapshot: true },
    { name: 'off', state: 'QUIT', running: false, snapshot: true },
    { name: 'held', state: 'PAUSED', running: false, snapshot: true },
    { name: 'persistent', state: 'RUNNING', running: true, snapshot: false },
    { name: 'db', state: 'RUNNING', running: true, snapshot: true },
  ].map((vm) => ({
    ...vm,
    cpus: 2,
    ram: 1024,
    disk: `/images/${vm.name}.qc2`,
    screenshot: `${vm.name}.png`,
  }));
  // 'gone' is not in the table
  const selection = ['web', 'off', 'held', 'persistent', 'db', 'gone'];
  const showing = (shown = vms) =>
    open({ experiment: { name: 'demo', vms: shown } });

  // Answers a bulk action with a finished job; VMs named in `outcomes` get that
  // outcome, the rest succeed, shown on the host the job left them.
  const answerJob = (outcomes = {}) => {
    const items = (vms) =>
      vms.map((name) => {
        const outcome = outcomes[name] ?? { status: 'succeeded' };
        return outcome.status === 'succeeded'
          ? {
              name,
              status: 'succeeded',
              result: listed({ name, state: 'ANSWERED', host: 'answered' }),
            }
          : { name, ...outcome };
      });
    let id = 0;
    axios.post.mockImplementation(async (_, body) => {
      const job = {
        id: `j${++id}`,
        kind: 'vms.action',
        status: 'succeeded',
        items: items(body.vms),
        revision: 2,
      };
      axios.get.mockResolvedValueOnce({ data: job });
      return { data: job };
    });
  };

  it.each([
    ['Start selected VMs', 'start', 'Start the VMs', 'Start', 'Started'],
    ['Pause selected VMs', 'pause', 'Pause the VMs', 'Pause', 'Paused'],
    [
      'Restart selected VMs',
      'restart',
      'Restart the VMs',
      'Restart',
      'Restarted',
    ],
    [
      'Shut down selected VMs',
      'shutdown',
      'Shut Down the VMs',
      'Shut Down',
      'Shut down',
    ],
    [
      'Reset disk state of selected VMs',
      'resetDisk',
      "Reset the VMs' Disks",
      'Reset',
      'Reset the disks of',
    ],
  ])(
    '%s asks once about the VMs the %s rule allows, naming those it skips, then runs one job for them',
    async (label, action, title, confirmText, done) => {
      const page = await showing();
      answerJob();
      const { allowed, skipped } = partitionVMsForAction(
        action,
        selection,
        vms,
      );

      await act(page, label, selection);
      expect(alerts()).toEqual([]);
      const [dialog] = questions();
      expect(asked(dialog)).toMatchObject({ title, confirmText });
      expect(messageOf(dialog)).toContain(namedList(allowed, 'VM'));
      expect(messageOf(dialog)).toContain(skippedVMsText(skipped));
      expect(axios.post).not.toHaveBeenCalled();

      await confirmDialog();
      expect(waiting(page)).toBe(false);

      // one request for all of them, not one each
      expect(axios.post.mock.calls).toEqual([
        ['experiments/demo/vms/actions', { action, vms: allowed }, undefined],
      ]);
      // each VM is shown as the job left it
      expect(
        vmRows(page)
          .filter((cells) => textOf(cells.Host) === 'answered')
          .map(nodeOf),
      ).toEqual(allowed);
      expect(toasts()).toEqual([
        { text: `${done} ${allowed.length} VMs`, type: 'is-success' },
      ]);
    },
  );

  it('kills the VMs the kill rule allows and takes them out of the table', async () => {
    const page = await showing();
    answerJob();
    const { skipped } = partitionVMsForAction('kill', selection, vms);

    await act(page, 'Kill selected VMs', selection);
    const [dialog] = questions();
    expect(asked(dialog)).toMatchObject({
      title: 'Kill the VMs',
      confirmText: 'KILL THEM!',
    });
    expect(messageOf(dialog)).toContain(
      '<p>This will kill the VMs web, persistent, and db. You will not be ' +
        'able to restore them until you restart the demo experiment!</p>',
    );
    expect(messageOf(dialog)).toContain(skippedVMsText(skipped));

    await confirmDialog();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/actions',
      { action: 'kill', vms: ['web', 'persistent', 'db'] },
      undefined,
    );
    expect(vmRows(page).map(nodeOf)).toEqual(['off', 'held']);
  });

  it('says how a partly failed job went, and retries the VMs worth it', async () => {
    const page = await showing();
    answerJob({
      persistent: {
        status: 'failed',
        error: 'vm persistent is locked',
        retryable: true,
      },
      db: { status: 'failed', error: 'minimega says no' },
    });

    await act(page, 'Restart selected VMs', ['web', 'persistent', 'db']);
    await confirmDialog();

    expect(toasts()).toEqual([]);
    expect(notices.open).toHaveBeenCalledTimes(1);
    const { message, indefinite } = lastNotice();
    expect(indefinite).toBe(true);
    const text = noticeText();
    expect(text).toContain('Restarted 1 of 3 VMs: 2 failed');
    expect(text).toContain('persistent: vm persistent is locked');
    expect(text).toContain('Retry failed (1)');

    // the retry starts a job for the retryable VM alone
    await mount(() => message)
      .find('button')
      .trigger('click');
    await flushPromises();
    expect(axios.post.mock.lastCall).toEqual([
      'experiments/demo/vms/actions',
      { action: 'restart', vms: ['persistent'] },
      undefined,
    ]);
  });

  it('says which VMs it skips and why, in the one dialog', async () => {
    const page = await showing();
    await act(page, 'Shut down selected VMs', selection);
    expect(messageOf(questions()[0])).toBe(
      '<p>This will shut down the VMs web, persistent, and db.</p>' +
        '<p class="mt-3 has-text-grey-dark">The VMs off and held are not ' +
        'running. The gone VM is not shown in the table.</p>',
    );

    await act(page, 'Restart selected VMs', selection);
    expect(messageOf(questions()[1])).toContain(
      'The held VM is paused and must be resumed first. ' +
        'The gone VM is not shown in the table.',
    );
    expect(alerts()).toEqual([]);
  });

  it('words its question for one VM', async () => {
    const page = await showing();
    await act(page, 'Shut down selected VMs', ['web']);
    expect(asked(questions()[0])).toMatchObject({
      title: 'Shut Down the VM',
      message: '<p>This will shut down the web VM.</p>',
    });
    await act(page, 'Reset disk state of selected VMs', ['web']);
    expect(titleOf(questions()[1])).toBe("Reset the VM's Disk");
    await act(page, 'Kill selected VMs', ['web']);
    expect(asked(questions()[2])).toEqual({
      title: 'Kill the VM',
      message:
        '<p>This will kill the web VM. You will not be able to restore it ' +
        'until you restart the demo experiment!</p>',
      confirmText: 'KILL IT!',
    });
    expect(alerts()).toEqual([]);
  });

  it("asks about one VM from its row's actions or its details, then runs a job for it", async () => {
    const page = await open({
      experiment: { name: 'demo', vms },
      expModal: { active: true, vm: vms[0] },
    });
    answerJob();
    const web = vmRows(page).find((cells) => nodeOf(cells) === 'web');
    const row = (action) => web.Actions.find(`.vm-row-${action}`);
    const details = (action) =>
      page.findComponent(VmDetailsCard).find(`.vm-action-${action}`);
    const pick = async (button) => {
      await button.trigger('click');
      await flushPromises();
      return questions().at(-1);
    };

    expect(asked(await pick(row('shutdown')))).toMatchObject({
      title: 'Shut Down the VM',
      message: '<p>This will shut down the web VM.</p>',
    });
    expect(titleOf(await pick(details('resetDisk')))).toBe(
      "Reset the VM's Disk",
    );
    expect(asked(await pick(details('kill')))).toMatchObject({
      title: 'Kill the VM',
      confirmText: 'KILL IT!',
    });
    expect(asked(await pick(row('snapshot')))).toMatchObject({
      title: 'Create a VM Snapshot',
      message: '<p>This will create a snapshot of the web VM.</p>',
    });
    expect(asked(await pick(row('restart')))).toMatchObject({
      title: 'Restart the VM',
      message: '<p>This will restart the web VM.</p>',
    });
    expect(questions()).toHaveLength(5);
    expect(alerts()).toEqual([]);

    await confirmDialog();
    expect(axios.post.mock.calls).toEqual([
      [
        'experiments/demo/vms/actions',
        { action: 'restart', vms: ['web'] },
        undefined,
      ],
    ]);
  });

  it('asks about and runs a job for the VMs the rule allows, of any selection', async () => {
    const jobs = [
      ['Start selected VMs', 'start'],
      ['Pause selected VMs', 'pause'],
      ['Restart selected VMs', 'restart'],
      ['Shut down selected VMs', 'shutdown'],
      ['Kill selected VMs', 'kill'],
      ['Reset disk state of selected VMs', 'resetDisk'],
    ];
    const picked = fc.shuffledSubarray(selection, { minLength: 1 });
    await fc.assert(
      fc.asyncProperty(
        fc.constantFrom(...jobs),
        picked,
        async ([label, action], names) => {
          document.body.innerHTML = '';
          resetMocks();
          setActivePinia(createPinia());
          const page = await showing();
          answerJob();
          const { allowed, skipped } = partitionVMsForAction(
            action,
            names,
            vms,
          );

          await act(page, label, names);
          if (!allowed.length) {
            expect(questions()).toEqual([]);
            expect(textOf(alerts()[0].querySelector('.media-content'))).toBe(
              skippedVMsText(skipped),
            );
          } else {
            expect(alerts()).toEqual([]);
            const message = messageOf(questions()[0]);
            expect(message).toContain(namedList(allowed, 'VM'));
            expect(message).toContain(skippedVMsText(skipped));
            await confirmDialog();
          }
          expect(axios.post.mock.calls).toEqual(
            allowed.length
              ? [
                  [
                    'experiments/demo/vms/actions',
                    { action, vms: allowed },
                    undefined,
                  ],
                ]
              : [],
          );
          page.unmount();
        },
      ),
      { numRuns: 8 },
    );
  });

  it('keeps waiting while the job is running', async () => {
    // the job store's poll while it waits
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    const page = await showing();
    axios.post.mockResolvedValue({
      data: { id: 'j1', status: 'running', items: [], revision: 1 },
    });

    await act(page, 'Restart selected VMs', ['web', 'db']);
    await confirmDialog();
    expect(waiting(page)).toBe(true);
  });

  it('reports a job that could not start and stops waiting', async () => {
    const page = await showing();
    const err = new Error('boom');
    axios.post.mockRejectedValue(err);

    await act(page, 'Restart selected VMs', ['web']);
    await confirmDialog();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(waiting(page)).toBe(false);
  });

  it('creates snapshots of the VMs the snapshot rule allows, and sums up how it went', async () => {
    const page = await showing();
    axios.post.mockResolvedValueOnce({});
    axios.post.mockRejectedValueOnce(
      Object.assign(new Error('500'), {
        response: { status: 500, data: 'disk full' },
      }),
    );
    const { allowed, skipped } = partitionVMsForAction(
      'snapshot',
      selection,
      vms,
    );

    await act(page, 'Create snapshots of selected VMs', selection);
    expect(alerts()).toEqual([]);
    const [dialog] = questions();
    expect(titleOf(dialog)).toBe('Create VM Snapshots');
    expect(messageOf(dialog)).toContain(
      'This will create snapshots of the VMs web and db.',
    );
    expect(messageOf(dialog)).toContain(skippedVMsText(skipped));

    await confirmDialog();
    expect(axios.post.mock.calls.map(([url]) => url)).toEqual(
      allowed.map((name) => `experiments/demo/vms/${name}/snapshots`),
    );
    // one summary, not a notification for each failure
    expect(notify.error).not.toHaveBeenCalled();
    expect(notices.open).toHaveBeenCalledTimes(1);
    expect(noticeText()).toContain('Snapshotted 1 of 2 VMs: 1 failed');

    await act(page, 'Create snapshots of selected VMs', ['web']);
    expect(asked(questions().at(-1))).toMatchObject({
      title: 'Create a VM Snapshot',
      message: '<p>This will create a snapshot of the web VM.</p>',
    });
  });

  it('creates memory snapshots in one go, summing up how they went', async () => {
    const page = await open({
      experiment: { name: 'demo', vms },
      memorySnapshotModal: {
        active: true,
        vm: [
          { name: 'web', filename: 'web_1' },
          { name: 'db', filename: 'db_1' },
        ],
      },
    });
    axios.post.mockResolvedValue({});

    await modalTitled(page, 'Create memory snapshot')
      .find('footer .is-success')
      .trigger('click');
    expect(modalTitled(page, 'Create memory snapshot')).toBeUndefined();
    expect(page.vm.memorySnapshotModal.vm).toEqual([]);
    await flushPromises();
    expect(axios.post.mock.calls).toEqual([
      [
        'experiments/demo/vms/web/memorySnapshot',
        { filename: 'web_1.elf' },
        { timeout: 0 },
      ],
      [
        'experiments/demo/vms/db/memorySnapshot',
        { filename: 'db_1.elf' },
        { timeout: 0 },
      ],
    ]);
    expect(toasts().map((t) => t.text)).toEqual([
      'Created memory snapshots of 2 VMs',
    ]);
  });

  // the VMs a modal's filename fields are for, and what each holds
  const filenames = (page, kind) =>
    page
      .findAll(`[aria-label^="${kind} filename for VM "]`)
      .map((input) => [
        input.attributes('aria-label').split(' ').at(-1),
        input.element.value,
      ]);

  it("suggests each backing image's name from the file name of the VM's disk", async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 8, 30, 12, 0, 0));
    const running = (name, disk) => ({
      name,
      running: true,
      snapshot: true,
      disk,
    });
    const page = await showing([
      // folders whose names hold the "." or "_" the name is cut at
      running('web', '/phenix/images/win.10/base.qc2'),
      running('db', '/phenix/images/my_vms/db_20260101120000.qc2'),
    ]);

    await act(page, 'Create backing images for selected VMs', ['web', 'db']);
    expect(filenames(page, 'Disk image')).toEqual([
      ['web', 'web_base_20260930120000'],
      ['db', 'db_db_20260930120000'],
    ]);
  });

  it('names the VMs the backing image and memory snapshot rules allow', async () => {
    const page = await showing();
    const alerted = () =>
      textOf(alerts().at(-1).querySelector('.media-content'));
    const listed = (kind) => filenames(page, kind).map(([name]) => name);

    await act(page, 'Create backing images for selected VMs', selection);
    const commit = partitionVMsForAction('commit', selection, vms);
    expect(alerted()).toBe(skippedVMsText(commit.skipped));
    expect(modalTitled(page, 'Create a Disk Image')).toBeDefined();
    expect(listed('Disk image')).toEqual(commit.allowed);

    await act(page, 'Create memory snapshots of selected VMs', selection);
    const memory = partitionVMsForAction('memorySnapshot', selection, vms);
    expect(alerted()).toBe(skippedVMsText(memory.skipped));
    expect(modalTitled(page, 'Create memory snapshot')).toBeDefined();
    expect(listed('Memory snapshot')).toEqual(memory.allowed);
  });

  // what the redeploy window offers each VM: its CPUs, memory, disk and
  // whether to replicate its injections
  const redeploying = (page) =>
    page.findAll('[aria-label^="CPUs for VM "]').map((cpus) => {
      const name = cpus.attributes('aria-label').split(' ').at(-1);
      const value = (label) => byLabel(page, `${label}${name}`).element.value;
      return {
        name,
        cpus: value('CPUs for VM '),
        ram: value('Memory for VM '),
        disk: value('Disk for VM '),
        inject: value('Replicate original injections for VM '),
      };
    });

  it('lists the VMs the redeploy rule allows in the redeploy modal', async () => {
    const syncing = { ...vms[0], name: 'syncing', busy: true };
    const page = await showing([...vms, syncing]);
    axios.get.mockResolvedValue({ data: { disks: [] } });

    await act(page, 'Redeploy selected VMs', ['web', 'syncing', 'gone']);
    expect(textOf(alerts()[0].querySelector('.media-content'))).toBe(
      'The syncing VM is busy with another action. ' +
        'The gone VM is not in the table.',
    );
    expect(modalTitled(page, 'Redeploy the VM')).toBeDefined();
    expect(redeploying(page)).toEqual([
      {
        name: 'web',
        cpus: '2',
        ram: '1024',
        disk: '/images/web.qc2',
        inject: 'false',
      },
    ]);
    // the disk images the modal offers: this experiment's
    expect(axios.get).toHaveBeenCalledWith('disks', {
      params: { expName: 'demo' },
    });
  });

  it('redeploys a VM on its own disk unless another one is picked', async () => {
    const page = await showing();
    const disks = [{ name: 'web.qc2', fullPath: '/images/win/web.qc2' }];
    axios.get.mockResolvedValue({ data: { disks } });
    axios.post.mockResolvedValue({});
    const sent = () => axios.post.mock.lastCall;
    const modal = () => modalTitled(page, 'Redeploy the VM');
    const click = async (text) => {
      await modal()
        .findAll('footer button')
        .find((b) => textOf(b) === text)
        .trigger('click');
      await flushPromises();
    };

    await act(page, 'Redeploy selected VMs', ['web']);
    expect(
      byLabel(page, 'Disk for VM web')
        .findAll('option')
        .map((o) => o.element.value),
    ).toContain('/images/win/web.qc2');
    await click('Redeploy');
    // a snapshot VM's disk is the image under its snapshot overlay, which
    // sending it back would drop
    expect(sent()).toEqual([
      'experiments/demo/vms/web/redeploy',
      { cpus: 2, ram: 1024 },
    ]);

    await click('Cancel');
    await act(page, 'Redeploy selected VMs', ['web']);
    await byLabel(page, 'Disk for VM web').setValue('/images/win/web.qc2');
    await click('Redeploy');
    expect(sent()).toEqual([
      'experiments/demo/vms/web/redeploy',
      { cpus: 2, ram: 1024, disk: '/images/win/web.qc2' },
    ]);
  });

  it('offers each VM in the redeploy window the listed disks, warning beside its picker', async () => {
    const vm = (name, path) => ({
      name,
      cpus: 2,
      ram: 1024,
      disk: path,
      currentDisk: path,
      inject: false,
    });
    const page = await open({
      disks: [
        disk('/images/win/web.qc2', 'win/web.qc2'),
        disk('/images/web.qc2', 'web.qc2'),
        disk('/data/vms/out.img'),
      ],
      redeployModal: {
        active: true,
        vm: [
          vm('web', '/images/win/web.qc2'),
          vm('out', '/data/vms/out.img'),
          vm('gone', '/images/gone.qc2'),
        ],
      },
    });

    // each VM's disk picker: its options, and the warnings beside it
    const pickers = Object.fromEntries(
      page.findAll('.disk-select').map((picker) => {
        const select = picker.find('select');
        return [
          select.attributes('aria-label').split(' ').at(-1),
          {
            options: select.findAll('option').map((o) => {
              const parent = o.element.parentElement;
              const group =
                parent.localName === 'optgroup' ? `[${parent.label}] ` : '';
              return (
                group + textOf(o) + (o.element.disabled ? ' (disabled)' : '')
              );
            }),
            warnings: tooltipButtons(picker).map((b) => b.label),
          },
        ];
      }),
    );
    const offered = [
      'web.qc2',
      'win/web.qc2',
      '[Outside the standard images directory] /data/vms/out.img',
    ];
    expect(pickers).toEqual({
      web: { options: offered, warnings: [] },
      out: {
        options: offered,
        warnings: ['Outside the standard images directory'],
      },
      gone: {
        options: ['/images/gone.qc2 (not listed) (disabled)', ...offered],
        warnings: ['Not in your disk list'],
      },
    });
  });

  it("offers a role that may not list disks the VM's own disk alone", async () => {
    rbac.allowed = denyOnly(['disks']);
    const page = await showing();

    await act(page, 'Redeploy selected VMs', ['web']);
    expect(axios.get).not.toHaveBeenCalled();
    const picker = byLabel(page, 'Disk for VM web');
    expect(picker.findAll('option').map((o) => o.element.value)).toEqual([
      '/images/web.qc2',
    ]);
    expect(picker.element.value).toBe('/images/web.qc2');
  });

  it('opens nothing when every VM is skipped', async () => {
    const page = await showing();
    const labels = [
      ...MAIN_BAR.filter((label) => !label.startsWith('Modify')),
      ...STATE_BAR.filter((label) => !label.startsWith('Close')),
    ];
    for (const label of labels) await act(page, label, ['gone']);
    expect(alerts()).toHaveLength(labels.length);
    expect(questions()).toEqual([]);
    expect(page.find('.modal.is-active').exists()).toBe(false);
    expect(axios.get).not.toHaveBeenCalled();
  });
});

describe("showing the server's copy of a VM", () => {
  const db = { name: 'db', running: true, screenshot: 'db.png' };
  // the page showing web, in the given running state, with its screenshot
  const showing = (running) =>
    open({
      experiment: {
        name: 'demo',
        vms: [{ name: 'web', running, screenshot: 'web.png' }, db],
      },
    });
  // the server's websocket news of a VM's new copy
  const update = (vm) => {
    const msg = {
      resource: {
        type: 'experiment/vm',
        action: 'update',
        name: `demo/${vm.name}`,
      },
      result: listed(vm),
    };
    for (const handler of ws.handlers) handler(msg);
    return flushPromises();
  };
  // each row's VM, host and screenshot
  const rows = (page) =>
    vmRows(page).map((cells) => [
      nodeOf(cells),
      textOf(cells.Host),
      cells.Screenshot.find('img').attributes('src'),
    ]);

  it.each([
    ['keeps the screenshot while the VM stays running', true, true, 'web.png'],
    ['drops it when the VM stops', true, false, notRunningImg],
    ['drops it when the VM starts again', false, true, loadingImg],
  ])('%s', async (_, before, after, screenshot) => {
    const page = await showing(before);
    await update({ name: 'web', running: after, host: 'new' });
    expect(rows(page)).toEqual([
      ['web', 'new', screenshot],
      ['db', '', 'db.png'],
    ]);
  });

  it("shows the copy's own screenshot", async () => {
    const page = await showing(true);
    await update({ name: 'web', running: true, screenshot: 'new.png' });
    expect(rows(page)[0][2]).toBe('new.png');
  });

  it('ignores a VM the table no longer shows', async () => {
    const page = await showing(true);
    await update({ name: 'gone', running: true, host: 'new' });
    expect(rows(page)).toEqual([
      ['web', '', 'web.png'],
      ['db', '', 'db.png'],
    ]);
  });
});

describe('the selection toolbar', () => {
  const experiment = {
    name: 'demo',
    vms: [{ name: 'web' }, { name: 'db' }],
  };
  // the aria-labels of the toolbar's buttons
  const toolbarOf = (page) =>
    page
      .findAll('button[aria-label]')
      .map((button) => button.attributes('aria-label'))
      .filter((label) => / selected VMs$|^Close state actions$/.test(label));
  const toolbar = async (data) =>
    toolbarOf(
      await open({ experiment, vmSelectedArray: ['web', 'db'], ...data }),
    );

  it('offers every action to a role that may take them all', async () => {
    expect(await toolbar()).toEqual(MAIN_BAR);
  });

  it('shows the modify state actions in their place', async () => {
    expect(await toolbar({ showModifyStateBar: true })).toEqual(STATE_BAR);
  });

  // each button needs its permission on every selected VM
  it.each([
    ['Start selected VMs', 'vms/start', 'update', false],
    ['Pause selected VMs', 'vms/stop', 'update', false],
    [
      'Create memory snapshots of selected VMs',
      'vms/memorySnapshot',
      'create',
      false,
    ],
    ['Create backing images for selected VMs', 'vms/commit', 'create', false],
    ['Create snapshots of selected VMs', 'vms/snapshots', 'create', false],
    ['Redeploy selected VMs', 'vms/redeploy', 'update', true],
    ['Reset disk state of selected VMs', 'vms/reset', 'update', true],
    ['Restart selected VMs', 'vms/restart', 'update', true],
    ['Shut down selected VMs', 'vms/shutdown', 'update', true],
    ['Kill selected VMs', 'vms', 'delete', true],
  ])(
    'hides %s from a role without %s %s on one of them',
    async (label, resource, verb, showModifyStateBar) => {
      rbac.allowed = denyOnly([resource, verb, 'demo/db']);
      const bar = showModifyStateBar ? STATE_BAR : MAIN_BAR;
      expect(await toolbar({ showModifyStateBar })).toEqual(
        bar.filter((shown) => shown !== label),
      );
    },
  );

  it('is hidden with nothing selected', async () => {
    expect(await toolbar({ vmSelectedArray: [] })).toEqual([]);
  });

  // the page showing web and db, both selected, running or not, and the
  // disks it may list
  async function selected({ running = true, ...data } = {}) {
    const page = await open({
      experiment: {
        name: 'demo',
        vms: ['web', 'db'].map((name) => ({
          name,
          state: running ? 'RUNNING' : 'QUIT',
          running,
          snapshot: true,
          cpus: 1,
          ram: 512,
          disk: `/images/${name}.qc2`,
          ipv4: ['10.0.0.1'],
          taps: ['tap0'],
          networks: ['dmz (100)'],
          captures: [],
        })),
      },
      vmSelectedArray: ['web', 'db'],
      ...data,
    });
    axios.get.mockResolvedValue({ data: { disks: [] } });
    return page;
  }
  // clicks the toolbar buttons with these labels in turn
  const click = async (page, labels) => {
    for (const label of labels) {
      await byLabel(page, label).trigger('click');
      await flushPromises();
    }
  };
  // the question asked, or else the modal opened: its title and text
  const opened = (page) => {
    const [question] = questions();
    if (question) {
      return {
        title: titleOf(question),
        text: textOf(question.querySelector('.media-content')),
      };
    }
    const modal = page.find('.modal.is-active');
    return {
      title: textOf(modal.find('header')),
      text: textOf(modal.find('section')),
    };
  };
  // the checkboxes checked in the VM table, by their labels
  const checked = (page) =>
    page
      .findComponent({ ref: 'vmTable' })
      .findAll('label.b-checkbox')
      .filter((box) => box.find('input').element.checked)
      .map(textOf);

  it.each([
    ['Start selected VMs', 'Start the VMs', { running: false }],
    ['Pause selected VMs', 'Pause the VMs'],
    ['Create memory snapshots of selected VMs', 'Create memory snapshot'],
    ['Create backing images for selected VMs', 'Create a Disk Image'],
    ['Create snapshots of selected VMs', 'Create VM Snapshots'],
    ['Redeploy selected VMs', 'Redeploy the VMs', { stateBar: true }],
    [
      'Reset disk state of selected VMs',
      "Reset the VMs' Disks",
      { stateBar: true },
    ],
    ['Restart selected VMs', 'Restart the VMs', { stateBar: true }],
    ['Shut down selected VMs', 'Shut Down the VMs', { stateBar: true }],
    ['Kill selected VMs', 'Kill the VMs', { stateBar: true }],
  ])(
    '%s asks "%s" about both VMs',
    async (label, title, { running, stateBar } = {}) => {
      const page = await selected({ running });
      await click(
        page,
        stateBar ? ['Modify state of selected VMs', label] : [label],
      );
      const { title: shown, text } = opened(page);
      expect(shown).toBe(title);
      expect(text).toMatch(/\bweb\b[\s\S]*\bdb\b/);
      expect(alerts()).toEqual([]);
    },
  );

  it('clears the selection once a button starts its action', async () => {
    const page = await selected({ checkAll: true });
    expect(checked(page)).toEqual([
      'Select all VMs',
      'Select VM web',
      'Select VM db',
    ]);

    await click(page, ['Modify state of selected VMs', 'Kill selected VMs']);
    expect(checked(page)).toEqual([]);
    expect(toolbarOf(page)).toEqual([]);
    // selecting again starts over with the main actions
    for (const name of ['web', 'db']) {
      await page
        .find(`label.b-checkbox:has(input[value="${name}"]) input`)
        .setValue(true);
    }
    expect(toolbarOf(page)).toEqual(MAIN_BAR);
  });

  it('selects every VM the table shows but an external one with Select all', async () => {
    const page = await open({
      experiment: {
        name: 'demo',
        vms: [{ name: 'web' }, { name: 'hil', external: true }, { name: 'db' }],
      },
    });
    const selectAll = () =>
      page
        .findAll('label.b-checkbox')
        .find((box) => textOf(box) === 'Select all VMs')
        .find('input');

    await selectAll().setValue(true);
    expect(checked(page)).toEqual([
      'Select all VMs',
      'Select VM web',
      'Select VM db',
    ]);
    expect(toolbarOf(page)).toEqual(MAIN_BAR);

    await selectAll().setValue(false);
    expect(checked(page)).toEqual([]);
  });
});
