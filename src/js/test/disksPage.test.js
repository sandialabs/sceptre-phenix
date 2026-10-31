// The Disks page: loading and refreshing the list, disk names, the actions it
// offers and why each is disabled, and the requests those actions send.
import fc from 'fast-check';
import { flushPromises } from '@vue/test-utils';
import realAxios from 'axios';
import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  fetching,
  loaderSpy,
  modules,
  notices,
  noticeText,
  notify,
  rbac,
  resetMocks,
  ws,
} = await vi.hoisted(() => import('./helpers/mocks.js'));
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/errorNotif.js', modules.errorNotif);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/utils/notify.js', modules.notify);
// a loader whose load() fetches as the real one does, without the cache
vi.mock('@/utils/pageLoader.js', fetching);

import JobProgress from '@/components/jobs/JobProgress.vue';
import { diskType as diskTypeOf } from '@/utils/diskFormat.js';
import { diskTotals } from '@/utils/diskTotals.js';
import { useJobStore } from '@/utils/jobs.js';
import Disks from '@/views/Disks.vue';
import { diskListArb } from './helpers/arbitraries.js';
import { disk, httpError, wsMsg } from './helpers/fixtures.js';
import {
  buttons,
  byLabel,
  confirmDialog,
  dialogs,
  mountPage,
  tableHeader,
  tableRows,
  textOf,
  tooltipButtons,
} from './helpers/mount.js';
import {
  allowAll,
  allowOnly,
  denyAll,
  denyOnly,
} from './helpers/permissions.js';
import { componentDeclarations } from './helpers/css.js';

beforeEach(() => {
  setActivePinia(createPinia());
  resetMocks({ store: { token: 't', features: [], role: null } });
  axios.get.mockResolvedValue({ data: { disks: [] } });
});

const OUTSIDE_TEXT =
  'Outside the standard images directory (/phenix/images). minimega does ' +
  'not copy this image to other cluster nodes. On a multi-node cluster, ' +
  'every node that may run a VM with this image needs the same file at ' +
  'this path.';

// the page listing these disks, which its loader does not replace
const page = (disks = []) =>
  mountPage(Disks, { route: '/disks/', data: { disks, loaded: true } });

const nameOf = (cells) => textOf(cells.Name.find('.disk-label'));

// the label each row's Name cell shows, in order
const names = (wrapper) => tableRows(wrapper).map(nameOf);

// opens the details window of the disk with this label, and gives it
async function openDetails(wrapper, label) {
  const row = tableRows(wrapper).find((cells) => nameOf(cells) === label);
  row.Name.element.closest('tr').click();
  await flushPromises();
  return wrapper.find('.modal.is-active');
}

// a row of the details list, by its term
const detail = (details, term) =>
  details
    .findAll('dl > div')
    .find((row) => textOf(row.find('dt')) === term)
    .find('dd');

// clicks the details window's button for an action, by its label
async function runAction(details, label) {
  const button = details
    .findAll('.actions button')
    .find((b) => textOf(b.find('b')) === label);
  await button.trigger('click');
  await flushPromises();
}

describe('the disk list', () => {
  it("has the server inspect every image again on the header's refresh", async () => {
    await page();

    await loaderSpy.options.refresh();
    await loaderSpy.load();
    expect(
      axios.get.mock.calls.map(([url, config]) => [url, config?.params]),
    ).toEqual([
      ['disks', { refresh: true }],
      ['disks', undefined],
    ]);
  });

  it('reloads when the server reports its images changed', async () => {
    await page();

    for (const type of ['disks', 'experiment']) {
      for (const handler of ws.handlers) {
        handler({ resource: { type, action: 'update' } });
      }
    }
    expect(loaderSpy.load).toHaveBeenCalledTimes(1);
    expect(loaderSpy.load).toHaveBeenCalledWith({ force: true });
  });

  // picks a file of this name in the upload window
  async function upload(name) {
    const wrapper = await page();
    await byLabel(wrapper, 'Upload a disk').trigger('click');
    const input = wrapper.find('.modal.is-active input[type="file"]');
    Object.defineProperty(input.element, 'files', {
      value: [new File(['x'], name)],
    });
    await input.trigger('change');
    await flushPromises();
  }

  it.each([
    ['text', 'no space left on device'],
    [
      'a JSON Error',
      { message: 'no space left on device', cause: '', code: 'internal' },
    ],
  ])('shows why an upload failed, told in %s', async (_, body) => {
    axios.post.mockRejectedValue(httpError(507, body));
    await upload('a.qc2');
    expect(notify.error).toHaveBeenCalledWith(
      'Error uploading: no space left on device',
    );
  });

  it('refuses to upload a file that is no disk image', async () => {
    // the file picker takes any .tgz, but only a rootfs archive is a disk
    await upload('notes.tgz');

    expect(notify.show).toHaveBeenCalledWith(
      'Cannot upload notes.tgz',
      'Valid disk file types are .qcow2, .qc2, *_rootfs.tgz, .hdd, and .iso.',
    );
    expect(axios.post).not.toHaveBeenCalled();
  });

  it('sorts sizes by their bytes, unknown sizes first', async () => {
    const sizes = ['2.1 GiB', '512 B', '', '3G', '900 MiB'];
    const wrapper = await page(
      sizes.map((size, i) =>
        disk(`/phenix/images/${i}.qc2`, `${i}.qc2`, { size }),
      ),
    );

    await tableHeader(wrapper, 'Size on Disk').trigger('click');
    expect(
      tableRows(wrapper).map((cells) => textOf(cells['Size on Disk'])),
    ).toEqual(['', '512 B', '900 MiB', '2.1 GiB', '3G']);
  });

  it('sorts any sizes by their bytes, in IEC or minimega units', async () => {
    const units = [
      ['B'],
      ['KiB', 'K'],
      ['MiB', 'M'],
      ['GiB', 'G'],
      ['TiB', 'T'],
    ];
    // a size as the list shows it, with its bytes; unknown sizes count -1
    const size = fc.oneof(
      fc.constant({ text: '', bytes: -1 }),
      fc
        .record({
          tenths: fc.nat(20000),
          power: fc.nat(units.length - 1),
          minimega: fc.boolean(),
        })
        .map(({ tenths, power, minimega }) => ({
          text: `${(tenths / 10).toFixed(1)}${minimega && power ? '' : ' '}${units[power].at(minimega ? -1 : 0)}`,
          bytes: (tenths / 10) * 1024 ** power,
        })),
    );
    await fc.assert(
      fc.asyncProperty(
        fc.array(size, { minLength: 1, maxLength: 6 }),
        async (sizes) => {
          const wrapper = await page(
            sizes.map(({ text }, i) =>
              disk(`/phenix/images/${i}.qc2`, `${i}.qc2`, { size: text }),
            ),
          );
          await tableHeader(wrapper, 'Size on Disk').trigger('click');

          const order = names(wrapper).map((name) => parseInt(name));
          const bytes = order.map((i) => sizes[i].bytes);
          expect(bytes).toEqual(bytes.toSorted((a, b) => a - b));
          expect(order.toSorted((a, b) => a - b)).toEqual(
            sizes.map((_, i) => i),
          );
          wrapper.unmount();
        },
      ),
      { numRuns: 10 },
    );
  });
});

