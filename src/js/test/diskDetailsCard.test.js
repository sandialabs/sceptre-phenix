// Mounts the Disks page's details card with real Buefy: its head, tiles,
// sections, actions and copy buttons, from a disk and GET disks/details.
import fc from 'fast-check';
import { flushPromises } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';

const { axios, modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);

import DiskDetailsCard from '@/components/DiskDetailsCard.vue';
import JobProgress from '@/components/jobs/JobProgress.vue';
import { disk, diskDetails, httpError, partition } from './helpers/fixtures.js';
import { allowAll, denyOnly } from './helpers/permissions.js';
import { componentDeclarations, contrast } from './helpers/css.js';
import {
  byLabel,
  mountComponent,
  textOf,
  tooltipButtons,
} from './helpers/mount.js';

const FILE = 'src/components/DiskDetailsCard.vue';

const kali = disk('/phenix/images/kali.qc2', 'kali.qc2', {
  size: '16 GiB',
  virtualSize: '80 GiB',
  format: 'qcow2',
  formatVersion: 3,
  formatIssue: null,
  inUse: true,
  experiments: [{ name: 'bennu', running: true }],
});
const base = disk('/phenix/images/base.qc2', 'base.qc2', {
  size: '4 GiB',
  format: 'qcow2',
  formatVersion: 3,
});
const snap = disk('/phenix/images/snap.qc2', 'snap.qc2', {
  backingImages: [kali.fullPath],
});

// the actions as Disks.vue offers them
const ACTIONS = [
  ['snapshot', 'Snapshot', 'camera'],
  ['commit', 'Commit', 'save'],
  ['rebase', 'Rebase', 'route'],
  ['clone', 'Clone', 'copy'],
  ['resize', 'Resize', 'expand'],
  ['download', 'Download', 'download'],
  ['rename', 'Rename', 'pencil'],
  ['delete', 'Delete', 'trash'],
].map(([name, label, icon]) => ({
  name,
  label,
  icon,
  description: `${label} it`,
  disabled: false,
  tooltip: label,
}));

beforeEach(() => {
  resetMocks({ store: {} });
  axios.get.mockResolvedValue({ data: diskDetails() });
});

// the card for a disk, once its details have loaded
async function card(props = {}) {
  const wrapper = await mountComponent(DiskDetailsCard, {
    props: {
      disk: kali,
      disks: [kali, base, snap],
      filesDir: '/phenix/images',
      actions: ACTIONS,
      ...props,
    },
  });
  await flushPromises();
  return wrapper;
}

// the text of a node with a space between its text nodes, as the card's
// gaps and boxes show it
const spaced = (node) => {
  const el = node.element ?? node;
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  const parts = [];
  while (walker.nextNode()) parts.push(walker.currentNode.data);
  return parts.join(' ').replace(/\s+/g, ' ').trim();
};

// the section whose title is `title`
const section = (wrapper, title) =>
  wrapper
    .findAll('.disk-section')
    .find((s) => textOf(s.find('.disk-section-title')) === title);

// each stat tile's value and label, by its key
const stats = (wrapper) =>
  Object.fromEntries(
    wrapper.findAll('.disk-stat').map((tile) => [
      tile
        .classes()
        .find((c) => c.startsWith('disk-stat-'))
        .slice(10),
      [
        textOf(tile.find('.disk-stat-value')),
        textOf(tile.find('.disk-stat-label')),
      ],
    ]),
  );

// each partition row's cells, as text
const partitionRows = (wrapper) =>
  wrapper
    .findAll('.disk-partitions tbody tr')
    .map((tr) => tr.findAll('td').map(spaced));

