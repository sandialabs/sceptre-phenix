// The stopped experiment page, the VM tiles, and the VM labels and mount
// browser modals.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const allowed = vi.hoisted(() => ({ fn: () => true }));
vi.mock('@/utils/rbac.js', () => ({
  roleAllowed: (...args) => allowed.fn(...args),
}));

const axios = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }));
vi.mock('@/utils/axios.js', () => ({ default: axios }));

vi.mock('@/store', () => ({
  usePhenixStore: () => ({ token: 't', features: [], role: null }),
}));

vi.mock('@/utils/websocket', () => ({
  addWsHandler: () => {},
  removeWsHandler: () => {},
}));

// the pages' lists come from the tests
const loader = await vi.hoisted(() => import('./helpers/pageLoader.js'));
vi.mock('@/utils/pageLoader.js', loader.idle);

import StoppedExperiment from '@/views/experiment/StoppedExperiment.vue';
import VMtilesView from '@/views/experiment/VMtilesView.vue';
import VMLabelsModal from '@/components/VMLabelsModal.vue';
import VMMountBrowserModal from '@/components/VMMountBrowserModal.vue';
import { flush } from './helpers/async.js';
import { makeContext } from './helpers/context.js';
import {
  renderSSR,
  stubBrowser,
  tableRows,
  textOf,
  tooltipButtons,
} from './helpers/render.js';

