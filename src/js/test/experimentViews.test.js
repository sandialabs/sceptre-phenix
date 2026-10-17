// The stopped experiment page, the VM tiles, and the VM labels and mount
// browser modals.
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { axios, capturing, loaderSpy, modules, rbac, resetMocks, ws } =
  await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
// the pages' lists come from the tests
vi.mock('@/utils/pageLoader.js', capturing);

import DiskSelect from '@/components/experiment/DiskSelect.vue';
import StoppedExperiment from '@/views/experiment/StoppedExperiment.vue';
import VMtilesView from '@/views/experiment/VMtilesView.vue';
import VMLabelsModal from '@/components/VMLabelsModal.vue';
import VMMountBrowserModal from '@/components/VMMountBrowserModal.vue';
import { never } from './helpers/async.js';
import { disk, wsMsg } from './helpers/fixtures.js';
import {
  byLabel,
  confirmDialog,
  dialogs,
  mountComponent,
  mountPage,
  tableRows,
  textOf,
  toasts,
  tooltipButtons,
} from './helpers/mount.js';
import { allowAll, denyAll, denyOnly } from './helpers/permissions.js';

beforeEach(() => {
  resetMocks({ store: { token: 't', features: [], role: null } });
  // the pages' own lists never arrive
  axios.get.mockImplementation(never);
});

const button = (wrapper, text) =>
  wrapper.findAll('button').find((b) => textOf(b) === text);