describe('the head', () => {
  const V2 = {
    level: 'warning',
    problem: 'kali.qc2 is a qcow2 version 2 image',
    fix: 'Upgrade it',
    command: 'qemu-img amend -f qcow2 -o compat=1.1 kali.qc2',
  };

  it.each([
    ['a whole image', {}, {}, 'ok', 'OK'],
    ['an old version', { formatIssue: V2 }, {}, 'warning', 'Warning'],
    [
      'an image outside the files directory',
      { outsideFilesDir: true },
      {},
      'warning',
      'Warning',
    ],
    [
      'a VM injecting into a missing partition',
      {},
      {
        vms: [
          {
            experiment: 'bennu',
            running: true,
            vm: 'kali',
            drive: 0,
            injectPartition: 3,
          },
        ],
      },
      'warning',
      'Warning',
    ],
    ['a broken chain', { brokenChain: true }, {}, 'danger', 'Error'],
    [
      'an image qemu refuses',
      { formatIssue: { ...V2, level: 'error' } },
      {},
      'danger',
      'Error',
    ],
  ])(
    'is colored and tagged for %s',
    async (_, fields, details, health, label) => {
      axios.get.mockResolvedValue({ data: diskDetails(details) });
      const wrapper = await card({ disk: { ...kali, ...fields } });

      expect(wrapper.find('.disk-card-head').classes()).toContain(
        `has-health-${health}`,
      );
      // the tag says it in text and an icon, not by its color alone
      const tag = wrapper.find('.disk-health-tag');
      expect(textOf(tag)).toBe(`Health: ${label}`);
      expect(tag.find('[data-icon]').exists()).toBe(true);
      expect(wrapper.find('.disk-problems').exists()).toBe(health !== 'ok');
    },
  );

  it('names the disk and tags its kind, type and use', async () => {
    const wrapper = await card({ disk: { ...kali, readOnly: true } });

    expect(textOf(wrapper.find('.disk-card-title'))).toBe('kali.qc2');
    expect(wrapper.findAll('.disk-card-title-row .tag').map(textOf)).toEqual([
      'Health: OK',
      'Kind: VM',
      'Type: qcow2 v3',
      'In use',
      'Read-only',
    ]);
    expect(textOf(wrapper.find('.disk-full-path'))).toBe(kali.fullPath);
    expect(textOf(wrapper.find('.disk-location-text'))).toBe(
      'The standard images directory (/phenix/images), at a path the disk list leaves out',
    );
    expect(wrapper.find('.disk-modified time').attributes('datetime')).toBe(
      '2026-09-28T14:12:00Z',
    );
  });

  it.each([
    ['VM', 'desktop'],
    ['ISO', 'compact-disc'],
    ['Container', 'cube'],
    ['', 'question-circle'],
  ])('marks the %s kind with its icon', async (kind, icon) => {
    const wrapper = await card({ disk: { ...kali, kind } });
    expect(
      wrapper.find('.disk-kind-tag [data-icon]').attributes('data-icon'),
    ).toBe(icon);
  });

  it('warns of an image outside the files directory where it says so', async () => {
    const wrapper = await card({ disk: { ...kali, outsideFilesDir: true } });
    const warnings = tooltipButtons(wrapper.find('.disk-location'));
    expect(warnings.map((w) => w.label)).toEqual([
      'Outside the standard images directory',
    ]);
  });
});

