// How the running experiment page runs the VM actions its details modal,
// Actions column and selection toolbar ask for, and its VM table and modal.
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
} = await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/notify.js', modules.notify);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);

import { h } from 'vue';

import delayedImg from '@/assets/imgs/delayed.png';
import externalImg from '@/assets/imgs/external.png';
import loadingImg from '@/assets/imgs/loading-screenshot.svg';
import notAvailableImg from '@/assets/imgs/not-available.png';
import notRunningImg from '@/assets/imgs/not-running.png';
import {
  VM_ACTIONS,
  partitionVMsForAction,
  skippedVMsText,
} from '@/components/experiment/vmActions.js';
import { namedList } from '@/utils/plural.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { flush } from './helpers/async.js';
import { lastAlert, lastConfirm, toasts } from './helpers/buefy.js';
import { disk } from './helpers/fixtures.js';
import { denyOnly } from './helpers/permissions.js';
import {
  labeledControls,
  openModal,
  renderSSR,
  selectOptions,
  stubBrowser,
  tableRows,
  textOf,
  tooltipButtons,
} from './helpers/render.js';
import { page } from './helpers/runningExperiment.js';
import { blockedStorage } from './helpers/storage.js';

const { onVmAction, showVlanModal } = RunningExperiment.methods;
const { expModalVm } = RunningExperiment.computed;

beforeEach(() => {
  resetMocks();
  setActivePinia(createPinia());
});

// the page's method for each VM action, and whether it takes the VM itself
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

it.each(handlers)('runs %s with %s', (action, handler, whole) => {
  const vm = { name: 'vm1', cdRom: '/iso/a.iso' };
  const ctx = { [handler]: vi.fn() };
  onVmAction.call(ctx, action, vm);
  expect(ctx[handler]).toHaveBeenCalledWith(whole ? vm : 'vm1');
});

it("shows the open VM's details with its row's live state", () => {
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
  const vm = expModalVm.call({
    expModal: { vm: details },
    experiment: { vms: [row] },
  });
  expect(vm).toMatchObject({
    cpus: 2,
    busy: true,
    percent: 40,
    captures: [{ interface: 0 }],
    screenshot: row.screenshot,
  });
});

it('shows the details alone when the VM has no row', () => {
  const details = { name: 'gone' };
  expect(
    expModalVm.call({ expModal: { vm: details }, experiment: { vms: [] } }),
  ).toBe(details);
});