const vmDisk = disk('/phenix/images/snap.qc2', 'snap.qc2', {
  backingImages: ['/phenix/images/base.qc2'],
});
const ACTIONS = [
  'Snapshot',
  'Commit',
  'Rebase',
  'Clone',
  'Resize',
  'Download',
  'Rename',
  'Delete',
];

describe('disk actions', () => {
  it('lists every action in the details window, and some on each row', async () => {
    const wrapper = await page([vmDisk]);
    const [row] = tableRows(wrapper);
    expect(
      row.Actions.findAll('button').map((b) => b.attributes('aria-label')),
    ).toEqual(['Snapshot', 'Clone', 'Download', 'Rename', 'Delete']);

    const details = await openDetails(wrapper, 'snap.qc2');
    expect(details.findAll('.actions button b').map(textOf)).toEqual(ACTIONS);
  });

  it('says how the disk rules an action out: its kind, backing image or use', async () => {
    // each action's tooltip in the details window of this disk
    const tooltips = async (shown) => {
      const details = await openDetails(await page([shown]), 'snap.qc2');
      const found = tooltipButtons(details.find('.actions'));
      return Object.fromEntries(found.map((t, i) => [ACTIONS[i], t.tooltip]));
    };

    expect((await tooltips(vmDisk)).Commit).toBe('Commit');
    for (const backingImages of [[], undefined]) {
      expect((await tooltips({ ...vmDisk, backingImages })).Commit).toBe(
        'Cannot commit: the disk has no backing image to commit into',
      );
    }
    const kernel = await tooltips({
      ...vmDisk,
      name: 'vmlinuz',
      kind: 'Kernel',
    });
    expect(kernel.Snapshot).toBe(
      'Cannot snapshot: only VM disks can be snapshotted',
    );
    expect(kernel.Commit).toBe('Cannot commit: only VM disks can be committed');
    expect(kernel.Rebase).toBe('Cannot rebase: only VM disks can be rebased');
    // the rest work on any disk
    for (const label of ['Clone', 'Resize', 'Download', 'Rename', 'Delete']) {
      expect(kernel[label]).toBe(label);
    }

    const used = await tooltips({
      ...vmDisk,
      inUse: true,
      experiments: [
        { name: 'a', running: true },
        { name: 'b', running: false },
      ],
    });
    for (const label of ['Commit', 'Rebase', 'Resize', 'Rename', 'Delete']) {
      expect(used[label]).toMatch(/: the disk is in use by a$/);
    }
    // cloning and downloading only read the disk
    expect(used.Clone).toBe('Clone');
    expect(used.Download).toBe('Download');
  });

  it('asks roleAllowed about a disk by name, and about creating one with no name', async () => {
    const wrapper = await page([vmDisk]);
    const calls = [];
    rbac.allowed = (...args) => calls.push(args) > 0;

    wrapper.vm.disabledReason('snapshot', vmDisk);
    wrapper.vm.disabledReason('commit', vmDisk);
    expect(calls).toStrictEqual([
      ['disks', 'create'],
      ['disks', 'get', 'snap.qc2'],
      ['disks', 'update', 'snap.qc2'],
      ['disks', 'update', 'base.qc2'],
    ]);
  });

  // each row's actions: Snapshot, Clone, Download, Rename and Delete, each
  // enabled or saying why not
  const labels = ['Snapshot', 'Clone', 'Download', 'Rename', 'Delete'];
  const OK = 'enabled';
  const NO = 'you do not have permission';
  const USED = 'the disk is in use by exp1';
  const VM_ONLY = 'only VM disks can be snapshotted';
  const OUT = 'the disk is outside the standard images directory';
  const KEPT = 'the disk is at a path the disk list leaves out';
  const MM = "minimega cannot use the disk's path";
  // phēnix acts on neither, whatever the role
  const readOnly = {
    '/data/vms/out.img': Array(5).fill(OUT),
    'exp1/files/vm.hdd': Array(5).fill(KEPT),
  };

  it.each([
    [
      'a role that may do anything',
      allowAll,
      {
        'snap.qc2': [OK, OK, OK, OK, OK],
        'isos/tools.iso': [VM_ONLY, OK, OK, OK, OK],
        'live.qc2': [USED, OK, OK, USED, USED],
        'my disk.qc2': [MM, OK, OK, OK, OK],
        ...readOnly,
      },
    ],
    [
      'a role that may only create disks, so not read one to copy',
      allowOnly(['*', 'create']),
      {
        'snap.qc2': [NO, NO, NO, NO, NO],
        'isos/tools.iso': [VM_ONLY, NO, NO, NO, NO],
        'live.qc2': [USED, NO, NO, USED, USED],
        'my disk.qc2': [MM, NO, NO, NO, NO],
        ...readOnly,
      },
    ],
    [
      'a role that may not get disks, so not download or copy them',
      denyOnly(['*', 'get']),
      {
        'snap.qc2': [NO, NO, NO, OK, OK],
        'isos/tools.iso': [VM_ONLY, NO, NO, OK, OK],
        'live.qc2': [USED, NO, NO, USED, USED],
        'my disk.qc2': [MM, NO, NO, OK, OK],
        ...readOnly,
      },
    ],
    [
      'a role that may do nothing',
      denyAll,
      {
        'snap.qc2': [NO, NO, NO, NO, NO],
        'isos/tools.iso': [VM_ONLY, NO, NO, NO, NO],
        'live.qc2': [USED, NO, NO, USED, USED],
        'my disk.qc2': [MM, NO, NO, NO, NO],
        ...readOnly,
      },
    ],
  ])(
    "shows each row's actions for %s, disabled ones saying why",
    async (_, can, want) => {
      rbac.allowed = can;
      const wrapper = await page([
        vmDisk,
        disk('/phenix/images/isos/tools.iso', 'isos/tools.iso', {
          kind: 'ISO',
        }),
        disk('/phenix/images/live.qc2', 'live.qc2', {
          inUse: true,
          experiments: [{ name: 'exp1', running: true }],
        }),
        // minimega cannot take a path with white space
        disk('/phenix/images/my disk.qc2', 'my disk.qc2'),
        disk('/data/vms/out.img'),
        disk('/phenix/images/exp1/files/vm.hdd', 'exp1/files/vm.hdd', {
          readOnly: true,
        }),
      ]);

      const rows = Object.fromEntries(
        tableRows(wrapper).map((cells) => {
          const buttons = tooltipButtons(cells.Actions);
          expect(buttons.map((b) => b.label)).toEqual(labels);
          return [
            nameOf(cells),
            buttons.map((b) =>
              b.disabled ? b.tooltip.replace(/^Cannot [a-z]+: /, '') : OK,
            ),
          ];
        }),
      );
      expect(rows).toEqual(want);
    },
  );
});

