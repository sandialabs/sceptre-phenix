// The Disks page: how it loads the disk list and keeps it current, how it
// names each disk, which actions it offers on a disk and the reason it gives
// for each one it disables, in the details window and on the table's rows,
// and the requests those actions send.
import realAxios from 'axios';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const rbac = vi.hoisted(() => ({ allowed: () => true }));
vi.mock('@/utils/rbac.js', () => ({
  roleAllowed: (...args) => rbac.allowed(...args),
}));
const axios = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  request: vi.fn(),
}));
vi.mock('@/utils/axios.js', () => ({ default: axios }));
const notify = vi.hoisted(() => ({ show: vi.fn() }));
vi.mock('@/utils/errorNotif', () => ({
  showError: notify.show,
  useErrorNotification: vi.fn(),
}));
vi.mock('@/store.js', () => ({
  usePhenixStore: () => ({ token: 't', features: [], role: null }),
}));
vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
}));
// a loader whose load() fetches as the real one does, without the cache
const loader = vi.hoisted(() => ({ options: null, load: null }));
vi.mock('@/utils/pageLoader.js', () => ({
  createPageLoader: (options) => {
    loader.options = options;
    loader.load = vi.fn(() => options.fetch(new AbortController().signal));
    return { start: () => {}, stop: () => {}, load: loader.load };
  },
  loadingText: (what) => `Loading ${what}…`,
}));

import Disks from '@/views/Disks.vue';
import { flush } from './helpers/async.js';
import { makeContext } from './helpers/context.js';
import {
  openModal,
  renderSSR,
  selectOptions,
  tableRows,
  textOf,
  tooltipButtons,
} from './helpers/render.js';

const page = (fields) => makeContext(Disks, fields);

beforeEach(() => {
  rbac.allowed = () => true;
  axios.get.mockReset();
  axios.get.mockResolvedValue({ data: { disks: [] } });
  axios.post.mockReset();
  axios.request.mockReset();
  notify.show.mockReset();
});