describe('StoppedExperiment', () => {
  // the page on experiment exp1, with these VMs once it has loaded
  const stopped = (vms, data) =>
    mountPage(StoppedExperiment, {
      route: '/experiment/exp1',
      data: { ...(vms && { experiment: { name: 'exp1', vms } }), ...data },
    });

  const publish = (msg) => {
    for (const handler of ws.handlers) handler(msg);
    return flushPromises();
  };

  it.each([
    [
      'VM updates',
      (exp) =>
        wsMsg('experiment/vm', `${exp}/vm1`, 'update', {
          name: 'vm1',
          host: 'b',
        }),
    ],
    [
      'schedules',
      (exp) =>
        wsMsg('experiment', exp, 'schedule', {
          schedule: [{ vm: 'vm1', host: 'b' }],
        }),
    ],
  ])('applies %s only for its own experiment', async (_, message) => {
    const page = await stopped([{ name: 'vm1', host: 'a' }], {
      hosts: ['a', 'b'],
    });
    const host = () => byLabel(page, 'Host for VM vm1').element.value;

    await publish(message('exp2'));
    expect(host()).toBe('a');

    await publish(message('exp1'));
    expect(host()).toBe('b');
  });

  it('ignores publishes before the experiment has loaded', async () => {
    await stopped();
    await publish(
      wsMsg('experiment/vm', 'exp1/vm1', 'update', { name: 'vm1' }),
    );
    expect(toasts()).toEqual([]);
  });

  it.each([
    ['shows', allowAll, ['VMs', 'Files']],
    ['hides', denyAll, ['VMs']],
  ])('%s the Files tab by file listing permission', async (_, role, want) => {
    rbac.allowed = vi.fn(role);
    const page = await stopped();
    const tabs = page
      .findAll('.tabs li')
      .filter((li) => li.element.style.display !== 'none');
    expect(tabs.map(textOf)).toEqual(want);
    expect(rbac.allowed).toHaveBeenCalledWith(
      'experiments/files',
      'list',
      'exp1',
    );
  });

  it('sets boot for the selected VMs, shows the answer and clears the selection', async () => {
    axios.patch.mockResolvedValue({
      data: { vms: [{ name: 'vm2', dnb: true }] },
    });
    const page = await stopped([
      { name: 'vm1', dnb: false },
      { name: 'vm2', dnb: false },
    ]);
    const boxes = () => page.findAll('td input[type="checkbox"]');
    await boxes().at(1).setValue(true);

    await byLabel(page, 'Set selected VMs to not boot').trigger('click');
    expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms', {
      vms: [{ name: 'vm2', dnb: true }],
      total: 1,
    });
    // rows picked one by one are cleared too, not only a whole-page pick
    expect(boxes().map((box) => box.element.checked)).toEqual([false, false]);
    expect(byLabel(page, 'Set selected VMs to not boot').exists()).toBe(false);

    await flushPromises();
    expect(
      tableRows(page).map((cells) =>
        cells.Boot.find('[data-icon="bolt"]').classes(),
      ),
    ).toEqual([['boot'], ['dnb']]);
    expect(toasts().map((t) => t.text)).toEqual([
      'The selected VM was set to not boot',
    ]);
  });

  describe('VM disks', () => {
    const disks = [
      disk('/phenix/images/win/web.qc2', 'win/web.qc2'),
      disk('/phenix/images/web.qc2', 'web.qc2'),
      disk('/data/vms/out.img'),
    ];
    const vms = [
      { name: 'web', disk: '/phenix/images/win/web.qc2' },
      { name: 'out', disk: '/data/vms/out.img' },
      { name: 'gone', disk: '/phenix/images/gone.qc2' },
    ];

    it("lists the disks this experiment's VMs can use, keeping each one whole", async () => {
      axios.get.mockImplementation(async (url) =>
        url === 'disks' ? { data: { disks } } : never(),
      );
      const page = await stopped(vms);

      expect(axios.get).toHaveBeenCalledWith('disks', {
        params: { expName: 'exp1' },
      });
      expect(page.findComponent(DiskSelect).props('disks')).toEqual(disks);
    });

    it("assigns the picked disk's full path, naming it safely in the confirmation", async () => {
      axios.patch.mockImplementation(never);
      const odd = '/data/<img src=x onerror=alert(1)>.img';
      const page = await stopped(
        [
          { name: 'web', disk: '' },
          { name: '<b>vm</b>', disk: '' },
        ],
        { disks: [...disks, disk(odd)] },
      );
      const message = () =>
        dialogs().at(-1).querySelector('.media-content p > div').innerHTML;

      await byLabel(page, 'Disk for VM web').setValue(
        '/phenix/images/win/web.qc2',
      );
      expect(message()).toBe(
        'This will assign the win/web.qc2 disk image to the web VM.',
      );
      await confirmDialog();
      expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/web', {
        disk: '/phenix/images/win/web.qc2',
      });

      // names from files and topologies are text, never markup
      await byLabel(page, 'Disk for VM <b>vm</b>').setValue(odd);
      expect(message()).toBe(
        'This will assign the /data/&lt;img src=x onerror=alert(1)&gt;.img ' +
          'disk image to the &lt;b&gt;vm&lt;/b&gt; VM.',
      );
    });

    // The Disk column of the page showing `vms`, given the disk list (unloaded
    // when undefined): each VM's picker options or text and its warnings.
    async function diskColumn(data) {
      const page = await stopped(vms, data);
      return Object.fromEntries(
        // the rows are in the VMs' order
        tableRows(page).map((cells, row) => {
          const select = cells.Disk.find('select');
          return [
            vms[row].name,
            {
              shown: select.exists()
                ? select
                    .findAll('option')
                    .map((o) => textOf(o) + (o.element.disabled ? ' (x)' : ''))
                : textOf(cells.Disk.find('span[title]')),
              warnings: tooltipButtons(cells.Disk).map((b) => b.label),
            },
          ];
        }),
      );
    }

    it('offers each VM the listed disks, warning of one outside the files directory or not listed', async () => {
      const offered = ['web.qc2', 'win/web.qc2', '/data/vms/out.img'];
      expect(await diskColumn({ disks })).toEqual({
        web: { shown: offered, warnings: [] },
        out: {
          shown: offered,
          warnings: ['Outside the standard images directory'],
        },
        gone: {
          shown: ['/phenix/images/gone.qc2 (not listed) (x)', ...offered],
          warnings: ['Not in your disk list'],
        },
      });
    });

    it('shows each VM its disk alone, with no warning, to a role that may not list disks', async () => {
      rbac.allowed = denyOnly(['disks']);
      expect(await diskColumn({})).toEqual({
        web: { shown: ['/phenix/images/win/web.qc2'], warnings: [] },
        out: { shown: ['/data/vms/out.img'], warnings: [] },
        gone: { shown: ['/phenix/images/gone.qc2'], warnings: [] },
      });
    });

    it("names each VM's disk to a role that may not change it", async () => {
      rbac.allowed = denyOnly(['vms']);
      expect(await diskColumn({ disks })).toEqual({
        web: { shown: 'win/web.qc2', warnings: [] },
        out: {
          shown: '/data/vms/out.img',
          warnings: ['Outside the standard images directory'],
        },
        gone: {
          shown: '/phenix/images/gone.qc2',
          warnings: ['Not in your disk list'],
        },
      });
    });
  });

  it('keeps the latest searches, each once, up to its cap', async () => {
    const page = await stopped(undefined, {
      searchName: ' vm-new ',
      searchHistory: Array.from({ length: 10 }, (_, i) => `vm-${i}`),
    });
    const history = () =>
      page.findComponent({ name: 'BAutocomplete' }).props('data');
    // a search the server found VMs for
    const found = async () => {
      loaderSpy.options.apply({
        name: 'exp1',
        vms: [{ name: 'vm-new' }],
        vm_count: 1,
        vlans: [],
      });
      await flushPromises();
    };

    await found();
    expect(history()).toHaveLength(10);
    expect(history()).toContain('vm-new');

    await found();
    expect(history().filter((s) => s == 'vm-new')).toHaveLength(1);
  });
});

