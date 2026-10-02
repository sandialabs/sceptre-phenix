// How the running experiment page starts packet captures (one interface, or
// every interface of a VM from the Actions column) with a spinner while it
// waits, picks a CD-ROM, and finds and shows a VM's disk, its backing image
// chain and its CD-ROM's ISO image.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notify, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/experimentMocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif', modules.errorNotif);
vi.mock('@/store', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);

import { cachePage, clearPageCache } from '@/utils/pageCache.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { flush } from './helpers/async.js';
import {
  labeledControls,
  openModal,
  renderSSR,
  selectOptions,
  stubBrowser,
  textOf,
  tooltipButtons,
} from './helpers/render.js';
import { page } from './helpers/runningExperiment.js';

const web = {
  name: 'web',
  running: true,
  networks: ['DMZ (149)', 'disconnected', 'MGMT (151)'],
  captures: [],
};

beforeEach(() => {
  resetMocks();
  clearPageCache();
});

describe('starting one capture', () => {
  it('shows a spinner until the start dialog opens', async () => {
    const ctx = page();
    const request = Promise.withResolvers();
    axios.get.mockReturnValue(request.promise);

    ctx.handlePcap(web, 0);
    expect(ctx.isCapturePending('web', 0)).toBe(true);
    expect(ctx.pendingCaptureIfaces('web')).toEqual([0]);
    expect(ctx.$buefy.dialog.confirm).not.toHaveBeenCalled();

    // a second click while waiting sends nothing more
    ctx.handlePcap(web, 0);
    expect(axios.get).toHaveBeenCalledTimes(1);

    request.resolve({ data: { captures: [] } });
    await flush();
    expect(ctx.isCapturePending('web', 0)).toBe(false);
    expect(ctx.pendingCaptureIfaces('web')).toEqual([]);
    expect(ctx.$buefy.dialog.confirm).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Start a Packet Capture' }),
    );
  });

  it('stops the spinner and says why when the request fails', async () => {
    const ctx = page();
    const err = new Error('boom');
    axios.get.mockRejectedValue(err);

    ctx.handlePcap(web, 2);
    await flush();
    expect(ctx.isCapturePending('web', 2)).toBe(false);
    expect(notify.error).toHaveBeenCalledWith(err);
  });

  it('keeps spinners per VM and interface', () => {
    const ctx = page();
    ctx.setCapturePending('web', 0, true);
    ctx.setCapturePending('web', 2, true);
    ctx.setCapturePending('db', 0, true);
    expect(ctx.pendingCaptureIfaces('web')).toEqual([0, 2]);
    ctx.setCapturePending('web', 0, false);
    expect(ctx.pendingCaptureIfaces('web')).toEqual([2]);
    expect(ctx.isCapturePending('db', 0)).toBe(true);
  });
});

describe('capturing all interfaces', () => {
  it('asks to start one capture per connected interface, then starts them', async () => {
    const ctx = page();
    axios.get.mockResolvedValue({ data: { captures: [{ interface: 2 }] } });
    axios.post.mockResolvedValue({ status: 204 });

    const run = ctx.captureAllInterfaces(web);
    expect(ctx.captureAllPending.web).toBe(true);
    await run;
    expect(ctx.captureAllPending.web).toBeUndefined();

    const dialog = ctx.$buefy.dialog.confirm.mock.calls[0][0];
    expect(dialog.title).toBe('Start a Packet Capture');
    expect(dialog.message).toMatch(/on interface 0 of the web VM/);
    expect(dialog.message).toMatch(/web_0_\d{4}-\d\d-\d\d_\d{4}\.pcap/);

    const confirmed = dialog.onConfirm();
    expect(ctx.captureAllPending.web).toBe(true);
    await confirmed;
    expect(ctx.captureAllPending.web).toBeUndefined();
    expect(axios.post).toHaveBeenCalledTimes(1);
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/web/captures',
      { interface: 0, filename: expect.stringMatching(/^web_0_.*\.pcap$/) },
    );
  });

  it('starts several with plural wording and reports each failure', async () => {
    const ctx = page();
    axios.get.mockResolvedValue({ data: { captures: [] } });
    const err = new Error('capture already exists');
    axios.post.mockResolvedValueOnce({}).mockRejectedValueOnce(err);

    await ctx.captureAllInterfaces(web);
    const dialog = ctx.$buefy.dialog.confirm.mock.calls[0][0];
    expect(dialog.title).toBe('Start Packet Captures');
    expect(dialog.message).toMatch(/packet captures on interfaces 0 and 2/);
    expect(dialog.confirmText).toBe('Start All');

    await dialog.onConfirm();
    expect(axios.post.mock.calls.map(([, body]) => body.interface)).toEqual([
      0, 2,
    ]);
    expect(notify.error).toHaveBeenCalledTimes(1);
    expect(notify.error).toHaveBeenCalledWith(err);
    expect(ctx.captureAllPending.web).toBeUndefined();
  });

  it('says so when every interface is already being captured', async () => {
    const ctx = page();
    axios.get.mockResolvedValue({
      data: { captures: [{ interface: 0 }, { interface: 2 }] },
    });

    await ctx.captureAllInterfaces(web);
    expect(ctx.$buefy.dialog.confirm).not.toHaveBeenCalled();
    expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
      expect.objectContaining({
        message: expect.stringMatching(/already being captured/),
      }),
    );
  });
});