// A disk as GET disks lists it: inside the files directory /phenix/images
// when given its path within it, else outside it and read-only.
const disk = (fullPath, relativePath = '', fields = {}) => ({
  name: fullPath.replace(/^.*\//, ''),
  fullPath,
  relativePath,
  outsideFilesDir: !relativePath,
  readOnly: !relativePath,
  kind: 'VM',
  inUse: false,
  backingImages: [],
  ...fields,
});

const OUTSIDE_TEXT =
  'Outside the standard images directory (/phenix/images). minimega does ' +
  'not copy this image to other cluster nodes, so on a multi-node cluster ' +
  'every node that may run a VM using it needs the same file at this path.';

// the page rendered with these disks, and more of its state
const rendered = (disks, data = {}) =>
  renderSSR(
    Disks,
    {},
    { route: '/disks/', data: { disks, loaded: true, ...data }, tables: true },
  );

// the text of a Name cell's label, without its warning
const labelOf = (cell) =>
  textOf(cell.match(/<span class="disk-label"[^>]*>([\s\S]*?)<\/span>/)[1]);

describe('the disk list', () => {
  async function opened() {
    const ctx = page();
    await Disks.created.call(ctx);
    return ctx;
  }
  const urls = () => axios.get.mock.calls.map(([url]) => url);

  it("has the server inspect every image again on the header's refresh", async () => {
    const ctx = await opened();

    await loader.options.refresh();
    await ctx.loader.load();
    expect(urls()).toEqual(['disks?refresh=true', 'disks']);
  });

  it('reloads when the server reports its images changed', async () => {
    const ctx = await opened();

    ctx.handleWs({ resource: { type: 'disks', action: 'update' } });
    ctx.handleWs({ resource: { type: 'experiment', action: 'update' } });
    expect(loader.load).toHaveBeenCalledTimes(1);
  });

  it('refuses to upload a file that is no disk image', () => {
    page().uploadDisk({ name: 'notes.txt' });

    expect(notify.show).toHaveBeenCalledWith(
      'Cannot upload notes.txt',
      'Valid disk file types are .qcow2, .qc2, *_rootfs.tgz, .hdd, and .iso.',
    );
    expect(axios.post).not.toHaveBeenCalled();
  });

  it('sorts sizes by their bytes, unknown sizes first', () => {
    const disks = ['2.1 GiB', '512 B', '', '3G', '900 MiB'].map((size) => ({
      size,
    }));
    disks.sort((a, b) => page().sortBy('size')(a, b, true));
    expect(disks.map((d) => d.size)).toEqual([
      '',
      '512 B',
      '900 MiB',
      '2.1 GiB',
      '3G',
    ]);
  });
});

const vmDisk = disk('/phenix/images/snap.qc2', 'snap.qc2', {
  backingImages: ['/phenix/images/base.qc2'],
});
const names = [
  'snapshot',
  'commit',
  'rebase',
  'clone',
  'resize',
  'download',
  'rename',
  'delete',
];

describe('disk actions', () => {
  it('lists every action in the details window, and some on each row', () => {
    const ctx = page();
    expect(ctx.diskActions.map((a) => a.name)).toEqual(names);
    expect(ctx.rowActions.map((a) => a.name)).toEqual([
      'snapshot',
      'clone',
      'download',
      'rename',
      'delete',
    ]);
  });

  it('says how the disk rules an action out: its kind, backing image or use', () => {
    const ctx = page();
    const tooltip = (name, disk) =>
      ctx.actionTooltip(
        ctx.diskActions.find((a) => a.name === name),
        disk,
      );

    expect(tooltip('commit', vmDisk)).toBe('Commit');
    for (const backingImages of [[], undefined]) {
      expect(tooltip('commit', { ...vmDisk, backingImages })).toBe(
        "Can't commit: the disk has no backing image to commit into",
      );
    }
    const kernel = { ...vmDisk, name: 'vmlinuz', kind: 'Kernel' };
    expect(tooltip('snapshot', kernel)).toBe(
      "Can't snapshot: only VM disks can be snapshotted",
    );
    expect(tooltip('commit', kernel)).toBe(
      "Can't commit: only VM disks can be committed",
    );
    expect(tooltip('rebase', kernel)).toBe(
      "Can't rebase: only VM disks can be rebased",
    );
    // the rest work on any disk
    for (const name of ['clone', 'resize', 'download', 'rename', 'delete']) {
      expect(tooltip(name, kernel)).toBe(
        ctx.diskActions.find((a) => a.name === name).label,
      );
    }

    const used = {
      ...vmDisk,
      inUse: true,
      experiments: [
        { name: 'a', running: true },
        { name: 'b', running: false },
      ],
    };
    for (const name of ['commit', 'rebase', 'resize', 'rename', 'delete']) {
      expect(tooltip(name, used)).toMatch(/: the disk is in use by a$/);
    }
    // cloning and downloading only read the disk
    expect(tooltip('clone', used)).toBe('Clone');
    expect(tooltip('download', used)).toBe('Download');
  });

  it('rules out the actions minimega runs on a path it cannot take', () => {
    const ctx = page();
    const spaced = disk('/phenix/images/my disk.qc2', 'my disk.qc2', {
      backingImages: ['/phenix/images/base.qc2'],
    });
    const reasons = Object.fromEntries(
      names.map((name) => [name, ctx.disabledReason(name, spaced)]),
    );
    const unusable = "minimega cannot use the disk's path";
    expect(reasons).toEqual({
      snapshot: unusable,
      commit: unusable,
      rebase: unusable,
      clone: null,
      resize: unusable,
      download: null,
      rename: null,
      delete: null,
    });
    // in a folder's name too
    for (const rel of ['a$b.qc2', 'x,y/a.qc2']) {
      const other = disk(`/phenix/images/${rel}`, rel);
      expect(ctx.disabledReason('resize', other)).toBe(unusable);
    }
  });

  // the permissions each action needs on snap.qc2, which is backed by base.qc2
  it.each([
    ['snapshot', [['disks', 'create']]],
    // committing writes the image into its backing image
    [
      'commit',
      [
        ['disks', 'update', 'snap.qc2'],
        ['disks', 'update', 'base.qc2'],
      ],
    ],
    ['rebase', [['disks', 'update', 'snap.qc2']]],
    ['clone', [['disks', 'create']]],
    ['resize', [['disks', 'update', 'snap.qc2']]],
    ['download', [['disks', 'get', 'snap.qc2']]],
    ['rename', [['disks', 'update', 'snap.qc2']]],
    ['delete', [['disks', 'delete', 'snap.qc2']]],
  ])('allows %s with the %j permissions alone', (action, needed) => {
    const ctx = page();
    const key = (permission) => JSON.stringify(permission);
    const neededKeys = new Set(needed.map(key));

    rbac.allowed = (...args) => neededKeys.has(key(args));
    expect(ctx.disabledReason(action, vmDisk)).toBe(null);
    // and refuses it without any one of them
    for (const missing of needed) {
      rbac.allowed = (...args) => key(args) != key(missing);
      expect(ctx.disabledReason(action, vmDisk)).toBe(
        "you don't have permission",
      );
    }
  });

  it('knows no reason to disable an action it does not have', () => {
    rbac.allowed = () => false;
    expect(page().disabledReason('unknown', vmDisk)).toBe(null);
  });

  // each row's actions: Snapshot, Clone, Download, Rename and Delete, each
  // enabled or saying why not
  const labels = ['Snapshot', 'Clone', 'Download', 'Rename', 'Delete'];
  const OK = 'enabled';
  const NO = "you don't have permission";
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
      () => true,
      {
        'snap.qc2': [OK, OK, OK, OK, OK],
        'isos/tools.iso': [VM_ONLY, OK, OK, OK, OK],
        'live.qc2': [USED, OK, OK, USED, USED],
        'my disk.qc2': [MM, OK, OK, OK, OK],
        ...readOnly,
      },
    ],
    [
      'a role that may only create disks',
      (_, verb) => verb == 'create',
      {
        'snap.qc2': [OK, OK, NO, NO, NO],
        'isos/tools.iso': [VM_ONLY, OK, NO, NO, NO],
        'live.qc2': [USED, OK, NO, USED, USED],
        'my disk.qc2': [MM, OK, NO, NO, NO],
        ...readOnly,
      },
    ],
    [
      'a role that may not download',
      (_, verb) => verb != 'get',
      {
        'snap.qc2': [OK, OK, NO, OK, OK],
        'isos/tools.iso': [VM_ONLY, OK, NO, OK, OK],
        'live.qc2': [USED, OK, NO, USED, USED],
        'my disk.qc2': [MM, OK, NO, OK, OK],
        ...readOnly,
      },
    ],
    [
      'a role that may do nothing',
      () => false,
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
      const disks = [
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
      ];
      rbac.allowed = can;
      const html = await rendered(disks);

      const rows = Object.fromEntries(
        tableRows(html).map((cells) => {
          const buttons = tooltipButtons(cells.Actions);
          expect(buttons.map((b) => b.label)).toEqual(labels);
          return [
            labelOf(cells.name),
            buttons.map((b) =>
              b.disabled ? b.tooltip.replace(/^Can't [a-z]+: /, '') : OK,
            ),
          ];
        }),
      );
      expect(rows).toEqual(want);
    },
  );
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

  it('names each disk by its path within the files directory, warning of those outside it', async () => {
    const html = await rendered(disks);

    const names = tableRows(html).map((cells) => [
      labelOf(cells.name),
      tooltipButtons(cells.name).map(({ label, tooltip }) => ({
        label,
        tooltip,
      })),
    ]);
    expect(names).toEqual([
      ['win/snap.qc2', []],
      ['win/base.qc2', []],
      [
        '/data/vms/base.qc2',
        [
          {
            label: 'Outside the standard images directory',
            tooltip: OUTSIDE_TEXT,
          },
        ],
      ],
      ['base.qc2', []],
      ['exp1/files/vm.hdd', []],
      ['my disk.qc2', []],
    ]);
  });

  it('finds and sorts disks by the names it shows', () => {
    const ctx = page();
    ctx.disks = disks;

    ctx.filterString = 'WIN/';
    expect(ctx.filteredDisks).toEqual([snap, base]);
    ctx.filterString = '/data';
    expect(ctx.filteredDisks).toEqual([outside]);

    const sorted = (isAsc) =>
      [...disks]
        .sort((a, b) => ctx.sortByLabel(a, b, isAsc))
        .map((d) => d.fullPath);
    expect(sorted(true)).toEqual([
      '/data/vms/base.qc2',
      '/phenix/images/base.qc2',
      '/phenix/images/exp1/files/vm.hdd',
      '/phenix/images/my disk.qc2',
      '/phenix/images/win/base.qc2',
      '/phenix/images/win/snap.qc2',
    ]);
    expect(sorted(false)).toEqual(sorted(true).reverse());
  });

  // the details window, up to its actions
  const details = (html) =>
    html.slice(
      html.indexOf('<div class="modal is-active"'),
      html.indexOf('>Actions</p>'),
    );

  it("shows where a disk is and links its backing chain by each image's full path", async () => {
    const html = details(
      await rendered(disks, { detailsModal: { active: true, disk: snap } }),
    );

    expect(textOf(html.match(/modal-card-title[^>]*>([^<]*)</)[1])).toBe(
      'win/snap.qc2',
    );
    expect(textOf(html)).toContain(
      'Location: The standard images directory (/phenix/images)',
    );
    // the chain names the listed images by label, with the same-named image
    // outside the files directory its own entry, and one not listed as is
    const chain = html.slice(html.indexOf('Backing Chain'));
    expect(
      [...chain.matchAll(/<(button|span)\b[^>]*>([^<]*)<\/\1>/g)].map(
        ([, tag, text]) => [tag, textOf(text)],
      ),
    ).toEqual([
      ['button', 'win/base.qc2'],
      ['button', '/data/vms/base.qc2'],
      ['span', '/data/vms/gone.qc2'],
    ]);
  });

  it.each([
    [
      'outside the files directory',
      outside,
      'Outside the standard images directory',
      [OUTSIDE_TEXT],
    ],
    [
      'in an experiment files folder',
      kept,
      'The standard images directory (/phenix/images), at a path the disk ' +
        'list leaves out',
      [],
    ],
  ])(
    'says where a read-only disk %s is',
    async (_, shown, location, warnings) => {
      const html = details(
        await rendered(disks, { detailsModal: { active: true, disk: shown } }),
      );
      const row = html.match(
        /<dt[^>]*>Location:<\/dt><dd[^>]*>([\s\S]*?)<\/dd>/,
      )[1];
      expect(textOf(row).startsWith(location)).toBe(true);
      expect(tooltipButtons(row).map((b) => b.tooltip)).toEqual(warnings);
    },
  );

  it('refuses to commit into a backing image phēnix does not act on', () => {
    const ctx = page();
    ctx.disks = disks;

    expect(ctx.actionTooltip({ name: 'commit', label: 'Commit' }, base)).toBe(
      "Can't commit: its backing image is outside the standard images directory",
    );
    const onKept = disk('/phenix/images/exp1/files/s.qc2', 'exp1/files/s.qc2', {
      backingImages: [kept.fullPath],
    });
    expect(ctx.disabledReason('commit', onKept)).toBe(
      'its backing image is at a path the disk list leaves out',
    );
    expect(ctx.disabledReason('commit', snap)).toBe(null);
  });

  it("opens a backing image's details by its full path, not a same-named image", () => {
    const ctx = page();
    ctx.disks = disks;
    ctx.detailsModal.disk = snap;

    ctx.openBacking('/data/vms/base.qc2');
    expect(ctx.detailsModal.disk).toBe(outside);
    ctx.openBacking('/phenix/images/win/base.qc2');
    expect(ctx.detailsModal.disk).toBe(base);
  });

  it('rebases only onto the other disks phēnix acts on and minimega takes, by label', async () => {
    const html = await rendered(disks, {
      detailsModal: { active: false, disk: base },
      rebaseModal: { active: true, dst: '', unsafe: false, isWaiting: false },
    });

    const select = openModal(html).match(
      /<select\b[^>]*>[\s\S]*?<\/select>/,
    )[0];
    expect(select).toMatch(/\saria-label="New backing image"/);
    expect(
      selectOptions(select).map(({ value, text }) => [text, value]),
    ).toEqual([
      ['None', ''],
      ['base.qc2', '/phenix/images/base.qc2'],
      ['win/snap.qc2', '/phenix/images/win/snap.qc2'],
    ]);
  });
});

