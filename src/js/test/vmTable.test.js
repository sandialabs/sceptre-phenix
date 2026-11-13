// The running experiment's VM table on its own: what it draws for each VM and
// what it asks the page for.
import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';

const { modules, rbac, resetMocks, store } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);
vi.mock('@/api/vnc.js', modules.vnc);

import loadingImg from '@/assets/imgs/loading-screenshot.svg';
import { placeholderImage } from '@/components/experiment/vmPlaceholder.js';
import VmNetworkMenu from '@/components/experiment/VmNetworkMenu.vue';
import VmRowActions from '@/components/experiment/VmRowActions.vue';
import VmTable from '@/components/experiment/VmTable.vue';
import { useTable } from '@/utils/useTable.js';
import { vm } from './helpers/fixtures.js';
import { allowOnly, denyOnly } from './helpers/permissions.js';
import { componentDeclarations, contrast } from './helpers/css.js';
import {
  mountComponent,
  tableHeader,
  tableRows,
  textOf,
} from './helpers/mount.js';

beforeEach(() => resetMocks());

const COLUMNS = {
  screenshot: true,
  host: true,
  ipv4: true,
  network: true,
  taps: true,
  uptime: true,
  labels: true,
  actions: true,
};

// web, capturing on its second interface, and db, busy
const web = vm({ captures: [{ interface: 1 }], host: 'node1' });
const db = vm({ name: 'db', busy: true, percent: 40 });

// the table on the demo experiment showing `vms`, with these props
const render = (vms = [web, db], props = {}, attachTo = undefined) =>
  mountComponent(VmTable, {
    route: '/experiment/demo',
    props: {
      vms,
      experiment: 'demo',
      table: useTable().table,
      columns: COLUMNS,
      ...props,
    },
    attachTo,
  });

const rowOf = (wrapper, name) =>
  tableRows(wrapper).find((cells) => textOf(cells.Node.find('.tag')) === name);