describe('VMtilesView', () => {
  const screenshot = 'data:image/png;base64,AA==';
  const vms = [
    { experiment: 'exp1', name: 'vm[1]', running: true, screenshot },
    { experiment: 'exp1', name: 'vm2', running: false },
    { experiment: 'exp2', name: 'vm[3]', running: false },
  ];

  // the page's heading and its tiles: the name on each, what its picture
  // says, and whether the picture opens the VM's console
  async function tiles(route, searchName = '') {
    const page = await mountComponent(VMtilesView, {
      route,
      data: { vms, searchName },
    });
    return {
      heading: textOf(page.find('h3')),
      tiles: page.findAll('.tile.is-child').map((tile) => ({
        name: textOf(tile.find('p.title')),
        alt: tile.find('img').attributes('alt'),
        console: tile.find('a[href*="/vnc?token=t"]').exists(),
      })),
    };
  }

  it("shows the routed experiment's VMs, found by a literal search", async () => {
    // sorted by name
    expect(await tiles('/experiment/exp1')).toEqual({
      heading: 'Experiment: exp1',
      tiles: [
        { name: 'vm2', alt: 'VM vm2 is not running', console: false },
        { name: 'vm[1]', alt: 'Open the console of VM vm[1]', console: true },
      ],
    });
    expect(
      (await tiles('/experiment/exp1', '[')).tiles.map((tile) => tile.name),
    ).toEqual(['vm[1]']);
  });

  it("shows every experiment's VMs under their experiment's name", async () => {
    expect(await tiles('/vmtiles', '[')).toEqual({
      heading: 'All Experiments',
      tiles: [
        {
          name: 'exp1/vm[1]',
          alt: 'Open the console of VM exp1/vm[1]',
          console: true,
        },
        {
          name: 'exp2/vm[3]',
          alt: 'VM exp2/vm[3] is not running',
          console: false,
        },
      ],
    });
  });
});

describe('VMLabelsModal', () => {
  // a label and a note, so the modal adds no blank rows, which it would save
  const NOTE = { __notes_1: 'n' };

  // it resizes its notes before b-table draws them, which throws
  beforeEach(() => {
    vi.spyOn(VMLabelsModal.methods, 'resizeNoteTextArea').mockReturnValue();
  });
  afterEach(() => vi.restoreAllMocks());

  const modal = (tags) =>
    mountComponent(VMLabelsModal, {
      props: { experiment: 'exp1', vmName: 'vm1', tags: { ...NOTE, ...tags } },
    });

  it('saves to the experiment it was given and closes on 200', async () => {
    axios.patch.mockResolvedValue({ status: 200, statusText: '' });
    const labels = await modal({ old: 'x' });
    const [key, value] = labels.findAll('table').at(1).findAll('td input');
    await key.setValue('k');
    await value.setValue('v');

    await button(labels, 'Save').trigger('click');
    await flushPromises();

    expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/vm1', {
      tag_update_mode: 'SET',
      tags: { k: 'v', ...NOTE },
    });
    expect(labels.emitted('saved')).toEqual([[]]);
    expect(labels.emitted('close')).toEqual([[]]);
  });

  it('closes without saving when nothing changed', async () => {
    const labels = await modal({ k: 'v' });

    await button(labels, 'Save').trigger('click');
    expect(axios.patch).not.toHaveBeenCalled();
    expect(labels.emitted('close')).toEqual([[]]);
  });
});

describe('VMMountBrowserModal', () => {
  beforeEach(() => {
    axios.post.mockImplementation(never);
    axios.delete.mockResolvedValue({});
  });

  const browser = (currentPath) =>
    mountComponent(VMMountBrowserModal, {
      props: { targetExp: 'exp1', targetVm: 'vm1' },
      data: { currentPath },
    });

  const crumbs = (wrapper) => wrapper.findAll('.breadcrumb li');

  it('links each crumb to its own position in the path', async () => {
    const wrapper = await browser('/a/a/b');
    expect(crumbs(wrapper).map(textOf)).toEqual(['mnt', 'a', 'a', 'b']);
    expect(crumbs(wrapper).at(-1).classes()).toContain('is-active');

    // the path each crumb opens
    for (const [i, path] of [
      [2, '/a/a'],
      [1, '/a'],
      [0, '/'],
    ]) {
      await crumbs(wrapper).at(i).find('a').trigger('click');
      expect(axios.get.mock.lastCall).toEqual([
        'experiments/exp1/vms/vm1/files',
        { params: { path } },
      ]);
      expect(crumbs(wrapper)).toHaveLength(i + 1);
    }
  });

  it('shows only the mount at its root', async () => {
    expect(crumbs(await browser('/')).map(textOf)).toEqual(['mnt']);
  });
});