describe('the stat tiles', () => {
  it('show the sizes, the space taken, the partitions and the backing depth', async () => {
    const wrapper = await card({
      disk: { ...kali, backingImages: [base.fullPath] },
    });

    expect(stats(wrapper)).toEqual({
      size: ['16 GiB', 'Size on disk'],
      virtual: ['80 GiB', 'Virtual size'],
      allocated: ['20%', 'Allocated'],
      partitions: ['3', 'Partitions'],
      table: ['MBR', 'Partition table'],
      backing: ['1', 'Backing image'],
    });
    expect(
      wrapper
        .find('.disk-stat-allocated .disk-stat-bar span')
        .attributes('style'),
    ).toBe('width: 20%;');
  });

  it('use icons that mean nothing else in phēnix', async () => {
    const wrapper = await card();
    expect(
      wrapper
        .findAll('.disk-stat > .icon [data-icon]')
        .map((i) => i.attributes('data-icon')),
    ).toEqual([
      'hdd',
      'expand',
      'chart-pie',
      'table-columns',
      'table-list',
      'layer-group',
    ]);
  });

  it('leave out the disk tiles for a container image', async () => {
    axios.get.mockResolvedValue({
      data: diskDetails({
        qcow: null,
        virtualBytes: 0,
        partitionTable: '',
        partitions: [],
        partitionsUnavailable:
          'a container image holds a file system archive, not a disk with partitions',
      }),
    });
    const wrapper = await card({
      disk: { ...kali, kind: 'Container', virtualSize: '' },
    });
    expect(stats(wrapper)).toEqual({
      size: ['16 GiB', 'Size on disk'],
      backing: ['0', 'Backing images'],
    });
  });

  it('show the allocated percent and bar for any sizes', async () => {
    await fc.assert(
      fc.asyncProperty(
        fc.integer({ min: 1, max: 2 ** 40 }),
        fc.integer({ min: 1, max: 2 ** 40 }),
        async (used, total) => {
          axios.get.mockResolvedValue({
            data: diskDetails({ allocatedBytes: used, virtualBytes: total }),
          });
          const wrapper = await card();
          const percent = (used / total) * 100;
          const text = percent < 1 ? '<1%' : `${Math.round(percent)}%`;
          expect(stats(wrapper).allocated[0]).toBe(text);
          const width = wrapper
            .find('.disk-stat-allocated .disk-stat-bar span')
            .element.style.width.replace('%', '');
          expect(Number(width)).toBeCloseTo(Math.min(100, percent));
          wrapper.unmount();
        },
      ),
      { numRuns: 20 },
    );
  });

  it('mark a broken chain in text and color', async () => {
    const wrapper = await card({ disk: { ...kali, brokenChain: true } });
    expect(stats(wrapper).backing).toEqual([
      '0',
      'Backing images, chain broken',
    ]);
    expect(wrapper.find('.disk-stat-backing').classes()).toContain('is-danger');
  });

  it('wait for the details, then say what is not known', async () => {
    let answer;
    axios.get.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    const wrapper = await card();

    expect(stats(wrapper).partitions[0]).toBe('…');
    expect(stats(wrapper).table[0]).toBe('…');
    expect(textOf(section(wrapper, 'Partitions').find('.disk-loading'))).toBe(
      'Reading the image…',
    );
    // the list's sizes show at once
    expect(stats(wrapper).allocated[0]).toBe('20%');

    answer({
      data: diskDetails({
        partitionTable: '',
        partitions: [],
        partitionsUnavailable:
          'a container image holds a file system archive, not a disk with partitions',
      }),
    });
    await flushPromises();
    expect(stats(wrapper).partitions[0]).toBe('?');
    expect(stats(wrapper).table[0]).toBe('?');
  });
});

describe('the format section', () => {
  it('shows the qcow header and its sizes', async () => {
    const wrapper = await card();
    const rows = section(wrapper, 'Format and health')
      .findAll('.disk-kv > div')
      .map((row) => [textOf(row.find('dt')), textOf(row.find('dd'))]);
    expect(rows).toEqual([
      ['Type', 'qcow2 v3'],
      ['Version', '3 (compat 1.1)'],
      ['Cluster size', '64.0 KiB'],
      ['Refcount width', '16 bits'],
      ['Encryption', 'None'],
      ['Compression', 'zlib'],
      ['Internal snapshots', '0'],
      ['File size', '15.0 GiB'],
      ['Space used', '16.0 GiB'],
      ['Virtual size', '80.0 GiB'],
    ]);
  });

  it('shows a qcow v1 header with its backing file and AES encryption', async () => {
    axios.get.mockResolvedValue({
      data: diskDetails({
        format: 'qcow',
        formatVersion: 1,
        qcow: {
          ...diskDetails().qcow,
          compat: '',
          clusterSize: 4096,
          refcountBits: 0,
          encryption: 'aes',
          backingFile: 'base.img',
          backingFormat: '',
        },
        fileBytes: 0,
        allocatedBytes: 0,
        virtualBytes: 0,
      }),
    });
    const wrapper = await card({
      disk: { ...kali, format: 'qcow', formatVersion: 1 },
    });
    const rows = section(wrapper, 'Format and health')
      .findAll('.disk-kv > div')
      .map((row) => [textOf(row.find('dt')), textOf(row.find('dd'))]);
    expect(rows).toEqual([
      ['Type', 'qcow v1'],
      ['Version', '1'],
      ['Cluster size', '4.0 KiB'],
      ['Encryption', 'AES'],
      ['Compression', 'zlib'],
      ['Internal snapshots', '0'],
      ['Header backing file', 'base.img'],
    ]);
  });

  it('names the backing format beside the header backing file', async () => {
    axios.get.mockResolvedValue({
      data: diskDetails({
        qcow: {
          ...diskDetails().qcow,
          backingFile: 'base.qc2',
          backingFormat: 'qcow2',
        },
      }),
    });
    const wrapper = await card();
    const row = section(wrapper, 'Format and health')
      .findAll('.disk-kv > div')
      .find((r) => textOf(r.find('dt')) === 'Header backing file');
    expect(textOf(row.find('dd'))).toBe('base.qc2 (qcow2)');
    expect(row.find('dd').classes()).toContain('disk-mono');
  });

  it('tags the features that need a note, with an icon and text', async () => {
    axios.get.mockResolvedValue({
      data: diskDetails({
        qcow: { ...diskDetails().qcow, dirty: true, lazyRefcounts: true },
      }),
    });
    const wrapper = await card();
    const tags = wrapper.findAll('.disk-features .tag');
    expect(tags.map(textOf)).toEqual(['Not closed cleanly', 'Lazy refcounts']);
    expect(
      tags.map((t) => t.find('[data-icon]').attributes('data-icon')),
    ).toEqual(['exclamation-triangle', 'info-circle']);
  });

  it('says why phēnix cannot open the file in place of its header', async () => {
    const why =
      'phenix cannot open the image (permission denied). If minimega runs on a different host, only minimega can read it';
    axios.get.mockResolvedValue({
      data: diskDetails({
        qcow: null,
        fileBytes: 0,
        allocatedBytes: 0,
        virtualBytes: 0,
        modified: '',
        fileUnavailable: why,
        partitionTable: '',
        partitions: [],
        partitionsUnavailable: why,
      }),
    });
    const wrapper = await card();

    for (const title of ['Format and health', 'Partitions']) {
      expect(textOf(section(wrapper, title).find('.disk-unavailable'))).toBe(
        `Not available: ${why}.`,
      );
    }
    expect(wrapper.find('.disk-modified').exists()).toBe(false);
  });
});

