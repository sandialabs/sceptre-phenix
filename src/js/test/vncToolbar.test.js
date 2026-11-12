// The top bar of the console page (T37, M2a and M6a): the VM it acts on, the
// VM actions the role may take, the connection indicator, the layout picker,
// and the dropdown panels.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';

const { modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
vi.mock('@/utils/rbac.js', modules.rbac);

import VncToolbar from '@/vnc/VncToolbar.vue';
import { CHORD_HINT } from '@/vnc/chord.js';
import { vm } from './helpers/fixtures.js';
import { mountComponent, textOf, tooltipButtons } from './helpers/mount.js';
import { allowOnly, denyAll, denyOnly } from './helpers/permissions.js';

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

// the VM action buttons: their accessible names
const actionNames = (wrapper) =>
  wrapper
    .findAll('.vnc-vm-actions button')
    .map((b) => b.attributes('aria-label'));

const actionButton = (wrapper, label) =>
  wrapper
    .findAll('.vnc-vm-actions button')
    .find((b) => b.attributes('aria-label') === label);

// the Buefy tooltip around the element that `selector` finds
const tooltipOf = (wrapper, selector) =>
  wrapper
    .findAllComponents({ name: 'BTooltip' })
    .find((tooltip) => tooltip.find(selector).exists());

const panelButton = (wrapper, text) =>
  wrapper.findAll('.vnc-panel-button').find((b) => textOf(b) === text);

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
  it('offers the power actions and the VM tools for a running VM', async () => {
    const wrapper = await mountToolbar();
    expect(actionNames(wrapper)).toEqual([
      'Pause',
      'Restart',
      'Shut down',
      'Snapshot',
      'Memory snapshot',
      'Screenshot',
      'Insert CD-ROM',
      'Port forward',
    ]);
  });

  it('gives the disk snapshot the clone icon and the screenshot the camera icon', async () => {
    const wrapper = await mountToolbar();
    const icon = (label) =>
      actionButton(wrapper, label).find('[data-icon]').attributes('data-icon');
    expect(icon('Snapshot')).toBe('clone');
    expect(icon('Screenshot')).toBe('camera');
    expect(icon('Insert CD-ROM')).toBe('compact-disc');
    expect(icon('Port forward')).toBe('arrow-right');
    expect(icon('Memory snapshot')).toBe('database');
  });

  it.each([
    ['Pause', 'pause', ['vms/stop', 'update', 'demo/web'], vm()],
    [
      'Resume',
      'start',
      ['vms/start', 'update', 'demo/web'],
      vm({ state: 'PAUSED', running: false }),
    ],
    [
      'Start',
      'start',
      ['vms/start', 'update', 'demo/web'],
      vm({ state: 'QUIT', running: false }),
    ],
    ['Restart', 'restart', ['vms/restart', 'update', 'demo/web'], vm()],
    ['Shut down', 'shutdown', ['vms/shutdown', 'update', 'demo/web'], vm()],
    ['Snapshot', 'snapshot', ['vms/snapshots', 'create', 'demo/web'], vm()],
    [
      'Memory snapshot',
      'memorySnapshot',
      ['vms/memorySnapshot', 'create', 'demo/web'],
      vm(),
    ],
    [
      'Screenshot',
      'screenshot',
      ['vms/screenshot', 'create', 'demo/web'],
      vm(),
    ],
    [
      'Port forward',
      'portForward',
      ['vms/forwards', 'create', 'demo/web'],
      vm(),
    ],
  ])('offers %s only to a role with %s', async (label, name, perm, row) => {
    rbac.allowed = allowOnly(perm);
    let wrapper = await mountToolbar({ vmRow: row });
    expect(actionNames(wrapper)).toEqual([label]);
    await actionButton(wrapper, label).trigger('click');
    expect(wrapper.emitted('action')).toEqual([[name]]);
    wrapper.unmount();

    rbac.allowed = denyOnly(perm);
    wrapper = await mountToolbar({ vmRow: row });
    expect(actionNames(wrapper)).not.toContain(label);
  });

  it('checks the permissions for the VM of the pane', async () => {
    rbac.allowed = allowOnly(['vms/restart', 'update', 'other/web']);
    const wrapper = await mountToolbar();
    expect(actionNames(wrapper)).toEqual([]);
  });

  it('keeps the input and view controls for a role with no power permissions', async () => {
    rbac.allowed = denyAll;
    const wrapper = await mountToolbar();
    expect(actionNames(wrapper)).toEqual([]);
    expect(wrapper.findAll('.vnc-panel-button').map(textOf)).toEqual([
      'Keys',
      'Clipboard',
      'View',
      'Add VM',
    ]);
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

  it('greys out every action of a busy VM', async () => {
    const wrapper = await mountToolbar({ vmRow: vm({ busy: true }) });
    const states = tooltipButtons(wrapper).filter((t) =>
      actionNames(wrapper).includes(t.label),
    );
    expect(states).toHaveLength(8);
    expect(states.every((t) => t.disabled)).toBe(true);
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
    const wrapper = await mountToolbar();
    await indicator(wrapper).trigger('click');
    expect(indicator(wrapper).attributes('aria-expanded')).toBe('true');
    expect(wrapper.emitted('details')).toHaveLength(1);
    const popover = wrapper.find('#vnc-panel-status');
    expect(textOf(popover.find('.vnc-panel-title'))).toBe('Connected');
    expect(popover.findAll('.vnc-detail').map(textOf)).toEqual([
      'Resolution: 1024×768',
      'Scaling: Fit, scaled 118%',
      'Encoding: Tight',
      'Quality: 6',
      'Compression: 2',
      'Copy/paste: Paste as keys',
    ]);
    expect(popover.find('button').exists()).toBe(false);
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
    await popover.find('button').trigger('click');
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
    const buttons = wrapper.findAll('button');
    const hide = buttons.at(-1);
    expect(hide.attributes('aria-label')).toBe('Hide toolbar');
    expect(
      textOf(tooltipOf(wrapper, '.vnc-hide-toolbar').vm.$refs.content),
    ).toBe('Hide the toolbar. Ctrl+] then T shows it again.');
    await hide.trigger('click');
    expect(wrapper.emitted('hide')).toEqual([[]]);
  });
});