beforeEach(() => {
  allowed.fn = () => true;
  axios.get.mockReset();
  axios.patch.mockReset();
  stubBrowser();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('StoppedExperiment', () => {
  // the page on experiment exp1, with these VMs once it has loaded
  function stopped(vms) {
    const ctx = makeContext(StoppedExperiment, {
      $route: { params: { id: 'exp1' } },
      $buefy: { toast: { open: vi.fn() } },
    });
    if (vms) ctx.experiment = { name: 'exp1', vms };
    return ctx;
  }

  it('applies VM updates only for its own experiment', () => {
    const ctx = stopped([{ name: 'vm1', host: 'a' }]);
    const update = (exp) => ({
      resource: { type: 'experiment/vm', name: `${exp}/vm1`, action: 'update' },
      result: { name: 'vm1', host: 'b' },
    });

    ctx.handleWs(update('exp2'));
    expect(ctx.experiment.vms[0].host).toBe('a');

    ctx.handleWs(update('exp1'));
    expect(ctx.experiment.vms[0].host).toBe('b');
  });

  it('applies schedules only for its own experiment', () => {
    const ctx = stopped([{ name: 'vm1', host: 'a' }]);
    const schedule = (exp) => ({
      resource: { type: 'experiment', name: exp, action: 'schedule' },
      result: { schedule: [{ vm: 'vm1', host: 'b' }] },
    });

    ctx.handleWs(schedule('exp2'));
    expect(ctx.experiment.vms[0].host).toBe('a');

    ctx.handleWs(schedule('exp1'));
    expect(ctx.experiment.vms[0].host).toBe('b');
  });

  it('ignores publishes before the experiment has loaded', () => {
    const ctx = stopped();
    expect(() =>
      ctx.handleWs({
        resource: { type: 'experiment/vm', name: 'exp1/vm1', action: 'update' },
        result: { name: 'vm1' },
      }),
    ).not.toThrow();
    expect(ctx.$buefy.toast.open).not.toHaveBeenCalled();
  });

  it('checks file listing permission for the routed experiment', () => {
    allowed.fn = vi.fn(() => false);
    expect(stopped().canListFiles).toBe(false);
    expect(allowed.fn).toHaveBeenCalledWith(
      'experiments/files',
      'list',
      'exp1',
    );
  });

  it('sets boot for the selected VMs, shows the answer and clears the selection', async () => {
    axios.patch.mockResolvedValue({
      data: { vms: [{ name: 'vm2', dnb: true }] },
    });
    const ctx = stopped([
      { name: 'vm1', dnb: false },
      { name: 'vm2', dnb: false },
    ]);
    ctx.selectedRows = ['vm2'];

    ctx.setBoot(true);
    expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms', {
      vms: [{ name: 'vm2', dnb: true }],
      total: 1,
    });
    // rows picked one by one are cleared too, not only a whole-page pick
    expect(ctx.selectedRows).toEqual([]);

    await flush();
    expect(ctx.experiment.vms).toEqual([
      { name: 'vm1', dnb: false },
      { name: 'vm2', dnb: true },
    ]);
    expect(ctx.$buefy.toast.open).toHaveBeenCalledWith(
      expect.objectContaining({
        message: 'The selected VM was set to not boot',
      }),
    );
  });

  describe('VM disks', () => {
    // a disk as GET disks lists it: inside the files directory when given its
    // path within it, else outside it
    const disk = (fullPath, relativePath = '') => ({
      kind: 'VM',
      name: fullPath.replace(/^.*\//, ''),
      fullPath,
      relativePath,
      outsideFilesDir: !relativePath,
      readOnly: !relativePath,
    });
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
      axios.get.mockResolvedValue({ data: { disks } });
      const ctx = stopped();

      ctx.updateDisks();
      expect(axios.get).toHaveBeenCalledWith('disks', {
        params: { expName: 'exp1' },
      });
      await flush();
      expect(ctx.disks).toEqual(disks);
    });

    it("assigns the picked disk's full path, naming it safely in the confirmation", () => {
      axios.patch.mockReturnValue(new Promise(() => {}));
      const ctx = stopped([{ name: 'web', disk: '' }]);
      ctx.$buefy.dialog = { confirm: vi.fn() };
      ctx.disks = disks;
      const confirmation = () => ctx.$buefy.dialog.confirm.mock.lastCall[0];

      ctx.assignDisk('web', '/phenix/images/win/web.qc2');
      expect(confirmation().message).toBe(
        'This will assign the win/web.qc2 disk image to the web VM.',
      );
      confirmation().onConfirm();
      expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/web', {
        disk: '/phenix/images/win/web.qc2',
      });

      // names from files and topologies are text, never markup
      ctx.assignDisk('<b>vm</b>', '/data/<img src=x onerror=alert(1)>.img');
      expect(confirmation().message).toBe(
        'This will assign the /data/&lt;img src=x onerror=alert(1)&gt;.img ' +
          'disk image to the &lt;b&gt;vm&lt;/b&gt; VM.',
      );
    });

    // The Disk column of the page showing `vms`, given the disk list (left
    // unloaded when undefined): each VM's picker options or text, and the
    // warnings beside it.
    async function diskColumn(data) {
      // the page's own lists never arrive
      axios.get.mockReturnValue(new Promise(() => {}));
      const html = await renderSSR(
        StoppedExperiment,
        {},
        {
          route: '/experiment/exp1',
          data: { experiment: { name: 'exp1', vms }, ...data },
          tables: true,
        },
      );
      return Object.fromEntries(
        // the rows are in the VMs' order
        tableRows(html).map((cells, row) => {
          const { name } = vms[row];
          const select = cells.disk.match(/<select\b[\s\S]*?<\/select>/)?.[0];
          return [
            name,
            {
              shown: select
                ? [...select.matchAll(/<option\b([^>]*)>([^<]*)</g)].map(
                    ([, attrs, text]) =>
                      textOf(text) + (/\sdisabled/.test(attrs) ? ' (x)' : ''),
                  )
                : textOf(cells.disk.split('<span class="disk-warning"')[0]),
              warnings: tooltipButtons(cells.disk).map((b) => b.label),
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
      allowed.fn = (resource) => resource !== 'disks';
      expect(await diskColumn({})).toEqual({
        web: { shown: ['/phenix/images/win/web.qc2'], warnings: [] },
        out: { shown: ['/data/vms/out.img'], warnings: [] },
        gone: { shown: ['/phenix/images/gone.qc2'], warnings: [] },
      });
    });

    it("names each VM's disk to a role that may not change it", async () => {
      allowed.fn = (resource) => resource !== 'vms';
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

  it('keeps the latest searches, each once, up to its cap', () => {
    const ctx = stopped();
    ctx.searchHistory = Array.from({ length: 10 }, (_, i) => `vm-${i}`);

    ctx.searchName = ' vm-new ';
    ctx.addSearchHistory();
    expect(ctx.searchHistory).toHaveLength(10);
    expect(ctx.searchHistory).toContain('vm-new');

    ctx.searchName = 'vm-new';
    ctx.addSearchHistory();
    expect(ctx.searchHistory.filter((s) => s == 'vm-new')).toHaveLength(1);
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
    vi.useFakeTimers();
    const html = await renderSSR(
      VMtilesView,
      {},
      { route, data: { vms, searchName } },
    );
    return {
      heading: textOf(html.match(/<h3>[\s\S]*?<\/h3>/)[0]),
      tiles: [
        ...html.matchAll(
          /<p class="title"[^>]*>([^<]*)<\/p><figure[^>]*>([\s\S]*?)<\/figure>/g,
        ),
      ].map(([, name, figure]) => ({
        name: name.trim(),
        alt: textOf(figure.match(/alt="([^"]*)"/)[1]),
        console: /<a\b[^>]*href="[^"]*\/vnc\?token=t"/.test(figure),
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
  const modal = (tags = {}) =>
    makeContext(VMLabelsModal, {
      experiment: 'exp1',
      vmName: 'vm1',
      tags,
      $emit: vi.fn(),
    });

  it('saves to the experiment it was given and closes on 200', async () => {
    axios.patch.mockResolvedValue({ status: 200, statusText: '' });
    const ctx = modal();
    ctx.workingTags = [{ key: 'k', value: 'v' }];

    ctx.save();
    await flush();

    expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/vm1', {
      tag_update_mode: 'SET',
      tags: { k: 'v' },
    });
    expect(ctx.$emit).toHaveBeenCalledWith('saved');
    expect(ctx.$emit).toHaveBeenCalledWith('close');
  });

  it('closes without saving when nothing changed', () => {
    const ctx = modal({ k: 'v' });
    ctx.workingTags = [{ key: 'k', value: 'v' }];

    ctx.save();
    expect(axios.patch).not.toHaveBeenCalled();
    expect(ctx.$emit).toHaveBeenCalledWith('close');
  });
});

describe('VMMountBrowserModal', () => {
  const parts = (currentPath) => {
    const ctx = makeContext(VMMountBrowserModal, {
      targetExp: 'exp1',
      targetVm: 'vm1',
    });
    ctx.currentPath = currentPath;
    return ctx.pathParts;
  };

  it('links each crumb to its own position in the path', () => {
    expect(parts('/a/a/b')).toEqual([
      { part: 'mnt', upTo: '/' },
      { part: 'a', upTo: '/a' },
      { part: 'a', upTo: '/a/a' },
      { part: 'b', upTo: '/a/a/b' },
    ]);
  });

  it('shows only the mount at its root', () => {
    expect(parts('/')).toEqual([{ part: 'mnt', upTo: '/' }]);
  });
});
