// A view-only live tile (M4, M5, T32): the pool makes it live, it draws a
// view-only console at quality 3 and compression 6, it shows its picture
// until the console draws and its last frame after a claim, a paused VM
// stays live, and a click opens the console page.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';

const { axios, modules, rbac, resetMocks } = await vi.hoisted(
  () => import('./helpers/mocks.js'),
);
const { FakeRfb, rfbs, resetRfbs } = await vi.hoisted(
  () => import('./helpers/fakeRfb.js'),
);
vi.mock('@/utils/axios.js', modules.axios);
vi.mock('@/store.js', modules.store);
vi.mock('@/utils/rbac.js', modules.rbac);
vi.mock('@/utils/websocket', modules.websocket);
vi.mock('@/vnc/loadRfb.js', () => ({ loadRfb: async () => FakeRfb }));

import VncTile from '@/vnc/VncTile.vue';
import { sharedLiveTilePool } from '@/vnc/liveTilePool.js';
import { vm } from './helpers/fixtures.js';
import { mountComponent, textOf } from './helpers/mount.js';
import { allowAll, denyOnly } from './helpers/permissions.js';

const PICTURE = 'data:image/png;base64,c2hvdA==';

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

const tile = async (row = vm(), props = {}) => {
  const wrapper = await mountComponent(VncTile, {
    props: {
      experiment: 'demo',
      vm: row,
      picture: PICTURE,
      alt: `Screenshot of VM ${row.name}`,
      linkLabel: `Open the console of VM ${row.name}`,
      ...props,
    },
    attachTo: document.body,
  });
  await flushPromises();
  return wrapper;
};

const tag = (wrapper) => {
  const el = wrapper.find('.vnc-tile-tag');
  return el.exists() ? textOf(el) : null;
};
const picture = (wrapper) => {
  const img = wrapper.find('img.vnc-tile-picture');
  return img.exists() && img.isVisible() ? img.attributes('src') : null;
};
const vncRequests = () =>
  axios.get.mock.calls.filter(([url]) => url.endsWith('/vnc'));

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

describe('a live tile', () => {
  it('draws a view-only console at quality 3 and compression 6', async () => {
    const wrapper = await tile();
    expect(vncRequests().map(([url]) => url)).toEqual([
      'experiments/demo/vms/web/vnc',
    ]);
    expect(rfbs).toHaveLength(1);
    const [rfb] = rfbs;
    expect(rfb.url).toBe(
      'ws://phenix.test/api/v1/experiments/demo/vms/web/vnc/ws?token=t',
    );
    expect(rfb.options).toEqual({ shared: true });
    expect(rfb.viewOnly).toBe(true);
    expect(rfb.scaleViewport).toBe(true);
    expect(rfb.qualityLevel).toBe(3);
    expect(rfb.compressionLevel).toBe(6);
    expect(wrapper.find('.vnc-console').attributes('tabindex')).toBe('-1');

    // the picture shows until the console draws
    expect(picture(wrapper)).toBe(PICTURE);
    expect(tag(wrapper)).toBeNull();
    rfb.connect();
    await flushPromises();
    expect(picture(wrapper)).toBeNull();
    expect(tag(wrapper)).toBe('LIVE');
    expect(wrapper.emitted('live')).toEqual([[true]]);
  });

  it('sends no key or pointer input, and a click opens the console page', async () => {
    const wrapper = await tile();
    rfbs[0].connect();
    await flushPromises();

    const link = wrapper.get('a.vnc-tile-link');
    expect(link.attributes('href')).toBe('/vnc?vm=demo/web');
    expect(textOf(link)).toBe('Open the console of VM web');
    await wrapper.get('.vnc-tile-screen').trigger('keydown', { key: 'a' });
    await link.trigger('click', { button: 0 });
    await flushPromises();

    expect(rfbs[0].viewOnly).toBe(true);
    expect(rfbs[0].keys).toEqual([]);
    expect(open).toHaveBeenCalledTimes(1);
    expect(open.mock.calls[0][0]).toMatch(/\/vnc\?vm=demo\/web$/);
    expect(open.mock.calls[0][1]).toMatch(/^phenix-vnc-[0-9a-f]{16}$/);
  });

  it('keeps a paused VM live and frozen, with a PAUSED tag', async () => {
    const wrapper = await tile(vm({ state: 'PAUSED', running: false }));
    expect(rfbs).toHaveLength(1);
    rfbs[0].connect();
    await flushPromises();
    expect(tag(wrapper)).toBe('PAUSED');
    expect(picture(wrapper)).toBeNull();
    expect(rfbs[0].disconnected).toBe(false);
  });

  it('shows its width', async () => {
    const wrapper = await tile(vm(), { width: 200 });
    expect(wrapper.get('.vnc-tile').attributes('style')).toContain(
      'width: 200px',
    );
  });
});