describe('the rows', () => {
  it('names each VM in a tag colored by its state, with a busy one progressing', async () => {
    const wrapper = await render();
    expect(
      tableRows(wrapper).map((cells) => [
        textOf(cells.Node.find('.tag')),
        cells.Node.find('.tag').classes('is-success'),
        cells.Node.find('progress').exists(),
      ]),
    ).toEqual([
      ['web', true, false],
      ['db', false, true],
    ]);
  });

  it('says why it has no rows', async () => {
    const wrapper = await render([], { emptyText: 'Loading VMs…' });
    expect(textOf(wrapper.find('tbody'))).toBe('Loading VMs…');
  });

  it('leaves out the columns the page hides', async () => {
    const wrapper = await render([web], {
      columns: { ...COLUMNS, host: false, labels: false },
    });
    const columns = Object.keys(tableRows(wrapper)[0]);
    expect(columns).not.toContain('Host');
    expect(columns).not.toContain('Labels');
    expect(columns).toContain('Taps');
  });

  it('puts a column shown again back where it was', async () => {
    const wrapper = await render([web]);
    const order = () => Object.keys(tableRows(wrapper)[0]);
    const all = order();
    for (const column of ['host', 'network', 'screenshot']) {
      await wrapper.setProps({ columns: { ...COLUMNS, [column]: false } });
      expect(order()).toHaveLength(all.length - 1);
      await wrapper.setProps({ columns: COLUMNS });
      expect(order()).toEqual(all);
    }
  });

  it('shows the Delay column only while a VM waits to start', async () => {
    const delayed = vm({
      name: 'late',
      state: 'BUILDING',
      delayed_start: '5m',
    });
    const columns = async (vms) => Object.keys(tableRows(await render(vms))[0]);
    expect(await columns([web])).not.toContain('Delay');
    expect(await columns([web, delayed])).toContain('Delay');
  });

  describe('the countdown of a VM that waits for a timer', () => {
    const START = Date.parse('2026-01-01T10:00:00Z');
    const waiting = (name, delayed_start) =>
      vm({ name, running: false, state: 'BUILDING', delayed_start });
    const delayOf = (wrapper, name) => textOf(rowOf(wrapper, name).Delay);

    beforeEach(() => {
      vi.useFakeTimers({
        now: START,
        toFake: ['setInterval', 'clearInterval', 'Date'],
      });
    });

    afterEach(() => vi.useRealTimers());

    it('counts down each second on one shared clock, and keeps the other delays', async () => {
      const vms = [
        waiting('late', 'timer:5m'),
        waiting('later', 'timer:10m'),
        waiting('manual', 'user'),
        waiting('after', 'cc:server'),
      ];
      const wrapper = await render(vms, {
        delayedDue: {
          late: '2026-01-01T10:04:12Z',
          later: '2026-01-01T10:10:00Z',
        },
      });
      expect(vi.getTimerCount()).toBe(1);
      expect(delayOf(wrapper, 'late')).toBe('starts in 4m 12s');
      expect(delayOf(wrapper, 'later')).toBe('starts in 10m 0s');
      expect(delayOf(wrapper, 'manual')).toBe('user');
      expect(delayOf(wrapper, 'after')).toBe('cc:server');

      vi.advanceTimersByTime(3000);
      await nextTick();
      expect(delayOf(wrapper, 'late')).toBe('starts in 4m 9s');
      expect(delayOf(wrapper, 'later')).toBe('starts in 9m 57s');

      vi.advanceTimersByTime(250_000);
      await nextTick();
      expect(delayOf(wrapper, 'late')).toBe('starts now');

      wrapper.unmount();
      expect(vi.getTimerCount()).toBe(0);
    });

    it('runs no clock without a due time', async () => {
      const wrapper = await render([waiting('late', 'timer:5m')]);
      expect(vi.getTimerCount()).toBe(0);
      expect(delayOf(wrapper, 'late')).toBe('timer:5m');
      await wrapper.setProps({
        delayedDue: { late: '2026-01-01T10:05:00Z' },
      });
      expect(vi.getTimerCount()).toBe(1);
      expect(delayOf(wrapper, 'late')).toBe('starts in 5m 0s');
      await wrapper.setProps({ delayedDue: {} });
      expect(vi.getTimerCount()).toBe(0);
    });
  });

  it('marks the captured interfaces and the ones waiting on the server', async () => {
    const wrapper = await render([web], {
      capturePending: { 'demo/web/0': true },
    });
    const { IP } = rowOf(wrapper, 'web');
    const ifaces = IP.findAll('.field button');
    expect(ifaces.map((d) => d.classes('is-success'))).toEqual([false, true]);
    expect(ifaces.map((d) => d.attributes('aria-busy'))).toEqual([
      'true',
      'false',
    ]);
    expect(IP.findAll('.capture-spinner')).toHaveLength(1);
    const { color } = componentDeclarations(
      'src/components/experiment/VmTable.vue',
      '.plain-button.is-success',
    );
    // on the table's $background-bis
    expect(contrast(color, '#484848')).toBeGreaterThanOrEqual(7);
  });

  it('pictures a VM waiting for its screenshot until they are due', async () => {
    const shot = async (screenshotsDue) =>
      rowOf(await render([web], { screenshotsDue }), 'web')
        .Screenshot.find('img')
        .attributes('src');
    expect(await shot(false)).toBe(loadingImg);
    expect(await shot(true)).toBe(placeholderImage('notAvailable'));
  });

  it('links a running VM to its console page, which opens in a new window', async () => {
    vi.stubGlobal('BroadcastChannel', undefined);
    const open = vi.spyOn(window, 'open').mockReturnValue({});
    const link = rowOf(await render([web]), 'web').Screenshot.find('a');
    expect(link.attributes('href')).toBe('/vnc?vm=demo/web');
    expect(link.attributes('target')).toBeUndefined();
    await link.trigger('click', { button: 0 });
    await flushPromises();
    expect(open).toHaveBeenCalledWith(
      expect.stringMatching(/\/vnc\?vm=demo\/web$/),
      expect.stringMatching(/^phenix-vnc-/),
    );
    open.mockRestore();
    vi.unstubAllGlobals();
  });

  it.each([
    ['running', vm(), true],
    ['paused', vm({ running: false, state: 'PAUSED' }), true],
    ['shut down', vm({ running: false, state: 'QUIT' }), false],
    ['busy', vm({ busy: true, percent: 40 }), false],
    [
      'waiting for its delayed start',
      vm({ running: false, state: 'BUILDING', delayed_start: '5m' }),
      false,
    ],
  ])(
    'links a %s VM to its console only when it has a screen and the role has vms/vnc get',
    async (_, row, linked) => {
      const link = async () =>
        rowOf(await render([row]), 'web')
          .Screenshot.find('a')
          .exists();
      expect(await link()).toBe(linked);
      rbac.allowed = denyOnly(['vms/vnc', 'get']);
      expect(await link()).toBe(false);
    },
  );

  it('counts labels and notes beside the tag icon, and shows no count for none', async () => {
    const vms = [
      vm({ name: 'bare', tags: {} }),
      vm({ name: 'untagged' }),
      vm({ name: 'hidden', tags: { __internal: 'x' } }),
      vm({ name: 'tagged', tags: { role: 'web', __notes_1: 'n', __x: '' } }),
    ];
    const wrapper = await render(vms);
    const count = (name) => {
      const cell = rowOf(wrapper, name).Labels;
      expect(cell.find('[data-icon="tag"]').exists()).toBe(true);
      const badge = cell.find('.vm-label-count-badge');
      return badge.exists() ? textOf(badge) : null;
    };
    expect(vms.map((v) => count(v.name))).toEqual([null, null, null, '2']);

    // a neutral gray, not red, with readable text
    const badge = componentDeclarations(
      'src/components/experiment/VmLabelCount.vue',
      '.vm-label-count-badge',
    );
    expect(badge['background-color']).toBe('#575757');
    expect(
      contrast(badge.color, badge['background-color']),
    ).toBeGreaterThanOrEqual(7);
  });

  it('shows how long a VM has run, a muted dash for one not running, and unknown for an external node', async () => {
    const wrapper = await render([
      vm({ name: 'up', uptime: 3725 }),
      vm({ name: 'down', running: false, state: 'QUIT', uptime: 0 }),
      vm({ name: 'ext', external: true, running: false }),
    ]);
    const uptime = (name) => rowOf(wrapper, name).Uptime;
    expect(textOf(uptime('up'))).toBe('01:02:05');
    expect(textOf(uptime('ext'))).toBe('unknown');

    const dash = uptime('down').find('.vm-uptime-none');
    expect([textOf(dash), dash.attributes('aria-hidden')]).toEqual([
      '—',
      'true',
    ]);
    expect(textOf(uptime('down').find('.is-sr-only'))).toBe('not running');
    const { color } = componentDeclarations(
      'src/components/experiment/VmTable.vue',
      '.vm-uptime-none',
    );
    // on the table's $background-bis
    expect(contrast(color, '#484848')).toBeGreaterThanOrEqual(7);
  });
});