describe('the partitions', () => {
  it('lists each partition, marking the one a VM injects into', async () => {
    const wrapper = await card();

    expect(
      textOf(section(wrapper, 'Partitions').find('.disk-table-summary')),
    ).toBe('MBR partition table, disk ID 0x8f3c2a11, 512-byte sectors.');
    expect(partitionRows(wrapper)).toEqual([
      [
        '1',
        'Linux 0x83',
        'ext4',
        '1.0 MiB',
        '1.0 GiB',
        'Boot Inject partition of kali (bennu)',
      ],
      ['2', 'Extended 0x05', '—', '2.0 MiB', '1.0 GiB', 'Extended'],
      [
        '5',
        'Linux swap / Solaris 0x82',
        'Linux swap',
        '5.0 MiB',
        '1.0 GiB',
        'Logical',
      ],
    ]);
    const rows = wrapper.findAll('.disk-partitions tbody tr');
    expect(rows.map((r) => r.classes('is-inject'))).toEqual([
      true,
      false,
      false,
    ]);
    expect(wrapper.find('.disk-partitions caption').text()).toBe(
      'Partitions of kali.qc2',
    );
    expect(
      wrapper.find('.disk-part-flags [data-icon]').attributes('data-icon'),
    ).toBe('flag');
  });

  it('lets the keyboard scroll the table', async () => {
    const wrap = (await card()).find('.disk-table-wrap');
    expect(wrap.attributes()).toMatchObject({
      tabindex: '0',
      role: 'region',
      'aria-label': 'Partitions of kali.qc2',
    });
  });

  it.each([
    [
      'unknown',
      'phēnix does not know this partition table.',
      ['?', 'Partitions'],
      ['Unknown', 'Partition table'],
    ],
    [
      '',
      'phēnix did not read the partition table.',
      ['?', 'Partitions'],
      ['?', 'Partition table'],
    ],
  ])(
    'says what it knows of a partition table of kind %j',
    async (scheme, summary, parts, table) => {
      axios.get.mockResolvedValue({
        data: diskDetails({ partitionTable: scheme, partitions: [] }),
      });
      const wrapper = await card();

      expect(
        textOf(section(wrapper, 'Partitions').find('.disk-table-summary')),
      ).toBe(summary);
      expect(stats(wrapper).partitions).toEqual(parts);
      expect(stats(wrapper).table).toEqual(table);
      expect(wrapper.find('.disk-table-wrap').exists()).toBe(false);
    },
  );

  it('names GPT partitions and warns of an inject partition with no file system', async () => {
    axios.get.mockResolvedValue({
      data: diskDetails({
        partitionTable: 'gpt',
        diskId: '',
        partitions: [
          partition(1, 'EFI System', 'C12A7328-F81F-11D2-BA4B-00A0C93EC93B', {
            fs: 'vfat',
            name: 'EFI',
          }),
          partition(2, 'Linux swap', '0657FD6D-A4AB-43C4-84E5-0933C84B4F4F', {
            fs: 'swap',
          }),
        ],
        partitionNotes: ['the backup GPT header is damaged'],
        vms: [
          {
            experiment: 'bennu',
            running: true,
            vm: 'kali',
            drive: 0,
            injectPartition: 2,
          },
        ],
      }),
    });
    const wrapper = await card();

    expect(
      textOf(section(wrapper, 'Partitions').find('.disk-table-summary')),
    ).toBe('GPT partition table, 512-byte sectors.');
    expect(partitionRows(wrapper)[0].slice(0, 3)).toEqual([
      '1',
      'EFI System C12A7328-F81F-11D2-BA4B-00A0C93EC93B “EFI”',
      'FAT',
    ]);
    expect(wrapper.findAll('.disk-notes li').map(textOf)).toEqual([
      'VM kali (bennu) injects files into partition 2, which holds Linux swap, not a file system that phēnix can write files to.',
      'the backup GPT header is damaged.',
    ]);
    expect(textOf(wrapper.find('.disk-problems'))).toContain(
      'injects files into partition 2',
    );
  });

  it('says what a disk with no partition table holds', async () => {
    axios.get.mockResolvedValue({
      data: diskDetails({
        partitionTable: 'none',
        partitions: [],
        filesystem: { type: 'ext4', version: '1.0', label: 'data' },
      }),
    });
    const wrapper = await card();

    expect(stats(wrapper).partitions).toEqual(['0', 'Partitions']);
    expect(stats(wrapper).table).toEqual(['None', 'Partition table']);
    expect(spaced(section(wrapper, 'Partitions'))).toBe(
      'Partitions No partition table. The whole disk holds ext4 1.0 labeled “data”.',
    );
  });
});