describe("a row's action tooltips", () => {
  // the Actions column is the table's last, so a tooltip centered on its
  // right-hand buttons would run off the window
  it('open to the left of their buttons', async () => {
    const wrapper = await page([disk('/phenix/images/a.qc2', 'a.qc2')]);
    const tooltips = tableRows(wrapper)[0].Actions.findAllComponents({
      name: 'BTooltip',
    });
    expect(
      tooltips.map((t) => t.find('button').attributes('aria-label')),
    ).toEqual(['Snapshot', 'Clone', 'Download', 'Rename', 'Delete']);
    for (const tooltip of tooltips) {
      await tooltip.find('.tooltip-trigger').trigger('mouseenter');
      expect(tooltip.classes()).toContain('is-left');
    }
  });
});

describe('disks in folders and outside the files directory', () => {
  const snap = disk('/phenix/images/win/snap.qc2', 'win/snap.qc2', {
    backingImages: [
      '/phenix/images/win/base.qc2',
      '/data/vms/base.qc2',
      '/data/vms/gone.qc2',
    ],
  });
  const base = disk('/phenix/images/win/base.qc2', 'win/base.qc2', {
    backingImages: ['/data/vms/base.qc2', '/data/vms/gone.qc2'],
  });
  const outside = disk('/data/vms/base.qc2', '', {
    backingImages: ['/data/vms/gone.qc2'],
  });
  const top = disk('/phenix/images/base.qc2', 'base.qc2');
  const kept = disk('/phenix/images/exp1/files/vm.hdd', 'exp1/files/vm.hdd', {
    readOnly: true,
  });
  const spaced = disk('/phenix/images/my disk.qc2', 'my disk.qc2');
  const disks = [snap, base, outside, top, kept, spaced];
  // their labels, as the list sorts them at first
  const sorted = [
    '/data/vms/base.qc2',
    'base.qc2',
    'exp1/files/vm.hdd',
    'my disk.qc2',
    'win/base.qc2',
    'win/snap.qc2',
  ];

  it('names each disk by its path within the files directory, warning of those outside it', async () => {
    const wrapper = await page(disks);

    const warnings = tableRows(wrapper).map((cells) =>
      tooltipButtons(cells.Name).map(({ label, tooltip }) => ({
        label,
        tooltip,
      })),
    );
    expect(names(wrapper)).toEqual(sorted);
    expect(warnings).toEqual([
      [
        {
          label: 'Outside the standard images directory',
          tooltip: OUTSIDE_TEXT,
        },
      ],
      ...Array(5).fill([]),
    ]);
  });

  it('finds and sorts disks by the names it shows', async () => {
    const wrapper = await page(disks);
    // the field draws its input afresh as its clear button comes and goes
    const find = (text) =>
      wrapper.find('input[placeholder="Find a Disk"]').setValue(text);

    await find('WIN/');
    expect(names(wrapper)).toEqual(['win/base.qc2', 'win/snap.qc2']);
    await find('/data');
    expect(names(wrapper)).toEqual(['/data/vms/base.qc2']);

    await find('');
    expect(names(wrapper)).toEqual(sorted);
    await tableHeader(wrapper, 'Name').trigger('click');
    expect(names(wrapper)).toEqual(sorted.toReversed());
  });

  it('finds any disk by any part of its name, in any case, and only disks holding it', async () => {
    const label = (d) => d.relativePath || d.fullPath;
    // a disk list, and a part of one disk's label, upper-cased or not
    const cases = diskListArb()
      .filter((list) => list.length > 0)
      .chain((list) =>
        fc.tuple(
          fc.constant(list),
          fc
            .record({
              found: fc.constantFrom(...list),
              ends: fc.tuple(fc.nat(), fc.nat()),
              upper: fc.boolean(),
            })
            .map(({ found, ends, upper }) => {
              const [a, b] = ends.map((n) => n % (label(found).length + 1));
              const part = label(found).slice(Math.min(a, b), Math.max(a, b));
              return { found, query: upper ? part.toUpperCase() : part };
            }),
        ),
      );
    await fc.assert(
      fc.asyncProperty(cases, async ([list, { found, query }]) => {
        const wrapper = await page(list);
        await wrapper.find('input[placeholder="Find a Disk"]').setValue(query);

        const shown = tableRows(wrapper).map(
          (cells) => cells.Name.find('.disk-label').element.textContent,
        );
        expect(shown).toContain(label(found));
        expect(shown.toSorted()).toEqual(
          list
            .map(label)
            .filter((l) => l.toLowerCase().includes(query.toLowerCase()))
            .toSorted(),
        );
        wrapper.unmount();
      }),
      { numRuns: 10 },
    );
  });

  it("shows where a disk is and links its backing chain by each image's full path", async () => {
    const details = await openDetails(await page(disks), 'win/snap.qc2');

    expect(textOf(details.find('.modal-card-title'))).toBe('win/snap.qc2');
    expect(textOf(detail(details, 'Location:'))).toBe(
      'The standard images directory (/phenix/images)',
    );
    // the chain names the listed images by label, with the same-named image
    // outside the files directory its own entry, and one not listed as is
    const chain = details.find('[style*="text-align: center"]');
    expect(
      chain
        .findAll('div')
        .map(({ element }) => element.lastElementChild)
        .map((el) => [el.localName, textOf(el)]),
    ).toEqual([
      ['button', 'win/base.qc2'],
      ['button', '/data/vms/base.qc2'],
      ['span', '/data/vms/gone.qc2'],
    ]);
  });

  it.each([
    [
      'outside the files directory',
      '/data/vms/base.qc2',
      'Outside the standard images directory',
      [OUTSIDE_TEXT],
    ],
    [
      'in an experiment files folder',
      'exp1/files/vm.hdd',
      'The standard images directory (/phenix/images), at a path the disk ' +
        'list leaves out',
      [],
    ],
  ])(
    'says where a read-only disk %s is',
    async (_, label, location, warnings) => {
      const details = await openDetails(await page(disks), label);
      const row = detail(details, 'Location:');
      expect(textOf(row).startsWith(location)).toBe(true);
      expect(tooltipButtons(row).map((b) => b.tooltip)).toEqual(warnings);
    },
  );

  it("opens a backing image's details by its full path, not a same-named image", async () => {
    const wrapper = await page(disks);
    const follow = async (label) => {
      const details = await openDetails(wrapper, 'win/snap.qc2');
      await details
        .findAll('.backing-link')
        .find((b) => textOf(b) === label)
        .trigger('click');
      return textOf(detail(details, 'Full Path:'));
    };

    expect(await follow('/data/vms/base.qc2')).toBe(outside.fullPath);
    expect(await follow('win/base.qc2')).toBe(base.fullPath);
  });

  it('rebases only onto the other disks phēnix acts on and minimega takes, by label', async () => {
    const wrapper = await page(disks);
    await runAction(await openDetails(wrapper, 'win/base.qc2'), 'Rebase');

    const select = wrapper.findAll('.modal.is-active').at(-1).find('select');
    expect(select.attributes('aria-label')).toBe('New backing image');
    expect(
      select.findAll('option').map((o) => [textOf(o), o.element.value]),
    ).toEqual([
      ['None', ''],
      ['base.qc2', '/phenix/images/base.qc2'],
      ['win/snap.qc2', '/phenix/images/win/snap.qc2'],
    ]);
  });
});

