// Mounts the VM details card and the Actions buttons with real Buefy.
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';
import { nextTick } from 'vue';

const { axios, modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
const { FakeRfb, rfbs, resetRfbs } = await vi.hoisted(
  () => import('./helpers/fakeRfb.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/vnc/loadRfb.js', () => ({ loadRfb: async () => FakeRfb }));

import { icon, library } from '@fortawesome/fontawesome-svg-core';

import { faSharkFin } from '@/utils/icons.js';
import VmDetailsCard from '@/components/experiment/VmDetailsCard.vue';
import VmRowActions from '@/components/experiment/VmRowActions.vue';
import { CHORD_HINT } from '@/vnc/chord.js';
import { sharedLiveTilePool } from '@/vnc/liveTilePool.js';
import { never } from './helpers/async.js';
import { componentDeclarations, contrast } from './helpers/css.js';
import { disk, vm as makeVm } from './helpers/fixtures.js';
import { allowAll, denyOnly } from './helpers/permissions.js';
import {
  byLabel,
  mountComponent,
  textOf,
  tooltipButtons,
} from './helpers/mount.js';

// web, capturing on its second interface
const vm = makeVm({ captures: [{ vm: 'web', interface: 1 }] });

const OUTSIDE = 'Outside the standard images directory';

// whether `wrapper` holds an element `selector` matches
const has = (wrapper, selector) => wrapper.find(selector).exists();

beforeEach(() => {
  resetMocks({ store: {} });
  resetRfbs();
  // the live console of a running VM waits for its session
  axios.get.mockImplementation(never);
});

afterEach(() => {
  sharedLiveTilePool().dispose();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const card = (props) =>
  mountComponent(VmDetailsCard, {
    props: { vm, fullName: 'demo/web', experiment: 'demo', ...props },
  });

// the section whose title starts with `title`
const section = (wrapper, title) =>
  wrapper
    .findAll('.vm-section')
    .find((s) => textOf(s.find('.vm-section-title')).startsWith(title));

// the value of a row of the storage section, by its key
const storageValue = (wrapper, key) =>
  section(wrapper, 'Storage')
    .findAll('.vm-kv')
    .find((kv) => textOf(kv.find('.vm-kv-key')) === key)
    ?.find('.vm-kv-value');

// the tooltips of the outside-the-files-directory warnings in `wrapper`
const outsideWarnings = (wrapper) =>
  wrapper
    .findAllComponents({ name: 'DiskWarning' })
    .flatMap(tooltipButtons)
    .filter((b) => b.label === OUTSIDE)
    .map((b) => b.tooltip);

const OUTSIDE_TEXT =
  /^Outside the standard images directory \(\/phenix\/images\)\./;

describe('VmDetailsCard storage', () => {
  it('leaves out the CD-ROM when none is inserted', async () => {
    const wrapper = await card();
    expect(storageValue(wrapper, 'CD-ROM')).toBeUndefined();
    expect(has(wrapper, '.vm-cdrom')).toBe(false);
  });

  it('shows the inserted CD-ROM', async () => {
    const wrapper = await card({
      vm: { ...vm, cdRom: '/phenix/images/tools.iso' },
    });
    expect(textOf(storageValue(wrapper, 'CD-ROM'))).toBe('tools.iso');
  });

  it('names the CD-ROM by its label, warning of one outside the files directory', async () => {
    const inside = disk('/phenix/images/isos/tools.iso', 'isos/tools.iso', {
      kind: 'ISO',
    });
    let wrapper = await card({
      vm: { ...vm, cdRom: inside.fullPath },
      cdRomDisk: inside,
      filesDir: '/phenix/images',
    });
    const value = storageValue(wrapper, 'CD-ROM');
    expect(textOf(value)).toBe('isos/tools.iso');
    expect(value.attributes('title')).toBe('/phenix/images/isos/tools.iso');
    expect(outsideWarnings(wrapper.find('.vm-cdrom'))).toEqual([]);

    const outside = disk('/data/isos/tools.iso', '', { kind: 'ISO' });
    wrapper = await card({
      vm: { ...vm, cdRom: outside.fullPath },
      cdRomDisk: outside,
      filesDir: '/phenix/images',
    });
    expect(textOf(storageValue(wrapper, 'CD-ROM'))).toBe(
      '/data/isos/tools.iso',
    );
    expect(outsideWarnings(wrapper.find('.vm-cdrom'))).toEqual([
      expect.stringMatching(OUTSIDE_TEXT),
    ]);
  });

  it("shows the disk's backing chain under it, nearest first", async () => {
    const wrapper = await card({
      backingChain: [
        { label: 'jammy_hardened.qc2', fullPath: '' },
        { label: 'jammy.qc2', fullPath: '' },
      ],
    });
    const chain = wrapper.find('.vm-backing-chain');
    const above = chain.element.previousElementSibling;
    expect(textOf(above.querySelector('.vm-kv-key'))).toBe('Disk');
    expect(textOf(above.querySelector('.vm-kv-value'))).toBe('web_snap.qc2');
    expect(chain.findAll('.vm-chain-name').map(textOf)).toEqual([
      'jammy_hardened.qc2',
      'jammy.qc2',
    ]);
    expect(chain.findAll('.vm-chain-arrow').map(textOf)).toEqual(['↓', '↓']);
  });

  it('names the disk by its label, warning of one outside the files directory', async () => {
    const inside = disk('/phenix/images/web/web_snap.qc2', 'web/web_snap.qc2');
    let wrapper = await card({
      vm: { ...vm, disk: inside.fullPath },
      disk: inside,
      filesDir: '/phenix/images',
    });
    expect(textOf(storageValue(wrapper, 'Disk'))).toBe('web/web_snap.qc2');
    expect(outsideWarnings(wrapper)).toEqual([]);

    const outside = disk('/data/vms/web.img');
    wrapper = await card({
      vm: { ...vm, disk: outside.fullPath },
      disk: outside,
      filesDir: '/phenix/images',
    });
    expect(textOf(storageValue(wrapper, 'Disk'))).toBe('/data/vms/web.img');
    expect(outsideWarnings(wrapper)).toEqual([
      expect.stringMatching(OUTSIDE_TEXT),
    ]);
  });

  it('links the disk, its backing images and the CD-ROM to their cards on the Disks page', async () => {
    const own = disk('/phenix/images/web_snap.qc2', 'web_snap.qc2');
    const iso = disk('/phenix/images/tools.iso', 'tools.iso', { kind: 'ISO' });
    const props = {
      vm: { ...vm, disk: own.fullPath, cdRom: iso.fullPath },
      disk: own,
      cdRomDisk: iso,
      backingChain: [
        { label: 'jammy.qc2', fullPath: '/phenix/images/jammy.qc2' },
        { label: '/gone/base.qc2', fullPath: '' },
      ],
    };
    const links = (wrapper) =>
      section(wrapper, 'Storage')
        .findAll('a.vm-disk-link')
        .map((a) => [textOf(a), a.attributes('href')]);
    const href = (path) => `/disks/?disk=${path}`;

    expect(links(await card(props))).toEqual([
      ['web_snap.qc2', href(own.fullPath)],
      ['jammy.qc2', href('/phenix/images/jammy.qc2')],
      ['tools.iso', href(iso.fullPath)],
    ]);
    // the image the list does not hold is text alone
    const wrapper = await card(props);
    expect(
      wrapper.findAll('.vm-chain-name').map((n) => n.element.tagName),
    ).toEqual(['A', 'SPAN']);

    rbac.allowed = denyOnly(['disks', 'list']);
    expect(links(await card(props))).toEqual([]);
  });

  it('names the disk by file name while the disk list is unknown', async () => {
    expect(textOf(storageValue(await card(), 'Disk'))).toBe('web_snap.qc2');
  });

  it('says None for a VM without a disk', async () => {
    const wrapper = await card({ vm: { ...vm, disk: '' } });
    expect(textOf(storageValue(wrapper, 'Disk'))).toBe('None');
  });

  it('shows the disk alone when it has no backing image', async () => {
    for (const backingChain of [[], null]) {
      const wrapper = await card({ backingChain });
      expect(has(wrapper, '.vm-backing-chain')).toBe(false);
    }
  });
});

describe('VmDetailsCard capture buttons', () => {
  it('start with the capture icon and stop with the stop icon', async () => {
    const wrapper = await card();
    expect(has(wrapper, '.vm-capture-start [data-icon="shark-fin"]')).toBe(
      true,
    );
    expect(has(wrapper, '.vm-capture-stop [data-icon="stop"]')).toBe(true);
  });

  it('shows a spinner on the interface waiting on the server', async () => {
    const wrapper = await card({ capturePending: [0] });
    expect(
      wrapper.findAll('.vm-capture').map((b) => b.classes('is-loading')),
    ).toEqual([true, false]);
  });
});

describe('VmDetailsCard Open in WebShark', () => {
  it('opens each running capture on the WebShark page', async () => {
    const wrapper = await card({ features: ['webshark'] });
    const links = wrapper.findAll('.vm-webshark');
    // only interface 1 is capturing
    expect(links).toHaveLength(1);
    expect(links[0].element.tagName).toBe('A');
    expect(links[0].attributes('href')).toBe(
      '/packets?exp=demo&vm=web&iface=1',
    );
    expect(links[0].attributes('aria-label')).toBe('Open in WebShark');
    // not the capture icon, which starts captures
    expect(has(links[0], '[data-icon="search"]')).toBe(true);
  });

  it('needs WebShark installed', async () => {
    expect((await card()).findAll('.vm-webshark')).toEqual([]);
  });
});

// the Network box's buttons, with their tooltips
const networkButtons = (wrapper) =>
  tooltipButtons(
    section(wrapper, 'Network').find('.vm-section-title .vm-section-actions'),
  );
const networkButton = (wrapper, name) =>
  networkButtons(wrapper).find((b) => b.control.classes(`vm-${name}`));

describe('VmDetailsCard Network buttons', () => {
  const twoCaptured = [
    { vm: 'web', interface: 0 },
    { vm: 'web', interface: 1 },
  ];

  it('start captures on every interface and stop them all', async () => {
    const [start, stop] = networkButtons(await card());
    expect(start.tooltip).toBe(
      'Start a packet capture on every connected interface of the VM',
    );
    expect(start.control.classes()).toEqual(
      expect.arrayContaining(['vm-captureAll', 'is-success']),
    );
    expect(start.disabled).toBe(false);
    expect(has(start.control, '[data-icon="play"]')).toBe(true);
    expect(stop.tooltip).toBe('Stop every packet capture running on the VM');
    expect(stop.control.classes()).toEqual(
      expect.arrayContaining(['vm-stopCaptures', 'is-danger']),
    );
    expect(stop.disabled).toBe(false);
    expect(has(stop.control, '[data-icon="stop"]')).toBe(true);
  });

  it('disable stop, saying why, when nothing is captured', async () => {
    const wrapper = await card({ vm: { ...vm, captures: [] } });
    const stop = networkButton(wrapper, 'stopCaptures');
    expect(stop.disabled).toBe(true);
    expect(stop.tooltip).toBe(
      'Cannot stop all packet captures: no packet capture is running',
    );
    expect(networkButton(wrapper, 'captureAll').disabled).toBe(false);
  });

  it('disable start, saying why, when every interface is captured', async () => {
    const wrapper = await card({ vm: { ...vm, captures: twoCaptured } });
    const start = networkButton(wrapper, 'captureAll');
    expect(start.disabled).toBe(true);
    expect(start.tooltip).toBe(
      'Cannot capture all interfaces: phēnix already captures every connected interface',
    );
    expect(networkButton(wrapper, 'stopCaptures').disabled).toBe(false);
  });

  it('spin the one waiting on the server and hold the other', async () => {
    const wrapper = await card({
      vm: { ...vm, captures: [] },
      captureAllPending: true,
    });
    const start = networkButton(wrapper, 'captureAll');
    const stop = networkButton(wrapper, 'stopCaptures');
    expect(start.control.classes('is-loading')).toBe(true);
    expect(stop.control.classes('is-loading')).toBe(false);
    expect(start.disabled).toBe(true);
    expect(stop.disabled).toBe(true);
  });

  it("link to the WebShark tab on the VM's running capture", async () => {
    const tab = networkButton(
      await card({ features: ['webshark'] }),
      'webshark-tab',
    );
    expect(tab.tooltip).toBe(
      "Open this VM's running capture in the WebShark tab",
    );
    expect(tab.control.attributes('href')).toBe(
      '/packets?exp=demo&vm=web&iface=1',
    );
  });

  it('link to the first of several running captures', async () => {
    const wrapper = await card({
      vm: { ...vm, captures: [...twoCaptured].reverse() },
      features: ['webshark'],
    });
    const tab = networkButton(wrapper, 'webshark-tab');
    expect(tab.tooltip).toBe(
      "Open this VM's capture of interface 0 in the WebShark tab",
    );
    expect(tab.control.attributes('href')).toMatch(/iface=0$/);
  });

  it("link to the experiment's captures when none is running", async () => {
    const wrapper = await card({
      vm: { ...vm, captures: [] },
      features: ['webshark'],
    });
    const tab = networkButton(wrapper, 'webshark-tab');
    expect(tab.tooltip).toBe(
      "Open the WebShark tab on this experiment's captures",
    );
    expect(tab.control.attributes('href')).toBe('/packets?exp=demo');
  });

  it('link to the WebShark tab only where it is installed', async () => {
    expect(networkButton(await card(), 'webshark-tab')).toBeUndefined();
  });
});

describe('VmDetailsCard annotations', () => {
  const annotations = (wrapper) => wrapper.find('.vm-annotations');

  it("wait for the VM's own details, which carry them", async () => {
    expect(annotations(await card()).exists()).toBe(false);
    const wrapper = await card({ vm: { ...vm, annotations: null } });
    expect(annotations(wrapper).exists()).toBe(false);
  });

  it('list each one by key, hiding passwords', async () => {
    const wrapper = await card({
      vm: {
        ...vm,
        annotations: {
          vncBanner: 'Authorized use only',
          'vrouter/vyos-password': 'hunter2',
          'phenix/startup-autotunnel': ['8080', '2222:22'],
          'phenix/default-apps': false,
        },
      },
    });
    expect(
      annotations(wrapper)
        .findAll('.vm-annotation-key')
        .map((key) => [textOf(key), textOf(key.element.nextElementSibling)]),
    ).toEqual([
      ['phenix/default-apps', 'false'],
      ['phenix/startup-autotunnel', '["8080","2222:22"]'],
      ['vncBanner', 'Authorized use only'],
      ['vrouter/vyos-password', '••••••'],
    ]);
    expect(wrapper.html()).not.toContain('hunter2');
    expect(byLabel(wrapper, 'Show vrouter/vyos-password').exists()).toBe(true);
  });

  it('say when there are none', async () => {
    const wrapper = await card({ vm: { ...vm, annotations: {} } });
    expect(textOf(annotations(wrapper))).toContain('No annotations');
  });

  it('edit as rows and save the whole set, keeping unchanged values as they were', async () => {
    const wrapper = await card({
      vm: {
        ...vm,
        annotations: {
          'phenix/startup-autotunnel': ['8080', '2222:22'],
          'phenix/default-apps': 'no',
          retries: 3,
        },
      },
    });
    await wrapper.find('.vm-edit-annotations').trigger('click');
    const rows = wrapper.findAll('.annotation-row');
    expect(
      rows.map((row) => {
        const key = row.find('.annotation-key');
        return [
          key.element.localName === 'code'
            ? textOf(key)
            : key.find('input').element.value,
          row.classes()[1].replace('annotation-', ''),
          row.find('.annotation-value input').element.value,
        ];
      }),
    ).toEqual([
      // not a boolean, so a custom row that can be fixed
      ['phenix/default-apps', 'text', 'no'],
      ['phenix/startup-autotunnel', 'list', '8080, 2222:22'],
      ['retries', 'text', '3'],
    ]);

    await rows[1].find('.annotation-value input').setValue('8080');
    const save = () => wrapper.find('.vm-save-annotations');
    await save().trigger('click');
    expect(save().classes('is-loading')).toBe(true);
    const [[payload, done]] = wrapper.emitted('save-annotations');
    expect(payload).toEqual({
      'phenix/default-apps': 'no',
      'phenix/startup-autotunnel': ['8080'],
      retries: 3,
    });

    done(false);
    await nextTick();
    expect(save().classes('is-loading')).toBe(false);
    expect(has(wrapper, '.annotation-list')).toBe(true);
    done(true);
    await nextTick();
    expect(has(wrapper, '.annotation-list')).toBe(false);
  });
});

describe('VmDetailsCard annotations, any set of them', () => {
  // the keys phenix knows, with values of their own type or not, and others
  const text = fc
    .string({ minLength: 1, maxLength: 8 })
    .filter((v) => v.trim());
  const value = fc.oneof(
    text,
    fc.boolean(),
    fc.integer(),
    fc.array(text, { minLength: 1, maxLength: 3 }),
    fc.dictionary(fc.stringMatching(/^[a-z]{1,3}$/), text, { maxKeys: 2 }),
  );
  const key = fc.oneof(
    fc.constantFrom(
      'phenix/default-apps',
      'phenix/startup-autotunnel',
      'phenix/startup-via-cc',
      'vrouter/vyos-password',
      'vrouter/enable-ssh',
    ),
    fc.stringMatching(/^[a-zA-Z][a-zA-Z0-9./_-]{0,10}$/),
  );
  const annotationSet = fc.dictionary(key, value, { minKeys: 1, maxKeys: 5 });

  it('list each one once, by key, and save unchanged as they were', async () => {
    await fc.assert(
      fc.asyncProperty(annotationSet, async (set) => {
        const wrapper = await card({ vm: { ...vm, annotations: set } });
        const listed = wrapper.findAll('.vm-annotation-key').map((k) => {
          const shown = k.element.nextElementSibling;
          return [
            k.attributes('title'),
            shown.getAttribute('title') ?? textOf(shown),
          ];
        });
        const keys = listed.map(([k]) => k);
        expect(keys.toSorted((a, b) => a.localeCompare(b))).toEqual(keys);
        expect(keys.toSorted()).toEqual(Object.keys(set).sort());
        for (const [k, shown] of listed) {
          if (k === 'vrouter/vyos-password') expect(shown).toBe('••••••');
          else if (typeof set[k] === 'string') expect(shown).toBe(set[k]);
          else expect(JSON.parse(shown)).toEqual(set[k]);
        }

        await wrapper.find('.vm-edit-annotations').trigger('click');
        await wrapper.find('.vm-save-annotations').trigger('click');
        expect(wrapper.emitted('save-annotations')[0][0]).toEqual(set);
        wrapper.unmount();
      }),
      { numRuns: 12 },
    );
  });
});

describe('VmDetailsCard header', () => {
  it('puts the description between the name and the links', async () => {
    const wrapper = await card({
      vm: { ...vm, description: 'Public web server' },
    });
    const header = wrapper.find('header');
    expect([...header.element.children].map((el) => el.classList[0])).toEqual([
      'vm-card-heading',
      'vm-card-description',
      'vm-card-links',
    ]);
    expect(textOf(header.find('.vm-card-description'))).toBe(
      'Public web server',
    );
    expect(header.classes()).toContain('has-description');
    expect(textOf(wrapper.find('.vm-card-body'))).not.toContain(
      'Public web server',
    );
  });

  it('leaves out the description box without one', async () => {
    const wrapper = await card();
    expect(has(wrapper, '.vm-card-description')).toBe(false);
    expect(wrapper.find('header').classes()).not.toContain('has-description');
  });

  it("links to the experiment's SOH and SCORCH pages, named in full", async () => {
    const wrapper = await card();
    const links = tooltipButtons(wrapper.find('.vm-card-links'))
      .slice(0, 2)
      .map(({ tooltip, label, control }) => ({
        tooltip,
        label,
        href: control.attributes('href'),
      }));
    expect(links).toEqual([
      {
        tooltip: 'view state of health',
        label: 'View state of health for experiment demo',
        href: '/soh/demo',
      },
      {
        tooltip: 'view SCORCH runs',
        label: 'View SCORCH runs for experiment demo',
        href: '/scorch/demo',
      },
    ]);
  });

  it('links to the VM docs and the Disks page', async () => {
    const wrapper = await card();
    expect(wrapper.find('.vm-card-docs').attributes('href')).toBe(
      'https://phenix.sceptre.dev/latest/vms/',
    );
    expect(wrapper.find('.vm-card-disks').attributes('href')).toBe('/disks/');
  });

  it('puts the miniccc agent first among the stats', async () => {
    const wrapper = await card();
    expect(wrapper.findAll('.vm-stat-label').map(textOf)).toEqual([
      'miniccc agent',
      'vCPU',
      'Memory',
      'Interfaces',
    ]);
  });
});

describe('VmDetailsCard permissions', () => {
  // [what a role loses, the permission it lacks, the card's props, the
  // selectors of what shows that part of the card, a part the role keeps]
  it.each([
    [
      'the WebShark links',
      ['vms/captures', 'list'],
      { features: ['webshark'] },
      ['.vm-webshark'],
      null,
    ],
    [
      'Capture all interfaces',
      ['vms/captures', 'create'],
      {},
      ['.vm-captureAll'],
      '.vm-stopCaptures',
    ],
    [
      'annotation editing',
      ['vms', 'patch'],
      { vm: makeVm({ annotations: {} }) },
      ['.vm-edit-annotations'],
      '.vm-annotations',
    ],
    [
      'the SOH and SCORCH links',
      ['experiments', 'get'],
      {},
      [
        '[aria-label="View state of health for experiment demo"]',
        '[aria-label="View SCORCH runs for experiment demo"]',
      ],
      '.vm-card-docs',
    ],
    ['the Disks link', ['disks'], {}, ['.vm-card-disks'], '.vm-card-docs'],
  ])(
    'hides %s from a role without %j',
    async (_, permission, props, shown, kept) => {
      rbac.allowed = allowAll;
      const allowed = await card(props);
      for (const part of shown) expect(has(allowed, part)).toBe(true);

      rbac.allowed = denyOnly(permission);
      const denied = await card(props);
      for (const part of shown) expect(has(denied, part)).toBe(false);
      if (kept) expect(has(denied, kept)).toBe(true);
    },
  );
});

describe('VmDetailsCard Screenshot', () => {
  // the action button with this class, its icon, tooltip and disabled state
  const action = (wrapper, name) => {
    const control = wrapper.find(`.vm-action-${name}`);
    const tooltip = wrapper
      .findAllComponents({ name: 'BTooltip' })
      .find((t) => t.find(`.vm-action-${name}`).exists());
    return {
      control,
      icon: control.find('[data-icon]').attributes('data-icon'),
      tooltip: textOf(tooltip.vm.$refs.content),
      disabled: control.attributes('aria-disabled') === 'true',
    };
  };

  it('saves a screenshot with the camera icon, next to the other actions', async () => {
    const wrapper = await card();
    const button = action(wrapper, 'screenshot');
    expect(textOf(button.control)).toBe('Screenshot');
    expect(button).toMatchObject({
      icon: 'camera',
      disabled: false,
      tooltip:
        "Save a full-size PNG of the VM's screen in the experiment files, in the screenshots folder",
    });
    expect(button.control.element.closest('.vm-tool-buttons')).not.toBeNull();
    await button.control.trigger('click');
    expect(wrapper.emitted('action')).toEqual([['screenshot']]);
  });

  it('gives the disk snapshot button and the snapshot list the clone icon', async () => {
    const wrapper = await card({ snapshots: ['before'] });
    expect(action(wrapper, 'snapshot').icon).toBe('clone');
    const row = section(wrapper, 'Snapshots').find('.vm-list-row');
    expect(row.find('[data-icon]').attributes('data-icon')).toBe('clone');
  });

  it('is greyed out, saying why, for a VM that is not running', async () => {
    const wrapper = await card({
      vm: makeVm({ state: 'QUIT', running: false }),
    });
    const button = action(wrapper, 'screenshot');
    expect(button).toMatchObject({
      disabled: true,
      tooltip: 'Cannot save a screenshot: the VM is not running',
    });
    await button.control.trigger('click');
    expect(wrapper.emitted('action')).toBeUndefined();
  });

  it.each([
    [allowAll, true],
    [denyOnly(['vms/screenshot', 'create']), false],
  ])('needs vms/screenshot create', async (allowed, shown) => {
    rbac.allowed = allowed;
    const wrapper = await card();
    expect(has(wrapper, '.vm-action-screenshot')).toBe(shown);
  });

  it('spins while the page saves the screenshot', async () => {
    const wrapper = await card({ pendingActions: ['screenshot'] });
    expect(action(wrapper, 'screenshot').control.classes()).toContain(
      'is-loading',
    );
  });
});

describe('VmDetailsCard Restart', () => {
  // the Restart button's tooltip and disabled state in `wrapper`
  const restart = (wrapper, selector) =>
    tooltipButtons(wrapper).find((b) => b.control.classes(selector));

  it.each([
    ['running', {}, false, 'Restart the running VM'],
    [
      'shut down',
      { state: 'QUIT', running: false },
      true,
      'Cannot restart: the VM is not running. Start it instead',
    ],
  ])(
    'agrees with the Actions column on a %s VM',
    async (_, fields, disabled, tooltip) => {
      const target = makeVm(fields);
      const details = await card({ vm: target });
      const row = await mountComponent(VmRowActions, {
        props: { vm: target, fullName: 'demo/web' },
      });
      for (const button of [
        restart(section(details, 'Power'), 'vm-action-restart'),
        restart(row, 'vm-row-restart'),
      ]) {
        expect(button).toMatchObject({ disabled, tooltip });
      }
    },
  );
});

describe('VmRowActions', () => {
  it('shows a spinner on an action still running', async () => {
    const actions = await mountComponent(VmRowActions, {
      props: {
        vm: { ...vm, captures: [] },
        fullName: 'demo/web',
        pending: ['captureAll'],
      },
    });
    const button = actions.find('.vm-row-captureAll');
    expect(button.classes()).toContain('is-loading');
    expect(button.attributes('aria-disabled')).toBe('true');
    expect(has(button, '[data-icon="shark-fin"]')).toBe(true);
  });
});

describe('VmDetailsCard OS tag', () => {
  it.each([
    ['linux', 'Linux', 'is-os-linux', 'linux'],
    ['windows', 'Windows', 'is-os-windows', 'windows'],
    ['vyos', 'VyOS', 'is-os-router', 'route'],
    ['other', 'Other', 'is-os-other', 'desktop'],
  ])(
    'names %s as %s with its color and icon',
    async (osType, label, color, iconName) => {
      const wrapper = await card({ vm: { ...vm, os_type: osType } });
      const tag = wrapper.find('.vm-card-title-row .vm-os-tag');
      expect(tag.classes()).toContain(color);
      expect(textOf(tag)).toBe(`Operating system: ${label}`);
      expect(tag.find('.is-sr-only').text()).toBe('Operating system:');
      expect(has(tag, `[data-icon="${iconName}"]`)).toBe(true);
    },
  );

  it('is left out when the VM gives no OS', async () => {
    const wrapper = await card({ vm: { ...vm, os_type: '' } });
    expect(has(wrapper, '.vm-os-tag')).toBe(false);
  });
});

describe('the capture icon', () => {
  it('is a solid icon Font Awesome can draw by name', () => {
    library.add(faSharkFin);
    const svg = icon({ prefix: 'fas', iconName: 'shark-fin' });
    expect(svg).toBeDefined();
    expect(svg.html.join('')).toMatch(/<svg[^>]*viewBox="0 0 512 512"/);
  });
});

describe('VmDetailsCard actions that cannot run now', () => {
  const FILE = 'src/components/experiment/VmDetailsCard.vue';
  const SECTION = '#484848';

  it.each([
    ['running', {}],
    ['shut down', { state: 'QUIT', running: false }],
  ])('say why only in their tooltips on a %s VM', async (_, fields) => {
    const wrapper = await card({ vm: makeVm(fields) });
    const power = section(wrapper, 'Power');
    expect(
      power
        .findAll('.vm-action')
        .some((b) => b.attributes('aria-disabled') === 'true'),
    ).toBe(true);
    expect(textOf(wrapper)).not.toContain('dashed outline');
  });

  it.each([
    ".vm-power-buttons :deep(.button[aria-disabled='true']:not(.is-inverted))",
    ".vm-tool-buttons :deep(.button[aria-disabled='true']:not(.is-inverted))",
    ".vm-section-actions .button[aria-disabled='true']:not(.is-inverted)",
  ])('draw %s as a dashed outline with readable text', (selector) => {
    const rule = componentDeclarations(FILE, selector);
    expect(rule['background-color']).toBe('transparent');
    expect(rule['border-style']).toBe('dashed');
    expect(contrast(rule['border-color'], SECTION)).toBeGreaterThanOrEqual(3);
    expect(contrast(rule.color, SECTION)).toBeGreaterThanOrEqual(4.5);
  });
});

describe("VmDetailsCard of a stopped experiment's VM", () => {
  const DEFAULTS = {
    cpus: 1,
    ram: 2048,
    disk: '/phenix/images/web_snap.qc2',
    inject_partition: 1,
    snapshot: true,
    dnb: false,
    host: '',
  };
  const stoppedVm = (fields) =>
    makeVm({
      state: '',
      running: false,
      ccActive: false,
      taps: [],
      inject_partition: 1,
      host: '',
      dnb: false,
      defaults: { ...DEFAULTS },
      annotations: {},
      ...fields,
    });
  const stoppedCard = (fields, props) =>
    card({ vm: stoppedVm(fields), stopped: true, hosts: ['n1'], ...props });
  const titles = (wrapper) =>
    wrapper.findAll('.vm-section-title').map((t) => textOf(t));

  it('shows its settings, storage, network, labels and annotations, and no live action', async () => {
    rbac.allowed = allowAll;
    const wrapper = await stoppedCard();
    expect(textOf(wrapper.find('.vm-state-tag'))).toBe('Stopped');
    expect(titles(wrapper).map((t) => t.split(' ')[0])).toEqual([
      'Settings',
      'Storage',
      'Network',
      'Labels',
      'Annotations',
      'Port',
    ]);
    for (const selector of [
      '.vm-power-buttons',
      '.vnc-preview',
      '.vm-open-console',
      '.vm-no-screen',
      '.vm-tool-buttons',
      '.vm-captureAll',
      '.vm-stopCaptures',
      '.vm-webshark-tab',
      '.vm-capture',
    ]) {
      expect(has(wrapper, selector), selector).toBe(false);
    }
    expect(wrapper.findAll('.vm-stat-label').map(textOf)).not.toContain(
      'miniccc agent',
    );
    // the network's names are text, with no VLAN change
    expect(wrapper.findAll('button.vm-iface-name')).toHaveLength(0);
  });

  it('edits each setting, asking the page to save it', async () => {
    const wrapper = await stoppedCard();
    expect(wrapper.findAll('.vm-setting-row .vm-kv-key').map(textOf)).toEqual([
      'Host',
      'CPUs',
      'Memory',
      'Disk',
      'Partition',
      'Persistence',
      'Boot',
    ]);
    await byLabel(wrapper, 'CPUs for VM web').setValue('2');
    await byLabel(wrapper, 'Boot VM web').trigger('click');
    await byLabel(wrapper, 'Persistent disk for VM web').trigger('click');
    expect(wrapper.emitted('change-setting')).toEqual([
      ['cpus', 2],
      ['dnb', true],
      ['snapshot', false],
    ]);
    // the boot switch says its state in words on the card
    expect(textOf(byLabel(wrapper, 'Boot VM web'))).toBe('Boot');
  });

  it('spins a setting the page saves, showing the value it saves', async () => {
    const wrapper = await stoppedCard({}, { settingsPending: { cpus: 3 } });
    expect(byLabel(wrapper, 'CPUs for VM web').element.value).toBe('3');
    expect(has(wrapper, '.vm-setting-cpus .vm-setting-spinner')).toBe(true);
    expect(has(wrapper, '.vm-setting-ram .vm-setting-spinner')).toBe(false);
  });

  it.each([
    ['at the topology values', {}, true],
    ['changed', { cpus: 4 }, false],
  ])('offers Reset settings for a VM %s', async (_, fields, off) => {
    const wrapper = await stoppedCard(fields);
    const tooltip = wrapper
      .findAllComponents({ name: 'BTooltip' })
      .find((t) => t.find('.vm-reset').exists());
    const reset = tooltip.find('.vm-reset');
    expect(textOf(reset)).toBe('Reset settings');
    expect(reset.attributes('aria-disabled')).toBe(off ? 'true' : undefined);
    expect(textOf(tooltip.vm.$refs.content)).toBe(
      off
        ? 'Cannot reset settings: they have the topology values'
        : 'Set every setting back to the topology value',
    );
    await reset.trigger('click');
    expect(wrapper.emitted('reset-settings')).toEqual(off ? undefined : [[]]);
  });

  it('spins Reset settings while it waits on the server', async () => {
    const wrapper = await stoppedCard({ cpus: 4 }, { resetting: true });
    expect(wrapper.find('.vm-reset').classes('is-loading')).toBe(true);
  });

  it.each([
    ['without its topology values', { defaults: null }, allowAll],
    ['to a role that may not change it', {}, denyOnly(['vms', 'patch'])],
  ])('offers no Reset settings %s', async (_, fields, role) => {
    rbac.allowed = role;
    const wrapper = await stoppedCard(fields);
    expect(has(wrapper, '.vm-reset')).toBe(false);
  });

  it('shows the settings as text to a role that may not change them', async () => {
    rbac.allowed = denyOnly(['vms', 'patch']);
    const wrapper = await stoppedCard({ host: 'n1', dnb: true });
    expect(wrapper.findAll('.vm-settings select')).toHaveLength(0);
    expect(
      wrapper
        .findAll('.vm-setting-row .vm-setting-control')
        .map((control) => textOf(control)),
    ).toEqual([
      'n1',
      '1',
      '2 GiB',
      '/phenix/images/web_snap.qc2',
      '1',
      'non-persistent',
      'do not boot',
    ]);
  });

  it('marks the settings changed from the topology values', async () => {
    const wrapper = await stoppedCard({ ram: 4096, host: 'n1' });
    expect(
      wrapper
        .findAll('.vm-settings .vm-setting-changed')
        .map((m) => m.attributes('aria-label')),
    ).toEqual([
      'Changed from the topology value: no host',
      'Changed from the topology value: 2 GiB',
    ]);
  });

  describe('port forwards started at experiment start', () => {
    const forwards = async (annotations) =>
      (await stoppedCard({ annotations }))
        .findAll(
          '.vm-startup-forwards .vm-list-text, .vm-startup-forwards .vm-empty',
        )
        .map(textOf);

    it('lists each autotunnel entry as the startup app opens it', async () => {
      expect(
        await forwards({
          'phenix/startup-autotunnel': [
            '8080',
            '2222:22',
            '9090:10.0.0.5:80',
            'a:b:c:d',
          ],
        }),
      ).toEqual([
        '8080 → 127.0.0.1:8080',
        '2222 → 127.0.0.1:22',
        '9090 → 10.0.0.5:80',
        'a:b:c:d (not a valid entry)',
      ]);
    });

    it('says how to add them when there are none', async () => {
      expect(await forwards({})).toEqual([
        'None. Add them with the phenix/startup-autotunnel annotation.',
      ]);
    });

    it("waits for the VM's own details", async () => {
      expect(await forwards(undefined)).toEqual(['Loading…']);
    });

    it('offers no live forward action', async () => {
      rbac.allowed = allowAll;
      const wrapper = await stoppedCard({
        annotations: { 'phenix/startup-autotunnel': ['80'] },
      });
      const section = wrapper.find('.vm-startup-forwards');
      expect(section.findAll('button')).toHaveLength(0);
      expect(titles(wrapper)).not.toContain('Port forwards add');
    });
  });

  it('is left out of a running VM, which keeps its live sections', async () => {
    rbac.allowed = allowAll;
    const wrapper = await card();
    expect(has(wrapper, '.vm-settings')).toBe(false);
    expect(has(wrapper, '.vm-startup-forwards')).toBe(false);
    expect(textOf(wrapper.find('.vm-state-tag'))).not.toBe('Stopped');
  });
});

// M3a: the console of a running VM on the left, live and view only, with
// "Open console" in the Power row.
describe('VmDetailsCard live console', () => {
  const session = {
    experiment: 'demo',
    vm: 'web',
    websocket: 'experiments/demo/vms/web/vnc/ws',
    banners: {
      top: {
        lines: ['EXP: demo - VM: web'],
        backgroundColor: 'white',
        textColor: 'black',
      },
      bottom: { lines: [], backgroundColor: 'white', textColor: 'black' },
    },
    display: { vga: 'std', remoteResize: false },
    clipboard: { mode: 'pasteAsKeys' },
  };
  const SHOT = 'data:image/png;base64,c2hvdA==';
  let open;

  beforeEach(() => {
    vi.stubGlobal('IntersectionObserver', undefined);
    vi.stubGlobal('BroadcastChannel', undefined);
    open = vi.spyOn(window, 'open').mockReturnValue({});
    axios.get.mockImplementation(async (url) =>
      url === 'experiments/demo/vms/web/vnc'
        ? { data: session }
        : Promise.reject(new Error(url)),
    );
  });

  const liveCard = async (props) => {
    const wrapper = await mountComponent(VmDetailsCard, {
      props: {
        vm,
        fullName: 'demo/web',
        experiment: 'demo',
        screenshot: SHOT,
        ...props,
      },
      attachTo: document.body,
    });
    await flushPromises();
    return wrapper;
  };
  const shot = (wrapper) => {
    const img = wrapper.find('.vnc-preview img');
    return img.exists() && img.isVisible() ? img.attributes('src') : null;
  };

  it('is the larger modal: console left, details right', () => {
    expect(
      componentDeclarations(
        'src/components/experiment/VmDetailsCard.vue',
        '.vm-card--console',
      ),
    ).toMatchObject({
      width: 'min(96rem, calc(100vw - 40px))',
      height: '90vh',
    });
    expect(
      componentDeclarations(
        'src/components/experiment/VmDetailsCard.vue',
        '.vm-console-column',
      ),
    ).toMatchObject({ width: '60%' });
    expect(
      componentDeclarations('src/vnc/VncPreview.vue', '.vnc-preview-screen'),
    ).toMatchObject({ 'aspect-ratio': '16 / 10' });
  });

  it('shows a live, view-only console with the banners of the VM', async () => {
    const wrapper = await liveCard();
    expect(wrapper.classes()).toContain('vm-card--console');
    expect(rfbs).toHaveLength(1);
    expect(rfbs[0].viewOnly).toBe(true);
    expect(rfbs[0].options).toEqual({ shared: true });
    expect(wrapper.get('.vnc-console').attributes('aria-label')).toBe(
      'Console of VM web in experiment demo',
    );
    expect(textOf(wrapper.get('.vnc-banner--top'))).toBe('EXP: demo - VM: web');
    expect(wrapper.find('.vnc-banner--bottom').exists()).toBe(false);

    // the screenshot shows until the console draws
    expect(shot(wrapper)).toBe(SHOT);
    rfbs[0].connect();
    await flushPromises();
    expect(shot(wrapper)).toBeNull();
    expect(textOf(wrapper.get('.vnc-preview-bar'))).toContain('View only');
  });

  it('takes the tile of the VM off while it is open', async () => {
    const tile = sharedLiveTilePool().register('demo/web');
    expect(tile.live.value).toBe(true);
    const wrapper = await liveCard();
    expect(tile.live.value).toBe(false);
    wrapper.unmount();
    expect(tile.live.value).toBe(true);
    tile.unregister();
  });

  it('gives the console keyboard input on Take control, and takes it back on View only', async () => {
    const wrapper = await liveCard();
    rfbs[0].connect();
    await flushPromises();

    await wrapper.get('.vnc-preview-control').trigger('click');
    await flushPromises();
    expect(rfbs[0].viewOnly).toBe(false);
    expect(document.activeElement).toBe(rfbs[0].canvas);
    expect(textOf(wrapper.get('.vnc-preview-bar'))).toContain(CHORD_HINT);
    // in control, a click on the screen goes to the VM, not to a link
    expect(wrapper.find('.vnc-preview-link').exists()).toBe(false);

    await wrapper.get('.vnc-preview-control').trigger('click');
    expect(rfbs[0].viewOnly).toBe(true);
    expect(textOf(wrapper.get('.vnc-preview-control'))).toBe('Take control');
  });

  it('leaves the console with the chord', async () => {
    const wrapper = await liveCard();
    rfbs[0].connect();
    await flushPromises();
    await wrapper.get('.vnc-preview-control').trigger('click');
    await flushPromises();

    const screen = wrapper.get('.vnc-preview-screen');
    await screen.trigger('keydown', { code: 'BracketRight', ctrlKey: true });
    await screen.trigger('keydown', { code: 'Tab', key: 'Tab' });
    expect(document.activeElement).toBe(
      wrapper.get('.vnc-preview-control').element,
    );
  });

  it('opens the console page from Open console and from a click on the screen', async () => {
    const wrapper = await liveCard();
    const button = wrapper.get('.vm-open-console');
    expect(button.attributes('href')).toBe('/vnc?vm=demo/web');
    expect(textOf(button)).toBe('Open console');
    // the Power row has it at its end
    expect(button.element.closest('.vm-power-row')).not.toBeNull();
    const screenLink = wrapper.get('.vnc-preview-link');
    expect(screenLink.attributes('href')).toBe('/vnc?vm=demo/web');

    await button.trigger('click', { button: 0 });
    await flushPromises();
    await screenLink.trigger('click', { button: 0 });
    await flushPromises();
    expect(open).toHaveBeenCalledTimes(2);
    expect(open.mock.calls.map(([href]) => href)).toEqual([
      expect.stringMatching(/\/vnc\?vm=demo\/web$/),
      expect.stringMatching(/\/vnc\?vm=demo\/web$/),
    ]);
    // a new console tab each time
    expect(open.mock.calls[0][1]).not.toBe(open.mock.calls[1][1]);
  });

  it('keeps a paused VM live', async () => {
    const wrapper = await liveCard({
      vm: makeVm({ state: 'PAUSED', running: false }),
    });
    expect(has(wrapper, '.vm-no-screen')).toBe(false);
    expect(rfbs).toHaveLength(1);
  });

  it.each([
    ['may', allowAll, true],
    ['may not', denyOnly(['vms/vnc', 'get']), false],
  ])(
    'shows the live console and Open console only when the role %s get vms/vnc',
    async (_, allowed, live) => {
      rbac.allowed = allowed;
      const wrapper = await liveCard();
      expect(rfbs).toHaveLength(live ? 1 : 0);
      expect(has(wrapper, '.vm-open-console')).toBe(live);
      expect(has(wrapper, '.vnc-preview-link')).toBe(live);
      if (!live) {
        expect(shot(wrapper)).toBe(SHOT);
        expect(wrapper.get('.vnc-preview img').attributes('alt')).toBe(
          'VM screenshot',
        );
      }
    },
  );

  it('shows the screenshot of a busy VM, with no console', async () => {
    const wrapper = await liveCard({ vm: makeVm({ busy: true }) });
    expect(rfbs).toEqual([]);
    expect(has(wrapper, '.vm-open-console')).toBe(false);
    expect(shot(wrapper)).toBe(SHOT);
  });
});
