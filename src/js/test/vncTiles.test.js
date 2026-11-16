// The running experiment's VNC tab (M4a, T32): a live tile per VM with a
// console, its hover bar, the Live / Snapshots switch, the "Live tile limit"
// option and live count, the zoom slider, the selection checkboxes, "Open
// selected in split", and the screenshots that the server sends only for tiles that are not live.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';

const { axios, modules, rbac, resetMocks, ws } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
const { FakeRfb, rfbs, resetRfbs } = await vi.hoisted(
  () => import('./helpers/fakeRfb.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/vnc/loadRfb.js', () => ({ loadRfb: async () => FakeRfb }));

import VncTiles from '@/components/experiment/VncTiles.vue';
import VncTileToolbar from '@/vnc/VncTileToolbar.vue';
import { sharedLiveTilePool } from '@/vnc/liveTilePool.js';
import { readPref, writePref } from '@/utils/prefs.js';
import { vm } from './helpers/fixtures.js';
import { denyAll, denyOnly } from './helpers/permissions.js';
import { buttons, mountComponent, textOf } from './helpers/mount.js';

const session = (exp, name) => ({
  experiment: exp,
  vm: name,
  websocket: `experiments/${exp}/vms/${name}/vnc/ws`,
  banners: {
    top: { lines: [], backgroundColor: 'white', textColor: 'black' },
    bottom: { lines: [], backgroundColor: 'white', textColor: 'black' },
  },
  display: { vga: 'std', remoteResize: false },
  clipboard: { mode: 'pasteAsKeys' },
});

let open;

beforeEach(() => {
  resetMocks();
  resetRfbs();
  // no observer: the pool counts each tile as on screen
  vi.stubGlobal('IntersectionObserver', undefined);
  vi.stubGlobal('BroadcastChannel', undefined);
  open = vi.spyOn(window, 'open').mockReturnValue({});
  axios.get.mockImplementation(async (url) => {
    const [, exp, name] = /^experiments\/([^/]+)\/vms\/([^/]+)\/vnc$/.exec(url);
    return { data: session(exp, name) };
  });
});

afterEach(() => {
  sharedLiveTilePool().dispose();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const render = async (props = {}) => {
  const wrapper = await mountComponent(VncTiles, {
    route: '/experiment/demo',
    props: { experiment: 'demo', zoom: 4, width: 200, ...props },
    attachTo: document.body,
  });
  await flushPromises();
  return wrapper;
};

const tileNames = (wrapper) => wrapper.findAll('.vnc-tile-name').map(textOf);
// the hover bar of the tile of VM `name`
const bar = (wrapper, name) =>
  wrapper
    .findAll('.vnc-tile-item')
    .find((item) => textOf(item.find('.vnc-tile-name')) === name)
    .get('.vnc-tile-bar');
const liveMessages = () =>
  ws.sent
    .filter(
      (m) =>
        m.resource.type === 'metadata/screenshot' &&
        m.resource.action === 'live',
    )
    .map((m) => m.request.vms);

describe('the tiles', () => {
  it('shows a live tile for each VM with a console, each opening it', async () => {
    const wrapper = await render({
      vms: [
        vm(),
        vm({ name: 'hil', external: true }),
        vm({ name: 'off', dnb: true }),
      ],
    });
    expect(tileNames(wrapper)).toEqual(['web']);
    expect(wrapper.get('.vnc-tile').attributes('style')).toContain(
      'width: 200px',
    );
    expect(rfbs).toHaveLength(1);
    expect(rfbs[0].viewOnly).toBe(true);
    const img = wrapper.get('img.vnc-tile-picture');
    expect(textOf(wrapper.get('a.vnc-tile-link'))).toBe(
      'Open the console of VM web: Loading the screenshot of VM web',
    );
    expect(img.attributes('alt')).toBe('');

    await wrapper.get('a.vnc-tile-link').trigger('click', { button: 0 });
    await flushPromises();
    expect(open.mock.calls[0][0]).toMatch(/\/vnc\?vm=demo\/web$/);
  });

  it.each([
    ['loading until the experiment has', '', 'Loading VMs…'],
    ['empty when it has none', 'demo', 'No VMs with a VNC console'],
  ])('says it is %s', async (_, experiment, text) => {
    expect(textOf(await render({ experiment, vms: [] }))).toContain(text);
  });

  it("asks for a VM's details by its name, if the role may get it", async () => {
    const wrapper = await render({ vms: [vm()] });
    const name = wrapper.get('button.vnc-tile-name');
    expect(name.attributes('aria-haspopup')).toBe('dialog');
    await name.trigger('click');
    expect(wrapper.emitted('info')).toEqual([[vm()]]);

    rbac.allowed = denyOnly(['vms', 'get']);
    const denied = await render({ vms: [vm()] });
    expect(denied.find('button.vnc-tile-name').exists()).toBe(false);
    expect(tileNames(denied)).toEqual(['web']);
  });
});

describe('the hover bar', () => {
  it('opens the console, the details, and the power actions the role may run', async () => {
    const wrapper = await render({ vms: [vm()] });
    const tileBar = bar(wrapper, 'web');
    const link = tileBar.get('a');
    expect(link.attributes('href')).toBe('/vnc?vm=demo/web');
    expect(link.attributes('aria-label')).toBe('Open the console of VM web');
    expect(buttons(tileBar)).toEqual([
      'Details of VM web',
      'Pause VM web',
      'Restart VM web',
      'Shut down VM web',
      'Kill VM web',
      'Screenshot VM web',
      'Snapshot VM web',
    ]);

    await tileBar
      .get('button[aria-label="Details of VM web"]')
      .trigger('click');
    await tileBar.get('button[aria-label="Pause VM web"]').trigger('click');
    expect(wrapper.emitted('info')).toEqual([[vm()]]);
    expect(wrapper.emitted('action')).toEqual([['pause', vm()]]);

    await link.trigger('click', { button: 0 });
    await flushPromises();
    expect(open).toHaveBeenCalledTimes(1);
  });

  it('offers Resume for a paused VM', async () => {
    const paused = vm({ state: 'PAUSED', running: false });
    const wrapper = await render({ vms: [paused] });
    expect(buttons(bar(wrapper, 'web'))).toEqual([
      'Details of VM web',
      'Resume VM web',
      'Screenshot VM web: Cannot save a screenshot: the VM is not running (disabled)',
      'Snapshot VM web: Cannot snapshot: the VM is not running (disabled)',
    ]);
  });

  it('offers Screenshot and Snapshot only in Live mode', async () => {
    const wrapper = await render({ vms: [vm()] });
    const tools = () =>
      bar(wrapper, 'web').findAll('.vnc-tile-screenshot, .vnc-tile-snapshot');
    expect(tools()).toHaveLength(2);

    sharedLiveTilePool().setLiveMode(false);
    await flushPromises();
    expect(tools()).toHaveLength(0);
    expect(buttons(bar(wrapper, 'web'))).toContain('Shut down VM web');

    sharedLiveTilePool().setLiveMode(true);
    await flushPromises();
    expect(tools()).toHaveLength(2);
  });

  it('saves a screenshot or a snapshot of the VM', async () => {
    const wrapper = await render({ vms: [vm()] });
    const tileBar = bar(wrapper, 'web');
    const screenshot = tileBar.get('.vnc-tile-screenshot');
    expect(screenshot.attributes('title')).toBe(
      "Save a full-size PNG of the VM's screen in the experiment files, in the screenshots folder",
    );
    await screenshot.trigger('click');
    await tileBar.get('.vnc-tile-snapshot').trigger('click');
    expect(wrapper.emitted('action')).toEqual([
      ['screenshot', vm()],
      ['snapshot', vm()],
    ]);
  });

  it('greys out Snapshot with the reason when the VM has no snapshots', async () => {
    const row = vm({ snapshot: false });
    const wrapper = await render({ vms: [row] });
    const snapshot = bar(wrapper, 'web').get('.vnc-tile-snapshot');
    const reason = 'Cannot snapshot: the VM does not have snapshots enabled';
    expect(snapshot.attributes('title')).toBe(reason);
    expect(snapshot.attributes('aria-label')).toBe(
      `Snapshot VM web: ${reason}`,
    );
    expect(snapshot.attributes('aria-disabled')).toBe('true');
    await snapshot.trigger('click');
    expect(wrapper.emitted('action')).toBeUndefined();
  });

  it('spins a tool while it runs, and does not run it again', async () => {
    const wrapper = await render({
      vms: [vm()],
      actionPending: { web: ['screenshot'] },
    });
    const screenshot = bar(wrapper, 'web').get('.vnc-tile-screenshot');
    expect(screenshot.classes()).toContain('is-loading');
    expect(screenshot.attributes('aria-busy')).toBe('true');
    await screenshot.trigger('click');
    expect(wrapper.emitted('action')).toBeUndefined();
  });

  it.each([
    [
      'vms/screenshot',
      'create',
      [
        'Screenshot VM web: Your role does not permit this action (vms/screenshot create)',
        'Snapshot VM web',
      ],
    ],
    [
      'vms/snapshots',
      'create',
      [
        'Screenshot VM web',
        'Snapshot VM web: Your role does not permit this action (vms/snapshots create)',
      ],
    ],
  ])(
    'greys out a tool without %s %s, naming the permission',
    async (resource, verb, shown) => {
      rbac.allowed = denyOnly([resource, verb]);
      const wrapper = await render({ vms: [vm()] });
      const tools = bar(wrapper, 'web').findAll(
        '.vnc-tile-screenshot, .vnc-tile-snapshot',
      );
      expect(tools.map((b) => b.attributes('aria-label'))).toEqual(shown);
      const denied = tools.find((b) => b.attributes('aria-disabled'));
      await denied.trigger('click');
      expect(wrapper.emitted('action')).toBeUndefined();
    },
  );

  it.each([
    ['running', vm(), true],
    ['paused', vm({ state: 'PAUSED', running: false }), true],
    ['shut down', vm({ state: 'QUIT', running: false }), false],
    ['busy', vm({ busy: true }), false],
  ])(
    'links a %s VM to its console only when it has a screen',
    async (_, row, linked) => {
      const wrapper = await render({ vms: [row] });
      expect(bar(wrapper, 'web').find('a').exists()).toBe(linked);
      expect(wrapper.find('a.vnc-tile-link').exists()).toBe(linked);
    },
  );

  it('greys out the actions a role may not take, and links nothing', async () => {
    rbac.allowed = denyAll;
    const wrapper = await render({ vms: [vm()] });
    const denied = (label, resource, verb) =>
      `${label} VM web: Your role does not permit this action (${resource} ${verb}) (disabled)`;
    expect(buttons(bar(wrapper, 'web'))).toEqual([
      denied('Pause', 'vms/stop', 'update'),
      denied('Restart', 'vms/restart', 'update'),
      denied('Shut down', 'vms/shutdown', 'update'),
      denied('Kill', 'vms', 'delete'),
      denied('Screenshot', 'vms/screenshot', 'create'),
      denied('Snapshot', 'vms/snapshots', 'create'),
    ]);
    expect(bar(wrapper, 'web').find('a').exists()).toBe(false);
    await bar(wrapper, 'web').find('button').trigger('click');
    expect(wrapper.emitted('action')).toBeUndefined();
  });
});

describe('the tile toolbar', () => {
  const limitSelect = (wrapper) => wrapper.get('select');

  it('offers the live tile limits, 16 by default, and keeps the choice', async () => {
    const wrapper = await render({ vms: [vm()] });
    const select = limitSelect(wrapper);
    expect(
      select.findAll('option').map((o) => o.attributes('value') ?? o.text()),
    ).toEqual(['4', '8', '16', '24', '32']);
    expect(select.element.value).toBe('16');

    await select.setValue('4');
    expect(readPref('phenix.vnc.liveTileLimit')).toBe('4');
    expect(sharedLiveTilePool().limit.value).toBe(4);
  });

  it('starts at the limit the browser keeps', async () => {
    writePref('phenix.vnc.liveTileLimit', '24');
    const wrapper = await mountComponent(VncTileToolbar);
    expect(limitSelect(wrapper).element.value).toBe('24');
    expect(sharedLiveTilePool().limit.value).toBe(24);
  });

  it('switches between Live and Snapshots', async () => {
    const wrapper = await render({ vms: [vm()] });
    const [live, snapshots] = wrapper.findAll('.vnc-tile-mode button');
    expect(textOf(live)).toContain('Live');
    expect(live.attributes('aria-pressed')).toBe('true');
    expect(snapshots.attributes('aria-pressed')).toBe('false');

    rfbs[0].connect();
    await snapshots.trigger('click');
    await flushPromises();
    expect(sharedLiveTilePool().liveMode.value).toBe(false);
    expect(snapshots.attributes('aria-pressed')).toBe('true');
    expect(rfbs[0].disconnected).toBe(true);

    await live.trigger('click');
    await flushPromises();
    expect(rfbs).toHaveLength(2);
  });

  it('counts the live tiles and the tiles off screen', async () => {
    sharedLiveTilePool().setLimit(1);
    const wrapper = await render({ vms: [vm(), vm({ name: 'db' })] });
    expect(textOf(wrapper.get('.vnc-tile-stats'))).toBe('1 of 2 live');

    sharedLiveTilePool().dispose();
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const hidden = await render({ vms: [vm(), vm({ name: 'db' })] });
    expect(textOf(hidden.get('.vnc-tile-stats'))).toBe(
      '0 of 2 live · 2 off screen',
    );
  });

  it('passes on the zoom picked', async () => {
    const wrapper = await render({ vms: [] });
    wrapper.findComponent({ name: 'BSlider' }).vm.$emit('update:modelValue', 8);
    expect(wrapper.emitted('update:zoom')).toEqual([[8]]);
  });
});

describe('Open selected in split', () => {
  const split = (wrapper) => wrapper.get('.vnc-open-split');

  it('opens the selected VMs in one console page', async () => {
    const wrapper = await render({
      vms: [vm(), vm({ name: 'db' }), vm({ name: 'kali' })],
      selected: ['db', 'web'],
    });
    const link = split(wrapper);
    expect(link.element.tagName).toBe('A');
    expect(link.attributes('href')).toBe('/vnc?vm=demo/web&vm=demo/db');
    await link.trigger('click', { button: 0 });
    await flushPromises();
    expect(open.mock.calls[0][0]).toMatch(/\/vnc\?vm=demo\/web&vm=demo\/db$/);
  });

  it.each([
    ['no VM is selected', [], 'Select one to four running VMs first'],
    [
      'more than four are',
      ['a', 'b', 'c', 'd', 'e'],
      'Select at most four VMs: one console page shows four',
    ],
  ])('is disabled, saying why, when %s', async (_, selected, reason) => {
    const vms = ['a', 'b', 'c', 'd', 'e'].map((name) => vm({ name }));
    const wrapper = await render({ vms, selected });
    const button = split(wrapper);
    expect(button.element.tagName).toBe('BUTTON');
    expect(button.attributes('aria-disabled')).toBe('true');
    expect(button.attributes('aria-label')).toContain(reason);
  });

  it.each([
    [[], 'Open selected in split'],
    [['web'], 'Open 1 selected in split'],
    [['web', 'db', 'kali'], 'Open 3 selected in split'],
    [['a', 'b', 'c', 'd', 'e'], 'Open 5 selected in split'],
  ])('counts the selected VMs with a console: %j', async (selected, label) => {
    const vms = ['web', 'db', 'kali', 'a', 'b', 'c', 'd', 'e'].map((name) =>
      vm({ name }),
    );
    const wrapper = await render({ vms, selected });
    expect(textOf(wrapper.get('.vnc-open-split'))).toBe(label);
  });

  it('leaves out the selected VMs without a console', async () => {
    rbac.allowed = denyOnly(['vms/vnc', 'get', 'demo/db']);
    const wrapper = await render({
      vms: [vm(), vm({ name: 'db' }), vm({ name: 'off', running: false })],
      selected: ['web', 'db', 'off'],
    });
    expect(split(wrapper).attributes('href')).toBe('/vnc?vm=demo/web');
  });
});

describe('selecting tiles', () => {
  const vms = [vm(), vm({ name: 'db' }), vm({ name: 'kali' })];
  // the checkbox whose label is "Select VM <name>"
  const box = (wrapper, name) =>
    wrapper
      .findAll('label.vnc-tile-select')
      .find((label) => textOf(label) === `Select VM ${name}`)
      ?.get('input');
  const lastSelected = (wrapper) => wrapper.emitted('update:selected').at(-1);
  const toolbarButton = (wrapper, text) =>
    wrapper.findAll('button').find((button) => textOf(button) === text);

  it('gives each tile a checkbox that the keyboard reaches', async () => {
    const wrapper = await render({ vms, selected: ['db'] });
    for (const name of ['web', 'db', 'kali']) {
      const input = box(wrapper, name);
      expect(input.attributes('type')).toBe('checkbox');
      expect(input.attributes('tabindex')).not.toBe('-1');
      expect(input.element.closest('a')).toBeNull();
    }
    expect(box(wrapper, 'web').element.checked).toBe(false);
    expect(box(wrapper, 'db').element.checked).toBe(true);
  });

  it('adds a VM to the shared selection, and the split opens it', async () => {
    const wrapper = await render({ vms, selected: ['db'] });
    await box(wrapper, 'web').setValue(true);
    expect(lastSelected(wrapper)).toEqual([['db', 'web']]);
    expect(open).not.toHaveBeenCalled();

    await wrapper.setProps({ selected: ['db', 'web'] });
    expect(wrapper.get('.vnc-open-split').attributes('href')).toBe(
      '/vnc?vm=demo/web&vm=demo/db',
    );
    await box(wrapper, 'db').setValue(false);
    expect(lastSelected(wrapper)).toEqual([['web']]);
  });

  it('shows the selection made in the VM table', async () => {
    const wrapper = await render({ vms });
    await wrapper.setProps({ selected: ['kali'] });
    expect(box(wrapper, 'kali').element.checked).toBe(true);
    expect(
      wrapper
        .findAll('.vnc-tile-item.is-selected')
        .map((item) => textOf(item.get('.vnc-tile-name'))),
    ).toEqual(['kali']);
  });

  it('gives a VM without a console a checkbox, as the table does', async () => {
    const wrapper = await render({
      vms: [vm(), vm({ name: 'off', running: false })],
      selected: ['off'],
    });
    expect(box(wrapper, 'off').element.checked).toBe(true);
    expect(wrapper.get('.vnc-open-split').attributes('aria-label')).toBe(
      'Open selected in split: Select one to four running VMs first',
    );
  });

  it('gives a busy VM no checkbox, as the table does', async () => {
    const wrapper = await render({
      vms: [vm(), vm({ name: 'db', busy: true })],
    });
    expect(box(wrapper, 'db')).toBeUndefined();
    await toolbarButton(wrapper, 'Select all').trigger('click');
    expect(lastSelected(wrapper)).toEqual([['web']]);
  });

  it('selects all shown tiles, and clears them, keeping the others', async () => {
    const wrapper = await render({ vms, selected: ['hidden', 'db'] });
    const all = toolbarButton(wrapper, 'Select all');
    const clear = toolbarButton(wrapper, 'Clear selection');
    expect(buttons(wrapper)).toEqual(
      expect.arrayContaining(['Select all', 'Clear selection']),
    );

    await all.trigger('click');
    expect(lastSelected(wrapper)).toEqual([['hidden', 'web', 'db', 'kali']]);
    await wrapper.setProps({ selected: lastSelected(wrapper)[0] });
    expect(buttons(wrapper)).toContain('Select all (disabled)');

    await clear.trigger('click');
    expect(lastSelected(wrapper)).toEqual([['hidden']]);
    await wrapper.setProps({ selected: ['hidden'] });
    expect(buttons(wrapper)).toEqual(
      expect.arrayContaining(['Select all', 'Clear selection (disabled)']),
    );
  });
});

describe('the screenshots of tiles that are live', () => {
  it('tells the server which tiles draw a live screen', async () => {
    const wrapper = await render({ vms: [vm(), vm({ name: 'db' })] });
    expect(liveMessages()).toEqual([]);

    rfbs[0].connect();
    rfbs[1].connect();
    await flushPromises();
    expect(liveMessages().at(-1)).toEqual(['demo/db', 'demo/web']);

    // a claim takes the tile off; the server sends its screenshots again
    const release = sharedLiveTilePool().claim('demo/web');
    await flushPromises();
    expect(liveMessages().at(-1)).toEqual(['demo/db']);
    release();

    // the server forgets the list when the websocket reconnects
    const sent = liveMessages().length;
    for (const f of ws.reconnect) f();
    expect(liveMessages()).toHaveLength(sent + 1);

    wrapper.unmount();
    expect(liveMessages().at(-1)).toEqual([]);
    expect(ws.reconnect).toEqual([]);
  });

  it('takes a tile that goes away off the list', async () => {
    const wrapper = await render({ vms: [vm(), vm({ name: 'db' })] });
    rfbs[0].connect();
    rfbs[1].connect();
    await flushPromises();
    expect(liveMessages().at(-1)).toEqual(['demo/db', 'demo/web']);

    await wrapper.setProps({ vms: [vm({ name: 'db' })] });
    await flushPromises();
    expect(liveMessages().at(-1)).toEqual(['demo/db']);
  });
});