describe('disks with a broken backing chain', () => {
  const MISSING = '/phenix/images/ro_kali.qc2';
  const PROBLEM = `backing file ${MISSING} is missing`;
  const broken = disk('/phenix/images/kali.qc2', 'kali.qc2', {
    backingImages: ['/phenix/images/mid.qc2'],
    brokenChain: true,
    chainProblem: PROBLEM,
    missingBacking: MISSING,
  });
  const mid = disk('/phenix/images/mid.qc2', 'mid.qc2', {
    brokenChain: true,
    chainProblem: PROBLEM,
    missingBacking: MISSING,
  });
  const loop = disk('/data/vms/loop.qc2', '', {
    brokenChain: true,
    chainProblem: 'backing chain loops back to /data/vms/loop.qc2',
  });
  const whole = disk('/phenix/images/whole.qc2', 'whole.qc2');
  const disks = [broken, mid, loop, whole];

  it('tags each broken disk, naming the problem in its tooltip and accessible name', async () => {
    const wrapper = await page(disks);

    const tags = tableRows(wrapper).map((cells) =>
      cells.Name.findAll('.broken-tag').map((tag) => textOf(tag)),
    );
    expect(names(wrapper)).toEqual([
      '/data/vms/loop.qc2',
      'kali.qc2',
      'mid.qc2',
      'whole.qc2',
    ]);
    expect(tags).toEqual([
      ['Broken chain: backing chain loops back to /data/vms/loop.qc2'],
      [`Broken chain: ${PROBLEM}`],
      [`Broken chain: ${PROBLEM}`],
      [],
    ]);

    // the tag says it in text, not by its color alone, and keeps its tooltip
    const [loopRow] = tableRows(wrapper);
    const tooltips = tooltipButtons(loopRow.Name).map((b) => b.tooltip);
    expect(tooltips[0]).toBe('backing chain loops back to /data/vms/loop.qc2');
    expect(textOf(loopRow.Name.find('.broken-tag .is-sr-only'))).toBe(
      ': backing chain loops back to /data/vms/loop.qc2',
    );
  });

  it('opens the details of a broken disk from its tag', async () => {
    const wrapper = await page(disks);
    const row = tableRows(wrapper).find(
      (cells) => nameOf(cells) === 'kali.qc2',
    );
    expect(row.Name.find('.broken-tag').element.localName).toBe('button');
    await row.Name.find('.broken-tag').trigger('click');
    await flushPromises();

    const details = wrapper.find('.modal.is-active');
    expect(textOf(details.find('.modal-card-title'))).toBe('kali.qc2');
  });

  it('shows the readable chain, then the missing file, under the reason a VM cannot start', async () => {
    const details = await openDetails(await page(disks), 'kali.qc2');

    expect(textOf(details.find('.chain-problem'))).toBe(
      `A VM cannot start from this image: ${PROBLEM}.`,
    );
    const chain = details.find('[style*="text-align: center"]');
    expect(
      chain
        .findAll(':scope > div')
        .map(({ element }) => element.lastElementChild)
        .map((el) => [el.localName, textOf(el)]),
    ).toEqual([
      ['button', 'mid.qc2'],
      ['span', `${MISSING} (missing)`],
    ]);
  });

  it('shows the problem of a broken chain with no backing image it could read', async () => {
    const details = await openDetails(await page(disks), 'mid.qc2');

    expect(textOf(details.find('.chain-problem'))).toBe(
      `A VM cannot start from this image: ${PROBLEM}.`,
    );
    expect(textOf(details.find('.missing-backing'))).toBe(
      `${MISSING} (missing)`,
    );
  });

  it('names no missing file for a chain that loops', async () => {
    const details = await openDetails(await page(disks), '/data/vms/loop.qc2');

    expect(textOf(details.find('.chain-problem'))).toBe(
      'A VM cannot start from this image: backing chain loops back to ' +
        '/data/vms/loop.qc2.',
    );
    expect(details.find('.missing-backing').exists()).toBe(false);
  });

  it('shows no chain problem for a whole chain', async () => {
    const details = await openDetails(await page(disks), 'whole.qc2');

    expect(details.find('.chain-problem').exists()).toBe(false);
    expect(details.text()).not.toContain('Backing Chain');
  });
});

