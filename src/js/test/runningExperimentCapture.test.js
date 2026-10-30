// The running experiment page: packet captures, the CD-ROM picker, and the VM
// details disk and backing chain.
import { flushPromises } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, modules, notices, noticeText, notify, rbac, resetMocks } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/notify.js', modules.notify);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/pageLoader.js', modules.pageLoader);

import { cachePage, clearPageCache } from '@/utils/pageCache.js';
import RunningExperiment from '@/views/experiment/RunningExperiment.vue';
import { never } from './helpers/async.js';
import { disk, vm } from './helpers/fixtures.js';
import { denyOnly } from './helpers/permissions.js';
import {
  dialogs,
  mountPage,
  tableRows,
  textOf,
  toasts,
  tooltipButtons,
} from './helpers/mount.js';

const web = vm({
  networks: ['DMZ (149)', 'disconnected', 'MGMT (151)'],
  ipv4: ['10.0.0.5', '', '10.1.0.5'],
  taps: ['tap0', 'tap1', 'tap2'],
});

const capturesUrl = (name) => `experiments/demo/vms/${name}/captures`;

// Answers the page's GETs: a URL in `answers` with what its function gives,
// a VM's details, snapshots and forwards from `vms`, and fails the rest.
function answerGets(vms, answers = {}) {
  axios.get.mockImplementation(async (url) => {
    if (url in answers) return answers[url]();
    const [, name, part] =
      url.match(/^experiments\/demo\/vms\/([^/]+)(.*)$/) ?? [];
    const found = vms.find((v) => v.name === name);
    if (found && part === '') return { data: found };
    if (found && part === '/snapshots') return { data: { snapshots: [] } };
    if (found && part === '/forwards') return { data: { listeners: [] } };
    throw new Error(`no answer for ${url}`);
  });
}

// the demo experiment's page, listing `vms`
const openPage = (vms, answers) => {
  answerGets(vms, answers);
  return mountPage(RunningExperiment, {
    route: '/experiment/demo',
    data: { experiment: { name: 'demo', running: true, vms } },
  });
};

// the cells of a VM's row in the VM table
const rowOf = (page, name) =>
  tableRows(page).find(
    (cells) => textOf(cells.Node.find('button.tag')) === name,
  );

// clicks the tap of a VM's interface, which starts or stops its capture
const clickTap = (page, name, iface) =>
  rowOf(page, name).Taps.findAll('.field button')[iface].trigger('click');

// the interfaces of a VM whose taps show a spinner
const spinningTaps = (page, name) =>
  rowOf(page, name)
    .Taps.findAll('.field button')
    .flatMap((tap, i) => (tap.attributes('aria-busy') === 'true' ? [i] : []));

// a button in a VM's Actions column
const rowAction = (page, name, action) =>
  rowOf(page, name).Actions.find(`.vm-row-${action}`);

// opens a VM's details by its name in the table
async function openDetails(page, name) {
  await rowOf(page, name).Node.find('button.tag').trigger('click');
  await flushPromises();
  return page.find('.vm-card');
}

const getUrls = () => axios.get.mock.calls.map(([url]) => url);

beforeEach(() => {
  resetMocks();
  clearPageCache();
});

