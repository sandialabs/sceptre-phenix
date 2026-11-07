// The running experiment's VNC tab (M4a, T32): a live tile per VM with a
// console, its hover bar, the Live / Snapshots switch, the "Live tile limit"
// option and live count, the zoom slider, "Open selected in split", and the
// screenshots that the server sends only for tiles that are not live.
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
import { componentDeclarations, contrast } from './helpers/css.js';
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
    ]);
  });

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

  it('offers nothing a role may not do', async () => {
    rbac.allowed = denyAll;
    const wrapper = await render({ vms: [vm()] });
    expect(buttons(bar(wrapper, 'web'))).toEqual([]);
    expect(bar(wrapper, 'web').find('a').exists()).toBe(false);
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
    [
      'no VM is selected',
      [],
      'Select one to four running VMs in the VMs tab first',
    ],
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

  it('leaves out the selected VMs without a console', async () => {
    rbac.allowed = denyOnly(['vms/vnc', 'get', 'demo/db']);
    const wrapper = await render({
      vms: [vm(), vm({ name: 'db' }), vm({ name: 'off', running: false })],
      selected: ['web', 'db', 'off'],
    });
    expect(split(wrapper).attributes('href')).toBe('/vnc?vm=demo/web');
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

it('writes the VM names at 7:1 or more on their bar', () => {
  const style = componentDeclarations(
    'src/components/experiment/VncTiles.vue',
    '.vnc-tile-name',
  );
  expect(style['background-color']).toBe('#4d4d4d');
  expect(contrast('#f5f5f5', style['background-color'])).toBeGreaterThanOrEqual(
    7,
  );
});
