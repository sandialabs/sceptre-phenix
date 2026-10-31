// Mounts the VM details card and the Actions buttons with real Buefy.
import fc from 'fast-check';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import { icon, library } from '@fortawesome/fontawesome-svg-core';

import { faSharkFin } from '@/utils/icons.js';
import VmDetailsCard from '@/components/experiment/VmDetailsCard.vue';
import VmRowActions from '@/components/experiment/VmRowActions.vue';
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

beforeEach(() => resetMocks({ store: {} }));

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