describe('the backing chain', () => {
  const MISSING = '/phenix/images/ro_kali.qc2';
  const broken = {
    ...snap,
    backingImages: [kali.fullPath, '/data/vms/unlisted.qc2'],
    brokenChain: true,
    chainProblem: `backing file ${MISSING} is missing`,
    missingBacking: MISSING,
  };

  it('draws this image, each image under it, then the missing file in a red box', async () => {
    const wrapper = await card({ disk: broken });
    const chain = section(wrapper, 'Backing chain');

    expect(textOf(chain.find('.chain-problem'))).toBe(
      `A VM cannot start from this image: backing file ${MISSING} is missing.`,
    );
    const items = chain.findAll('.disk-chain-item');
    expect(items.map(spaced)).toEqual([
      'snap.qc2 This image',
      '↓ Backed by kali.qc2 qcow2 v3 · 16 GiB',
      '↓ Backed by /data/vms/unlisted.qc2 Not in your disk list',
      `↓ Backed by ${MISSING} (missing)`,
    ]);
    expect(items.at(-1).classes()).toContain('is-missing');
    expect(items.at(-1).find('[data-icon]').attributes('data-icon')).toBe(
      'times-circle',
    );
  });

  it("opens a listed image's card from its box", async () => {
    const wrapper = await card({ disk: broken });
    await wrapper.find('.backing-link').trigger('click');
    expect(wrapper.emitted('open')).toEqual([[kali.fullPath]]);
  });

  it('does not say there is no backing image when the chain is broken', async () => {
    const wrapper = await card({
      disk: {
        ...kali,
        brokenChain: true,
        chainProblem: `${kali.fullPath} is in a loop`,
      },
    });
    const chain = section(wrapper, 'Backing chain');
    expect(spaced(chain)).toBe(
      `Backing chain A VM cannot start from this image: ${kali.fullPath} is in a loop.`,
    );
    expect(chain.find('.disk-chain').exists()).toBe(false);
  });

  it('says a base image has no backing image', async () => {
    const wrapper = await card();
    expect(spaced(section(wrapper, 'Backing chain'))).toBe(
      'Backing chain None: this image has no backing image.',
    );
  });
});