describe('the Type column', () => {
  const IMAGES = '/phenix/images';
  const V2 = {
    level: 'warning',
    problem: `${IMAGES}/kali.qc2 is a qcow2 version 2 image, an old version of the format`,
    fix: 'Stop each VM that uses it, then upgrade it to version 3',
    command: `qemu-img amend -f qcow2 -o compat=1.1 ${IMAGES}/kali.qc2`,
  };
  const AES = {
    level: 'error',
    problem: `${IMAGES}/aes.qc2 uses the old qcow AES encryption, which qemu does not open for VMs`,
    fix:
      'Write a copy without encryption. The Image Format Versions section ' +
      'of the disks docs shows how',
    command: '',
  };
  const typed = (name, fields) =>
    disk(`${IMAGES}/${name}`, name, { formatIssue: null, ...fields });
  const disks = [
    typed('base.qc2', { format: 'qcow2', formatVersion: 3 }),
    typed('kali.qc2', { format: 'qcow2', formatVersion: 2, formatIssue: V2 }),
    typed('aes.qc2', { format: 'qcow2', formatVersion: 3, formatIssue: AES }),
    typed('old.qcow', {
      format: 'qcow',
      formatVersion: 1,
      formatIssue: { ...V2, problem: 'old', fix: 'Convert it' },
    }),
    typed('win.vmdk', {
      kind: 'Unknown',
      format: 'vmdk',
      formatIssue: { ...V2, problem: 'vmdk', fix: 'Convert it' },
    }),
    typed('tools.iso', { kind: 'ISO', format: 'raw' }),
    typed('sensor_rootfs.tgz', { kind: 'Container', format: 'raw' }),
  ];
  const typeOf = (cells) => textOf(cells.Type.find('.disk-type'));

  it('shows the type of each disk, sorted by it both ways', async () => {
    const wrapper = await page(disks);

    expect(
      tableRows(wrapper).map((cells) => [nameOf(cells), typeOf(cells)]),
    ).toEqual([
      ['aes.qc2', 'qcow2 v3'],
      ['base.qc2', 'qcow2 v3'],
      ['kali.qc2', 'qcow2 v2'],
      ['old.qcow', 'qcow v1'],
      ['sensor_rootfs.tgz', 'tgz'],
      ['tools.iso', 'ISO'],
      ['win.vmdk', 'vmdk'],
    ]);

    const sorted = [
      'ISO',
      'qcow v1',
      'qcow2 v2',
      'qcow2 v3',
      'qcow2 v3',
      'tgz',
      'vmdk',
    ];
    await tableHeader(wrapper, 'Type').trigger('click');
    expect(tableRows(wrapper).map(typeOf)).toEqual(sorted);
    await tableHeader(wrapper, 'Type').trigger('click');
    expect(tableRows(wrapper).map(typeOf)).toEqual(sorted.toReversed());
  });

  it('marks a format issue with an icon whose name, tooltip and description say it', async () => {
    const wrapper = await page(disks);

    const marks = tableRows(wrapper).map((cells) =>
      tooltipButtons(cells.Type).map(({ label, tooltip, control }) => ({
        label,
        tooltip,
        icon: control.find('[data-icon]').attributes('data-icon'),
        described: textOf(
          wrapper.find(`[id="${control.attributes('aria-describedby')}"]`),
        ),
      })),
    );
    const v2Text =
      `${V2.problem}. Stop each VM that uses it, then upgrade it to ` +
      `version 3: ${V2.command}`;
    const aesText = `${AES.problem}. ${AES.fix}.`;
    expect(marks).toEqual([
      [
        {
          label: 'Format error',
          tooltip: aesText,
          icon: 'times-circle',
          described: aesText,
        },
      ],
      [],
      [
        {
          label: 'Format warning',
          tooltip: v2Text,
          icon: 'exclamation-triangle',
          described: v2Text,
        },
      ],
      [expect.objectContaining({ label: 'Format warning' })],
      [],
      [],
      [expect.objectContaining({ label: 'Format warning' })],
    ]);
    // the icon is a button, so the keyboard reaches its tooltip
    expect(
      tooltipButtons(tableRows(wrapper)[0].Type)[0].control.element.localName,
    ).toBe('button');
  });

  it('sorts any list of types as the column shows them', async () => {
    const formats = fc.record({
      format: fc.constantFrom('qcow2', 'qcow', 'raw', 'vmdk', 'qed', ''),
      formatVersion: fc.nat(12),
    });
    await fc.assert(
      fc.asyncProperty(
        fc.array(formats, { minLength: 1, maxLength: 6 }),
        async (list) => {
          const wrapper = await page(
            list.map((fields, i) => typed(`${i}.img`, fields)),
          );
          await tableHeader(wrapper, 'Type').trigger('click');

          const shown = tableRows(wrapper).map(typeOf);
          const collate = (a, b) =>
            a.localeCompare(b, 'en-US', { numeric: true, sensitivity: 'base' });
          expect(shown).toEqual(shown.toSorted(collate));
          expect(shown.toSorted()).toEqual(
            list
              .map((fields, i) => diskTypeOf(typed(`${i}.img`, fields)))
              .toSorted(),
          );
          wrapper.unmount();
        },
      ),
      { numRuns: 20 },
    );
  });

  it('shows the type, the problem, the fix and its command in the details', async () => {
    const details = await openDetails(await page(disks), 'kali.qc2');

    expect(textOf(detail(details, 'Type:').find('.disk-type'))).toBe(
      'qcow2 v2',
    );
    expect(textOf(details.find('.format-problem'))).toBe(
      `Format warning: ${V2.problem}.`,
    );
    expect(textOf(details.find('.format-fix'))).toBe(`${V2.fix}:`);
    expect(textOf(details.find('.format-command'))).toBe(V2.command);
    const docs = details.find('a.format-docs');
    expect([textOf(docs), docs.attributes('href')]).toEqual([
      'The fix for each image format (phēnix docs)',
      'https://phenix.sceptre.dev/latest/disks/#image-format-versions',
    ]);
  });

  it('shows a fix with no command, and no format section for a disk that needs none', async () => {
    const wrapper = await page(disks);

    let details = await openDetails(wrapper, 'aes.qc2');
    expect(textOf(details.find('.format-problem'))).toBe(
      `Format error: ${AES.problem}.`,
    );
    expect(textOf(details.find('.format-fix'))).toBe(`${AES.fix}.`);
    expect(details.find('.format-command').exists()).toBe(false);

    await details.find('.modal-close, .delete').trigger('click');
    details = await openDetails(wrapper, 'base.qc2');
    expect(textOf(detail(details, 'Type:'))).toBe('qcow2 v3');
    expect(details.find('.format-issue').exists()).toBe(false);
  });
});

