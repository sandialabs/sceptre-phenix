// The top bar of the console page (T37, M2a and M6a): the VM it acts on, the
// VM actions the role may take, the connection indicator, the layout picker,
// and the dropdown panels.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';
import { nextTick } from 'vue';

const { modules, rbac, resetMocks, store } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);

import VncToolbar from '@/vnc/VncToolbar.vue';
import { CHORD_HINT } from '@/vnc/chord.js';
import { vm } from './helpers/fixtures.js';
import {
  buttons,
  mountComponent,
  textOf,
  toasts,
  tooltipButtons,
} from './helpers/mount.js';
import {
  allowAll,
  allowOnly,
  denyAll,
  denyOnly,
} from './helpers/permissions.js';

const CONNECTED = { state: 'connected', message: '', retrySeconds: null };
const DETAILS = {
  width: 1024,
  height: 768,
  scale: 1.18,
  scaling: 'fit',
  quality: 6,
  compression: 2,
  clipboardMode: 'pasteAsKeys',
};

function mountToolbar(props = {}, slots = {}) {
  return mountComponent(VncToolbar, {
    props: {
      experiment: 'demo',
      vm: 'web',
      vmRow: vm(),
      status: CONNECTED,
      details: DETAILS,
      layout: 'single',
      paneCount: 1,
      ...props,
    },
    slots: {
      keys: '<p class="keys-panel">keys panel</p>',
      clipboard: '<p class="clipboard-panel">clipboard panel</p>',
      view: '<p class="view-panel">view panel</p>',
      add: '<p class="add-panel">add panel</p>',
      ...slots,
    },
    attachTo: document.body,
  });
}

// the VM action buttons, not those of the panels: their accessible names
const actionButtons = (wrapper) =>
  wrapper
    .findAll('.vnc-vm-actions button')
    .filter((b) => !b.element.closest('.vnc-panel'));

const actionNames = (wrapper) =>
  actionButtons(wrapper).map((b) => b.attributes('aria-label'));

const actionButton = (wrapper, label) =>
  actionButtons(wrapper).find((b) => b.attributes('aria-label') === label);

// the Buefy tooltip around the element that `selector` finds
const tooltipOf = (wrapper, selector) =>
  wrapper
    .findAllComponents({ name: 'BTooltip' })
    .find((tooltip) => tooltip.find(selector).exists());

const panelButton = (wrapper, label) =>
  wrapper
    .findAll('.vnc-panel-button')
    .find((b) => b.attributes('aria-label') === label);

beforeEach(() => resetMocks());