describe('the images built on this one', () => {
  it('lists each overlay, opening a listed one, and says what a change does to them', async () => {
    axios.get.mockResolvedValue({
      data: diskDetails({
        overlays: [
          { fullPath: snap.fullPath, direct: true },
          { fullPath: '/data/vms/far.qc2', direct: false },
        ],
      }),
    });
    const wrapper = await card();
    const overlays = section(wrapper, 'Built on this image');

    expect(overlays.findAll('.disk-list li').map(spaced)).toEqual([
      'snap.qc2 Direct',
      '/data/vms/far.qc2 Through another image',
    ]);
    expect(textOf(overlays.find('.disk-overlay-note'))).toBe(
      'These images need this file. If you rename or delete it, they stop working.',
    );
    await overlays.find('.overlay-link').trigger('click');
    expect(wrapper.emitted('open')).toEqual([[snap.fullPath]]);
  });

  it('says when no listed image is built on it', async () => {
    const wrapper = await card();
    expect(
      textOf(section(wrapper, 'Built on this image').find('.disk-empty')),
    ).toBe('No listed image is built on this image.');
  });
});

describe('the experiments using the image', () => {
  const listed = {
    ...kali,
    experiments: [
      { name: 'bennu', running: true },
      { name: 'demo', running: false },
    ],
  };

  it.each([
    [
      'may get both experiments',
      allowAll,
      ['/experiment/bennu', '/experiment/bennu?vm=kali', '/experiment/demo'],
      'demo Stopped Through an image built on this image',
    ],
    [
      'may not get demo',
      denyOnly(['experiments', 'get', 'demo']),
      ['/experiment/bennu', '/experiment/bennu?vm=kali'],
      'demo Stopped Your role cannot see the VMs of this experiment',
    ],
  ])(
    'links each experiment a role %s, with its state and VMs',
    async (_, can, links, demo) => {
      rbac.allowed = can;
      const wrapper = await card({ disk: listed });
      const used = section(wrapper, 'Used by');

      expect(used.findAll('a').map((a) => a.attributes('href'))).toEqual(links);
      expect(used.findAll('.disk-use').map(spaced)).toEqual([
        'bennu Running VM kali , drive 0, inject partition 1',
        demo,
      ]);
    },
  );

  it('says when no experiment uses the image', async () => {
    axios.get.mockResolvedValue({ data: diskDetails({ vms: [] }) });
    const wrapper = await card({ disk: { ...kali, experiments: [] } });
    expect(textOf(section(wrapper, 'Used by').find('.disk-empty'))).toBe(
      'No experiment uses this image.',
    );
  });
});

describe('the actions', () => {
  it('show each action as an icon and a label, its description in a tooltip', async () => {
    const wrapper = await card();
    const buttons = wrapper.findAll('.disk-action');

    expect(
      buttons.map((b) => [
        b.attributes('aria-label'),
        b.find('[data-icon]').attributes('data-icon'),
        textOf(b.find('span:not(.icon):not(.is-sr-only)')).split(' ')[0],
        textOf(wrapper.find(`[id="${b.attributes('aria-describedby')}"]`)),
      ]),
    ).toEqual(ACTIONS.map((a) => [a.label, a.icon, a.label, a.description]));
    const tips = tooltipButtons(wrapper.find('.disk-action-buttons'));
    expect(tips.map((t) => t.tooltip)).toEqual(
      ACTIONS.map((a) => a.description),
    );
    await buttons[3].trigger('click');
    expect(wrapper.emitted('action')).toEqual([['clone']]);
  });

  it('sit at the bottom of the left column, as on the VM card', async () => {
    const wrapper = await card();
    const [left] = wrapper.findAll('.disk-columns > .column');
    const sections = left.findAll('.disk-section');
    expect(sections.at(-1).classes()).toContain('actions');
  });

  it('keep a disabled action focusable, saying why and sending nothing', async () => {
    const reason = 'Cannot rename: the disk is in use by bennu';
    const wrapper = await card({
      actions: ACTIONS.map((a) =>
        a.name === 'rename' ? { ...a, disabled: true, tooltip: reason } : a,
      ),
    });
    const rename = byLabel(wrapper, 'Rename');

    expect(rename.attributes('aria-disabled')).toBe('true');
    expect(rename.attributes('disabled')).toBeUndefined();
    const described = wrapper.find(
      `[id="${rename.attributes('aria-describedby')}"]`,
    );
    expect(textOf(described)).toBe(reason);
    const tips = tooltipButtons(wrapper.find('.disk-action-buttons'));
    expect(tips.find((t) => t.label === 'Rename')).toMatchObject({
      tooltip: reason,
      disabled: true,
    });
    await rename.trigger('click');
    expect(wrapper.emitted('action')).toBeUndefined();
  });

  it("shows the disk's clone jobs", async () => {
    const job = {
      id: 'j1',
      kind: 'disk.clone',
      status: 'running',
      revision: 1,
    };
    const wrapper = await card({ cloneJobs: [job] });
    expect(
      wrapper.findAllComponents(JobProgress).map((p) => p.props('job').id),
    ).toEqual(['j1']);
    const none = await card();
    expect(textOf(section(none, 'Clone jobs').find('.disk-empty'))).toBe(
      'No clone jobs.',
    );
  });
});