it("opens the VLAN change for the interface's network", () => {
  const ctx = { vlanModal: {} };
  showVlanModal.call(ctx, { name: 'vm1', networks: ['a (1)', 'b (2)'] }, 1);
  expect(ctx.vlanModal).toEqual({
    active: true,
    vmName: 'vm1',
    vmFromNet: 'b (2)',
    vmNetIndex: 1,
  });
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
    renderSSR(
      RunningExperiment,
      {},
      {
        route: '/experiment/demo',
        tables: true,
        data: { experiment: { name: 'demo', vms }, vmsLoaded: true, ...data },
      },
    );
  // the table's rows, each by column
  const rows = async () => tableRows(await render([web, busy]));
  const row = async () => (await rows())[0];
  // the IPs or taps a cell marks as being captured
  const captured = (cell) =>
    [...cell.matchAll(/<div class="([^"]*)" aria-busy[^>]*>\s*(\S+)/g)]
      .filter(([, type]) => type === 'is-success')
      .map(([, , text]) => text);

  beforeEach(() => {
    stubBrowser();
    // the netflow check that creating the page makes
    axios.get.mockRejectedValue(new Error('no netflow'));
  });
  afterEach(() => vi.unstubAllGlobals());

  it("colors each VM's name by its state, a busy one as a warning", async () => {
    const colors = (await rows()).map(
      (cells) =>
        cells.name.match(/class="tag is-medium is-clickable ([^"]*)"/)[1],
    );
    expect(colors).toEqual(['is-success', 'is-warning']);
  });

  it('marks the IPs and taps of the interfaces being captured', async () => {
    const { ipv4, taps } = await row();
    expect(captured(ipv4)).toEqual(['10.0.0.2']);
    expect(captured(taps)).toEqual(['tap1']);
  });

  it('keeps the columns each user hides hidden, in this browser', async () => {
    const ctx = page();
    ctx.columnVisibility.host = false;
    ctx.persistColumnVisibility({ key: 'host', storageKey: 'showHostColumn' });
    expect(localStorage.getItem('alice.showHostColumn')).toBe('false');

    const columns = Object.keys(await row());
    expect(columns).not.toContain('host');
    expect(columns).toContain('ipv4');
  });

  it('shows every column when the browser blocks storage', async () => {
    vi.stubGlobal('localStorage', blockedStorage());
    page().persistColumnVisibility({ key: 'host', storageKey: 'x' });
    expect(Object.keys(await row())).toContain('host');
  });

  // what a picture of a VM's screen shows: the image, its alt text, and the
  // console it opens, if any
  const picture = (html) => {
    const img = html.match(/<img\b[^>]*>/)[0];
    return {
      image: img.match(/src="([^"]*)"/)[1],
      alt: textOf(img.match(/alt="([^"]*)"/)[1]),
      console: html.match(/<a href="([^"]*)"/)?.[1] ?? null,
    };
  };
  // the pictures on the VNC tab's tiles
  const tiles = (html) =>
    [
      ...html.matchAll(
        /(<a href="[^"]*"[^>]*><img\b[^>]*><\/a>)<(?:a|span) class="vnc-tile-name"/g,
      ),
    ].map(([, tile]) => picture(tile));
  const vnc = '/api/v1/experiments/demo/vms/vm1/vnc?token=t';

  // Each VM state getVmScreenshot pictures: the VM, the page's state, the
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
      const html = await render([vm], data);

      expect(picture(tableRows(html)[0].screenshot)).toEqual({
        image,
        alt,
        console: linked ? vnc : null,
      });
      // a VNC tile always opens the console, and says so first
      expect(tiles(html)).toEqual(
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
    expect(columns).toContain('name');
    expect(columns).not.toContain('actions');

    rbac.allowed = (resource, verb) =>
      !resource.startsWith('vms/') ||
      (resource === 'vms/restart' && verb === 'update');
    expect(Object.keys(await row())).toContain('actions');
  });
});