describe('the VM it acts on', () => {
  it('names the VM, with no state and no pane prefix in single view', async () => {
    const wrapper = await mountToolbar();
    expect(textOf(wrapper.find('.vnc-target'))).toBe('demo / web');
    expect(wrapper.text()).not.toContain('acting on');
    expect(wrapper.find('.tag').exists()).toBe(false);
  });

  it('says which pane it acts on in split view', async () => {
    const wrapper = await mountToolbar({ split: true, paneCount: 2 });
    expect(textOf(wrapper.find('.vnc-target'))).toBe('acting on demo / web');
  });

  it('shows no state tag for a paused VM', async () => {
    const wrapper = await mountToolbar({
      vmRow: vm({ state: 'PAUSED', running: false }),
    });
    expect(wrapper.find('.vnc-target .tag').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('Paused');
  });

  it('links to the experiment page and to the VM details on it', async () => {
    const wrapper = await mountToolbar();
    const experiment = wrapper.find('.vnc-experiment-link');
    expect(experiment.attributes('href')).toBe('/experiment/demo');
    expect(experiment.attributes('target')).toBe('_blank');
    expect(experiment.attributes('aria-label')).toBe('Experiment demo');
    expect(experiment.find('[data-icon]').attributes('data-icon')).toBe(
      'flask',
    );
    expect(
      textOf(tooltipOf(wrapper, '.vnc-experiment-link').vm.$refs.content),
    ).toBe('Open the page of experiment demo in a new tab');
    const details = wrapper.find('.vnc-details-link');
    expect(details.attributes('href')).toBe('/experiment/demo?vm=web');
    expect(details.attributes('target')).toBe('_blank');
    expect(details.attributes('aria-label')).toBe('Details of VM web');
    expect(details.find('[data-icon]').attributes('data-icon')).toBe(
      'circle-info',
    );
    expect(
      textOf(tooltipOf(wrapper, '.vnc-details-link').vm.$refs.content),
    ).toBe('Open the details of VM web on the experiment page in a new tab');
  });

  it('leaves the VM details link to the pane headers in split view', async () => {
    const wrapper = await mountToolbar({ split: true, paneCount: 2 });
    expect(wrapper.find('.vnc-experiment-link').exists()).toBe(true);
    expect(wrapper.find('.vnc-details-link').exists()).toBe(false);
  });

  it.each([
    [allowOnly(['experiments', 'get', 'demo']), true],
    [denyOnly(['experiments', 'get', 'demo']), false],
  ])(
    'links to the experiment only for a role that may get it',
    async (allowed, shown) => {
      rbac.allowed = allowed;
      const wrapper = await mountToolbar();
      expect(wrapper.find('.vnc-experiment-link').exists()).toBe(shown);
      expect(wrapper.find('.vnc-details-link').exists()).toBe(shown);
    },
  );

  it('has the KEYBOARD tag with the chord hint only while the console has input', async () => {
    const wrapper = await mountToolbar();
    expect(wrapper.find('.vnc-keyboard-tag').exists()).toBe(false);
    await wrapper.setProps({ keyboard: true });
    const tag = tooltipOf(wrapper, '.vnc-keyboard-tag');
    expect(textOf(wrapper.find('.vnc-keyboard-tag'))).toBe('Keyboard');
    expect(textOf(tag.vm.$refs.content)).toBe(CHORD_HINT);
  });

  it('shows the empty state with no VM', async () => {
    const wrapper = await mountToolbar({
      experiment: '',
      vm: '',
      vmRow: null,
      paneCount: 0,
      status: { state: 'idle' },
    });
    expect(textOf(wrapper.find('.vnc-target'))).toBe('No VM');
    expect(actionNames(wrapper)).toEqual([]);
  });
});

describe('the VM actions', () => {
  // a role without this permission, and the tooltip that names it
  const NOT_PERMITTED = (resource, verb) =>
    `Your role does not permit this action (${resource} ${verb})`;

  it('offers the icon buttons for a running VM, in order', async () => {
    const wrapper = await mountToolbar();
    expect(actionNames(wrapper)).toEqual([
      'Pause',
      'Restart',
      'Shut down',
      'Screenshot',
      'Capture interface 0',
      'Port forwards',
      'Mount disk',
    ]);
  });

  it('puts the VM buttons, Clipboard, Actions, Keys, View, then the layouts', async () => {
    const wrapper = await mountToolbar();
    const order = [
      '.vnc-vm-actions',
      '[aria-controls="vnc-panel-clipboard"]',
      '.vnc-actions-trigger',
      '[aria-controls="vnc-panel-keys"]',
      '[aria-controls="vnc-panel-view"]',
      '.vnc-layouts',
      '[aria-controls="vnc-panel-add"]',
      '.vnc-fullscreen-button',
      '.vnc-status',
      '.vnc-hide-toolbar',
    ].map((selector) => wrapper.find(selector).element);
    const all = [...wrapper.element.querySelectorAll('*')];
    expect(order.map((el) => all.indexOf(el))).toEqual(
      order.map((el) => all.indexOf(el)).toSorted((a, b) => a - b),
    );
    expect(order.every((el) => all.includes(el))).toBe(true);
  });

  it('gives each icon button its icon', async () => {
    const wrapper = await mountToolbar();
    const icon = (label) =>
      actionButton(wrapper, label).find('[data-icon]').attributes('data-icon');
    expect(
      Object.fromEntries(actionNames(wrapper).map((l) => [l, icon(l)])),
    ).toEqual({
      Pause: 'pause',
      Restart: 'sync-alt',
      'Shut down': 'power-off',
      Screenshot: 'camera',
      'Capture interface 0': 'shark-fin',
      'Port forwards': 'arrow-right',
      'Mount disk': 'hdd',
    });
  });

  it.each([
    ['Pause', 'pause', ['vms/stop', 'update'], vm()],
    [
      'Resume',
      'start',
      ['vms/start', 'update'],
      vm({ state: 'PAUSED', running: false }),
    ],
    [
      'Start',
      'start',
      ['vms/start', 'update'],
      vm({ state: 'QUIT', running: false }),
    ],
    ['Restart', 'restart', ['vms/restart', 'update'], vm()],
    ['Shut down', 'shutdown', ['vms/shutdown', 'update'], vm()],
    ['Screenshot', 'screenshot', ['vms/screenshot', 'create'], vm()],
    ['Mount disk', 'mount', ['vms/mount', 'post'], vm()],
    ['Capture interface 0', 'captureIface', ['vms/captures', 'create'], vm()],
    [
      'Stop captures',
      'stopCaptures',
      ['vms/captures', 'delete'],
      vm({ captures: [{ interface: 1 }] }),
    ],
  ])(
    'runs %s for a role with %s, and greys it out with the permission for one without',
    async (label, name, [resource, verb], row) => {
      store.features = ['vm-mount'];
      rbac.allowed = allowAll;
      let wrapper = await mountToolbar({ vmRow: row });
      const allowed = tooltipButtons(wrapper).find((t) => t.label === label);
      expect(allowed.disabled).toBe(false);
      await allowed.control.trigger('click');
      expect(wrapper.emitted('action')).toEqual([[name]]);
      wrapper.unmount();

      rbac.allowed = denyOnly([resource, verb]);
      wrapper = await mountToolbar({ vmRow: row });
      const denied = tooltipButtons(wrapper).find((t) => t.label === label);
      expect(denied.disabled).toBe(true);
      expect(denied.tooltip).toBe(NOT_PERMITTED(resource, verb));
      await denied.control.trigger('click');
      expect(wrapper.emitted('action')).toBeUndefined();
    },
  );

  it('checks the permissions for the VM of the pane', async () => {
    rbac.allowed = allowOnly(['vms/restart', 'update', 'other/web']);
    const wrapper = await mountToolbar();
    const restart = tooltipButtons(wrapper).find((t) => t.label === 'Restart');
    expect(restart.disabled).toBe(true);
    expect(restart.tooltip).toBe(NOT_PERMITTED('vms/restart', 'update'));
  });

  it('shows every VM button greyed out to a role with no permissions, and keeps the input and view controls', async () => {
    rbac.allowed = denyAll;
    const wrapper = await mountToolbar();
    expect(actionNames(wrapper)).toHaveLength(7);
    const states = tooltipButtons(wrapper).filter((t) =>
      actionNames(wrapper).includes(t.label),
    );
    expect(states.every((t) => t.disabled)).toBe(true);
    expect(
      wrapper
        .findAll('.vnc-panel-button')
        .map((b) => b.attributes('aria-label')),
    ).toEqual(['Port forwards', 'Clipboard', 'Keys', 'View', 'Add VM']);
    expect(wrapper.find('[aria-label="Full screen"]').exists()).toBe(true);
  });

  it('greys out an action that the VM state rules out, with the reason', async () => {
    const wrapper = await mountToolbar({
      vmRow: vm({ state: 'PAUSED', running: false }),
    });
    const restart = tooltipButtons(wrapper).find((t) => t.label === 'Restart');
    expect(restart.disabled).toBe(true);
    expect(restart.tooltip).toBe(
      'Cannot restart: the VM is paused. Resume paused VMs first',
    );
    await restart.control.trigger('click');
    expect(wrapper.emitted('action')).toBeUndefined();
  });

  it('greys out every VM action of a busy VM, but not the port forward list', async () => {
    const wrapper = await mountToolbar({ vmRow: vm({ busy: true }) });
    const states = tooltipButtons(wrapper).filter((t) =>
      actionNames(wrapper).includes(t.label),
    );
    expect(states.filter((t) => !t.disabled).map((t) => t.label)).toEqual([
      'Port forwards',
    ]);
  });

  it('spins the button of the action that runs', async () => {
    const wrapper = await mountToolbar({ pending: 'restart' });
    const restart = actionButton(wrapper, 'Restart');
    expect(restart.classes()).toContain('is-loading');
    expect(restart.attributes('aria-busy')).toBe('true');
  });

  it('has no actions before the VM row loads', async () => {
    const wrapper = await mountToolbar({ vmRow: null });
    expect(actionNames(wrapper)).toEqual([]);
    expect(wrapper.find('.vnc-actions-menu').exists()).toBe(false);
  });
});

describe('the capture button', () => {
  const capture = (wrapper) =>
    tooltipButtons(wrapper).find((t) =>
      ['Capture interface 0', 'Stop captures'].includes(t.label),
    );

  it.each([
    [
      'starts a capture on interface 0 while no capture runs',
      vm(),
      'Capture interface 0',
      'Start a packet capture on interface 0 of the VM',
      'captureIface',
    ],
    [
      'stops every capture while one runs on any interface',
      vm({ captures: [{ interface: 1 }] }),
      'Stop captures',
      'Stop every packet capture running on the VM',
      'stopCaptures',
    ],
    [
      'stays able to stop captures when interface 0 is disconnected',
      vm({
        networks: ['disconnected', 'MGMT (151)'],
        captures: [{ interface: 1 }],
      }),
      'Stop captures',
      'Stop every packet capture running on the VM',
      'stopCaptures',
    ],
  ])('%s', async (_, row, label, tooltip, action) => {
    const wrapper = await mountToolbar({ vmRow: row });
    const button = capture(wrapper);
    expect(button).toMatchObject({ label, tooltip, disabled: false });
    expect(button.control.find('[data-icon]').attributes('data-icon')).toBe(
      action === 'stopCaptures' ? 'stop' : 'shark-fin',
    );
    await button.control.trigger('click');
    expect(wrapper.emitted('action')).toEqual([[action]]);
  });

  it.each([
    [
      'the VM has no interface 0',
      vm({ networks: [], ipv4: [], taps: [] }),
      'Cannot capture interface 0: the VM has no interface 0',
    ],
    [
      'interface 0 is disconnected',
      vm({ networks: ['disconnected', 'MGMT (151)'] }),
      'Cannot capture interface 0: interface 0 of the VM is disconnected',
    ],
    [
      'the VM is not running',
      vm({ state: 'QUIT', running: false }),
      'Cannot capture interface 0: the VM is not running',
    ],
  ])('is greyed out when %s', async (_, row, reason) => {
    const wrapper = await mountToolbar({ vmRow: row });
    expect(capture(wrapper)).toMatchObject({
      label: 'Capture interface 0',
      tooltip: reason,
      disabled: true,
    });
  });
});

describe('Mount disk', () => {
  const mount = (wrapper) =>
    tooltipButtons(wrapper).find((t) => t.label === 'Mount disk');

  it.each([
    [[], vm(), 'Cannot mount disk: the server does not support it'],
    [
      ['vm-mount'],
      vm({ state: 'QUIT', running: false }),
      'Cannot mount disk: the VM is not running',
    ],
    [
      ['vm-mount'],
      vm({ ccActive: false }),
      'Cannot mount disk: it needs an active miniccc agent in the VM',
    ],
  ])(
    'is greyed out with features %j and the reason',
    async (features, row, reason) => {
      store.features = features;
      const wrapper = await mountToolbar({ vmRow: row });
      expect(mount(wrapper)).toMatchObject({ tooltip: reason, disabled: true });
    },
  );

  it('asks the page to mount the disk when it can', async () => {
    store.features = ['vm-mount'];
    const wrapper = await mountToolbar();
    await mount(wrapper).control.trigger('click');
    expect(wrapper.emitted('action')).toEqual([['mount']]);
  });
});

describe('Port forwards', () => {
  const forwards = (wrapper) =>
    tooltipButtons(wrapper).find((t) => t.label === 'Port forwards');

  it('opens the panel of the port forwards, which the page fills', async () => {
    const wrapper = await mountToolbar(
      {},
      { forwards: '<p class="forwards-panel">forwards panel</p>' },
    );
    expect(wrapper.find('.forwards-panel').isVisible()).toBe(false);
    await forwards(wrapper).control.trigger('click');
    expect(wrapper.find('.forwards-panel').isVisible()).toBe(true);
    expect(forwards(wrapper).control.attributes('aria-expanded')).toBe('true');
    expect(wrapper.emitted('action')).toBeUndefined();
  });

  it('is greyed out for a role that may not list the port forwards', async () => {
    rbac.allowed = denyOnly(['vms/forwards', 'list']);
    const wrapper = await mountToolbar(
      {},
      { forwards: '<p class="forwards-panel">forwards panel</p>' },
    );
    expect(forwards(wrapper)).toMatchObject({
      tooltip: 'Your role does not permit this action (vms/forwards list)',
      disabled: true,
    });
    await forwards(wrapper).control.trigger('click');
    expect(wrapper.find('.forwards-panel').isVisible()).toBe(false);
  });
});

describe('the Actions menu', () => {
  // each row of the menu by its text, and --- for each line between groups
  const menuRows = (wrapper) =>
    wrapper
      .findAll('.vnc-actions-menu .dropdown-item, .vnc-actions-menu hr')
      .map((row) =>
        row.element.tagName === 'HR'
          ? '---'
          : textOf(row.find('.vnc-actions-label')),
      );

  const menuItem = (wrapper, text) =>
    wrapper
      .findAll('.vnc-actions-menu .dropdown-item')
      .find((row) => textOf(row.find('.vnc-actions-label')) === text);

  const tipOf = (row) =>
    textOf(row.findComponent({ name: 'BTooltip' }).vm.$refs.content);

  it('has an icon and the Actions label, with no arrow', async () => {
    const wrapper = await mountToolbar();
    const trigger = wrapper.find('.vnc-actions-trigger');
    expect(textOf(trigger)).toBe('Actions');
    expect(
      trigger.findAll('[data-icon]').map((i) => i.attributes('data-icon')),
    ).toEqual(['bars']);
  });

  it('lists the actions in groups, one to a row with its icon, the last group in red', async () => {
    const wrapper = await mountToolbar();
    expect(menuRows(wrapper)).toEqual([
      'Snapshot',
      'Memory snapshot',
      'Backing image',
      'Capture all interfaces',
      '---',
      'Insert CD-ROM',
      '---',
      'Edit labels',
      'Edit annotations',
      '---',
      'Redeploy',
      'Reset disk',
      'Kill',
    ]);
    const items = wrapper.findAll('.vnc-actions-menu .dropdown-item');
    expect(items.every((i) => i.attributes('role') === 'menuitem')).toBe(true);
    expect(items.every((i) => i.find('[data-icon]').exists())).toBe(true);
    expect(
      items
        .filter((i) => i.classes('vnc-actions-item--danger'))
        .map((i) => textOf(i.find('.vnc-actions-label'))),
    ).toEqual(['Redeploy', 'Reset disk', 'Kill']);
  });

  it('offers to stop the captures while one runs', async () => {
    const wrapper = await mountToolbar({
      vmRow: vm({ captures: [{ interface: 0 }] }),
    });
    expect(menuRows(wrapper)).toContain('Stop all packet captures');
    expect(menuRows(wrapper)).not.toContain('Capture all interfaces');
    await menuItem(wrapper, 'Stop all packet captures').trigger('click');
    expect(wrapper.emitted('action')).toEqual([['stopCaptures']]);
  });

  it.each([
    ['Snapshot', 'snapshot'],
    ['Memory snapshot', 'memorySnapshot'],
    ['Backing image', 'commit'],
    ['Capture all interfaces', 'captureAll'],
    ['Insert CD-ROM', 'cdrom'],
    ['Edit labels', 'labels'],
    ['Edit annotations', 'annotations'],
    ['Redeploy', 'redeploy'],
    ['Reset disk', 'resetDisk'],
    ['Kill', 'kill'],
  ])('asks the page for %s', async (text, action) => {
    const wrapper = await mountToolbar();
    await menuItem(wrapper, text).trigger('click');
    expect(wrapper.emitted('action')).toEqual([[action]]);
  });

  it.each([
    ['Kill', ['vms', 'delete'], vm()],
    ['Redeploy', ['vms/redeploy', 'update'], vm()],
    ['Edit labels', ['vms', 'patch'], vm()],
    ['Backing image', ['vms/commit', 'create'], vm()],
  ])(
    'greys out %s for a role without %j, and names the permission',
    async (text, [resource, verb], row) => {
      rbac.allowed = denyOnly([resource, verb]);
      const wrapper = await mountToolbar({ vmRow: row });
      const item = menuItem(wrapper, text);
      expect(item.attributes('aria-disabled')).toBe('true');
      expect(tipOf(item)).toBe(
        `Your role does not permit this action (${resource} ${verb})`,
      );
      await item.trigger('click');
      expect(wrapper.emitted('action')).toBeUndefined();
    },
  );

  it.each([
    ['Snapshot', 'Cannot snapshot: the VM is not running'],
    ['Kill', 'Cannot kill: the VM is not running'],
    [
      'Reset disk',
      'Cannot reset disk: the VM is paused. Resume paused VMs first',
    ],
  ])(
    'greys out %s when the VM state rules it out, with the reason',
    async (text, reason) => {
      const wrapper = await mountToolbar({
        vmRow: vm({ state: 'PAUSED', running: false }),
      });
      const item = menuItem(wrapper, text);
      expect(item.attributes('aria-disabled')).toBe('true');
      expect(tipOf(item)).toBe(reason);
    },
  );

  it('describes an action it can take in its tooltip', async () => {
    const wrapper = await mountToolbar();
    expect(tipOf(menuItem(wrapper, 'Edit annotations'))).toBe(
      "Edit the annotations of the VM in this experiment's copy of the topology",
    );
  });

  it('spins while an action of the menu runs', async () => {
    const wrapper = await mountToolbar({ pending: 'kill' });
    expect(wrapper.find('.vnc-actions-trigger').classes()).toContain(
      'is-loading',
    );
  });
});

describe('the connection indicator', () => {
  const indicator = (wrapper) => wrapper.find('.vnc-status');

  it.each([
    [CONNECTED, 'Connected', 'is-ok'],
    [
      { state: 'waiting', message: 'The connection was lost', retrySeconds: 5 },
      'Reconnecting in 5 s',
      'is-wait',
    ],
    [
      { state: 'notRunning', message: 'VM is not running' },
      'VM is not running',
      'is-error',
    ],
    [{ state: 'loading' }, 'Connecting', 'is-busy'],
  ])('shows %o as "%s"', async (status, label, tone) => {
    const wrapper = await mountToolbar({ status });
    expect(textOf(indicator(wrapper))).toBe(label);
    expect(indicator(wrapper).find('.vnc-dot').classes()).toContain(tone);
  });

  it('tells the details in its tooltip', async () => {
    const wrapper = await mountToolbar();
    const tip = tooltipOf(wrapper, '.vnc-status');
    expect(textOf(tip.vm.$refs.content)).toBe(
      '1024×768 · scaled 118% · Tight · quality 6 · paste as keys',
    );
  });

  it('opens a popover with the details, and asks for fresh ones', async () => {
    const wrapper = await mountToolbar({
      vmRow: vm({ host: 'compute1', uptime: 93784 }),
    });
    await indicator(wrapper).trigger('click');
    expect(indicator(wrapper).attributes('aria-expanded')).toBe('true');
    expect(wrapper.emitted('details')).toHaveLength(1);
    const popover = wrapper.find('#vnc-panel-status');
    expect(popover.findAll('.vnc-panel-title').map(textOf)).toEqual([
      'Connected',
      'VM web',
    ]);
    expect(popover.findAll('.vnc-detail').map(textOf)).toEqual([
      'Resolution: 1024×768',
      'Scaling: Fit, scaled 118%',
      'Encoding: Tight',
      'Quality: 6',
      'Compression: 2',
      'Copy/paste: Paste as keys',
      'Host: compute1',
      'Uptime: 1 day, 02:03:04',
      'miniccc agent: Active',
      'IP addresses:',
    ]);
    expect(
      popover
        .findAll('.vnc-addresses li')
        .map((li) =>
          ['.vnc-address', '.vnc-address-network']
            .map((part) => textOf(li.find(part)))
            .join(' '),
        ),
    ).toEqual(['10.0.0.5 (dmz)', '10.1.0.5 (mgmt)']);
    expect(buttons(popover)).toEqual(['Copy 10.0.0.5', 'Copy 10.1.0.5']);
  });

  it('says when the VM has no IP address or miniccc agent', async () => {
    const wrapper = await mountToolbar({
      vmRow: vm({ ipv4: [], ccActive: false }),
    });
    await indicator(wrapper).trigger('click');
    const popover = wrapper.find('#vnc-panel-status');
    expect(popover.findAll('.vnc-detail').map(textOf).slice(-2)).toEqual([
      'miniccc agent: Not active',
      'IP addresses: None',
    ]);
    expect(popover.find('.vnc-addresses').exists()).toBe(false);
  });

  it('copies an IP address in the click and says so', async () => {
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
    try {
      const wrapper = await mountToolbar();
      await indicator(wrapper).trigger('click');
      await wrapper.find('[aria-label="Copy 10.1.0.5"]').trigger('click');
      expect(writeText).toHaveBeenCalledExactlyOnceWith('10.1.0.5');
      await flushPromises();
      expect(toasts()).toContainEqual({
        text: 'Copied 10.1.0.5',
        type: 'is-success',
      });
    } finally {
      delete navigator.clipboard;
    }
  });

  it('offers Reconnect and the reason while the console waits', async () => {
    const wrapper = await mountToolbar({
      status: {
        state: 'waiting',
        message: 'The connection was lost',
        retrySeconds: 5,
      },
    });
    await indicator(wrapper).trigger('click');
    const popover = wrapper.find('#vnc-panel-status');
    expect(textOf(popover)).toContain('The connection was lost');
    await popover
      .findAll('button')
      .find((b) => textOf(b) === 'Reconnect')
      .trigger('click');
    expect(wrapper.emitted('reconnect')).toHaveLength(1);
  });
});

describe('the layout picker', () => {
  const layoutButtons = (wrapper) =>
    wrapper.findAll('.vnc-layouts button').map((b) => ({
      label: b.attributes('aria-label'),
      pressed: b.attributes('aria-pressed'),
      disabled: b.attributes('aria-disabled') === 'true',
    }));

  it('marks the layout in use and greys out the layouts with too few panes', async () => {
    const wrapper = await mountToolbar({
      layout: 'main-2',
      paneCount: 3,
      split: true,
    });
    expect(layoutButtons(wrapper)).toEqual([
      { label: 'One pane', pressed: 'false', disabled: true },
      { label: 'Two columns', pressed: 'false', disabled: true },
      { label: 'Two rows', pressed: 'false', disabled: true },
      {
        label: 'One large pane and two stacked',
        pressed: 'true',
        disabled: false,
      },
      { label: 'Grid of four', pressed: 'false', disabled: false },
    ]);
    const single = tooltipButtons(wrapper).find((t) => t.label === 'One pane');
    expect(single.tooltip).toBe('Cannot use this layout: close a pane first');
  });

  it('asks for the layout a click picks, but not a greyed-out one', async () => {
    const wrapper = await mountToolbar({ layout: 'cols', paneCount: 2 });
    const buttons = wrapper.findAll('.vnc-layouts button');
    await buttons[0].trigger('click');
    await buttons[4].trigger('click');
    expect(wrapper.emitted('layout')).toEqual([['grid']]);
  });
});

describe('the dropdown panels', () => {
  it.each(['Keys', 'Clipboard', 'View'])(
    'has an icon-only %s button, with no arrow, and its name in a tooltip',
    async (label) => {
      const wrapper = await mountToolbar();
      expect(textOf(panelButton(wrapper, label))).toBe('');
      expect(panelButton(wrapper, label).findAll('[data-icon]')).toHaveLength(
        1,
      );
      const tip = tooltipOf(wrapper, `[aria-label="${label}"]`);
      expect(textOf(tip.vm.$refs.content)).toBe(label);
    },
  );

  it.each([
    ['Keys', '.keys-panel'],
    ['Clipboard', '.clipboard-panel'],
    ['View', '.view-panel'],
    ['Add VM', '.add-panel'],
  ])(
    'opens the %s panel under its button, one panel at a time',
    async (text, panel) => {
      const wrapper = await mountToolbar();
      const button = panelButton(wrapper, text);
      expect(button.attributes('aria-expanded')).toBe('false');
      expect(wrapper.find(panel).isVisible()).toBe(false);
      await button.trigger('click');
      expect(button.attributes('aria-expanded')).toBe('true');
      expect(wrapper.find(panel).isVisible()).toBe(true);
      const other = text === 'Keys' ? 'View' : 'Keys';
      await panelButton(wrapper, other).trigger('click');
      expect(wrapper.find(panel).isVisible()).toBe(false);
    },
  );

  it.each([
    ['Keys', 'keys'],
    ['View', 'view'],
    ['Add VM', 'add'],
    ['Port forwards', 'forwards'],
  ])('keeps the tooltip of %s off its open panel', async (label, slot) => {
    const wrapper = await mountToolbar(
      {},
      { [slot]: `<p class="${slot}-slot">panel</p>` },
    );
    const tip = tooltipOf(wrapper, `[aria-label="${label}"]`);
    expect(tip.props('active')).toBe(true);
    await panelButton(wrapper, label).trigger('click');
    expect(wrapper.find(`.${slot}-slot`).isVisible()).toBe(true);
    expect(tip.props('active')).toBe(false);
    await panelButton(wrapper, label).trigger('click');
    expect(tip.props('active')).toBe(true);
  });

  it('closes a panel with Esc and gives focus back to the console', async () => {
    const wrapper = await mountToolbar();
    await panelButton(wrapper, 'Keys').trigger('click');
    await wrapper.find('.keys-panel').trigger('keydown', { key: 'Escape' });
    expect(wrapper.find('.keys-panel').isVisible()).toBe(false);
    expect(wrapper.emitted('panel-close')).toHaveLength(1);
    expect(document.activeElement).toBe(panelButton(wrapper, 'Keys').element);
  });

  it('closes a panel on a click outside it, and keeps focus there', async () => {
    const wrapper = await mountToolbar();
    await panelButton(wrapper, 'View').trigger('click');
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();
    expect(wrapper.find('.view-panel').isVisible()).toBe(false);
    expect(wrapper.emitted('panel-close')).toBeUndefined();
  });

  it('greys out Add VM when the console shows four VMs', async () => {
    const wrapper = await mountToolbar({
      paneCount: 4,
      layout: 'grid',
      split: true,
    });
    const add = tooltipButtons(wrapper).find((t) => t.label === 'Add VM');
    expect(add.disabled).toBe(true);
    expect(add.tooltip).toBe('Cannot add a VM: the console shows four VMs');
    await add.control.trigger('click');
    expect(wrapper.find('.add-panel').isVisible()).toBe(false);
  });

  it('asks for the whole page full screen', async () => {
    const wrapper = await mountToolbar();
    const button = wrapper.find('.vnc-fullscreen-button');
    expect(button.attributes('aria-label')).toBe('Full screen');
    expect(button.find('[data-icon]').attributes('data-icon')).toBe('expand');
    await button.trigger('click');
    expect(wrapper.emitted('fullscreen')).toHaveLength(1);
    expect(wrapper.emitted('exit-fullscreen')).toBeUndefined();
  });

  it('asks to exit full screen while the page is full screen', async () => {
    const wrapper = await mountToolbar({ fullscreen: true });
    const button = wrapper.find('.vnc-fullscreen-button');
    expect(button.attributes('aria-label')).toBe('Exit full screen');
    expect(button.find('[data-icon]').attributes('data-icon')).toBe('compress');
    expect(
      textOf(tooltipOf(wrapper, '.vnc-fullscreen-button').vm.$refs.content),
    ).toBe('Exit full screen');
    await button.trigger('click');
    expect(wrapper.emitted('exit-fullscreen')).toHaveLength(1);
    expect(wrapper.emitted('fullscreen')).toBeUndefined();
  });

  it.each([
    ['Keys', true],
    ['Clipboard', true],
    ['View', false],
  ])('makes the %s panel as wide as its rows: %s', async (text, wide) => {
    const wrapper = await mountToolbar();
    const id = panelButton(wrapper, text).attributes('aria-controls');
    expect(wrapper.find(`#${id}`).classes('vnc-panel--wide')).toBe(wide);
  });
});

describe('Hide toolbar', () => {
  it('is the last control of the bar, with an accessible name, and asks to hide the bar', async () => {
    const wrapper = await mountToolbar();
    const hide = wrapper.findAll('button').at(-1);
    expect(hide.attributes('aria-label')).toBe('Hide toolbar');
    const tip = tooltipOf(wrapper, '.vnc-hide-toolbar');
    expect(textOf(tip.vm.$refs.content)).toBe(
      'Hide the toolbar (Ctrl+] then T)',
    );
    // at the right end of the bar, it opens to the left to stay in view
    expect(tip.props('position')).toBe('is-left');
    await hide.trigger('click');
    expect(wrapper.emitted('hide')).toEqual([[]]);
  });
});