describe('what it asks the page for', () => {
  it.each([
    ['info', 'Node', 'button.tag', [web]],
    ['capture', 'Taps', '.field > div:nth-child(2) button', [web, 1]],
    ['edit-labels', 'Labels', '.is-clickable', [web]],
  ])('emits %s from the %s cell', async (event, column, selector, args) => {
    const wrapper = await render([web]);
    await rowOf(wrapper, 'web')[column].find(selector).trigger('click');
    expect(wrapper.emitted(event)).toEqual([args]);
  });

  it('opens the details alone of a VM the role may not get', async () => {
    rbac.allowed = denyOnly(['vms', 'get']);
    const wrapper = await render([web]);
    await rowOf(wrapper, 'web').Node.find('button.tag').trigger('click');
    expect(wrapper.emitted('details')).toEqual([[web]]);
    expect(wrapper.emitted('info')).toBeUndefined();
  });

  it('lists the IP cell as text for a role without its permissions', async () => {
    const cell = async () => rowOf(await render([web]), 'web').IP;
    expect((await cell()).find('.field').exists()).toBe(true);
    rbac.allowed = denyOnly(['vms/captures', 'create']);
    expect((await cell()).find('.field').exists()).toBe(false);
    expect(textOf(await cell())).toMatch(/, /);
  });

  it("passes on its Actions column's actions with their VM", async () => {
    const wrapper = await render([web]);
    wrapper.findComponent(VmRowActions).vm.$emit('action', 'kill');
    expect(wrapper.emitted('action')).toEqual([['kill', web]]);
  });

  it('spins the capture actions of a VM starting or stopping them all', async () => {
    const wrapper = await render([web], {
      captureAllPending: { 'demo/web': true },
    });
    expect(wrapper.findComponent(VmRowActions).props('pending')).toEqual([
      'captureAll',
      'stopCaptures',
    ]);
  });

  it.each([
    ['shutdown', false, 'start'],
    ['start', true, 'shutdown'],
  ])(
    'keeps spinning a pending %s after the VM swaps its button',
    async (action, running, shown) => {
      const wrapper = await render([vm({ running })], {
        actionPending: { web: [action] },
      });
      const button = wrapper.find(`.vm-row-${shown}`);
      expect(button.classes()).toContain('is-loading');
      expect(button.attributes('aria-disabled')).toBe('true');
    },
  );

  it('selects VMs and all of them through its checkboxes', async () => {
    const wrapper = await render([web, vm({ name: 'ext', external: true })], {
      selected: ['web'],
    });
    const box = (label) =>
      wrapper
        .findAll('label.b-checkbox')
        .find((b) => textOf(b) === label)
        .find('input');

    expect(box('Select VM web').element.checked).toBe(true);
    expect(box('Select VM ext').attributes('disabled')).toBeDefined();
    await box('Select VM web').setValue(false);
    expect(wrapper.emitted('update:selected')).toEqual([[[]]]);
    await box('Select all VMs').setValue(true);
    expect(wrapper.emitted('update:checkAll')).toEqual([[true]]);
  });

  it.each([
    ['Labels', 'labels'],
    ['Network', 'network'],
    ['IP', 'ipv4'],
    ['Taps', 'taps'],
  ])('asks to sort by the %s column, both ways', async (header, field) => {
    const wrapper = await render();
    await tableHeader(wrapper, header).trigger('click');
    await tableHeader(wrapper, header).trigger('click');
    expect(wrapper.emitted('sort')).toEqual([
      [field, 'asc'],
      [field, 'desc'],
    ]);
  });
});