describe('changing a VLAN', () => {
  const vms = [
    { name: 'web', running: true, networks: ['dmz (100)'], screenshot: 'shot' },
  ];

  it('moves the interface to the picked VLAN and shows the VM the server returns', async () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
    axios.patch.mockResolvedValue({
      data: { name: 'web', running: true, networks: ['mgmt (200)'] },
    });

    ctx.changeVlan(0, { alias: 'MGMT', vlan: 200 }, 'DMZ (100)', 'web');
    const dialog = lastConfirm(ctx);
    expect(dialog).toMatchObject({
      title: 'Change the VLAN',
      message:
        'This will change the VLAN from dmz (100) to mgmt (200) for the web VM.',
      confirmText: 'Change',
    });

    dialog.onConfirm();
    expect(ctx.isWaiting).toBe(true);
    expect(axios.patch).toHaveBeenCalledWith('experiments/demo/vms/web', {
      interface: { index: 0, vlan: 'MGMT' },
    });
    await flush();
    expect(ctx.isWaiting).toBe(false);
    expect(ctx.experiment.vms).toEqual([
      {
        name: 'web',
        running: true,
        networks: ['mgmt (200)'],
        screenshot: 'shot',
      },
    ]);
  });

  it('disconnects the interface', async () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
    const err = new Error('boom');
    axios.patch.mockRejectedValue(err);

    ctx.changeVlan(0, '0', 'DMZ (100)', 'web');
    const dialog = lastConfirm(ctx);
    expect(dialog).toMatchObject({
      title: 'Disconnect a VM Network Interface',
      message: 'This will disconnect the 0 interface for the web VM.',
      confirmText: 'Disconnect',
    });

    dialog.onConfirm();
    expect(axios.patch).toHaveBeenCalledWith('experiments/demo/vms/web', {
      interface: { index: 0, vlan: '' },
    });
    await flush();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(ctx.isWaiting).toBe(false);
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
  // the listed forwards: which port each forwards, and who may delete it
  const shown = (ctx) =>
    ctx.expModal.forwards.map((f) => [f.srcPort, f.canDelete]);

  afterEach(() => vi.unstubAllGlobals());

  it('labels each field of the dialog that creates one', async () => {
    stubBrowser();
    // the netflow check that creating the page makes
    axios.get.mockRejectedValue(new Error('no netflow'));
    const html = await renderSSR(
      RunningExperiment,
      {},
      { route: '/experiment/demo', data: { portForwardModal: { ...modal } } },
    );

    expect(labeledControls(openModal(html))).toEqual([
      { label: 'Source Port', control: 'input' },
      { label: 'Destination Host', control: 'input' },
      { label: 'Destination Port', control: 'input' },
    ]);
  });

  it("lists them with the VM's details, and only its owner may delete one", async () => {
    const ctx = page();
    axios.get.mockImplementation(async (url) => ({
      data: url.endsWith('/forwards')
        ? forwards
        : url.endsWith('/snapshots')
          ? { snapshots: ['snap1'] }
          : { name: 'web', cpus: 2 },
    }));

    await ctx.getInfo({ name: 'web' });
    expect(ctx.expModal).toMatchObject({
      active: true,
      fullName: 'demo/web',
      vm: { name: 'web', cpus: 2 },
      snapshots: ['snap1'],
    });
    expect(shown(ctx)).toEqual([
      [8080, true],
      [2222, false],
    ]);
  });

  it("shows the details to roles that can't list snapshots or forwards", async () => {
    rbac.allowed = denyOnly(['vms/snapshots'], ['vms/forwards']);
    const ctx = page();
    axios.get.mockResolvedValue({ data: { name: 'web' } });

    await ctx.getInfo({ name: 'web' });
    expect(axios.get.mock.calls.map(([url]) => url)).toEqual([
      'experiments/demo/vms/web',
    ]);
    expect(ctx.expModal).toMatchObject({ vm: { name: 'web' }, forwards: [] });
  });

  it('creates one and lists them again', async () => {
    const ctx = page({ portForwardModal: { ...modal } });
    ctx.expModal.fullName = 'demo/web';
    axios.post.mockResolvedValue({});
    axios.get.mockResolvedValue({ data: forwards });

    await ctx.createPortForward();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/web/forwards',
      null,
      { params: { src: '8080', host: '127.0.0.1', dst: '80' } },
    );
    expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
      expect.objectContaining({
        message: 'Port forward 8080 → 127.0.0.1:80 created for the web VM.',
      }),
    );
    expect(shown(ctx)).toEqual([
      [8080, true],
      [2222, false],
    ]);
    expect(ctx.portForwardModal).toMatchObject({ active: false, vmName: null });
  });

  it("doesn't list them again for a role that can't", async () => {
    rbac.allowed = denyOnly(['vms/forwards', 'list']);
    const ctx = page({ portForwardModal: { ...modal } });
    axios.post.mockResolvedValue({});

    await ctx.createPortForward();
    expect(axios.get).not.toHaveBeenCalled();
    expect(ctx.$buefy.toast.open).toHaveBeenCalled();
  });

  it('deletes one and lists the rest', async () => {
    const ctx = page();
    axios.delete.mockResolvedValue({});
    axios.get.mockResolvedValue({
      data: { listeners: forwards.listeners.slice(1) },
    });

    await ctx.deletePortForward('web', forwards.listeners[0]);
    expect(axios.delete).toHaveBeenCalledWith(
      'experiments/demo/vms/web/forwards',
      { params: { host: '127.0.0.1', dst: 80 } },
    );
    expect(shown(ctx)).toEqual([[2222, false]]);
  });
});