describe('starting one capture', () => {
  it('starts it without asking, with a spinner until the server has started it', async () => {
    const request = Promise.withResolvers();
    const started = Promise.withResolvers();
    const page = await openPage([web], {
      [capturesUrl('web')]: () => request.promise,
    });
    axios.post.mockReturnValue(started.promise);

    await clickTap(page, 'web', 0);
    expect(spinningTaps(page, 'web')).toEqual([0]);

    // a second click while waiting sends nothing more
    await clickTap(page, 'web', 0);
    expect(getUrls().filter((url) => url === capturesUrl('web'))).toHaveLength(
      1,
    );

    request.resolve({ data: { captures: [] } });
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(spinningTaps(page, 'web')).toEqual([0]);
    expect(axios.post).toHaveBeenCalledWith(capturesUrl('web'), {
      interface: 0,
      filename: expect.stringMatching(/^web_0_.*\.pcap$/),
    });

    started.resolve({});
    await flushPromises();
    expect(spinningTaps(page, 'web')).toEqual([]);
    expect(page.find('.loading-overlay').exists()).toBe(false);
  });

  it("stops the interface's capture from the details without asking or closing them", async () => {
    const page = await openPage([{ ...web, captures: [{ interface: 2 }] }], {
      [capturesUrl('web')]: async () => ({
        data: { captures: [{ interface: 0 }, { interface: 2 }] },
      }),
    });
    const stop = Promise.withResolvers();
    axios.delete.mockReturnValue(stop.promise);
    const card = await openDetails(page, 'web');

    await card.find('.vm-capture-stop').trigger('click');
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(axios.delete).toHaveBeenCalledWith(capturesUrl('web'), {
      params: { iface: 2 },
    });
    expect(page.find('.vm-card .vm-capture-stop').classes()).toContain(
      'is-loading',
    );
    expect(page.find('.loading-overlay').exists()).toBe(false);

    stop.resolve({});
    await flushPromises();
    expect(page.find('.vm-card').exists()).toBe(true);
    expect(page.find('.vm-card .vm-capture-stop').classes()).not.toContain(
      'is-loading',
    );
  });

  it('stops the spinner and says why when the request fails', async () => {
    const err = new Error('boom');
    const page = await openPage([web], {
      [capturesUrl('web')]: () => Promise.reject(err),
    });

    await clickTap(page, 'web', 2);
    await flushPromises();
    expect(spinningTaps(page, 'web')).toEqual([]);
    expect(notify.error).toHaveBeenCalledWith(err);
  });

  it('keeps spinners per VM and interface', async () => {
    const answers = { web: [], db: [] };
    const waiting = (name) => () => {
      const request = Promise.withResolvers();
      answers[name].push(request);
      return request.promise;
    };
    const db = { ...web, name: 'db' };
    const page = await openPage([web, db], {
      [capturesUrl('web')]: waiting('web'),
      [capturesUrl('db')]: waiting('db'),
    });
    const card = await openDetails(page, 'web');
    // the details card's capture buttons, on the connected interfaces 0 and 2
    const cardSpinners = () =>
      card.findAll('.vm-capture').map((b) => b.classes('is-loading'));

    await clickTap(page, 'web', 0);
    await clickTap(page, 'web', 2);
    await clickTap(page, 'db', 0);
    expect(spinningTaps(page, 'web')).toEqual([0, 2]);
    expect(cardSpinners()).toEqual([true, true]);

    answers.web[0].resolve({ data: { captures: [] } });
    await flushPromises();
    expect(spinningTaps(page, 'web')).toEqual([2]);
    expect(cardSpinners()).toEqual([false, true]);
    expect(spinningTaps(page, 'db')).toEqual([0]);
  });
});

describe('capturing all interfaces', () => {
  it('starts one capture per connected interface without asking, spinning until they start', async () => {
    const listed = Promise.withResolvers();
    const started = Promise.withResolvers();
    const page = await openPage([web], {
      [capturesUrl('web')]: () => listed.promise,
    });
    axios.post.mockReturnValue(started.promise);
    const button = () => rowAction(page, 'web', 'captureAll');

    await button().trigger('click');
    expect(button().classes()).toContain('is-loading');
    listed.resolve({ data: { captures: [{ interface: 2 }] } });
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(button().classes()).toContain('is-loading');
    started.resolve({ status: 204 });
    await flushPromises();
    expect(button().classes()).not.toContain('is-loading');
    expect(axios.post).toHaveBeenCalledTimes(1);
    expect(axios.post).toHaveBeenCalledWith(capturesUrl('web'), {
      interface: 0,
      filename: expect.stringMatching(/^web_0_.*\.pcap$/),
    });
  });

  it('starts several and sums up how they went', async () => {
    const page = await openPage([web], {
      [capturesUrl('web')]: async () => ({ data: { captures: [] } }),
    });
    const err = new Error('capture already exists');
    axios.post.mockResolvedValueOnce({}).mockRejectedValueOnce(err);

    await rowAction(page, 'web', 'captureAll').trigger('click');
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(axios.post.mock.calls.map(([, body]) => body.interface)).toEqual([
      0, 2,
    ]);
    // one summary, naming the failure
    expect(notify.error).not.toHaveBeenCalled();
    expect(notices.open).toHaveBeenCalledTimes(1);
    expect(noticeText()).toContain('interface 2: capture already exists');
    expect(rowAction(page, 'web', 'captureAll').classes()).not.toContain(
      'is-loading',
    );
  });

  it('says so when every interface is already being captured', async () => {
    const page = await openPage([web], {
      [capturesUrl('web')]: async () => ({
        data: { captures: [{ interface: 0 }, { interface: 2 }] },
      }),
    });

    await rowAction(page, 'web', 'captureAll').trigger('click');
    await flushPromises();
    expect(dialogs()).toEqual([]);
    expect(toasts()).toEqual([
      expect.objectContaining({
        text: expect.stringMatching(/already captures/),
      }),
    ]);
  });
});