describe('the network menus', () => {
  // web on dmz and mgmt, capturing on mgmt; app captures dmz in a lower case
  const app = vm({
    name: 'app',
    networks: ['dmz (149)', 'disconnected'],
    captures: [{ interface: 0 }],
  });

  // the items that change a running VM's interface, and the Files tab link
  const VLAN_ITEMS = ['Change VLAN…', 'Disconnect', 'Set link quality…'];
  const FILES_ITEM = 'Show capture files in the Files tab';
  // the experiment capture item, greyed out while minimega lacks support
  const CAPTURE_VLAN = (vlan) =>
    `Capture VLAN ${vlan}… (the server did not report capture support) (disabled)`;

  // the network buttons of a VM's interfaces
  const triggers = (wrapper, name) =>
    rowOf(wrapper, name).Network.findAll('.vm-network-trigger');

  // Opens a menu by a click on its button. Vue ignores an event sent in the
  // millisecond its handler was attached, so the test waits past it.
  async function open(trigger) {
    await trigger.trigger('click');
    await flushPromises();
    await new Promise((resolve) => setTimeout(resolve, 2));
  }

  // opens the menu of a VM's interface
  const openMenu = (wrapper, name, iface) =>
    open(triggers(wrapper, name)[iface]);

  // the open menu's items, buttons and links, drawn on the page body
  const items = () => [
    ...document.querySelectorAll('.vm-network-items [role="menuitem"]'),
  ];
  const itemTexts = () =>
    items().map((b) => textOf(b) + (b.disabled ? ' (disabled)' : ''));
  const pick = async (text) => {
    items()
      .find((b) => textOf(b) === text)
      .click();
    await flushPromises();
  };

  it('names each network on its menu button', async () => {
    const wrapper = await render([web]);
    expect(triggers(wrapper, 'web').map(textOf)).toEqual([
      'dmz (149)',
      'mgmt (151)',
    ]);
    expect(triggers(wrapper, 'web')[0].attributes()).toMatchObject({
      'aria-label': 'Network menu for interface 0 of VM web: dmz (149)',
      'aria-expanded': 'false',
      'aria-busy': 'false',
    });
    // a closed menu draws no items
    expect(items()).toEqual([]);
  });

  it.each([
    [
      'an interface not captured',
      [web],
      0,
      [
        ...VLAN_ITEMS,
        'Start capture on this interface',
        'Start interface captures on VLAN dmz',
        CAPTURE_VLAN('dmz'),
        'Show VMs on VLAN dmz',
        FILES_ITEM,
      ],
    ],
    [
      'a captured interface',
      [web],
      1,
      [
        ...VLAN_ITEMS,
        'Stop capture on this interface',
        'Start interface captures on VLAN mgmt',
        'Stop interface captures on VLAN mgmt',
        CAPTURE_VLAN('mgmt'),
        'Show VMs on VLAN mgmt',
        FILES_ITEM,
      ],
    ],
    [
      'an interface on a VLAN another VM captures',
      [web, app],
      0,
      [
        ...VLAN_ITEMS,
        'Start capture on this interface',
        'Start interface captures on VLAN dmz',
        'Stop interface captures on VLAN dmz',
        CAPTURE_VLAN('dmz'),
        'Show VMs on VLAN dmz',
        FILES_ITEM,
      ],
    ],
  ])('offers for %s', async (_, vms, iface, want) => {
    const wrapper = await render(vms);
    await openMenu(wrapper, 'web', iface);
    expect(itemTexts()).toEqual(want);
    expect(triggers(wrapper, 'web')[iface].attributes('aria-expanded')).toBe(
      'true',
    );
  });

  it('offers no Disconnect, VLAN captures or VLAN search for a disconnected interface', async () => {
    const wrapper = await render([app]);
    await openMenu(wrapper, 'app', 1);
    expect(itemTexts()).toEqual([
      'Change VLAN…',
      'Set link quality…',
      'Start capture on this interface (disabled)',
      FILES_ITEM,
    ]);
  });

  it.each([
    ['change-vlan', 0, 'Change VLAN…', [web, 0]],
    ['disconnect', 0, 'Disconnect', [web, 0]],
    ['link-quality', 1, 'Set link quality…', [web, 1]],
    ['search', 0, 'Show VMs on VLAN dmz', ['"dmz (149)"']],
    ['start-capture', 0, 'Start capture on this interface', [web, 0]],
    ['capture', 1, 'Stop capture on this interface', [web, 1]],
    ['capture-vlan', 0, 'Start interface captures on VLAN dmz', ['DMZ', true]],
    [
      'capture-vlan',
      1,
      'Stop interface captures on VLAN mgmt',
      ['MGMT', false],
    ],
  ])(
    'emits %s for interface %i from %s, then closes',
    async (event, iface, text, args) => {
      const wrapper = await render([web]);
      await openMenu(wrapper, 'web', iface);
      await pick(text);
      expect(wrapper.emitted(event)).toEqual([args]);
      expect(items()).toEqual([]);
    },
  );

  it.each([
    [['vms', 'patch'], 'Change VLAN…'],
    [['vms', 'patch'], 'Disconnect'],
    [['vms', 'patch'], 'Set link quality…'],
    [['vms', 'list'], 'Show VMs on VLAN mgmt'],
    [['experiments/files', 'list'], FILES_ITEM],
    [['vms/captures', 'create'], 'Start interface captures on VLAN mgmt'],
    [['vms/captures', 'delete'], 'Stop capture on this interface'],
    [['vms/captures', 'delete'], 'Stop interface captures on VLAN mgmt'],
  ])('offers to a role without %j no %s', async (perm, text) => {
    const offered = async () => {
      document.body.innerHTML = '';
      const wrapper = await render([web]);
      await openMenu(wrapper, 'web', 1);
      return itemTexts();
    };
    expect(await offered()).toContain(text);
    rbac.allowed = denyOnly(perm);
    expect(await offered()).not.toContain(text);
  });

  describe('the experiment capture of a VLAN', () => {
    const supported = { captureSupport: { available: true, reason: '' } };

    it('opens the capture dialog on the VLAN, then closes', async () => {
      const wrapper = await render([web], supported);
      await openMenu(wrapper, 'web', 0);
      expect(itemTexts()).toContain('Capture VLAN dmz…');
      await pick('Capture VLAN dmz…');
      expect(wrapper.emitted('capture-experiment')).toEqual([['DMZ']]);
      expect(items()).toEqual([]);
    });

    it('is offered for a VM not running too', async () => {
      const off = vm({ running: false, state: 'QUIT' });
      const wrapper = await render([off], supported);
      await openMenu(wrapper, 'web', 0);
      expect(itemTexts()).toContain('Capture VLAN dmz…');
    });

    it.each([
      ['experiments/captures', 'create'],
      ['experiments/captures', '*'],
    ])('is not offered to a role without %s %s', async (...perm) => {
      const offered = async () => {
        document.body.innerHTML = '';
        const wrapper = await render([web], supported);
        await openMenu(wrapper, 'web', 1);
        return itemTexts();
      };
      expect(await offered()).toContain('Capture VLAN mgmt…');
      rbac.allowed = denyOnly(perm);
      expect(await offered()).not.toContain('Capture VLAN mgmt…');
    });

    it('is greyed out with the short reason while minimega lacks support', async () => {
      const wrapper = await render([web], {
        captureSupport: {
          available: false,
          reason: 'requires minimega 3.4.0 or later; the head node runs 3.3.0',
        },
      });
      await openMenu(wrapper, 'web', 0);
      expect(itemTexts()).toContain(
        'Capture VLAN dmz… (requires minimega 3.4.0 or later) (disabled)',
      );
      // a disabled item cannot be picked
      items()
        .find((b) => textOf(b).startsWith('Capture VLAN'))
        .click();
      await flushPromises();
      expect(wrapper.emitted('capture-experiment')).toBeUndefined();
    });
  });

  it('offers no VLAN change, Disconnect or link quality for a VM not running', async () => {
    const wrapper = await render([vm({ running: false, state: 'QUIT' })]);
    await openMenu(wrapper, 'web', 0);
    expect(itemTexts()).toEqual([
      'Start capture on this interface (the VM is not running) (disabled)',
      'Start interface captures on VLAN dmz (the VM is not running) (disabled)',
      CAPTURE_VLAN('dmz'),
      'Show VMs on VLAN dmz',
      FILES_ITEM,
    ]);
  });

  it('offers no VLAN search for a network whose name has a double quote', async () => {
    const wrapper = await render([{ ...web, networks: ['A"B (5)'] }]);
    await openMenu(wrapper, 'web', 0);
    expect(itemTexts()).not.toContain('Show VMs on VLAN a"b');
    expect(itemTexts()).toContain('Start interface captures on VLAN a"b');
  });

  // the open menu's link with this text, by the URL it goes to
  const hrefOf = (text) =>
    items()
      .find((el) => textOf(el) === text)
      ?.getAttribute('href');

  it("links to the interface's capture files in the Files tab, and closes on a click", async () => {
    const wrapper = await mountComponent(VmTable, {
      route: '/experiment/demo',
      props: {
        vms: [web],
        experiment: 'demo',
        table: useTable().table,
        columns: COLUMNS,
      },
      attachTo: document.body,
    });
    await openMenu(wrapper, 'web', 1);
    expect(hrefOf(FILES_ITEM)).toBe('/experiment/demo?tab=files&q=web_1_');

    items()
      .find((el) => textOf(el) === FILES_ITEM)
      .click();
    await flushPromises();
    expect(items()).toEqual([]);
    expect(document.activeElement).toBe(triggers(wrapper, 'web')[1].element);
    expect(wrapper.router.currentRoute.value.query).toEqual({
      tab: 'files',
      q: 'web_1_',
    });
  });

  it.each([
    ['a live capture with WebShark installed', true, 1, ['webshark'], null],
    ['an interface not captured', false, 0, ['webshark'], null],
    ['a server without WebShark', false, 1, [], null],
    [
      'a role that may not list captures',
      false,
      1,
      ['webshark'],
      ['vms/captures', 'list'],
    ],
  ])(
    'for %s, links to WebShark: %s',
    async (_, shown, iface, features, denied) => {
      store.features = features;
      if (denied) rbac.allowed = denyOnly(denied);
      const wrapper = await render([web]);
      await openMenu(wrapper, 'web', iface);
      expect(hrefOf('Open live capture in WebShark')).toBe(
        shown ? '/packets?exp=demo&vm=web&iface=1' : undefined,
      );
    },
  );

  it('shows as text the networks of a VM not running that have nothing to offer', async () => {
    rbac.allowed = allowOnly(['vms/captures']);
    const off = vm({ running: false, state: 'QUIT' });
    const wrapper = await render([off]);
    const cell = rowOf(wrapper, 'web').Network;
    expect(cell.find('.vm-network-trigger').exists()).toBe(false);
    expect(cell.findAll('.field > div').map(textOf)).toEqual([
      'dmz (149)',
      'mgmt (151)',
    ]);
  });

  it('offers a role that may change nothing the VMs on a VLAN alone', async () => {
    rbac.allowed = allowOnly(['vms', 'list']);
    const wrapper = await render([web]);
    await openMenu(wrapper, 'web', 0);
    expect(itemTexts()).toEqual(['Show VMs on VLAN dmz']);
  });

  it('greys out the items of a VM not running while its capture waits, saying why', async () => {
    rbac.allowed = allowOnly(['vms/captures']);
    const off = vm({ running: false, state: 'QUIT' });
    const wrapper = await render([off], {
      capturePending: { 'demo/web/0': true },
    });
    await openMenu(wrapper, 'web', 0);
    expect(itemTexts()).toEqual([
      'Start capture on this interface (the VM is not running) (disabled)',
      'Start interface captures on VLAN dmz (the VM is not running) (disabled)',
    ]);
  });

  it.each([
    [
      'a VLAN change',
      { actionPending: { web: ['vlan:0'] } },
      [
        ...VLAN_ITEMS,
        'Start capture on this interface',
        'Start interface captures on VLAN dmz',
        CAPTURE_VLAN('dmz'),
        'Show VMs on VLAN dmz',
        FILES_ITEM,
      ],
    ],
    [
      "the interface's capture",
      { capturePending: { 'demo/web/0': true } },
      [
        ...VLAN_ITEMS,
        'Start capture on this interface (disabled)',
        'Start interface captures on VLAN dmz',
        CAPTURE_VLAN('dmz'),
        'Show VMs on VLAN dmz',
        FILES_ITEM,
      ],
    ],
    [
      "its VLAN's captures",
      { vlanCapturePending: { DMZ: true } },
      [
        ...VLAN_ITEMS,
        'Start capture on this interface',
        'Start interface captures on VLAN dmz (disabled)',
        CAPTURE_VLAN('dmz'),
        'Show VMs on VLAN dmz',
        FILES_ITEM,
      ],
    ],
  ])(
    'spins while %s waits on the server, greying out what waits',
    async (_, props, want) => {
      const wrapper = await render([web], props);
      const [dmz, mgmt] = triggers(wrapper, 'web');
      expect(dmz.find('.capture-spinner').exists()).toBe(true);
      expect(dmz.attributes('aria-busy')).toBe('true');
      expect(mgmt.find('.capture-spinner').exists()).toBe(false);
      await openMenu(wrapper, 'web', 0);
      expect(itemTexts()).toEqual(want);
    },
  );

  it('lists the networks of a busy VM as text', async () => {
    const cell = rowOf(await render([web, db]), 'db').Network;
    expect(cell.find('.vm-network-trigger').exists()).toBe(false);
    expect(textOf(cell)).toBe('dmz (149), mgmt (151)');
  });

  // presses a key on the focused element
  async function key(name) {
    document.activeElement.dispatchEvent(
      new KeyboardEvent('keydown', { key: name, bubbles: true }),
    );
    await flushPromises();
  }

  it.each([
    {
      on: 'Escape in the menu, giving the focus back to its button',
      act: () => key('Escape'),
      refocus: true,
    },
    {
      on: 'Tab in the menu, giving the focus back to its button',
      act: () => key('Tab'),
      refocus: true,
    },
    {
      on: 'Escape on the button of a menu with no item to press',
      noItems: true,
      act: ({ trigger }) => trigger.trigger('keydown', { key: 'Escape' }),
      refocus: true,
    },
    {
      on: 'Tab on the button of a menu with no item to press',
      noItems: true,
      act: ({ trigger }) => trigger.trigger('keydown', { key: 'Tab' }),
    },
    {
      on: 'a click elsewhere, but not a click in the menu',
      act: async () => {
        document.querySelector('.vm-network-items').click();
        expect(items()).toHaveLength(8);
        document.body.click();
      },
    },
    {
      on: 'a second click of its button',
      act: ({ trigger }) => open(trigger),
    },
    {
      on: 'a resize of the window',
      act: () => window.dispatchEvent(new Event('resize')),
    },
    {
      on: 'a scroll of the page, but not a scroll of the menu',
      act: async () => {
        const removed = vi.spyOn(window, 'removeEventListener');
        document
          .querySelector('.vm-network-items')
          .dispatchEvent(new Event('scroll'));
        await flushPromises();
        expect(items()).toHaveLength(8);
        window.dispatchEvent(new Event('scroll'));
        expect(removed).toHaveBeenCalledWith(
          'scroll',
          expect.any(Function),
          true,
        );
        removed.mockRestore();
      },
    },
    {
      on: 'a scroll of a scrolled box',
      act: () => {
        const box = document.body.appendChild(document.createElement('div'));
        box.dispatchEvent(new Event('scroll'));
        box.remove();
      },
    },
    {
      on: 'the VM turning busy',
      act: ({ wrapper }) => wrapper.setProps({ vms: [{ ...web, busy: true }] }),
    },
    {
      on: 'the rows changing order',
      vms: [web, app],
      act: ({ wrapper }) => wrapper.setProps({ vms: [app, web] }),
    },
    {
      on: 'the rows showing another VM in its place',
      vms: [web, app],
      act: ({ wrapper }) => wrapper.setProps({ vms: [app] }),
    },
    {
      on: 'the menu being given another VM',
      alone: true,
      act: ({ wrapper }) => wrapper.setProps({ vm: { ...web, name: 'db' } }),
    },
  ])('closes on $on', async ({ vms = [web], noItems, alone, act, refocus }) => {
    if (noItems) rbac.allowed = allowOnly(['vms/captures']);
    const pending = noItems
      ? {
          capturePending: { 'demo/web/0': true },
          vlanCapturePending: { DMZ: true },
        }
      : {};
    // the table, or the menu of web's interface 0 alone, attached to the page
    const wrapper = alone
      ? await mountComponent(VmNetworkMenu, {
          route: '/experiment/demo',
          props: { vm: web, index: 0, experiment: 'demo' },
          attachTo: document.body,
        })
      : await render(vms, pending, document.body);
    const trigger = wrapper.find('.vm-network-trigger');
    trigger.element.focus();
    await open(trigger);
    if (noItems) {
      expect(itemTexts()).toEqual([
        'Start capture on this interface (disabled)',
        'Start interface captures on VLAN dmz (disabled)',
      ]);
      expect(document.activeElement).toBe(trigger.element);
    } else {
      expect(items()).not.toEqual([]);
      expect(textOf(document.activeElement)).toBe('Change VLAN…');
    }

    await act({ wrapper, trigger });
    await flushPromises();
    expect(items()).toEqual([]);
    expect(wrapper.find('[aria-expanded="true"]').exists()).toBe(false);
    if (refocus) expect(document.activeElement).toBe(trigger.element);
    wrapper.unmount();
  });

  it('moves the focus between the items of a menu the down arrow opens', async () => {
    const wrapper = await render([web], {}, document.body);
    const trigger = triggers(wrapper, 'web')[1];
    trigger.element.focus();
    await trigger.trigger('keydown', { key: 'ArrowDown' });
    await flushPromises();
    const focused = () => textOf(document.activeElement);
    expect(focused()).toBe('Change VLAN…');
    expect(trigger.attributes('aria-controls')).toBe(
      document.querySelector('.vm-network-items').id,
    );

    await key('ArrowDown');
    expect(focused()).toBe('Disconnect');
    // the last item is a link, which the keys reach as they do the buttons
    await key('End');
    expect(focused()).toBe(FILES_ITEM);
    await key('ArrowUp');
    expect(focused()).toBe('Show VMs on VLAN mgmt');
    await key('ArrowDown');
    await key('ArrowDown');
    expect(focused()).toBe('Change VLAN…');
    await key('ArrowUp');
    expect(focused()).toBe(FILES_ITEM);
    await key('Home');
    expect(focused()).toBe('Change VLAN…');
    // a key that moves nothing is left alone
    await key('a');
    expect(focused()).toBe('Change VLAN…');
    wrapper.unmount();
  });
});