describe('the copy buttons', () => {
  const writeText = vi.fn();
  beforeEach(() => {
    writeText.mockReset().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
  });

  it.each([
    ['Copy the full path', kali.fullPath, 'Copied the full path.'],
    [
      'Copy the qemu-img info command',
      `qemu-img info --backing-chain -U ${kali.fullPath}`,
      'Copied the qemu-img info command.',
    ],
    [
      'Copy the fix command',
      'qemu-img amend -f qcow2 -o compat=1.1 /phenix/images/kali.qc2',
      'Copied the fix command.',
    ],
  ])('%s, saying so in a live region', async (label, text, status) => {
    const wrapper = await card({
      disk: {
        ...kali,
        formatIssue: {
          level: 'warning',
          problem: 'old',
          fix: 'Upgrade it',
          command:
            'qemu-img amend -f qcow2 -o compat=1.1 /phenix/images/kali.qc2',
        },
      },
    });
    const button = byLabel(wrapper, label);
    await button.trigger('click');
    await flushPromises();

    expect(writeText).toHaveBeenCalledWith(text);
    expect(textOf(wrapper.find('[role="status"]'))).toBe(status);
    expect(button.find('[data-icon]').attributes('data-icon')).toBe(
      'check-circle',
    );
  });

  it('says it copied again for a second copy of the same text', async () => {
    const wrapper = await card();
    const button = byLabel(wrapper, 'Copy the full path');
    const status = () => textOf(wrapper.find('[role="status"]'));
    await button.trigger('click');
    await flushPromises();
    expect(status()).toBe('Copied the full path.');

    let done;
    writeText.mockReturnValueOnce(new Promise((resolve) => (done = resolve)));
    await button.trigger('click');
    expect(status()).toBe('');
    done();
    await flushPromises();
    expect(status()).toBe('Copied the full path.');
  });

  it('clears the live region once the button stops showing that it copied', async () => {
    vi.useFakeTimers();
    try {
      const wrapper = await card();
      await byLabel(wrapper, 'Copy the full path').trigger('click');
      await vi.advanceTimersByTimeAsync(0);
      expect(textOf(wrapper.find('[role="status"]'))).toBe(
        'Copied the full path.',
      );
      await vi.advanceTimersByTimeAsync(2000);
      expect(textOf(wrapper.find('[role="status"]'))).toBe('');
    } finally {
      vi.useRealTimers();
    }
  });

  it('says when the browser did not copy', async () => {
    writeText.mockRejectedValue(new Error('denied'));
    const wrapper = await card();
    await byLabel(wrapper, 'Copy the full path').trigger('click');
    await flushPromises();
    expect(textOf(wrapper.find('[role="status"]'))).toBe(
      'The browser did not copy the full path.',
    );
  });
});