describe('the live event', () => {
  it('reports that a drawing tile stops when it unmounts', async () => {
    const onLive = vi.fn();
    const wrapper = await tile(vm(), { onLive });
    rfbs[0].connect();
    await flushPromises();
    expect(onLive.mock.calls).toEqual([[true]]);
    wrapper.unmount();
    expect(onLive.mock.calls).toEqual([[true], [false]]);
  });

  it('reports nothing when a tile that does not draw unmounts', async () => {
    const onLive = vi.fn();
    const wrapper = await tile(vm(), { onLive });
    wrapper.unmount();
    expect(onLive).not.toHaveBeenCalled();
  });
});

describe('one connection for each VM in a tab', () => {
  it('disconnects while the details modal or a console pane has the VM, and shows the last frame', async () => {
    const wrapper = await tile();
    rfbs[0].connect();
    await flushPromises();

    const release = sharedLiveTilePool().claim('demo/web');
    await flushPromises();
    expect(rfbs[0].disconnected).toBe(true);
    expect(picture(wrapper)).toMatch(/^blob:/);
    expect(tag(wrapper)).toBe('Snapshot');
    expect(wrapper.emitted('live')).toEqual([[true], [false]]);

    // a newer screenshot replaces the last frame
    await wrapper.setProps({ picture: 'data:image/png;base64,bmV3' });
    expect(picture(wrapper)).toBe('data:image/png;base64,bmV3');

    release();
    await flushPromises();
    expect(rfbs).toHaveLength(2);
    expect(rfbs[1].disconnected).toBe(false);
  });
});

describe('a tile that is not live', () => {
  it('shows the picture with "Snapshot" over the live tile limit', async () => {
    sharedLiveTilePool().setLimit(1);
    const web = await tile();
    const db = await tile(vm({ name: 'db' }));
    expect(rfbs.map((rfb) => rfb.url)).toEqual([
      'ws://phenix.test/api/v1/experiments/demo/vms/web/vnc/ws?token=t',
    ]);
    expect(tag(db)).toBe('Snapshot');
    expect(picture(db)).toBe(PICTURE);
    expect(picture(web)).toBe(PICTURE);
  });

  it('turns off in Snapshots mode', async () => {
    const wrapper = await tile();
    rfbs[0].connect();
    await flushPromises();
    sharedLiveTilePool().setLiveMode(false);
    await flushPromises();
    expect(rfbs[0].disconnected).toBe(true);
    expect(tag(wrapper)).toBe('Snapshot');
  });

  it.each([
    ['a VM that is not running', vm({ running: false, state: 'QUIT' })],
    ['a busy VM', vm({ busy: true })],
    ['a do-not-boot VM', vm({ running: false, state: '', dnb: true })],
  ])(
    'gives %s its picture, no connection and no console link',
    async (_, row) => {
      const wrapper = await tile(row);
      expect(vncRequests()).toEqual([]);
      expect(rfbs).toEqual([]);
      expect(picture(wrapper)).toBe(PICTURE);
      expect(tag(wrapper)).toBeNull();
      expect(wrapper.find('a.vnc-tile-link').exists()).toBe(false);
      expect(wrapper.get('img.vnc-tile-picture').attributes('alt')).toBe(
        'Screenshot of VM web',
      );
    },
  );

  it('links a paused VM to its console', async () => {
    const wrapper = await tile(vm({ state: 'PAUSED', running: false }));
    expect(wrapper.find('a.vnc-tile-link').exists()).toBe(true);
  });

  it.each([
    ['may', allowAll, true],
    ['may not', denyOnly(['vms/vnc', 'get']), false],
  ])(
    'connects and links to the console only when the role %s get vms/vnc',
    async (_, allowed, live) => {
      rbac.allowed = allowed;
      const wrapper = await tile();
      expect(rfbs).toHaveLength(live ? 1 : 0);
      expect(wrapper.find('a.vnc-tile-link').exists()).toBe(live);
      const img = wrapper.get('img.vnc-tile-picture');
      // the link names the picture; without it, the picture has its own text
      expect(img.attributes('alt')).toBe(live ? '' : 'Screenshot of VM web');
    },
  );
});