describe('stopping all captures', () => {
  const capturing = { ...web, captures: [{ interface: 0 }, { interface: 2 }] };

  it('asks once, then stops every capture with a spinner', async () => {
    const ctx = page();
    const stop = Promise.withResolvers();
    axios.delete.mockReturnValue(stop.promise);

    ctx.stopAllCaptures(capturing);
    const dialog = ctx.$buefy.dialog.confirm.mock.calls[0][0];
    expect(dialog.message).toMatch(/interfaces 0 and 2 of the web VM/);
    expect(dialog.confirmText).toBe('Stop All');

    const run = dialog.onConfirm();
    expect(ctx.captureAllPending.web).toBe(true);
    expect(axios.delete).toHaveBeenCalledWith(
      'experiments/demo/vms/web/captures',
    );
    stop.resolve({});
    await run;
    expect(ctx.captureAllPending.web).toBeUndefined();
  });

  it('reports a failed stop and stops the spinner', async () => {
    const ctx = page();
    axios.delete.mockRejectedValue(new Error('boom'));

    ctx.stopAllCaptures(capturing);
    await ctx.$buefy.dialog.confirm.mock.calls[0][0].onConfirm();

    expect(notify.error).toHaveBeenCalled();
    expect(ctx.captureAllPending.web).toBeUndefined();
  });
});