describe('disk action requests', () => {
  // a path whose characters a query string would otherwise mangle
  const path = '/phenix/images/a+b&c=d/x%1.qc2';
  const target = disk('/phenix/images/b&c.qc2', 'b&c.qc2');

  // the page, and the details window of the disk at that path
  async function opened() {
    const wrapper = await page([
      disk(path, 'a+b&c=d/x%1.qc2', {
        backingImages: ['/phenix/images/base.qc2'],
      }),
      target,
    ]);
    return { wrapper, details: await openDetails(wrapper, 'a+b&c=d/x%1.qc2') };
  }

  // the window an action in the details opened over them
  const actionWindow = (wrapper) => wrapper.findAll('.modal.is-active').at(-1);

  // the query the server gets for a request's config
  const query = (config) =>
    Object.fromEntries(
      new URL(realAxios.getUri(config), 'http://phenix.test/api/v1/')
        .searchParams,
    );

  it.each([
    ['Snapshot', 'post', 'disks/snapshot', { new: 'n+1&.qc2' }],
    ['Rename', 'post', 'disks/rename', { new: 'n+1&.qc2' }],
    ['Resize', 'post', 'disks/resize', { size: '+5G' }],
    ['Delete', 'delete', 'disks', {}],
  ])(
    '%s sends the disk and its input as query parameters, intact',
    async (action, verb, url, input) => {
      axios.request.mockResolvedValue({});
      await runAction((await opened()).details, action);
      await confirmDialog(Object.values(input)[0]);

      const config = axios.request.mock.lastCall[0];
      expect(config).toEqual({
        method: verb,
        url,
        params: { disk: path, ...input },
      });
      expect(query(config)).toEqual({ disk: path, ...input });
      await vi.waitFor(() => expect(dialogs()).toEqual([]));
      expect(loaderSpy.load).toHaveBeenCalled();
    },
  );

  // has the server start the clone as this job
  const starts = (id) =>
    axios.post.mockResolvedValue({
      data: { id, kind: 'disk.clone', status: 'running', revision: 1 },
    });

  it('clones in a background job, closing the prompt once it starts and reloading when it ends', async () => {
    starts('j1');
    const done = {
      id: 'j1',
      kind: 'disk.clone',
      status: 'succeeded',
      revision: 3,
    };
    axios.get.mockResolvedValue({ data: done });

    await runAction((await opened()).details, 'Clone');
    await confirmDialog('n+1&.qc2');

    const [url, body, config] = axios.post.mock.lastCall;
    expect([url, body]).toEqual(['disks/clone/jobs', null]);
    expect(query({ url, ...config })).toEqual({ disk: path, new: 'n+1&.qc2' });
    await vi.waitFor(() => expect(dialogs()).toEqual([]));
    expect(loaderSpy.load).not.toHaveBeenCalled();

    useJobStore().apply(wsMsg('job', 'j1', 'update', done));
    await flushPromises();
    expect(loaderSpy.load).toHaveBeenCalled();
  });

  it('says why a clone failed, and stops waiting for a job the server forgot', async () => {
    starts('j1');
    const failed = {
      id: 'j1',
      kind: 'disk.clone',
      status: 'failed',
      error: 'every item failed',
      items: [{ name: path, status: 'failed', error: 'no space left' }],
      revision: 3,
    };
    axios.get.mockResolvedValue({ data: failed });
    const { details } = await opened();

    await runAction(details, 'Clone');
    await confirmDialog('x');
    useJobStore().apply(wsMsg('job', 'j1', 'update', failed));
    await flushPromises();
    expect(notices.open).toHaveBeenCalledTimes(1);
    expect(noticeText()).toContain('no space left');

    // the server restarted while the next clone ran
    starts('j2');
    await runAction(details, 'Clone');
    await confirmDialog('y');
    useJobStore().apply(wsMsg('job', 'j2', 'remove', {}));
    await flushPromises();
    expect(notify.error.mock.lastCall[0].message).toMatch(
      /no longer has job j2/,
    );
    expect(loaderSpy.load).toHaveBeenCalledTimes(2);
  });

  it('keeps the prompt open and shows why a clone could not start', async () => {
    const err = httpError(409, 'disk image exists');
    axios.post.mockRejectedValue(err);

    await runAction((await opened()).details, 'Clone');
    await confirmDialog('x');

    expect(notify.error).toHaveBeenCalledWith(err);
    const [prompt] = dialogs();
    expect(prompt).toBeDefined();
    expect(
      prompt.querySelector('footer .button:last-child').classList,
    ).not.toContain('is-loading');
  });

  it("shows a disk's clones in its details", async () => {
    useJobStore().put({
      id: 'j1',
      kind: 'disk.clone',
      params: { disk: path },
      status: 'running',
      revision: 1,
    });
    useJobStore().put({
      id: 'j2',
      kind: 'disk.clone',
      params: { disk: '/other.qc2' },
      status: 'running',
      revision: 1,
    });
    const { wrapper } = await opened();
    expect(
      wrapper.findAllComponents(JobProgress).map((p) => p.props('job').id),
    ).toEqual(['j1']);
  });

  it('rebases with the disk, backing image and mode as query parameters', async () => {
    axios.request.mockResolvedValue({});
    const { wrapper, details } = await opened();
    await runAction(details, 'Rebase');

    const rebase = actionWindow(wrapper);
    await rebase.find('select').setValue(target.fullPath);
    await rebase.find('input[type="checkbox"]').setValue(true);
    await rebase.findAll('footer button').at(-1).trigger('click');
    expect(query(axios.request.mock.lastCall[0])).toEqual({
      disk: path,
      backing: target.fullPath,
      unsafe: 'true',
    });
  });

  it.each([
    ['Commit', 'post'],
    ['Rebase', 'request'],
  ])(
    'leaves the %s dialog ready to cancel when it fails',
    async (action, method) => {
      axios[method].mockRejectedValue(httpError(500, 'read-only image'));
      const { wrapper, details } = await opened();
      await runAction(details, action);
      const dialog = actionWindow(wrapper);
      if (action === 'Rebase') {
        await dialog.find('select').setValue(target.fullPath);
      }
      await dialog.findAll('footer button').at(-1).trigger('click');
      await flushPromises();
      expect(buttons(dialog)).toEqual(['Cancel', 'OK']);
    },
  );

  it('commits, then deletes the committed disk when asked', async () => {
    axios.post.mockResolvedValue({});
    axios.request.mockResolvedValue({});
    const { wrapper, details } = await opened();
    await runAction(details, 'Commit');

    const commit = actionWindow(wrapper);
    await commit.find('input[type="checkbox"]').setValue(true);
    await commit.findAll('footer button').at(-1).trigger('click');
    expect(axios.post).toHaveBeenCalledWith('disks/commit', null, {
      params: { disk: path },
    });
    expect(query({ url: 'disks/commit', params: { disk: path } })).toEqual({
      disk: path,
    });
    await flushPromises();
    expect(axios.request).toHaveBeenCalledWith({
      method: 'delete',
      url: 'disks',
      params: { disk: path },
    });
  });
});