describe('stopping all captures', () => {
  const capturing = { ...web, captures: [{ interface: 0 }, { interface: 2 }] };

  it('stops every capture without asking, with a spinner', async () => {
    const page = await openPage([capturing]);
    const stop = Promise.withResolvers();
    axios.delete.mockReturnValue(stop.promise);
    const button = () => rowAction(page, 'web', 'stopCaptures');

    await button().trigger('click');
    expect(dialogs()).toEqual([]);
    expect(button().classes()).toContain('is-loading');
    expect(axios.delete).toHaveBeenCalledWith(capturesUrl('web'));
    stop.resolve({});
    await flushPromises();
    expect(button().classes()).not.toContain('is-loading');
  });

  it('reports a failed stop and stops the spinner', async () => {
    const page = await openPage([capturing]);
    axios.delete.mockRejectedValue(new Error('boom'));

    await rowAction(page, 'web', 'stopCaptures').trigger('click');
    await flushPromises();

    expect(notify.error).toHaveBeenCalled();
    expect(rowAction(page, 'web', 'stopCaptures').classes()).not.toContain(
      'is-loading',
    );
  });
});

describe('the CD-ROM picker', () => {
  // ISO images inside the files directory /images, one in a folder and one
  // minimega cannot take, and a same-named one outside it
  const iso = (fullPath, relativePath) =>
    disk(fullPath, relativePath, { kind: 'ISO' });
  const isos = [
    iso('/images/virtio.iso', 'virtio.iso'),
    iso('/images/tools.iso', 'tools.iso'),
    iso('/images/win/drivers.iso', 'win/drivers.iso'),
    iso('/images/my disc.iso', 'my disc.iso'),
    iso('/data/isos/tools.iso'),
  ];
  const listed = async () => ({ data: { disks: isos } });

  // the open picker, or undefined; a closed modal is hidden
  const pickerOf = (page) =>
    page
      .findAll('.modal.is-active')
      .find(
        (modal) =>
          modal.element.style.display !== 'none' &&
          modal.find('.optical-disc-card').exists(),
      );

  // the page with web's CD-ROM picker opened from its details, the ISO list
  // answered by `listIsos`
  async function openPicker(cdRom = '', listIsos = listed) {
    const page = await openPage([{ ...web, cdRom }], { disks: listIsos });
    const card = await openDetails(page, 'web');
    await card.find('.vm-action-cdrom').trigger('click');
    await flushPromises();
    return page;
  }

  // the text of the picker's state: loading, an error, or no images
  const stateOf = (page) => {
    const state = pickerOf(page).find('.optical-disc-state');
    return state.exists() ? textOf(state) : null;
  };

  it('shows a spinner while it lists the ISO images', async () => {
    const request = Promise.withResolvers();
    const page = await openPicker('', () => request.promise);

    const picker = pickerOf(page);
    expect(textOf(picker.find('.modal-card-title'))).toBe(
      'Insert a CD-ROM for web',
    );
    expect(picker.find('.optical-disc-current').exists()).toBe(false);
    expect(stateOf(page)).toBe('Loading ISO images…');
    expect(picker.find('.optical-disc-insert').attributes('disabled')).toBe('');
    expect(axios.get).toHaveBeenCalledWith('disks', {
      params: { diskType: 'ISO' },
    });

    request.resolve({ data: { disks: isos } });
    await flushPromises();
    expect(stateOf(page)).toBeNull();
    const listed = pickerOf(page)
      .findAll('option[value]')
      .map((o) => o.element.value);
    expect(listed.sort()).toEqual(isos.map((d) => d.fullPath).sort());
  });

  it('starts from the inserted ISO', async () => {
    // what the picker says is inserted, and the ISO picked, when it opens
    const opened = async (cdRom) => {
      const picker = pickerOf(await openPicker(cdRom));
      const inserted = picker.find('.optical-disc-current');
      expect(inserted.find('span').attributes('title')).toBe(cdRom);
      return [textOf(inserted), picker.find('select').element.value];
    };

    expect(await opened('/images/tools.iso')).toEqual([
      'Inserted: tools.iso',
      '/images/tools.iso',
    ]);
    expect((await opened('/images/win/drivers.iso'))[0]).toBe(
      'Inserted: win/drivers.iso',
    );
    // one the list does not hold, by its full path, as a same-named ISO in
    // another folder may be listed
    expect((await opened('/images/old/tools.iso'))[0]).toBe(
      'Inserted: /images/old/tools.iso',
    );
  });

  it('is empty, not silent, when there are no ISO images', async () => {
    const page = await openPicker('', async () => ({ data: { disks: [] } }));
    expect(stateOf(page)).toMatch(/^There are no ISO images to insert\./);
    expect(pickerOf(page).find('select').exists()).toBe(false);
  });

  it('says why when the images cannot be listed', async () => {
    const err = new Error('forbidden');
    const page = await openPicker('', () => Promise.reject(err));
    expect(textOf(pickerOf(page).find('[role="alert"]'))).toMatch(
      /Could not list the ISO images/,
    );
    expect(notify.error).toHaveBeenCalledWith(err);
  });

  it("explains a role that can't list disks without asking the server", async () => {
    rbac.allowed = denyOnly(['disks']);
    const page = await openPicker();
    expect(getUrls()).not.toContain('disks');
    expect(textOf(pickerOf(page).find('[role="alert"]'))).toMatch(
      /cannot list disk images/,
    );
  });

  it('ignores a listing that arrives after the picker closed', async () => {
    const stale = Promise.withResolvers();
    let listing = stale.promise;
    const page = await openPicker('', () => listing);
    const cancel = pickerOf(page)
      .findAll('button')
      .find((b) => textOf(b) === 'Cancel');
    await cancel.trigger('click');
    expect(pickerOf(page)).toBeUndefined();

    // reopened, it waits on its own listing, not the stale one
    listing = never();
    await page.find('.vm-card .vm-action-cdrom').trigger('click');
    stale.resolve({ data: { disks: isos } });
    await flushPromises();
    expect(stateOf(page)).toBe('Loading ISO images…');
    expect(pickerOf(page).findAll('option')).toEqual([]);
  });

  it('labels its list of ISO images, and each image by its label', async () => {
    const picker = pickerOf(await openPicker());

    const labels = picker.findAll('label[for]');
    expect(
      labels.map((label) => [
        textOf(label),
        picker.find(`#${label.attributes('for')}`).element.localName,
      ]),
    ).toEqual([['ISO image', 'select']]);
    // the options by label, sorted, those outside the files directory in a
    // group of their own, and one minimega cannot take disabled
    expect(
      picker.findAll('option').map(({ element: o }) => {
        const group = o.parentElement.localName === 'optgroup';
        return (
          `${group ? `[${o.parentElement.label}] ` : ''}${textOf(o)} = ` +
          `${o.getAttribute('value') ?? ''}${o.disabled ? ' (disabled)' : ''}`
        );
      }),
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

  it('inserts the picked ISO and ejects the inserted one', async () => {
    axios.post.mockResolvedValue({});
    axios.delete.mockResolvedValue({});

    let page = await openPicker();
    await pickerOf(page).find('select').setValue('/images/tools.iso');
    await pickerOf(page).find('.optical-disc-insert').trigger('click');
    expect(axios.post).toHaveBeenCalledWith(
      'experiments/demo/vms/web/cdrom',
      null,
      { params: { isoPath: '/images/tools.iso' } },
    );
    expect(pickerOf(page)).toBeUndefined();

    page = await openPicker('/images/tools.iso');
    await pickerOf(page).find('.optical-disc-eject').trigger('click');
    expect(axios.delete).toHaveBeenCalledWith('experiments/demo/vms/web/cdrom');
  });
});

describe("the VM details' disk and backing image chain", () => {
  const disks = [
    disk('/images/web/web_snap.qc2', 'web/web_snap.qc2', {
      backingImages: ['/images/jammy/jammy.qc2', '/data/vms/jammy.qc2'],
    }),
    disk('/images/jammy/jammy.qc2', 'jammy/jammy.qc2', {
      backingImages: ['/data/vms/jammy.qc2'],
    }),
    disk('/data/vms/jammy.qc2'),
    disk('/images/isos/tools.iso', 'isos/tools.iso', { kind: 'ISO' }),
  ];

  // the value of a row of the details' Storage section, by its key
  const storage = (card, key) => {
    const row = card
      .findAll('.vm-kv')
      .find((kv) => textOf(kv.find('.vm-kv-key')) === key);
    return row && textOf(row.find('.vm-kv-value'));
  };
  const chainOf = (card) => card.findAll('.vm-chain-name').map(textOf);

  it("shows the open VM's disk and its chain, each by its label", async () => {
    cachePage('disks', disks);
    const shown = {
      ...web,
      disk: '/images/web/web_snap.qc2',
      cdRom: '/images/isos/tools.iso',
    };
    const card = await openDetails(await openPage([shown]), 'web');
    expect(storage(card, 'Disk')).toBe('web/web_snap.qc2');
    expect(chainOf(card)).toEqual(['jammy/jammy.qc2', '/data/vms/jammy.qc2']);
    expect(storage(card, 'CD-ROM')).toBe('isos/tools.iso');
  });

  it('finds the ISO image in the CD-ROM drive of a VM without a disk', async () => {
    cachePage('disks', disks);
    const live = {
      ...web,
      name: 'live',
      disk: '',
      cdRom: '/images/isos/tools.iso',
    };
    const card = await openDetails(await openPage([live]), 'live');
    expect(storage(card, 'Disk')).toBe('None');
    expect(chainOf(card)).toEqual([]);
    expect(storage(card, 'CD-ROM')).toBe('isos/tools.iso');
  });

  it('names them by label in the details on the page, warning of one outside the files directory', async () => {
    cachePage('disks', disks);
    const shown = {
      ...web,
      disk: '/data/vms/jammy.qc2',
      cdRom: '/images/isos/tools.iso',
    };
    const card = await openDetails(await openPage([shown]), 'web');

    expect(storage(card, 'Disk')).toBe('/data/vms/jammy.qc2');
    expect(storage(card, 'CD-ROM')).toBe('isos/tools.iso');
    expect(
      card
        .findAllComponents({ name: 'DiskWarning' })
        .flatMap(tooltipButtons)
        .filter((b) => b.label === 'Outside the standard images directory')
        .map((b) => b.tooltip),
    ).toEqual([
      'Outside the standard images directory (/images). minimega does not ' +
        'copy this image to other cluster nodes. On a multi-node cluster, ' +
        'every node that may run a VM with this image needs the same file ' +
        'at this path.',
    ]);
  });

  it("leaves it out for roles that can't list disks", async () => {
    rbac.allowed = denyOnly(['disks']);
    const shown = { ...web, disk: '/images/web_snap.qc2' };
    const card = await openDetails(await openPage([shown]), 'web');
    expect(getUrls()).not.toContain('disks');
    expect(chainOf(card)).toEqual([]);
    expect(storage(card, 'Disk')).toBe('web_snap.qc2');
  });

  it('drops a chain that arrives after another VM was opened', async () => {
    const listing = Promise.withResolvers();
    const db = { ...web, name: 'db', disk: '', cdRom: '' };
    const page = await openPage(
      [{ ...web, disk: '/images/web/web_snap.qc2' }, db],
      { disks: () => listing.promise },
    );
    await openDetails(page, 'web');
    expect(getUrls()).toContain('disks');
    const card = await openDetails(page, 'db');

    listing.resolve({ data: { disks } });
    await flushPromises();
    expect(textOf(card.find('.vm-card-title'))).toBe('db');
    expect(chainOf(card)).toEqual([]);
  });
});