describe('loading the details', () => {
  it('asks for the disk by its full path, and again for another disk', async () => {
    const wrapper = await card();
    expect(axios.get).toHaveBeenCalledWith('disks/details', {
      params: { disk: kali.fullPath },
      signal: expect.any(AbortSignal),
    });

    await wrapper.setProps({ disk: base });
    await flushPromises();
    expect(axios.get.mock.lastCall[1].params).toEqual({ disk: base.fullPath });
  });

  it("shows only the newest disk's details, and moves focus to its name", async () => {
    const answers = [];
    axios.get.mockImplementation(
      () => new Promise((resolve) => answers.push(resolve)),
    );
    const wrapper = await mountComponent(DiskDetailsCard, {
      props: { disk: kali, disks: [kali, base], actions: ACTIONS },
      attachTo: document.body,
    });
    await wrapper.setProps({ disk: base });
    await nextTick();
    expect(document.activeElement).toBe(
      wrapper.find('.disk-card-title').element,
    );

    // the first request's answer comes last, and is dropped
    answers[1]({
      data: diskDetails({ fullPath: base.fullPath, diskId: 'base' }),
    });
    answers[0]({ data: diskDetails() });
    await flushPromises();
    expect(
      textOf(section(wrapper, 'Partitions').find('.disk-table-summary')),
    ).toContain('disk ID base');
  });

  it('says why the details did not load, in place of each part', async () => {
    axios.get.mockRejectedValue(httpError(404, 'disk image not found'));
    const wrapper = await card();
    const why =
      'Not available: phēnix cannot load the details: disk image not found.';

    for (const title of ['Partitions', 'Built on this image']) {
      expect(textOf(section(wrapper, title).find('.disk-unavailable'))).toBe(
        why,
      );
    }
    expect(stats(wrapper).partitions[0]).toBe('?');
    // what the list gives still shows
    expect(spaced(section(wrapper, 'Used by').find('.disk-use'))).toBe(
      'bennu Running',
    );
  });
});

describe('accessible names', () => {
  it('names each icon button and link', async () => {
    const wrapper = await card({
      disk: {
        ...kali,
        formatIssue: {
          level: 'warning',
          problem: 'old',
          fix: 'Fix',
          command: 'x',
        },
      },
    });
    const named = wrapper
      .findAll('button.disk-copy, a.disk-card-docs')
      .map((el) => el.attributes('aria-label'));
    expect(named).toEqual([
      'Copy the full path',
      'Disks documentation',
      'Copy the fix command',
      'Copy the qemu-img info command',
    ]);
    // every button has a name
    for (const button of wrapper.findAll('button')) {
      expect(textOf(button) || button.attributes('aria-label')).toBeTruthy();
    }
  });
});

describe('the colors', () => {
  // the card's surfaces: the head, the body, a section, a box in a section
  const HEAD = '#4f4f4f';
  const SECTION = '#484848';
  const BOX = '#545454';
  const fg = (selector) => componentDeclarations(FILE, selector).color;
  const bg = (selector) =>
    componentDeclarations(FILE, selector)['background-color'];

  it.each([
    ['meta text on the head', fg('.disk-card-meta'), HEAD],
    ['kind and type tags', fg('.disk-kind-tag'), bg('.disk-type-tag')],
    ['inject tag', fg('.disk-inject-tag'), bg('.disk-inject-tag')],
    ['section titles', fg('.disk-section-title'), SECTION],
    ['stat labels', fg('.disk-stat-label'), SECTION],
    ['empty notes', fg('.disk-empty'), SECTION],
    ['unavailable reasons', fg('.disk-unavailable'), SECTION],
    ['links on a section', '#8fd8ea', SECTION],
    ['links on a box', '#8fd8ea', BOX],
    ['chain box text', fg('.disk-chain-meta'), BOX],
    [
      'partition IDs on an inject row',
      '#d0d0d0',
      bg('.disk-partitions tr.is-inject td'),
    ],
    ['problem text on a danger box', '#f5f5f5', bg('.disk-problems.is-danger')],
    ['problem text on a warning box', '#f5f5f5', bg('.disk-problems')],
    ['danger icons on a danger box', '#ffc8cf', bg('.disk-problems.is-danger')],
    ['warning icons on a warning box', '#ffe08a', bg('.disk-problems')],
    [
      'disabled action text',
      fg(
        ".disk-action-buttons :deep(.button[aria-disabled='true']:not(.is-inverted))",
      ),
      bg(
        ".disk-action-buttons :deep(.button[aria-disabled='true']:not(.is-inverted))",
      ),
    ],
  ])('keep %s at 4.5:1 or more', (_, text, surface) => {
    expect(contrast(text, surface)).toBeGreaterThanOrEqual(4.5);
  });
});