describe("saving the open VM's annotations", () => {
  const open = () =>
    page({
      expModal: { active: true, vm: { name: 'vm1', annotations: { a: '1' } } },
    });

  it('replaces them and shows what the server saved', async () => {
    axios.patch.mockResolvedValueOnce({
      data: { name: 'vm1', annotations: { b: true } },
    });
    const ctx = open();
    const done = vi.fn();
    await ctx.saveAnnotations({ b: true }, done);
    expect(axios.patch).toHaveBeenCalledWith('experiments/demo/vms/vm1', {
      annotations: { b: true },
    });
    expect(ctx.expModal.vm).toEqual({
      name: 'vm1',
      annotations: { b: true },
    });
    expect(done).toHaveBeenCalledWith(true);
  });

  it('keeps the editor open and says why when the server refuses', async () => {
    const err = new Error('invalid annotations');
    axios.patch.mockRejectedValueOnce(err);
    const ctx = open();
    const done = vi.fn();
    await ctx.saveAnnotations({ 'phenix/default-apps': 'no' }, done);
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(ctx.expModal.vm.annotations).toEqual({ a: '1' });
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

  // Answers a bulk action with a finished job; VMs named in `outcomes` get that
  // outcome, the rest succeed.
  const answerJob = (outcomes = {}) => {
    const items = (vms) =>
      vms.map((name) => {
        const outcome = outcomes[name] ?? { status: 'succeeded' };
        return outcome.status === 'succeeded'
          ? { name, status: 'succeeded', result: { name, state: 'ANSWERED' } }
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
    ['startVm', 'start', 'Start the VMs', 'Start', 'Started'],
    ['pauseVm', 'pause', 'Pause the VMs', 'Pause', 'Paused'],
    ['restartVm', 'restart', 'Restart the VMs', 'Restart', 'Restarted'],
    ['shutdownVm', 'shutdown', 'Shut Down the VMs', 'Shut Down', 'Shut down'],
    [
      'resetVmState',
      'resetDisk',
      "Reset the VMs' Disks",
      'Reset',
      'Reset the disks of',
    ],
  ])(
    '%s asks once about the VMs the %s rule allows, naming those it skips, then runs one job for them',
    async (method, action, title, confirmText, done) => {
      const ctx = page({
        experiment: { name: 'demo', vms: structuredClone(vms) },
      });
      answerJob();
      const { allowed, skipped } = partitionVMsForAction(
        action,
        selection,
        vms,
      );

      ctx[method](selection);
      expect(ctx.$buefy.dialog.alert).not.toHaveBeenCalled();
      const dialog = lastConfirm(ctx);
      expect(dialog).toMatchObject({ title, confirmText });
      expect(dialog.message).toContain(namedList(allowed, 'VM'));
      expect(dialog.message).toContain(skippedVMsText(skipped));
      expect(axios.post).not.toHaveBeenCalled();

      dialog.onConfirm();
      await flush();
      await flush();
      expect(ctx.isWaiting).toBe(false);

      // one request for all of them, not one each
      expect(axios.post.mock.calls).toEqual([
        ['experiments/demo/vms/actions', { action, vms: allowed }, undefined],
      ]);
      // each VM is shown as the job left it
      expect(
        ctx.experiment.vms
          .filter((vm) => vm.state === 'ANSWERED')
          .map((vm) => vm.name),
      ).toEqual(allowed);
      expect(toasts(ctx)).toEqual([
        expect.objectContaining({
          message: `${done} ${allowed.length} VMs`,
          type: 'is-success',
        }),
      ]);
    },
  );

  it('kills the VMs the kill rule allows and takes them out of the table', async () => {
    const ctx = page({ experiment: { name: 'demo', vms: [...vms] } });
    answerJob();
    const { skipped } = partitionVMsForAction('kill', selection, vms);

    ctx.killVm(selection);
    const dialog = lastConfirm(ctx);
    expect(dialog).toMatchObject({
      title: 'Kill the VMs',
      confirmText: 'KILL THEM!',
    });
    expect(dialog.message).toContain(
      '<p>This will kill the VMs web, persistent, and db. You will not be ' +
        'able to restore them until you restart the demo experiment!</p>',
    );
    expect(dialog.message).toContain(skippedVMsText(skipped));

    dialog.onConfirm();
    await flush();
    await flush();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/actions',
      { action: 'kill', vms: ['web', 'persistent', 'db'] },
      undefined,
    );
    expect(ctx.experiment.vms.map((vm) => vm.name)).toEqual(['off', 'held']);
  });

  it('says how a partly failed job went, and retries the VMs worth it', async () => {
    const ctx = page({
      experiment: { name: 'demo', vms: structuredClone(vms) },
    });
    answerJob({
      persistent: {
        status: 'failed',
        error: 'vm persistent is locked',
        retryable: true,
      },
      db: { status: 'failed', error: 'minimega says no' },
    });

    ctx.restartVm(['web', 'persistent', 'db']);
    lastConfirm(ctx).onConfirm();
    await flush();
    await flush();

    expect(toasts(ctx)).toEqual([]);
    expect(notices.open).toHaveBeenCalledTimes(1);
    const { message, indefinite } = lastNotice();
    expect(indefinite).toBe(true);
    const text = noticeText();
    expect(text).toContain('Restarted 1 of 3 VMs; 2 failed');
    expect(text).toContain('persistent: vm persistent is locked');
    expect(text).toContain('Retry failed (1)');

    // the retry starts a job for the retryable VM alone
    const retry = message.children.find((child) => child.type === 'button');
    retry.props.onClick();
    await flush();
    await flush();
    expect(axios.post.mock.lastCall).toEqual([
      'experiments/demo/vms/actions',
      { action: 'restart', vms: ['persistent'] },
      undefined,
    ]);
  });

  it('says which VMs it skips and why, in the one dialog', () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
    ctx.shutdownVm(selection);
    expect(lastConfirm(ctx).message).toBe(
      '<p>This will shut down the VMs web, persistent, and db.</p>' +
        '<p class="mt-3 has-text-grey-dark">The VMs off and held are not ' +
        'running. The gone VM is not shown in the table.</p>',
    );

    ctx.restartVm(selection);
    expect(lastConfirm(ctx).message).toContain(
      'The held VM is paused and must be resumed first. ' +
        'The gone VM is not shown in the table.',
    );
    expect(ctx.$buefy.dialog.alert).not.toHaveBeenCalled();
  });

  it('words its question for one VM', () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
    ctx.shutdownVm('web');
    expect(lastConfirm(ctx)).toMatchObject({
      title: 'Shut Down the VM',
      message: '<p>This will shut down the web VM.</p>',
    });
    ctx.resetVmState('web');
    expect(lastConfirm(ctx).title).toBe("Reset the VM's Disk");
    ctx.killVm('web');
    expect(lastConfirm(ctx)).toMatchObject({
      title: 'Kill the VM',
      message:
        '<p>This will kill the web VM. You will not be able to restore it ' +
        'until you restart the demo experiment!</p>',
      confirmText: 'KILL IT!',
    });
    expect(ctx.$buefy.dialog.alert).not.toHaveBeenCalled();
  });

  it('keeps waiting while the job is running', async () => {
    // the job store's poll while it waits
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    try {
      const ctx = page({ experiment: { name: 'demo', vms } });
      axios.post.mockResolvedValue({
        data: { id: 'j1', status: 'running', items: [], revision: 1 },
      });

      ctx.restartVm(['web', 'db']);
      lastConfirm(ctx).onConfirm();
      await flush();
      expect(ctx.isWaiting).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('reports a job that could not start and stops waiting', async () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
    const err = new Error('boom');
    axios.post.mockRejectedValue(err);

    ctx.restartVm('web');
    lastConfirm(ctx).onConfirm();
    await flush();
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(ctx.isWaiting).toBe(false);
  });

  it('creates snapshots of the VMs the snapshot rule allows, and sums up how it went', async () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
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

    ctx.captureSnapshot(selection);
    await flush();
    expect(ctx.$buefy.dialog.alert).not.toHaveBeenCalled();
    expect(lastConfirm(ctx)).toMatchObject({ title: 'Create VM Snapshots' });
    expect(lastConfirm(ctx).message).toContain(
      'This will create snapshots of the VMs web and db.',
    );
    expect(lastConfirm(ctx).message).toContain(skippedVMsText(skipped));

    lastConfirm(ctx).onConfirm();
    await flush();
    expect(axios.post.mock.calls.map(([url]) => url)).toEqual(
      allowed.map((name) => `experiments/demo/vms/${name}/snapshots`),
    );
    await flush();
    // one summary, not a notification for each failure
    expect(notify.error).not.toHaveBeenCalled();
    expect(notices.open).toHaveBeenCalledTimes(1);
    expect(noticeText()).toContain('Snapshotted 1 of 2 VMs; 1 failed');

    ctx.captureSnapshot('web');
    await flush();
    expect(lastConfirm(ctx)).toMatchObject({
      title: 'Create a VM Snapshot',
      message: '<p>This will create a snapshot of the web VM.</p>',
    });
  });

  it('creates memory snapshots in one go, summing up how they went', async () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
    axios.post.mockResolvedValue({});
    ctx.memorySnapshotModal = {
      active: true,
      vm: [
        { name: 'web', filename: 'web_1' },
        { name: 'db', filename: 'db_1' },
      ],
    };

    ctx.createMemorySnapshot(ctx.memorySnapshotModal.vm);
    expect(ctx.memorySnapshotModal).toEqual({ active: false, vm: [] });
    await flush();
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
    expect(toasts(ctx)).toEqual([
      expect.objectContaining({ message: 'Created memory snapshots of 2 VMs' }),
    ]);
  });

  it("suggests each backing image's name from the file name of the VM's disk", () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 8, 30, 12, 0, 0));
    try {
      const running = (name, disk) => ({
        name,
        running: true,
        snapshot: true,
        disk,
      });
      const ctx = page({
        experiment: {
          name: 'demo',
          vms: [
            // folders whose names hold the "." or "_" the name is cut at
            running('web', '/phenix/images/win.10/base.qc2'),
            running('db', '/phenix/images/my_vms/db_20260101120000.qc2'),
          ],
        },
      });

      ctx.diskImage(['web', 'db']);
      expect(ctx.diskImageModal.vm.map((vm) => vm.filename)).toEqual([
        'web_base_20260930120000',
        'db_db_20260930120000',
      ]);
    } finally {
      vi.useRealTimers();
    }
  });

  it('names the VMs the backing image and memory snapshot rules allow', () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
    const listed = (modal) => modal.vm.map((vm) => vm.name);

    ctx.diskImage(selection);
    const commit = partitionVMsForAction('commit', selection, vms);
    expect(lastAlert(ctx).message).toBe(skippedVMsText(commit.skipped));
    expect(ctx.diskImageModal.active).toBe(true);
    expect(listed(ctx.diskImageModal)).toEqual(commit.allowed);

    ctx.queueMemorySnapshotVMs(selection);
    const memory = partitionVMsForAction('memorySnapshot', selection, vms);
    expect(lastAlert(ctx).message).toBe(skippedVMsText(memory.skipped));
    expect(ctx.memorySnapshotModal.active).toBe(true);
    expect(listed(ctx.memorySnapshotModal)).toEqual(memory.allowed);
  });

  it('lists the VMs the redeploy rule allows in the redeploy modal', () => {
    const syncing = { ...vms[0], name: 'syncing', busy: true };
    const ctx = page({ experiment: { name: 'demo', vms: [...vms, syncing] } });
    axios.get.mockResolvedValue({ data: { disks: [] } });

    ctx.redeploy(['web', 'syncing', 'gone']);
    expect(lastAlert(ctx).message).toBe(
      'The syncing VM is busy with another action. ' +
        'The gone VM is not shown in the table.',
    );
    expect(ctx.redeployModal).toMatchObject({
      active: true,
      vm: [
        {
          name: 'web',
          cpus: 2,
          ram: 1024,
          disk: '/images/web.qc2',
          inject: false,
        },
      ],
    });
    // the disk images the modal offers: this experiment's
    expect(axios.get).toHaveBeenCalledWith('disks', {
      params: { expName: 'demo' },
    });
  });

  it('redeploys a VM on its own disk unless another one is picked', async () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
    const disks = [{ name: 'web.qc2', fullPath: '/images/win/web.qc2' }];
    axios.get.mockResolvedValue({ data: { disks } });
    axios.post.mockResolvedValue({});
    const sent = () => axios.post.mock.lastCall;

    ctx.redeploy(['web']);
    await flush();
    expect(ctx.disks).toEqual(disks);
    ctx.redeployVm(ctx.redeployModal.vm);
    // a snapshot VM's disk is the image under its snapshot overlay, which
    // sending it back would drop
    expect(sent()).toEqual([
      'experiments/demo/vms/web/redeploy',
      { cpus: 2, ram: 1024 },
    ]);

    ctx.resetRedeployModal();
    ctx.redeploy(['web']);
    ctx.redeployModal.vm[0].disk = '/images/win/web.qc2';
    ctx.redeployVm(ctx.redeployModal.vm);
    expect(sent()).toEqual([
      'experiments/demo/vms/web/redeploy',
      { cpus: 2, ram: 1024, disk: '/images/win/web.qc2' },
    ]);
  });

  it('offers each VM in the redeploy window the listed disks, warning beside its picker', async () => {
    stubBrowser();
    // the netflow check that creating the page makes
    axios.get.mockRejectedValue(new Error('no netflow'));
    const redeploying = (name, path) => ({
      name,
      cpus: 2,
      ram: 1024,
      disk: path,
      currentDisk: path,
      inject: false,
    });
    const html = await renderSSR(
      RunningExperiment,
      {},
      {
        route: '/experiment/demo',
        data: {
          disks: [
            disk('/images/win/web.qc2', 'win/web.qc2'),
            disk('/images/web.qc2', 'web.qc2'),
            disk('/data/vms/out.img'),
          ],
          redeployModal: {
            active: true,
            vm: [
              redeploying('web', '/images/win/web.qc2'),
              redeploying('out', '/data/vms/out.img'),
              redeploying('gone', '/images/gone.qc2'),
            ],
          },
        },
      },
    );

    // each VM's disk picker: its options, and the warnings beside it
    const pickers = Object.fromEntries(
      openModal(html)
        .split('<div class="disk-select')
        .slice(1)
        .map((part) => {
          const picker = part.slice(0, part.indexOf('Replicate Original'));
          const select = picker.match(/<select\b[^>]*>[\s\S]*?<\/select>/)[0];
          return [
            select.match(/aria-label="Disk for VM ([^"]*)"/)[1],
            {
              options: selectOptions(select).map(
                ({ group, text, disabled }) =>
                  `${group ? `[${group}] ` : ''}${text}` +
                  (disabled ? ' (disabled)' : ''),
              ),
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

  it("offers a role that may not list disks the VM's own disk alone", () => {
    rbac.allowed = denyOnly(['disks']);
    const ctx = page({ experiment: { name: 'demo', vms } });

    ctx.redeploy(['web']);
    expect(axios.get).not.toHaveBeenCalled();
    expect(ctx.disks).toBeNull();
    expect(ctx.redeployModal.vm[0].disk).toBe('/images/web.qc2');
  });

  it('opens nothing when every VM is skipped', () => {
    const ctx = page({ experiment: { name: 'demo', vms } });
    const methods = [
      'startVm',
      'pauseVm',
      'restartVm',
      'shutdownVm',
      'killVm',
      'redeploy',
      'resetVmState',
      'captureSnapshot',
      'diskImage',
      'queueMemorySnapshotVMs',
    ];
    for (const method of methods) ctx[method](['gone']);
    expect(ctx.$buefy.dialog.alert).toHaveBeenCalledTimes(methods.length);
    expect(ctx.$buefy.dialog.confirm).not.toHaveBeenCalled();
    expect(ctx.redeployModal.active).toBe(false);
    expect(ctx.diskImageModal.active).toBe(false);
    expect(ctx.memorySnapshotModal.active).toBe(false);
    expect(axios.get).not.toHaveBeenCalled();
  });
});

describe("showing the server's copy of a VM", () => {
  const db = { name: 'db', running: true, screenshot: 'db.png' };
  // the page showing web, in the given running state, with its screenshot
  const showing = (running) =>
    page({
      experiment: {
        name: 'demo',
        vms: [{ name: 'web', running, screenshot: 'web.png' }, db],
      },
    });

  it.each([
    ['keeps the screenshot while the VM stays running', true, true, 'web.png'],
    ['drops it when the VM stops', true, false, ''],
    ['drops it when the VM starts again', false, true, ''],
  ])('%s', (_, before, after, screenshot) => {
    const ctx = showing(before);
    ctx.replaceVm({ name: 'web', running: after, state: 'NEW' });
    expect(ctx.experiment.vms).toEqual([
      { name: 'web', running: after, state: 'NEW', screenshot },
      db,
    ]);
  });

  it("shows the copy's own screenshot", () => {
    const ctx = showing(true);
    ctx.replaceVm({ name: 'web', running: true, screenshot: 'new.png' });
    expect(ctx.experiment.vms[0].screenshot).toBe('new.png');
  });

  it('ignores a VM the table no longer shows', () => {
    const ctx = showing(true);
    const before = ctx.experiment.vms;
    ctx.replaceVm({ name: 'gone', running: true });
    expect(ctx.experiment.vms).toBe(before);
  });
});

describe('the selection toolbar', () => {
  const experiment = {
    name: 'demo',
    vms: [{ name: 'web' }, { name: 'db' }],
  };
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
  // the aria-labels of the toolbar's buttons
  const toolbar = async (data) =>
    tooltipButtons(
      await renderSSR(
        RunningExperiment,
        {},
        {
          route: '/experiment/demo',
          data: { experiment, vmSelectedArray: ['web', 'db'], ...data },
        },
      ),
    )
      .map((button) => button.label)
      .filter((label) => / selected VMs$|^Close state actions$/.test(label));

  beforeEach(() => {
    stubBrowser();
    // the netflow check that creating the page makes
    axios.get.mockRejectedValue(new Error('no netflow'));
  });
  afterEach(() => vi.unstubAllGlobals());

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

  // Renders web and db (selected, running or not), clicks the `labels` buttons
  // in turn, and returns the dialog or modal, skipped count, state and HTML.
  async function click(labels, { running = true, ...data } = {}) {
    const vms = ['web', 'db'].map((name) => ({
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
    }));
    const dialog = { confirm: vi.fn(), alert: vi.fn() };
    let state = {
      experiment: { name: 'demo', vms },
      vmSelectedArray: ['web', 'db'],
      ...data,
    };
    for (const label of labels) {
      // each button's click handler, by its aria-label
      const clicks = {};
      const BButton = {
        inheritAttrs: false,
        setup(_, { attrs }) {
          clicks[attrs['aria-label']] = attrs.onClick;
          return () => h('button');
        },
      };
      let clicked;
      await renderSSR(
        {
          ...RunningExperiment,
          components: { ...RunningExperiment.components, BButton },
          mixins: [
            ...RunningExperiment.mixins,
            {
              created() {
                clicked = this;
                this.$buefy = { dialog, toast: { open: vi.fn() } };
              },
            },
          ],
        },
        {},
        { route: '/experiment/demo', data: state },
      );
      if (!clicks[label]) throw new Error(`no button labeled ${label}`);
      clicks[label]();
      state = { ...clicked.$data };
    }

    const html = await renderSSR(
      RunningExperiment,
      {},
      { route: '/experiment/demo', tables: true, data: state },
    );
    const confirm = dialog.confirm.mock.lastCall?.[0];
    const modal = openModal(html);
    return {
      opened: confirm
        ? { title: confirm.title, text: confirm.message }
        : modal && {
            title: textOf(modal.match(/<header[\s\S]*?<\/header>/)[0]),
            text: textOf(modal.match(/<section[\s\S]*?<\/section>/)[0]),
          },
      skipped: dialog.alert.mock.calls.length,
      state,
      html,
    };
  }

  // the checkboxes checked in the VM table, by their labels
  const checked = (html) =>
    [
      ...html
        .match(/<table class="table-rows"[\s\S]*?<\/table>/)[0]
        .matchAll(/<label class="b-checkbox[\s\S]*?<\/label>/g),
    ]
      .filter(([box]) => /<input[^>]*\schecked/.test(box))
      .map(([box]) => textOf(box));

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
      const labels = stateBar
        ? ['Modify state of selected VMs', label]
        : [label];
      const { opened, skipped } = await click(labels, { running });
      expect(opened.title).toBe(title);
      expect(opened.text).toMatch(/\bweb\b[\s\S]*\bdb\b/);
      expect(skipped).toBe(0);
    },
  );

  it('clears the selection once a button starts its action', async () => {
    const selected = { checkAll: true, vmSelectedArray: ['web', 'db'] };
    expect(checked((await click([], selected)).html)).toEqual([
      'Select all VMs',
      'Select VM web',
      'Select VM db',
    ]);

    const { html, state } = await click(
      ['Modify state of selected VMs', 'Kill selected VMs'],
      selected,
    );
    expect(checked(html)).toEqual([]);
    expect(await toolbar(state)).toEqual([]);
    // selecting again starts over with the main actions
    expect(await toolbar({ ...state, vmSelectedArray: ['web', 'db'] })).toEqual(
      MAIN_BAR,
    );
  });
});