describe('disk action requests', () => {
  // a path whose characters a query string would otherwise mangle
  const path = '/phenix/images/a+b&c=d/x%1.qc2';
  const dialog = () => ({
    startLoading: vi.fn(),
    close: vi.fn(),
    cancelLoading: vi.fn(),
  });
  const opened = () =>
    page({
      $buefy: { dialog: { prompt: vi.fn(), confirm: vi.fn() } },
      loader: { load: vi.fn() },
    });
  // the query the server gets for a request's config
  const query = (config) =>
    Object.fromEntries(
      new URL(realAxios.getUri(config), 'http://phenix.test/api/v1/')
        .searchParams,
    );

  it.each([
    ['snapshotDisk', 'prompt', 'post', 'disks/snapshot', { new: 'n+1&.qc2' }],
    ['cloneDisk', 'prompt', 'post', 'disks/clone', { new: 'n+1&.qc2' }],
    ['renameDisk', 'prompt', 'post', 'disks/rename', { new: 'n+1&.qc2' }],
    ['resizeDisk', 'prompt', 'post', 'disks/resize', { size: '+5G' }],
    ['deleteDisk', 'confirm', 'delete', 'disks', {}],
  ])(
    '%s sends the disk and its input as query parameters, intact',
    async (method, kind, verb, url, input) => {
      const ctx = opened();
      axios.request.mockResolvedValue({});

      ctx[method](path);
      const confirm = ctx.$buefy.dialog[kind].mock.lastCall[0];
      const open = dialog();
      confirm.onConfirm(Object.values(input)[0], open);

      const config = axios.request.mock.lastCall[0];
      expect(config).toEqual({
        method: verb,
        url,
        params: { disk: path, ...input },
      });
      expect(query(config)).toEqual({ disk: path, ...input });
      await flush();
      expect(open.close).toHaveBeenCalled();
      expect(ctx.loader.load).toHaveBeenCalled();
    },
  );

  it('rebases with the disk, backing image and mode as query parameters', () => {
    const ctx = opened();
    axios.request.mockResolvedValue({});

    ctx.rebaseDisk(path, '/phenix/images/b&c.qc2', true);
    expect(query(axios.request.mock.lastCall[0])).toEqual({
      disk: path,
      backing: '/phenix/images/b&c.qc2',
      unsafe: 'true',
    });
  });

  it('commits, then deletes the committed disk when asked', async () => {
    const ctx = opened();
    axios.post.mockResolvedValue({});
    axios.request.mockResolvedValue({});

    ctx.commitDisk(path, true);
    expect(axios.post).toHaveBeenCalledWith('disks/commit', null, {
      params: { disk: path },
    });
    expect(query({ url: 'disks/commit', params: { disk: path } })).toEqual({
      disk: path,
    });
    await flush();
    expect(axios.request).toHaveBeenCalledWith({
      method: 'delete',
      url: 'disks',
      params: { disk: path },
    });
  });
});
