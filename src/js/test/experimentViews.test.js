// The stopped experiment page, the VM tiles, and the VM labels and mount
// browser modals.
import { flushPromises } from '@vue/test-utils';
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const {
  axios,
  capturing,
  loaderSpy,
  modules,
  noticeText,
  notices,
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
// the pages' lists come from the tests
vi.mock('@/utils/pageLoader.js', capturing);

import DiskSelect from '@/components/experiment/DiskSelect.vue';
import StoppedExperiment from '@/views/experiment/StoppedExperiment.vue';
import VMtilesView from '@/views/experiment/VMtilesView.vue';
import VMLabelsModal from '@/components/VMLabelsModal.vue';
import VMMountBrowserModal from '@/components/VMMountBrowserModal.vue';
import { never } from './helpers/async.js';
import { pathSegment } from './helpers/arbitraries.js';
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
  tooltipPositions,
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

  // Persistence is the last column, at the window's right edge
  it('opens the Persistence tooltip to the left, inside the window', async () => {
    const page = await stopped([{ name: 'vm1', snapshot: true }]);
    expect(tooltipPositions(tableRows(page)[0].Persistence)).toEqual([
      'is-left',
    ]);
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

  it('sums up the VMs the server left unchanged in one notice', async () => {
    axios.patch.mockResolvedValue({
      data: {
        vms: [{ name: 'vm1', dnb: true }],
        errors: [
          {
            name: 'vm2',
            status: 'skipped',
            error: 'you do not have permission',
          },
          { name: 'vm3', status: 'failed', error: 'minimega says no' },
        ],
      },
    });
    const page = await stopped([
      { name: 'vm1', dnb: false },
      { name: 'vm2', dnb: false },
      { name: 'vm3', dnb: false },
    ]);
    for (const box of page.findAll('td input[type="checkbox"]')) {
      await box.setValue(true);
    }

    await byLabel(page, 'Set selected VMs to not boot').trigger('click');
    await flushPromises();

    expect(toasts()).toEqual([]);
    expect(notices.open).toHaveBeenCalledTimes(1);
    expect(noticeText()).toContain('Updated 1 of 3 VMs: 1 failed, 1 skipped');
    expect(noticeText()).toContain('vm3: minimega says no');
    expect(noticeText()).toContain('vm2: you do not have permission');
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

  describe('search history', () => {
    // the page with this history, and its history once a search finds VMs
    async function searching(searchHistory) {
      const page = await stopped(undefined, { searchHistory });
      const search = async (text) => {
        await page.find('.autocomplete input').setValue(text);
        // the test answers the search, so drop the page's debounced load
        page.vm.searchVMs.cancel();
        loaderSpy.options.apply({
          name: 'exp1',
          vms: [{ name: 'vm-new' }],
          vm_count: 1,
          vlans: [],
        });
        await flushPromises();
        return page.findComponent({ name: 'BAutocomplete' }).props('data');
      };
      return { page, search };
    }
    const term = fc.constantFrom(
      ...Array.from({ length: 13 }, (_, i) => `vm-${i}`),
    );

    it('keeps each search once, adding a new one, up to its cap', async () => {
      const searches = fc.array(
        fc.oneof(
          term,
          term.map((t) => ` ${t} `),
          fc.constantFrom('', 'ab'),
        ),
        { minLength: 1, maxLength: 4 },
      );
      await fc.assert(
        fc.asyncProperty(
          // full or nearly so, now and then
          fc.oneof(
            fc.uniqueArray(term, { maxLength: 10 }),
            fc.uniqueArray(term, { minLength: 9, maxLength: 10 }),
          ),
          searches,
          async (before, typed) => {
            const { page, search } = await searching(before.toSorted());
            // the page changes its history in place, so keep copies
            let last = [...before];
            for (const text of typed) {
              const history = await search(text);
              const kept = text.trim();
              expect(new Set(history).size).toBe(history.length);
              expect(history.length).toBeLessThanOrEqual(10);
              // a search it does not add drops nothing
              expect(history.length).toBeGreaterThanOrEqual(last.length);
              if (kept.length >= 4 && !last.includes(kept)) {
                expect(history).toContain(kept);
                expect(history).toHaveLength(Math.min(last.length + 1, 10));
              }
              last = [...history];
            }
            page.unmount();
          },
        ),
        { numRuns: 20 },
      );
    });

    it('keeps a full history whole on a search it does not add', async () => {
      const full = Array.from({ length: 10 }, (_, i) => `vm-${i}`);
      const { search } = await searching(full);
      for (const text of ['', 'ab', 'vm-9', 'vm-0']) {
        expect(await search(text)).toEqual(full);
      }
    });

    it('drops the oldest search from a full history, not the last by name', async () => {
      // oldest first
      const full = ['zzzz', ...Array.from({ length: 9 }, (_, i) => `vm-${i}`)];
      const { search } = await searching(full);
      expect(await search('aaaa')).toEqual(
        ['aaaa', ...full.slice(1)].toSorted(),
      );
    });
  });

  describe('debounced search', () => {
    afterEach(() => vi.useRealTimers());

    it('loads nothing for a search typed just before the page closes', async () => {
      const page = await stopped();
      vi.useFakeTimers();
      await page.find('.autocomplete input').setValue('vm-1');
      page.unmount();
      loaderSpy.load.mockClear();
      await vi.runAllTimersAsync();
      expect(loaderSpy.load).not.toHaveBeenCalled();
    });

    it('searches on each open page, not only the last one typed in', async () => {
      const pages = [await stopped(), await stopped()];
      vi.useFakeTimers();
      loaderSpy.load.mockClear();
      for (const page of pages) {
        await page.find('.autocomplete input').setValue('vm-1');
      }
      await vi.runAllTimersAsync();
      expect(loaderSpy.load).toHaveBeenCalledTimes(2);
    });
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

  it("finds the routed experiment's VMs by any part of their names, in any case, sorted", async () => {
    const name = fc.stringMatching(/^[a-cA-C1[(.éÉ]{1,4}$/);
    const others = fc.uniqueArray(name, { maxLength: 6 });
    const vmsAndSearch = fc
      .tuple(others, others)
      .chain(([mine, theirs]) =>
        fc.tuple(
          fc.constant(mine),
          fc.constant(theirs),
          fc.oneof(
            fc.stringMatching(/^[a-cA-C1[(.éÉ]{0,2}$/),
            ...mine.map((n) => fc.nat(n.length).map((i) => n.slice(i, i + 2))),
          ),
        ),
      );
    await fc.assert(
      fc.asyncProperty(vmsAndSearch, async ([mine, theirs, search]) => {
        const page = await mountComponent(VMtilesView, {
          route: '/experiment/exp1',
          data: {
            vms: [
              ...mine.map((n) => ({ experiment: 'exp1', name: n })),
              ...theirs.map((n) => ({ experiment: 'exp2', name: n })),
            ],
          },
        });
        await page.find('.autocomplete input').setValue(search);
        const lower = (n) => n.toLowerCase();
        const want = mine
          .filter((n) => lower(n).includes(lower(search)))
          .sort((a, b) => (lower(a) > lower(b)) - (lower(a) < lower(b)));
        expect(page.findAll('.tile.is-child p.title').map(textOf)).toEqual(
          want,
        );
        page.unmount();
      }),
      { numRuns: 20 },
    );
  });

  it("finds every experiment's VMs by the exp/vm on their tiles", async () => {
    expect((await tiles('/vmtiles', 'exp1/')).tiles.map((t) => t.name)).toEqual(
      ['exp1/vm2', 'exp1/vm[1]'],
    );
    expect((await tiles('/vmtiles', 'exp1_')).tiles).toEqual([]);
  });

  it("offers as matches the names on every experiment's tiles the search finds", async () => {
    const name = fc.stringMatching(/^[ab_/]{1,3}$/);
    await fc.assert(
      fc.asyncProperty(
        fc.uniqueArray(fc.tuple(fc.constantFrom('e', 'e_a'), name), {
          maxLength: 6,
          selector: ([e, n]) => `${e}/${n}`,
        }),
        fc.stringMatching(/^[eab_/]{0,3}$/),
        async (pairs, search) => {
          const page = await mountComponent(VMtilesView, {
            route: '/vmtiles',
            data: {
              vms: pairs.map(([experiment, name]) => ({ experiment, name })),
            },
          });
          await page.find('.autocomplete input').setValue(search);
          const shown = page.findAll('.tile.is-child p.title').map(textOf);
          expect(shown.toSorted()).toEqual(
            pairs
              .map(([e, n]) => `${e}/${n}`)
              .filter((full) => full.includes(search))
              .toSorted(),
          );
          expect(
            page.findComponent({ name: 'BAutocomplete' }).props('data'),
          ).toEqual(shown);
          page.unmount();
        },
      ),
      { numRuns: 20 },
    );
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
  // a note, which a save keeps as it is
  const NOTE = { __notes_1: 'n' };

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

  it('opens a VM with no labels or notes on a blank one of each, and saves neither', async () => {
    const labels = await mountComponent(VMLabelsModal, {
      props: { experiment: 'exp1', vmName: 'vm1', tags: {} },
    });
    expect(labels.findAll('textarea')).toHaveLength(1);
    expect(labels.findAll('table').at(1).findAll('td input')).toHaveLength(2);

    await button(labels, 'Save').trigger('click');
    expect(axios.patch).not.toHaveBeenCalled();
    expect(labels.emitted('close')).toEqual([[]]);
  });

  it('drops a note cleared of its text', async () => {
    axios.patch.mockResolvedValue({ status: 200, statusText: '' });
    const blank = fc
      .array(fc.constantFrom(' ', '\t', '\n'), { maxLength: 4 })
      .map((chars) => chars.join(''));
    await fc.assert(
      fc.asyncProperty(blank, async (text) => {
        axios.patch.mockClear();
        const labels = await modal({ old: 'x' });
        await labels.find('textarea').setValue(text);
        await button(labels, 'Save').trigger('click');
        expect(axios.patch).toHaveBeenCalledWith('experiments/exp1/vms/vm1', {
          tag_update_mode: 'SET',
          tags: { old: 'x' },
        });
        labels.unmount();
      }),
      { numRuns: 20 },
    );
  });

  it('sizes each note to its text as it opens, and as it is typed', async () => {
    // happy-dom lays out nothing: give each line of a note 20px
    vi.spyOn(
      HTMLTextAreaElement.prototype,
      'scrollHeight',
      'get',
    ).mockImplementation(function () {
      return this.value.split('\n').length * 20;
    });
    const labels = await modal({ __notes_2: 'a\nb\nc' });
    await flushPromises();
    const heights = () =>
      labels.findAll('textarea').map((t) => t.element.style.height);
    expect(heights()).toEqual(['20px', '60px']);

    await labels.findAll('textarea').at(0).setValue('a\nb');
    await flushPromises();
    expect(heights()).toEqual(['40px', '60px']);
  });

  it.each([
    ['a label', { k: 'v' }],
    ['a hidden tag', { k: 'v', __sohStyle: 'fill: red' }],
  ])(
    'closes without saving when nothing changed on a VM with %s',
    async (_, tags) => {
      const labels = await modal(tags);

      await button(labels, 'Save').trigger('click');
      expect(axios.patch).not.toHaveBeenCalled();
      expect(labels.emitted('close')).toEqual([[]]);
    },
  );

  it('keeps the hidden tags it does not show, such as a node style', async () => {
    axios.patch.mockResolvedValue({ status: 200, statusText: '' });
    const hiddenKey = fc
      .string({ minLength: 1, maxLength: 8 })
      .map((s) => '__' + s)
      .filter((k) => !k.startsWith('__notes_') && k !== '__proto__');
    await fc.assert(
      fc.asyncProperty(
        fc.dictionary(hiddenKey, fc.string(), { maxKeys: 3 }),
        async (hidden) => {
          axios.patch.mockClear();
          const labels = await modal({ old: 'x', ...hidden });
          await labels.findAll('table').at(1).find('td input').setValue('new');
          await button(labels, 'Save').trigger('click');
          expect(axios.patch.mock.lastCall[1].tags).toEqual({
            new: 'x',
            ...NOTE,
            ...hidden,
          });
          labels.unmount();
        },
      ),
      { numRuns: 20 },
    );
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

  it('names each part of any path in a crumb that opens the path up to it', async () => {
    const segment = pathSegment.filter((s) => !/\s/.test(s));
    await fc.assert(
      fc.asyncProperty(
        fc.array(segment, { minLength: 1, maxLength: 4 }),
        fc.nat(),
        async (parts, n) => {
          const wrapper = await browser('/' + parts.join('/'));
          expect(crumbs(wrapper).map(textOf)).toEqual(['mnt', ...parts]);
          const i = n % parts.length;
          await crumbs(wrapper).at(i).find('a').trigger('click');
          expect(axios.get.mock.lastCall[1].params.path).toBe(
            '/' + parts.slice(0, i).join('/'),
          );
          wrapper.unmount();
        },
      ),
      { numRuns: 20 },
    );
  });

  it('shows only the mount at its root', async () => {
    expect(crumbs(await browser('/')).map(textOf)).toEqual(['mnt']);
  });
});