describe('the disk totals', () => {
  // the counter's stats, as "label: value"
  const totalsText = (wrapper) =>
    wrapper
      .findAll('[aria-label="Disk totals"] .stat')
      .map((s) => `${textOf(s.find('dt'))}: ${textOf(s.find('dd'))}`);

  it('counts every disk, the ones in use, and their sizes, whatever the search', async () => {
    const wrapper = await page([
      disk('/phenix/images/a.qc2', 'a.qc2', {
        inUse: true,
        size: '512.0 MiB',
        virtualSize: '2.0 GiB',
      }),
      disk('/phenix/images/b.qc2', 'b.qc2', {
        size: '1.5G',
        virtualSize: '1G',
      }),
      disk('/phenix/images/c.iso', 'c.iso', { virtualSize: '' }),
    ]);
    const want = [
      'Disks: 3',
      'In use: 1',
      'Total size on disk: 2.0 GiB (1 unknown)',
    ];
    expect(totalsText(wrapper)).toEqual(want);

    await wrapper.find('input[placeholder="Find a Disk"]').setValue('a.qc2');
    expect(tableRows(wrapper)).toHaveLength(1);
    expect(totalsText(wrapper)).toEqual(want);
  });

  it('counts one disk, and shows nothing before the list loads', async () => {
    const one = await page([disk('/phenix/images/a.qc2', 'a.qc2')]);
    expect(totalsText(one)[0]).toBe('Disks: 1');
    expect(totalsText(one)[2]).toBe('Total size on disk: 0 B (1 unknown)');

    const known = await page([
      disk('/phenix/images/a.qc2', 'a.qc2', { size: '3 K', virtualSize: '1G' }),
    ]);
    expect(totalsText(known)[2]).toBe('Total size on disk: 3.0 KiB');

    const loading = await mountPage(Disks, {
      route: '/disks/',
      data: { disks: [], loaded: false },
    });
    expect(loading.find('[aria-label="Disk totals"]').exists()).toBe(false);
  });

  // at 1024 px the Paginate switch stayed beside the counts while the search
  // dropped below them
  it('sits left of the paging, search and upload controls, which wrap together as on the Configs page', async () => {
    rbac.allowed = allowAll;
    const wrapper = await page(
      Array.from({ length: 11 }, (_, i) =>
        disk(`/phenix/images/d${i}.qc2`, `d${i}.qc2`),
      ),
    );
    const toolbar = wrapper.find('.level.disks-toolbar');
    expect(
      toolbar.find('.level-left [aria-label="Disk totals"]').exists(),
    ).toBe(true);
    const right = toolbar.find('.level-right');
    expect([
      right.find('.switch').exists(),
      right.find('input[placeholder="Find a Disk"]').exists(),
      right.find('[aria-label="Upload a disk"]').exists(),
    ]).toEqual([true, true, true]);

    const style = (selector) =>
      componentDeclarations('src/views/Disks.vue', selector);
    expect(style('.level.disks-toolbar')).toMatchObject({
      'flex-wrap': 'wrap',
    });
    expect(style('.disks-toolbar .level-right')).toEqual({
      'margin-left': 'auto',
    });
  });

  it('adds up any list of disks', () => {
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    const sizeArb = fc.option(
      fc.record({ tenths: fc.nat(20000), power: fc.nat(units.length - 1) }),
    );
    const entry = fc.record({
      inUse: fc.boolean(),
      onDisk: sizeArb,
    });
    const text = (size) =>
      size ? `${(size.tenths / 10).toFixed(1)} ${units[size.power]}` : '';
    const sum = (sizes) =>
      sizes.reduce(
        (total, { tenths, power }) => total + (tenths / 10) * 1024 ** power,
        0,
      );
    fc.assert(
      fc.property(fc.array(entry, { maxLength: 30 }), (entries) => {
        const disks = entries.map(({ inUse, onDisk }, i) =>
          disk(`/phenix/images/${i}.qc2`, `${i}.qc2`, {
            inUse,
            size: text(onDisk),
          }),
        );
        const knownOnDisk = entries
          .filter((e) => e.onDisk)
          .map((e) => e.onDisk);
        const got = diskTotals(disks);
        expect(got.disks).toBe(entries.length);
        expect(got.inUse).toBe(entries.filter((e) => e.inUse).length);
        expect(got.unknownDiskSizes).toBe(entries.length - knownOnDisk.length);
        expect(got.sizeBytes).toBeCloseTo(sum(knownOnDisk), 0);
      }),
    );
  });
});