describe('the CD-ROM picker', () => {
  afterEach(() => vi.unstubAllGlobals());

  // ISO images inside the files directory /images, one in a folder and one
  // minimega cannot take, and a same-named one outside it
  const iso = (fullPath, relativePath = '') => ({
    kind: 'ISO',
    name: fullPath.replace(/^.*\//, ''),
    fullPath,
    relativePath,
    outsideFilesDir: !relativePath,
  });
  const isos = [
    iso('/images/virtio.iso', 'virtio.iso'),
    iso('/images/tools.iso', 'tools.iso'),
    iso('/images/win/drivers.iso', 'win/drivers.iso'),
    iso('/images/my disc.iso', 'my disc.iso'),
    iso('/data/isos/tools.iso'),
  ];

  it('shows a spinner while it lists the ISO images', async () => {
    const ctx = page();
    const request = Promise.withResolvers();
    axios.get.mockReturnValue(request.promise);

    const open = ctx.showChangeDisc({ name: 'web', cdRom: '' });
    expect(ctx.opticalDiscModal).toMatchObject({
      active: true,
      vmName: 'web',
      loading: true,
      current: '',
      disc: null,
    });
    expect(axios.get).toHaveBeenCalledWith('disks', {
      params: { diskType: 'ISO' },
    });

    request.resolve({ data: { disks: isos } });
    await open;
    expect(ctx.opticalDiscModal.loading).toBe(false);
    expect(ctx.opticalDiscModal.isos).toEqual(isos);
  });

  it('starts from the inserted ISO', async () => {
    const ctx = page();
    axios.get.mockResolvedValue({ data: { disks: isos } });
    await ctx.showChangeDisc({ name: 'web', cdRom: '/images/tools.iso' });
    expect(ctx.opticalDiscModal).toMatchObject({
      current: '/images/tools.iso',
      disc: '/images/tools.iso',
    });
    expect(ctx.insertedIsoLabel).toBe('tools.iso');

    await ctx.showChangeDisc({ name: 'web', cdRom: '/images/win/drivers.iso' });
    expect(ctx.insertedIsoLabel).toBe('win/drivers.iso');

    // one the list does not hold, by its full path, as a same-named ISO in
    // another folder may be listed
    await ctx.showChangeDisc({ name: 'web', cdRom: '/images/old/tools.iso' });
    expect(ctx.insertedIsoLabel).toBe('/images/old/tools.iso');
  });

  it('is empty, not silent, when there are no ISO images', async () => {
    const ctx = page();
    axios.get.mockResolvedValue({ data: { disks: [] } });
    await ctx.showChangeDisc({ name: 'web' });
    expect(ctx.opticalDiscModal).toMatchObject({
      loading: false,
      error: null,
      isos: [],
    });
  });

  it('says why when the images cannot be listed', async () => {
    const ctx = page();
    const err = new Error('forbidden');
    axios.get.mockRejectedValue(err);
    await ctx.showChangeDisc({ name: 'web' });
    expect(ctx.opticalDiscModal.loading).toBe(false);
    expect(ctx.opticalDiscModal.error).toMatch(/couldn't be listed/);
    expect(notify.error).toHaveBeenCalledWith(err);
  });

  it("explains a role that can't list disks without asking the server", async () => {
    rbac.allowed = (resource) => resource !== 'disks';
    const ctx = page();
    await ctx.showChangeDisc({ name: 'web' });
    expect(axios.get).not.toHaveBeenCalled();
    expect(ctx.opticalDiscModal.error).toMatch(/can't list disk images/);
  });

  it('ignores a listing that arrives after the picker closed', async () => {
    const ctx = page();
    const request = Promise.withResolvers();
    axios.get.mockReturnValue(request.promise);
    const open = ctx.showChangeDisc({ name: 'web' });
    ctx.resetOpticalDiscModal();
    request.resolve({ data: { disks: isos } });
    await open;
    expect(ctx.opticalDiscModal).toMatchObject({ active: false, isos: [] });
  });

  it('labels its list of ISO images, and each image by its label', async () => {
    stubBrowser();
    // the netflow check that creating the page makes
    axios.get.mockRejectedValue(new Error('no netflow'));
    const html = await renderSSR(
      RunningExperiment,
      {},
      {
        route: '/experiment/demo',
        data: {
          opticalDiscModal: {
            active: true,
            vmName: 'web',
            loading: false,
            error: null,
            isos,
            current: '/images/win/drivers.iso',
            disc: null,
          },
        },
      },
    );

    const modal = openModal(html);
    expect(labeledControls(modal)).toEqual([
      { label: 'ISO image', control: 'select' },
    ]);
    expect(textOf(modal)).toContain('Inserted: win/drivers.iso');
    // the options by label, sorted, those outside the files directory in a
    // group of their own, and one minimega cannot take disabled
    const select = modal.match(/<select\b[\s\S]*?<\/select>/)[0];
    expect(
      selectOptions(select).map(
        ({ group, value, text, disabled }) =>
          `${group ? `[${group}] ` : ''}${text} = ${value}` +
          (disabled ? ' (disabled)' : ''),
      ),
    ).toEqual([
      'Select an ISO image =  (disabled)',
      'my disc.iso (minimega cannot use this name) = /images/my disc.iso ' +
        '(disabled)',
      'tools.iso = /images/tools.iso',
      'virtio.iso = /images/virtio.iso',
      'win/drivers.iso = /images/win/drivers.iso',
      '[Outside the standard images directory] /data/isos/tools.iso = ' +
        '/data/isos/tools.iso',
    ]);
  });

  it('inserts the picked ISO and ejects the inserted one', () => {
    const ctx = page();
    axios.post.mockResolvedValue({});
    axios.delete.mockResolvedValue({});

    ctx.opticalDiscModal = {
      ...ctx.opticalDiscModal,
      vmName: 'web',
      disc: '/images/tools.iso',
    };
    ctx.insertOpticalDisc();
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/web/cdrom',
      null,
      { params: { isoPath: '/images/tools.iso' } },
    );
    expect(ctx.opticalDiscModal.active).toBe(false);

    ctx.opticalDiscModal = { ...ctx.opticalDiscModal, vmName: 'web' };
    ctx.ejectOpticalDisc();
    expect(axios.delete).toHaveBeenCalledWith('experiments/demo/vms/web/cdrom');
  });
});

describe("the VM details' disk and backing image chain", () => {
  const disks = [
    {
      kind: 'VM',
      name: 'web_snap.qc2',
      fullPath: '/images/web/web_snap.qc2',
      relativePath: 'web/web_snap.qc2',
      backingImages: ['/images/jammy/jammy.qc2', '/data/vms/jammy.qc2'],
    },
    {
      kind: 'VM',
      name: 'jammy.qc2',
      fullPath: '/images/jammy/jammy.qc2',
      relativePath: 'jammy/jammy.qc2',
      backingImages: ['/data/vms/jammy.qc2'],
    },
    {
      kind: 'VM',
      name: 'jammy.qc2',
      fullPath: '/data/vms/jammy.qc2',
      relativePath: '',
      outsideFilesDir: true,
      backingImages: [],
    },
    {
      kind: 'ISO',
      name: 'tools.iso',
      fullPath: '/images/isos/tools.iso',
      relativePath: 'isos/tools.iso',
      outsideFilesDir: false,
      backingImages: [],
    },
  ];

  it("shows the open VM's disk and its chain, each by its label", async () => {
    cachePage('disks', disks);
    const vm = {
      name: 'web',
      disk: '/images/web/web_snap.qc2',
      cdRom: '/images/isos/tools.iso',
    };
    const ctx = page({ expModal: { active: true, vm, backingChain: null } });
    await ctx.loadBackingChain(vm);
    expect(ctx.expModal).toMatchObject({
      backingChain: ['jammy/jammy.qc2', '/data/vms/jammy.qc2'],
      disk: disks[0],
      cdRomDisk: disks[3],
      filesDir: '/images',
    });
  });

  it('finds the ISO image in the CD-ROM drive of a VM without a disk', async () => {
    cachePage('disks', disks);
    const vm = { name: 'live', disk: '', cdRom: '/images/isos/tools.iso' };
    const ctx = page({ expModal: { active: true, vm, backingChain: null } });
    await ctx.loadBackingChain(vm);
    expect(ctx.expModal).toMatchObject({
      backingChain: [],
      disk: null,
      cdRomDisk: disks[3],
    });
  });

  it('names them by label in the details on the page, warning of one outside the files directory', async () => {
    stubBrowser();
    // the netflow check that creating the page makes
    axios.get.mockRejectedValue(new Error('no netflow'));
    const vm = {
      name: 'web',
      state: 'RUNNING',
      running: true,
      snapshot: true,
      cpus: 1,
      ram: 1024,
      disk: '/data/vms/jammy.qc2',
      cdRom: '/images/isos/tools.iso',
      networks: [],
      ipv4: [],
      taps: [],
      captures: [],
    };
    const html = await renderSSR(
      RunningExperiment,
      {},
      {
        route: '/experiment/demo',
        data: {
          expModal: {
            active: true,
            fullName: 'demo/web',
            vm,
            snapshots: false,
            forwards: [],
            backingChain: [],
            disk: disks[2],
            cdRomDisk: disks[3],
            filesDir: '/images',
          },
        },
      },
    );

    // the Storage section of the open details
    const details = html.slice(html.indexOf('<div class="modal is-active"'));
    const start = details.indexOf('Storage');
    const storage = details.slice(
      start,
      details.indexOf('<p class="vm-section-title"', start),
    );
    expect(textOf(storage)).toMatch(
      /Disk \/data\/vms\/jammy\.qc2 .*CD-ROM isos\/tools\.iso$/,
    );
    expect(
      tooltipButtons(storage)
        .filter((b) => b.label === 'Outside the standard images directory')
        .map((b) => b.tooltip),
    ).toEqual([
      'Outside the standard images directory (/images). minimega does not ' +
        'copy this image to other cluster nodes, so on a multi-node cluster ' +
        'every node that may run a VM using it needs the same file at this ' +
        'path.',
    ]);
  });

  it("leaves it out for roles that can't list disks", async () => {
    rbac.allowed = (resource) => resource !== 'disks';
    const vm = { name: 'web', disk: '/images/web_snap.qc2' };
    const ctx = page({ expModal: { active: true, vm, backingChain: null } });
    await ctx.loadBackingChain(vm);
    expect(axios.get).not.toHaveBeenCalled();
    expect(ctx.expModal.backingChain).toBeNull();
  });

  it('drops a chain that arrives after another VM was opened', async () => {
    cachePage('disks', disks);
    const ctx = page({
      expModal: { active: true, vm: { name: 'db' }, backingChain: null },
    });
    await ctx.loadBackingChain({
      name: 'web',
      disk: '/images/web/web_snap.qc2',
    });
    expect(ctx.expModal.backingChain).toBeNull();
  });
});
